// Command map-regions builds internal/maps/regions.json: the map areas
// Arcana offers (world, continents, countries, and states/provinces of large
// countries) with their bounding boxes and the exact size of each detail
// level, measured against a Protomaps planet build.
//
// Boundaries come from Natural Earth (public domain). Sizes are measured by
// planning each extraction against the planet's directories, which are
// downloaded once (about 350 MB) and cached locally; no tile data is
// downloaded.
//
//	go run ./tools/map-regions -admin0 ne_50m_admin_0_countries.geojson \
//	    -admin1 ne_50m_admin_1_states_provinces.geojson \
//	    -planet https://build.protomaps.com/20261121.pmtiles -cache /tmp/planet-dirs \
//	    -out internal/maps/regions.json
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/maps"
)

type level struct {
	MaxZoom uint8  `json:"maxZoom"`
	Tiles   int    `json:"tiles"`
	Bytes   uint64 `json:"bytes"`
}

type region struct {
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	Kind   string    `json:"kind"` // world, continent, country, subdivision
	ISO    string    `json:"iso,omitempty"`
	Parent string    `json:"parent,omitempty"`
	BBox   maps.BBox `json:"bbox"`
	Levels []level   `json:"levels"`
}

type output struct {
	Format     string   `json:"format"`
	Build      string   `json:"build"`
	Source     string   `json:"source"`
	Measured   string   `json:"measured"`
	Boundaries string   `json:"boundaries"`
	Regions    []region `json:"regions"`
}

// localReader serves the planet's header, root and leaf directories from a
// local cache, and fetches anything else (metadata) remotely.
type localReader struct {
	remote *maps.HTTPRangeReader
	head   []byte
	leafOf uint64
	leaves *os.File
}

func (l *localReader) ReadRange(ctx context.Context, off, n uint64) ([]byte, error) {
	if off+n <= uint64(len(l.head)) {
		return l.head[off : off+n], nil
	}
	if off >= l.leafOf {
		b := make([]byte, n)
		if _, err := l.leaves.ReadAt(b, int64(off-l.leafOf)); err != nil {
			return nil, err
		}
		return b, nil
	}
	return l.remote.ReadRange(ctx, off, n)
}

type feature struct {
	Properties map[string]any `json:"properties"`
	Geometry   struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	} `json:"geometry"`
}

func polygons(f feature) [][][][2]float64 {
	switch f.Geometry.Type {
	case "Polygon":
		var p [][][2]float64
		json.Unmarshal(f.Geometry.Coordinates, &p)
		return [][][][2]float64{p}
	case "MultiPolygon":
		var mp [][][][2]float64
		json.Unmarshal(f.Geometry.Coordinates, &mp)
		return mp
	}
	return nil
}

// bboxOf returns the smallest box around all polygon parts, allowing it to
// wrap across the antimeridian when that is narrower (e.g. Fiji, Russia).
func bboxOf(polys [][][][2]float64) maps.BBox {
	w, s, e, n := 180.0, 90.0, -180.0, -90.0
	var lons []float64
	for _, p := range polys {
		for _, pt := range p[0] {
			w, e = math.Min(w, pt[0]), math.Max(e, pt[0])
			s, n = math.Min(s, pt[1]), math.Max(n, pt[1])
			lons = append(lons, pt[0])
		}
	}
	// Try the wrapped alternative: shift negative longitudes by 360.
	w2, e2 := 360.0, -360.0
	for _, lon := range lons {
		if lon < 0 {
			lon += 360
		}
		w2, e2 = math.Min(w2, lon), math.Max(e2, lon)
	}
	if e2-w2 < e-w-1 && e2 > 180 {
		w, e = w2, e2-360
	}
	pad := 0.01
	s, n = math.Max(-85.05, s-pad), math.Min(85.05, n+pad)
	return maps.BBox{round(clampLon(w - pad)), round(s), round(clampLon(e + pad)), round(n)}
}

func clampLon(v float64) float64 { return math.Max(-180, math.Min(180, v)) }
func round(v float64) float64    { return math.Round(v*1000) / 1000 }

var slugRe = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	return strings.Trim(slugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

func str(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func load(path string) []feature {
	data, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	var fc struct {
		Features []feature `json:"features"`
	}
	if err := json.Unmarshal(data, &fc); err != nil {
		log.Fatal(err)
	}
	return fc.Features
}

func main() {
	admin0 := flag.String("admin0", "", "Natural Earth admin-0 GeoJSON")
	admin1 := flag.String("admin1", "", "Natural Earth admin-1 GeoJSON")
	planet := flag.String("planet", "", "planet PMTiles URL")
	cache := flag.String("cache", "planet-dirs", "directory for the cached planet directories")
	out := flag.String("out", "internal/maps/regions.json", "output file")
	flag.Parse()
	ctx := context.Background()

	var regions []region
	regions = append(regions, region{ID: "world", Name: "World", Kind: "world", BBox: maps.BBox{-180, -85.05, 180, 85.05}})
	continents := map[string][][][][2]float64{}
	countryID := map[string]string{}
	// Several Natural Earth units can share an ISO code (Australia and its
	// external territories); the largest one gets the ISO-based ID.
	admin0Features := load(*admin0)
	largest := map[string]float64{}
	for _, f := range admin0Features {
		iso := str(f.Properties, "ISO_A2_EH")
		if b := bboxOf(polygons(f)); (b[2]-b[0])*(b[3]-b[1]) > largest[iso] {
			largest[iso] = (b[2] - b[0]) * (b[3] - b[1])
		}
	}
	for _, f := range admin0Features {
		p := f.Properties
		name := str(p, "NAME")
		iso := str(p, "ISO_A2_EH")
		if iso == "-99" || iso == "" {
			iso = ""
		}
		polys := polygons(f)
		if len(polys) == 0 || str(p, "CONTINENT") == "Antarctica" {
			continue
		}
		id := "country-" + slug(name)
		if b := bboxOf(polys); iso != "" && (b[2]-b[0])*(b[3]-b[1]) >= largest[iso] {
			id = "country-" + strings.ToLower(iso)
		}
		countryID[str(p, "ADM0_A3")] = id
		cont := str(p, "CONTINENT")
		if cont != "Seven seas (open ocean)" {
			continents[cont] = append(continents[cont], polys...)
		}
		regions = append(regions, region{ID: id, Name: name, Kind: "country", ISO: iso, BBox: bboxOf(polys)})
	}
	for name, polys := range continents {
		regions = append(regions, region{ID: "continent-" + slug(name), Name: name, Kind: "continent", BBox: bboxOf(polys)})
	}
	for _, f := range load(*admin1) {
		p := f.Properties
		polys := polygons(f)
		iso := str(p, "iso_3166_2")
		if len(polys) == 0 || iso == "" || strings.Contains(iso, "~") {
			continue
		}
		regions = append(regions, region{ID: "sub-" + strings.ToLower(iso), Name: str(p, "name"), Kind: "subdivision", ISO: iso,
			Parent: countryID[str(p, "adm0_a3")], BBox: bboxOf(polys)})
	}
	sort.Slice(regions, func(i, j int) bool {
		order := map[string]int{"world": 0, "continent": 1, "country": 2, "subdivision": 3}
		if order[regions[i].Kind] != order[regions[j].Kind] {
			return order[regions[i].Kind] < order[regions[j].Kind]
		}
		return regions[i].Name < regions[j].Name
	})

	// Planet directories.
	remote := &maps.HTTPRangeReader{URL: *planet}
	head, err := remote.ReadRange(ctx, 0, 16384)
	if err != nil {
		log.Fatal(err)
	}
	h, err := maps.ParseHeader(head)
	if err != nil {
		log.Fatal(err)
	}
	os.MkdirAll(*cache, 0o755)
	leafPath := filepath.Join(*cache, fmt.Sprintf("leaves-%d.bin", h.LeafLength))
	if st, err := os.Stat(leafPath); err != nil || uint64(st.Size()) != h.LeafLength {
		log.Printf("downloading %d MB of planet directories", h.LeafLength>>20)
		if err := download(ctx, *planet, h.LeafOffset, h.LeafLength, leafPath); err != nil {
			log.Fatal(err)
		}
	}
	lf, err := os.Open(leafPath)
	if err != nil {
		log.Fatal(err)
	}
	lr := &localReader{remote: remote, head: head, leafOf: h.LeafOffset, leaves: lf}

	plans := map[string][]uint8{"world": {6, 7}, "continent": {8, 10}, "country": {10, 13, 15}, "subdivision": {10, 13, 15}}
	for i := range regions {
		r := &regions[i]
		zs := plans[r.Kind]
		top := uint8(0)
		for _, z := range zs {
			if maps.TileCount(r.BBox, 0, z) <= maps.MaxPlanTiles {
				top = z
			}
		}
		if top == 0 {
			log.Printf("skip %s: too large", r.ID)
			continue
		}
		plan, err := maps.MakePlan(ctx, lr, r.BBox, 0, top, nil)
		if err != nil {
			log.Printf("%s: %v", r.ID, err)
			continue
		}
		for _, z := range zs {
			if z > top {
				continue
			}
			seen := map[uint64]bool{}
			var bytes uint64
			tiles := 0
			for _, e := range plan.Entries {
				ez, _, _ := maps.IDToZXY(e.TileID)
				if ez > z {
					continue
				}
				tiles++
				if !seen[e.Offset] {
					seen[e.Offset] = true
					bytes += uint64(e.Length)
				}
			}
			// Directories and header add about 1 %.
			bytes += bytes/100 + 16384
			r.Levels = append(r.Levels, level{MaxZoom: z, Tiles: tiles, Bytes: bytes})
		}
		log.Printf("%-40s %v", r.ID, r.Levels)
	}
	var kept []region
	for _, r := range regions {
		if len(r.Levels) > 0 {
			kept = append(kept, r)
		}
	}
	build := strings.TrimSuffix(filepath.Base(*planet), ".pmtiles")
	o := output{Format: "arcana-map-regions", Build: build, Source: *planet, Measured: time.Now().UTC().Format("2006-01-02"),
		Boundaries: "Natural Earth 1:50m (public domain)", Regions: kept}
	data, _ := json.MarshalIndent(o, "", " ")
	if err := os.WriteFile(*out, append(data, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %d regions to %s", len(kept), *out)
}

func download(ctx context.Context, url string, off, n uint64, path string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+n-1))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return errors.New(resp.Status)
	}
	f, err := os.Create(path + ".part")
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	f.Close()
	return os.Rename(path+".part", path)
}

package maps

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/catalog"
)

//go:embed regions.json
var regionsJSON []byte

// Region is an area Arcana offers as a map download.
type Region struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Kind   string  `json:"kind"`
	ISO    string  `json:"iso,omitempty"`
	Parent string  `json:"parent,omitempty"`
	BBox   BBox    `json:"bbox"`
	Levels []Level `json:"levels"`
}

// Level is a detail level with its measured size.
type Level struct {
	MaxZoom uint8  `json:"maxZoom"`
	Tiles   int    `json:"tiles"`
	Bytes   uint64 `json:"bytes"`
}

// RegionCatalog is the bundled list of regions.
type RegionCatalog struct {
	Format   string   `json:"format"`
	Build    string   `json:"build"`
	Source   string   `json:"source"`
	Measured string   `json:"measured"`
	Regions  []Region `json:"regions"`
}

// Regions returns the bundled region catalog.
func Regions() (RegionCatalog, error) {
	var rc RegionCatalog
	if err := json.Unmarshal(regionsJSON, &rc); err != nil {
		return rc, err
	}
	return rc, nil
}

// MethodExtract marks resources installed by extracting from a remote
// PMTiles archive instead of downloading a file.
const MethodExtract = "pmtiles-extract"

// Provider offers OpenStreetMap-based vector maps of regions, extracted on
// demand from the most recent Protomaps planet build.
type Provider struct {
	// BuildsURL lists available planet builds.
	BuildsURL string
	// BaseURL is where builds are served.
	BaseURL string
}

// ID implements catalog.Provider.
func (p *Provider) ID() string { return "maps" }

// Name implements catalog.Provider.
func (p *Provider) Name() string { return "OpenStreetMap regions (Protomaps)" }

func (p *Provider) buildsURL() string {
	if p.BuildsURL != "" {
		return p.BuildsURL
	}
	return "https://build-metadata.protomaps.dev/builds.json"
}

func (p *Provider) baseURL() string {
	if p.BaseURL != "" {
		return strings.TrimRight(p.BaseURL, "/")
	}
	return "https://build.protomaps.com"
}

// SourceURL is the planet archive of a build.
func (p *Provider) SourceURL(build string) string { return p.baseURL() + "/" + build + ".pmtiles" }

var buildKeyRe = regexp.MustCompile(`^(\d{8})\.pmtiles$`)

// LatestBuild returns the newest planet build key (YYYYMMDD).
func (p *Provider) LatestBuild(ctx context.Context, f catalog.Fetcher) (string, error) {
	data, err := f.Get(ctx, p.buildsURL(), 8<<20)
	if err != nil {
		return "", err
	}
	var builds []struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(data, &builds); err != nil {
		return "", fmt.Errorf("map builds list: %w", err)
	}
	var keys []string
	for _, b := range builds {
		if m := buildKeyRe.FindStringSubmatch(b.Key); m != nil {
			keys = append(keys, m[1])
		}
	}
	if len(keys) == 0 {
		return "", errors.New("map builds list is empty")
	}
	sort.Strings(keys)
	return keys[len(keys)-1], nil
}

// Fetch implements catalog.Provider using the latest build.
func (p *Provider) Fetch(ctx context.Context, f catalog.Fetcher) ([]catalog.Resource, error) {
	build, err := p.LatestBuild(ctx, f)
	if err != nil {
		return nil, err
	}
	return p.Resources(build)
}

// Offline returns region resources pointing at the build they were measured
// on; Resolve switches to the newest build before downloading.
func (p *Provider) Offline() ([]catalog.Resource, error) {
	rc, err := Regions()
	if err != nil {
		return nil, err
	}
	return p.Resources(rc.Build)
}

var levelLabels = map[uint8]string{
	6: "World overview: countries, major cities", 7: "World overview with more cities and roads",
	8: "Overview: major roads and cities", 10: "Overview: roads, towns and terrain features",
	13: "Standard: streets, places and points of interest", 15: "Detailed: all streets, buildings and paths",
}

var kindSub = map[string]string{"world": "world", "continent": "continents", "country": "countries", "subdivision": "states and provinces"}

// Resources builds catalog entries for a planet build (YYYYMMDD).
func (p *Provider) Resources(build string) ([]catalog.Resource, error) {
	rc, err := Regions()
	if err != nil {
		return nil, err
	}
	return p.resourcesFor(rc, build)
}

func (p *Provider) resourcesFor(rc RegionCatalog, build string) ([]catalog.Resource, error) {
	if len(build) != 8 {
		return nil, errors.New("invalid build")
	}
	version := build[:4] + "-" + build[4:6]
	published := build[:4] + "-" + build[4:6] + "-" + build[6:8]
	source := p.SourceURL(build)
	verified, _ := time.Parse("2006-01-02", rc.Measured)
	parents := map[string]string{}
	for _, r := range rc.Regions {
		parents[r.ID] = r.Name
	}
	var out []catalog.Resource
	for _, r := range rc.Regions {
		for _, l := range r.Levels {
			label := levelLabels[l.MaxZoom]
			if label == "" {
				label = fmt.Sprintf("Up to zoom level %d", l.MaxZoom)
			}
			title := r.Name + " — map"
			if r.Kind == "subdivision" && parents[r.Parent] != "" {
				title = r.Name + ", " + parents[r.Parent] + " — map"
			}
			var regions []string
			if r.ISO != "" {
				regions = []string{r.ISO}
			}
			scope := "regional"
			if r.Kind == "world" {
				scope = "global"
			}
			res := catalog.Resource{
				ID:       fmt.Sprintf("maps.%s-z%d", r.ID, l.MaxZoom),
				Provider: "maps",
				Title:    title,
				Description: fmt.Sprintf("Offline vector map of %s from OpenStreetMap, rendered on your device. %s. Place names become searchable offline after installation.",
					r.Name, label),
				Category:     catalog.CategoryMaps,
				Subcategory:  kindSub[r.Kind],
				Family:       "maps." + r.ID,
				Variant:      fmt.Sprintf("z%d", l.MaxZoom),
				VariantLabel: label,
				Publisher:    "OpenStreetMap contributors; Protomaps",
				SourceURL:    "https://protomaps.com/",
				Version:      version,
				Published:    published,
				LastVerified: &verified,
				Languages:    []string{"mul"},
				Coverage:     catalog.Coverage{Scope: scope, Regions: regions, BBox: []float64{r.BBox[0], r.BBox[1], r.BBox[2], r.BBox[3]}},
				Format:       "pmtiles",
				Files: []catalog.File{{
					Name:    fmt.Sprintf("%s-z%d.pmtiles", r.ID, l.MaxZoom),
					Mirrors: []catalog.Mirror{{URL: source}},
				}},
				DownloadSize:  int64(l.Bytes),
				SizeEstimated: true,
				Capabilities:  catalog.Capabilities{TitleSearch: true},
				Media:         catalog.Media{Detail: map[bool]string{true: "full", false: "reduced"}[l.MaxZoom >= 15]},
				License: catalog.License{
					ID: "ODbL-1.0", Name: "Open Data Commons Open Database License 1.0 (OpenStreetMap)",
					URL: "https://www.openstreetmap.org/copyright", Redistribution: catalog.RedistPermitted, Verified: true,
					Attribution: "© OpenStreetMap contributors. Basemap by Protomaps. Natural Earth. Land cover: ESA WorldCover (CC BY 4.0).",
					Notes:       "Maps are a Produced Work under the ODbL: keep the attribution visible. The place-name index Arcana builds from them is a Derivative Database under the ODbL.",
				},
				UpdateStrategy: catalog.UpdateReplace,
				Warnings: []catalog.Warning{{Kind: "content",
					Text: "Map data from OpenStreetMap volunteers may be incomplete or out of date. Do not rely on it alone where errors could be dangerous."}},
				Stats: map[string]int64{"tiles": int64(l.Tiles)},
				ProviderRef: map[string]string{
					"method": MethodExtract, "source": source, "build": build,
					"bbox":    fmt.Sprintf("%g,%g,%g,%g", r.BBox[0], r.BBox[1], r.BBox[2], r.BBox[3]),
					"minzoom": "0", "maxzoom": fmt.Sprint(l.MaxZoom), "region": r.ID,
				},
			}
			switch r.Kind {
			case "world":
				res.Popularity = 100
			case "continent":
				res.Popularity = 50
			}
			out = append(out, res)
		}
	}
	return out, nil
}

// Resolve implements catalog.Resolver: it points the resource at the newest
// planet build (builds are only kept for a short time).
func (p *Provider) Resolve(ctx context.Context, f catalog.Fetcher, r catalog.Resource) (catalog.Resource, error) {
	build, err := p.LatestBuild(ctx, f)
	if err != nil {
		return r, err
	}
	ref := map[string]string{}
	for k, v := range r.ProviderRef {
		ref[k] = v
	}
	ref["build"] = build
	ref["source"] = p.SourceURL(build)
	r.ProviderRef = ref
	files := append([]catalog.File(nil), r.Files...)
	files[0].Mirrors = []catalog.Mirror{{URL: ref["source"]}}
	r.Files = files
	return r, nil
}

// CustomArea builds a resource for a user-chosen bounding box.
func (p *Provider) CustomArea(build, name string, b BBox, maxzoom uint8, estimate uint64) (catalog.Resource, error) {
	if !b.Valid() || maxzoom > 15 {
		return catalog.Resource{}, errors.New("invalid area")
	}
	slugName := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slugName == "" {
		slugName = "area"
	}
	if len(slugName) > 40 {
		slugName = slugName[:40]
	}
	id := fmt.Sprintf("custom-%s-%x", slugName, uint32(b[0]*1000)^uint32(b[1]*1000)<<8^uint32(b[2]*1000)<<16^uint32(b[3]*1000)<<24)
	region := Region{ID: id, Name: name, Kind: "custom", BBox: b, Levels: []Level{{MaxZoom: maxzoom, Bytes: estimate}}}
	rc := RegionCatalog{Format: "arcana-map-regions", Build: build, Measured: time.Now().UTC().Format("2006-01-02"), Regions: []Region{region}}
	res, err := p.resourcesFor(rc, build)
	if err != nil || len(res) != 1 {
		return catalog.Resource{}, errors.New("could not describe the area")
	}
	res[0].Subcategory = "custom areas"
	return res[0], nil
}

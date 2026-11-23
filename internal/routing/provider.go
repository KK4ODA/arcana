package routing

// Road networks in the catalog: one per map region that Geofabrik publishes
// an OpenStreetMap extract for, matched by ISO 3166 code. Arcana downloads
// the extract, checks it against Geofabrik's MD5, and builds the network
// on the user's computer (see Install).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/maps"
)

// MethodBuild marks resources installed by building a road network from an
// OSM extract.
const MethodBuild = "osm-roads"

// Format is the catalog format of road networks.
const Format = "arcroute"

// Provider offers road networks for directions.
type Provider struct {
	// IndexURL is Geofabrik's extract index (index-v1-nogeom.json).
	IndexURL string
	// Now is the clock (for tests).
	Now func() time.Time
}

// DefaultIndexURL lists Geofabrik's extracts.
const DefaultIndexURL = "https://download.geofabrik.de/index-v1-nogeom.json"

// ID implements catalog.Provider.
func (p *Provider) ID() string { return "roads" }

// Name implements catalog.Provider.
func (p *Provider) Name() string { return "OpenStreetMap road networks (Geofabrik)" }

// pbfPerMapByte estimates an extract's size from the region's standard map
// (zoom 13): measured 1.3 (Luxembourg) to 2.1 (Georgia, US); the higher
// value keeps space checks on the safe side.
const pbfPerMapByte = 2.1

// MaxExtract is the largest extract offered. Building needs memory of about
// five times the extract's size (measured: 1.9 GB for Georgia's 392 MB), so
// larger regions would exhaust a typical computer; their states or
// provinces can be used instead where offered.
const MaxExtract = 1_000_000_000

// buildMemoryPerByte is the measured peak memory per byte of extract.
const buildMemoryPerByte = 5

// isoList reads an ISO property that is a string ("LU", "HT,DO") or, as
// Geofabrik documents it, an array of strings.
type isoList []string

func (l *isoList) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				*l = append(*l, p)
			}
		}
		return nil
	}
	var a []string
	if err := json.Unmarshal(b, &a); err != nil {
		return nil // unknown shape: no codes
	}
	*l = a
	return nil
}

type indexFile struct {
	Features []struct {
		Properties struct {
			ID      string  `json:"id"`
			Name    string  `json:"name"`
			Country isoList `json:"iso3166-1:alpha2"`
			Sub     isoList `json:"iso3166-2"`
			URLs    struct {
				PBF string `json:"pbf"`
			} `json:"urls"`
		} `json:"properties"`
	} `json:"features"`
}

type extract struct{ id, pbf string }

// parseIndex maps ISO codes to extracts. Extracts covering several
// countries are skipped; the first extract listed for a code is kept.
func parseIndex(data []byte) (map[string]extract, error) {
	var idx indexFile
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("road network index: %w", err)
	}
	out := map[string]extract{}
	for _, f := range idx.Features {
		pr := f.Properties
		if !strings.HasPrefix(pr.URLs.PBF, "https://") {
			continue
		}
		var codes []string
		if len(pr.Sub) == 1 {
			codes = pr.Sub
		} else if len(pr.Country) == 1 && len(pr.Sub) == 0 {
			codes = pr.Country
		}
		for _, c := range codes {
			c = strings.ToUpper(c)
			if _, dup := out[c]; !dup {
				out[c] = extract{pr.ID, pr.URLs.PBF}
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("road network index lists no extracts")
	}
	return out, nil
}

// Fetch implements catalog.Provider.
func (p *Provider) Fetch(ctx context.Context, f catalog.Fetcher) ([]catalog.Resource, error) {
	u := p.IndexURL
	if u == "" {
		u = DefaultIndexURL
	}
	data, err := f.Get(ctx, u, 16<<20)
	if err != nil {
		return nil, err
	}
	ex, err := parseIndex(data)
	if err != nil {
		return nil, err
	}
	return p.resources(ex)
}

func (p *Provider) resources(ex map[string]extract) ([]catalog.Resource, error) {
	rc, err := maps.Regions()
	if err != nil {
		return nil, err
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	t := now().UTC()
	version := t.Format("2006-01")
	parents := map[string]string{}
	for _, r := range rc.Regions {
		parents[r.ID] = r.Name
	}
	var out []catalog.Resource
	for _, r := range rc.Regions {
		e, ok := ex[strings.ToUpper(r.ISO)]
		if r.ISO == "" || !ok {
			continue
		}
		var mapBytes uint64
		for _, l := range r.Levels {
			if l.MaxZoom <= 13 && l.Bytes > mapBytes {
				mapBytes = l.Bytes
			}
		}
		est := int64(float64(mapBytes) * pbfPerMapByte)
		if est <= 0 || est > MaxExtract {
			continue
		}
		name := r.Name
		if r.Kind == "subdivision" && parents[r.Parent] != "" {
			name = r.Name + ", " + parents[r.Parent]
		}
		verified := t
		res := catalog.Resource{
			ID:       "roads." + r.ID,
			Provider: "roads",
			Title:    name + " — road network",
			Description: fmt.Sprintf("Roads and paths of %s from OpenStreetMap, for offline directions by car, bicycle and on foot, "+
				"including turn-by-turn guidance from your GPS position. Arcana downloads the region's OpenStreetMap extract, "+
				"checks it, and builds a compact road network from it on this computer, which needs about %s of free memory "+
				"while it runs.", r.Name, memWords(est*buildMemoryPerByte)),
			Category:     catalog.CategoryMaps,
			Subcategory:  "road networks for directions",
			Family:       "roads." + r.ID,
			Publisher:    "OpenStreetMap contributors; extract by Geofabrik",
			SourceURL:    "https://download.geofabrik.de/",
			Version:      version,
			Published:    t.Format("2006-01-02"),
			LastVerified: &verified,
			Languages:    []string{"mul"},
			Coverage:     catalog.Coverage{Scope: "regional", Regions: []string{r.ISO}, BBox: []float64{r.BBox[0], r.BBox[1], r.BBox[2], r.BBox[3]}},
			Format:       Format,
			Files: []catalog.File{{
				Name:    r.ID + ".arcroute",
				Mirrors: []catalog.Mirror{{URL: e.pbf}},
			}},
			DownloadSize:  est,
			SizeEstimated: true,
			License: catalog.License{
				ID: "ODbL-1.0", Name: "Open Data Commons Open Database License 1.0 (OpenStreetMap)",
				URL: "https://www.openstreetmap.org/copyright", Redistribution: catalog.RedistPermitted, Verified: true,
				Attribution: "© OpenStreetMap contributors.",
				Notes:       "The road network Arcana builds is a Derivative Database under the ODbL; routes drawn from it are Produced Works and must credit OpenStreetMap contributors.",
			},
			UpdateStrategy: catalog.UpdateReplace,
			Warnings: []catalog.Warning{{Kind: "content",
				Text: "Directions come from OpenStreetMap volunteers' data, which can be incomplete or out of date: a road may be closed, private or missing. Follow road signs and traffic laws, and do not rely on directions alone where a mistake could be dangerous."}},
			ProviderRef: map[string]string{
				"method": MethodBuild, "source": e.pbf, "md5": e.pbf + ".md5", "sourceHash": "md5",
				"extract": e.id, "region": r.ID,
			},
		}
		out = append(out, res)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func memWords(b int64) string {
	if b < 1e9 {
		return fmt.Sprintf("%d MB", max(100, (b/1e6+50)/100*100))
	}
	return fmt.Sprintf("%.1f GB", float64(b)/1e9)
}

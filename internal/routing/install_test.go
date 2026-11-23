package routing

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KK4ODA/arcana/internal/catalog"
)

func TestInstallDownloadsChecksAndBuilds(t *testing.T) {
	data := writePBF(newTown().nodes, newTown().ways, nil)
	sum := md5.Sum(data)
	md5hex := hex.EncodeToString(sum[:])
	var cut atomic.Bool // when set, the first response stops half way
	cut.Store(true)
	var badSum atomic.Bool
	var ranges atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/town.osm.pbf.md5":
			s := md5hex
			if badSum.Load() {
				s = strings.Repeat("0", 32)
			}
			fmt.Fprintf(w, "%s  town.osm.pbf\n", s)
		case "/town.osm.pbf":
			if r.Header.Get("Range") != "" {
				ranges.Add(1)
			}
			if cut.Load() && r.Header.Get("Range") == "" {
				// Promise the whole file, send half, then drop the
				// connection.
				w.Header().Set("ETag", `"v1"`)
				w.Header().Set("Content-Length", fmt.Sprint(len(data)))
				w.WriteHeader(200)
				w.Write(data[:len(data)/2])
				w.(http.Flusher).Flush()
				hj, _ := w.(http.Hijacker)
				c, _, _ := hj.Hijack()
				c.Close()
				return
			}
			w.Header().Set("ETag", `"v1"`)
			http.ServeContent(w, r, "town.osm.pbf", time.Time{}, strings.NewReader(string(data)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	res := catalog.Resource{ID: "roads.test", ProviderRef: map[string]string{
		"method": MethodBuild, "source": ts.URL + "/town.osm.pbf", "md5": ts.URL + "/town.osm.pbf.md5", "sourceHash": "md5"}}
	dir := t.TempDir()
	out := filepath.Join(dir, "test.arcroute")
	var planned int64
	req := InstallRequest{Resource: res, Out: out, Client: ts.Client(),
		Planned: func(n int64) error { planned = n; return nil }}

	// The first attempt is cut off; the partial file stays for resuming.
	if _, _, err := Install(context.Background(), req); err == nil {
		t.Fatal("a cut-off download must fail")
	}
	if fi, err := os.Stat(out + ".osm.pbf"); err != nil || fi.Size() == 0 || fi.Size() >= int64(len(data)) {
		t.Fatalf("partial download not kept: %v", err)
	}
	cut.Store(false)
	size, sha, err := Install(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if ranges.Load() == 0 {
		t.Error("the second attempt did not resume with a range request")
	}
	if planned != int64(len(data)) {
		t.Errorf("planned %d, want %d", planned, len(data))
	}
	b, _ := os.ReadFile(out)
	h := sha256.Sum256(b)
	if int64(len(b)) != size || hex.EncodeToString(h[:]) != sha {
		t.Errorf("size/sha mismatch")
	}
	if _, err := os.Stat(out + ".osm.pbf"); !os.IsNotExist(err) {
		t.Error("the extract was not removed after building")
	}
	g, err := Load(out)
	if err != nil || g.NumEdges() == 0 || !strings.Contains(g.Source, md5hex) {
		t.Fatalf("installed network: %v", err)
	}

	// A checksum mismatch discards the download.
	os.Remove(out)
	badSum.Store(true)
	if _, _, err := Install(context.Background(), req); !errors.Is(err, ErrChecksum) {
		t.Fatalf("bad checksum: %v", err)
	}
	if _, err := os.Stat(out + ".osm.pbf"); !os.IsNotExist(err) {
		t.Error("a download that failed its checksum was kept")
	}
}

func TestParseIndexAndResources(t *testing.T) {
	index := `{"type":"FeatureCollection","features":[
	 {"type":"Feature","properties":{"id":"luxembourg","name":"Luxembourg","iso3166-1:alpha2":"LU","parent":"europe",
	   "urls":{"pbf":"https://download.geofabrik.de/europe/luxembourg/luxembourg-latest.osm.pbf"}},"geometry":null},
	 {"type":"Feature","properties":{"id":"us/georgia","name":"Georgia","path-slug":"georgia","iso3166-2":"US-GA","parent":"us",
	   "urls":{"pbf":"https://download.geofabrik.de/north-america/us/georgia/georgia-latest.osm.pbf"}},"geometry":null},
	 {"type":"Feature","properties":{"id":"georgia","name":"Georgia","iso3166-1:alpha2":["GE"],"parent":"europe",
	   "urls":{"pbf":"https://download.geofabrik.de/europe/georgia/georgia-latest.osm.pbf"}},"geometry":null},
	 {"type":"Feature","properties":{"id":"haiti-and-domrep","name":"Haiti and Dominican Republic","iso3166-1:alpha2":"HT,DO",
	   "urls":{"pbf":"https://download.geofabrik.de/central-america/haiti-and-domrep-latest.osm.pbf"}},"geometry":null},
	 {"type":"Feature","properties":{"id":"germany","name":"Germany","iso3166-1:alpha2":"DE",
	   "urls":{"pbf":"https://download.geofabrik.de/europe/germany/germany-latest.osm.pbf"}},"geometry":null},
	 {"type":"Feature","properties":{"id":"bayern","name":"Bayern","parent":"germany",
	   "urls":{"pbf":"https://download.geofabrik.de/europe/germany/bayern/bayern-latest.osm.pbf"}},"geometry":null}
	]}`
	ex, err := parseIndex([]byte(index))
	if err != nil {
		t.Fatal(err)
	}
	if ex["US-GA"].id != "us/georgia" || ex["GE"].id != "georgia" || ex["LU"].id != "luxembourg" {
		t.Errorf("index: %+v", ex)
	}
	if _, ok := ex["HT"]; ok {
		t.Error("a multi-country extract must not stand for one country")
	}
	p := &Provider{Now: func() time.Time { return time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC) }}
	res, err := p.resources(ex)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]catalog.Resource{}
	for _, r := range res {
		if err := r.Validate(); err != nil {
			t.Errorf("%s: %v", r.ID, err)
		}
		byID[r.ID] = r
	}
	ga, ok := byID["roads.sub-us-ga"]
	if !ok || ga.Title != "Georgia, United States of America — road network" || ga.Version != "2026-11" ||
		ga.Files[0].Mirrors[0].URL != "https://download.geofabrik.de/north-america/us/georgia/georgia-latest.osm.pbf" ||
		ga.ProviderRef["md5"] != ga.Files[0].Mirrors[0].URL+".md5" || ga.Format != Format {
		t.Errorf("Georgia: %+v", ga)
	}
	if _, ok := byID["roads.country-lu"]; !ok {
		t.Error("Luxembourg missing")
	}
	// Germany's extract is too large to build on a typical computer.
	if _, ok := byID["roads.country-de"]; ok {
		t.Error("Germany should be too large")
	}
}

package routing

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"testing"
	"time"
)

func fileSource(p string) Source {
	return func() (io.ReadCloser, int64, error) {
		f, err := os.Open(p)
		if err != nil {
			return nil, 0, err
		}
		st, _ := f.Stat()
		return f, st.Size(), nil
	}
}

// TestBuildRealExtract builds a network from ARCANA_TEST_PBF and, when
// ARCANA_TEST_ROUTE is "lat,lon;lat,lon", routes between the points.
func TestBuildRealExtract(t *testing.T) {
	p := os.Getenv("ARCANA_TEST_PBF")
	if p == "" {
		t.Skip("set ARCANA_TEST_PBF to an .osm.pbf file")
	}
	start := time.Now()
	g, st, err := Build(context.Background(), fileSource(p), BuildOptions{Source: p})
	if err != nil {
		t.Fatal(err)
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	t.Logf("built %s in %s (peak heap about %d MB, %d missing nodes)", st, time.Since(start).Round(time.Millisecond), ms.HeapSys>>20, st.MissingNodes)
	var buf bytes.Buffer
	if _, err := g.WriteTo(&buf); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir() + "/net.arcroute"
	os.WriteFile(out, buf.Bytes(), 0o644)
	start = time.Now()
	g2, err := Load(out)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("file %d MB, loaded in %s", buf.Len()>>20, time.Since(start).Round(time.Millisecond))
	r := NewRouter(g2)
	pts := []Point{{49.6116, 6.1319}, {49.4958, 5.9806}} // Luxembourg to Esch-sur-Alzette
	if v := os.Getenv("ARCANA_TEST_ROUTE"); v != "" {
		var a, b Point
		if _, err := fmt.Sscanf(v, "%g,%g;%g,%g", &a.Lat, &a.Lon, &b.Lat, &b.Lon); err != nil {
			t.Fatal(err)
		}
		pts = []Point{a, b}
	}
	for _, m := range []Mode{Car, Bike, Foot} {
		start = time.Now()
		rt, err := r.Route(context.Background(), pts, m)
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		t.Logf("%s: %.1f km, %.0f min, %d steps, %s", m, rt.Distance/1000, rt.Duration/60, len(rt.Steps), time.Since(start).Round(time.Millisecond))
		if m == Car {
			for i, s := range rt.Steps {
				if i < 25 || i > len(rt.Steps)-5 {
					t.Logf("  %-60s %6.0f m", s.Text, s.Distance)
				}
			}
			t.Logf("warnings: %v", rt.Warnings)
		}
	}
}

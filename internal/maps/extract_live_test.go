package maps

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLiveExtract extracts a small country from a real planet build when
// ARCANA_PLANET_URL is set (e.g. https://build.protomaps.com/20261121.pmtiles).
func TestLiveExtract(t *testing.T) {
	url := os.Getenv("ARCANA_PLANET_URL")
	if url == "" {
		t.Skip("set ARCANA_PLANET_URL to run against a real planet build")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	rr := &HTTPRangeReader{URL: url}
	start := time.Now()
	plan, err := MakePlan(ctx, rr, BBox{5.73, 49.44, 6.53, 50.19}, 0, 15, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("plan in %s: %d tiles, %d unique, tile bytes %d, download %d in %d ranges, directories %d",
		time.Since(start).Round(time.Millisecond), plan.Tiles, plan.UniqueTiles, plan.TileBytes, plan.DownloadBytes, len(plan.Ranges), plan.DirectoryBytes)
	out := filepath.Join(t.TempDir(), "lu.pmtiles")
	start = time.Now()
	sum, err := Extract(ctx, rr, plan, out, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(out)
	t.Logf("extracted %d bytes in %s, sha256 %s", st.Size(), time.Since(start).Round(time.Millisecond), sum)
	a, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	tile, err := a.Tile(14, lonToX(6.13, 14), latToY(49.61, 14)) // Luxembourg City
	if err != nil || len(tile) == 0 {
		t.Fatalf("city tile: %v", err)
	}
	if os.Getenv("ARCANA_KEEP_EXTRACT") != "" {
		data, _ := os.ReadFile(out)
		os.WriteFile(os.Getenv("ARCANA_KEEP_EXTRACT"), data, 0o644)
	}
}

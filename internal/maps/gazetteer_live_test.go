package maps

import (
	"context"
	"os"
	"testing"
)

// TestGazetteerOnRealExtract indexes a real extract when
// ARCANA_TEST_PMTILES points to one (e.g. from TestLiveExtract).
func TestGazetteerOnRealExtract(t *testing.T) {
	p := os.Getenv("ARCANA_TEST_PMTILES")
	if p == "" {
		t.Skip("set ARCANA_TEST_PMTILES to a Protomaps extract")
	}
	a, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	g, err := OpenGazetteer("")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	n, err := g.IndexArchive(context.Background(), "test", "1", a, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := g.Search(context.Background(), os.Getenv("ARCANA_TEST_PLACE"), 5)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("indexed %d places; search: %+v", n, res)
	if n == 0 || len(res) == 0 {
		t.Fatal("no places found")
	}
}

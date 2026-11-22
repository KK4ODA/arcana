package catalog

import (
	"compress/gzip"
	"os"
	"testing"
)

// TestGutenbergRealCatalog parses a downloaded pg_catalog.csv.gz when
// ARCANA_PG_CATALOG points to one.
func TestGutenbergRealCatalog(t *testing.T) {
	p := os.Getenv("ARCANA_PG_CATALOG")
	if p == "" {
		t.Skip("set ARCANA_PG_CATALOG to a pg_catalog.csv.gz to run")
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	res, err := (&GutenbergProvider{}).ParseCSV(zr)
	if err != nil {
		t.Fatal(err)
	}
	valid, errs := ValidateAll("gutenberg", res)
	cats := map[string]int{}
	for _, r := range valid {
		cats[r.Category]++
	}
	t.Logf("parsed %d, valid %d, rejected %d; by category %v", len(res), len(valid), len(errs), cats)
	for i, e := range errs {
		if i < 5 {
			t.Log(e)
		}
	}
	if len(valid) < 1000 {
		t.Fatal("too few practical books")
	}
}

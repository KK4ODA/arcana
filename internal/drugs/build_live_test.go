package drugs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestBuildLive builds a reference from real openFDA partitions named in
// ARCANA_OPENFDA_ZIPS (colon-separated paths) and reports its size.
func TestBuildLive(t *testing.T) {
	zips := filepath.SplitList(os.Getenv("ARCANA_OPENFDA_ZIPS"))
	if len(zips) == 0 || zips[0] == "" {
		t.Skip("set ARCANA_OPENFDA_ZIPS to openFDA drug-label zip files")
	}
	out := filepath.Join(t.TempDir(), "drug-labels.db")
	start := time.Now()
	st, err := Build(context.Background(), zips, out, map[string]string{"export": "live"})
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(out)
	var in int64
	for _, z := range zips {
		zi, _ := os.Stat(z)
		in += zi.Size()
	}
	t.Logf("%+v in %s; zips %d MB → database %d MB (%.2f×)", st, time.Since(start).Round(time.Second), in>>20, fi.Size()>>20, float64(fi.Size())/float64(in))
	r, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, q := range []string{"ibuprofen", "bismuth", "tylenol", "hand sanitizer"} {
		gs, total, err := r.Search(context.Background(), q, SearchOptions{Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("search %q: %d groups", q, total)
		for _, g := range gs {
			t.Logf("   %s — %d products, e.g. %s (%s)", g.Title, g.Products, g.Example.Name, g.Example.Kind)
		}
	}
	res, err := r.BySituation(context.Background(), []string{"diarrhea", "upset-stomach"}, SituationOptions{Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range res {
		t.Logf("situation %v: %d groups", s.Situations, len(s.Groups))
		for _, g := range s.Groups {
			t.Logf("   %s — %d products", g.Title, g.Products)
		}
	}
}

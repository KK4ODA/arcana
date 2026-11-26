package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/KK4ODA/arcana/internal/catalog"
)

func res(id, family, variant, title, cat string, size int64, langs ...string) catalog.Resource {
	return catalog.Resource{ID: id, Provider: "kiwix", Family: family, Variant: variant, Title: title, Category: cat,
		Version: "2026-10", Languages: langs, Format: "zim", DownloadSize: size,
		Files: []catalog.File{{Name: "x.zim", MetalinkURL: "https://x/x.meta4"}}}
}

func TestCatalogSearch(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	rs := []catalog.Resource{
		res("kiwix.wikipedia_en_all_maxi", "kiwix.wikipedia_en_all", "maxi", "Wikipedia", "reference", 100, "en"),
		res("kiwix.wikipedia_en_all_mini", "kiwix.wikipedia_en_all", "mini", "Wikipedia", "reference", 10, "en"),
		res("kiwix.wikipedia_es_all_mini", "kiwix.wikipedia_es_all", "mini", "Wikipedia en español", "reference", 5, "es"),
		res("kiwix.wikipedia_en_medicine_maxi", "kiwix.wikipedia_en_medicine", "maxi", "Médecine Wikipedia", "medicine", 2, "en"),
	}
	rs[3].Description = "Medical encyclopedia"
	if err := s.ReplaceProviderResources(ctx, "kiwix", "Kiwix", rs, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, total, err := s.SearchResources(ctx, Query{Text: "wiki", Language: "en", Sort: "size"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || got[0].ID != "kiwix.wikipedia_en_medicine_maxi" {
		t.Fatalf("got %d results, first %v", total, got)
	}
	// Diacritics folded; FTS syntax in user input is neutralized.
	if _, n, err := s.SearchResources(ctx, Query{Text: `médecine"(*:`}); err != nil || n != 1 {
		t.Fatalf("diacritic search: n=%d err=%v", n, err)
	}
	if _, n, _ := s.SearchResources(ctx, Query{Category: "medicine"}); n != 1 {
		t.Fatalf("category filter n=%d", n)
	}
	fam, err := s.FamilyVariants(ctx, []string{"kiwix.wikipedia_en_all"})
	if err != nil || len(fam["kiwix.wikipedia_en_all"]) != 2 {
		t.Fatalf("family variants: %v %v", fam, err)
	}
	langs, _ := s.LanguageCounts(ctx)
	if len(langs) != 2 || langs[0].Key != "en" || langs[0].Count != 3 {
		t.Fatalf("language counts %v", langs)
	}
	// Replacing a provider's entries is atomic and complete.
	if err := s.ReplaceProviderResources(ctx, "kiwix", "Kiwix", rs[:1], time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, n, _ := s.SearchResources(ctx, Query{Text: "medical"}); n != 0 {
		t.Fatal("stale FTS rows left behind")
	}
}

func TestModelFilterAndOrder(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	model := func(id, title string, mem int64, pop int) catalog.Resource {
		return catalog.Resource{ID: id, Provider: "ollama", Title: title, Category: "assistant", Version: "1",
			Languages: []string{"mul"}, Format: "gguf", DownloadSize: mem / 2, Popularity: pop,
			Stats: map[string]int64{"memoryBytes": mem}, Files: []catalog.File{{Name: "m.gguf", MetalinkURL: "https://x/x.meta4"}}}
	}
	rs := []catalog.Resource{
		model("ollama.a-70b", "a:70b", 48<<30, 1),
		model("ollama.b-1b", "b:1b", 1<<30, 1),
		model("ollama.c-7b", "c:7b", 6<<30, 5),
	}
	engine := model("ollama.engine", "engine", 0, 0)
	engine.Stats = nil
	rs = append(rs, engine)
	if err := s.ReplaceProviderResources(ctx, "ollama", "Ollama", rs, time.Now()); err != nil {
		t.Fatal(err)
	}
	got, n, err := s.SearchResources(ctx, Query{Category: "assistant", MaxMemory: 8 << 30, RankFirst: true})
	if err != nil {
		t.Fatal(err)
	}
	// Entries that state no memory need (the engine) stay listed.
	if n != 3 {
		t.Fatalf("memory filter kept %d entries, want 3", n)
	}
	// With RankFirst and no search words, a provider's rank orders the list
	// before titles.
	if got[0].ID != "ollama.c-7b" || got[1].ID != "ollama.b-1b" {
		t.Fatalf("order %s, %s", got[0].ID, got[1].ID)
	}
}

func TestJobsPersistAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	j := &Job{ID: "j1", ResourceID: "x.y", State: JobRunning, Offset: 4096, HashState: map[string][]byte{"sha256": {1, 2, 3}}}
	if err := s.PutJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	// No Close: simulate a crash. WAL with synchronous=FULL must have made
	// the commit durable.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.GetJob(ctx, "j1")
	if err != nil || got.Offset != 4096 || string(got.HashState["sha256"]) != "\x01\x02\x03" {
		t.Fatalf("job after reopen: %+v %v", got, err)
	}
	s.db.Close()
}

func TestRecoverFromDamagedDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	os.WriteFile(path, []byte("this is not a sqlite database, it was truncated by a USB pull"), 0o644)
	s, rebuild, err := OpenOrRecover(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !rebuild {
		t.Fatal("damaged database should request a rebuild")
	}
	matches, _ := filepath.Glob(path + ".damaged-*")
	if len(matches) == 0 {
		t.Fatal("damaged database should be kept aside for diagnosis")
	}
}

func TestMemoryStore(t *testing.T) {
	s, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	in := Installed{ResourceID: "a.b", Version: "1", Files: []InstalledFile{{Path: "1/x", Size: 10}}}
	if err := s.ReplaceInstalled(context.Background(), []Installed{in}); err != nil {
		t.Fatal(err)
	}
	all, _ := s.ListInstalled(context.Background())
	if len(all) != 1 || all[0].TotalSize() != 10 {
		t.Fatalf("%v", all)
	}
}

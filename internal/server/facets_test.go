package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/KK4ODA/arcana/internal/catalog"
)

// TestFacetsListCategoriesAlphabetically: Discover's sidebar shows the
// categories in the order the facets give them, so that order is by title.
func TestFacetsListCategoriesAlphabetically(t *testing.T) {
	e := newEnv(t)
	rec := e.do(e.appH, "GET", "/api/catalog/facets", local, "127.0.0.1:18470", nil, nil)
	if rec.Code != 200 {
		t.Fatalf("facets: %d %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Categories []struct{ ID, Title string } `json:"categories"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Categories) != len(catalog.Categories) {
		t.Fatalf("%d categories, want all %d", len(body.Categories), len(catalog.Categories))
	}
	titles := make([]string, len(body.Categories))
	for i, c := range body.Categories {
		titles[i] = c.Title
	}
	if !sort.SliceIsSorted(titles, func(i, j int) bool { return strings.ToLower(titles[i]) < strings.ToLower(titles[j]) }) {
		t.Errorf("categories not in alphabetical order:\n%s", strings.Join(titles, "\n"))
	}
}

// TestCatalogOrderWithoutWords: with no search words Discover lists titles
// in order, so hundreds of language models do not push the Kiwix library
// off the first page; only the question-answering category puts the
// providers' ranks first, which keeps Arcana's tested models on top.
func TestCatalogOrderWithoutWords(t *testing.T) {
	e := newEnv(t)
	var kiwix []catalog.Resource
	// About as many archives as the real Kiwix catalog lists.
	for i := 0; i < 3000; i++ {
		id := fmt.Sprintf("kiwix.archive_%03d", i)
		kiwix = append(kiwix, catalog.Resource{ID: id, Provider: "kiwix", Title: fmt.Sprintf("%c%c archive %d", 'A'+i%26, 'a'+(i/26)%26, i),
			Category: "reference", Version: "2026-11", Languages: []string{"en"}, Format: "zim", DownloadSize: 1 << 20,
			Files: []catalog.File{{Name: id + ".zim", MetalinkURL: "https://download.kiwix.org/x.meta4"}}})
	}
	if err := e.app.Store.ReplaceProviderResources(context.Background(), "kiwix", "Kiwix", kiwix, time.Now()); err != nil {
		t.Fatal(err)
	}
	page := func(url string) []catalog.Resource {
		rec := e.do(e.appH, "GET", url, local, "127.0.0.1:18470", nil, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: %d %s", url, rec.Code, rec.Body.String())
		}
		var body struct{ Resources []catalog.Resource }
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Resources
	}
	all := page("/api/catalog?limit=100")
	count := map[string]int{}
	titles := make([]string, len(all))
	for i, r := range all {
		count[r.Provider]++
		titles[i] = strings.ToLower(r.Title)
	}
	if !sort.StringsAreSorted(titles) {
		t.Errorf("unfiltered list is not in title order: %v", titles[:10])
	}
	for p, n := range count {
		if n > len(all)*8/10 {
			t.Errorf("provider %s fills %d of the first %d entries: %v", p, n, len(all), count)
		}
	}
	if count["kiwix"] == 0 || count["ollama"] > 20 {
		t.Errorf("Kiwix archives missing or language models crowding the first page: %v", count)
	}
	models := page("/api/catalog?category=assistant&limit=10")
	for _, r := range models[:4] {
		if r.Provider != "arcana" {
			t.Errorf("question answering starts with %s, want the engine and Arcana's tested models first", r.ID)
		}
	}
}

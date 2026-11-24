package server

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"

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

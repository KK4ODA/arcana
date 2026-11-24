package server

import (
	"io/fs"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/web"
)

// TestDiscoverLinksUseRealCategories checks every link into Discover that
// names a category. A category the catalog does not have filters out
// everything, so the link opens an empty list ("Get a road network" and
// "Browse map regions" named "maps" instead of "maps-navigation").
func TestDiscoverLinksUseRealCategories(t *testing.T) {
	known := map[string]bool{"": true}
	for _, c := range catalog.Categories {
		known[c.ID] = true
	}
	linkRe := regexp.MustCompile("#/discover\\?([^'\"`\\s]+)")
	checked := 0
	err := fs.WalkDir(web.Static, "static/js", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") {
			return err
		}
		data, _ := fs.ReadFile(web.Static, p)
		for _, m := range linkRe.FindAllStringSubmatch(string(data), -1) {
			if strings.Contains(m[1], "${") {
				continue // built at run time
			}
			q, err := url.ParseQuery(m[1])
			if err != nil {
				t.Errorf("%s: link #/discover?%s does not parse: %v", p, m[1], err)
				continue
			}
			if !q.Has("category") {
				continue
			}
			checked++
			if c := q.Get("category"); !known[c] {
				t.Errorf("%s: link #/discover?%s names category %q, which the catalog does not have", p, m[1], c)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("found no Discover links with a category: the pattern no longer matches the interface")
	}
}

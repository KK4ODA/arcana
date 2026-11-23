package kiwix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// TestSearchInsideSample covers what the reader's search box uses: titles
// completed in one archive, and pages of full-text results from it.
func TestSearchInsideSample(t *testing.T) {
	bin := testBinary(t)
	sample, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "sample", "arcana-sample_en_2026-11.zim"))
	s := New(bin, t.TempDir(), nil)
	defer s.Stop()
	if err := s.Apply(context.Background(), []Archive{{ResourceID: "arcana.sample", Path: sample, Language: "en"}}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	b := s.Books()["arcana.sample"]

	sg, err := s.Suggest(ctx, "arcana.sample", "mor", 5)
	if err != nil || len(sg) == 0 {
		t.Fatalf("suggest: %v %+v", err, sg)
	}
	if sg[0].Title != "International Morse code" || sg[0].Path != b.ContentPath+"morse_code" || sg[0].ResourceID != "arcana.sample" {
		t.Errorf("first suggestion %+v", sg[0])
	}
	// The path is one the archive serves.
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + sg[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("suggested page: %d", resp.StatusCode)
	}

	// Paging: the second page starts where the first ended.
	all, total, err := s.SearchBook(ctx, "arcana.sample", "morse", 0, 25)
	if err != nil || total < 2 || len(all) != total {
		t.Fatalf("search book: %v total %d results %d", err, total, len(all))
	}
	p1, _, _ := s.SearchBook(ctx, "arcana.sample", "morse", 0, 1)
	p2, total2, _ := s.SearchBook(ctx, "arcana.sample", "morse", 1, 1)
	if len(p1) != 1 || len(p2) != 1 || p1[0].Path == p2[0].Path || total2 != total || p2[0].Path != all[1].Path {
		t.Errorf("paging: %+v %+v (all %+v)", p1, p2, all)
	}
	if _, _, err := s.SearchBook(ctx, "not.installed", "morse", 0, 5); err == nil {
		t.Error("an archive that is not open must be an error")
	}

	groups, err := s.SearchEach(ctx, "morse", 1)
	if err != nil || len(groups) != 1 || groups[0].Total != total || len(groups[0].Results) != 1 || groups[0].Book == "" {
		t.Errorf("search each: %v %+v", err, groups)
	}
	titles, err := s.SuggestAll(ctx, "International Morse code", 5, 8)
	if err != nil || len(titles) == 0 || titles[0].Title != "International Morse code" {
		t.Errorf("suggest all: %v %+v", err, titles)
	}
}

// fakeKiwix answers like kiwix-serve for two English archives, of which
// "nofts" has no full-text index: kiwix-serve then refuses any search that
// includes it.
func fakeKiwix(t *testing.T) *Server {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch r.URL.Path {
		case URLRoot + "/search":
			names := q["books.name"]
			for _, n := range names {
				if n == "nofts" {
					http.Error(w, "no full-text index", http.StatusBadRequest)
					return
				}
			}
			start, _ := strconv.Atoi(q.Get("start"))
			fmt.Fprintf(w, `<rss><channel><totalResults>3</totalResults>`)
			for i := start; i < 3 && i < start+2; i++ {
				fmt.Fprintf(w, `<item><title>Water %d</title><link>/kiwix/content/good/A/%d</link><description>clean <b>water</b></description></item>`, i, i)
			}
			fmt.Fprint(w, `</channel></rss>`)
		case URLRoot + "/suggest":
			switch q.Get("content") {
			case "good":
				fmt.Fprint(w, `[{"value":"Water purification","label":"x","kind":"path","path":"A/Water purification"},
					{"value":"Water","label":"x","kind":"path","path":"A/Water"},
					{"value":"water ","label":"containing 'water'...","kind":"pattern"}]`)
			case "nofts":
				fmt.Fprint(w, `[{"value":"Water pump","label":"x","kind":"path","path":"Water_pump"},
					{"value":"Water","label":"x","kind":"path","path":"Water"}]`)
			default:
				http.NotFound(w, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	u, _ := url.Parse(ts.URL)
	port, _ := strconv.Atoi(u.Port())
	s := New("", t.TempDir(), nil)
	s.books = map[string]Book{
		"res.good":  {ResourceID: "res.good", Name: "good", Title: "Good", Language: "en"},
		"res.nofts": {ResourceID: "res.nofts", Name: "nofts", Title: "No index", Language: "en"},
	}
	s.proc = &process{port: port}
	return s
}

// If kiwix-serve refuses a search across several archives because one of
// them cannot be searched, the others must still answer.
func TestSearchSkipsArchiveWithoutIndex(t *testing.T) {
	s := fakeKiwix(t)
	res, total, err := s.Search(context.Background(), "water", nil, 25)
	if err != nil || total != 3 || len(res) != 2 || res[0].ResourceID != "res.good" {
		t.Fatalf("search: %v total %d %+v", err, total, res)
	}
	groups, err := s.SearchEach(context.Background(), "water", 5)
	if err != nil || len(groups) != 2 {
		t.Fatalf("search each: %v %+v", err, groups)
	}
	if groups[0].ResourceID != "res.good" || groups[0].Total != 3 || groups[1].Error != ErrNotSearchable.Error() {
		t.Errorf("groups: %+v", groups)
	}
	if _, _, err := s.SearchBook(context.Background(), "res.nofts", "water", 0, 5); !errors.Is(err, ErrNotSearchable) {
		t.Errorf("search in an archive without index: %v", err)
	}
	p2, total, err := s.SearchBook(context.Background(), "res.good", "water", 2, 2)
	if err != nil || total != 3 || len(p2) != 1 || p2[0].Title != "Water 2" {
		t.Errorf("second page: %v %d %+v", err, total, p2)
	}
}

func TestSuggestAllRanksExactTitlesFirst(t *testing.T) {
	s := fakeKiwix(t)
	got, err := s.SuggestAll(context.Background(), "water", 5, 10)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, g := range got {
		titles = append(titles, g.Book+": "+g.Title)
	}
	want := []string{"Good: Water", "No index: Water", "No index: Water pump", "Good: Water purification"}
	if !reflect.DeepEqual(titles, want) {
		t.Errorf("got %q, want %q", titles, want)
	}
	// Paths are escaped for a URL and keep their directories.
	for _, g := range got {
		if g.Title == "Water purification" && g.Path != "/kiwix/content/good/A/Water%20purification" {
			t.Errorf("path %q", g.Path)
		}
		if strings.Contains(g.Path, " ") {
			t.Errorf("unescaped path %q", g.Path)
		}
	}
	if got, _ := s.SuggestAll(context.Background(), "water", 5, 1); len(got) != 1 {
		t.Errorf("limit not applied: %+v", got)
	}
}

// Typing "ray char" in a Wikipedia archive: kiwix-serve's order put
// category pages and long titles before the article itself.
func TestRankSuggestions(t *testing.T) {
	mk := func(titles ...string) []Suggestion {
		var out []Suggestion
		for _, ti := range titles {
			out = append(out, Suggestion{Title: ti, Path: "/" + ti})
		}
		return out
	}
	cases := []struct {
		term  string
		in    []string
		want  []string
		limit int
	}{
		{"ray char", []string{"Category:Ray Charles", "Charles, Ray", "Ray Charles discography", "Ray Charles (album)", "Ray Charles"},
			[]string{"Ray Charles", "Ray Charles (album)", "Ray Charles discography", "Charles, Ray", "Category:Ray Charles"}, 0},
		{"geor", []string{"Category:Albany, Georgia", "Category:George Martin", "Lake George", "Georgia on My Mind"},
			[]string{"Georgia on My Mind", "Lake George", "Category:Albany, Georgia", "Category:George Martin"}, 0},
		// Someone typing a namespace wants those pages.
		{"category:geo", []string{"George", "Category:George Martin"}, []string{"Category:George Martin", "George"}, 0},
		// A colon after a number or before a space is not a namespace.
		{"mission", []string{"Mission: Impossible", "10:15 Mission"}, []string{"Mission: Impossible", "10:15 Mission"}, 0},
		{"ray", []string{"Ray Charles", "Ray"}, []string{"Ray"}, 1},
	}
	for _, c := range cases {
		var got []string
		for _, s := range RankSuggestions(c.term, [][]Suggestion{mk(c.in...)}, c.limit) {
			got = append(got, s.Title)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %q, want %q", c.term, got, c.want)
		}
	}
}

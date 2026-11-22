package kiwix

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/KK4ODA/arcana/internal/buildinfo"
)

func TestBookName(t *testing.T) {
	cases := map[string]string{
		"/x/wikipedia_en_all_maxi_2026-08.zim": "wikipedia_en_all_maxi_2026-08",
		"C++ Notes.zim":                        "cplusplus_notes",
		"Split.zimaa":                          "split",
	}
	for in, want := range cases {
		if got := BookName(in); got != want {
			t.Errorf("BookName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeSnippet(t *testing.T) {
	in := `Ray &amp; <b>Georgia</b> <script>alert(1)</script><img src=x onerror=alert(1)> &lt;b&gt;fake`
	got := SanitizeSnippet(in)
	if !strings.Contains(got, "<b>Georgia</b>") {
		t.Errorf("highlight lost: %q", got)
	}
	if strings.Contains(got, "<script") || strings.Contains(got, "<img") {
		t.Errorf("markup survived: %q", got)
	}
	if !strings.Contains(got, "Ray &amp; ") {
		t.Errorf("entities not round-tripped: %q", got)
	}
	// Escaped tags in the source text stay inert text.
	if strings.Contains(got, "<b>fake") {
		t.Errorf("escaped tag became markup: %q", got)
	}
	if got := SanitizeSnippet("<b>unbalanced"); strings.Contains(got, "<b>") {
		t.Errorf("unbalanced tags must be dropped: %q", got)
	}
}

// kiwix-serve formats totalResults for display ("1,800" in 3.8.2), which
// once made every search with 1,000 or more matches fail.
func TestSearchFormattedTotal(t *testing.T) {
	var totalXML string // the <opensearch:totalResults> element, or ""
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != URLRoot+"/search" || r.URL.Query().Get("books.name") != "ham_en_all" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml; charset=utf-8")
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:opensearch="http://a9.com/-/spec/opensearch/1.1/" xmlns:atom="http://www.w3.org/2005/Atom">
  <channel>
    <title>Search: repeater</title>
    %s
    <opensearch:startIndex>1</opensearch:startIndex>
    <opensearch:itemsPerPage>25</opensearch:itemsPerPage>
    <item><title>Repeater offsets</title><link>/kiwix/content/ham_en_all/A/1</link>
      <description>Which <b>repeater</b> offset</description></item>
    <item><title>Linking repeaters</title><link>/kiwix/content/ham_en_all/A/2</link>
      <description>Two <b>repeaters</b></description></item>
  </channel>
</rss>`, totalXML)
	}))
	defer ts.Close()
	u, _ := url.Parse(ts.URL)
	port, _ := strconv.Atoi(u.Port())
	s := New("", t.TempDir(), nil)
	s.books = map[string]Book{"ham.se": {ResourceID: "ham.se", Name: "ham_en_all", Title: "Ham SE", Language: "en"}}
	s.proc = &process{port: port}

	cases := []struct {
		name, total string
		want        int
	}{
		{"comma", "1,800", 1800},
		{"period", "1.800", 1800},
		{"space", "1 800", 1800},
		{"no-break space", "1 800", 1800},
		{"narrow no-break space", "1 800", 1800},
		{"millions", "2,345,678", 2345678},
		{"plain", "42", 42},
		// An unreadable or missing count falls back to the items received.
		{"unreadable", "many", 2},
		{"missing", "", 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			totalXML = ""
			if c.total != "" {
				totalXML = "<opensearch:totalResults>" + c.total + "</opensearch:totalResults>"
			}
			res, total, err := s.Search(context.Background(), "repeater", nil, 25)
			if err != nil {
				t.Fatalf("search failed: %v", err)
			}
			if total != c.want {
				t.Errorf("total = %d, want %d", total, c.want)
			}
			if len(res) != 2 || res[0].ResourceID != "ham.se" || res[1].Title != "Linking repeaters" {
				t.Errorf("results: %+v", res)
			}
		})
	}
	if _, ok := parseCount("99999999999999999999999"); ok {
		t.Error("an out-of-range count must be reported as unreadable")
	}
}

func TestMapBooks(t *testing.T) {
	feed := `<feed><entry><id>urn:uuid:abc</id><title>Sample</title><language>eng</language>
		<link type="text/html" href="/kiwix/content/arcana-sample_en_2026-11"/></entry>
		<entry><id>urn:uuid:def</id><title>Odd</title><link type="text/html" href="/kiwix/content/renamed"/></entry></feed>`
	books, err := mapBooks([]byte(feed), []Archive{
		{ResourceID: "arcana.sample", Path: "/lib/content/arcana.sample/2026-11/arcana-sample_en_2026-11.zim", Language: "en"},
		{ResourceID: "x.odd", Path: "/lib/content/x.odd/1/Ünusual name.zim", UUID: "def"},
		{ResourceID: "x.missing", Path: "/lib/content/x.missing/1/gone.zim"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if b := books["arcana.sample"]; b.Name != "arcana-sample_en_2026-11" || b.ContentPath != "/kiwix/content/arcana-sample_en_2026-11/" {
		t.Errorf("by name: %+v", b)
	}
	if b := books["x.odd"]; b.Name != "renamed" {
		t.Errorf("UUID fallback: %+v", b)
	}
	if _, ok := books["x.missing"]; ok {
		t.Error("archive skipped by kiwix-serve must not be listed")
	}
}

// testBinary finds kiwix-serve fetched by scripts/fetch-kiwix.sh.
func testBinary(t *testing.T) string {
	if p := os.Getenv("ARCANA_KIWIX_SERVE"); p != "" {
		return p
	}
	name := "kiwix-serve"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join("..", "..", "third_party", "kiwix", buildinfo.Platform(), "kiwix", name)
	if _, err := os.Stat(p); err != nil {
		t.Skip("kiwix-serve not available; run scripts/fetch-kiwix.sh")
	}
	abs, _ := filepath.Abs(p)
	return abs
}

func TestServeAndSearchSample(t *testing.T) {
	bin := testBinary(t)
	sample, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "sample", "arcana-sample_en_2026-11.zim"))
	dir := t.TempDir()
	s := New(bin, dir, nil)
	defer s.Stop()
	if err := s.Apply(context.Background(), []Archive{{ResourceID: "arcana.sample", Path: sample, Language: "en"}}); err != nil {
		t.Fatal(err)
	}
	b, ok := s.Books()["arcana.sample"]
	if !ok {
		t.Fatalf("book not mapped: %+v", s.Books())
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + b.ContentPath + "morse_code")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("article status %d", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Error("proxy must strip the permissive CORS header")
	}
	res, total, err := s.Search(context.Background(), "morse", nil, 10)
	if err != nil || total == 0 || res[0].ResourceID != "arcana.sample" {
		t.Fatalf("search: %v %d %+v", err, total, res)
	}
	// Body matches are highlighted in the snippet.
	res, _, err = s.Search(context.Background(), "distress", nil, 10)
	if err != nil || len(res) == 0 || !strings.Contains(res[0].Snippet, "<b>distress</b>") {
		t.Errorf("snippet lacks highlight: %v %+v", err, res)
	}
	// Removing all archives stops the process.
	if err := s.Apply(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(); st.State != "idle" {
		t.Fatalf("state %s", st.State)
	}
}

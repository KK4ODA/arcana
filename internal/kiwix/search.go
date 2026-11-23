package kiwix

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

// Result is one full-text search hit.
type Result struct {
	ResourceID string `json:"resourceId"`
	Book       string `json:"book"`
	Title      string `json:"title"`
	// Snippet is HTML-escaped text in which only <b> highlighting remains.
	Snippet string `json:"snippet"`
	Path    string `json:"path"`
}

// Group is the first page of full-text results from one archive.
type Group struct {
	ResourceID string   `json:"resourceId"`
	Book       string   `json:"book"`
	Total      int      `json:"total"`
	Results    []Result `json:"results"`
	// Error is set when the archive could not be searched by full text,
	// for example because it has no full-text index.
	Error string `json:"error,omitempty"`
}

// Suggestion is an article whose title matches what was typed.
type Suggestion struct {
	ResourceID string `json:"resourceId"`
	Book       string `json:"book"`
	Title      string `json:"title"`
	Path       string `json:"path"`
}

// ErrNotSearchable is returned for an archive kiwix-serve cannot search by
// full text (most often because it was published without a full-text
// index). Its titles can still be searched with Suggest.
var ErrNotSearchable = errors.New("this archive has no full-text index; only its titles can be searched")

// maxParallel bounds the queries Arcana sends kiwix-serve at once, so a
// library with many archives does not queue dozens of searches behind each
// other inside kiwix-serve's small thread pool.
const maxParallel = 4

type rss struct {
	Channel struct {
		// Total is text: kiwix-serve formats it for display, with a
		// thousands separator ("1,800"). See parseCount.
		Total string `xml:"totalResults"`
		Items []struct {
			Title string `xml:"title"`
			Link  string `xml:"link"`
			// innerxml: kiwix-serve marks matches with <b> child
			// elements, which chardata would silently drop.
			Description struct {
				Inner string `xml:",innerxml"`
			} `xml:"description"`
		} `xml:"item"`
	} `xml:"channel"`
}

// query fetches one page of full-text results across the given archives,
// which kiwix-serve requires to share a language.
func (s *Server) query(ctx context.Context, target *url.URL, books []Book, pattern string, start, limit int) ([]Result, int, error) {
	byName := map[string]Book{}
	v := url.Values{}
	for _, b := range books {
		v.Add("books.name", b.Name)
		byName[b.Name] = b
	}
	v.Set("pattern", pattern)
	v.Set("format", "xml")
	v.Set("start", strconv.Itoa(start))
	v.Set("pageLength", strconv.Itoa(limit))
	u := target.String() + URLRoot + "/search?" + v.Encode()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// kiwix-serve answers with an error page for archives without a
		// full-text index.
		return nil, 0, ErrNotSearchable
	}
	var doc rss
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, 0, fmt.Errorf("reading search results: %w", err)
	}
	total, ok := parseCount(doc.Channel.Total)
	if !ok {
		// A count that cannot be read must not discard real results.
		total = start + len(doc.Channel.Items)
	}
	results := make([]Result, 0, len(doc.Channel.Items))
	for _, it := range doc.Channel.Items {
		name := strings.TrimPrefix(it.Link, URLRoot+"/content/")
		if i := strings.IndexByte(name, '/'); i >= 0 {
			name = name[:i]
		}
		b := byName[name]
		results = append(results, Result{
			ResourceID: b.ResourceID,
			Book:       b.Title,
			Title:      it.Title,
			Snippet:    SanitizeSnippet(it.Description.Inner),
			Path:       it.Link,
		})
	}
	return results, total, nil
}

// Search runs a full-text query. kiwix-serve only searches several archives
// at once when they share one language, so archives are grouped by
// language and the groups are queried in turn. If kiwix-serve refuses a
// group as a whole, its archives are searched one by one so that one
// archive it cannot search does not hide the others' results.
func (s *Server) Search(ctx context.Context, query string, only map[string]bool, limit int) ([]Result, int, error) {
	target, ok := s.target()
	if !ok {
		return nil, 0, errors.New("archive reader is not running")
	}
	if limit <= 0 || limit > 140 {
		limit = 25
	}
	groups := map[string][]Book{}
	for id, b := range s.Books() {
		if only != nil && !only[id] {
			continue
		}
		groups[b.Language] = append(groups[b.Language], b)
	}
	var results []Result
	total := 0
	var lastErr error
	langs := make([]string, 0, len(groups))
	for l := range groups {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	for _, lang := range langs {
		books := groups[lang]
		sort.Slice(books, func(i, j int) bool { return books[i].Name < books[j].Name })
		res, n, err := s.query(ctx, target, books, query, 0, limit)
		if errors.Is(err, ErrNotSearchable) && len(books) > 1 {
			for _, b := range books {
				r1, n1, err1 := s.query(ctx, target, []Book{b}, query, 0, limit)
				if err1 != nil {
					if !errors.Is(err1, ErrNotSearchable) {
						lastErr = err1
					}
					continue
				}
				results = append(results, r1...)
				total += n1
			}
			continue
		}
		if err != nil {
			if !errors.Is(err, ErrNotSearchable) {
				lastErr = err
			}
			continue
		}
		results = append(results, res...)
		total += n
	}
	if len(results) == 0 && lastErr != nil {
		return nil, 0, lastErr
	}
	return results, total, nil
}

// SearchBook returns one page of full-text results from a single archive,
// starting at result number start (0-based).
func (s *Server) SearchBook(ctx context.Context, resourceID, query string, start, limit int) ([]Result, int, error) {
	target, ok := s.target()
	if !ok {
		return nil, 0, errors.New("archive reader is not running")
	}
	b, ok := s.Books()[resourceID]
	if !ok {
		return nil, 0, fmt.Errorf("%s is not open in the archive reader", resourceID)
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if start < 0 {
		start = 0
	}
	return s.query(ctx, target, []Book{b}, query, start, limit)
}

// SearchEach searches every archive separately and returns the first
// perBook results of each, largest result count first. Searching archives
// separately keeps a small archive's matches visible next to a large
// encyclopedia's, and tells the reader how many matches each source has.
func (s *Server) SearchEach(ctx context.Context, query string, perBook int) ([]Group, error) {
	target, ok := s.target()
	if !ok {
		return nil, errors.New("archive reader is not running")
	}
	if perBook <= 0 || perBook > 25 {
		perBook = 5
	}
	books := s.Books()
	ids := make([]string, 0, len(books))
	for id := range books {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	groups := make([]Group, len(ids))
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup
	for i, id := range ids {
		b := books[id]
		groups[i] = Group{ResourceID: id, Book: b.Title}
		wg.Add(1)
		go func(g *Group, b Book) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				g.Error = ctx.Err().Error()
				return
			}
			res, n, err := s.query(ctx, target, []Book{b}, query, 0, perBook)
			if err != nil {
				g.Error = err.Error()
				return
			}
			g.Results, g.Total = res, n
		}(&groups[i], b)
	}
	wg.Wait()
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Total > groups[j].Total })
	return groups, nil
}

type suggestion struct {
	Value string `json:"value"`
	Kind  string `json:"kind"`
	Path  string `json:"path"`
}

// Suggest returns up to count articles in one archive whose titles match
// term, the way the search box of a Kiwix reader completes a title. It
// works for every archive, with or without a full-text index.
func (s *Server) Suggest(ctx context.Context, resourceID, term string, count int) ([]Suggestion, error) {
	target, ok := s.target()
	if !ok {
		return nil, errors.New("archive reader is not running")
	}
	b, ok := s.Books()[resourceID]
	if !ok {
		return nil, fmt.Errorf("%s is not open in the archive reader", resourceID)
	}
	if count <= 0 || count > 50 {
		count = 10
	}
	// kiwix-serve's own order favours many weak matches (in Wikipedia,
	// category pages) over the article someone is typing, so ask for more
	// and rank them here.
	found, err := s.suggest(ctx, target, b, term, min(50, count*3))
	if err != nil {
		return nil, err
	}
	return RankSuggestions(term, [][]Suggestion{found}, count), nil
}

func (s *Server) suggest(ctx context.Context, target *url.URL, b Book, term string, count int) ([]Suggestion, error) {
	if count <= 0 || count > 50 {
		count = 10
	}
	v := url.Values{}
	v.Set("content", b.Name)
	v.Set("term", term)
	v.Set("count", strconv.Itoa(count))
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, target.String()+URLRoot+"/suggest?"+v.Encode(), nil)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("title search failed (%s)", resp.Status)
	}
	var raw []suggestion
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("reading title matches: %w", err)
	}
	out := make([]Suggestion, 0, len(raw))
	for _, r := range raw {
		// "pattern" entries are kiwix-serve's offer to run a full-text
		// search; Arcana has its own.
		if r.Kind != "path" || r.Path == "" {
			continue
		}
		out = append(out, Suggestion{
			ResourceID: b.ResourceID,
			Book:       b.Title,
			Title:      html.UnescapeString(r.Value),
			Path:       URLRoot + "/content/" + url.PathEscape(b.Name) + "/" + escapePath(r.Path),
		})
	}
	return out, nil
}

// SuggestAll asks every archive for title matches and returns the best
// limit of them: titles equal to the term first, then titles starting with
// it, then the rest, taking each archive's best matches before its weaker
// ones.
func (s *Server) SuggestAll(ctx context.Context, term string, perBook, limit int) ([]Suggestion, error) {
	target, ok := s.target()
	if !ok {
		return nil, errors.New("archive reader is not running")
	}
	books := s.Books()
	ids := make([]string, 0, len(books))
	for id := range books {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	lists := make([][]Suggestion, len(ids))
	sem := make(chan struct{}, maxParallel)
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Add(1)
		go func(i int, b Book) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			lists[i], _ = s.suggest(ctx, target, b, term, min(50, perBook*3))
		}(i, books[id])
	}
	wg.Wait()
	return RankSuggestions(term, lists, limit), nil
}

// RankSuggestions merges per-archive title matches into the order a reader
// expects:
//  0. the title is exactly the term;
//  1. the title starts with the term (shorter titles first, so "Ray
//     Charles" comes before "Ray Charles discography");
//  2. a word in the title starts with each word of the term;
//  3. anything else kiwix-serve matched;
//  4. pages in a namespace such as "Category:" or "Template:".
//
// Ties keep each archive's own order and alternate between archives, so
// one large archive does not crowd out the others.
func RankSuggestions(term string, lists [][]Suggestion, limit int) []Suggestion {
	type ranked struct {
		s         Suggestion
		rank, pos int
		list, len int
	}
	t := strings.ToLower(strings.Join(strings.Fields(term), " "))
	words := strings.Fields(t)
	var all []ranked
	seen := map[string]bool{}
	for li, l := range lists {
		for pos, sg := range l {
			if seen[sg.Path] {
				continue
			}
			seen[sg.Path] = true
			title := strings.ToLower(sg.Title)
			rank := 3
			switch {
			case namespaced(sg.Title) && !strings.Contains(t, ":"):
				rank = 4
			case title == t:
				rank = 0
			case strings.HasPrefix(title, t):
				rank = 1
			case wordsPrefix(title, words):
				rank = 2
			}
			r := ranked{s: sg, rank: rank, pos: pos, list: li}
			if rank == 1 {
				r.len = len(title)
			}
			all = append(all, r)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.len != b.len {
			return a.len < b.len
		}
		if a.pos != b.pos {
			return a.pos < b.pos
		}
		return a.list < b.list
	})
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	out := make([]Suggestion, len(all))
	for i, r := range all {
		out[i] = r.s
	}
	return out
}

// wordsPrefix reports whether every query word begins some word of title.
func wordsPrefix(title string, words []string) bool {
	if len(words) == 0 {
		return false
	}
	tw := strings.FieldsFunc(title, func(r rune) bool {
		return r == ' ' || r == '(' || r == ')' || r == ',' || r == '-' || r == '_' || r == '/' || r == ':'
	})
	for _, w := range words {
		ok := false
		for _, x := range tw {
			if strings.HasPrefix(x, w) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// namespaced reports titles like "Category:Gospel music" or "Template:Infobox":
// a single word followed by a colon and no space, as MediaWiki namespaces
// are written.
func namespaced(title string) bool {
	i := strings.IndexByte(title, ':')
	if i <= 0 || i == len(title)-1 || title[i+1] == ' ' {
		return false
	}
	for _, r := range title[:i] {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// escapePath percent-encodes each segment of an archive path.
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, sg := range segs {
		segs[i] = url.PathEscape(sg)
	}
	return strings.Join(segs, "/")
}

// parseCount reads a result count that kiwix-serve may have formatted for
// display. The thousands separator depends on the locale ("1,800", "1.800",
// "1 800", or with a no-break or narrow no-break space), so every character
// other than an ASCII digit is dropped. It reports false when no number
// remains or the number is out of range.
func parseCount(s string) (int, bool) {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
	if digits == "" {
		return 0, false
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}
	return n, true
}

// SanitizeSnippet converts an XML search snippet into safe HTML: text is
// unescaped and re-escaped, <b> highlighting is kept, and every other tag is
// removed.
func SanitizeSnippet(inner string) string {
	s := strings.Map(func(r rune) rune {
		if r == '\x01' || r == '\x02' {
			return -1
		}
		return r
	}, inner)
	s = strings.ReplaceAll(s, "<b>", "\x01")
	s = strings.ReplaceAll(s, "</b>", "\x02")
	s = tagRe.ReplaceAllString(s, "")
	s = html.EscapeString(html.UnescapeString(s))
	if strings.Count(s, "\x01") != strings.Count(s, "\x02") {
		s = strings.NewReplacer("\x01", "", "\x02", "").Replace(s)
	}
	return strings.NewReplacer("\x01", "<b>", "\x02", "</b>").Replace(s)
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

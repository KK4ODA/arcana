package docs

import (
	"sort"
	"strings"

	"github.com/ledongthuc/pdf"
)

// Limits on what is read from a PDF's bookmarks. PDFs are untrusted: their
// outline and name trees can be cyclic or enormous.
const (
	maxOutlineItems = 20000
	maxNameTreeNode = 10000
	maxNamedDests   = 100000
	maxTreeDepth    = 32
)

// outlineHeadings returns, for each of the first n pages, the title of the
// bookmark (outline item) the page falls under: the last one, in page
// order, that points at this page or an earlier one. Pages before the first
// bookmark, and every page of a PDF without bookmarks, get "".
func outlineHeadings(r *pdf.Reader, n int) (out []string) {
	out = make([]string, n)
	defer func() {
		if recover() != nil {
			out = make([]string, n)
		}
	}()
	root := r.Trailer().Key("Root")
	first := root.Key("Outlines").Key("First")
	if first.IsNull() || n == 0 {
		return out
	}
	// The library does not expose object numbers, so pages are recognised
	// by their dictionaries, which list each page's own content streams.
	pageOf := map[string]int{}
	for i := 1; i <= n; i++ {
		if v := r.Page(i).V; !v.IsNull() {
			pageOf[v.String()] = i
		}
	}
	var dests map[string]pdf.Value
	named := func() map[string]pdf.Value {
		if dests == nil {
			dests = namedDests(root)
		}
		return dests
	}
	type mark struct {
		page  int
		title string
	}
	var marks []mark
	seen := 0
	var walk func(item pdf.Value, depth int)
	walk = func(item pdf.Value, depth int) {
		for ; !item.IsNull() && seen < maxOutlineItems && depth < maxTreeDepth; item = item.Key("Next") {
			seen++
			title := strings.Join(strings.Fields(item.Key("Title").Text()), " ")
			if p := destPage(item, named, pageOf); p >= 1 && p <= n && title != "" {
				marks = append(marks, mark{p, title})
			}
			walk(item.Key("First"), depth+1)
		}
	}
	walk(first, 0)
	// Document order breaks ties, so "3.1 Setup" wins over its chapter
	// "3 Operation" when both start on the same page.
	sort.SliceStable(marks, func(i, j int) bool { return marks[i].page < marks[j].page })
	cur, j := "", 0
	for p := 1; p <= n; p++ {
		for j < len(marks) && marks[j].page <= p {
			cur = marks[j].title
			j++
		}
		out[p-1] = cur
	}
	return out
}

// destPage finds the page an outline item goes to: its /Dest, or the /D of
// a GoTo action, through a named destination if need be.
func destPage(item pdf.Value, named func() map[string]pdf.Value, pageOf map[string]int) int {
	d := item.Key("Dest")
	if d.IsNull() {
		if a := item.Key("A"); a.Key("S").Name() == "GoTo" {
			d = a.Key("D")
		}
	}
	for range 4 {
		switch d.Kind() {
		case pdf.Name:
			d = named()[d.Name()]
		case pdf.String:
			d = named()[d.RawString()]
		case pdf.Dict:
			d = d.Key("D")
		case pdf.Array:
			if d.Len() == 0 {
				return 0
			}
			pg := d.Index(0)
			if pg.Kind() == pdf.Integer {
				// Some producers write a page index (from 0) where a
				// page reference belongs.
				return int(pg.Int64()) + 1
			}
			return pageOf[pg.String()]
		default:
			return 0
		}
	}
	return 0
}

// namedDests collects named destinations from the catalog's /Dests
// dictionary (PDF 1.1) and its /Names /Dests name tree.
func namedDests(root pdf.Value) map[string]pdf.Value {
	out := map[string]pdf.Value{}
	d := root.Key("Dests")
	for _, k := range d.Keys() {
		if len(out) >= maxNamedDests {
			break
		}
		out[k] = d.Key(k)
	}
	visited := 0
	var walk func(node pdf.Value, depth int)
	walk = func(node pdf.Value, depth int) {
		if node.IsNull() || depth > maxTreeDepth || visited >= maxNameTreeNode {
			return
		}
		visited++
		names := node.Key("Names")
		for i := 0; i+1 < names.Len() && len(out) < maxNamedDests; i += 2 {
			out[names.Index(i).RawString()] = names.Index(i + 1)
		}
		kids := node.Key("Kids")
		for i := 0; i < kids.Len(); i++ {
			walk(kids.Index(i), depth+1)
		}
	}
	walk(root.Key("Names").Key("Dests"), 0)
	return out
}

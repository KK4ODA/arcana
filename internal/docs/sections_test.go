package docs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bookmark is an outline item for pdfWithOutline. How says how it points at
// its page: "dest" (a /Dest array), "goto" (a GoTo action) or "named" (a
// named destination in the catalog's /Dests).
type bookmark struct {
	title string
	page  int
	how   string
}

// pdfWithOutline builds a PDF like pdfDoc, with bookmarks.
func pdfWithOutline(marks []bookmark, streams ...string) []byte {
	pageObj := func(p int) int { return 5 + 2*(p-1) }
	kids := make([]string, len(streams))
	for i := range streams {
		kids[i] = fmt.Sprintf("%d 0 R", pageObj(i+1))
	}
	outlineRoot := 5 + 2*len(streams)
	var named []string
	for i, m := range marks {
		if m.how == "named" {
			named = append(named, fmt.Sprintf("/m%d [%d 0 R /Fit]", i, pageObj(m.page)))
		}
	}
	objs := []string{
		fmt.Sprintf("<< /Type /Catalog /Pages 2 0 R /Outlines %d 0 R /Dests << %s >> >>", outlineRoot, strings.Join(named, " ")),
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(streams)),
		fontWithWidths,
		"<< /Title (Station Manual) >>",
	}
	for i, s := range streams {
		objs = append(objs,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents %d 0 R /Resources << /Font << /F1 3 0 R >> >> >>", 6+2*i),
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(s), s))
	}
	objs = append(objs, fmt.Sprintf("<< /Type /Outlines /First %d 0 R /Last %d 0 R /Count %d >>", outlineRoot+1, outlineRoot+len(marks), len(marks)))
	for i, m := range marks {
		item := fmt.Sprintf("/Title (%s) /Parent %d 0 R", m.title, outlineRoot)
		if i > 0 {
			item += fmt.Sprintf(" /Prev %d 0 R", outlineRoot+i)
		}
		if i < len(marks)-1 {
			item += fmt.Sprintf(" /Next %d 0 R", outlineRoot+i+2)
		}
		switch m.how {
		case "goto":
			item += fmt.Sprintf(" /A << /S /GoTo /D [%d 0 R /XYZ 0 792 0] >>", pageObj(m.page))
		case "named":
			item += fmt.Sprintf(" /Dest /m%d", i)
		default:
			item += fmt.Sprintf(" /Dest [%d 0 R /Fit]", pageObj(m.page))
		}
		objs = append(objs, "<< "+item+" >>")
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R /Info 4 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

func page(text string) string { return "BT /F1 10 Tf 72 720 Td (" + text + ") Tj ET" }

func TestPDFPagesGetBookmarkHeadings(t *testing.T) {
	ex := extractPDF(t, pdfWithOutline([]bookmark{
		{"1 Introduction", 1, "dest"},
		{"2 Antennas", 2, "goto"},
		{"2.1 Dipoles", 3, "named"},
		{"2.2 Verticals", 3, "dest"},
	}, page("About this manual."), page("Antenna basics."), page("A half wave dipole."), page("Dipole length table."), "q Q"))
	if !ex.Paged || len(ex.Sections) != 5 {
		t.Fatalf("paged=%v sections=%d", ex.Paged, len(ex.Sections))
	}
	want := []string{"1 Introduction", "2 Antennas", "2.2 Verticals", "2.2 Verticals", "2.2 Verticals"}
	for i, s := range ex.Sections {
		if s.Heading != want[i] {
			t.Errorf("page %d heading %q, want %q", i+1, s.Heading, want[i])
		}
	}
	if ex.Sections[2].Text != "A half wave dipole." || ex.Sections[4].Text != "" {
		t.Errorf("page texts %q", ex.Sections)
	}
	// A PDF without bookmarks still has one section per page.
	ex = extractPDF(t, pdfDoc(fontWithWidths, page("One."), page("Two.")))
	if len(ex.Sections) != 2 || ex.Sections[1].Heading != "" || ex.Text != "One.\n\nTwo." {
		t.Errorf("no outline: %+v %q", ex.Sections, ex.Text)
	}
}

func TestSplitSections(t *testing.T) {
	md := "# Station\nIntro.\n\n## Antenna\nDipole notes.\n```\n# not a heading\n```\n## Power\nBatteries."
	got := splitSections(md, "", true)
	var heads []string
	for _, s := range got {
		heads = append(heads, s.Heading)
	}
	if strings.Join(heads, "|") != "Station|Antenna|Power" || !strings.Contains(got[1].Text, "# not a heading") {
		t.Errorf("markdown sections: %+v", got)
	}
	// Long text breaks at blank lines once a section is long enough, and
	// every section keeps the heading it was given.
	para := strings.Repeat("word ", 200) // 1,000 bytes
	long := strings.Repeat(para+"\n\n", 10)
	got = splitSections(long, "Chapter 2", false)
	if len(got) < 3 || len(got) > 4 {
		t.Fatalf("%d sections from 10,000 bytes", len(got))
	}
	for _, s := range got {
		if s.Heading != "Chapter 2" || len(s.Text) > sectionChars*3/2+len(para) {
			t.Errorf("section %q… (%d bytes)", s.Heading, len(s.Text))
		}
	}
	// One enormous line still divides, at spaces and on rune boundaries.
	got = splitSections(strings.Repeat("αβγ ", 3000), "", false)
	if len(got) < 3 {
		t.Errorf("one long line: %d sections", len(got))
	}
	for _, s := range got {
		if !strings.HasPrefix(s.Text, "αβγ") {
			t.Errorf("section starts mid-word: %q", s.Text[:8])
		}
	}
}

func TestEPUBChaptersAreSections(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "e.epub")
	minimalEPUB(t, p)
	ex, err := Extract(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(ex.Sections) != 1 || ex.Sections[0].Heading != "Knots" || ex.Paged {
		t.Errorf("sections %+v", ex.Sections)
	}
}

func TestSearchShowsMatchingPages(t *testing.T) {
	dir := t.TempDir()
	ix, err := OpenIndex(filepath.Join(dir, "documents.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	manual := filepath.Join(dir, "manual.pdf")
	os.WriteFile(manual, pdfWithOutline([]bookmark{{"Antennas", 2, "dest"}},
		page("Read this first."), page("Cut the dipole to length."), page("A balun stops common mode current."),
		page("Dipole height above ground.")), 0o644)
	notes := filepath.Join(dir, "notes.md")
	os.WriteFile(notes, []byte("# Repeaters\nOffset and tone.\n\n# Simplex\nCalling frequencies."), 0o644)
	ctx := context.Background()
	if _, err := ix.Sync(ctx, []Doc{
		{Key: "user:manual.pdf", Source: "user", Name: "manual.pdf", Path: manual, URL: UserDocURL("manual.pdf"), Size: 1, ModTime: 1},
		{Key: "user:notes.md", Source: "user", Name: "notes.md", Path: notes, URL: UserDocURL("notes.md"), Size: 1, ModTime: 1},
	}, nil); err != nil {
		t.Fatal(err)
	}
	pages := func(h Hit) string {
		var s []string
		for _, p := range h.Pages {
			s = append(s, fmt.Sprintf("%d:%s", p.N, p.Heading))
		}
		return strings.Join(s, ",")
	}

	hits, n, err := ix.Search(ctx, "dipole", 10)
	if err != nil || n != 1 || !hits[0].Paged || hits[0].PageMatches != 2 || pages(hits[0]) != "2:Antennas,4:Antennas" && pages(hits[0]) != "4:Antennas,2:Antennas" {
		t.Fatalf("dipole: %d %+v %v", n, hits, err)
	}
	if !strings.Contains(hits[0].Pages[0].Snippet, "<b>") || hits[0].Snippet != hits[0].Pages[0].Snippet {
		t.Errorf("snippets: %+v", hits[0])
	}
	// The words are on different pages: the document matches as a whole,
	// and the pages holding either word are shown.
	hits, n, _ = ix.Search(ctx, "balun height", 10)
	if n != 1 || hits[0].PageMatches != 2 || !strings.Contains(pages(hits[0]), "3:") || !strings.Contains(pages(hits[0]), "4:") {
		t.Fatalf("spread over pages: %d %+v", n, hits)
	}
	// Only the title matches: no pages, and the snippet is how it begins.
	hits, n, _ = ix.Search(ctx, "station manual", 10)
	if n != 1 || len(hits[0].Pages) != 0 || !strings.HasPrefix(hits[0].Snippet, "Read this first.") {
		t.Fatalf("title only: %d %+v", n, hits)
	}
	// Markdown sections are numbered and named by their headings.
	hits, _, _ = ix.Search(ctx, "calling", 10)
	if len(hits) != 1 || hits[0].Paged || pages(hits[0]) != "2:Simplex" {
		t.Fatalf("markdown: %+v", hits)
	}

	d, err := ix.Sections(ctx, "user:manual.pdf", 1<<20)
	if err != nil || !d.Paged || len(d.Sections) != 4 || d.Sections[1].N != 2 || d.Sections[1].Heading != "Antennas" || d.Title != "Station Manual" {
		t.Fatalf("sections: %+v %v", d, err)
	}
	if d, _ := ix.Sections(ctx, "user:manual.pdf", 30); !d.Truncated || len(d.Sections) != 2 {
		t.Errorf("truncated sections: %+v", d)
	}
	_, text, _, err := ix.Text(ctx, "user:manual.pdf", 1<<20)
	if err != nil || !strings.Contains(text, "Read this first.\n\nCut the dipole") {
		t.Errorf("text %q %v", text, err)
	}
	if _, err := ix.Sections(ctx, "user:missing.pdf", 100); err == nil {
		t.Error("a missing document should be an error")
	}

	// Re-indexing a changed document replaces its pages; removing it
	// removes them.
	os.WriteFile(manual, pdfDoc(fontWithWidths, page("Only a vertical now.")), 0o644)
	if res, _ := ix.Sync(ctx, []Doc{
		{Key: "user:manual.pdf", Source: "user", Name: "manual.pdf", Path: manual, URL: UserDocURL("manual.pdf"), Size: 2, ModTime: 2},
	}, nil); res.Indexed != 1 || res.Removed != 1 {
		t.Fatalf("resync: %+v", res)
	}
	if _, n, _ := ix.Search(ctx, "dipole", 10); n != 0 {
		t.Error("old pages still match")
	}
	if hits, _, _ := ix.Search(ctx, "vertical", 10); len(hits) != 1 || pages(hits[0]) != "1:" {
		t.Errorf("new pages: %+v", hits)
	}
	var rows int
	ix.db.QueryRow("SELECT count(*) FROM pages_fts").Scan(&rows)
	if rows != 1 {
		t.Errorf("%d page rows left, want 1", rows)
	}
}

package docs

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimalPDF builds a valid one-page PDF containing text, with a correct
// cross-reference table.
func minimalPDF(text string) []byte {
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>",
		"", // content stream, filled below
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		"<< /Title (Field Guide) >>",
	}
	stream := fmt.Sprintf("BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	objs[3] = fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream)
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
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R /Info 6 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

func minimalEPUB(t *testing.T, path string) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name, body string) {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<?xml version="1.0"?><container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`)
	add("OEBPS/content.opf", `<?xml version="1.0"?><package><metadata><title>Rope Work</title></metadata>
		<manifest><item id="c1" href="ch1.xhtml"/><item id="c2" href="../../../etc/passwd"/></manifest>
		<spine><itemref idref="c1"/><itemref idref="c2"/></spine></package>`)
	add("OEBPS/ch1.xhtml", `<html><head><style>.x{}</style></head><body><h1>Knots</h1><p>The bowline forms a fixed loop.</p><script>alert(1)</script></body></html>`)
	zw.Close()
	os.WriteFile(path, buf.Bytes(), 0o644)
}

func TestExtractFormats(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		"a.txt":  []byte("Boil water for one minute."),
		"b.md":   []byte("# Solar Basics\n\nPanels charge batteries."),
		"c.html": []byte("<html><title>Radio</title><body><script>var hidden=1</script><p>Antenna <b>dipole</b> length</p></body></html>"),
		"d.pdf":  minimalPDF("Purify water with bleach"),
	}
	for n, b := range files {
		os.WriteFile(filepath.Join(dir, n), b, 0o644)
	}
	minimalEPUB(t, filepath.Join(dir, "e.epub"))
	ctx := context.Background()
	cases := map[string][2]string{
		"a.txt":  {"a", "Boil water"},
		"b.md":   {"Solar Basics", "Panels charge"},
		"c.html": {"Radio", "dipole"},
		"d.pdf":  {"Field Guide", "bleach"},
		"e.epub": {"Rope Work", "bowline"},
	}
	for name, want := range cases {
		ex, err := Extract(ctx, filepath.Join(dir, name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if ex.Title != want[0] || !strings.Contains(ex.Text, want[1]) {
			t.Errorf("%s: title %q text %q", name, ex.Title, ex.Text)
		}
		if strings.Contains(ex.Text, "hidden") || strings.Contains(ex.Text, "alert") {
			t.Errorf("%s: script text leaked into index", name)
		}
	}
}

func TestMalformedDocumentsDoNotCrash(t *testing.T) {
	dir := t.TempDir()
	bad := map[string][]byte{
		"t.pdf":  minimalPDF("x")[:120],
		"g.pdf":  []byte("%PDF-1.4 garbage \x00\xff"),
		"z.epub": []byte("PK\x03\x04 not really"),
	}
	for n, b := range bad {
		p := filepath.Join(dir, n)
		os.WriteFile(p, b, 0o644)
		if _, err := Extract(context.Background(), p); err == nil {
			t.Errorf("%s: expected an error", n)
		}
	}
}

func TestIndexSyncAndSearch(t *testing.T) {
	dir := t.TempDir()
	ix, err := OpenIndex(filepath.Join(dir, "documents.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	p1 := filepath.Join(dir, "water.txt")
	os.WriteFile(p1, []byte("Chlorine & iodine tablets disinfect water."), 0o644)
	p2 := filepath.Join(dir, "solar.md")
	os.WriteFile(p2, []byte("# Solar\nCharge controllers protect batteries."), 0o644)
	docs := []Doc{
		{Key: "user:water.txt", Source: "user", Name: "water.txt", Path: p1, URL: UserDocURL("water.txt"), Size: 1, ModTime: 1},
		{Key: "res:x.y/solar.md", Source: "resource", ResourceID: "x.y", Name: "solar.md", Path: p2, URL: FileURL("x.y", "solar.md"), Size: 1, ModTime: 1},
	}
	res, err := ix.Sync(context.Background(), docs, nil)
	if err != nil || res.Indexed != 2 {
		t.Fatalf("sync: %+v %v", res, err)
	}
	hits, total, err := ix.Search(context.Background(), "iodine", 10)
	if err != nil || total != 1 || hits[0].Key != "user:water.txt" {
		t.Fatalf("search: %+v %d %v", hits, total, err)
	}
	if !strings.Contains(hits[0].Snippet, "<b>iodine</b>") || !strings.Contains(hits[0].Snippet, "&amp;") {
		t.Errorf("snippet: %q", hits[0].Snippet)
	}
	// Searching one source: a resource's documents, or the person's own.
	for _, c := range []struct {
		query, in string
		want      int
	}{
		{"iodine", UserDocuments, 1}, {"iodine", "x.y", 0}, {"batteries", "x.y", 1},
		{"batteries", UserDocuments, 0}, {"batteries", "other", 0}, {"batteries", "", 1},
		// Shared: installed resources' documents, never the person's own.
		{"iodine", SharedDocuments, 0}, {"batteries", SharedDocuments, 1},
	} {
		if _, n, err := ix.SearchIn(context.Background(), c.query, c.in, 0, 10); err != nil || n != c.want {
			t.Errorf("%q in %q: %d matches (%v), want %d", c.query, c.in, n, err, c.want)
		}
	}
	if hits, n, _ := ix.SearchIn(context.Background(), "batteries", "", 1, 10); n != 1 || len(hits) != 0 {
		t.Errorf("offset past the end: %d %+v", n, hits)
	}
	// Unchanged documents are not re-extracted; removed ones are dropped.
	res, _ = ix.Sync(context.Background(), docs[1:], nil)
	if res.Indexed != 0 || res.Removed != 1 {
		t.Fatalf("resync: %+v", res)
	}
	if _, n, _ := ix.Search(context.Background(), "iodine", 10); n != 0 {
		t.Fatal("removed document still searchable")
	}
}

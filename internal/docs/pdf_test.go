package docs

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ledongthuc/pdf"
	_ "modernc.org/sqlite"
)

func extractPDF(t *testing.T, data []byte) Extracted {
	t.Helper()
	p := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	ex, err := Extract(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return ex
}

// Words drawn as separately positioned pieces, and lines moved with Td,
// were run together ("97.15Station", "structures.A"), so a search for
// "station" missed the heading.
func TestPDFSeparatesPositionedWordsAndLines(t *testing.T) {
	ex := extractPDF(t, pdfDoc(fontWithWidths,
		"BT /F1 10 Tf 72 720 Td (97.15) Tj 30 0 Td (Station antenna structures.) Tj 0 -12 Td (A station may) Tj ET"))
	if !strings.Contains(ex.Text, "97.15 Station antenna structures.\nA station may") {
		t.Fatalf("text %q", ex.Text)
	}
	if ex.Problem != "" {
		t.Errorf("problem %q", ex.Problem)
	}
}

// TJ arrays carry word spacing as position adjustments rather than space
// characters; kerning adjustments are much smaller and stay inside words.
func TestPDFWordSpacingFromTJ(t *testing.T) {
	ex := extractPDF(t, pdfDoc(fontWithWidths,
		"BT /F1 10 Tf 72 720 Td [(Dip) 20 (ole) -250 (antenna) -250 (length)] TJ ET"))
	if !strings.Contains(ex.Text, "Dipole antenna length") {
		t.Fatalf("text %q", ex.Text)
	}
}

func TestPDFRejoinsHyphenatedLines(t *testing.T) {
	ex := extractPDF(t, pdfDoc(fontWithWidths, "BT /F1 10 Tf 72 720 Td "+
		"(The control op-) Tj 0 -12 Td (erator of a station uses a high-) Tj 0 -12 Td "+
		"(frequency signal. Any operator may use high or low frequency bands.) Tj ET"))
	for _, want := range []string{"control operator of a station", "high-frequency signal"} {
		if !strings.Contains(ex.Text, want) {
			t.Errorf("missing %q in %q", want, ex.Text)
		}
	}
}

// A heading set with character spacing stays one word per word.
func TestPDFLetterSpacedHeading(t *testing.T) {
	ex := extractPDF(t, pdfDoc(fontWithWidths, "BT /F1 10 Tf 3 Tc 72 720 Td (STATION ANTENNA) Tj ET"))
	if !strings.Contains(ex.Text, "STATION ANTENNA") {
		t.Fatalf("text %q", ex.Text)
	}
}

func TestPDFScannedPagesAreReported(t *testing.T) {
	scan := "q 612 0 0 792 0 0 cm Q" // a page with no text, like a scanned image
	ex := extractPDF(t, pdfDoc(fontWithWidths, scan, scan, scan))
	if !strings.Contains(ex.Problem, "no text that search can read") || !strings.Contains(ex.Problem, "OCR") {
		t.Errorf("all pages scanned: problem %q", ex.Problem)
	}
	text := "BT /F1 10 Tf 72 720 Td (Field expedient antennas are built from wire.) Tj ET"
	ex = extractPDF(t, pdfDoc(fontWithWidths, text, scan, scan))
	if !strings.HasPrefix(ex.Problem, "2 of 3 pages have no text") {
		t.Errorf("two of three scanned: problem %q", ex.Problem)
	}
	if ex = extractPDF(t, pdfDoc(fontWithWidths, text, text, scan)); ex.Problem != "" {
		t.Errorf("one blank page of three is normal: problem %q", ex.Problem)
	}
}

func TestLayoutText(t *testing.T) {
	g := func(s string, x, y, w float64) pdf.Text { return pdf.Text{S: s, X: x, Y: y, W: w, FontSize: 10} }
	cases := []struct {
		name   string
		glyphs []pdf.Text
		want   string
	}{
		{"superscript stays on the line", []pdf.Text{g("m", 0, 0, 5), g("2", 5, 3, 3), g("x", 8, 0, 5)}, "m2x"},
		{"space glyph, no double space", []pdf.Text{g("a", 0, 0, 5), g(" ", 5, 0, 2.5), g("b", 10, 0, 5)}, "a b"},
		{"line feed after TJ is ignored", []pdf.Text{g("a", 0, 0, 5), g("\n", 5, 0, 0), g("b", 5, 0, 5)}, "ab"},
		{"new column at the same height", []pdf.Text{g("a", 300, 0, 5), g("b", 0, 0, 5)}, "a b"},
		{"paragraph gap", []pdf.Text{g("a", 0, 100, 5), g("b", 0, 50, 5)}, "a\n\nb"},
		{"unknown widths add no spaces", []pdf.Text{g("a", 0, 0, 0), g("b", 30, 0, 0)}, "ab"},
	}
	for _, c := range cases {
		if got := layoutText(c.glyphs); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if widthsKnown([]pdf.Text{g("a", 0, 0, 0), g("b", 5, 0, 0), g("c", 10, 0, 5)}) {
		t.Error("two of three glyphs without width: should fall back")
	}
}

func TestDehyphenate(t *testing.T) {
	cases := []struct{ in, want string }{
		{"op-\nerator", "operator"},
		// Every occurrence is broken: the halves are not evidence.
		{"op-\nerator and op-\nerator", "operator and operator"},
		{"high-\nfrequency; high and frequency", "high-frequency; high and frequency"},
		{"high-\nfrequency; highfrequency", "highfrequency; highfrequency"},
		{"Smith-\nJones", "Smith-\nJones"},
		{"pages 10-\n12", "pages 10-\n12"},
		{"soft­\nhyphen", "softhyphen"},
		{"no breaks here", "no breaks here"},
	}
	for _, c := range cases {
		if got := dehyphenate(c.in); got != c.want {
			t.Errorf("dehyphenate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Documents indexed by an older extractor are extracted again, and the
// reason a document cannot be searched is kept.
func TestIndexReextractsOldVersionsAndKeepsProblems(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "documents.db")
	// An index as earlier versions made it: no ver column.
	old, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE docs (key TEXT PRIMARY KEY, source TEXT NOT NULL, resource_id TEXT NOT NULL,
		name TEXT NOT NULL, url TEXT NOT NULL, title TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL,
		error TEXT NOT NULL DEFAULT '');
	CREATE VIRTUAL TABLE docs_fts USING fts5(key UNINDEXED, title, body, tokenize = 'unicode61 remove_diacritics 2');
	INSERT INTO docs VALUES ('user:rules.pdf', 'user', '', 'rules.pdf', '/userdocs/rules.pdf', 'Rules', 1, 1, '');
	INSERT INTO docs_fts VALUES ('user:rules.pdf', 'Rules', '97.15Station antenna structures');`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	rules := filepath.Join(dir, "rules.pdf")
	os.WriteFile(rules, pdfDoc(fontWithWidths, "BT /F1 10 Tf 72 720 Td (97.15) Tj 30 0 Td (Station antenna structures.) Tj ET"), 0o644)
	scan := filepath.Join(dir, "scan.pdf")
	os.WriteFile(scan, pdfDoc(fontWithWidths, "q Q", "q Q"), 0o644)
	docs := []Doc{
		{Key: "user:rules.pdf", Source: "user", Name: "rules.pdf", Path: rules, URL: UserDocURL("rules.pdf"), Size: 1, ModTime: 1},
		{Key: "user:scan.pdf", Source: "user", Name: "scan.pdf", Path: scan, URL: UserDocURL("scan.pdf"), Size: 1, ModTime: 1},
	}

	ix, err := OpenIndex(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Close()
	ctx := context.Background()
	if _, n, _ := ix.Search(ctx, "station", 10); n != 0 {
		t.Fatal("the old row should not match: its text was run together")
	}
	res, err := ix.Sync(ctx, docs, nil)
	if err != nil || res.Indexed != 2 || len(res.Failed) != 0 {
		t.Fatalf("sync: %+v %v", res, err)
	}
	if _, n, _ := ix.Search(ctx, "station", 10); n != 1 {
		t.Error("the unchanged file was not extracted again")
	}
	if res, _ = ix.Sync(ctx, docs, nil); res.Indexed != 0 {
		t.Errorf("current rows were extracted again: %+v", res)
	}
	problems, err := ix.Problems(ctx)
	if err != nil || len(problems) != 1 || !strings.Contains(problems["user:scan.pdf"], "scanned") {
		t.Fatalf("problems: %v %v", problems, err)
	}
	if ix.Problem(ctx, "user:scan.pdf") == "" || ix.Problem(ctx, "user:rules.pdf") != "" {
		t.Error("Problem disagrees with Problems")
	}
}

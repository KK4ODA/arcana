// Package docs extracts text from documents (PDF, EPUB, HTML, Markdown,
// plain text) and maintains a full-text index over installed non-ZIM
// resources and user documents. ZIM archives carry their own indexes and are
// searched through kiwix-serve; this package covers everything else, so that
// one search box finds both.
//
// Documents are untrusted input. Extraction runs with size limits, a time
// limit and panic recovery, and never executes anything.
package docs

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
)

// MaxText is the most text kept per document for indexing.
const MaxText = 4 << 20

// ErrUnsupported is returned for formats without an extractor.
var ErrUnsupported = errors.New("unsupported document format")

// Extracted is the text and title of a document.
type Extracted struct {
	Title string
	Text  string
	// Problem explains, for the reader, why search cannot see some or all
	// of a document that was otherwise read without error. For example, a
	// PDF made of scanned page images has no text to search.
	Problem string
}

// ExtractorVersion is stored with each indexed document. Raising it makes
// the next index sync extract every document again, so improvements to
// extraction reach documents that have not changed.
//
// 2: PDF text is rebuilt from glyph positions, hyphenated line breaks are
// rejoined, and scanned PDFs are reported.
const ExtractorVersion = 2

// Time allowed to read one document: extractBase, plus perPDFPage for each
// page of a PDF (at most maxPDFPages), because rebuilding a page's text from
// its glyph positions takes tens of milliseconds on slow hardware. A caller's
// own deadline replaces this budget.
const (
	extractBase = 60 * time.Second
	perPDFPage  = 250 * time.Millisecond
	maxPDFPages = 5000
)

// Supported reports whether a file name has an extractor.
func Supported(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt", ".text", ".md", ".markdown", ".html", ".htm", ".xhtml", ".epub", ".pdf", ".csv", ".gpx", ".json":
		return true
	}
	return false
}

// Extract reads a document. It recovers from panics in parsers (malformed
// PDFs) and gives up after the context deadline or, without one, after a
// budget that grows with a PDF's page count.
func Extract(ctx context.Context, p string) (ex Extracted, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("document could not be parsed: %v", r)
		}
	}()
	var budget *time.Timer
	var expired <-chan time.Time
	if _, ok := ctx.Deadline(); !ok {
		budget = time.NewTimer(extractBase)
		defer budget.Stop()
		expired = budget.C
	}
	// Cancelled on return, so a PDF that ran out of time stops at the next
	// page instead of running on in the background.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		ex  Extracted
		err error
	}
	ch := make(chan result, 1)
	pages := make(chan int, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{err: fmt.Errorf("document could not be parsed: %v", r)}
			}
		}()
		e, err := extract(ctx, p, func(n int) {
			select {
			case pages <- n:
			default:
			}
		})
		ch <- result{e, err}
	}()
	for {
		select {
		case r := <-ch:
			r.ex.Text = clip(r.ex.Text, MaxText)
			r.ex.Title = clip(strings.TrimSpace(r.ex.Title), 300)
			if r.ex.Title == "" {
				r.ex.Title = strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
			}
			return r.ex, r.err
		case n := <-pages:
			if budget != nil {
				budget.Reset(extractBase + time.Duration(min(n, maxPDFPages))*perPDFPage)
			}
		case <-expired:
			return Extracted{}, fmt.Errorf("document took too long to read: %w", context.DeadlineExceeded)
		case <-ctx.Done():
			return Extracted{}, fmt.Errorf("document took too long to read: %w", ctx.Err())
		}
	}
}

// extract reads one document. PDFs report their page count through pages
// (once) and stop between pages when ctx ends.
func extract(ctx context.Context, p string, pages func(int)) (Extracted, error) {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".txt", ".text", ".csv", ".json", ".gpx":
		b, err := readLimited(p, MaxText)
		return Extracted{Text: toUTF8(b)}, err
	case ".md", ".markdown":
		b, err := readLimited(p, MaxText)
		text := toUTF8(b)
		return Extracted{Title: markdownTitle(text), Text: text}, err
	case ".html", ".htm", ".xhtml":
		f, err := os.Open(p)
		if err != nil {
			return Extracted{}, err
		}
		defer f.Close()
		return htmlText(io.LimitReader(f, 32<<20))
	case ".epub":
		return epubText(p)
	case ".pdf":
		return pdfText(ctx, p, pages)
	}
	return Extracted{}, ErrUnsupported
}

func readLimited(p string, n int64) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, n))
}

func toUTF8(b []byte) string {
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	if utf8.Valid(b) {
		return string(b)
	}
	// Treat invalid input as Latin-1, the most common legacy encoding in
	// old plain-text documents.
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return string(r)
}

func markdownTitle(s string) string {
	for _, line := range strings.SplitN(s, "\n", 30) {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return ""
}

// htmlText returns visible text, skipping scripts, styles and markup.
func htmlText(r io.Reader) (Extracted, error) {
	z := html.NewTokenizer(r)
	var b strings.Builder
	var title string
	skip := 0
	inTitle := false
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			if errors.Is(z.Err(), io.EOF) {
				return Extracted{Title: title, Text: collapse(b.String())}, nil
			}
			return Extracted{Title: title, Text: collapse(b.String())}, z.Err()
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "script", "style", "noscript", "template", "svg":
				if tt == html.StartTagToken {
					skip++
				}
			case "title":
				inTitle = tt == html.StartTagToken
			case "p", "div", "br", "li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "section", "article", "td":
				b.WriteByte('\n')
			}
		case html.EndTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "script", "style", "noscript", "template", "svg":
				if skip > 0 {
					skip--
				}
			case "title":
				inTitle = false
			}
		case html.TextToken:
			if skip > 0 {
				continue
			}
			t := string(z.Text())
			if inTitle {
				title += t
				continue
			}
			b.WriteString(t)
			b.WriteByte(' ')
			if b.Len() > MaxText {
				return Extracted{Title: title, Text: collapse(b.String())}, nil
			}
		}
	}
}

func collapse(s string) string {
	var out strings.Builder
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	return out.String()
}

// epubText reads the spine documents of an EPUB (a ZIP of XHTML files).
// Entry names come from the archive and are only used to look up entries in
// it, never as file-system paths.
func epubText(p string) (Extracted, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return Extracted{}, err
	}
	defer zr.Close()
	files := map[string]*zip.File{}
	for _, f := range zr.File {
		files[f.Name] = f
	}
	readEntry := func(name string, limit int64) ([]byte, error) {
		f, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("missing %s", name)
		}
		if f.UncompressedSize64 > 64<<20 {
			return nil, errors.New("entry too large")
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return io.ReadAll(io.LimitReader(rc, limit))
	}
	var container struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	data, err := readEntry("META-INF/container.xml", 1<<20)
	if err != nil {
		return Extracted{}, err
	}
	if err := xml.Unmarshal(data, &container); err != nil || len(container.Rootfiles) == 0 {
		return Extracted{}, errors.New("invalid EPUB container")
	}
	opfPath := container.Rootfiles[0].FullPath
	var opf struct {
		Title    string `xml:"metadata>title"`
		Manifest []struct {
			ID   string `xml:"id,attr"`
			Href string `xml:"href,attr"`
		} `xml:"manifest>item"`
		Spine []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"spine>itemref"`
	}
	data, err = readEntry(opfPath, 4<<20)
	if err != nil {
		return Extracted{}, err
	}
	if err := xml.Unmarshal(data, &opf); err != nil {
		return Extracted{}, fmt.Errorf("invalid EPUB package: %w", err)
	}
	hrefs := map[string]string{}
	for _, it := range opf.Manifest {
		hrefs[it.ID] = it.Href
	}
	base := path.Dir(opfPath)
	var b strings.Builder
	for _, ref := range opf.Spine {
		href, ok := hrefs[ref.IDRef]
		if !ok {
			continue
		}
		name := path.Clean(path.Join(base, href))
		chunk, err := readEntry(name, 16<<20)
		if err != nil {
			continue
		}
		ex, _ := htmlText(bytes.NewReader(chunk))
		b.WriteString(ex.Text)
		if b.Len() > MaxText {
			break
		}
	}
	return Extracted{Title: strings.TrimSpace(opf.Title), Text: b.String()}, nil
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// HTMLText extracts the title and readable text of an HTML page (scripts,
// styles and markup removed; block elements become line breaks).
func HTMLText(r io.Reader) (title, text string, err error) {
	ex, err := htmlText(r)
	return strings.TrimSpace(ex.Title), ex.Text, err
}

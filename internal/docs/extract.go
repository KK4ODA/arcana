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

	"github.com/ledongthuc/pdf"
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
}

// Supported reports whether a file name has an extractor.
func Supported(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".txt", ".text", ".md", ".markdown", ".html", ".htm", ".xhtml", ".epub", ".pdf", ".csv", ".gpx", ".json":
		return true
	}
	return false
}

// Extract reads a document. It recovers from panics in parsers (malformed
// PDFs) and gives up after the context deadline.
func Extract(ctx context.Context, p string) (ex Extracted, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("document could not be parsed: %v", r)
		}
	}()
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}
	type result struct {
		ex  Extracted
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{err: fmt.Errorf("document could not be parsed: %v", r)}
			}
		}()
		e, err := extract(p)
		ch <- result{e, err}
	}()
	select {
	case r := <-ch:
		r.ex.Text = clip(r.ex.Text, MaxText)
		r.ex.Title = clip(strings.TrimSpace(r.ex.Title), 300)
		if r.ex.Title == "" {
			r.ex.Title = strings.TrimSuffix(filepath.Base(p), filepath.Ext(p))
		}
		return r.ex, r.err
	case <-ctx.Done():
		return Extracted{}, fmt.Errorf("document took too long to read: %w", ctx.Err())
	}
}

func extract(p string) (Extracted, error) {
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
		return pdfText(p)
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

func pdfText(p string) (Extracted, error) {
	f, r, err := pdf.Open(p)
	if err != nil {
		return Extracted{}, err
	}
	defer f.Close()
	var b strings.Builder
	n := r.NumPage()
	for i := 1; i <= n && i <= 5000; i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}
		b.WriteString(text)
		b.WriteByte('\n')
		if b.Len() > MaxText {
			break
		}
	}
	title := ""
	if info := r.Trailer().Key("Info"); !info.IsNull() {
		title = info.Key("Title").Text()
	}
	return Extracted{Title: title, Text: b.String()}, nil
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

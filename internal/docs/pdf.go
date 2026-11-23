package docs

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/ledongthuc/pdf"
)

// minPageChars is the fewest visible characters a PDF page needs to count as
// having text. A scanned page has none; a page of prose has thousands.
const minPageChars = 10

// Spacing thresholds, in units of the font size.
const (
	// wordGap: a horizontal gap wider than this between two glyphs on a line
	// separates words. Kerning moves glyphs by a few hundredths of an em; a
	// word space is about a quarter of one.
	wordGap = 0.15
	// lineStep: a vertical move larger than this starts a new line.
	// Superscripts and subscripts move less.
	lineStep = 0.5
	// paragraphStep: a vertical move larger than this also leaves a blank
	// line, so the document view shows a paragraph break.
	paragraphStep = 2.0
	// backtrack: moving left by more than this on the same line starts a new
	// piece of text (another column at the same height, or text drawn twice).
	backtrack = 1.0
)

func pdfText(ctx context.Context, p string, pages func(int)) (Extracted, error) {
	f, r, err := pdf.Open(p)
	if err != nil {
		return Extracted{}, err
	}
	defer f.Close()
	n := r.NumPage()
	if pages != nil {
		pages(n)
	}
	var b strings.Builder
	read, empty := 0, 0
	for i := 1; i <= n && i <= maxPDFPages; i++ {
		if err := ctx.Err(); err != nil {
			return Extracted{}, err // Extract has already given up on this document.
		}
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text := pageText(page)
		read++
		if visibleChars(text) < minPageChars {
			empty++
		}
		b.WriteString(text)
		b.WriteString("\n\n")
		if b.Len() > MaxText {
			break
		}
	}
	title := ""
	if info := r.Trailer().Key("Info"); !info.IsNull() {
		title = info.Key("Title").Text()
	}
	ex := Extracted{Title: title, Text: dehyphenate(ligatures.Replace(b.String()))}
	if read > 0 && empty*2 >= read {
		ex.Problem = scannedProblem(empty, read)
	}
	return ex, nil
}

func scannedProblem(empty, pages int) string {
	const fix = "To make it searchable, run text recognition (OCR) on the file, for example with OCRmyPDF, and add the result."
	if empty == pages {
		return "This PDF has no text that search can read: its pages are probably scanned images. " + fix
	}
	return fmt.Sprintf("%d of %d pages have no text that search can read: they are probably scanned images. %s", empty, pages, fix)
}

// pageText returns the text of one page. Where the fonts give glyph widths,
// the text is rebuilt from glyph positions, which separates words and lines
// that the content stream runs together ("97.15Station"). Otherwise gaps
// cannot be measured, and the content stream's own text is used.
func pageText(page pdf.Page) (s string) {
	defer func() {
		if recover() != nil {
			s = plainText(page)
		}
	}()
	glyphs := page.Content().Text
	if !widthsKnown(glyphs) {
		return plainText(page)
	}
	return layoutText(glyphs)
}

func plainText(page pdf.Page) string {
	t, err := page.GetPlainText(nil)
	if err != nil {
		return ""
	}
	return t
}

// widthsKnown reports whether most visible glyphs have a width. The PDF
// library reads widths only from simple fonts' /Widths arrays; for other
// fonts every width is 0 and the position does not advance within a run.
func widthsKnown(glyphs []pdf.Text) bool {
	known, total := 0, 0
	for _, g := range glyphs {
		if !visible(g.S) {
			continue
		}
		total++
		if g.W > 0 {
			known++
		}
	}
	return total > 0 && known*2 >= total
}

// placed is a glyph on a line, with what separates it from the previous one.
type placed struct {
	s string
	// gap is the horizontal distance from the end of the previous glyph, in
	// units of its font size. It is meaningful only when measured is true
	// (the previous glyph's width is known).
	gap      float64
	measured bool
	// space is true when a space glyph came between the two.
	space bool
}

// layoutText rebuilds text from glyphs in content-stream order: a vertical
// move starts a new line, and a wide horizontal gap separates words.
func layoutText(glyphs []pdf.Text) string {
	var b strings.Builder
	var line []placed
	var prev *pdf.Text
	space := false
	for i := range glyphs {
		g := &glyphs[i]
		if !visible(g.S) {
			// A space glyph separates words. The library also emits a line
			// feed after every TJ array; position decides lines instead.
			if g.S != "\n" && g.S != "" {
				space = true
			}
			continue
		}
		p := placed{s: g.S, space: space}
		if prev != nil {
			fs := fontSize(prev)
			dy := math.Abs(g.Y - prev.Y)
			if dy > lineStep*fs {
				b.WriteString(renderLine(line))
				b.WriteByte('\n')
				if dy > paragraphStep*fs {
					b.WriteByte('\n')
				}
				line = line[:0]
				p.space = false
			} else {
				p.gap = (g.X - (prev.X + prev.W)) / fs
				p.measured = prev.W > 0
			}
		}
		line = append(line, p)
		prev = g
		space = false
	}
	b.WriteString(renderLine(line))
	return b.String()
}

// renderLine joins a line's glyphs, putting a space wherever there was a
// space glyph or a word-sized gap.
func renderLine(line []placed) string {
	threshold := wordGap
	// Letter-spaced text (a heading set as "S T A T I O N" by widening every
	// gap) would otherwise become single letters. Its gaps are all about the
	// same, so only gaps clearly wider than the typical one separate words.
	if m, ok := letterSpacing(line); ok {
		threshold = m + wordGap
	}
	var b strings.Builder
	for i, p := range line {
		if i > 0 && (p.space || p.measured && (p.gap > threshold || p.gap < -backtrack)) {
			b.WriteByte(' ')
		}
		b.WriteString(p.s)
	}
	return b.String()
}

// letterSpacing reports whether most measured gaps on a line are wider than a
// word gap but narrower than an em, as in letter-spaced headings, and returns
// the median gap. Table cells have wider and uneven gaps, and ordinary text
// has wide gaps only between words.
func letterSpacing(line []placed) (float64, bool) {
	var gaps []float64
	wide := 0
	for _, p := range line[min(1, len(line)):] {
		if p.space || !p.measured {
			continue
		}
		gaps = append(gaps, p.gap)
		if p.gap > wordGap {
			wide++
		}
	}
	if len(gaps) < 5 || wide*10 < len(gaps)*7 {
		return 0, false
	}
	sort.Float64s(gaps)
	m := gaps[len(gaps)/2]
	return m, m < 1
}

func fontSize(t *pdf.Text) float64 {
	return math.Max(math.Abs(t.FontSize), 1)
}

func visible(s string) bool {
	return strings.IndexFunc(s, func(r rune) bool { return !unicode.IsSpace(r) && !unicode.IsControl(r) }) >= 0
}

func visibleChars(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) && !unicode.IsControl(r) {
			n++
		}
	}
	return n
}

// ligatures spells out typographic ligatures so that "conﬁguration" is found
// by a search for "configuration".
var ligatures = strings.NewReplacer("ﬀ", "ff", "ﬁ", "fi", "ﬂ", "fl", "ﬃ", "ffi", "ﬄ", "ffl", "ﬅ", "st", "ﬆ", "st")

var (
	// hyphenBreak matches a word broken across lines with a hyphen (or a
	// soft hyphen) where the next line continues in lower case.
	hyphenBreak = regexp.MustCompile(`(\p{L}+)[-\x{00AD}\x{2010}][ \t]*\n[ \t]*(\p{Ll}\p{L}*)`)
	letters     = regexp.MustCompile(`\p{L}+`)
)

// dehyphenate rejoins words broken across lines, so that "op-⏎erator" is
// indexed as "operator". A real compound such as "high-⏎frequency" keeps
// its hyphen: the document itself decides, by whether the joined word or
// both halves appear elsewhere in it.
func dehyphenate(s string) string {
	breaks := hyphenBreak.FindAllStringSubmatchIndex(s, -1)
	if len(breaks) == 0 {
		return s
	}
	count := map[string]int{}
	for _, w := range letters.FindAllString(s, -1) {
		count[strings.ToLower(w)]++
	}
	// The halves at the breaks themselves are not evidence.
	for _, m := range breaks {
		count[strings.ToLower(s[m[2]:m[3]])]--
		count[strings.ToLower(s[m[4]:m[5]])]--
	}
	var b strings.Builder
	last := 0
	for _, m := range breaks {
		head, tail := s[m[2]:m[3]], s[m[4]:m[5]]
		b.WriteString(s[last:m[2]])
		b.WriteString(head)
		if count[strings.ToLower(head+tail)] <= 0 && count[strings.ToLower(head)] > 0 && count[strings.ToLower(tail)] > 0 {
			b.WriteByte('-')
		}
		b.WriteString(tail)
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

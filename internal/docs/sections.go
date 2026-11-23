package docs

import (
	"regexp"
	"strings"
)

// sectionChars is about how much text one section holds in a document that
// does not divide itself into pages: some 450 words, a screen or two.
const sectionChars = 3000

var markdownHeadingRe = regexp.MustCompile(`^#{1,6}[ \t]+(.*?)[ \t#]*$`)

// splitSections divides text into sections of about sectionChars. It breaks
// at a blank line once a section is long enough, or at any line break when
// a section has grown half as long again. In Markdown every heading outside
// a code block starts a section and names it; otherwise every section
// carries heading.
func splitSections(text, heading string, markdown bool) []Section {
	var out []Section
	var b strings.Builder
	cur := heading
	flush := func() {
		if t := strings.TrimSpace(b.String()); t != "" {
			out = append(out, Section{Heading: cur, Text: t})
		}
		b.Reset()
	}
	fenced := false
	for _, line := range strings.SplitAfter(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if markdown {
			if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
				fenced = !fenced
			} else if m := markdownHeadingRe.FindStringSubmatch(trimmed); !fenced && m != nil {
				flush()
				cur = m[1]
			}
		}
		for _, piece := range splitLong(line) {
			b.WriteString(piece)
			if n := b.Len(); n >= sectionChars && (trimmed == "" || n >= sectionChars*3/2) {
				flush()
			}
		}
	}
	flush()
	return out
}

// splitLong cuts a line longer than a section at spaces, so that a file
// written as one enormous line still divides into sections.
func splitLong(line string) []string {
	if len(line) <= sectionChars {
		return []string{line}
	}
	var out []string
	for len(line) > sectionChars {
		cut := strings.LastIndexAny(line[:sectionChars], " \t")
		if cut <= 0 {
			cut = sectionChars
			for cut < len(line) && !isRuneStart(line[cut]) {
				cut++
			}
		}
		out = append(out, line[:cut]+"\n")
		line = strings.TrimLeft(line[cut:], " \t")
	}
	return append(out, line)
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

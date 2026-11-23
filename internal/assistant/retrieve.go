package assistant

import (
	"context"
	"math"
	"sort"
	"strings"
	"unicode"
)

// Hit is a search result the assistant may read.
type Hit struct {
	ResourceID string
	// Path locates the article or document inside the resource (as the
	// reader and the integration API use it).
	Path  string
	Title string
	// Kind is "archive", "document" or "user" (the person's own files).
	Kind string
}

// Source describes where a passage came from, so an answer can show the
// edition, license and warnings of everything it relied on.
type Source struct {
	ResourceTitle string    `json:"resourceTitle"`
	Publisher     string    `json:"publisher,omitempty"`
	Edition       string    `json:"edition,omitempty"`
	Published     string    `json:"published,omitempty"`
	License       string    `json:"license,omitempty"`
	Warnings      []Warning `json:"warnings"`
}

// Warning mirrors a catalog warning (medical, regulatory, outdated, …).
type Warning struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Library is what the assistant needs from Arcana: search, text and
// provenance. Implemented by the app package.
type Library interface {
	// Search finds articles and documents. includePrivate adds the
	// person's own documents.
	Search(ctx context.Context, query string, limit int, includePrivate bool) ([]Hit, error)
	// Text returns the plain text of a hit, at most max bytes.
	Text(ctx context.Context, h Hit, max int) (title, text string, err error)
	// Source describes an installed resource ("" for the person's own
	// documents).
	Source(resourceID string) (Source, bool)
}

// Passage is a piece of text given to the model and shown with the answer.
type Passage struct {
	N          int     `json:"n"`
	ResourceID string  `json:"resourceId"`
	Path       string  `json:"path"`
	Kind       string  `json:"kind"`
	Title      string  `json:"title"`
	Text       string  `json:"text"`
	Score      float64 `json:"score"`
	Source     Source  `json:"source"`
}

// stopwords are dropped from questions before searching. Question words,
// pronouns and filler such as "please tell me" carry no topic.
var stopwords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`a an the and or but if then so of to in on at by for with from into onto about
		above below over under between through during before after again further once here there all any both each few
		more most other some such no nor not only own same than too very can could should would will shall may might must
		do does did doing done is are was were be been being am have has had having i me my mine myself we us our ours you
		your yours he him his she her hers it its they them their theirs this that these those what which who whom whose
		when where why how whats hows wheres whos please tell explain describe give show know want need like get got use
		used using way ways thing things something anything mean means meaning meant definition define defined called
		call say says said one also just really much many lot lots kind sort type example examples best good better
		should shouldnt cant dont doesnt isnt arent wasnt werent im ive id youre theyre`) {
		m[w] = true
	}
	return m
}()

// tokens splits text into lower-case words (letters and digits).
func tokens(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// Keywords extracts the topic words of a question, in order, without
// duplicates (at most 8).
func Keywords(question string) []string {
	var out []string
	seen := map[string]bool{}
	all := tokens(strings.ReplaceAll(question, "'", ""))
	for _, t := range all {
		if stopwords[t] || seen[t] || (len([]rune(t)) < 2 && !isDigits(t)) {
			continue
		}
		seen[t] = true
		out = append(out, t)
		if len(out) == 8 {
			break
		}
	}
	if len(out) == 0 {
		// Nothing but common words ("what is it?"): keep them rather than
		// searching for nothing.
		for _, t := range all {
			if !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
		}
	}
	return out
}

func isDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
}

// matches reports whether a word in a passage counts as the keyword. Short
// keywords (codes such as "qth" or "sos") must match exactly; longer ones
// also match inflected forms ("dash" → "dashes", "convert" → "converting").
func matches(word, kw string) bool {
	if word == kw {
		return true
	}
	if len(kw) < 4 {
		return false
	}
	if strings.HasPrefix(word, kw) && len(word)-len(kw) <= 4 {
		return true
	}
	// "batteries" for "battery", "treated" for "treat".
	stem := kw
	if len(kw) > 5 {
		stem = kw[:len(kw)-1]
	}
	return len(stem) >= 4 && strings.HasPrefix(word, stem) && len(word)-len(stem) <= 4
}

// RetrieveOptions tune retrieval.
type RetrieveOptions struct {
	// Passages is the most passages returned (default 5).
	Passages int
	// Budget is the most characters of passage text in total (default 6000).
	Budget int
	// PassageSize is the target size of one passage in characters
	// (default 700).
	PassageSize int
	// IncludePrivate adds the person's own documents.
	IncludePrivate bool
}

func (o *RetrieveOptions) defaults() {
	if o.Passages <= 0 {
		o.Passages = 5
	}
	if o.Budget <= 0 {
		o.Budget = 6000
	}
	if o.PassageSize <= 0 {
		o.PassageSize = 700
	}
}

// maxArticles bounds how many articles are read for one question.
const maxArticles = 10

// maxArticleText is how much of each article is scored.
const maxArticleText = 200_000

type candidate struct {
	hit    Hit
	title  string
	text   string
	start  int
	words  []string
	titleW []string
}

// Retrieve searches the library for the question and returns the most
// relevant passages, best first, with their coverage of the question's
// keywords as Score (0–1, weighted by how rare each keyword is).
func Retrieve(ctx context.Context, lib Library, question string, opt RetrieveOptions) ([]Passage, []string, error) {
	opt.defaults()
	kws := Keywords(question)
	if len(kws) == 0 {
		return nil, kws, nil
	}
	hits, err := gather(ctx, lib, kws, opt.IncludePrivate)
	if err != nil && len(hits) == 0 {
		return nil, kws, err
	}
	var pool []candidate
	for _, h := range hits {
		if ctx.Err() != nil {
			return nil, kws, ctx.Err()
		}
		title, text, err := lib.Text(ctx, h, maxArticleText)
		if err != nil || strings.TrimSpace(text) == "" {
			continue
		}
		if title == "" {
			title = h.Title
		}
		for _, w := range windows(text, opt.PassageSize) {
			pool = append(pool, candidate{hit: h, title: title, text: w.text, start: w.start,
				words: tokens(w.text), titleW: tokens(title)})
		}
	}
	if len(pool) == 0 {
		return nil, kws, nil
	}
	// Rare keywords matter more: inverse frequency across the passages read.
	weight := map[string]float64{}
	total := 0.0
	for _, kw := range kws {
		df := 0
		for _, c := range pool {
			if containsKW(c.words, kw) {
				df++
			}
		}
		weight[kw] = math.Log(1 + float64(len(pool))/float64(1+df))
		total += weight[kw]
	}
	type scored struct {
		c        candidate
		coverage float64
		score    float64
	}
	var all []scored
	for _, c := range pool {
		cov, freq := 0.0, 0
		titleHits := 0
		for _, kw := range kws {
			inText := countKW(c.words, kw)
			inTitle := containsKW(c.titleW, kw)
			switch {
			case inText > 0:
				cov += weight[kw]
				freq += inText
			case inTitle:
				// The article is about it even if this paragraph does
				// not repeat the word.
				cov += 0.6 * weight[kw]
			}
			if inTitle {
				titleHits++
			}
		}
		if total > 0 {
			cov /= total
		}
		density := float64(freq) / float64(len(c.words)+20)
		score := cov + 0.05*float64(titleHits)/float64(len(kws)) + math.Min(density, 0.1)
		all = append(all, scored{c: c, coverage: cov, score: score})
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].score > all[j].score })
	var out []Passage
	perArticle := map[string]int{}
	used := 0
	for _, s := range all {
		if len(out) == opt.Passages || s.coverage == 0 {
			break
		}
		key := s.c.hit.ResourceID + "\x00" + s.c.hit.Path
		if perArticle[key] == 2 {
			continue
		}
		if used+len(s.c.text) > opt.Budget && len(out) > 0 {
			continue
		}
		perArticle[key]++
		used += len(s.c.text)
		src, _ := lib.Source(s.c.hit.ResourceID)
		if src.Warnings == nil {
			src.Warnings = []Warning{}
		}
		out = append(out, Passage{N: len(out) + 1, ResourceID: s.c.hit.ResourceID, Path: s.c.hit.Path, Kind: s.c.hit.Kind,
			Title: s.c.title, Text: s.c.text, Score: math.Round(s.coverage*100) / 100, Source: src})
	}
	return out, kws, nil
}

// gather runs searches: all keywords together first, then the most
// specific keywords on their own when that finds little (full-text search
// requires every word, which a long question rarely satisfies).
func gather(ctx context.Context, lib Library, kws []string, private bool) ([]Hit, error) {
	var out []Hit
	seen := map[string]bool{}
	add := func(hs []Hit) {
		for _, h := range hs {
			k := h.ResourceID + "\x00" + h.Path
			if seen[k] || len(out) >= maxArticles {
				continue
			}
			seen[k] = true
			out = append(out, h)
		}
	}
	hs, err := lib.Search(ctx, strings.Join(kws, " "), 6, private)
	add(hs)
	if len(out) >= 4 || len(kws) == 1 {
		return out, err
	}
	// Longer words are usually more specific.
	byLen := append([]string(nil), kws...)
	sort.SliceStable(byLen, func(i, j int) bool { return len(byLen[i]) > len(byLen[j]) })
	if len(byLen) > 2 {
		pair, err2 := lib.Search(ctx, byLen[0]+" "+byLen[1], 4, private)
		add(pair)
		if err == nil {
			err = err2
		}
	}
	for _, kw := range byLen {
		if len(out) >= maxArticles {
			break
		}
		one, err2 := lib.Search(ctx, kw, 3, private)
		add(one)
		if err == nil {
			err = err2
		}
	}
	return out, err
}

func containsKW(words []string, kw string) bool {
	for _, w := range words {
		if matches(w, kw) {
			return true
		}
	}
	return false
}

func countKW(words []string, kw string) int {
	n := 0
	for _, w := range words {
		if matches(w, kw) {
			n++
		}
	}
	return n
}

type window struct {
	text  string
	start int
}

// windows splits text into passages of about size characters along line
// breaks (paragraphs, list items and table rows). Very long lines are cut
// at sentence ends.
func windows(text string, size int) []window {
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		for len(l) > size*2 {
			cut := sentenceCut(l, size)
			lines = append(lines, strings.TrimSpace(l[:cut]))
			l = strings.TrimSpace(l[cut:])
		}
		if l != "" {
			lines = append(lines, l)
		}
	}
	var out []window
	var b strings.Builder
	start, pos := 0, 0
	for _, l := range lines {
		if b.Len() > 0 && b.Len()+len(l) > size {
			out = append(out, window{text: b.String(), start: start})
			b.Reset()
			start = pos
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(l)
		pos += len(l) + 1
	}
	if b.Len() > 0 {
		out = append(out, window{text: b.String(), start: start})
	}
	return out
}

func sentenceCut(s string, size int) int {
	limit := size * 3 / 2
	if limit > len(s) {
		limit = len(s)
	}
	best := -1
	for i := size / 2; i < limit-1; i++ {
		if (s[i] == '.' || s[i] == '?' || s[i] == '!') && s[i+1] == ' ' {
			best = i + 1
			if i >= size {
				break
			}
		}
	}
	if best > 0 {
		return best
	}
	cut := size
	for cut > 0 && s[cut]&0xC0 == 0x80 { // do not split a UTF-8 sequence
		cut--
	}
	return cut
}

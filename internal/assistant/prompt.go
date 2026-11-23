package assistant

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// NotFound is the sentence the model is told to use when the passages do
// not answer the question; Arcana also uses it when retrieval finds
// nothing relevant enough.
const NotFound = "I could not find this in your library."

const libraryRules = `You answer questions for the reader of an offline reference library.
Rules:
- Use only the numbered passages from the library. Do not add facts from your own memory.
- After each sentence that uses a passage, cite it with its number in square brackets, like [1] or [2][3].
- If the passages do not answer the question, reply exactly: "` + NotFound + `" and nothing else.
- Answer in the language of the question, in plain words, in at most about 200 words. Use short numbered steps for procedures.
- If a passage concerns medicine, safety or the law, end with one sentence advising the reader to get qualified help when they can.`

const generalRules = `You answer questions for someone using an offline library computer. The library was not searched for this question, so answer from general knowledge.
- Say plainly when you are not sure. Never invent sources, numbers or quotations.
- Answer in the language of the question, in plain words, in at most about 200 words.
- For medicine, safety or the law, end with one sentence advising the reader to get qualified help when they can.`

// BuildMessages assembles the chat for a question. With passages the model
// must answer from them; without (retrieval switched off by the reader) it
// answers from general knowledge and the answer is labelled as such.
func BuildMessages(question string, passages []Passage, instructions string, withLibrary bool) []Message {
	sys := generalRules
	if withLibrary {
		sys = libraryRules
	}
	if s := strings.TrimSpace(instructions); s != "" {
		sys += "\n\nThe library's owner adds these instructions (the rules above still apply):\n" + s
	}
	if !withLibrary {
		return []Message{{Role: "system", Content: sys}, {Role: "user", Content: question}}
	}
	var b strings.Builder
	b.WriteString("Passages from the library:\n\n")
	for _, p := range passages {
		fmt.Fprintf(&b, "[%d] %s", p.N, p.Title)
		if p.Source.ResourceTitle != "" && p.Source.ResourceTitle != p.Title {
			fmt.Fprintf(&b, " (from %s", p.Source.ResourceTitle)
			if p.Source.Published != "" {
				fmt.Fprintf(&b, ", %s", p.Source.Published)
			}
			b.WriteString(")")
		}
		b.WriteString("\n")
		b.WriteString(p.Text)
		b.WriteString("\n\n")
	}
	b.WriteString("Question: ")
	b.WriteString(question)
	return []Message{{Role: "system", Content: sys}, {Role: "user", Content: b.String()}}
}

// Check is the result of checking an answer's citations.
type Check struct {
	// Cited lists the passage numbers the answer refers to.
	Cited []int `json:"cited"`
	// Invalid lists numbers that refer to no passage (removed from the
	// answer).
	Invalid []int `json:"invalid,omitempty"`
	// Uncited counts sentences that make a claim without a citation.
	Uncited int `json:"uncited"`
	// NotFound is true when the answer says the library does not cover
	// the question.
	NotFound bool `json:"notFound"`
	// Unsupported is true when the answer cites nothing at all although it
	// claims to answer.
	Unsupported bool `json:"unsupported"`
}

var citeRe = regexp.MustCompile(`\[(\d{1,2})\]`)

// CheckAnswer verifies citations against the passages and removes those
// that point nowhere.
func CheckAnswer(answer string, passages []Passage) (string, Check) {
	var c Check
	valid := map[int]bool{}
	for _, p := range passages {
		valid[p.N] = true
	}
	seen := map[int]bool{}
	cleaned := citeRe.ReplaceAllStringFunc(answer, func(m string) string {
		n, _ := strconv.Atoi(m[1 : len(m)-1])
		if !valid[n] {
			c.Invalid = append(c.Invalid, n)
			return ""
		}
		if !seen[n] {
			seen[n] = true
			c.Cited = append(c.Cited, n)
		}
		return m
	})
	trimmed := strings.TrimSpace(cleaned)
	norm := strings.ToLower(strings.Trim(trimmed, `"' .`))
	if strings.Contains(norm, strings.ToLower(strings.TrimSuffix(NotFound, "."))) || trimmed == "" {
		c.NotFound = true
		if len(c.Cited) == 0 {
			return NotFound, c
		}
	}
	for _, s := range sentences(trimmed) {
		// Short fragments and introductions ("The steps are:") are not
		// claims that need a source.
		if len(tokens(s)) < 5 || citeRe.MatchString(s) || strings.HasSuffix(s, ":") {
			continue
		}
		c.Uncited++
	}
	c.Unsupported = !c.NotFound && len(c.Cited) == 0
	return trimmed, c
}

// sentences splits prose roughly at sentence ends and line breaks, leaving
// a citation that follows the full stop with its sentence.
func sentences(s string) []string {
	var out []string
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		b.WriteRune(rs[i])
		end := rs[i] == '\n' || ((rs[i] == '.' || rs[i] == '?' || rs[i] == '!') && (i+1 == len(rs) || rs[i+1] == ' ' || rs[i+1] == '\n' || rs[i+1] == '['))
		if !end {
			continue
		}
		// Absorb citations directly after the punctuation: "… water. [2]".
		for i+1 < len(rs) && (rs[i+1] == ' ' || rs[i+1] == '[') {
			j := i + 1
			for j < len(rs) && rs[j] == ' ' {
				j++
			}
			if j < len(rs) && rs[j] == '[' {
				k := j
				for k < len(rs) && rs[k] != ']' {
					k++
				}
				if k < len(rs) {
					b.WriteString(string(rs[i+1 : k+1]))
					i = k
					continue
				}
			}
			break
		}
		if t := strings.TrimSpace(b.String()); t != "" {
			out = append(out, t)
		}
		b.Reset()
	}
	if t := strings.TrimSpace(b.String()); t != "" {
		out = append(out, t)
	}
	return out
}

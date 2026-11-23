package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Golden is one question of an evaluation set.
type Golden struct {
	Question string `json:"question"`
	// Answerable is false for questions the library does not cover; the
	// right result is NotFound.
	Answerable bool `json:"answerable"`
	// Expect lists acceptable sources (any one is enough).
	Expect []Expect `json:"expect,omitempty"`
	// MustContain lists words the answer should contain (checked only
	// when a model answers; case-insensitive).
	MustContain []string `json:"mustContain,omitempty"`
}

// Expect identifies a source by resource and a fragment of its path.
type Expect struct {
	ResourceID string `json:"resourceId"`
	Path       string `json:"path,omitempty"`
}

// GoldenSet is a file of evaluation questions.
type GoldenSet struct {
	Name      string   `json:"name"`
	Questions []Golden `json:"questions"`
}

// LoadGoldenSet reads an evaluation set.
func LoadGoldenSet(p string) (GoldenSet, error) {
	var gs GoldenSet
	data, err := os.ReadFile(p)
	if err != nil {
		return gs, err
	}
	if err := json.Unmarshal(data, &gs); err != nil {
		return gs, fmt.Errorf("%s: %w", p, err)
	}
	if len(gs.Questions) == 0 {
		return gs, fmt.Errorf("%s: no questions", p)
	}
	return gs, nil
}

// EvalItem is the outcome for one question.
type EvalItem struct {
	Question string `json:"question"`
	// Retrieved is true when an expected source is among the passages
	// (answerable) or nothing passed the relevance floor (unanswerable).
	Retrieved bool `json:"retrieved"`
	// Top is true when the first passage is an expected source.
	Top bool `json:"top"`
	// Answered is set when a model ran: the answer cites an expected
	// passage and contains the required words (answerable), or says
	// NotFound (unanswerable).
	Answered *bool  `json:"answered,omitempty"`
	Answer   string `json:"answer,omitempty"`
	Detail   string `json:"detail,omitempty"`
	MS       int64  `json:"ms"`
}

// EvalReport summarises an evaluation run.
type EvalReport struct {
	Name           string     `json:"name"`
	Questions      int        `json:"questions"`
	RetrievalScore float64    `json:"retrievalScore"`
	TopScore       float64    `json:"topScore"`
	AnswerScore    *float64   `json:"answerScore,omitempty"`
	Items          []EvalItem `json:"items"`
}

func (g Golden) expected(p Passage) bool {
	for _, e := range g.Expect {
		if p.ResourceID == e.ResourceID && (e.Path == "" || strings.Contains(p.Path, e.Path)) {
			return true
		}
	}
	return false
}

// Evaluate runs a golden set. With withModel false only retrieval and the
// relevance floor are measured (fast, deterministic, no model needed).
func (s *Service) Evaluate(ctx context.Context, gs GoldenSet, withModel bool) EvalReport {
	rep := EvalReport{Name: gs.Name, Questions: len(gs.Questions)}
	cfg := s.Config()
	cfg.defaults()
	var retrieved, top, answered int
	for _, g := range gs.Questions {
		started := time.Now()
		it := EvalItem{Question: g.Question}
		ps, _, err := Retrieve(ctx, s.Lib, g.Question, RetrieveOptions{Passages: cfg.Passages, Budget: cfg.budget()})
		skipped := len(ps) == 0 || ps[0].Score < cfg.RelevanceFloor
		switch {
		case err != nil:
			it.Detail = "search failed: " + err.Error()
		case !g.Answerable:
			it.Retrieved = skipped
			it.Top = skipped
			if !skipped {
				it.Detail = fmt.Sprintf("passed the floor with %q (%.2f)", ps[0].Title, ps[0].Score)
			}
		default:
			for i, p := range ps {
				if g.expected(p) && !skipped {
					it.Retrieved = true
					if i == 0 {
						it.Top = true
					}
				}
			}
			if skipped && len(ps) > 0 {
				it.Detail = fmt.Sprintf("best passage %q scored %.2f, below the floor", ps[0].Title, ps[0].Score)
			} else if !it.Retrieved {
				it.Detail = "no expected source among the passages"
			}
		}
		if withModel && err == nil {
			res, aerr := s.Ask(ctx, Request{Question: g.Question, Retrieval: true}, nil)
			ok := false
			switch {
			case aerr != nil:
				it.Detail = strings.TrimSpace(it.Detail + " answer failed: " + aerr.Error())
			case !g.Answerable:
				ok = res.Check.NotFound
			default:
				citedExpected := false
				for _, n := range res.Check.Cited {
					if n-1 < len(res.Passages) && g.expected(res.Passages[n-1]) {
						citedExpected = true
					}
				}
				ok = citedExpected && !res.Check.NotFound
				low := strings.ToLower(res.Answer)
				for _, w := range g.MustContain {
					if !strings.Contains(low, strings.ToLower(w)) {
						ok = false
					}
				}
			}
			it.Answered = &ok
			it.Answer = res.Answer
			if ok {
				answered++
			}
		}
		if it.Retrieved {
			retrieved++
		}
		if it.Top {
			top++
		}
		it.MS = time.Since(started).Milliseconds()
		rep.Items = append(rep.Items, it)
	}
	if n := float64(len(gs.Questions)); n > 0 {
		rep.RetrievalScore = float64(retrieved) / n
		rep.TopScore = float64(top) / n
		if withModel {
			a := float64(answered) / n
			rep.AnswerScore = &a
		}
	}
	return rep
}

// Print writes a readable report.
func (r EvalReport) Print(w io.Writer) {
	fmt.Fprintf(w, "Evaluation: %s (%d questions)\n", r.Name, r.Questions)
	for _, it := range r.Items {
		mark := "ok "
		if !it.Retrieved {
			mark = "MISS"
		}
		fmt.Fprintf(w, "  [%s] %s", mark, it.Question)
		if it.Answered != nil {
			if *it.Answered {
				fmt.Fprint(w, "  answer: ok")
			} else {
				fmt.Fprint(w, "  answer: WRONG")
			}
		}
		fmt.Fprintf(w, "  (%d ms)\n", it.MS)
		if it.Detail != "" {
			fmt.Fprintf(w, "         %s\n", it.Detail)
		}
		if it.Answered != nil && !*it.Answered && it.Answer != "" {
			fmt.Fprintf(w, "         answer was: %s\n", strings.ReplaceAll(it.Answer, "\n", " "))
		}
	}
	fmt.Fprintf(w, "Retrieval: %.0f%%   first passage correct: %.0f%%", r.RetrievalScore*100, r.TopScore*100)
	if r.AnswerScore != nil {
		fmt.Fprintf(w, "   answers: %.0f%%", *r.AnswerScore*100)
	}
	fmt.Fprintln(w)
}

// Package assistant answers questions from the installed library.
//
// Retrieval comes first: Arcana's own search (Xapian indexes inside ZIM
// archives through kiwix-serve, and SQLite FTS5 for documents) finds
// articles, the assistant scores passages from them, and only passages
// that cover the question go to a language model. The model runs as a
// sidecar (llamafile, started on demand on loopback) or on any
// OpenAI-compatible server the owner names (Ollama, LM Studio, …). Every
// answer lists the passages it used, with their editions and warnings, and
// citations are checked before the answer is shown as finished.
package assistant

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Config tunes answering. Zero values use defaults.
type Config struct {
	// Passages is the most passages given to the model (default 5).
	Passages int
	// RelevanceFloor is the share of the question's keywords (weighted by
	// rarity) the best passage must contain before the model is asked
	// (default 0.5). Below it the answer is NotFound.
	RelevanceFloor float64
	// ContextSize is the model's context window in tokens (default 4096);
	// passages are budgeted to fit.
	ContextSize int
	// MaxAnswerTokens bounds the answer (default 600).
	MaxAnswerTokens int
	// Instructions are the owner's additions to the system prompt.
	Instructions string
}

func (c *Config) defaults() {
	if c.Passages <= 0 || c.Passages > 12 {
		c.Passages = 5
	}
	if c.RelevanceFloor <= 0 || c.RelevanceFloor > 1 {
		c.RelevanceFloor = 0.5
	}
	if c.ContextSize < 1024 {
		c.ContextSize = 4096
	}
	if c.MaxAnswerTokens <= 0 {
		c.MaxAnswerTokens = 600
	}
}

// budget is how many characters of passages fit in the context window,
// leaving room for the instructions, the question and the answer (about
// three characters per token).
func (c Config) budget() int {
	b := (c.ContextSize - c.MaxAnswerTokens - 500) * 3
	if b < 1500 {
		b = 1500
	}
	return b
}

// Request is one question.
type Request struct {
	Question string
	// Retrieval searches the library first (the normal case). Without it
	// the model answers from general knowledge and the answer says so.
	Retrieval bool
	// IncludePrivate allows the person's own documents (only for the
	// host): searched with the whole library, or when Sources names them.
	IncludePrivate bool
	// Sources limits retrieval to these installed resources, by ID, and
	// OwnDocuments for the person's own documents. Empty searches the
	// whole library.
	Sources []string
}

// ErrPrivateSource is returned when a question names the person's own
// documents without access to them.
var ErrPrivateSource = errors.New("your documents can only be searched on the computer running Arcana")

// scope turns a request's sources into where retrieval looks, and the
// sources named, each once.
func (r Request) scope() (Scope, []string, error) {
	named := []string{}
	seen := map[string]bool{}
	for _, id := range r.Sources {
		if id = strings.TrimSpace(id); id != "" && !seen[id] {
			seen[id] = true
			named = append(named, id)
		}
	}
	if len(named) == 0 {
		return Scope{Own: r.IncludePrivate}, named, nil
	}
	sc := Scope{Only: true}
	for _, id := range named {
		if id == OwnDocuments {
			if !r.IncludePrivate {
				return Scope{}, nil, ErrPrivateSource
			}
			sc.Own = true
			continue
		}
		sc.Resources = append(sc.Resources, id)
	}
	return sc, named, nil
}

// Event is streamed to the person while an answer is prepared.
type Event struct {
	// Type is "status", "passages", "token", "done" or "error".
	Type string `json:"type"`
	// Status is "searching", "waiting", "loading" or "answering".
	Status   string    `json:"status,omitempty"`
	Message  string    `json:"message,omitempty"`
	Passages []Passage `json:"passages,omitempty"`
	Token    string    `json:"token,omitempty"`
	Result   *Result   `json:"result,omitempty"`
}

// Result is a finished answer.
type Result struct {
	Question string    `json:"question"`
	Answer   string    `json:"answer"`
	Passages []Passage `json:"passages"`
	Keywords []string  `json:"keywords"`
	Check    Check     `json:"check"`
	// FromLibrary is false when retrieval was switched off.
	FromLibrary bool `json:"fromLibrary"`
	// Sources are the sources searched, as the request named them; empty
	// when the whole library was.
	Sources []string `json:"sources"`
	// Skipped is true when no passage was relevant enough to ask the
	// model at all.
	Skipped   bool  `json:"skipped"`
	Truncated bool  `json:"truncated"`
	ElapsedMS int64 `json:"elapsedMs"`
}

// BackendFunc returns a ready backend, starting a local model if needed.
// It may call emit with "loading" status while a model starts.
type BackendFunc func(ctx context.Context, emit func(Event)) (Backend, error)

// Service answers questions. One answer is generated at a time; others
// wait (a small computer cannot run two at once usefully).
type Service struct {
	Lib     Library
	Backend BackendFunc
	Config  func() Config
	slot    chan struct{}
}

// New creates a service.
func New(lib Library, backend BackendFunc, cfg func() Config) *Service {
	return &Service{Lib: lib, Backend: backend, Config: cfg, slot: make(chan struct{}, 1)}
}

// ErrNoQuestion is returned for an empty question.
var ErrNoQuestion = errors.New("ask a question")

// MaxQuestion bounds the question length in bytes.
const MaxQuestion = 1000

// Ask answers a question, reporting progress through emit (which may be
// nil). The final Result is also sent as a "done" event.
func (s *Service) Ask(ctx context.Context, req Request, emit func(Event)) (Result, error) {
	if emit == nil {
		emit = func(Event) {}
	}
	started := time.Now()
	q := strings.TrimSpace(req.Question)
	if q == "" {
		return Result{}, ErrNoQuestion
	}
	if len(q) > MaxQuestion {
		return Result{}, errors.New("the question is too long")
	}
	scope, named, err := req.scope()
	if err != nil {
		return Result{}, err
	}
	cfg := s.Config()
	cfg.defaults()
	res := Result{Question: q, FromLibrary: req.Retrieval, Passages: []Passage{}, Keywords: []string{}, Sources: []string{}}
	if req.Retrieval {
		res.Sources = named
		msg := "Searching your library…"
		if scope.Only {
			msg = "Searching the chosen sources…"
		}
		emit(Event{Type: "status", Status: "searching", Message: msg})
		ps, kws, err := Retrieve(ctx, s.Lib, q, RetrieveOptions{Passages: cfg.Passages, Budget: cfg.budget(), Scope: scope})
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if kws != nil {
			res.Keywords = kws
		}
		if err != nil && len(ps) == 0 {
			return res, err
		}
		if len(ps) == 0 || ps[0].Score < cfg.RelevanceFloor {
			res.Skipped = true
			res.Answer = NotFound
			res.Check = Check{NotFound: true}
			if len(ps) > 0 {
				// Show what came closest so the reader can look further.
				res.Passages = ps[:min(len(ps), 3)]
			}
			res.ElapsedMS = time.Since(started).Milliseconds()
			emit(Event{Type: "done", Result: &res})
			return res, nil
		}
		// Weakly related passages only confuse a small model.
		kept := ps[:0:0]
		for _, p := range ps {
			if p.Score >= cfg.RelevanceFloor*0.6 {
				kept = append(kept, p)
			}
		}
		for i := range kept {
			kept[i].N = i + 1
		}
		res.Passages = kept
		emit(Event{Type: "passages", Passages: kept})
	}

	// One generation at a time.
	select {
	case s.slot <- struct{}{}:
	default:
		emit(Event{Type: "status", Status: "waiting", Message: "Waiting for another answer to finish…"})
		select {
		case s.slot <- struct{}{}:
		case <-ctx.Done():
			return res, ctx.Err()
		}
	}
	defer func() { <-s.slot }()

	be, err := s.Backend(ctx, emit)
	if err != nil {
		return res, err
	}
	emit(Event{Type: "status", Status: "answering", Message: "Writing the answer…"})
	msgs := BuildMessages(q, res.Passages, cfg.Instructions, req.Retrieval)
	out, err := be.Chat(ctx, ChatRequest{Messages: msgs, MaxTokens: cfg.MaxAnswerTokens, Temperature: 0.2},
		func(t string) { emit(Event{Type: "token", Token: t}) })
	if err != nil {
		return res, err
	}
	res.Truncated = out.Truncated
	if req.Retrieval {
		res.Answer, res.Check = CheckAnswer(out.Text, res.Passages)
	} else {
		res.Answer = strings.TrimSpace(out.Text)
		res.Check = Check{Cited: []int{}}
	}
	if res.Check.Cited == nil {
		res.Check.Cited = []int{}
	}
	res.ElapsedMS = time.Since(started).Milliseconds()
	emit(Event{Type: "done", Result: &res})
	return res, nil
}

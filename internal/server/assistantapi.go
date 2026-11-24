package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/assistant"
)

// Questions are answered one at a time; a few more may wait or search.
const maxAskRequests = 4

var askRequests atomic.Int32

func (s *Server) registerAssistant(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/assistant", s.withApp(s.handleAssistantStatus))
	// A GET, so read-only network clients can ask when the owner allows
	// it; asking changes nothing in the library.
	mux.HandleFunc("GET /api/assistant/ask", s.withApp(s.handleAsk))
	mux.HandleFunc("POST /api/assistant/test", s.withApp(s.handleAssistantTest))
	mux.HandleFunc("POST /api/assistant/stop", s.withApp(s.handleAssistantStop))
}

// canAsk reports whether this client may ask questions.
func (s *Server) canAsk(r *http.Request, a *app.App) (bool, string) {
	st := a.Lib.Settings.Get().Assistant
	switch {
	case !st.Enabled:
		return false, "Question answering is turned off."
	case !s.isAdmin(r) && !st.NetworkClients:
		return false, "Questions can be asked on the computer running Arcana. Its owner can allow devices on the network to ask (Settings → Assistant)."
	}
	return true, ""
}

// maxAskSources bounds how many sources one question names.
const maxAskSources = 50

// askSources reads the sources a question is limited to (in=ID,ID,…): the
// installed archives and document collections AskSources lists, and @user,
// the person's own documents, only on the computer running Arcana. None is
// the whole library. A refusal comes with its HTTP status and message.
func askSources(r *http.Request, a *app.App, private bool) ([]string, int, string) {
	raw := strings.TrimSpace(r.URL.Query().Get("in"))
	if raw == "" {
		return nil, 0, ""
	}
	var ids []string
	for _, id := range strings.Split(raw, ",") {
		if id = strings.TrimSpace(id); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) > maxAskSources {
		return nil, http.StatusBadRequest, fmt.Sprintf("Choose at most %d sources.", maxAskSources)
	}
	known := map[string]bool{}
	for _, src := range a.AskSources(r.Context(), false) {
		known[src.ID] = true
	}
	for _, id := range ids {
		switch {
		case id == assistant.OwnDocuments && !private:
			return nil, http.StatusForbidden, "Your documents can only be searched on the computer running Arcana."
		case id == assistant.OwnDocuments:
		case !known[id]:
			return nil, http.StatusBadRequest, fmt.Sprintf("%q is not installed, or holds nothing that can be searched.", id)
		}
	}
	return ids, 0, ""
}

func (s *Server) handleAssistantStatus(w http.ResponseWriter, r *http.Request, a *app.App) {
	st := a.AssistantStatus(r.Context())
	ok, why := s.canAsk(r, a)
	resp := map[string]any{"status": st, "canAsk": ok && st.Ready, "private": isLocal(r),
		// What a question can be limited to; the host user's own
		// documents only on the computer running Arcana.
		"sources": a.AskSources(r.Context(), isLocal(r))}
	if !ok {
		resp["reason"] = why
	} else if !st.Ready {
		resp["reason"] = st.Problem
	}
	if !s.isAdmin(r) {
		// Network clients need not see which server or model is used.
		st.Server, st.Models = "", nil
		resp["status"] = st
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request, a *app.App) {
	if ok, why := s.canAsk(r, a); !ok {
		writeError(w, http.StatusForbidden, why)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len(q) > assistant.MaxQuestion {
		writeError(w, http.StatusBadRequest, "Ask a question of at most 1000 characters.")
		return
	}
	sources, code, msg := askSources(r, a, isLocal(r))
	if code != 0 {
		writeError(w, code, msg)
		return
	}
	if askRequests.Add(1) > maxAskRequests {
		askRequests.Add(-1)
		writeError(w, http.StatusTooManyRequests, "Several questions are already being answered. Try again in a minute.")
		return
	}
	defer askRequests.Add(-1)

	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	var mu sync.Mutex
	broken := false
	send := func(event string, v any) {
		mu.Lock()
		defer mu.Unlock()
		if broken {
			return
		}
		data, _ := json.Marshal(v)
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil || rc.Flush() != nil {
			broken = true
		}
	}
	// Loading a model from a slow drive can take minutes; comments keep
	// proxies and browsers from giving up on a silent connection.
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				mu.Lock()
				if !broken {
					if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil || rc.Flush() != nil {
						broken = true
					}
				}
				mu.Unlock()
			}
		}
	}()
	req := assistant.Request{Question: q, Retrieval: r.URL.Query().Get("retrieval") != "0",
		// The host user's own documents never reach other devices.
		IncludePrivate: isLocal(r), Sources: sources}
	_, err := a.Assistant.Ask(ctx, req, func(e assistant.Event) { send(e.Type, e) })
	if err != nil && !errors.Is(err, context.Canceled) {
		msg := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "The answer took too long and was stopped."
		}
		send("error", assistant.Event{Type: "error", Message: msg})
	}
}

func (s *Server) handleAssistantTest(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		URL    string `json:"url"`
		APIKey string `json:"apiKey"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if req.APIKey == "" {
		req.APIKey = a.Lib.Settings.Get().Assistant.APIKey
	}
	models, err := a.TestAssistantServer(r.Context(), req.URL, req.APIKey)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": nonNil(models)})
}

func (s *Server) handleAssistantStop(w http.ResponseWriter, r *http.Request, a *app.App) {
	a.StopAssistantEngine()
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleIntAsk answers a question for a paired program (integration API
// v1). The answer is returned whole, with the passages it cites and their
// citations; the person's own documents are never used.
func (s *Server) handleIntAsk(w http.ResponseWriter, r *http.Request, a *app.App) {
	if !a.Lib.Settings.Get().Assistant.Enabled {
		writeError(w, http.StatusServiceUnavailable, "Question answering is turned off on this Arcana.")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len(q) > assistant.MaxQuestion {
		writeError(w, http.StatusBadRequest, "q is required (at most 1000 characters)")
		return
	}
	// Programs never search the host user's own documents.
	sources, code, msg := askSources(r, a, false)
	if code != 0 {
		writeError(w, code, msg)
		return
	}
	if askRequests.Add(1) > maxAskRequests {
		askRequests.Add(-1)
		writeError(w, http.StatusTooManyRequests, "Several questions are already being answered. Try again in a minute.")
		return
	}
	defer askRequests.Add(-1)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	res, err := a.Assistant.Ask(ctx, assistant.Request{Question: q, Retrieval: true, Sources: sources}, nil)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	cits := s.citations(r, a)
	type passage struct {
		assistant.Passage
		Citation *app.Citation `json:"citation,omitempty"`
	}
	ps := []passage{}
	for _, p := range res.Passages {
		pp := passage{Passage: p}
		if c, ok := cits[p.ResourceID]; ok {
			pp.Citation = &c
		}
		ps = append(ps, pp)
	}
	writeJSON(w, http.StatusOK, map[string]any{"question": res.Question, "answer": res.Answer, "notFound": res.Check.NotFound,
		"unsupported": res.Check.Unsupported, "cited": res.Check.Cited, "passages": ps, "truncated": res.Truncated,
		"sources": res.Sources})
}

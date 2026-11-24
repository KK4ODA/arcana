package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeLib is an in-memory library: articles matched when they contain
// every search word (like Xapian's default AND).
type fakeLib struct {
	articles []fakeArticle
	searches []string
	scopes   []Scope
}

type fakeArticle struct {
	id, path, title, text, kind string
}

func (f *fakeLib) Search(ctx context.Context, q string, limit int, scope Scope) ([]Hit, error) {
	f.searches = append(f.searches, q)
	f.scopes = append(f.scopes, scope)
	var out []Hit
	for _, a := range f.articles {
		if a.kind == "user" && !scope.Own {
			continue
		}
		if scope.Only && a.kind != "user" && !slices.Contains(scope.Resources, a.id) {
			continue
		}
		words := tokens(a.title + " " + a.text)
		all := true
		for _, kw := range tokens(q) {
			if !containsKW(words, kw) {
				all = false
			}
		}
		if all {
			out = append(out, Hit{ResourceID: a.id, Path: a.path, Title: a.title, Kind: a.kind})
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *fakeLib) Text(ctx context.Context, h Hit, max int) (string, string, error) {
	for _, a := range f.articles {
		if a.id == h.ResourceID && a.path == h.Path {
			return a.title, a.text, nil
		}
	}
	return "", "", fmt.Errorf("missing")
}

func (f *fakeLib) Source(id string) (Source, bool) {
	if id == "med" {
		return Source{ResourceTitle: "Field medicine", Edition: "2026-01", Warnings: []Warning{{Kind: "medical", Text: "Dated reference."}}}, true
	}
	return Source{ResourceTitle: "Reference " + id, Edition: "1"}, true
}

func sampleLib() *fakeLib {
	return &fakeLib{articles: []fakeArticle{
		{"radio", "/q_codes", "Common Q codes", "Q codes are three-letter abbreviations used in radio.\nQTH\nLocation of the station\nQRP\nReduce power; low-power operation", "archive"},
		{"radio", "/morse", "International Morse code", "Morse code represents characters as dots and dashes.\nA dash lasts three dots; the gap between letters is three dots.", "archive"},
		{"med", "/burns", "Burns", "Cool a burn with cool running water for twenty minutes.\nDo not apply ice or butter to a burn.", "archive"},
		{"me", "user:plan.md", "Family plan", "Meet at the water tower if the house is not safe.", "user"},
	}}
}

func TestKeywords(t *testing.T) {
	got := Keywords("What does QTH mean, please?")
	if strings.Join(got, ",") != "qth" {
		t.Fatalf("keywords = %v", got)
	}
	got = Keywords("How do I treat a burn on my hand?")
	if strings.Join(got, ",") != "treat,burn,hand" {
		t.Fatalf("keywords = %v", got)
	}
	if got := Keywords("what is it?"); len(got) == 0 {
		t.Fatal("all-stopword question produced no keywords")
	}
}

func TestMatches(t *testing.T) {
	for _, c := range []struct {
		w, kw string
		ok    bool
	}{{"dashes", "dash", true}, {"qth", "qth", true}, {"qths", "qth", false}, {"batteries", "battery", true},
		{"converting", "convert", true}, {"treatment", "treat", true}, {"concatenation", "cat", false}} {
		if matches(c.w, c.kw) != c.ok {
			t.Errorf("matches(%q, %q) = %v", c.w, c.kw, !c.ok)
		}
	}
}

func TestRetrieveRanksAndFalls(t *testing.T) {
	lib := sampleLib()
	ps, _, err := Retrieve(context.Background(), lib, "What does QTH mean?", RetrieveOptions{})
	if err != nil || len(ps) == 0 {
		t.Fatalf("retrieve: %v %v", ps, err)
	}
	if ps[0].Path != "/q_codes" || ps[0].Score < 0.99 {
		t.Fatalf("top passage = %+v", ps[0])
	}
	// A long question: no article contains every word, so the retriever
	// falls back to single keywords.
	lib.searches = nil
	ps, _, _ = Retrieve(context.Background(), lib, "How long should I keep cooling a burn under water after an accident?", RetrieveOptions{})
	if len(ps) == 0 || ps[0].ResourceID != "med" {
		t.Fatalf("burn passages = %+v (searches %v)", ps, lib.searches)
	}
	if len(lib.searches) < 2 {
		t.Fatalf("expected fallback searches, got %v", lib.searches)
	}
	if ps[0].Source.Warnings[0].Kind != "medical" {
		t.Fatalf("source warnings not attached: %+v", ps[0].Source)
	}
	// Private documents only when asked for.
	ps, _, _ = Retrieve(context.Background(), lib, "Where do we meet at the water tower?", RetrieveOptions{})
	for _, p := range ps {
		if p.Kind == "user" {
			t.Fatalf("private document leaked: %+v", p)
		}
	}
	ps, _, _ = Retrieve(context.Background(), lib, "Where do we meet at the water tower?", RetrieveOptions{Scope: Scope{Own: true}})
	if len(ps) == 0 || ps[0].Kind != "user" {
		t.Fatalf("private document not found: %+v", ps)
	}
}

// A question can be limited to chosen sources: only their articles are
// read, and a scope with nothing in it finds nothing.
func TestRetrieveWithinChosenSources(t *testing.T) {
	lib := sampleLib()
	ps, _, err := Retrieve(context.Background(), lib, "What does QTH mean?", RetrieveOptions{Scope: Scope{Only: true, Resources: []string{"radio"}}})
	if err != nil || len(ps) == 0 || ps[0].ResourceID != "radio" {
		t.Fatalf("within radio: %+v %v", ps, err)
	}
	for _, sc := range lib.scopes {
		if !sc.Only || len(sc.Resources) != 1 || sc.Resources[0] != "radio" || sc.Own {
			t.Fatalf("search scopes = %+v", lib.scopes)
		}
	}
	// The radio archive alone has nothing on burns.
	ps, _, _ = Retrieve(context.Background(), lib, "How do I cool a burn?", RetrieveOptions{Scope: Scope{Only: true, Resources: []string{"radio"}}})
	for _, p := range ps {
		if p.ResourceID != "radio" {
			t.Fatalf("passage from outside the chosen source: %+v", p)
		}
	}
	// Several sources, and the person's own documents among them.
	ps, _, _ = Retrieve(context.Background(), lib, "Where do we meet at the water tower?", RetrieveOptions{Scope: Scope{Only: true, Resources: []string{"med"}, Own: true}})
	if len(ps) == 0 || ps[0].Kind != "user" {
		t.Fatalf("own documents among chosen sources: %+v", ps)
	}
	ps, _, _ = Retrieve(context.Background(), lib, "What does QTH mean?", RetrieveOptions{Scope: Scope{Only: true}})
	if len(ps) != 0 {
		t.Fatalf("an empty choice found %+v", ps)
	}
}

func TestRequestScope(t *testing.T) {
	sc, named, err := Request{IncludePrivate: true}.scope()
	if err != nil || sc.Only || !sc.Own || len(named) != 0 {
		t.Fatalf("whole library: %+v %v %v", sc, named, err)
	}
	sc, named, err = Request{Sources: []string{"radio", " radio", "", OwnDocuments, "med"}, IncludePrivate: true}.scope()
	if err != nil || !sc.Only || !sc.Own || strings.Join(sc.Resources, ",") != "radio,med" ||
		strings.Join(named, ",") != "radio,"+OwnDocuments+",med" {
		t.Fatalf("chosen: %+v %v %v", sc, named, err)
	}
	if _, _, err = (Request{Sources: []string{OwnDocuments}}).scope(); !errors.Is(err, ErrPrivateSource) {
		t.Fatalf("own documents without access: %v", err)
	}
}

func TestWindows(t *testing.T) {
	long := strings.Repeat("Word word word. ", 200)
	ws := windows("Title\n"+long+"\nShort line", 300)
	if len(ws) < 5 {
		t.Fatalf("expected several windows, got %d", len(ws))
	}
	for _, w := range ws {
		if len(w.text) > 900 {
			t.Fatalf("window too long: %d", len(w.text))
		}
	}
}

func TestThinkFilter(t *testing.T) {
	var f thinkFilter
	var out strings.Builder
	for _, chunk := range []string{"<thi", "nk>plan the ", "answer</th", "ink>\nCool the burn [1].", " Done <", "b>"} {
		out.WriteString(f.Write(chunk))
	}
	out.WriteString(f.Flush())
	if got := out.String(); got != "Cool the burn [1]. Done <b>" {
		t.Fatalf("filtered = %q", got)
	}
}

func TestCheckAnswer(t *testing.T) {
	ps := []Passage{{N: 1}, {N: 2}}
	ans, c := CheckAnswer("Cool the burn with water [1]. Do not use ice. [2][7]\nSee a doctor if it blisters badly or covers a large area.", ps)
	if strings.Contains(ans, "[7]") || len(c.Invalid) != 1 || c.Invalid[0] != 7 {
		t.Fatalf("invalid citation not removed: %q %+v", ans, c)
	}
	if len(c.Cited) != 2 || c.Uncited != 1 || c.NotFound || c.Unsupported {
		t.Fatalf("check = %+v", c)
	}
	ans, c = CheckAnswer(" \"I could not find this in your library.\" ", ps)
	if ans != NotFound || !c.NotFound || c.Unsupported {
		t.Fatalf("not-found answer: %q %+v", ans, c)
	}
	_, c = CheckAnswer("The answer is forty-two, as everyone knows from the books.", ps)
	if !c.Unsupported {
		t.Fatalf("uncited answer not flagged: %+v", c)
	}
}

func TestNormalizeBaseURL(t *testing.T) {
	for in, want := range map[string]string{
		"http://127.0.0.1:11434":                     "http://127.0.0.1:11434/v1",
		"http://127.0.0.1:11434/v1/":                 "http://127.0.0.1:11434/v1",
		"https://lm.example/api/v1/chat/completions": "https://lm.example/api/v1",
		"http://192.168.1.5:1234/openai":             "http://192.168.1.5:1234/openai/v1",
	} {
		got, err := NormalizeBaseURL(in)
		if err != nil || got != want {
			t.Errorf("NormalizeBaseURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "ftp://x", "http://user:pw@host/v1", "http://host/v1?x=1", "localhost:11434"} {
		if _, err := NormalizeBaseURL(bad); err == nil {
			t.Errorf("NormalizeBaseURL(%q) accepted", bad)
		}
	}
}

// fakeServer is an OpenAI-compatible chat server that streams a fixed
// answer and records requests.
type fakeServer struct {
	mu       sync.Mutex
	answer   string
	requests []map[string]any
	key      string
}

func (f *fakeServer) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"object":"list","data":[{"id":"tiny"},{"id":"other"}]}`)
	})
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"status":"ok"}`) })
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if f.key != "" && r.Header.Get("Authorization") != "Bearer "+f.key {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":{"message":"bad key"}}`)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.requests = append(f.requests, body)
		answer := f.answer
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		for _, part := range splitEvery(answer, 7) {
			b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": part}}}})
			fmt.Fprintf(w, "data: %s\n\n", b)
			w.(http.Flusher).Flush()
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":5}}\n\ndata: [DONE]\n\n")
	})
	return mux
}

func splitEvery(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	return append(out, s)
}

func TestClientStreams(t *testing.T) {
	fs := &fakeServer{answer: "<think>hmm</think>QTH means the location of the station [1].", key: "k"}
	srv := httptest.NewServer(fs.handler())
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "tiny", Extra: map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": false}}}
	var streamed strings.Builder
	res, err := c.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "hi"}}, MaxTokens: 50}, func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "QTH means the location of the station [1]." || streamed.String() != res.Text {
		t.Fatalf("text = %q streamed %q", res.Text, streamed.String())
	}
	if res.PromptTokens != 12 || res.CompletionTokens != 5 {
		t.Fatalf("usage = %+v", res)
	}
	if fs.requests[0]["chat_template_kwargs"] == nil || fs.requests[0]["max_tokens"].(float64) != 50 {
		t.Fatalf("request = %v", fs.requests[0])
	}
	models, err := c.Models(context.Background())
	if err != nil || len(models) != 2 {
		t.Fatalf("models = %v %v", models, err)
	}
	bad := &Client{BaseURL: srv.URL + "/v1", APIKey: "wrong"}
	if _, err := bad.Chat(context.Background(), ChatRequest{}, nil); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("bad key error = %v", err)
	}
}

func TestServiceAsk(t *testing.T) {
	fs := &fakeServer{answer: "QTH is the location of the station [1]."}
	srv := httptest.NewServer(fs.handler())
	defer srv.Close()
	calls := 0
	svc := New(sampleLib(), func(ctx context.Context, emit func(Event)) (Backend, error) {
		calls++
		return &Client{BaseURL: srv.URL + "/v1", Model: "tiny"}, nil
	}, func() Config { return Config{Instructions: "Use metric units."} })

	var events []string
	res, err := svc.Ask(context.Background(), Request{Question: "What does QTH mean?", Retrieval: true}, func(e Event) {
		events = append(events, e.Type+":"+e.Status)
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Answer != "QTH is the location of the station [1]." || len(res.Check.Cited) != 1 || res.Check.Unsupported {
		t.Fatalf("result = %+v", res)
	}
	if res.Passages[0].Path != "/q_codes" {
		t.Fatalf("passages = %+v", res.Passages)
	}
	joined := strings.Join(events, " ")
	for _, want := range []string{"status:searching", "passages:", "status:answering", "token:", "done:"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("events %v lack %s", events, want)
		}
	}
	msgs := fs.requests[0]["messages"].([]any)
	sys := msgs[0].(map[string]any)["content"].(string)
	user := msgs[1].(map[string]any)["content"].(string)
	if !strings.Contains(sys, "Use metric units.") || !strings.Contains(user, "[1] Common Q codes") || !strings.Contains(user, "Question: What does QTH mean?") {
		t.Fatalf("prompt:\n%s\n---\n%s", sys, user)
	}

	// Nothing relevant: the model is never asked.
	res, err = svc.Ask(context.Background(), Request{Question: "Who won the 1966 World Cup final?", Retrieval: true}, nil)
	if err != nil || !res.Skipped || res.Answer != NotFound || calls != 1 {
		t.Fatalf("irrelevant question: %+v err=%v calls=%d", res, err, calls)
	}

	// Limited to one source: the result says which, and a source without
	// the answer skips the model.
	res, err = svc.Ask(context.Background(), Request{Question: "What does QTH mean?", Retrieval: true, Sources: []string{"radio"}}, nil)
	if err != nil || res.Skipped || strings.Join(res.Sources, ",") != "radio" || res.Passages[0].ResourceID != "radio" || calls != 2 {
		t.Fatalf("within radio: %+v err=%v calls=%d", res, err, calls)
	}
	res, err = svc.Ask(context.Background(), Request{Question: "What does QTH mean?", Retrieval: true, Sources: []string{"med"}}, nil)
	if err != nil || !res.Skipped || calls != 2 {
		t.Fatalf("within med: %+v err=%v calls=%d", res, err, calls)
	}
	if _, err = svc.Ask(context.Background(), Request{Question: "Where do we meet?", Retrieval: true, Sources: []string{OwnDocuments}}, nil); !errors.Is(err, ErrPrivateSource) {
		t.Fatalf("own documents without access: %v", err)
	}
	res, _ = svc.Ask(context.Background(), Request{Question: "Who won the 1966 World Cup final?", Retrieval: true}, nil)
	if res.Sources == nil || len(res.Sources) != 0 {
		t.Fatalf("whole library sources = %#v", res.Sources)
	}

	// Retrieval off: no passages, labelled as general knowledge.
	fs.answer = "Probably England."
	res, err = svc.Ask(context.Background(), Request{Question: "Who won the 1966 World Cup final?", Retrieval: false}, nil)
	if err != nil || res.FromLibrary || len(res.Passages) != 0 || res.Answer != "Probably England." {
		t.Fatalf("general answer: %+v %v", res, err)
	}
}

func TestEvaluateRetrievalOnly(t *testing.T) {
	svc := New(sampleLib(), nil, func() Config { return Config{} })
	gs := GoldenSet{Name: "fake", Questions: []Golden{
		{Question: "What does QTH mean?", Answerable: true, Expect: []Expect{{ResourceID: "radio", Path: "q_codes"}}},
		{Question: "How long is a dash in Morse code?", Answerable: true, Expect: []Expect{{ResourceID: "radio", Path: "morse"}}},
		{Question: "Who painted the Mona Lisa?", Answerable: false},
	}}
	rep := svc.Evaluate(context.Background(), gs, false)
	if rep.RetrievalScore != 1 || rep.TopScore != 1 {
		var b strings.Builder
		rep.Print(&b)
		t.Fatalf("report:\n%s", b.String())
	}
}

func TestLoadSampleGoldenSet(t *testing.T) {
	gs, err := LoadGoldenSet("testdata/sample-golden.json")
	if err != nil || len(gs.Questions) < 6 {
		t.Fatalf("golden set: %v %v", gs, err)
	}
}

// The engine test runs this test binary as a stand-in for llamafile.
func TestMain(m *testing.M) {
	if os.Getenv("ARCANA_FAKE_ENGINE") == "1" {
		fakeEngineMain()
		return
	}
	os.Exit(m.Run())
}

func fakeEngineMain() {
	var port, key, model string
	args := os.Args
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "--port":
			port = args[i+1]
		case "--api-key":
			key = args[i+1]
		case "-m":
			model = args[i+1]
		}
	}
	if _, err := os.Stat(model); err != nil {
		fmt.Fprintln(os.Stderr, "error: failed to load model", model)
		os.Exit(1)
	}
	fs := &fakeServer{answer: "Loaded " + model + " [1].", key: key}
	_ = http.ListenAndServe("127.0.0.1:"+port, fs.handler())
}

func TestEngineLifecycle(t *testing.T) {
	dir := t.TempDir()
	model := dir + "/m.gguf"
	os.WriteFile(model, []byte("GGUF"), 0o644)
	exe, _ := os.Executable()
	e := NewEngine(nil)
	defer e.Close()
	e.command = func(name string, arg ...string) *exec.Cmd {
		cmd := exec.Command(exe, arg...)
		cmd.Env = append(os.Environ(), "ARCANA_FAKE_ENGINE=1")
		return cmd
	}
	cfg := EngineConfig{Binary: exe, Model: model, ContextSize: 2048}
	base, key, release, err := e.Acquire(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if e.Status().State != EngineReady {
		t.Fatalf("state = %+v", e.Status())
	}
	c := &Client{BaseURL: base + "/v1", APIKey: key}
	res, err := c.Chat(context.Background(), ChatRequest{Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	if err != nil || !strings.Contains(res.Text, "m.gguf") {
		t.Fatalf("chat via engine: %q %v", res.Text, err)
	}
	// Without the key the engine refuses.
	if _, err := (&Client{BaseURL: base + "/v1"}).Chat(context.Background(), ChatRequest{}, nil); err == nil {
		t.Fatal("engine answered without its key")
	}
	release()
	// Same model: reused.
	base2, _, release2, err := e.Acquire(context.Background(), cfg)
	if err != nil || base2 != base {
		t.Fatalf("not reused: %s %s %v", base, base2, err)
	}
	release2()
	// Idle timeout stops it.
	e.mu.Lock()
	e.IdleTimeout = time.Millisecond
	e.last = time.Now().Add(-time.Hour)
	e.mu.Unlock()
	e.Stop()
	if e.Status().State != EngineStopped {
		t.Fatalf("state after stop = %+v", e.Status())
	}
	// A model the engine cannot load fails with its error output.
	os.Remove(model)
	if _, _, _, err := e.Acquire(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing model error = %v", err)
	}
	if e.Status().State != EngineFailed {
		t.Fatalf("state = %+v", e.Status())
	}
}

package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/assistant"
	"github.com/KK4ODA/arcana/internal/docs"
	"github.com/KK4ODA/arcana/internal/integration"
	"github.com/KK4ODA/arcana/internal/library"
)

// fakeModel is an OpenAI-compatible server that answers from the first
// passage it is given, the way a cooperative model would.
func fakeModel(t *testing.T) (*httptest.Server, *[]string) {
	var prompts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			fmt.Fprint(w, `{"data":[{"id":"fake-1"}]}`)
			return
		}
		var body struct {
			Messages []assistant.Message `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		user := body.Messages[len(body.Messages)-1].Content
		prompts = append(prompts, user)
		answer := "According to the library, this is covered [1]."
		w.Header().Set("Content-Type", "text/event-stream")
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": answer}}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", b)
	}))
	t.Cleanup(srv.Close)
	return srv, &prompts
}

func importSample(t *testing.T, e *env) {
	t.Helper()
	if _, err := e.app.ImportFile(context.Background(), filepath.Join("..", "..", "testdata", "sample", "arcana-sample_en_2026-11.zim"), app.ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if _, ok := e.app.Kiwix.Books()["arcana.sample"]; ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("sample archive not served")
}

func sseEvents(t *testing.T, body string) map[string][]string {
	out := map[string][]string{}
	sc := bufio.NewScanner(strings.NewReader(body))
	ev := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			ev = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			out[ev] = append(out[ev], strings.TrimPrefix(line, "data: "))
		}
	}
	return out
}

func TestAssistantAnswersFromSampleLibrary(t *testing.T) {
	if kiwixBinary() == "" {
		t.Skip("kiwix-serve not available")
	}
	e := newEnv(t)
	importSample(t, e)
	model, prompts := fakeModel(t)
	host := "127.0.0.1:18470"

	// Off by default.
	rec := e.do(e.appH, "GET", "/api/assistant/ask?q=What+does+QTH+mean", local, host, nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disabled assistant answered: %d", rec.Code)
	}
	if _, err := e.app.Lib.Settings.Update(func(s *library.Settings) error {
		s.Assistant.Enabled, s.Assistant.Backend, s.Assistant.URL, s.Assistant.ExternalModel = true, "external", model.URL+"/v1", "fake-1"
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// The golden set's retrieval passes against the real archive.
	gs, err := assistant.LoadGoldenSet(filepath.Join("..", "assistant", "testdata", "sample-golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	rep := e.app.Assistant.Evaluate(context.Background(), gs, false)
	{
		var b strings.Builder
		rep.Print(&b)
		t.Log("\n" + b.String())
	}
	if rep.RetrievalScore < 1 {
		var b strings.Builder
		rep.Print(&b)
		t.Fatalf("retrieval on the sample library:\n%s", b.String())
	}

	rec = e.do(e.appH, "GET", "/api/assistant/ask?q=What+does+QTH+mean%3F", local, host, nil, nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("ask: %d %s", rec.Code, rec.Body.String())
	}
	evs := sseEvents(t, rec.Body.String())
	if len(evs["done"]) != 1 || len(evs["passages"]) != 1 || len(evs["token"]) == 0 {
		t.Fatalf("events: %v", evs)
	}
	var done assistant.Event
	json.Unmarshal([]byte(evs["done"][0]), &done)
	if done.Result == nil || done.Result.Passages[0].ResourceID != "arcana.sample" || !strings.Contains(done.Result.Passages[0].Path, "q_codes") ||
		len(done.Result.Check.Cited) != 1 || done.Result.Passages[0].Source.Edition == "" {
		t.Fatalf("result: %+v", done.Result)
	}
	if !strings.Contains((*prompts)[0], "QTH") || !strings.Contains((*prompts)[0], "Location of the station") {
		t.Fatalf("prompt lacks the passage:\n%s", (*prompts)[0])
	}

	// A question the library cannot answer never reaches the model.
	n := len(*prompts)
	rec = e.do(e.appH, "GET", "/api/assistant/ask?q=How+do+I+treat+a+snake+bite%3F", local, host, nil, nil)
	evs = sseEvents(t, rec.Body.String())
	json.Unmarshal([]byte(evs["done"][0]), &done)
	if !done.Result.Skipped || done.Result.Answer != assistant.NotFound || len(*prompts) != n {
		t.Fatalf("unanswerable: %+v", done.Result)
	}

	// Network clients may not ask until the owner allows it.
	rec = e.do(e.appH, "GET", "/api/assistant/ask?q=What+does+QTH+mean", lanClient, host, nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("network client asked: %d", rec.Code)
	}
	rec = e.do(e.appH, "GET", "/api/assistant", lanClient, host, nil, nil)
	if !strings.Contains(rec.Body.String(), `"canAsk":false`) || strings.Contains(rec.Body.String(), model.URL[7:]) {
		t.Fatalf("status for network client: %s", rec.Body.String())
	}
	e.app.Lib.Settings.Update(func(s *library.Settings) error { s.Assistant.NetworkClients = true; return nil })
	rec = e.do(e.appH, "GET", "/api/assistant/ask?q=What+does+QTH+mean", lanClient, host, nil, nil)
	if rec.Code != 200 || len(sseEvents(t, rec.Body.String())["done"]) != 1 {
		t.Fatalf("allowed network client: %d %s", rec.Code, rec.Body.String())
	}

	// Testing a server lists its models; settings never echo the key.
	var sess map[string]any
	json.Unmarshal(e.do(e.appH, "GET", "/api/session", local, host, nil, nil).Body.Bytes(), &sess)
	tok := map[string]string{"X-Arcana-Token": sess["token"].(string), "Content-Type": "application/json"}
	rec = e.do(e.appH, "POST", "/api/assistant/test", local, host, tok, strings.NewReader(`{"url":"`+model.URL+`"}`))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "fake-1") {
		t.Fatalf("test server: %d %s", rec.Code, rec.Body.String())
	}
	body := `{"assistant":{"enabled":true,"backend":"external","url":"` + model.URL + `","externalModel":"fake-1","apiKey":"secret-key","relevanceFloor":0.5,"passages":5,"contextSize":4096}}`
	rec = e.do(e.appH, "PUT", "/api/settings", local, host, tok, strings.NewReader(body))
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "secret-key") {
		t.Fatalf("save settings: %d %s", rec.Code, rec.Body.String())
	}
	if got := e.app.Lib.Settings.Get().Assistant; got.APIKey != "secret-key" || got.URL != model.URL+"/v1" {
		t.Fatalf("saved: %+v", got)
	}
	// Sending the placeholder back keeps the key.
	rec = e.do(e.appH, "PUT", "/api/settings", local, host, tok, strings.NewReader(strings.Replace(body, "secret-key", keptSecret, 1)))
	if rec.Code != 200 || e.app.Lib.Settings.Get().Assistant.APIKey != "secret-key" {
		t.Fatalf("key not kept: %d %+v", rec.Code, e.app.Lib.Settings.Get().Assistant)
	}
	rec = e.do(e.appH, "PUT", "/api/settings", local, host, tok, strings.NewReader(`{"assistant":{"enabled":true,"backend":"cloud","relevanceFloor":0.5,"passages":5,"contextSize":4096}}`))
	if rec.Code != 400 {
		t.Fatalf("invalid backend accepted: %d", rec.Code)
	}
}

func TestAssistantLocalBackendNeedsModel(t *testing.T) {
	e := newEnv(t)
	e.app.Lib.Settings.Update(func(s *library.Settings) error { s.Assistant.Enabled = true; return nil })
	rec := e.do(e.appH, "GET", "/api/assistant", local, "127.0.0.1:18470", nil, nil)
	if !strings.Contains(rec.Body.String(), "No question-answering model is installed") {
		t.Fatalf("status: %s", rec.Body.String())
	}
}

func TestMeshPrivacy(t *testing.T) {
	e := newEnv(t)
	host := "127.0.0.1:18470"
	if rec := e.do(e.appH, "GET", "/api/mesh", local, host, nil, nil); !strings.Contains(rec.Body.String(), `"state":"off"`) {
		t.Fatalf("host: %s", rec.Body.String())
	}
	if rec := e.do(e.appH, "GET", "/api/mesh", lanClient, host, nil, nil); !strings.Contains(rec.Body.String(), `"state":"hidden"`) {
		t.Fatalf("network client saw the mesh: %s", rec.Body.String())
	}
	e.app.Lib.Settings.Update(func(s *library.Settings) error { s.Mesh.ShareWithClients = true; return nil })
	if rec := e.do(e.appH, "GET", "/api/mesh", lanClient, host, nil, nil); !strings.Contains(rec.Body.String(), `"state":"off"`) {
		t.Fatalf("shared mesh hidden: %s", rec.Body.String())
	}
	var sess map[string]any
	json.Unmarshal(e.do(e.appH, "GET", "/api/session", local, host, nil, nil).Body.Bytes(), &sess)
	tok := map[string]string{"X-Arcana-Token": sess["token"].(string), "Content-Type": "application/json"}
	if rec := e.do(e.appH, "PUT", "/api/settings", local, host, tok, strings.NewReader(`{"mesh":{"source":"radio"}}`)); rec.Code != 400 {
		t.Fatalf("bad source accepted: %d", rec.Code)
	}
	if rec := e.do(e.appH, "PUT", "/api/settings", local, host, tok, strings.NewReader(`{"mesh":{"source":"tcp","address":""}}`)); rec.Code != 400 {
		t.Fatalf("empty address accepted: %d", rec.Code)
	}
}

// TestAskWithinChosenSources: a question can be limited to chosen sources.
// Only their passages are used, and a source without the answer skips the
// model. The host user's own documents can be chosen only on the host, and
// a source that is not installed is refused before anything is searched.
func TestAskWithinChosenSources(t *testing.T) {
	if kiwixBinary() == "" {
		t.Skip("kiwix-serve not available")
	}
	if assistant.OwnDocuments != docs.UserDocuments {
		t.Fatalf("own documents are %q to the assistant and %q to library search", assistant.OwnDocuments, docs.UserDocuments)
	}
	e := newEnv(t)
	importSample(t, e)
	model, prompts := fakeModel(t)
	host := "127.0.0.1:18470"
	if _, err := e.app.Lib.Settings.Update(func(s *library.Settings) error {
		s.Assistant.Enabled, s.Assistant.Backend, s.Assistant.URL, s.Assistant.ExternalModel = true, "external", model.URL+"/v1", "fake-1"
		s.Assistant.NetworkClients = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// A document of the host user's own that covers the same question.
	var sess map[string]any
	json.Unmarshal(e.do(e.appH, "GET", "/api/session", local, host, nil, nil).Body.Bytes(), &sess)
	rec := e.do(e.appH, "POST", "/api/user/documents", local, host,
		map[string]string{"X-Arcana-Token": sess["token"].(string), "X-Filename": "station%20notes.txt"},
		strings.NewReader("QTH\nLocation of the station: the garage, grid square EM73."))
	if rec.Code != 200 {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	for i := 0; i < 100; i++ {
		if strings.Contains(e.do(e.appH, "GET", "/api/search?q=garage&resource=@user", local, host, nil, nil).Body.String(), "station notes") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// The sources a question can be limited to; the host's own documents
	// only for the host.
	sources := func(remote string) map[string]string {
		var resp struct {
			Sources []app.AskSource `json:"sources"`
		}
		json.Unmarshal(e.do(e.appH, "GET", "/api/assistant", remote, host, nil, nil).Body.Bytes(), &resp)
		out := map[string]string{}
		for _, s := range resp.Sources {
			out[s.ID] = s.Kind
		}
		return out
	}
	if got := sources(local); got["arcana.sample"] != "archive" || got["@user"] != "own" {
		t.Fatalf("sources for the host: %v", got)
	}
	if got := sources(lanClient); got["arcana.sample"] != "archive" || got["@user"] != "" {
		t.Fatalf("sources for a network client: %v", got)
	}

	ask := func(remote, in string) (int, *assistant.Result, string) {
		rec := e.do(e.appH, "GET", "/api/assistant/ask?q=What+does+QTH+mean%3F&in="+in, remote, host, nil, nil)
		if rec.Code != 200 {
			return rec.Code, nil, rec.Body.String()
		}
		evs := sseEvents(t, rec.Body.String())
		if len(evs["done"]) != 1 {
			t.Fatalf("in=%s: events %v", in, evs)
		}
		var done assistant.Event
		json.Unmarshal([]byte(evs["done"][0]), &done)
		return rec.Code, done.Result, ""
	}
	kinds := func(r *assistant.Result) map[string]bool {
		out := map[string]bool{}
		for _, p := range r.Passages {
			out[p.Kind] = true
		}
		return out
	}
	// The archive alone, the host's own documents alone, both.
	_, res, _ := ask(local, "arcana.sample")
	if res.Skipped || strings.Join(res.Sources, ",") != "arcana.sample" || len(kinds(res)) != 1 || !kinds(res)["archive"] {
		t.Fatalf("within the archive: %+v", res)
	}
	_, res, _ = ask(local, "%40user")
	if res.Skipped || strings.Join(res.Sources, ",") != "@user" || len(kinds(res)) != 1 || !kinds(res)["user"] {
		t.Fatalf("within own documents: %+v", res)
	}
	if !strings.Contains((*prompts)[len(*prompts)-1], "the garage") {
		t.Fatalf("prompt lacks the own document: %s", (*prompts)[len(*prompts)-1])
	}
	_, res, _ = ask(local, "arcana.sample,%40user")
	if res.Skipped || !kinds(res)["archive"] || !kinds(res)["user"] {
		t.Fatalf("within both: %+v", res)
	}
	// The whole library: both sources answer, and the result names none.
	_, res, _ = ask(local, "")
	if res.Skipped || res.Sources == nil || len(res.Sources) != 0 || !kinds(res)["archive"] || !kinds(res)["user"] {
		t.Fatalf("whole library: %+v", res)
	}
	// A network client: the archive, never the host's own documents.
	_, res, _ = ask(lanClient, "arcana.sample")
	if res.Skipped || kinds(res)["user"] {
		t.Fatalf("network client within the archive: %+v", res)
	}
	if code, _, body := ask(lanClient, "%40user"); code != http.StatusForbidden {
		t.Fatalf("network client chose own documents: %d %s", code, body)
	}
	n := len(*prompts)
	if code, _, body := ask(local, "no.such.archive"); code != http.StatusBadRequest || !strings.Contains(body, "no.such.archive") {
		t.Fatalf("unknown source: %d %s", code, body)
	}
	if len(*prompts) != n {
		t.Fatal("a refused question reached the model")
	}

	// The integration API: chosen sources, never the host's own documents.
	_, tok, err := e.app.Integrations.Create("Radio program", []string{integration.ScopeAsk})
	if err != nil {
		t.Fatal(err)
	}
	intAsk := func(in string) (int, map[string]any) {
		rec := e.do(e.appH, "GET", "/api/integration/v1/ask?q=What+does+QTH+mean%3F&in="+in, lanClient, host,
			map[string]string{"Authorization": "Bearer " + tok}, nil)
		var m map[string]any
		json.Unmarshal(rec.Body.Bytes(), &m)
		return rec.Code, m
	}
	if code, m := intAsk("arcana.sample"); code != 200 || m["notFound"] == true || fmt.Sprint(m["sources"]) != "[arcana.sample]" {
		t.Fatalf("integration within the archive: %d %v", code, m)
	}
	if code, m := intAsk("%40user"); code != http.StatusForbidden {
		t.Fatalf("integration chose own documents: %d %v", code, m)
	}
	if code, m := intAsk("no.such.archive"); code != http.StatusBadRequest {
		t.Fatalf("integration unknown source: %d %v", code, m)
	}
}

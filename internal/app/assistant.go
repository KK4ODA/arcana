package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/assistant"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/store"
)

// Formats of the assistant's engine and models.
const (
	formatEngine = "llamafile"
	formatModel  = "gguf"
)

// openAssistant prepares question answering. The engine only starts when a
// question needs it.
func (a *App) openAssistant() {
	a.engine = assistant.NewEngine(a.Log)
	a.Assistant = assistant.New(&assistantLibrary{a: a}, a.assistantBackend, a.assistantConfig)
	a.ApplyAssistantSettings()
}

// ApplyAssistantSettings applies changed settings: a disabled assistant or
// a different backend releases the local engine's memory at once.
func (a *App) ApplyAssistantSettings() {
	if a.engine == nil {
		return
	}
	s := a.Lib.Settings.Get().Assistant
	idle := time.Duration(s.IdleMinutes) * time.Minute
	if idle <= 0 {
		idle = 10 * time.Minute
	}
	a.engine.IdleTimeout = idle
	if !s.Enabled || s.Backend != "local" {
		a.engine.Stop()
	}
}

func (a *App) assistantConfig() assistant.Config {
	s := a.Lib.Settings.Get().Assistant
	return assistant.Config{Passages: s.Passages, RelevanceFloor: s.RelevanceFloor, ContextSize: s.ContextSize,
		Instructions: s.Instructions}
}

// AssistantModel is an installed model the local engine can run.
type AssistantModel struct {
	ResourceID string `json:"resourceId"`
	Title      string `json:"title"`
	Size       int64  `json:"size"`
	Variant    string `json:"variant,omitempty"`
}

// AssistantStatus describes whether questions can be answered.
type AssistantStatus struct {
	Enabled bool   `json:"enabled"`
	Backend string `json:"backend"`
	// Ready is true when a question can be answered now or after the
	// engine starts.
	Ready bool `json:"ready"`
	// Problem explains why it is not ready.
	Problem string `json:"problem,omitempty"`
	// Engine reports the local engine (local backend only).
	Engine          assistant.EngineStatus `json:"engine"`
	EngineInstalled bool                   `json:"engineInstalled"`
	Models          []AssistantModel       `json:"models"`
	// Model is the model that will answer.
	Model string `json:"model,omitempty"`
	// Server is the external server's host (external backend only).
	Server string `json:"server,omitempty"`
	// Remote is true when questions and passages leave this computer
	// for a server that is not on the local network.
	Remote         bool `json:"remote"`
	NetworkClients bool `json:"networkClients"`
}

// AssistantStatus reports readiness for the interface.
func (a *App) AssistantStatus(ctx context.Context) AssistantStatus {
	s := a.Lib.Settings.Get().Assistant
	st := AssistantStatus{Enabled: s.Enabled, Backend: s.Backend, NetworkClients: s.NetworkClients, Models: []AssistantModel{}}
	if a.engine != nil {
		st.Engine = a.engine.Status()
	}
	engine, models := a.assistantFiles(ctx)
	st.EngineInstalled = engine != ""
	for _, m := range models {
		st.Models = append(st.Models, m.AssistantModel)
	}
	switch {
	case !s.Enabled:
		st.Problem = "Question answering is turned off (Settings → Assistant)."
	case s.Backend == "external":
		st.Model = s.ExternalModel
		if u, err := url.Parse(s.URL); err == nil {
			st.Server = u.Host
			st.Remote = !privateHost(u.Hostname())
		}
		if _, err := assistant.NormalizeBaseURL(s.URL); err != nil {
			st.Problem = "Set the model server address in Settings → Assistant."
		} else {
			st.Ready = true
		}
	default:
		m, err := pickModel(models, s.Model)
		switch {
		case err != nil:
			st.Problem = err.Error()
		case engine == "":
			st.Problem = "Download the llamafile model engine (Discover → Question answering)."
		default:
			st.Model = m.Title
			st.Ready = true
			if st.Engine.State == assistant.EngineFailed {
				st.Problem = st.Engine.Error
			}
		}
	}
	return st
}

type installedModel struct {
	AssistantModel
	path string
}

// assistantFiles finds the installed engine and models.
func (a *App) assistantFiles(ctx context.Context) (engine string, models []installedModel) {
	installed, err := a.Store.ListInstalled(ctx)
	if err != nil {
		return "", nil
	}
	for _, in := range installed {
		if in.Resource.Format != formatEngine && in.Resource.Format != formatModel {
			continue
		}
		var p string
		var size int64
		for _, f := range in.Files {
			fp, err := a.Content.FilePath(in, f)
			if err != nil {
				continue
			}
			p, size = fp, f.Size
			break
		}
		if p == "" {
			continue
		}
		if in.Resource.Format == formatEngine {
			engine = p
			continue
		}
		models = append(models, installedModel{AssistantModel{ResourceID: in.ResourceID, Title: in.Resource.Title,
			Size: size, Variant: in.Resource.Variant}, p})
	}
	// Largest first: the best answers the computer has downloaded.
	sort.Slice(models, func(i, j int) bool { return models[i].Size > models[j].Size })
	return engine, models
}

func pickModel(models []installedModel, want string) (installedModel, error) {
	if len(models) == 0 {
		return installedModel{}, errors.New("No question-answering model is installed. Download one in Discover → Question answering.")
	}
	if want == "" {
		return models[0], nil
	}
	for _, m := range models {
		if m.ResourceID == want {
			return m, nil
		}
	}
	return installedModel{}, fmt.Errorf("The chosen model (%s) is not installed. Choose another in Settings → Assistant.", want)
}

// releasingBackend releases the engine when the answer is finished.
type releasingBackend struct {
	assistant.Backend
	release func()
}

func (b *releasingBackend) Chat(ctx context.Context, req assistant.ChatRequest, onToken func(string)) (assistant.ChatResult, error) {
	defer b.release()
	return b.Backend.Chat(ctx, req, onToken)
}

// assistantBackend returns the configured backend, starting the local
// engine if needed.
func (a *App) assistantBackend(ctx context.Context, emit func(assistant.Event)) (assistant.Backend, error) {
	s := a.Lib.Settings.Get().Assistant
	if !s.Enabled {
		return nil, errors.New("question answering is turned off")
	}
	if s.Backend == "external" {
		base, err := assistant.NormalizeBaseURL(s.URL)
		if err != nil {
			return nil, err
		}
		return &assistant.Client{BaseURL: base, APIKey: s.APIKey, Model: s.ExternalModel}, nil
	}
	engine, models := a.assistantFiles(ctx)
	m, err := pickModel(models, s.Model)
	if err != nil {
		return nil, err
	}
	if engine == "" {
		return nil, errors.New("the llamafile model engine is not installed")
	}
	ctxSize := s.ContextSize
	if ctxSize < 1024 {
		ctxSize = 4096
	}
	if a.engine.Status().State != assistant.EngineReady {
		emit(assistant.Event{Type: "status", Status: "loading",
			Message: "Starting " + m.Title + ". The first answer after a while takes longer…"})
	}
	base, key, release, err := a.engine.Acquire(ctx, assistant.EngineConfig{Binary: engine, Model: m.path,
		ContextSize: ctxSize, Threads: s.Threads})
	if err != nil {
		return nil, err
	}
	return &releasingBackend{release: release, Backend: &assistant.Client{BaseURL: base + "/v1", APIKey: key, Model: "local",
		// llama.cpp's server accepts this; it switches off the long
		// hidden reasoning some models (Qwen3) produce by default.
		Extra: map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": false}}}}, nil
}

// TestAssistantServer checks an external server and lists its models.
func (a *App) TestAssistantServer(ctx context.Context, rawURL, key string) ([]string, error) {
	base, err := assistant.NormalizeBaseURL(rawURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return (&assistant.Client{BaseURL: base, APIKey: key}).Models(ctx)
}

// StopAssistantEngine frees the local engine's memory.
func (a *App) StopAssistantEngine() {
	if a.engine != nil {
		a.engine.Stop()
	}
}

func (a *App) closeAssistant() {
	if a.engine != nil {
		a.engine.Close()
	}
}

// privateHost reports whether a host name is on this computer or the local
// network (so questions sent there do not leave the premises).
func privateHost(host string) bool {
	if host == "localhost" || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".lan") || strings.HasSuffix(host, ".home.arpa") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

// ---------------------------------------------------------------- library

// assistantLibrary gives the assistant Arcana's search, text and citations.
type assistantLibrary struct{ a *App }

func (l *assistantLibrary) Search(ctx context.Context, q string, limit int, private bool) ([]assistant.Hit, error) {
	var out []assistant.Hit
	var firstErr error
	if k := l.a.Kiwix; k != nil && len(k.Books()) > 0 {
		sctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		hits, _, err := k.Search(sctx, q, nil, limit)
		cancel()
		if err != nil {
			firstErr = err
		}
		for _, h := range hits {
			out = append(out, assistant.Hit{ResourceID: h.ResourceID, Path: h.Path, Title: h.Title, Kind: "archive"})
		}
	}
	if l.a.Docs != nil {
		hits, _, err := l.a.Docs.Search(ctx, q, limit)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		for _, h := range hits {
			switch h.Source {
			case "user":
				if private {
					out = append(out, assistant.Hit{ResourceID: "", Path: h.Key, Title: h.Title, Kind: "user"})
				}
			case "resource":
				out = append(out, assistant.Hit{ResourceID: h.ResourceID, Path: strings.TrimPrefix(h.Key, "res:"+h.ResourceID+"/"),
					Title: h.Title, Kind: "document"})
			}
		}
	}
	return out, firstErr
}

func (l *assistantLibrary) Text(ctx context.Context, h assistant.Hit, max int) (string, string, error) {
	if h.Kind == "user" {
		title, text, _, err := l.a.Docs.Text(ctx, h.Path, max)
		if title == "" {
			title = path.Base(strings.TrimPrefix(h.Path, "user:"))
		}
		return title, text, err
	}
	art, _, err := l.a.ArticleText(ctx, h.ResourceID, h.Path, max)
	if err != nil {
		return "", "", err
	}
	return art.Title, art.Text, nil
}

func (l *assistantLibrary) Source(id string) (assistant.Source, bool) {
	if id == "" {
		return assistant.Source{ResourceTitle: "Your documents", Warnings: []assistant.Warning{}}, true
	}
	in, err := l.a.Store.GetInstalled(context.Background(), id)
	if err != nil {
		return assistant.Source{}, false
	}
	return sourceFor(in), true
}

func sourceFor(in store.Installed) assistant.Source {
	r := in.Resource
	src := assistant.Source{ResourceTitle: r.Title, Publisher: r.Publisher, Edition: in.Version, Published: r.Published,
		License: r.License.ID, Warnings: []assistant.Warning{}}
	for _, w := range r.Warnings {
		src.Warnings = append(src.Warnings, assistant.Warning{Kind: w.Kind, Text: w.Text})
	}
	return src
}

// AssistantSettingsValid checks settings before they are saved.
func AssistantSettingsValid(s library.AssistantSettings) error {
	switch s.Backend {
	case "local", "external":
	default:
		return fmt.Errorf("unknown assistant backend %q", s.Backend)
	}
	if s.Backend == "external" || s.URL != "" {
		if _, err := assistant.NormalizeBaseURL(s.URL); err != nil && s.Backend == "external" {
			return err
		}
	}
	if s.RelevanceFloor < 0.1 || s.RelevanceFloor > 1 {
		return errors.New("strictness must be between 0.1 and 1")
	}
	if s.Passages < 1 || s.Passages > 12 {
		return errors.New("passages must be between 1 and 12")
	}
	if s.ContextSize < 1024 || s.ContextSize > 131072 {
		return errors.New("context size must be between 1024 and 131072 tokens")
	}
	if s.Threads < 0 || s.Threads > 256 || s.IdleMinutes < 0 || s.IdleMinutes > 24*60 {
		return errors.New("invalid engine settings")
	}
	if len(s.Instructions) > 4000 || len(s.ExternalModel) > 200 || len(s.APIKey) > 500 || len(s.Model) > 128 || len(s.URL) > 500 {
		return errors.New("an assistant setting is too long")
	}
	return nil
}

// StartReader starts the archive reader without the other background
// services (for command-line questions).
func (a *App) StartReader() { a.reloadKiwix() }

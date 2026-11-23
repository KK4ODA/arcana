// Package server is Arcana's embedded HTTP server.
//
// It listens on two ports. The application origin (default 8470) serves the
// user interface and JSON API. The content origin (port+1) serves reference
// material: ZIM archives through kiwix-serve and installed documents. The two
// are separate browser origins, so scripts inside downloaded content cannot
// read the session token or call the management API.
package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/assistant"
	"github.com/KK4ODA/arcana/internal/auth"
	"github.com/KK4ODA/arcana/internal/buildinfo"
	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/content"
	"github.com/KK4ODA/arcana/internal/hotspot"
	"github.com/KK4ODA/arcana/internal/kiwix"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/mdns"
	"github.com/KK4ODA/arcana/internal/storage"
	"github.com/KK4ODA/arcana/internal/store"
	"github.com/KK4ODA/arcana/internal/tlsutil"
	"github.com/KK4ODA/arcana/internal/translate"
	"github.com/KK4ODA/arcana/internal/web"
)

// Config controls listening.
type Config struct {
	// Port for the application origin; Port+1 is the content origin.
	Port int
	// ShareOnLAN listens on all interfaces instead of loopback only.
	ShareOnLAN bool
	// OpenLibrary is called in setup mode to open a newly created library.
	OpenLibrary func(root string) (*app.App, error)
	// DefaultLibraryPath is suggested in setup mode.
	DefaultLibraryPath string
	// CaptivePort, when set, serves the appliance's captive portal: every
	// request is redirected to the library (requires ShareOnLAN).
	CaptivePort int
	Log         *slog.Logger
}

// Server serves the UI, API and content.
type Server struct {
	cfg    Config
	policy *accessPolicy
	log    *slog.Logger

	mu  sync.RWMutex
	app *app.App

	sessions *auth.Sessions
	// translateCheck remembers translation files already verified.
	translateCheck translate.Verifier
	limiter        *auth.Limiter
	nw             netState
	shares         shareStore

	appSrv     *http.Server
	contentSrv *http.Server
	appPort    int
	content    int
	startedAt  time.Time
}

// New creates a server. a may be nil (setup mode).
func New(a *app.App, cfg Config) *Server {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Port <= 0 {
		cfg.Port = 8470
	}
	s := &Server{cfg: cfg, policy: newAccessPolicy(), log: cfg.Log, app: a,
		sessions: auth.NewSessions(), limiter: auth.NewLimiter(15*time.Minute, 5, 30)}
	s.policy.remote = s.remoteSession
	s.nw.hotspot = hotspot.New()
	return s
}

func (s *Server) current() *app.App {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.app
}

// Listen binds both ports, trying successive pairs if the preferred ones are
// in use (for example when another program already uses 8470).
func (s *Server) Listen() (net.Listener, net.Listener, error) {
	host := "127.0.0.1"
	if s.cfg.ShareOnLAN {
		host = ""
	}
	var lastErr error
	for i := 0; i < 20; i++ {
		p := s.cfg.Port + 2*i
		la, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(p)))
		if err != nil {
			lastErr = err
			continue
		}
		lc, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(p+1)))
		if err != nil {
			la.Close()
			lastErr = err
			continue
		}
		s.appPort, s.content = p, p+1
		return la, lc, nil
	}
	return nil, nil, fmt.Errorf("no free port pair near %d: %w", s.cfg.Port, lastErr)
}

// AppPort returns the bound application port.
func (s *Server) AppPort() int { return s.appPort }

// Serve runs until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, la, lc net.Listener) error {
	s.startedAt = time.Now()
	// Requests inherit ctx so long-lived streams (GPS updates) end when
	// the server stops.
	s.appSrv = &http.Server{Handler: s.appHandler(), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second,
		BaseContext: func(net.Listener) context.Context { return ctx }}
	s.contentSrv = &http.Server{Handler: s.contentHandler(), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second}
	// HTTPS (when enabled) is offered on the same ports as HTTP.
	s.nw.ctx = ctx
	if s.cfg.CaptivePort > 0 && s.cfg.ShareOnLAN {
		lp, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(s.cfg.CaptivePort)))
		if err != nil {
			s.log.Warn("captive portal unavailable", "port", s.cfg.CaptivePort, "err", err)
		} else {
			cs := &http.Server{Handler: s.captiveHandler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
			go cs.Serve(lp)
			go func() { <-ctx.Done(); cs.Close() }()
			s.log.Info("captive portal", "port", s.cfg.CaptivePort)
		}
	}
	_ = s.applyTLS()
	la = tlsutil.NewSniffer(la, s.tlsConfig)
	lc = tlsutil.NewSniffer(lc, s.tlsConfig)
	s.applyMDNS()
	s.policy.refreshHosts(s.extraHosts())
	errc := make(chan error, 2)
	go func() { errc <- s.appSrv.Serve(la) }()
	go func() { errc <- s.contentSrv.Serve(lc) }()
	// Interface addresses change (Wi-Fi joins, hotspot starts); keep the
	// Host allowlist current.
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.policy.refreshHosts(s.extraHosts())
			}
		}
	}()
	select {
	case <-ctx.Done():
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.appSrv.Shutdown(shutdownCtx)
	_ = s.contentSrv.Shutdown(shutdownCtx)
	return nil
}

// Handlers are exposed for tests.
func (s *Server) appHandler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(web.Static, "static")
	files := http.FileServerFS(static)
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || !strings.Contains(path.Base(r.URL.Path), ".") {
			// Single-page app: every view is served by index.html.
			w.Header().Set("Cache-Control", "no-cache")
			data, _ := fs.ReadFile(static, "index.html")
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write(data)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	}))

	mux.HandleFunc("GET /api/session", s.handleSession)
	mux.HandleFunc("GET /api/status", s.withApp(s.handleStatus))
	mux.HandleFunc("GET /api/library", s.withApp(s.handleLibrary))
	mux.HandleFunc("DELETE /api/library/{id}", s.withApp(s.handleRemove))
	mux.HandleFunc("GET /api/search", s.withApp(s.handleSearch))
	mux.HandleFunc("GET /api/suggest", s.withApp(s.handleSuggest))
	mux.HandleFunc("GET /api/catalog", s.withApp(s.handleCatalog))
	mux.HandleFunc("GET /api/catalog/facets", s.withApp(s.handleFacets))
	mux.HandleFunc("GET /api/catalog/item/{id}", s.withApp(s.handleCatalogItem))
	mux.HandleFunc("POST /api/catalog/refresh", s.withApp(s.handleRefresh))
	mux.HandleFunc("GET /api/presets", s.withApp(s.handlePresets))
	mux.HandleFunc("POST /api/recommend", s.withApp(s.handleRecommend))
	mux.HandleFunc("POST /api/welcome/plan", s.withApp(s.handleWelcomePlan))
	mux.HandleFunc("POST /api/storage/plan", s.withApp(s.handlePlan))
	mux.HandleFunc("GET /api/downloads", s.withApp(s.handleDownloads))
	mux.HandleFunc("POST /api/downloads", s.withApp(s.handleEnqueue))
	mux.HandleFunc("POST /api/downloads/{id}/{action}", s.withApp(s.handleJobAction))
	mux.HandleFunc("DELETE /api/downloads/{id}", s.withApp(s.handleJobRemove))
	mux.HandleFunc("GET /api/updates", s.withApp(s.handleUpdates))
	mux.HandleFunc("GET /api/updates/schedule", s.adminOnly(s.withApp(s.handleUpdateSchedule)))
	mux.HandleFunc("POST /api/updates/schedule/run", s.withApp(s.handleUpdateScheduleRun))
	mux.HandleFunc("GET /api/check", s.adminOnly(s.withApp(s.handleCheck)))
	mux.HandleFunc("GET /api/settings", s.adminOnly(s.withApp(s.handleGetSettings)))
	mux.HandleFunc("PUT /api/settings", s.withApp(s.handlePutSettings))
	mux.HandleFunc("GET /api/server", s.handleServerInfo)
	mux.HandleFunc("GET /api/setup/volumes", localOnly(s.handleVolumes))
	mux.HandleFunc("POST /api/setup/init", s.handleInit)
	s.registerPhase2(mux)
	s.registerMaps(mux)
	s.registerRouting(mux)
	s.registerGPS(mux)
	s.registerAuth(mux)
	s.registerNetwork(mux)
	s.registerIntegration(mux)
	s.registerAssistant(mux)
	s.registerDrugs(mux)
	s.registerTranslate(mux)
	mux.HandleFunc("GET /api/mesh", s.withApp(s.handleMesh))
	mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "unknown API endpoint")
	})

	var h http.Handler = mux
	h = s.policy.guardMutations(h)
	h = appHeaders(s.content)(h)
	h = s.policy.guardHost(h)
	return h
}

func init() { kiwix.InjectScript = "/_arcana/frame.js" }

func (s *Server) contentHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(kiwix.URLRoot+"/", func(w http.ResponseWriter, r *http.Request) {
		a := s.current()
		if a == nil || a.Kiwix == nil {
			http.Error(w, "No library is open.", http.StatusServiceUnavailable)
			return
		}
		a.Kiwix.Handler().ServeHTTP(w, r)
	})
	mux.HandleFunc("GET /files/{id}/{name}", s.handleFile)
	mux.HandleFunc("GET /userdocs/{name}", s.handleUserDocFile)
	mux.HandleFunc("GET /_arcana/frame.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(frameScript)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Arcana content server. Open the library from the Arcana interface.", http.StatusNotFound)
	})
	var h http.Handler = mux
	h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "read-only", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		// Browsers send the app origin's cookies here too (cookies ignore
		// ports); content never needs them.
		r.Header.Del("Cookie")
		mux.ServeHTTP(w, r)
	})
	return s.policy.guardHost(h)
}

// handleFile serves an installed non-ZIM document (PDF, EPUB, ...). Only
// files recorded in a resource's sidecar can be served; the path is never
// taken from the request.
func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	a := s.current()
	if a == nil {
		http.NotFound(w, r)
		return
	}
	in, err := a.Store.GetInstalled(r.Context(), r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	for _, f := range in.Files {
		if path.Base(f.Path) != r.PathValue("name") {
			continue
		}
		p, err := a.Content.FilePath(in, f)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		serveDocument(w, r, p)
		return
	}
	http.NotFound(w, r)
}

func (s *Server) withApp(h func(http.ResponseWriter, *http.Request, *app.App)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a := s.current()
		if a == nil {
			writeError(w, http.StatusConflict, "No library is open yet. Create or select a library first.")
			return
		}
		h(w, r, a)
	}
}

// ---------------------------------------------------------------- helpers

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func itoa(n int) string { return strconv.Itoa(n) }

func errStatus(err error) int {
	switch {
	case errors.Is(err, library.ErrReadOnly):
		return http.StatusConflict
	case errors.Is(err, catalog.ErrNotFound):
		return http.StatusNotFound
	}
	var ise *storage.InsufficientSpaceError
	if errors.As(err, &ise) {
		return http.StatusInsufficientStorage
	}
	return http.StatusInternalServerError
}

// ---------------------------------------------------------------- handlers

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	a := s.current()
	resp := map[string]any{
		"version":     buildinfo.Version,
		"platform":    buildinfo.Platform(),
		"local":       isLocal(r),
		"admin":       isLocal(r),
		"secure":      r.TLS != nil,
		"setup":       a == nil,
		"contentPort": s.content,
	}
	// The token is only useful to local clients (remote clients are
	// read-only), and only same-origin scripts can read this response.
	if isLocal(r) {
		resp["token"] = s.policy.token
	} else if sess := s.remoteSession(r); sess != nil {
		resp["admin"] = true
		resp["remoteAdmin"] = true
		resp["token"] = sess.CSRF
	}
	if a != nil {
		resp["authEnabled"] = a.Admin.Enabled()
	}
	if a != nil {
		resp["readOnly"] = a.Lib.ReadOnly
		resp["readOnlyReason"] = a.Lib.ReadOnlyReason
		resp["libraryLabel"] = a.Lib.Identity.Label
	}
	writeJSON(w, http.StatusOK, resp)
}

type installedView struct {
	ResourceID   string            `json:"resourceId"`
	Title        string            `json:"title"`
	Description  string            `json:"description"`
	Version      string            `json:"version"`
	Published    string            `json:"published"`
	Category     string            `json:"category"`
	Languages    []string          `json:"languages"`
	Format       string            `json:"format"`
	Size         int64             `json:"size"`
	InstalledAt  time.Time         `json:"installedAt"`
	License      catalog.License   `json:"license"`
	Warnings     []catalog.Warning `json:"warnings"`
	Publisher    string            `json:"publisher"`
	SourceURL    string            `json:"sourceUrl"`
	Verification string            `json:"verification"`
	Unverified   bool              `json:"unverified"`
	// OpenPath is the path on the content origin, empty if the resource
	// cannot be opened (e.g. ZIM reader unavailable).
	OpenPath string `json:"openPath"`
	Search   bool   `json:"search"`

	// OpenError says why an installed archive cannot be opened.
	OpenError string `json:"openError,omitempty"`
}

func (s *Server) installedViews(r *http.Request, a *app.App) ([]installedView, error) {
	list, err := a.Store.ListInstalled(r.Context())
	if err != nil {
		return nil, err
	}
	var books map[string]kiwix.Book
	if a.Kiwix != nil {
		books = a.Kiwix.Books()
	}
	out := make([]installedView, 0, len(list))
	for _, in := range list {
		res := in.Resource
		v := installedView{
			ResourceID: in.ResourceID, Title: res.Title, Description: res.Description, Version: in.Version,
			Published: res.Published, Category: res.Category, Languages: res.Languages, Format: res.Format,
			Size: in.TotalSize(), InstalledAt: in.InstalledAt, License: res.License, Warnings: res.Warnings,
			Publisher: res.Publisher, SourceURL: res.SourceURL, Verification: in.Verification, Unverified: in.Unverified,
		}
		if v.Warnings == nil {
			v.Warnings = []catalog.Warning{}
		}
		if b, ok := books[in.ResourceID]; ok {
			v.OpenPath = b.ContentPath
			v.Search = res.Capabilities.FullTextSearch
		} else if res.Format == "gguf" || res.Format == "llamafile" || res.Format == "druglabels" ||
			res.Format == "bergamot" || res.Format == "translation-models" {
			// Models and the engine are used by the assistant; the drug
			// reference has its own view.
		} else if res.Format != "zim" && len(in.Files) > 0 {
			v.OpenPath = "/files/" + in.ResourceID + "/" + path.Base(in.Files[0].Path)
		} else if res.Format == "zim" {
			v.OpenError = "The archive reader is not available on this computer."
			if a.Kiwix != nil {
				if why := a.Kiwix.Problem(in.ResourceID); why != "" {
					v.OpenError = why
				}
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Server) handleLibrary(w http.ResponseWriter, r *http.Request, a *app.App) {
	views, err := s.installedViews(r, a)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"resources": views})
}

func (s *Server) handleRemove(w http.ResponseWriter, r *http.Request, a *app.App) {
	if a.Lib.ReadOnly {
		writeError(w, http.StatusConflict, library.ErrReadOnly.Error())
		return
	}
	if err := a.Content.Remove(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "removed"})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request, a *app.App) {
	qv := r.URL.Query()
	q := strings.TrimSpace(qv.Get("q"))
	if q == "" || len(q) > 200 {
		writeError(w, 400, "enter a search term")
		return
	}
	resource := qv.Get("resource")
	start := clampInt(qv.Get("start"), 0, 0, 10000)
	limit := clampInt(qv.Get("limit"), 25, 1, 100)
	resp := map[string]any{"query": q, "results": []kiwix.Result{}, "total": 0}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	books := map[string]kiwix.Book{}
	if a.Kiwix != nil {
		books = a.Kiwix.Books()
	}

	// One source: a page of its results, for "more results" and for the
	// search box inside the reader.
	if resource != "" {
		resp["resource"], resp["start"] = resource, start
		if _, ok := books[resource]; ok {
			results, total, err := a.Kiwix.SearchBook(ctx, resource, q, start, limit)
			switch {
			case errors.Is(err, kiwix.ErrNotSearchable):
				resp["notSearchable"] = true
			case err != nil:
				resp["error"] = "Full-text search is unavailable: " + err.Error()
			default:
				resp["results"], resp["total"] = nonNil(results), total
			}
			if start == 0 {
				titles, _ := a.Kiwix.Suggest(ctx, resource, q, 10)
				resp["titles"] = nonNil(titles)
			}
		} else {
			hits, n, err := a.Docs.SearchIn(ctx, q, resource, start, limit)
			if err != nil {
				resp["error"] = "Document search is unavailable: " + err.Error()
			}
			resp["documents"], resp["documentTotal"] = nonNil(hits), n
		}
		writeJSON(w, 200, resp)
		return
	}

	// Everything: articles whose titles match, then each archive's best
	// full-text matches, then documents.
	if a.Kiwix != nil && len(books) > 0 {
		var titles []kiwix.Suggestion
		done := make(chan struct{})
		go func() {
			defer close(done)
			titles, _ = a.Kiwix.SuggestAll(ctx, q, 5, 8)
		}()
		groups, err := a.Kiwix.SearchEach(ctx, q, 5)
		<-done
		resp["titles"] = nonNil(titles)
		if err != nil {
			resp["error"] = "Full-text search is unavailable: " + err.Error()
		}
		var results []kiwix.Result
		var shown []kiwix.Group
		titleOnly := []string{}
		total := 0
		for _, g := range groups {
			switch {
			case g.Error == kiwix.ErrNotSearchable.Error():
				titleOnly = append(titleOnly, g.ResourceID)
			case g.Total > 0 || g.Error != "":
				shown = append(shown, g)
				results = append(results, g.Results...)
				total += g.Total
			}
		}
		resp["groups"], resp["titleOnly"] = nonNil(shown), titleOnly
		resp["results"], resp["total"] = nonNil(results), total
	}
	// Documents (PDF, EPUB, user files) from Arcana's own index.
	if hits, n, err := a.Docs.Search(r.Context(), q, 25); err == nil {
		resp["documents"], resp["documentTotal"] = nonNil(hits), n
	}
	// Titles of installed resources also match, so documents without a
	// full-text index can still be found.
	views, _ := s.installedViews(r, a)
	lq := strings.ToLower(q)
	var matches []installedView
	for _, v := range views {
		if strings.Contains(strings.ToLower(v.Title+" "+v.Description), lq) {
			matches = append(matches, v)
		}
	}
	resp["resources"] = matches
	writeJSON(w, 200, resp)
}

// handleSuggest completes article titles as someone types: in one archive
// (resource=ID) or across all of them.
func (s *Server) handleSuggest(w http.ResponseWriter, r *http.Request, a *app.App) {
	qv := r.URL.Query()
	q := strings.TrimSpace(qv.Get("q"))
	if q == "" || len(q) > 200 {
		writeJSON(w, 200, map[string]any{"titles": []kiwix.Suggestion{}})
		return
	}
	limit := clampInt(qv.Get("limit"), 10, 1, 50)
	if a.Kiwix == nil {
		writeJSON(w, 200, map[string]any{"titles": []kiwix.Suggestion{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var titles []kiwix.Suggestion
	var err error
	if id := qv.Get("resource"); id != "" {
		titles, err = a.Kiwix.Suggest(ctx, id, q, limit)
	} else {
		titles, err = a.Kiwix.SuggestAll(ctx, q, 5, limit)
	}
	if err != nil {
		writeError(w, 503, "Title search is unavailable: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"titles": nonNil(titles)})
}

// clampInt parses a query parameter, using def when it is missing or not a
// number and keeping it within [lo, hi].
func clampInt(v string, def, lo, hi int) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return max(lo, min(hi, n))
}

// nonNil makes an empty result encode as [] rather than null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

type catalogItem struct {
	catalog.Resource
	Status           string `json:"status"` // available, installed, queued, update
	InstalledVersion string `json:"installedVersion,omitempty"`
	Fits             bool   `json:"fits"`
	FitReason        string `json:"fitReason,omitempty"`
}

func (s *Server) decorate(r *http.Request, a *app.App, rs []catalog.Resource) []catalogItem {
	installed := map[string]string{}
	if list, err := a.Store.ListInstalled(r.Context()); err == nil {
		for _, in := range list {
			installed[in.ResourceID] = in.Version
		}
	}
	queued := map[string]bool{}
	if jobs, err := a.Store.ListJobs(r.Context()); err == nil {
		for _, j := range jobs {
			if j.Active() {
				queued[j.ResourceID] = true
			}
		}
	}
	vol, _ := storage.Stat(a.Lib.Root)
	out := make([]catalogItem, 0, len(rs))
	for _, res := range rs {
		it := catalogItem{Resource: res, Status: "available", Fits: true}
		if v, ok := installed[res.ID]; ok {
			it.InstalledVersion = v
			it.Status = "installed"
			if catalog.CompareVersions(res.Version, v) > 0 {
				it.Status = "update"
			}
		}
		if queued[res.ID] {
			it.Status = "queued"
		}
		for _, f := range res.Files {
			size := f.Size
			if size == 0 {
				size = res.DownloadSize
			}
			if ok, why := storage.CanStore(vol.FSType, size); !ok {
				it.Fits, it.FitReason = false, why
			}
		}
		if it.Warnings == nil {
			it.Warnings = []catalog.Warning{}
		}
		out = append(out, it)
	}
	return out
}

func (s *Server) handleCatalog(w http.ResponseWriter, r *http.Request, a *app.App) {
	qv := r.URL.Query()
	limit, _ := strconv.Atoi(qv.Get("limit"))
	offset, _ := strconv.Atoi(qv.Get("offset"))
	rs, total, err := a.Store.SearchResources(r.Context(), store.Query{
		Text: qv.Get("q"), Category: qv.Get("category"), Language: qv.Get("lang"),
		Provider: qv.Get("provider"), Format: qv.Get("format"), Sort: qv.Get("sort"),
		Limit: limit, Offset: offset,
	})
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"total": total, "resources": s.decorate(r, a, rs)})
}

func (s *Server) handleFacets(w http.ResponseWriter, r *http.Request, a *app.App) {
	cats, _ := a.Store.CategoryCounts(r.Context(), r.URL.Query().Get("lang"))
	langs, _ := a.Store.LanguageCounts(r.Context())
	providers, _ := a.Store.Providers(r.Context())
	counts := map[string]int{}
	for _, c := range cats {
		counts[c.Key] = c.Count
	}
	type cat struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		Count int    `json:"count"`
	}
	var out []cat
	for _, c := range catalog.Categories {
		out = append(out, cat{c.ID, c.Title, counts[c.ID]})
	}
	writeJSON(w, 200, map[string]any{"categories": out, "languages": langs, "providers": providers})
}

func (s *Server) handleCatalogItem(w http.ResponseWriter, r *http.Request, a *app.App) {
	res, err := a.Store.GetResource(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	var alternatives []catalogItem
	if res.Family != "" {
		fam, _ := a.Store.FamilyVariants(r.Context(), []string{res.Family})
		alternatives = s.decorate(r, a, fam[res.Family])
	}
	writeJSON(w, 200, map[string]any{"resource": s.decorate(r, a, []catalog.Resource{res})[0], "alternatives": alternatives})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request, a *app.App) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	results, err := a.RefreshCatalog(ctx)
	if err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"results": results})
}

func (s *Server) handlePresets(w http.ResponseWriter, r *http.Request, a *app.App) {
	presets, err := a.Presets()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"presets": presets})
}

func (s *Server) handleRecommend(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		Preset   string `json:"preset"`
		Language string `json:"language"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.Language == "" {
		req.Language = "en"
	}
	rec, err := a.Recommend(r.Context(), req.Preset, req.Language)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, rec)
}

func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		ResourceIDs []string `json:"resourceIds"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if len(req.ResourceIDs) > 5000 {
		writeError(w, 400, "selection too large")
		return
	}
	rep, err := a.Storage(r.Context(), req.ResourceIDs)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, rep)
}

func (s *Server) handleDownloads(w http.ResponseWriter, r *http.Request, a *app.App) {
	views, err := a.Downloads.List(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"jobs": views})
}

func (s *Server) handleEnqueue(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		ResourceIDs []string `json:"resourceIds"`
	}
	if err := readJSON(r, &req); err != nil || len(req.ResourceIDs) == 0 || len(req.ResourceIDs) > 500 {
		writeError(w, 400, "provide between 1 and 500 resource IDs")
		return
	}
	writeJSON(w, 200, map[string]any{"results": a.EnqueueByID(r.Context(), req.ResourceIDs)})
}

func (s *Server) handleJobAction(w http.ResponseWriter, r *http.Request, a *app.App) {
	id := r.PathValue("id")
	var err error
	switch r.PathValue("action") {
	case "pause":
		err = a.Downloads.Pause(r.Context(), id)
	case "resume":
		err = a.Downloads.Resume(r.Context(), id)
	case "cancel":
		err = a.Downloads.Cancel(r.Context(), id)
	default:
		writeError(w, 404, "unknown action")
		return
	}
	if err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) handleJobRemove(w http.ResponseWriter, r *http.Request, a *app.App) {
	if err := a.Downloads.Remove(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) handleUpdates(w http.ResponseWriter, r *http.Request, a *app.App) {
	ups, err := a.Updates(r.Context())
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	providers, _ := a.Store.Providers(r.Context())
	if ups == nil {
		ups = []app.Update{}
	}
	writeJSON(w, 200, map[string]any{"updates": ups, "providers": providers})
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request, a *app.App) {
	full := r.URL.Query().Get("full") == "1"
	rep, err := a.Content.Check(r.Context(), full)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if rep.Problems == nil {
		rep.Problems = []content.Problem{}
	}
	writeJSON(w, 200, rep)
}

// keptSecret stands in for a saved secret in settings sent to the
// browser; sending it back leaves the secret unchanged.
const keptSecret = "(saved)"

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request, a *app.App) {
	set := a.Lib.Settings.Get()
	if set.Assistant.APIKey != "" {
		set.Assistant.APIKey = keptSecret
	}
	vol, _ := storage.Stat(a.Lib.Root)
	writeJSON(w, 200, map[string]any{
		"settings":       set,
		"defaultReserve": library.DefaultReserve(int64(vol.TotalBytes)),
		"libraryRoot":    a.Lib.Root,
		"libraryId":      a.Lib.Identity.LibraryID,
		"readOnly":       a.Lib.ReadOnly,
	})
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		ReserveBytes                 *int64                         `json:"reserveBytes"`
		MaxConcurrentDownloads       *int                           `json:"maxConcurrentDownloads"`
		BandwidthLimit               *int64                         `json:"bandwidthLimit"`
		AllowPrivateNetworkDownloads *bool                          `json:"allowPrivateNetworkDownloads"`
		ShareOnLAN                   *bool                          `json:"shareOnLan"`
		KiwixEnabled                 *bool                          `json:"kiwixEnabled"`
		GutenbergEnabled             *bool                          `json:"gutenbergEnabled"`
		OpenFDAEnabled               *bool                          `json:"openfdaEnabled"`
		AutoRefreshHours             *int                           `json:"autoRefreshHours"`
		UpdateURL                    *string                        `json:"updateUrl"`
		AutoUpdateCheck              *bool                          `json:"autoUpdateCheck"`
		Feeds                        *[]library.FeedSource          `json:"feeds"`
		MapsEnabled                  *bool                          `json:"mapsEnabled"`
		MDNS                         *bool                          `json:"mdns"`
		MDNSName                     *string                        `json:"mdnsName"`
		GPS                          *library.GPSSettings           `json:"gps"`
		Assistant                    *library.AssistantSettings     `json:"assistant"`
		ContentUpdates               *library.ContentUpdateSettings `json:"contentUpdates"`
		WelcomeDone                  *bool                          `json:"welcomeDone"`
		Mesh                         *library.MeshSettings          `json:"mesh"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	next, err := a.Lib.Settings.Update(func(st *library.Settings) error {
		if req.ReserveBytes != nil {
			if *req.ReserveBytes < 0 {
				return errors.New("reserve cannot be negative")
			}
			st.ReserveBytes = *req.ReserveBytes
		}
		if req.MaxConcurrentDownloads != nil {
			if *req.MaxConcurrentDownloads < 1 || *req.MaxConcurrentDownloads > 8 {
				return errors.New("concurrent downloads must be between 1 and 8")
			}
			st.MaxConcurrentDownloads = *req.MaxConcurrentDownloads
		}
		if req.BandwidthLimit != nil {
			if *req.BandwidthLimit < 0 {
				return errors.New("bandwidth limit cannot be negative")
			}
			st.BandwidthLimit = *req.BandwidthLimit
		}
		if req.AllowPrivateNetworkDownloads != nil {
			st.AllowPrivateNetworkDownloads = *req.AllowPrivateNetworkDownloads
		}
		if req.ShareOnLAN != nil {
			st.Server.ShareOnLAN = *req.ShareOnLAN
		}
		if req.KiwixEnabled != nil {
			st.Catalog.KiwixEnabled = *req.KiwixEnabled
		}
		if req.GutenbergEnabled != nil {
			st.Catalog.GutenbergEnabled = *req.GutenbergEnabled
		}
		if req.OpenFDAEnabled != nil {
			st.Catalog.OpenFDAEnabled = *req.OpenFDAEnabled
		}
		if req.AutoRefreshHours != nil {
			if *req.AutoRefreshHours < 0 || *req.AutoRefreshHours > 24*90 {
				return errors.New("automatic refresh interval must be between 0 (off) and 2160 hours")
			}
			st.Catalog.AutoRefreshHours = *req.AutoRefreshHours
		}
		if req.UpdateURL != nil {
			u := strings.TrimSpace(*req.UpdateURL)
			if u != "" && !strings.HasPrefix(u, "https://") {
				return errors.New("the update address must start with https://")
			}
			st.Updates.URL = u
		}
		if req.AutoUpdateCheck != nil {
			st.Updates.AutoCheck = *req.AutoUpdateCheck
		}
		if req.Feeds != nil {
			seen := map[string]bool{"arcana": true, "kiwix": true, "gutenberg": true, "maps": true, "local": true, "roads": true, "openfda": true}
			for _, f := range *req.Feeds {
				probe := catalog.Resource{ID: f.ID + ".x", Provider: f.ID, Title: "x", Version: "1", Format: "pdf",
					Files: []catalog.File{{Name: "x.pdf", Mirrors: []catalog.Mirror{{URL: f.URL}}}}}
				if err := probe.Validate(); err != nil || seen[f.ID] {
					return fmt.Errorf("feed %q: use a unique lower-case ID and an http(s) URL", f.ID)
				}
				if f.PublicKey != "" {
					if k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(f.PublicKey)); err != nil || len(k) != 32 {
						return fmt.Errorf("feed %q: the publisher key must be a base64 Ed25519 public key", f.ID)
					}
				}
				seen[f.ID] = true
			}
			st.Catalog.Feeds = *req.Feeds
		}
		if req.MapsEnabled != nil {
			st.Maps.Enabled = *req.MapsEnabled
		}
		if req.MDNS != nil {
			st.Server.MDNS = *req.MDNS
		}
		if req.MDNSName != nil {
			label := mdns.SanitizeLabel(*req.MDNSName)
			if label == "" || label != strings.ToLower(strings.TrimSpace(*req.MDNSName)) {
				return errors.New("the network name may use letters, digits and hyphens only")
			}
			st.Server.Name = label
		}
		if req.GPS != nil {
			g := *req.GPS
			switch g.Source {
			case "", "serial", "udp", "tcp", "gpsd", "simulator", "manual":
			default:
				return fmt.Errorf("unknown position source %q", g.Source)
			}
			if g.BaudRate < 0 || g.BaudRate > 921600 || g.UDPPort < 0 || g.UDPPort > 65535 ||
				g.SimulatorSpeed < 0 || g.SimulatorSpeed > 100 {
				return errors.New("invalid GPS settings")
			}
			if g.Source == "manual" && (g.ManualLat < -90 || g.ManualLat > 90 || g.ManualLon < -180 || g.ManualLon > 180) {
				return errors.New("latitude must be -90..90 and longitude -180..180")
			}
			if len(g.SerialPort) > 200 || len(g.Address) > 200 || len(g.SimulatorTrack) > 200 {
				return errors.New("invalid GPS settings")
			}
			st.GPS = g
		}
		if req.Mesh != nil {
			ms := *req.Mesh
			switch ms.Source {
			case "", "tcp", "serial":
			default:
				return fmt.Errorf("unknown radio connection %q", ms.Source)
			}
			if len(ms.Address) > 200 || len(ms.SerialPort) > 200 || ms.BaudRate < 0 || ms.BaudRate > 921600 {
				return errors.New("invalid radio settings")
			}
			if ms.Source == "tcp" && strings.TrimSpace(ms.Address) == "" {
				return errors.New("enter the radio's address, for example 192.168.1.40")
			}
			ms.Address = strings.TrimSpace(ms.Address)
			st.Mesh = ms
		}
		if req.WelcomeDone != nil {
			st.WelcomeDone = *req.WelcomeDone
		}
		if req.ContentUpdates != nil {
			cu := *req.ContentUpdates
			if cu.WindowStart < 0 || cu.WindowStart > 23 || cu.WindowEnd < 0 || cu.WindowEnd > 23 {
				return errors.New("update hours must be between 0 and 23")
			}
			if cu.EveryDays < 1 || cu.EveryDays > 90 {
				return errors.New("check interval must be between 1 and 90 days")
			}
			if cu.BandwidthLimit < 0 {
				return errors.New("bandwidth limit cannot be negative")
			}
			if cu.Only != "all" && cu.Only != "important" {
				return errors.New(`updates to install must be "all" or "important"`)
			}
			st.ContentUpdates = cu
		}
		if req.Assistant != nil {
			as := *req.Assistant
			as.URL = strings.TrimSpace(as.URL)
			if as.APIKey == keptSecret {
				as.APIKey = st.Assistant.APIKey
			}
			if err := app.AssistantSettingsValid(as); err != nil {
				return err
			}
			if as.Backend == "external" {
				as.URL, _ = assistant.NormalizeBaseURL(as.URL)
			}
			st.Assistant = as
		}
		return nil
	})
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	a.Downloads.ApplySettings()
	if req.ContentUpdates != nil {
		// Applies the window at once (pausing or resuming scheduled
		// downloads) without waiting for the next minute.
		go a.ApplyContentUpdateSettings()
	}
	if req.GPS != nil {
		a.ApplyGPSSettings()
	}
	if req.Mesh != nil {
		go a.ApplyMeshSettings()
	}
	if req.Assistant != nil {
		a.ApplyAssistantSettings()
	}
	if next.Assistant.APIKey != "" {
		next.Assistant.APIKey = keptSecret
	}
	if req.MDNS != nil || req.MDNSName != nil {
		s.applyMDNS()
		s.policy.refreshHosts(s.extraHosts())
	}
	writeJSON(w, 200, map[string]any{"settings": next, "restartRequired": req.ShareOnLAN != nil})
}

func (s *Server) handleVolumes(w http.ResponseWriter, r *http.Request) {
	vols, err := storage.ListVolumes()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	type vol struct {
		storage.Volume
		FS       storage.FSInfo `json:"fs"`
		Nominal  int64          `json:"nominal"`
		Existing string         `json:"existingLibrary,omitempty"`
	}
	out := []vol{}
	for _, v := range vols {
		n, _ := storage.NominalCapacity(v.TotalBytes)
		item := vol{Volume: v, FS: storage.FileSystemInfo(v.FSType), Nominal: int64(n)}
		if root, ok := library.Find(v.MountPoint, 0); ok {
			item.Existing = root
		}
		out = append(out, item)
	}
	writeJSON(w, 200, map[string]any{"volumes": out, "suggestedPath": s.cfg.DefaultLibraryPath})
}

func (s *Server) handleInit(w http.ResponseWriter, r *http.Request) {
	if s.current() != nil {
		writeError(w, http.StatusConflict, "A library is already open.")
		return
	}
	var req struct {
		Path  string `json:"path"`
		Label string `json:"label"`
	}
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.Path) == "" {
		writeError(w, 400, "choose a folder for the library")
		return
	}
	root := filepath.Clean(req.Path)
	if !filepath.IsAbs(root) {
		writeError(w, 400, "use a full path, for example E:\\Arcana")
		return
	}
	if !library.IsLibrary(root) {
		if _, err := library.Init(root, req.Label); err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	if s.cfg.OpenLibrary == nil {
		writeError(w, 500, "cannot open library")
		return
	}
	a, err := s.cfg.OpenLibrary(root)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	s.mu.Lock()
	s.app = a
	s.mu.Unlock()
	_ = s.applyTLS()
	s.applyMDNS()
	writeJSON(w, 200, map[string]string{"root": root})
}

// SetApp replaces the open library (used after setup).
func (s *Server) SetApp(a *app.App) {
	s.mu.Lock()
	s.app = a
	s.mu.Unlock()
}

// App returns the open library, if any.
func (s *Server) App() *app.App { return s.current() }

func (s *Server) handleUpdateSchedule(w http.ResponseWriter, r *http.Request, a *app.App) {
	writeJSON(w, http.StatusOK, a.ContentUpdateStatus())
}

// handleUpdateScheduleRun checks for content updates now and queues what
// fits, whatever the window.
func (s *Server) handleUpdateScheduleRun(w http.ResponseWriter, r *http.Request, a *app.App) {
	if a.Lib.ReadOnly {
		writeError(w, http.StatusConflict, "The library is read-only.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Minute)
	defer cancel()
	ev := a.RunContentUpdatesNow(ctx)
	writeJSON(w, http.StatusOK, map[string]any{"result": ev, "status": a.ContentUpdateStatus()})
}

// handleWelcomePlan turns the Getting started choices into a download plan.
func (s *Server) handleWelcomePlan(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req app.WelcomeRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	plan, err := a.PlanWelcome(r.Context(), req)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, plan)
}

// handleMesh reports the Meshtastic radio's nodes and messages: to the
// host and administrators, and to network clients only when shared.
func (s *Server) handleMesh(w http.ResponseWriter, r *http.Request, a *app.App) {
	set := a.Lib.Settings.Get().Mesh
	if !s.isAdmin(r) && !set.ShareWithClients {
		writeJSON(w, http.StatusOK, map[string]any{"state": "hidden", "nodes": []any{}, "messages": []any{}, "shared": false})
		return
	}
	st := a.Mesh.Status()
	writeJSON(w, http.StatusOK, map[string]any{"state": st.State, "error": st.Error, "source": st.Source, "myNode": st.MyNode,
		"nodes": st.Nodes, "messages": st.Messages, "since": st.Since, "shared": set.ShareWithClients})
}

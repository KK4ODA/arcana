package server

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/buildinfo"
	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/integration"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/maps"
)

// The integration API (v1) lets other programs on this computer or the
// network use the library: search with citations, article text, location,
// coordinates and copying resources between Arcana libraries. Every call
// needs a bearer token with the right scope; nothing in v1 changes the
// library. Field names are stable within v1; additions only.
const integrationPrefix = "/api/integration/v1"

type ctxKey int

const clientKey ctxKey = 0

func (s *Server) registerIntegration(mux *http.ServeMux) {
	p := integrationPrefix
	mux.HandleFunc("GET "+p+"/{$}", s.withApp(s.handleIntegrationInfo))
	mux.HandleFunc("POST "+p+"/pair", s.withApp(s.handlePairRequest))
	mux.HandleFunc("GET "+p+"/pair/{id}", s.withApp(s.handlePairPoll))
	mux.HandleFunc("GET "+p+"/search", s.withApp(s.scoped(integration.ScopeSearch, s.handleIntSearch)))
	mux.HandleFunc("GET "+p+"/documents/{resource}/text", s.withApp(s.scoped(integration.ScopeDocs, s.handleIntText)))
	mux.HandleFunc("GET "+p+"/documents/{resource}/excerpt", s.withApp(s.scoped(integration.ScopeDocs, s.handleIntExcerpt)))
	mux.HandleFunc("POST "+p+"/share/excerpt", s.withApp(s.scoped(integration.ScopeShare, s.handleIntShare)))
	mux.HandleFunc("GET "+p+"/location", s.withApp(s.scoped(integration.ScopeLocation, s.handleIntLocation)))
	mux.HandleFunc("GET "+p+"/maps/reference", s.withApp(s.scoped(integration.ScopeMaps, s.handleIntReference)))
	mux.HandleFunc("GET "+p+"/events", s.withApp(s.handleIntEvents))
	mux.HandleFunc("GET "+p+"/library", s.withApp(s.scoped(integration.ScopeLibrary, s.handleIntLibrary)))
	mux.HandleFunc("GET "+p+"/files/{id}/{name}", s.withApp(s.scoped(integration.ScopeLibrary, s.handleIntFile)))
	mux.HandleFunc("GET "+p+"/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "unknown integration endpoint")
	})

	// Host-only management (the operator's side of pairing) and peers.
	mux.HandleFunc("GET /api/integrations", localOnly(s.withApp(s.handleIntegrations)))
	mux.HandleFunc("POST /api/integrations", s.withApp(s.handleCreateIntegration))
	mux.HandleFunc("DELETE /api/integrations/{id}", s.withApp(s.handleRevokeIntegration))
	mux.HandleFunc("POST /api/integrations/pair/{id}/{decision}", s.withApp(s.handlePairDecision))
	mux.HandleFunc("GET /api/peers", localOnly(s.withApp(s.handlePeers)))
	mux.HandleFunc("POST /api/peers", s.withApp(s.handleAddPeer))
	mux.HandleFunc("DELETE /api/peers/{id}", s.withApp(s.handleRemovePeer))
	mux.HandleFunc("GET /api/peers/{id}/library", localOnly(s.withApp(s.handlePeerLibrary)))
	mux.HandleFunc("POST /api/peers/{id}/pull", s.withApp(s.handlePeerPull))
	// Short links created by programs with documents:share.
	mux.HandleFunc("GET /s/{code}", s.handleShareLink)
}

func buildVersion() string { return buildinfo.Version }

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func (s *Server) client(r *http.Request, a *app.App) (integration.Client, bool) {
	tok := bearer(r)
	if tok == "" {
		return integration.Client{}, false
	}
	return a.Integrations.Authenticate(tok)
}

func (s *Server) scoped(scope string, h func(http.ResponseWriter, *http.Request, *app.App)) func(http.ResponseWriter, *http.Request, *app.App) {
	return func(w http.ResponseWriter, r *http.Request, a *app.App) {
		c, ok := s.client(r, a)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="arcana"`)
			writeError(w, http.StatusUnauthorized, "A valid access token is required. Pair this program with Arcana first.")
			return
		}
		if !c.Has(scope) {
			writeError(w, http.StatusForbidden, "This token does not allow "+scope+".")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), clientKey, c)), a)
	}
}

func (s *Server) handleIntegrationInfo(w http.ResponseWriter, r *http.Request, a *app.App) {
	resp := map[string]any{"api": "arcana-integration", "version": 1, "server": buildVersion(), "authenticated": false,
		"scopes": integration.AllScopes}
	if c, ok := s.client(r, a); ok {
		resp["authenticated"], resp["client"], resp["granted"] = true, c.Name, c.Scopes
		resp["library"] = map[string]string{"id": a.Lib.Identity.LibraryID, "label": a.Lib.Identity.Label}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePairRequest(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	p, secret, err := a.Integrations.RequestPairing(req.Name, req.Scopes, clientAddr(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.log.Info("integration pairing requested", "name", p.Name, "from", p.Addr, "code", p.Code)
	writeJSON(w, http.StatusAccepted, map[string]any{"id": p.ID, "code": p.Code, "secret": secret, "expires": p.Expires,
		"message": "Ask the Arcana operator to approve this request (Settings → Integrations) and compare the code."})
}

func (s *Server) handlePairPoll(w http.ResponseWriter, r *http.Request, a *app.App) {
	status, token := a.Integrations.Poll(r.PathValue("id"), r.URL.Query().Get("secret"))
	resp := map[string]any{"status": status}
	if token != "" {
		resp["token"] = token
	}
	code := http.StatusOK
	if status == "unknown" {
		code = http.StatusNotFound
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, code, resp)
}

// ---------------------------------------------------------------- content

type intResult struct {
	ResourceID string `json:"resourceId"`
	Title      string `json:"title"`
	Snippet    string `json:"snippet"`
	Path       string `json:"path"`
	// Page is the best matching page of a PDF document (from 1), when
	// the result is one.
	Page     int          `json:"page,omitempty"`
	Citation app.Citation `json:"citation"`
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func plainSnippet(s string) string {
	return strings.TrimSpace(html.UnescapeString(tagRe.ReplaceAllString(s, "")))
}

func (s *Server) citations(r *http.Request, a *app.App) map[string]app.Citation {
	out := map[string]app.Citation{}
	if list, err := a.Store.ListInstalled(r.Context()); err == nil {
		for _, in := range list {
			out[in.ResourceID] = app.CitationFor(in)
		}
	}
	return out
}

func (s *Server) handleIntSearch(w http.ResponseWriter, r *http.Request, a *app.App) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len(q) > 200 {
		writeError(w, http.StatusBadRequest, "q is required (at most 200 characters)")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	cits := s.citations(r, a)
	results := []intResult{}
	var searchErr string
	if a.Kiwix != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		hits, _, err := a.Kiwix.Search(ctx, q, nil, limit)
		cancel()
		if err != nil {
			searchErr = err.Error()
		}
		for _, h := range hits {
			if c, ok := cits[h.ResourceID]; ok {
				results = append(results, intResult{ResourceID: h.ResourceID, Title: h.Title, Snippet: plainSnippet(h.Snippet), Path: h.Path, Citation: c})
			}
		}
	}
	if hits, _, err := a.Docs.Search(r.Context(), q, limit); err == nil {
		for _, h := range hits {
			// The host user's own documents are private.
			c, ok := cits[h.ResourceID]
			if h.Source != "resource" || !ok {
				continue
			}
			res := intResult{ResourceID: h.ResourceID, Title: h.Title, Snippet: plainSnippet(h.Snippet),
				Path: strings.TrimPrefix(h.Key, "res:"+h.ResourceID+"/"), Citation: c}
			if h.Paged && len(h.Pages) > 0 {
				res.Page = h.Pages[0].N
			}
			results = append(results, res)
		}
	}
	if len(results) > limit {
		results = results[:limit]
	}
	resp := map[string]any{"query": q, "results": results}
	if searchErr != "" && len(results) == 0 {
		resp["error"] = "Archive search is unavailable: " + searchErr
	}
	writeJSON(w, http.StatusOK, resp)
}

func boundedInt(v string, def, max int) int {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func (s *Server) articleErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		writeError(w, http.StatusNotFound, "resource not installed")
	case errors.Is(err, app.ErrBadPath):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusNotFound, err.Error())
	}
}

func (s *Server) handleIntText(w http.ResponseWriter, r *http.Request, a *app.App) {
	max := boundedInt(r.URL.Query().Get("max"), 20000, 2<<20)
	art, cit, err := a.ArticleText(r.Context(), r.PathValue("resource"), r.URL.Query().Get("path"), max)
	if err != nil {
		s.articleErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"article": art, "citation": cit})
}

func (s *Server) handleIntExcerpt(w http.ResponseWriter, r *http.Request, a *app.App) {
	max := boundedInt(r.URL.Query().Get("max"), 1200, 4000)
	art, cit, err := a.ArticleText(r.Context(), r.PathValue("resource"), r.URL.Query().Get("path"), max)
	if err != nil {
		s.articleErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"title": art.Title, "path": art.Path, "excerpt": art.Text,
		"truncated": art.Truncated, "footer": cit.Footer(), "citation": cit})
}

// ---------------------------------------------------------------- shares

type share struct {
	resource, path string
	expires        time.Time
}

type shareStore struct {
	mu sync.Mutex
	m  map[string]share
}

const shareAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

func (s *Server) handleIntShare(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		Resource string `json:"resource"`
		Path     string `json:"path"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	// Validates that the page exists and belongs to the resource.
	art, _, err := a.ArticleText(r.Context(), req.Resource, req.Path, 1)
	if err != nil {
		s.articleErr(w, err)
		return
	}
	b := make([]byte, 8)
	rand.Read(b)
	code := make([]byte, 8)
	for i := range b {
		code[i] = shareAlphabet[int(b[i])%len(shareAlphabet)]
	}
	s.shares.mu.Lock()
	if s.shares.m == nil {
		s.shares.m = map[string]share{}
	}
	now := time.Now()
	for k, v := range s.shares.m {
		if now.After(v.expires) {
			delete(s.shares.m, k)
		}
	}
	if len(s.shares.m) >= 500 {
		s.shares.mu.Unlock()
		writeError(w, http.StatusTooManyRequests, "too many active links")
		return
	}
	exp := now.Add(24 * time.Hour)
	s.shares.m[string(code)] = share{resource: req.Resource, path: art.Path, expires: exp}
	s.shares.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"code": string(code), "path": "/s/" + string(code), "expires": exp,
		"urls": s.shareURLs(string(code))})
}

func (s *Server) shareURLs(code string) []string {
	var out []string
	if s.cfg.ShareOnLAN {
		for _, ip := range lanAddresses() {
			out = append(out, fmt.Sprintf("http://%s:%d/s/%s", ip, s.appPort, code))
		}
	}
	return append(out, fmt.Sprintf("http://127.0.0.1:%d/s/%s", s.appPort, code))
}

func (s *Server) handleShareLink(w http.ResponseWriter, r *http.Request) {
	s.shares.mu.Lock()
	sh, ok := s.shares.m[r.PathValue("code")]
	s.shares.mu.Unlock()
	if !ok || time.Now().After(sh.expires) {
		http.Error(w, "This link has expired.", http.StatusNotFound)
		return
	}
	target := "/#/read/" + url.PathEscape(sh.resource)
	if strings.HasPrefix(sh.path, "/kiwix/content/") {
		target += "?path=" + url.QueryEscape(sh.path)
	}
	http.Redirect(w, r, target, http.StatusFound)
}

// ---------------------------------------------------------------- location

func (s *Server) handleIntLocation(w http.ResponseWriter, r *http.Request, a *app.App) {
	st := a.GPS.Status()
	if st.Fix == nil || st.State == "lost" || st.State == "off" || st.State == "error" {
		writeJSON(w, http.StatusNotFound, map[string]any{"state": st.State, "error": "no current position"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"state": st.State, "stale": st.State == "stale", "age": st.Age,
		"source": st.Source, "fix": st.Fix, "maidenhead": maps.Maidenhead(st.Fix.Lat, st.Fix.Lon)})
}

func (s *Server) handleIntReference(w http.ResponseWriter, r *http.Request, a *app.App) {
	lat, err1 := strconv.ParseFloat(r.URL.Query().Get("lat"), 64)
	lon, err2 := strconv.ParseFloat(r.URL.Query().Get("lon"), 64)
	if err1 != nil || err2 != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		writeError(w, http.StatusBadRequest, "lat and lon are required (decimal degrees)")
		return
	}
	resp := map[string]any{
		"lat": lat, "lon": lon,
		"decimal":    fmt.Sprintf("%.5f, %.5f", lat, lon),
		"dms":        maps.DMS(lat, "N", "S") + " " + maps.DMS(lon, "E", "W"),
		"ddm":        maps.DDM(lat, "N", "S") + " " + maps.DDM(lon, "E", "W"),
		"maidenhead": maps.Maidenhead(lat, lon),
	}
	near, err := a.NearbyPlaces(r.Context(), lat, lon, 50000, 3)
	if err == nil {
		resp["nearby"] = near
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleIntEvents streams location (scope location:read) and library
// changes (scope library:read or documents:read) as server-sent events.
func (s *Server) handleIntEvents(w http.ResponseWriter, r *http.Request, a *app.App) {
	c, ok := s.client(r, a)
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer realm="arcana"`)
		writeError(w, http.StatusUnauthorized, "A valid access token is required.")
		return
	}
	wantLoc := c.Has(integration.ScopeLocation)
	wantLib := c.Has(integration.ScopeLibrary) || c.Has(integration.ScopeDocs)
	if !wantLoc && !wantLib {
		writeError(w, http.StatusForbidden, "This token allows no events.")
		return
	}
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	send := func(event string, v any) bool {
		data, _ := json.Marshal(v)
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	loc, unsubLoc := a.GPS.Subscribe()
	defer unsubLoc()
	lib, unsubLib := a.SubscribeEvents()
	defer unsubLib()
	if !send("hello", map[string]any{"version": 1, "location": wantLoc, "library": wantLib}) {
		return
	}
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	var last time.Time
	for {
		select {
		case <-r.Context().Done():
			return
		case st := <-loc:
			if !wantLoc || time.Since(last) < time.Second {
				continue
			}
			last = time.Now()
			if !send("location", map[string]any{"state": st.State, "fix": st.Fix, "age": st.Age}) {
				return
			}
		case ev := <-lib:
			if wantLib && !send("library", ev) {
				return
			}
		case <-tick.C:
			if _, err := fmt.Fprint(w, ": keep-alive\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}

// ---------------------------------------------------------------- peers

func (s *Server) handleIntLibrary(w http.ResponseWriter, r *http.Request, a *app.App) {
	lib, err := a.LibraryForPeers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lib)
}

func (s *Server) handleIntFile(w http.ResponseWriter, r *http.Request, a *app.App) {
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
		fh, err := os.Open(p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer fh.Close()
		st, err := fh.Stat()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Arcana-SHA256", f.SHA256)
		w.Header().Set("ETag", `"`+f.SHA256+`"`)
		http.ServeContent(w, r, "", st.ModTime(), fh)
		return
	}
	http.NotFound(w, r)
}

// ---------------------------------------------------------------- management

func (s *Server) handleIntegrations(w http.ResponseWriter, r *http.Request, a *app.App) {
	writeJSON(w, http.StatusOK, map[string]any{"scopes": integration.AllScopes, "clients": a.Integrations.List(),
		"pending": a.Integrations.Pending(), "endpoint": integrationPrefix + "/"})
}

func (s *Server) handleCreateIntegration(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	c, tok, err := a.Integrations.Create(req.Name, req.Scopes)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"client": c, "token": tok})
}

func (s *Server) handleRevokeIntegration(w http.ResponseWriter, r *http.Request, a *app.App) {
	if err := a.Integrations.Revoke(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handlePairDecision(w http.ResponseWriter, r *http.Request, a *app.App) {
	d := r.PathValue("decision")
	if d != "approve" && d != "deny" {
		writeError(w, http.StatusBadRequest, "decision must be approve or deny")
		return
	}
	if err := a.Integrations.Decide(r.PathValue("id"), d == "approve"); err != nil {
		writeError(w, http.StatusNotFound, "This request has expired or was already decided.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type peerView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request, a *app.App) {
	out := []peerView{}
	for _, p := range a.Lib.Settings.Get().Peers {
		out = append(out, peerView{p.ID, p.Name, p.URL})
	}
	writeJSON(w, http.StatusOK, map[string]any{"peers": out})
}

var peerIDRe = regexp.MustCompile(`[^a-z0-9]+`)

func (s *Server) handleAddPeer(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		Name  string `json:"name"`
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	req.Name, req.URL, req.Token = strings.TrimSpace(req.Name), strings.TrimRight(strings.TrimSpace(req.URL), "/"), strings.TrimSpace(req.Token)
	u, err := url.Parse(req.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") {
		writeError(w, http.StatusBadRequest, "Enter the other Arcana's address, for example http://192.168.1.20:8470")
		return
	}
	if !strings.HasPrefix(req.Token, "arc1_") || len(req.Token) > 100 {
		writeError(w, http.StatusBadRequest, "Enter an access token created on the other Arcana (Settings → Integrations, permission \"library:read\").")
		return
	}
	if req.Name == "" || len(req.Name) > 80 {
		req.Name = u.Host
	}
	id := strings.Trim(peerIDRe.ReplaceAllString(strings.ToLower(req.Name), "-"), "-")
	if id == "" {
		id = "peer"
	}
	_, err = a.Lib.Settings.Update(func(st *library.Settings) error {
		for _, p := range st.Peers {
			if p.ID == id || p.URL == req.URL {
				return errors.New("this library is already listed")
			}
		}
		if len(st.Peers) >= 20 {
			return errors.New("too many peers")
		}
		st.Peers = append(st.Peers, library.PeerSource{ID: id, Name: req.Name, URL: req.URL, Token: req.Token})
		return nil
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Check it right away so mistakes show up now.
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	lib, err := a.PeerLibrary(ctx, id)
	resp := map[string]any{"id": id}
	if err != nil {
		resp["warning"] = err.Error()
	} else {
		resp["resources"] = len(lib.Resources)
		resp["label"] = lib.Library["label"]
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleRemovePeer(w http.ResponseWriter, r *http.Request, a *app.App) {
	id := r.PathValue("id")
	_, err := a.Lib.Settings.Update(func(st *library.Settings) error {
		out := st.Peers[:0]
		for _, p := range st.Peers {
			if p.ID != id {
				out = append(out, p)
			}
		}
		st.Peers = out
		return nil
	})
	if err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handlePeerLibrary(w http.ResponseWriter, r *http.Request, a *app.App) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	lib, err := a.PeerLibrary(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lib)
}

func (s *Server) handlePeerPull(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := readJSON(r, &req); err != nil || len(req.IDs) == 0 || len(req.IDs) > 500 {
		writeError(w, http.StatusBadRequest, "choose resources to copy")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	res, err := a.PullFromPeer(ctx, r.PathValue("id"), req.IDs)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res})
}

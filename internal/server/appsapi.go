package server

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/apps"
	"github.com/KK4ODA/arcana/internal/tlsutil"
)

// Apps from the catalog (ADR-0030). Each installed app has a browser origin
// of its own, port P+slot, so an app cannot read Arcana's session, the
// content origin's pages or another app's saved data.

type appListeners struct {
	mu  sync.Mutex
	ctx context.Context
	// open maps an app ID to its server.
	open map[string]*appServer
}

type appServer struct {
	srv  *http.Server
	port int
	err  string // why the port could not be opened
}

func (s *Server) registerApps(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/apps", s.withApp(s.handleApps))
	mux.HandleFunc("POST /api/apps/{id}/{action}", s.adminOnly(s.withApp(s.handleAppAction)))
}

// handleAppAction starts or stops a native app, or allows or refuses it
// to devices on the network.
func (s *Server) handleAppAction(w http.ResponseWriter, r *http.Request, a *app.App) {
	spec, ok := a.Apps.Installed(r.PathValue("id"))
	if !ok || spec.Service == nil {
		writeError(w, http.StatusNotFound, "No such app is installed.")
		return
	}
	var err error
	switch r.PathValue("action") {
	case "start":
		err = a.Apps.Start(spec.ID)
	case "stop":
		a.Apps.Stop(spec.ID)
	case "network":
		var req struct {
			Allow bool `json:"allow"`
		}
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request")
			return
		}
		err = a.Apps.SetNetwork(spec.ID, req.Allow)
	default:
		writeError(w, http.StatusNotFound, "unknown action")
		return
	}
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.handleApps(w, r, a)
}

// appURL is an app's address as the browser reaches it.
func (s *Server) appURL(r *http.Request, spec *apps.Spec) string {
	return s.originOnPort(r, spec.Port(s.appPort)) + "/"
}

type appView struct {
	apps.Status
	URL string `json:"url"`
	// Secure says whether the address the browser uses is a secure
	// context (this computer, or HTTPS).
	Secure bool `json:"secure"`
	// PortProblem says why the app's port could not be opened.
	PortProblem string `json:"portProblem,omitempty"`
	// Password is the administrator password Arcana set, shown to
	// Arcana's administrators.
	Password string `json:"password,omitempty"`
	// Allowed says whether this device may open the app.
	Allowed bool `json:"allowed"`
	// HTTPS says whether Arcana offers HTTPS, which some apps need.
	HTTPS bool `json:"https"`
}

func (s *Server) handleApps(w http.ResponseWriter, r *http.Request, a *app.App) {
	host := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		host = h
	}
	secure := r.TLS != nil || isLoopbackHost(host)
	out := []appView{}
	for _, st := range a.Apps.Status() {
		spec, ok := apps.Lookup(st.ID)
		if !ok {
			continue
		}
		v := appView{Status: st, URL: s.appURL(r, spec), Secure: secure, Allowed: spec.Service == nil || s.mayUseService(r, a, spec),
			HTTPS: s.nw.tlsCfg.Load() != nil}
		if spec.NeedsHTTPS != "" && v.HTTPS && r.TLS == nil {
			v.URL = "https://" + strings.TrimPrefix(v.URL, "http://")
		}
		if spec.Service != nil && s.isAdmin(r) {
			v.Password = a.Apps.Password(spec.ID)
		}
		s.apps.mu.Lock()
		if as := s.apps.open[st.ID]; as != nil {
			v.PortProblem = as.err
		}
		s.apps.mu.Unlock()
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"apps": out})
}

// appOriginHandler serves one app's origin.
func (s *Server) appOriginHandler(spec *apps.Spec) http.Handler {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := s.current()
		if a == nil || a.Apps == nil {
			apps.NotReady(w, apps.Status{Title: spec.Title, Problem: "No library is open."})
			return
		}
		if _, ok := a.Apps.Installed(spec.ID); !ok {
			apps.NotReady(w, apps.Status{Title: spec.Title, Problem: spec.Title + " is not installed. Install it from Arcana's Apps page."})
			return
		}
		if spec.Service != nil && !s.mayUseService(r, a, spec) {
			apps.Refused(w, spec.Title+" is available on the computer running Arcana. To use it from this device, sign in to Arcana as an administrator, or ask the owner to allow devices on the network on Arcana's Apps page.")
			return
		}
		// Browsers send Arcana's cookies to every port of the host; no
		// app needs them.
		dropCookies(r, sessionCookie)
		if spec.Service != nil {
			a.Apps.ServiceHandler(spec).ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		a.Apps.WebHandler(spec).ServeHTTP(w, r)
	})
	return s.policy.guardHost(h)
}

// mayUseService decides who may use a native app (ADR-0017, ADR-0030): the
// computer running Arcana, administrators signed in from the network, and
// every device on the network once the owner allows it for that app.
// These apps keep accounts, files and passwords; Arcana's own read-only
// access for network devices does not extend to them by itself.
func (s *Server) mayUseService(r *http.Request, a *app.App, spec *apps.Spec) bool {
	if isLocal(r) {
		return true
	}
	if a.Apps.NetworkAllowed(spec.ID) {
		return true
	}
	return s.remoteSession(r) != nil
}

// dropCookies removes the named cookies from a request.
func dropCookies(r *http.Request, names ...string) {
	if r.Header.Get("Cookie") == "" {
		return
	}
	drop := map[string]bool{}
	for _, n := range names {
		drop[n] = true
	}
	var keep []*http.Cookie
	for _, c := range r.Cookies() {
		if !drop[c.Name] {
			keep = append(keep, c)
		}
	}
	r.Header.Del("Cookie")
	for _, c := range keep {
		r.AddCookie(c)
	}
}

// syncAppListeners opens the port of every installed app and closes the
// ports of apps no longer installed.
func (s *Server) syncAppListeners() {
	s.apps.mu.Lock()
	defer s.apps.mu.Unlock()
	if s.apps.ctx == nil || s.appPort == 0 {
		return // not serving yet
	}
	if s.apps.open == nil {
		s.apps.open = map[string]*appServer{}
	}
	want := map[string]*apps.Spec{}
	if a := s.current(); a != nil && a.Apps != nil {
		for _, st := range a.Apps.Status() {
			if spec, ok := apps.Lookup(st.ID); ok {
				want[spec.ID] = spec
			}
		}
	}
	for id, as := range s.apps.open {
		if want[id] == nil || as.port != want[id].Port(s.appPort) {
			if as.srv != nil {
				as.srv.Close()
			}
			delete(s.apps.open, id)
		}
	}
	host := "127.0.0.1"
	if s.cfg.ShareOnLAN {
		host = ""
	}
	for id, spec := range want {
		if s.apps.open[id] != nil {
			continue
		}
		port := spec.Port(s.appPort)
		l, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			// Kept, so the Apps page can say why, and tried again when the
			// library changes.
			s.apps.open[id] = &appServer{port: port, err: "Port " + strconv.Itoa(port) + " is in use by another program, so " + spec.Title + " cannot be opened."}
			s.log.Warn("app port unavailable", "app", id, "port", port, "err", err)
			continue
		}
		srv := &http.Server{Handler: s.appOriginHandler(spec), ReadHeaderTimeout: 15 * time.Second, IdleTimeout: 120 * time.Second,
			BaseContext: func(net.Listener) context.Context { return s.apps.ctx }}
		s.apps.open[id] = &appServer{srv: srv, port: port}
		go srv.Serve(tlsutil.NewSniffer(l, s.tlsConfig))
		s.log.Info("app origin", "app", id, "port", port)
	}
}

// retryAppPorts forgets ports that could not be opened, so the next sync
// tries them again.
func (s *Server) retryAppPorts() {
	s.apps.mu.Lock()
	for id, as := range s.apps.open {
		if as.srv == nil {
			delete(s.apps.open, id)
		}
	}
	s.apps.mu.Unlock()
	s.syncAppListeners()
}

// serveApps starts serving app origins until ctx ends.
func (s *Server) serveApps(ctx context.Context) {
	s.apps.mu.Lock()
	s.apps.ctx = ctx
	s.apps.mu.Unlock()
	s.watchApps(s.current())
	s.syncAppListeners()
	go func() {
		<-ctx.Done()
		s.apps.mu.Lock()
		defer s.apps.mu.Unlock()
		for id, as := range s.apps.open {
			if as.srv != nil {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = as.srv.Shutdown(shutdownCtx)
				cancel()
			}
			delete(s.apps.open, id)
		}
	}()
}

// watchApps keeps the app ports in step with the library's apps.
func (s *Server) watchApps(a *app.App) {
	if a != nil && a.Apps != nil {
		a.Apps.SetOnChange(s.retryAppPorts)
	}
}

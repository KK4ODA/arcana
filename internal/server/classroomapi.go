package server

import (
	"context"
	"errors"
	"html"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/kolibri"
)

// The classroom: Kolibri run by Arcana on its own origin (port+2), with
// its channel apps on the content origin (ADR-0024).

func (s *Server) registerClassroom(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/classroom", s.withApp(s.handleClassroom))
	mux.HandleFunc("POST /api/classroom/setup", s.withApp(s.handleClassroomSetup))
	mux.HandleFunc("POST /api/classroom/stop", s.withApp(s.handleClassroomStop))
	mux.HandleFunc("POST /api/classroom/password", s.withApp(s.handleClassroomPassword))
	mux.HandleFunc("GET /api/classroom/channels/{id}/tree", s.adminOnly(s.withApp(s.handleClassroomTree)))
	mux.HandleFunc("POST /api/classroom/channels/{id}/size", s.withApp(s.handleClassroomSize))
	mux.HandleFunc("POST /api/classroom/import", s.withApp(s.handleClassroomImport))
	mux.HandleFunc("POST /api/classroom/imports/{task}/{action}", s.withApp(s.handleClassroomTask))
}

// kolibriOrigin is Kolibri's origin as the browser reaches it.
func (s *Server) kolibriOrigin(r *http.Request) string {
	return s.originOnPort(r, s.kolibriPort)
}

// contentOriginFor is the content origin as the browser reaches it.
func (s *Server) contentOriginFor(r *http.Request) string {
	return s.originOnPort(r, s.content)
}

func (s *Server) originOnPort(r *http.Request, port int) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(r.Host); err == nil {
		host = h
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		host = "[" + host + "]"
	}
	scheme := "http://"
	if r.TLS != nil {
		scheme = "https://"
	}
	return scheme + host + ":" + strconv.Itoa(port)
}

func (s *Server) handleClassroom(w http.ResponseWriter, r *http.Request, a *app.App) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	st := a.ClassroomStatus(ctx, s.isAdmin(r))
	writeJSON(w, http.StatusOK, map[string]any{"classroom": st, "url": s.kolibriOrigin(r) + "/"})
}

func (s *Server) handleClassroomSetup(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		Language string `json:"language"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := a.SetUpClassroom(r.Context(), req.Language); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.handleClassroom(w, r, a)
}

func (s *Server) handleClassroomStop(w http.ResponseWriter, r *http.Request, a *app.App) {
	if err := a.StopClassroom(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.handleClassroom(w, r, a)
}

func (s *Server) handleClassroomPassword(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := a.SetClassroomPassword(ctx, req.Password); err != nil {
		code := http.StatusConflict
		if errors.Is(err, kolibri.ErrBadPassword) {
			code = http.StatusUnprocessableEntity
		}
		writeError(w, code, err.Error())
		return
	}
	s.handleClassroom(w, r, a)
}

func (s *Server) handleClassroomTree(w http.ResponseWriter, r *http.Request, a *app.App) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	tree, err := a.ClassroomTree(ctx, r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, tree)
}

func (s *Server) handleClassroomSize(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		NodeIDs []string `json:"nodeIds"`
	}
	if err := readJSON(r, &req); err != nil || len(req.NodeIDs) > 500 {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	size, count, err := a.ClassroomSize(r.Context(), r.PathValue("id"), req.NodeIDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"size": size, "resources": count})
}

func (s *Server) handleClassroomImport(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		Channel string   `json:"channel"`
		NodeIDs []string `json:"nodeIds"`
	}
	if err := readJSON(r, &req); err != nil || len(req.NodeIDs) > 500 {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	task, err := a.ClassroomImport(ctx, req.Channel, req.NodeIDs)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleClassroomTask(w http.ResponseWriter, r *http.Request, a *app.App) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	if err := a.ClassroomTask(ctx, r.PathValue("task"), r.PathValue("action")); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// kolibriHandler serves Kolibri's origin: the running classroom, or a page
// that says why there is none.
func (s *Server) kolibriHandler() http.Handler {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := s.current()
		if a == nil || !a.ClassroomWanted() {
			classroomPage(w, http.StatusNotFound, "No classroom is set up on this Arcana. The owner can set one up on Arcana's Classroom page.")
			return
		}
		s.kolibriProxy(a, r).Handler().ServeHTTP(w, r)
	})
	return s.policy.guardHost(h)
}

func (s *Server) kolibriProxy(a *app.App, r *http.Request) *kolibri.Proxy {
	return &kolibri.Proxy{
		Server:        a.KolibriServer(),
		ContentPort:   s.content,
		ContentOrigin: s.contentOriginFor,
		DropCookies:   []string{sessionCookie},
		NotReady: func(w http.ResponseWriter, r *http.Request, st kolibri.Status) {
			msg := "The classroom is not running yet. The owner can check it on Arcana's Classroom page."
			switch st.State {
			case "starting":
				msg = "The classroom is starting. Its first start prepares its database and can take a few minutes. This page reloads by itself."
			case "failed":
				msg = "The classroom could not start. The owner can see why on Arcana's Classroom page."
			}
			w.Header().Set("Refresh", "5")
			classroomPage(w, http.StatusServiceUnavailable, msg)
		},
	}
}

// classroomPage writes a plain page on Kolibri's origin.
func classroomPage(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Classroom</title>` +
		`<body style="font:16px system-ui,sans-serif;max-width:40em;margin:3em auto;padding:0 1em;line-height:1.5"><h1>Classroom</h1><p>` +
		html.EscapeString(msg) + `</p></body>`))
}

// kolibriZip serves Kolibri's channel apps on the content origin.
func (s *Server) kolibriZip(w http.ResponseWriter, r *http.Request) {
	a := s.current()
	if a == nil || !a.ClassroomWanted() {
		http.NotFound(w, r)
		return
	}
	s.kolibriProxy(a, r).ZipHandler().ServeHTTP(w, r)
}

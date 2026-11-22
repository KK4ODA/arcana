package server

import (
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/auth"
)

const sessionCookie = "arcana_session"

// hostOnlyPrefixes are actions a signed-in network administrator still
// cannot take: they reveal or change the host's files and programs, the
// administration password itself, or the host user's private notes.
var hostOnlyPrefixes = []string{
	"/api/setup/", "/api/transfer/", "/api/app/update", "/api/auth/password", "/api/manifest",
	"/api/user/notes", "/api/user/recent", "/api/user/documents", "/api/server/tls", "/api/server/hotspot",
	"/api/integrations", "/api/peers",
}

func hostOnlyPath(p string) bool {
	for _, pre := range hostOnlyPrefixes {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// remoteSession returns the signed-in session of a network client, if any.
func (s *Server) remoteSession(r *http.Request) *auth.Session {
	if isLocal(r) {
		return nil
	}
	a := s.current()
	if a == nil || !a.Admin.Enabled() {
		return nil
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	return s.sessions.Get(c.Value)
}

// isAdmin reports whether the request may administer the library.
func (s *Server) isAdmin(r *http.Request) bool {
	return isLocal(r) || s.remoteSession(r) != nil
}

// adminOnly restricts a read endpoint to the host and signed-in
// administrators.
func (s *Server) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isAdmin(r) {
			writeError(w, http.StatusForbidden, "Sign in as an administrator to see this.")
			return
		}
		next(w, r)
	}
}

func (s *Server) registerAuth(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth", s.withApp(s.handleAuthStatus))
	mux.HandleFunc("POST /api/auth/login", s.withApp(s.handleLogin))
	mux.HandleFunc("POST /api/auth/logout", s.withApp(s.handleLogout))
	mux.HandleFunc("PUT /api/auth/password", s.withApp(s.handleSetPassword))
	mux.HandleFunc("DELETE /api/auth/password", s.withApp(s.handleClearPassword))
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request, a *app.App) {
	resp := map[string]any{
		"enabled":  a.Admin.Enabled(),
		"signedIn": s.remoteSession(r) != nil,
		"secure":   r.TLS != nil,
	}
	if isLocal(r) {
		resp["sessions"] = s.sessions.Count()
		if t := a.Admin.SetAt(); !t.IsZero() {
			resp["setAt"] = t
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request, a *app.App) {
	if isLocal(r) {
		writeError(w, http.StatusBadRequest, "This computer is already an administrator.")
		return
	}
	if !a.Admin.Enabled() {
		writeError(w, http.StatusForbidden, "Remote administration is off. Turn it on in the Server view on the computer running Arcana.")
		return
	}
	addr := clientAddr(r)
	if ok, wait := s.limiter.Allow(addr); !ok {
		w.Header().Set("Retry-After", fmt.Sprint(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("Too many attempts. Try again in %d minutes.", int(wait.Minutes())+1))
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	ok, err := a.Admin.Verify(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		s.limiter.Fail(addr)
		s.log.Warn("failed administrator sign-in", "from", addr)
		// A small fixed delay on top of Argon2id's cost.
		time.Sleep(500 * time.Millisecond)
		writeError(w, http.StatusUnauthorized, "Wrong password.")
		return
	}
	s.limiter.Succeed(addr)
	sess := s.sessions.New(addr)
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sess.ID, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: int(auth.SessionAbsolute.Seconds())})
	s.log.Info("administrator signed in", "from", addr)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "token": sess.CSRF})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request, a *app.App) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSetPassword(w http.ResponseWriter, r *http.Request, a *app.App) {
	if a.Lib.ReadOnly {
		writeError(w, http.StatusConflict, "The library is read-only.")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if err := a.Admin.Set(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// A new password signs everyone out.
	s.sessions.Clear()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleClearPassword(w http.ResponseWriter, r *http.Request, a *app.App) {
	if err := a.Admin.Clear(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.sessions.Clear()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

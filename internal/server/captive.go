package server

import (
	"fmt"
	"html"
	"net"
	"net/http"
	"strconv"
)

// captiveHandler serves the captive-portal port of the appliance. On an
// Arcana access point every name resolves to the appliance and port 80 is
// redirected here, so phones' connectivity checks (Android generate_204,
// Apple hotspot-detect, Windows connecttest) and any address typed into a
// browser land on a page that leads to the library. It only ever redirects:
// no API, no content, so unknown Host names are harmless here.
func (s *Server) captiveHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := "arcana.local"
		if la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
			if ta, ok := la.(*net.TCPAddr); ok && !ta.IP.IsUnspecified() {
				host = ta.IP.String()
			}
		}
		target := "http://" + net.JoinHostPort(host, strconv.Itoa(s.appPort)) + "/"
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Security-Policy", "default-src 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Location", target)
		w.WriteHeader(http.StatusFound)
		if r.Method == http.MethodHead {
			return
		}
		fmt.Fprintf(w, `<!doctype html><meta name="viewport" content="width=device-width"><title>Arcana</title>`+
			`<h1>Arcana offline library</h1><p>This network has no internet access. It offers a library of reference material.</p>`+
			`<p><a href="%s">Open the library</a></p>`, html.EscapeString(target))
	})
}

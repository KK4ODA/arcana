package apps

import (
	"html"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// WebCSP is the Content-Security-Policy of web apps. Their scripts run as
// their authors wrote them (some inline, some compiled at run time), but
// they can load and contact nothing outside their own origin: an app
// cannot reach the internet, and works the same offline.
const WebCSP = "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval' 'wasm-unsafe-eval' blob:; " +
	"style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; " +
	"connect-src 'self' data: blob:; worker-src 'self' blob:; media-src 'self' data: blob:; " +
	"frame-src 'self' data: blob:; manifest-src 'self'; frame-ancestors 'none'; base-uri 'self'; " +
	"form-action 'self'; object-src data:"

// ServiceCSP is added to every response of a native app, beside the app's
// own policy; a browser enforces both. It lets the app's pages do what
// they do on their own origin but load and contact nothing elsewhere:
// Jellyfin's web client fetches Google's Chromecast script and Stirling
// PDF's pages load an analytics library, both from the internet.
const ServiceCSP = "default-src 'self' data: blob:; script-src 'self' 'unsafe-inline' 'unsafe-eval' 'wasm-unsafe-eval' blob:; " +
	"style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self' data:; " +
	"connect-src 'self' data: blob:; worker-src 'self' blob:; media-src 'self' data: blob:; " +
	"frame-src 'self' data: blob:; manifest-src 'self'; form-action 'self'; object-src data:"

// WebHandler serves a web app's files from its unpacked folder. It only
// reads, and only files under the app's root.
func (m *Manager) WebHandler(spec *Spec) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "This app is read-only.", http.StatusMethodNotAllowed)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", WebCSP)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		dir, st, ok := m.ready(spec.ID)
		if !ok {
			NotReady(w, st)
			return
		}
		root := filepath.Join(dir, filepath.FromSlash(spec.Root))
		name := path.Clean("/" + r.URL.Path)
		if name == "/" {
			name = "/" + index(spec)
		}
		if extra, ok := spec.Extra[name]; ok {
			http.ServeFile(w, r, filepath.Join(dir, filepath.FromSlash(extra)))
			return
		}
		file, ok := siteFile(root, name)
		if !ok && spec.SPA && !strings.Contains(path.Base(name), ".") {
			file, ok = siteFile(root, "/"+index(spec))
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		if path.Base(file) == index(spec) || strings.HasSuffix(file, ".html") || strings.HasSuffix(file, "service-worker.js") {
			// Pages and service workers name the other files; revalidate
			// them so an updated app is seen at once.
			h.Set("Cache-Control", "no-cache")
		}
		if serveRewritten(w, r, spec, name, file) {
			return
		}
		http.ServeFile(w, r, file)
	})
}

// serveRewritten serves a file with the spec's rewrites for its path
// applied, and reports whether it did.
func serveRewritten(w http.ResponseWriter, r *http.Request, spec *Spec, name, file string) bool {
	var rw []Rewrite
	for _, x := range spec.Rewrites {
		if x.Path == name || (name == "/"+index(spec) && x.Path == "/") {
			rw = append(rw, x)
		}
	}
	if len(rw) == 0 {
		return false
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	st, err := os.Stat(file)
	if err != nil {
		return false
	}
	s := string(data)
	for _, x := range rw {
		s = strings.Replace(s, x.Old, x.New, 1)
	}
	http.ServeContent(w, r, path.Base(name), st.ModTime(), strings.NewReader(s))
	return true
}

func index(spec *Spec) string {
	if spec.Index != "" {
		return spec.Index
	}
	return "index.html"
}

// siteFile maps a cleaned URL path to a regular file under root ("/"
// and folders map to their index.html).
func siteFile(root, name string) (string, bool) {
	p := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(name, "/")))
	if rel, err := filepath.Rel(root, p); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	st, err := os.Stat(p)
	if err == nil && st.IsDir() {
		p = filepath.Join(p, "index.html")
		st, err = os.Stat(p)
	}
	if err != nil || !st.Mode().IsRegular() {
		return "", false
	}
	return p, true
}

// NotReady writes the page shown while an app cannot be opened.
func NotReady(w http.ResponseWriter, st Status) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	title := st.Title
	if title == "" {
		title = "App"
	}
	msg := st.Problem
	switch st.State {
	case "preparing":
		msg = title + " is being prepared on this computer. This page reloads by itself."
		h.Set("Refresh", "3")
	case "starting":
		msg = title + " is starting. Its first start can take a minute; this page reloads by itself."
		h.Set("Refresh", "3")
	}
	if msg == "" {
		msg = title + " is not available."
	}
	// The page's style is in a <style> element, which the app origins'
	// CSP ('unsafe-inline' for styles) allows.
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>` + html.EscapeString(title) + `</title><style>body{font:16px system-ui,sans-serif;max-width:40em;margin:3em auto;padding:0 1em}</style>` +
		`<h1>` + html.EscapeString(title) + `</h1><p>` + html.EscapeString(msg) + `</p>`))
}

// Refused writes the page shown to a device that may not use an app.
func Refused(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<title>Not available</title><style>body{font:16px system-ui,sans-serif;max-width:40em;margin:3em auto;padding:0 1em}</style>` +
		`<h1>Not available on this device</h1><p>` + html.EscapeString(msg) + `</p>`))
}

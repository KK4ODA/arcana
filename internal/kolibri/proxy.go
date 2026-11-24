package kolibri

import (
	"html"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

// Proxy forwards Kolibri's own origin (Arcana's port P+2) and its zip
// content (under ZipPrefix on the content origin) to the running Kolibri.
type Proxy struct {
	Server *Server
	// ContentOrigin returns the content origin as the browser sees it for
	// a request ("http://192.168.1.20:8471").
	ContentOrigin func(r *http.Request) string
	// ContentPort is the content origin's port, which Kolibri writes into
	// its CSP as a bare number.
	ContentPort int
	// NotReady writes the page shown while Kolibri is not running.
	NotReady func(w http.ResponseWriter, r *http.Request, st Status)
}

// Handler serves Kolibri's origin.
func (p *Proxy) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base, ok := p.Server.Base()
		if !ok {
			p.notReady(w, r)
			return
		}
		target, _ := url.Parse(base)
		tls := r.TLS != nil
		rp := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				// Kolibri builds links from the Host the browser used.
				pr.Out.Host = pr.In.Host
				pr.SetXForwarded()
			},
			ModifyResponse: func(resp *http.Response) error {
				if csp := resp.Header.Get("Content-Security-Policy"); csp != "" {
					resp.Header.Set("Content-Security-Policy", FixCSP(csp, p.ContentPort, p.ContentOrigin(r)))
				}
				if loc := resp.Header.Get("Location"); tls && strings.HasPrefix(loc, "http://"+r.Host+"/") {
					resp.Header.Set("Location", "https://"+strings.TrimPrefix(loc, "http://"))
				}
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				p.notReady(w, r)
			},
		}
		rp.ServeHTTP(w, r)
	})
}

// ZipHandler serves Kolibri's zip content; it is mounted at ZipPrefix on
// the content origin and only reads.
func (p *Proxy) ZipHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			w.Header().Set("Allow", "GET, HEAD, OPTIONS")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		base, ok := p.Server.ZipBase()
		if !ok {
			http.Error(w, "Kolibri is not running.", http.StatusServiceUnavailable)
			return
		}
		target, _ := url.Parse(base)
		rp := &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.SetURL(target)
				pr.Out.Host = pr.In.Host
				// No cookies cross into channel apps.
				pr.Out.Header.Del("Cookie")
			},
			ModifyResponse: func(resp *http.Response) error {
				resp.Header.Del("Set-Cookie")
				return nil
			},
		}
		rp.ServeHTTP(w, r)
	})
}

func (p *Proxy) notReady(w http.ResponseWriter, r *http.Request) {
	st := p.Server.Status()
	if p.NotReady != nil {
		p.NotReady(w, r, st)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Refresh", "5")
	w.WriteHeader(http.StatusServiceUnavailable)
	msg := "The classroom (Kolibri) is not running."
	switch st.State {
	case "starting":
		msg = "The classroom (Kolibri) is starting. Its first start prepares its database and can take a few minutes; this page reloads by itself."
	case "failed":
		msg = "The classroom (Kolibri) could not start: " + st.LastError
	}
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Classroom</title>` +
		`<body style="font:16px system-ui,sans-serif;max-width:40em;margin:3em auto;padding:0 1em"><h1>Classroom</h1><p>` +
		html.EscapeString(msg) + `</p></body>`))
}

// FixCSP repairs the Content-Security-Policy Kolibri sends behind Arcana.
// With ZIP_CONTENT_ORIGIN set to a port, Kolibri writes that port as a
// bare source ("frame-src 'self' data: blob: 8471"), which browsers read
// as a host called "8471". The port is replaced with the content origin,
// which frame-src and media-src must allow.
func FixCSP(csp string, port int, contentOrigin string) string {
	bare := strconv.Itoa(port)
	parts := strings.Split(csp, ";")
	for i, part := range parts {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		name := strings.ToLower(fields[0])
		var out []string
		has := false
		for _, f := range fields[1:] {
			if f == bare {
				f = contentOrigin
			}
			if f == contentOrigin {
				if has {
					continue
				}
				has = true
			}
			out = append(out, f)
		}
		if (name == "frame-src" || name == "media-src") && !has && contentOrigin != "" {
			out = append(out, contentOrigin)
		}
		parts[i] = " " + strings.Join(append([]string{fields[0]}, out...), " ")
	}
	return strings.TrimSpace(strings.Join(parts, ";"))
}

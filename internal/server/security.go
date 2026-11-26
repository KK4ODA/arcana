package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"

	"github.com/KK4ODA/arcana/internal/auth"
)

// accessPolicy decides who may do what.
//
//   - Requests are accepted only for known Host names (loopback names, this
//     machine's addresses, and configured .local names). This defeats DNS
//     rebinding: a hostile web page cannot reach Arcana through a domain it
//     controls.
//   - Every state-changing request must carry the per-process session token
//     in a header. Only same-origin pages can read the token, so other web
//     sites cannot forge requests (CSRF).
//   - Administrative actions are accepted from the local computer, and from
//     network clients that signed in with the administration password (off
//     by default). Other network clients get read-only access. Signed-in
//     clients use a per-session token instead of the host token, and a few
//     host-only actions stay unavailable to them.
type accessPolicy struct {
	token string
	// remote returns the signed-in session of a network client, if any.
	remote func(r *http.Request) *auth.Session

	mu    sync.RWMutex
	hosts map[string]bool
}

func newAccessPolicy() *accessPolicy {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	p := &accessPolicy{token: hex.EncodeToString(b[:])}
	p.refreshHosts(nil)
	return p
}

// refreshHosts rebuilds the allowed Host list from local interface
// addresses plus extra names.
func (p *accessPolicy) refreshHosts(extra []string) {
	hosts := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true, "arcana.local": true}
	if h, err := os.Hostname(); err == nil && h != "" {
		h = strings.ToLower(h)
		hosts[h] = true
		hosts[strings.TrimSuffix(h, ".local")+".local"] = true
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok {
				hosts[ipn.IP.String()] = true
			}
		}
	}
	for _, e := range extra {
		hosts[strings.ToLower(e)] = true
	}
	p.mu.Lock()
	p.hosts = hosts
	p.mu.Unlock()
}

func (p *accessPolicy) hostAllowed(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.ToLower(strings.Trim(host, "[]"))
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.hosts[host]
}

// isLocal reports whether the request comes from this computer.
func isLocal(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (p *accessPolicy) tokenOK(r *http.Request) bool {
	got := r.Header.Get("X-Arcana-Token")
	return len(got) == len(p.token) && subtle.ConstantTimeCompare([]byte(got), []byte(p.token)) == 1
}

// guardHost rejects unknown Host headers.
func (p *accessPolicy) guardHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !p.hostAllowed(r.Host) {
			http.Error(w, "Unknown host name. Open Arcana using this computer's address.", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// guardMutations enforces the token for unsafe methods and the local-only
// rule for administrative routes.
func (p *accessPolicy) guardMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			// Other origins, including Arcana's own content origin on the
			// neighbouring port (same site, different origin), are refused.
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				writeError(w, http.StatusForbidden, "Cross-site request refused.")
				return
			}
			if strings.HasPrefix(r.URL.Path, integrationPrefix+"/") {
				// The integration API authenticates every call with a
				// bearer token (or is pairing, which only records a
				// request for the operator to approve).
				break
			}
			if isLocal(r) {
				if !p.tokenOK(r) {
					writeError(w, http.StatusForbidden, "Missing or invalid session token. Reload the page.")
					return
				}
				break
			}
			if r.URL.Path == "/api/auth/login" {
				// Signing in needs no session. Requiring JSON forces a
				// CORS preflight for any cross-origin attempt.
				if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
					writeError(w, http.StatusUnsupportedMediaType, "JSON required.")
					return
				}
				break
			}
			var sess *auth.Session
			if p.remote != nil {
				sess = p.remote(r)
			}
			if sess == nil {
				writeError(w, http.StatusForbidden, "This Arcana server is read-only for devices on the network. Make changes on the computer running Arcana, or sign in as an administrator.")
				return
			}
			if hostOnlyPath(r.URL.Path) {
				writeError(w, http.StatusForbidden, "Available only on the computer running Arcana.")
				return
			}
			got := r.Header.Get("X-Arcana-Token")
			if len(got) != len(sess.CSRF) || subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRF)) != 1 {
				writeError(w, http.StatusForbidden, "Missing or invalid session token. Reload the page.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// localOnly restricts a read endpoint that reveals host details (drives,
// file paths, settings) to the local computer.
func localOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isLocal(r) {
			writeError(w, http.StatusForbidden, "Available only on the computer running Arcana.")
			return
		}
		next(w, r)
	}
}

func isLoopbackHost(host string) bool {
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// appHeaders are sent with every response from the application origin.
func appHeaders(contentPort int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			host := r.Host
			if hh, _, err := net.SplitHostPort(r.Host); err == nil {
				host = hh
			}
			if strings.Contains(host, ":") {
				host = "[" + host + "]"
			}
			scheme := "http://"
			if r.TLS != nil {
				scheme = "https://"
			}
			contentOrigin := scheme + host + ":" + itoa(contentPort)
			// 'wasm-unsafe-eval' lets the translation worker compile the
			// Bergamot engine; it does not permit eval() of JavaScript.
			// Films play in the interface from the content origin
			// (media-src), which keeps the session token out of reach.
			h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'wasm-unsafe-eval'; style-src 'self'; img-src 'self' data: blob:; worker-src 'self'; "+
				"connect-src 'self'; media-src "+contentOrigin+"; frame-src "+contentOrigin+"; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("X-Frame-Options", "DENY")
			// Browsers ignore (and warn about) COOP on plain-HTTP network
			// addresses; send it where it takes effect.
			if r.TLS != nil || isLoopbackHost(host) {
				h.Set("Cross-Origin-Opener-Policy", "same-origin")
			}
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			next.ServeHTTP(w, r)
		})
	}
}

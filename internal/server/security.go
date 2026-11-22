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
//   - Administrative actions are accepted only from the local computer.
//     Clients on the network get read-only access. (Password-authenticated
//     remote administration is planned for Phase 4.)
type accessPolicy struct {
	token string

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
			if !isLocal(r) {
				writeError(w, http.StatusForbidden, "This Arcana server is read-only for devices on the network. Make changes on the computer running Arcana.")
				return
			}
			if !p.tokenOK(r) {
				writeError(w, http.StatusForbidden, "Missing or invalid session token. Reload the page.")
				return
			}
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				writeError(w, http.StatusForbidden, "Cross-site request refused.")
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
			contentOrigin := "http://" + host + ":" + itoa(contentPort)
			h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; worker-src 'self'; "+
				"connect-src 'self'; frame-src "+contentOrigin+"; frame-ancestors 'none'; base-uri 'none'; form-action 'self'; object-src 'none'")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Cross-Origin-Resource-Policy", "same-origin")
			next.ServeHTTP(w, r)
		})
	}
}

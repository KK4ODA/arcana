package server

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/buildinfo"
	"github.com/KK4ODA/arcana/internal/hotspot"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/mdns"
	"github.com/KK4ODA/arcana/internal/tlsutil"
)

type libSettings = library.Settings

// netState holds the optional network features: HTTPS, mDNS and the
// Wi-Fi hotspot helper.
type netState struct {
	ctx context.Context

	tlsCfg atomic.Pointer[tls.Config]

	mu          sync.Mutex
	tlsInfo     *tlsutil.Info
	tlsErr      string
	mdns        *mdns.Responder
	mdnsCancel  context.CancelFunc
	mdnsErr     string
	mdnsRunning bool
	hotspot     *hotspot.Manager
}

func (s *Server) tlsConfig() *tls.Config { return s.nw.tlsCfg.Load() }

// certificateNames lists names and addresses a self-signed certificate
// should cover.
func (s *Server) certificateNames() ([]string, []net.IP) {
	names := []string{"localhost"}
	if h := s.mdnsHost(); h != "" {
		names = append(names, h)
	}
	if hn, err := os.Hostname(); err == nil && hn != "" {
		hn = strings.ToLower(hn)
		names = append(names, hn)
		if !strings.HasSuffix(hn, ".local") {
			names = append(names, hn+".local")
		}
	}
	ips := []net.IP{net.IPv4(127, 0, 0, 1)}
	for _, a := range lanAddresses() {
		ips = append(ips, net.ParseIP(a))
	}
	return names, ips
}

// applyTLS loads the certificate selected in settings.
func (s *Server) applyTLS() error {
	a := s.current()
	// Computed before locking: mdnsHost takes the same lock.
	names, ips := s.certificateNames()
	s.nw.mu.Lock()
	defer s.nw.mu.Unlock()
	s.nw.tlsInfo, s.nw.tlsErr = nil, ""
	if a == nil {
		s.nw.tlsCfg.Store(nil)
		return nil
	}
	var (
		cert tls.Certificate
		info tlsutil.Info
		err  error
	)
	switch mode := a.Lib.Settings.Get().Server.TLS; mode {
	case "":
		s.nw.tlsCfg.Store(nil)
		return nil
	case "self-signed":
		if a.Lib.ReadOnly {
			err = errors.New("the library is read-only, so a certificate cannot be stored")
			break
		}
		cert, info, err = tlsutil.SelfSigned(filepath.Join(a.Lib.Layout.State(), "tls"), names, ips)
	case "custom":
		dir := filepath.Join(a.Lib.User(), "tls")
		cert, info, err = tlsutil.Custom(filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem"))
	default:
		err = fmt.Errorf("unknown HTTPS mode %q", mode)
	}
	if err != nil {
		s.nw.tlsCfg.Store(nil)
		s.nw.tlsErr = err.Error()
		s.log.Warn("HTTPS unavailable", "err", err)
		return err
	}
	s.nw.tlsInfo = &info
	s.nw.tlsCfg.Store(tlsutil.ServerConfig(cert))
	return nil
}

func (s *Server) mdnsHost() string {
	s.nw.mu.Lock()
	r := s.nw.mdns
	s.nw.mu.Unlock()
	if r != nil {
		return r.Host()
	}
	if a := s.current(); a != nil {
		if l := mdns.SanitizeLabel(a.Lib.Settings.Get().Server.Name); l != "" {
			return l + ".local"
		}
	}
	return "arcana.local"
}

// applyMDNS (re)starts the responder when sharing on the network.
func (s *Server) applyMDNS() {
	s.nw.mu.Lock()
	if s.nw.mdnsCancel != nil {
		s.nw.mdnsCancel()
		s.nw.mdnsCancel = nil
	}
	s.nw.mdns, s.nw.mdnsErr, s.nw.mdnsRunning = nil, "", false
	a := s.current()
	ctx := s.nw.ctx
	if a == nil || ctx == nil || !s.cfg.ShareOnLAN || !a.Lib.Settings.Get().Server.MDNS {
		s.nw.mu.Unlock()
		return
	}
	set := a.Lib.Settings.Get()
	label := mdns.SanitizeLabel(set.Server.Name)
	if label == "" {
		label = "arcana"
	}
	instance := "Arcana"
	if l := strings.TrimSpace(a.Lib.Identity.Label); l != "" {
		instance = "Arcana – " + l
	}
	r := mdns.New(mdns.Config{Name: label, Instance: instance, Port: s.appPort, Log: s.log,
		TXT: []string{"path=/", "version=" + buildinfo.Version},
		// Programs (RadioPost, Aether HF, other Arcanas) find the
		// integration API by browsing for _arcana._tcp.
		Extra: []mdns.Service{{Type: "_arcana._tcp", TXT: []string{"api=" + integrationPrefix + "/", "apiversion=1"}}}})
	mctx, cancel := context.WithCancel(ctx)
	s.nw.mdns, s.nw.mdnsCancel, s.nw.mdnsRunning = r, cancel, true
	s.nw.mu.Unlock()
	go func() {
		err := r.Run(mctx)
		s.nw.mu.Lock()
		if s.nw.mdns == r {
			s.nw.mdnsRunning = false
			if err != nil {
				s.nw.mdnsErr = err.Error()
			}
		}
		s.nw.mu.Unlock()
		if err != nil {
			s.log.Warn("mDNS responder stopped", "err", err)
		}
	}()
}

func (s *Server) mdnsActive() bool {
	s.nw.mu.Lock()
	defer s.nw.mu.Unlock()
	return s.nw.mdnsRunning
}

// extraHosts are Host names accepted besides local addresses.
func (s *Server) extraHosts() []string { return []string{s.mdnsHost()} }

func (s *Server) networkInfo(r *http.Request) map[string]any {
	s.nw.mu.Lock()
	defer s.nw.mu.Unlock()
	out := map[string]any{}
	m := map[string]any{"running": s.nw.mdnsRunning}
	if s.nw.mdns != nil {
		m["host"] = s.nw.mdns.Host()
	}
	if s.nw.mdnsErr != "" {
		m["error"] = s.nw.mdnsErr
	}
	out["mdns"] = m
	t := map[string]any{"enabled": s.nw.tlsCfg.Load() != nil}
	if s.nw.tlsInfo != nil {
		t["certificate"] = s.nw.tlsInfo
	}
	if s.nw.tlsErr != "" {
		t["error"] = s.nw.tlsErr
	}
	out["tls"] = t
	return out
}

func (s *Server) registerNetwork(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/server/qr.svg", s.handleQR)
	mux.HandleFunc("GET /api/server/wifi-qr.svg", s.adminOnly(s.withApp(s.handleWifiQR)))
	mux.HandleFunc("PUT /api/server/tls", s.withApp(s.handleSetTLS))
	mux.HandleFunc("GET /api/server/hotspot", localOnly(s.handleHotspotStatus))
	mux.HandleFunc("POST /api/server/hotspot", s.withApp(s.handleHotspot))
}

func (s *Server) handleSetTLS(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	switch req.Mode {
	case "", "self-signed", "custom":
	default:
		writeError(w, http.StatusBadRequest, "mode must be off, self-signed or custom")
		return
	}
	prev := a.Lib.Settings.Get().Server.TLS
	if _, err := a.Lib.Settings.Update(func(st *libSettings) error { st.Server.TLS = req.Mode; return nil }); err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	if err := s.applyTLS(); err != nil {
		// Keep the previous working mode rather than a broken one.
		a.Lib.Settings.Update(func(st *libSettings) error { st.Server.TLS = prev; return nil })
		s.applyTLS()
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.networkInfo(r))
}

func (s *Server) handleHotspotStatus(w http.ResponseWriter, r *http.Request) {
	st := s.nw.hotspot.Status(r.Context())
	resp := map[string]any{"status": st}
	if a := s.current(); a != nil {
		set := a.Lib.Settings.Get().Server
		resp["ssid"], resp["hasPassword"] = set.HotspotSSID, set.HotspotPassword != ""
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleHotspot(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		Action   string `json:"action"`
		Device   string `json:"device"`
		SSID     string `json:"ssid"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var err error
	switch req.Action {
	case "start":
		set := a.Lib.Settings.Get().Server
		if req.SSID == "" {
			req.SSID = set.HotspotSSID
		}
		if req.Password == "" {
			req.Password = set.HotspotPassword
		}
		if err = hotspot.Validate(req.SSID, req.Password); err != nil {
			break
		}
		if !a.Lib.ReadOnly {
			a.Lib.Settings.Update(func(st *libSettings) error {
				st.Server.HotspotSSID, st.Server.HotspotPassword = req.SSID, req.Password
				return nil
			})
		}
		err = s.nw.hotspot.Start(r.Context(), req.Device, req.SSID, req.Password)
	case "stop":
		err = s.nw.hotspot.Stop(r.Context())
	default:
		err = errors.New("action must be start or stop")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// New interfaces appear: refresh the Host allowlist and mDNS.
	s.policy.refreshHosts(s.extraHosts())
	writeJSON(w, http.StatusOK, map[string]any{"status": s.nw.hotspot.Status(r.Context())})
}

func (s *Server) handleWifiQR(w http.ResponseWriter, r *http.Request, a *app.App) {
	set := a.Lib.Settings.Get().Server
	if set.HotspotSSID == "" {
		writeError(w, http.StatusNotFound, "No hotspot has been configured.")
		return
	}
	svg, err := qrSVG(wifiQRText(set.HotspotSSID, set.HotspotPassword))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(svg)
}

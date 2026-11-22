package server

import (
	"fmt"
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/kiwix"
	"github.com/KK4ODA/arcana/internal/store"
)

// attention is an item on the dashboard's "needs attention" list.
type attention struct {
	Level   string `json:"level"` // info, warning, error
	Message string `json:"message"`
	View    string `json:"view,omitempty"`
}

// handleStatus answers the dashboard's questions in one request: how much
// is installed, how much space remains, whether the library works offline,
// whether updates exist, whether sharing is active, GPS state, and what
// needs attention.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request, a *app.App) {
	ctx := r.Context()
	var att []attention
	for _, n := range a.StartupNotes {
		att = append(att, attention{Level: "info", Message: n})
	}

	installed, _ := a.Store.ListInstalled(ctx)
	var installedBytes int64
	zims := 0
	unverified := 0
	for _, in := range installed {
		installedBytes += in.TotalSize()
		if in.Resource.Format == "zim" {
			zims++
		}
		if in.Unverified {
			unverified++
		}
	}

	storageRep, err := a.Storage(ctx, nil)
	if err != nil {
		att = append(att, attention{Level: "error", Message: "Cannot read drive capacity: " + err.Error()})
	} else {
		switch {
		case storageRep.Plan.AfterQueue-storageRep.Plan.Reserve < 0:
			att = append(att, attention{Level: "warning", View: "downloads",
				Message: "Queued downloads exceed the free space above your reserve. Some downloads will pause."})
		case storageRep.Plan.Available < storageRep.Plan.Reserve:
			att = append(att, attention{Level: "warning", View: "settings",
				Message: "Free space is below the reserve you set. New downloads are blocked."})
		}
		for _, wmsg := range storageRep.FS.Warnings {
			if storageRep.Volume.FSType == "fat32" || storageRep.Volume.FSType == "ntfs" {
				att = append(att, attention{Level: "warning", Message: wmsg})
			}
		}
	}

	views, _ := a.Downloads.List(ctx)
	active, failed, waiting := 0, 0, 0
	for _, v := range views {
		switch v.State {
		case store.JobFailed:
			failed++
		case store.JobWaiting:
			waiting++
			active++
		case store.JobCompleted, store.JobCancelled:
		default:
			active++
		}
	}
	if failed > 0 {
		att = append(att, attention{Level: "error", View: "downloads", Message: fmt.Sprintf("%d download(s) failed.", failed)})
	}
	if waiting > 0 {
		att = append(att, attention{Level: "info", View: "downloads",
			Message: fmt.Sprintf("%d download(s) waiting to retry (network unavailable or server busy).", waiting)})
	}

	updates, _ := a.Updates(ctx)
	high := 0
	for _, u := range updates {
		if u.Priority == "high" {
			high++
		}
	}
	providers, _ := a.Store.Providers(ctx)
	lastCheck := ""
	for _, p := range providers {
		if p.ID != "arcana" && p.FetchedAt > lastCheck {
			lastCheck = p.FetchedAt
		}
	}
	if lastCheck == "" {
		att = append(att, attention{Level: "info", View: "discover",
			Message: "The online catalog has never been loaded. Connect to the internet and refresh it in Discover to see all available resources."})
	} else if t, err := time.Parse(time.RFC3339, lastCheck); err == nil && time.Since(t) > 30*24*time.Hour {
		att = append(att, attention{Level: "info", View: "discover",
			Message: fmt.Sprintf("The catalog was last refreshed %d days ago. When you are online, refresh it to learn about newer editions.", int(time.Since(t).Hours()/24))})
	}

	var ks kiwix.Status
	if a.Kiwix != nil {
		ks = a.Kiwix.Status()
		switch {
		case ks.State == "unavailable" && zims > 0:
			att = append(att, attention{Level: "error",
				Message: "The ZIM reader (kiwix-serve) was not found for this platform, so installed archives cannot be opened. See README: Portable use."})
		case ks.State == "failed":
			att = append(att, attention{Level: "error", Message: "The ZIM reader failed to start: " + ks.LastError})
		}
	}
	if unverified > 0 {
		att = append(att, attention{Level: "info", View: "library",
			Message: fmt.Sprintf("%d resource(s) came from sources that publish no checksum; their authenticity could not be verified.", unverified)})
	}

	// "Working offline": installed content is served from the drive and
	// the reader is running. Arcana never needs the internet to read.
	offlineReady := len(installed) > 0 && (zims == 0 || ks.State == "running")

	writeJSON(w, 200, map[string]any{
		"library": map[string]any{
			"id": a.Lib.Identity.LibraryID, "label": a.Lib.Identity.Label, "root": rootFor(r, a),
			"readOnly": a.Lib.ReadOnly, "readOnlyReason": a.Lib.ReadOnlyReason,
		},
		"installed":    map[string]any{"count": len(installed), "bytes": installedBytes},
		"storage":      storageRep,
		"offline":      map[string]any{"ready": offlineReady, "reader": ks},
		"downloads":    map[string]any{"active": active, "failed": failed, "waiting": waiting},
		"updates":      map[string]any{"available": len(updates), "highPriority": high, "lastCatalogRefresh": lastCheck},
		"sharing":      s.sharingInfo(r),
		"gps":          gpsSummary(r, a),
		"maps":         mapSummary(a),
		"attention":    att,
		"serverUptime": int64(time.Since(s.startedAt).Seconds()),
	})
}

// rootFor hides the host path from network clients.
func rootFor(r *http.Request, a *app.App) string {
	if isLocal(r) {
		return a.Lib.Root
	}
	return ""
}

func (s *Server) sharingInfo(r *http.Request) map[string]any {
	urls := []string{}
	if s.cfg.ShareOnLAN {
		for _, ip := range lanAddresses() {
			urls = append(urls, fmt.Sprintf("http://%s:%d/", ip, s.appPort))
		}
	}
	return map[string]any{
		"active":      s.cfg.ShareOnLAN,
		"urls":        urls,
		"appPort":     s.appPort,
		"contentPort": s.content,
		"localUrl":    fmt.Sprintf("http://127.0.0.1:%d/", s.appPort),
	}
}

func (s *Server) handleServerInfo(w http.ResponseWriter, r *http.Request) {
	info := s.sharingInfo(r)
	info["note"] = "Devices on the network get read-only access. Discovery by name (arcana.local), QR codes and Wi-Fi hotspot control are planned for Phase 4."
	if a := s.current(); a != nil {
		info["configuredShareOnLan"] = a.Lib.Settings.Get().Server.ShareOnLAN
	}
	writeJSON(w, 200, info)
}

// lanAddresses lists non-loopback IPv4 addresses, private ranges first.
func lanAddresses() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, ipn.IP.String())
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		pi := net.ParseIP(out[i]).IsPrivate()
		pj := net.ParseIP(out[j]).IsPrivate()
		return pi && !pj
	})
	return out
}

// gpsSummary is the dashboard's position readout.
func gpsSummary(r *http.Request, a *app.App) map[string]any {
	if !isLocal(r) && !a.Lib.Settings.Get().GPS.ShareWithClients {
		return map[string]any{"state": "private", "label": "Not shared", "sub": "the host keeps its position private"}
	}
	st := a.GPS.Status()
	out := map[string]any{"state": st.State, "source": st.Source}
	labels := map[string]string{
		"off": "Off", "connecting": "Connecting", "waiting": "Waiting for data", "searching": "Searching for satellites",
		"current": "Position current", "stale": "Position stale", "lost": "Signal lost", "manual": "Fixed position", "error": "Receiver problem",
	}
	out["label"] = labels[st.State]
	out["href"] = "#/maps"
	out["dot"] = map[string]string{"current": "ok", "stale": "warn", "lost": "err", "error": "err",
		"searching": "info", "waiting": "info", "connecting": "info", "manual": "info"}[st.State]
	switch {
	case st.State == "off":
		out["sub"] = "choose a receiver in Settings"
	case st.State == "error":
		out["sub"] = st.Error
	case st.Fix != nil:
		out["sub"] = fmt.Sprintf("%.5f, %.5f", st.Fix.Lat, st.Fix.Lon)
		out["fix"] = st.Fix
		out["age"] = st.Age
	default:
		out["sub"] = st.Detail
	}
	return out
}

func mapSummary(a *app.App) map[string]any {
	ms := a.MapStatus()
	return map[string]any{"sets": len(ms.Sets), "indexing": ms.Indexing, "maxZoom": ms.MaxZoom}
}

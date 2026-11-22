package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/gps"
)

func (s *Server) registerGPS(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/gps", s.withApp(s.gpsVisible(s.handleGPSStatus)))
	mux.HandleFunc("GET /api/gps/stream", s.withApp(s.gpsVisible(s.handleGPSStream)))
	mux.HandleFunc("GET /api/gps/ports", s.adminOnly(s.handleGPSPorts))
	mux.HandleFunc("GET /api/gps/waypoints", s.withApp(s.gpsVisible(s.handleWaypoints)))
	mux.HandleFunc("POST /api/gps/waypoints", s.withApp(s.handleAddWaypoint))
	mux.HandleFunc("DELETE /api/gps/waypoints/{id}", s.withApp(s.handleDeleteWaypoint))
	mux.HandleFunc("GET /api/gps/tracks", s.withApp(s.gpsVisible(s.handleTracks)))
	mux.HandleFunc("GET /api/gps/tracks/{name}", s.withApp(s.gpsVisible(s.handleTrack)))
	mux.HandleFunc("DELETE /api/gps/tracks/{name}", s.withApp(s.handleDeleteTrack))
	mux.HandleFunc("GET /api/gps/export", s.withApp(s.gpsVisible(s.handleGPXExport)))
	mux.HandleFunc("POST /api/gps/import", s.withApp(s.handleGPXImport))
}

// gpsVisible limits position data to the host computer unless the host
// chose to share it with network clients.
func (s *Server) gpsVisible(h func(http.ResponseWriter, *http.Request, *app.App)) func(http.ResponseWriter, *http.Request, *app.App) {
	return func(w http.ResponseWriter, r *http.Request, a *app.App) {
		if !s.isAdmin(r) && !a.Lib.Settings.Get().GPS.ShareWithClients {
			writeError(w, http.StatusForbidden, "The host has not shared its position with network clients.")
			return
		}
		h(w, r, a)
	}
}

func gpsErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, gps.ErrReadOnly):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, os.ErrNotExist):
		writeError(w, http.StatusNotFound, "not found")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

type gpsStatusResponse struct {
	gps.Status
	Shared bool `json:"shared"`
}

func (s *Server) handleGPSStatus(w http.ResponseWriter, r *http.Request, a *app.App) {
	writeJSON(w, http.StatusOK, gpsStatusResponse{a.GPS.Status(), a.Lib.Settings.Get().GPS.ShareWithClients})
}

// handleGPSStream sends status updates as server-sent events: on every new
// fix (at most twice a second) and every 5 seconds so freshness changes
// (current, stale, lost) are visible without a new fix.
func (s *Server) handleGPSStream(w http.ResponseWriter, r *http.Request, a *app.App) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, unsub := a.GPS.Subscribe()
	defer unsub()
	send := func(st gps.Status) bool {
		data, _ := json.Marshal(gpsStatusResponse{st, a.Lib.Settings.Get().GPS.ShareWithClients})
		if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !send(a.GPS.Status()) {
		return
	}
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	var last time.Time
	for {
		select {
		case <-r.Context().Done():
			return
		case st := <-ch:
			if time.Since(last) < 500*time.Millisecond {
				continue
			}
			last = time.Now()
			if !send(st) {
				return
			}
		case <-tick.C:
			if !send(a.GPS.Status()) {
				return
			}
		}
	}
}

func (s *Server) handleGPSPorts(w http.ResponseWriter, r *http.Request) {
	ports, err := gps.SerialPorts()
	resp := map[string]any{"ports": ports}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleWaypoints(w http.ResponseWriter, r *http.Request, a *app.App) {
	ws, err := a.GPS.Waypoints()
	if err != nil {
		gpsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"waypoints": ws})
}

func (s *Server) handleAddWaypoint(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		Name   string   `json:"name"`
		Lat    float64  `json:"lat"`
		Lon    float64  `json:"lon"`
		Ele    *float64 `json:"ele"`
		Note   string   `json:"note"`
		Symbol string   `json:"symbol"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	wp, err := a.GPS.AddWaypoint(gps.Waypoint{Name: req.Name, Lat: req.Lat, Lon: req.Lon, Ele: req.Ele, Note: req.Note, Symbol: req.Symbol})
	if err != nil {
		gpsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, wp)
}

func (s *Server) handleDeleteWaypoint(w http.ResponseWriter, r *http.Request, a *app.App) {
	if err := a.GPS.DeleteWaypoint(r.PathValue("id")); err != nil {
		gpsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleTracks(w http.ResponseWriter, r *http.Request, a *app.App) {
	ts, err := a.GPS.Tracks()
	if err != nil {
		gpsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tracks": ts})
}

// handleTrack returns a track as a GeoJSON feature for the map.
func (s *Server) handleTrack(w http.ResponseWriter, r *http.Request, a *app.App) {
	t, err := a.GPS.Track(r.PathValue("name"))
	if err != nil {
		gpsErr(w, err)
		return
	}
	coords := [][][2]float64{}
	for _, seg := range t.Segments {
		line := make([][2]float64, 0, len(seg))
		for _, p := range seg {
			line = append(line, [2]float64{p.Lon, p.Lat})
		}
		coords = append(coords, line)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"type":       "Feature",
		"properties": map[string]any{"name": t.Name, "length": t.Length(), "points": t.Points()},
		"geometry":   map[string]any{"type": "MultiLineString", "coordinates": coords},
	})
}

func (s *Server) handleDeleteTrack(w http.ResponseWriter, r *http.Request, a *app.App) {
	if err := a.GPS.DeleteTrack(r.PathValue("name")); err != nil {
		gpsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleGPXExport(w http.ResponseWriter, r *http.Request, a *app.App) {
	var tracks []string
	if t := r.URL.Query().Get("tracks"); t != "" {
		tracks = strings.Split(t, ",")
	}
	name := "arcana-waypoints.gpx"
	if len(tracks) == 1 {
		name = tracks[0]
	}
	var buf strings.Builder
	if err := a.GPS.ExportGPX(&buf, tracks); err != nil {
		gpsErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/gpx+xml")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.Header().Set("Cache-Control", "no-store")
	io.WriteString(w, buf.String())
}

func (s *Server) handleGPXImport(w http.ResponseWriter, r *http.Request, a *app.App) {
	body := http.MaxBytesReader(w, r.Body, gps.MaxGPXBytes)
	res, err := a.GPS.ImportGPX(body, r.URL.Query().Get("name"))
	if err != nil {
		gpsErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

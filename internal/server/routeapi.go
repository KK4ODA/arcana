package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/routing"
)

func (s *Server) registerRouting(mux *http.ServeMux) {
	// Directions are computed from installed data and reveal nothing
	// about the host, so every client may ask for them.
	mux.HandleFunc("GET /api/roads", s.withApp(s.handleRoads))
	mux.HandleFunc("GET /api/route", s.withApp(s.handleRoute))
}

func (s *Server) handleRoads(w http.ResponseWriter, r *http.Request, a *app.App) {
	writeJSON(w, http.StatusOK, map[string]any{"networks": a.Roads().Networks(), "enabled": a.RoadProvider() != nil})
}

// parsePoints reads "lat,lon;lat,lon;…".
func parsePoints(s string) ([]routing.Point, error) {
	var pts []routing.Point
	for _, part := range strings.Split(s, ";") {
		ll := strings.Split(strings.TrimSpace(part), ",")
		if len(ll) != 2 {
			return nil, errors.New("points must be written lat,lon;lat,lon")
		}
		lat, err1 := strconv.ParseFloat(strings.TrimSpace(ll[0]), 64)
		lon, err2 := strconv.ParseFloat(strings.TrimSpace(ll[1]), 64)
		if err1 != nil || err2 != nil {
			return nil, errors.New("points must be written lat,lon;lat,lon")
		}
		pts = append(pts, routing.Point{Lat: lat, Lon: lon})
	}
	return pts, nil
}

func (s *Server) handleRoute(w http.ResponseWriter, r *http.Request, a *app.App) {
	q := r.URL.Query()
	if len(q.Get("points")) > 2000 {
		writeError(w, http.StatusBadRequest, "too many points")
		return
	}
	pts, err := parsePoints(q.Get("points"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mode := routing.Car
	if m := q.Get("mode"); m != "" {
		var ok bool
		if mode, ok = routing.ParseMode(m); !ok {
			writeError(w, http.StatusBadRequest, "mode must be car, bike or foot")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	rt, net, err := a.Roads().Route(ctx, pts, mode)
	if err != nil {
		status := http.StatusUnprocessableEntity
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusServiceUnavailable
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"route": rt, "network": net})
}

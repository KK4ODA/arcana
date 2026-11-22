package server

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/maps"
	"github.com/KK4ODA/arcana/internal/web"
)

func (s *Server) registerMaps(mux *http.ServeMux) {
	// Tiles and glyphs are read by every client, including LAN clients.
	mux.HandleFunc("GET /maps/tiles/{z}/{x}/{y}", s.withApp(s.handleTile))
	mux.HandleFunc("GET /maps/fonts/{stack}/{range}", handleGlyphs)
	mux.HandleFunc("GET /api/maps", s.withApp(s.handleMapStatus))
	mux.HandleFunc("GET /api/maps/search", s.withApp(s.handlePlaceSearch))
	// Planning contacts the map source, so it is a host-only action like
	// every other mutation.
	mux.HandleFunc("POST /api/maps/plan", s.withApp(s.handleAreaPlan))
	mux.HandleFunc("POST /api/maps/areas", s.withApp(s.handleAreaQueue))
}

func (s *Server) handleTile(w http.ResponseWriter, r *http.Request, a *app.App) {
	z, err1 := strconv.ParseUint(r.PathValue("z"), 10, 8)
	x, err2 := strconv.ParseUint(r.PathValue("x"), 10, 32)
	y, err3 := strconv.ParseUint(strings.TrimSuffix(r.PathValue("y"), ".mvt"), 10, 32)
	if err1 != nil || err2 != nil || err3 != nil || z > 22 {
		http.NotFound(w, r)
		return
	}
	svc := a.Maps()
	etag := `"` + strconv.FormatUint(svc.Generation(), 36) + `"`
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	data, comp, err := svc.TileOrDerived(uint8(z), uint32(x), uint32(y))
	if errors.Is(err, maps.ErrNoTile) {
		// MapLibre treats 204 as an empty tile.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		a.Log.Warn("map tile", "z", z, "x", x, "y", y, "err", err)
		http.Error(w, "tile unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.mapbox-vector-tile")
	w.Header().Set("Vary", "Accept-Encoding")
	acceptsGzip := strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
	switch {
	case comp == maps.CompressionGzip && acceptsGzip:
		w.Header().Set("Content-Encoding", "gzip")
	case comp == maps.CompressionNone && acceptsGzip && len(data) > 1024:
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
		zw.Write(data)
		zw.Close()
		data = buf.Bytes()
		w.Header().Set("Content-Encoding", "gzip")
	case comp != maps.CompressionNone:
		raw, err := maps.Decompress(data, comp)
		if err != nil {
			http.Error(w, "tile unavailable", http.StatusInternalServerError)
			return
		}
		data = raw
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Write(data)
}

var glyphRangeRe = regexp.MustCompile(`^\d{1,5}-\d{1,5}\.pbf$`)

// handleGlyphs serves map label glyphs. A font stack lists fonts in order of
// preference; the first one bundled is used. Ranges that are not bundled
// (for example CJK, which MapLibre draws with the system font) get an empty
// glyph set so the map renders without errors.
func handleGlyphs(w http.ResponseWriter, r *http.Request) {
	rng := r.PathValue("range")
	if !glyphRangeRe.MatchString(rng) {
		http.NotFound(w, r)
		return
	}
	static, _ := fs.Sub(web.Static, "static/maps/fonts")
	w.Header().Set("Content-Type", "application/x-protobuf")
	w.Header().Set("Cache-Control", "max-age=86400")
	for _, font := range strings.Split(r.PathValue("stack"), ",") {
		font = strings.TrimSpace(font)
		if font == "" || strings.ContainsAny(font, `/\.`) {
			continue
		}
		f, err := static.Open(font + "/" + rng)
		if err != nil {
			continue
		}
		defer f.Close()
		io.Copy(w, f)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleMapStatus(w http.ResponseWriter, r *http.Request, a *app.App) {
	writeJSON(w, http.StatusOK, a.MapStatus())
}

func (s *Server) handlePlaceSearch(w http.ResponseWriter, r *http.Request, a *app.App) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 200 {
		q = q[:200]
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	places, err := a.SearchPlaces(r.Context(), q, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if places == nil {
		places = []maps.Place{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"places": places})
}

type areaRequest struct {
	Name    string    `json:"name"`
	BBox    maps.BBox `json:"bbox"`
	MaxZoom uint8     `json:"maxZoom"`
}

func (s *Server) handleAreaPlan(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req areaRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	plan, err := a.PlanArea(r.Context(), req.Name, req.BBox, req.MaxZoom)
	if err != nil {
		writeError(w, areaStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) handleAreaQueue(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req areaRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	plan, err := a.QueueArea(r.Context(), req.Name, req.BBox, req.MaxZoom)
	if err != nil {
		writeError(w, areaStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func areaStatus(err error) int {
	if errors.Is(err, maps.ErrTooManyTiles) {
		return http.StatusBadRequest
	}
	if st := errStatus(err); st != http.StatusInternalServerError {
		return st
	}
	return http.StatusBadGateway
}

package server

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/translate"
)

// Translation runs in the reader's browser; Arcana serves the engine and
// models (decompressed, checked against Mozilla's published hash) to
// every client, like any other installed reference material.
func (s *Server) registerTranslate(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/translate", s.withApp(s.handleTranslateInfo))
	mux.HandleFunc("GET /translate/engine.wasm", s.withApp(s.handleTranslateEngine))
	mux.HandleFunc("GET /translate/model/{resource}/{name}", s.withApp(s.handleTranslateModel))
}

type translateFile struct {
	translate.ModelFile
	URL string `json:"url"`
}

func (s *Server) handleTranslateInfo(w http.ResponseWriter, r *http.Request, a *app.App) {
	eng, langs := a.Translation(r.Context())
	type lang struct {
		Code       string          `json:"code"`
		Name       string          `json:"name"`
		ResourceID string          `json:"resourceId"`
		Version    string          `json:"version"`
		Directions []string        `json:"directions"`
		Files      []translateFile `json:"files"`
	}
	out := []lang{}
	for _, l := range langs {
		ol := lang{Code: l.Code, Name: l.Name, ResourceID: l.ResourceID, Version: l.Version, Directions: l.Directions}
		for _, f := range l.Files {
			ol.Files = append(ol.Files, translateFile{ModelFile: f,
				URL: "/translate/model/" + url.PathEscape(l.ResourceID) + "/" + url.PathEscape(f.Name) + "?v=" + url.QueryEscape(f.DecompressedHash[:12])})
		}
		out = append(out, ol)
	}
	resp := map[string]any{"engine": eng != nil, "languages": out}
	if eng != nil {
		resp["engineVersion"] = eng.Version
		resp["engineUrl"] = "/translate/engine.wasm?v=" + url.QueryEscape(eng.DecompressedHash[:min(12, len(eng.DecompressedHash))])
	}
	writeJSON(w, http.StatusOK, resp)
}

// serveDecompressed sends a zstd file's verified content. The ETag is the
// decompressed SHA-256, so browsers keep their copy until it changes.
func (s *Server) serveDecompressed(w http.ResponseWriter, r *http.Request, p, hash string, size int64, ctype string) {
	if hash == "" {
		http.Error(w, "missing checksum", http.StatusInternalServerError)
		return
	}
	etag := `"` + hash + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, max-age=604800")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if err := s.translateCheck.Check(p, hash, size); err != nil {
		s.log.Warn("translation file damaged", "file", p, "err", err)
		status := http.StatusInternalServerError
		if errors.Is(err, translate.ErrDamaged) {
			status = http.StatusUnprocessableEntity
		}
		http.Error(w, "This file is damaged. Run a full library check and download it again.", status)
		return
	}
	w.Header().Set("Content-Type", ctype)
	if size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	if r.Method == http.MethodHead {
		return
	}
	if _, err := translate.Decompress(p, w); err != nil {
		s.log.Warn("serve translation file", "file", p, "err", err)
	}
}

func (s *Server) handleTranslateEngine(w http.ResponseWriter, r *http.Request, a *app.App) {
	eng, _ := a.Translation(r.Context())
	if eng == nil {
		http.Error(w, "The translation engine is not installed.", http.StatusNotFound)
		return
	}
	s.serveDecompressed(w, r, eng.Path, eng.DecompressedHash, eng.DecompressedSize, "application/wasm")
}

func (s *Server) handleTranslateModel(w http.ResponseWriter, r *http.Request, a *app.App) {
	name := r.PathValue("name")
	if strings.ContainsAny(name, `/\`) {
		http.NotFound(w, r)
		return
	}
	p, mf, err := a.TranslationFile(r.Context(), r.PathValue("resource"), name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.serveDecompressed(w, r, p, mf.DecompressedHash, mf.DecompressedSize, "application/octet-stream")
}

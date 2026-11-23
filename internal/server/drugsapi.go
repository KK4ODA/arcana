package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/drugs"
)

// The drug label reference is reference content like an archive: every
// client may read it.
func (s *Server) registerDrugs(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/drugs", s.withApp(s.handleDrugsInfo))
	mux.HandleFunc("GET /api/drugs/search", s.withApp(s.handleDrugsSearch))
	mux.HandleFunc("GET /api/drugs/group", s.withApp(s.handleDrugsGroup))
	mux.HandleFunc("GET /api/drugs/label/{id}", s.withApp(s.handleDrugsLabel))
	mux.HandleFunc("GET /api/drugs/situations", s.withApp(s.handleDrugsSituations))
}

func drugsReader(w http.ResponseWriter, a *app.App) *drugs.Reader {
	r := a.Drugs()
	if r == nil {
		writeError(w, http.StatusNotFound, "The FDA drug label reference is not installed. Download it from Discover → Medicine and public health.")
	}
	return r
}

func drugsOptions(r *http.Request) drugs.SearchOptions {
	q := r.URL.Query()
	o := drugs.SearchOptions{Homeopathic: q.Get("homeopathic") == "1", Limit: clampInt(q.Get("limit"), 25, 1, 100), Offset: clampInt(q.Get("offset"), 0, 0, 100000)}
	switch k := q.Get("kind"); k {
	case drugs.KindOTC, drugs.KindRx, drugs.KindHomeopathic:
		o.Kind = k
	}
	return o
}

func (s *Server) handleDrugsInfo(w http.ResponseWriter, r *http.Request, a *app.App) {
	resp := map[string]any{"installed": false, "situations": drugs.Situations, "resourceId": drugs.ResourceID}
	if rd := a.Drugs(); rd != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		st, err := rd.Stats(ctx)
		if err == nil {
			resp["installed"], resp["stats"] = true, st
		}
		if in, err := a.Store.GetInstalled(r.Context(), a.DrugsResourceID()); err == nil {
			resp["citation"] = app.CitationFor(in)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleDrugsSearch(w http.ResponseWriter, r *http.Request, a *app.App) {
	rd := drugsReader(w, a)
	if rd == nil {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len(q) > 200 {
		writeError(w, http.StatusBadRequest, "enter a medicine or ingredient name")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	groups, total, err := rd.Search(ctx, q, drugsOptions(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"query": q, "groups": groups, "total": total})
}

func (s *Server) handleDrugsGroup(w http.ResponseWriter, r *http.Request, a *app.App) {
	rd := drugsReader(w, a)
	if rd == nil {
		return
	}
	key := r.URL.Query().Get("key")
	if key == "" || len(key) > 500 {
		writeError(w, http.StatusBadRequest, "key is required")
		return
	}
	products, total, err := rd.GroupProducts(r.Context(), key, drugsOptions(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "products": products, "total": total})
}

func (s *Server) handleDrugsLabel(w http.ResponseWriter, r *http.Request, a *app.App) {
	rd := drugsReader(w, a)
	if rd == nil {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid label")
		return
	}
	p, secs, err := rd.Label(r.Context(), id)
	switch {
	case errors.Is(err, drugs.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"product": p, "sections": nonNil(secs)})
}

func (s *Server) handleDrugsSituations(w http.ResponseWriter, r *http.Request, a *app.App) {
	rd := drugsReader(w, a)
	if rd == nil {
		return
	}
	var slugs []string
	for _, s := range strings.Split(r.URL.Query().Get("s"), ",") {
		if s = strings.TrimSpace(s); s != "" && len(slugs) < 5 {
			slugs = append(slugs, s)
		}
	}
	kind := r.URL.Query().Get("kind")
	if kind != "all" && kind != drugs.KindRx {
		kind = drugs.KindOTC
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res, err := rd.BySituation(ctx, slugs, drugs.SituationOptions{Kind: kind, Homeopathic: r.URL.Query().Get("homeopathic") == "1"})
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res})
}

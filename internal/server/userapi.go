package server

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/userdata"
)

func (s *Server) registerPhase2(mux *http.ServeMux) {
	// User data. Bookmarks and favorites are readable by network clients
	// (they are part of the shared library); history and notes are private
	// to the host computer.
	mux.HandleFunc("GET /api/user/bookmarks", s.withApp(s.handleBookmarks))
	mux.HandleFunc("POST /api/user/bookmarks", s.withApp(s.handleAddBookmark))
	mux.HandleFunc("DELETE /api/user/bookmarks/{id}", s.withApp(s.handleRemoveBookmark))
	mux.HandleFunc("GET /api/user/favorites", s.withApp(s.handleFavorites))
	mux.HandleFunc("PUT /api/user/favorites", s.withApp(s.handleSetFavorite))
	mux.HandleFunc("GET /api/user/recent", localOnly(s.withApp(s.handleRecent)))
	mux.HandleFunc("POST /api/user/recent", s.withApp(s.handleRecordVisit))
	mux.HandleFunc("DELETE /api/user/recent", s.withApp(s.handleClearRecent))
	mux.HandleFunc("GET /api/user/notes", localOnly(s.withApp(s.handleNotes)))
	mux.HandleFunc("POST /api/user/notes", s.withApp(s.handleSaveNote))
	mux.HandleFunc("DELETE /api/user/notes/{id}", s.withApp(s.handleDeleteNote))
	mux.HandleFunc("GET /api/user/documents", localOnly(s.withApp(s.handleUserDocs)))
	mux.HandleFunc("POST /api/user/documents", s.withApp(s.handleUploadDoc))
	mux.HandleFunc("DELETE /api/user/documents/{name}", s.withApp(s.handleDeleteDoc))
	mux.HandleFunc("POST /api/presets", s.withApp(s.handleSavePreset))
	mux.HandleFunc("GET /api/documents/text", s.withApp(s.handleDocText))

	// Background tasks, transfer, maintenance and application updates.
	mux.HandleFunc("GET /api/tasks", s.withApp(s.handleTasks))
	mux.HandleFunc("POST /api/tasks/{id}/cancel", s.withApp(s.handleCancelTask))
	mux.HandleFunc("POST /api/transfer/export", s.withApp(s.handleExport))
	mux.HandleFunc("GET /api/transfer/scan", localOnly(s.withApp(s.handleScan)))
	mux.HandleFunc("POST /api/transfer/import", s.withApp(s.handleImport))
	mux.HandleFunc("POST /api/manifest", s.withApp(s.handleManifest))
	mux.HandleFunc("POST /api/check/full", s.withApp(s.handleFullCheck))
	mux.HandleFunc("POST /api/documents/reindex", s.withApp(s.handleReindex))
	mux.HandleFunc("GET /api/app/update", localOnly(s.withApp(s.handleUpdateInfo)))
	mux.HandleFunc("POST /api/app/update/check", s.withApp(s.handleUpdateCheck))
	mux.HandleFunc("POST /api/app/update/download", s.withApp(s.handleUpdateDownload))
	mux.HandleFunc("POST /api/app/update/install", s.withApp(s.handleUpdateInstall))
	mux.HandleFunc("POST /api/app/update/stage", s.withApp(s.handleUpdateStage))
	mux.HandleFunc("POST /api/app/update/apply", s.withApp(s.handleUpdateApply))
	mux.HandleFunc("POST /api/app/update/rollback", s.withApp(s.handleUpdateRollback))
}

func userErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, userdata.ErrReadOnly), errors.Is(err, library.ErrReadOnly):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func (s *Server) handleBookmarks(w http.ResponseWriter, r *http.Request, a *app.App) {
	b, err := a.User.Bookmarks()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if b == nil {
		b = []userdata.Bookmark{}
	}
	writeJSON(w, 200, map[string]any{"bookmarks": b})
}

type pageRef struct {
	ResourceID string `json:"resourceId"`
	Path       string `json:"path"`
	Title      string `json:"title"`
}

func (s *Server) handleAddBookmark(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req pageRef
	if err := readJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	b, err := a.User.AddBookmark(req.ResourceID, req.Path, req.Title)
	if err != nil {
		userErr(w, err)
		return
	}
	writeJSON(w, 200, b)
}

func (s *Server) handleRemoveBookmark(w http.ResponseWriter, r *http.Request, a *app.App) {
	if err := a.User.RemoveBookmark(r.PathValue("id")); err != nil {
		userErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "removed"})
}

func (s *Server) handleFavorites(w http.ResponseWriter, r *http.Request, a *app.App) {
	f, err := a.User.Favorites()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if f == nil {
		f = []string{}
	}
	writeJSON(w, 200, map[string]any{"favorites": f})
}

func (s *Server) handleSetFavorite(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		ResourceID string `json:"resourceId"`
		On         bool   `json:"on"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := a.User.SetFavorite(req.ResourceID, req.On); err != nil {
		userErr(w, err)
		return
	}
	s.handleFavorites(w, r, a)
}

func (s *Server) handleRecent(w http.ResponseWriter, r *http.Request, a *app.App) {
	v, err := a.User.Recent()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if v == nil {
		v = []userdata.Visit{}
	}
	writeJSON(w, 200, map[string]any{"recent": v})
}

func (s *Server) handleRecordVisit(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req pageRef
	if err := readJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := a.User.RecordVisit(req.ResourceID, req.Path, req.Title); err != nil {
		userErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) handleClearRecent(w http.ResponseWriter, r *http.Request, a *app.App) {
	if err := a.User.ClearRecent(); err != nil {
		userErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "cleared"})
}

func (s *Server) handleNotes(w http.ResponseWriter, r *http.Request, a *app.App) {
	n, err := a.User.Notes(r.URL.Query().Get("resource"))
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if n == nil {
		n = []userdata.Note{}
	}
	writeJSON(w, 200, map[string]any{"notes": n})
}

func (s *Server) handleSaveNote(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req userdata.Note
	if err := readJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	n, err := a.User.SaveNote(req)
	if err != nil {
		userErr(w, err)
		return
	}
	writeJSON(w, 200, n)
}

func (s *Server) handleDeleteNote(w http.ResponseWriter, r *http.Request, a *app.App) {
	if err := a.User.DeleteNote(r.PathValue("id")); err != nil {
		userErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Server) handleUserDocs(w http.ResponseWriter, r *http.Request, a *app.App) {
	docs, err := a.UserDocuments()
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if docs == nil {
		docs = []app.UserDocument{}
	}
	writeJSON(w, 200, map[string]any{"documents": docs, "indexed": a.Docs.Count(r.Context())})
}

// handleUploadDoc accepts the raw file body; the name comes from the
// X-Filename header (URL-encoded) and is validated as a single safe file
// name.
func (s *Server) handleUploadDoc(w http.ResponseWriter, r *http.Request, a *app.App) {
	name := r.Header.Get("X-Filename")
	if dec, err := urlUnescape(name); err == nil {
		name = dec
	}
	r.Body = http.MaxBytesReader(w, r.Body, app.MaxUserDocument)
	saved, err := a.SaveUserDocument(name, r.Body, r.ContentLength)
	if err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"name": saved})
}

func (s *Server) handleDeleteDoc(w http.ResponseWriter, r *http.Request, a *app.App) {
	if err := a.DeleteUserDocument(r.PathValue("name")); err != nil {
		userErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

// handleDocText returns extracted text for an in-app preview. Text is
// returned as JSON and rendered with textContent, so documents cannot inject
// markup into the interface.
func (s *Server) handleDocText(w http.ResponseWriter, r *http.Request, a *app.App) {
	key := r.URL.Query().Get("key")
	if strings.HasPrefix(key, "user:") && !isLocal(r) {
		writeError(w, 403, "Your documents are only available on the computer running Arcana.")
		return
	}
	title, text, u, err := a.Docs.Text(r.Context(), key, 2<<20)
	if err != nil {
		writeError(w, 404, "document not indexed")
		return
	}
	writeJSON(w, 200, map[string]any{"key": key, "title": title, "text": text, "url": u, "truncated": len(text) >= 2<<20})
}

func (s *Server) handleSavePreset(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		ResourceIDs []string `json:"resourceIds"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	p, err := a.SavePreset(r.Context(), req.Title, req.Description, req.ResourceIDs)
	if err != nil {
		userErr(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request, a *app.App) {
	writeJSON(w, 200, map[string]any{"tasks": a.Tasks.List()})
}

func (s *Server) handleCancelTask(w http.ResponseWriter, r *http.Request, a *app.App) {
	if err := a.Tasks.Cancel(r.PathValue("id")); err != nil {
		writeError(w, 404, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "cancelling"})
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		ResourceIDs   []string `json:"resourceIds"`
		Destination   string   `json:"destination"`
		UserDocuments bool     `json:"userDocuments"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	t, err := a.StartExport(req.ResourceIDs, filepath.Clean(req.Destination), req.UserDocuments)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request, a *app.App) {
	src := r.URL.Query().Get("source")
	if !filepath.IsAbs(src) {
		writeError(w, 400, "use a full path to the export folder or library")
		return
	}
	m, plans, err := a.ScanSource(r.Context(), filepath.Clean(src))
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"source": m.SourceLabel, "libraryId": m.SourceLibraryID, "plans": plans,
		"userDocuments": len(m.UserDocuments)})
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		Source        string   `json:"source"`
		ResourceIDs   []string `json:"resourceIds"`
		UserDocuments bool     `json:"userDocuments"`
	}
	if err := readJSON(r, &req); err != nil || !filepath.IsAbs(req.Source) {
		writeError(w, 400, "use a full path to the export folder or library")
		return
	}
	t, err := a.StartImport(filepath.Clean(req.Source), req.ResourceIDs, req.UserDocuments)
	if err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request, a *app.App) {
	p, err := a.WriteLibraryManifest(r.Context())
	if err != nil {
		writeError(w, errStatus(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"path": p})
}

func (s *Server) handleFullCheck(w http.ResponseWriter, r *http.Request, a *app.App) {
	t, err := a.StartFullCheck()
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) handleReindex(w http.ResponseWriter, r *http.Request, a *app.App) {
	a.RequestReindex()
	writeJSON(w, 200, map[string]string{"status": "scheduled"})
}

func (s *Server) handleUpdateInfo(w http.ResponseWriter, r *http.Request, a *app.App) {
	writeJSON(w, 200, a.AppUpdateInfo())
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request, a *app.App) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	info, err := a.CheckAppUpdate(ctx)
	if err != nil {
		info.CheckMessage = err.Error()
	}
	writeJSON(w, 200, info)
}

func (s *Server) handleUpdateDownload(w http.ResponseWriter, r *http.Request, a *app.App) {
	t, err := a.StartAppUpdateDownload()
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, t)
}

// handleUpdateInstall downloads, verifies and installs the newest version
// in one step, then restarts Arcana (when this copy can restart itself).
func (s *Server) handleUpdateInstall(w http.ResponseWriter, r *http.Request, a *app.App) {
	t, err := a.StartAppUpdateInstall()
	if err != nil {
		writeError(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, t)
}

func (s *Server) handleUpdateStage(w http.ResponseWriter, r *http.Request, a *app.App) {
	var req struct {
		Source string `json:"source"`
	}
	if err := readJSON(r, &req); err != nil || !filepath.IsAbs(req.Source) {
		writeError(w, 400, "use a full path to the folder containing release.json")
		return
	}
	m, err := a.StageAppUpdateFromDir(r.Context(), filepath.Clean(req.Source))
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, m)
}

func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request, a *app.App) {
	st, err := a.Updater.Apply()
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"status": st, "message": "Installed. Close Arcana and start it again to use version " + st.AppliedVersion + "."})
}

func (s *Server) handleUpdateRollback(w http.ResponseWriter, r *http.Request, a *app.App) {
	st, err := a.Updater.Rollback()
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"status": st, "message": "Restored the previous version. Restart Arcana."})
}

// handleUserDoc serves a user document on the content origin, with the same
// rules as installed documents.
func (s *Server) handleUserDocFile(w http.ResponseWriter, r *http.Request) {
	a := s.current()
	if a == nil || !isLocal(r) {
		http.NotFound(w, r)
		return
	}
	p, err := a.UserDocumentPath(r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	serveDocument(w, r, p)
}

func serveDocument(w http.ResponseWriter, r *http.Request, p string) {
	fh, err := os.Open(p)
	if err != nil {
		http.Error(w, "File is missing from the drive. Run a library check.", http.StatusNotFound)
		return
	}
	defer fh.Close()
	st, _ := fh.Stat()
	ctype := mime.TypeByExtension(strings.ToLower(filepath.Ext(p)))
	switch {
	case strings.HasPrefix(ctype, "application/pdf"), strings.HasPrefix(ctype, "application/epub+zip"),
		strings.HasPrefix(ctype, "text/plain"), strings.HasPrefix(ctype, "image/png"), strings.HasPrefix(ctype, "image/jpeg"),
		strings.HasPrefix(ctype, "image/gif"), strings.HasPrefix(ctype, "image/webp"), strings.HasPrefix(ctype, "audio/"),
		strings.HasPrefix(ctype, "video/mp4"), strings.HasPrefix(ctype, "video/webm"), strings.HasPrefix(ctype, "text/csv"),
		strings.HasPrefix(ctype, "text/markdown"):
	case strings.HasSuffix(strings.ToLower(p), ".md"):
		ctype = "text/plain; charset=utf-8"
	default:
		// Anything that could run in the browser (HTML, SVG, scripts) is
		// downloaded rather than rendered.
		ctype = "application/octet-stream"
		w.Header().Set("Content-Disposition", "attachment")
	}
	w.Header().Set("Content-Type", ctype)
	csp := "default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'; media-src 'self'"
	if !strings.HasPrefix(ctype, "application/pdf") {
		// Browsers' PDF viewers do not run in sandboxed documents; PDFs
		// are still isolated on the content origin.
		csp += "; sandbox"
	}
	w.Header().Set("Content-Security-Policy", csp)
	http.ServeContent(w, r, "", st.ModTime(), fh)
}

func urlUnescape(s string) (string, error) {
	return url.PathUnescape(s)
}

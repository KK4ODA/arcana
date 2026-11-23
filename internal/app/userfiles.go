package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/docs"
	"github.com/KK4ODA/arcana/internal/fsutil"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/storage"
)

// UserDocument describes a file in user/documents.
type UserDocument struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	URL      string    `json:"url"`
	Indexed  bool      `json:"indexed"`
}

// MaxUserDocument bounds one uploaded document (2 GB).
const MaxUserDocument = 2 << 30

// UserDocuments lists the user's own documents.
func (a *App) UserDocuments() ([]UserDocument, error) {
	entries, err := os.ReadDir(a.Lib.Documents())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []UserDocument
	for _, e := range entries {
		if !e.Type().IsRegular() || fsutil.ValidateFileName(e.Name()) != nil {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, UserDocument{Name: e.Name(), Size: info.Size(), Modified: info.ModTime().UTC(),
			URL: docs.UserDocURL(e.Name()), Indexed: docs.Supported(e.Name())})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// SaveUserDocument stores an uploaded document. Existing files are never
// replaced: a new name is chosen instead.
func (a *App) SaveUserDocument(name string, r io.Reader, size int64) (string, error) {
	if a.Lib.ReadOnly {
		return "", library.ErrReadOnly
	}
	name = strings.TrimSpace(name)
	if err := fsutil.ValidateFileName(name); err != nil {
		return "", fmt.Errorf("file name: %w", err)
	}
	if size > MaxUserDocument {
		return "", errors.New("documents larger than 2 GB cannot be added through the browser; copy them into the user/documents folder instead")
	}
	vol, err := storage.Stat(a.Lib.Root)
	if err == nil {
		if err := storage.CheckWrite(vol.AvailBytes, size, 0, a.Downloads.Reserve(vol)); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(a.Lib.Documents(), 0o755); err != nil {
		return "", err
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	target := filepath.Join(a.Lib.Documents(), name)
	for i := 2; ; i++ {
		if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
			break
		}
		name = fmt.Sprintf("%s (%d)%s", base, i, ext)
		target = filepath.Join(a.Lib.Documents(), name)
	}
	tmp, err := os.CreateTemp(a.Lib.Documents(), ".upload-*")
	if err != nil {
		return "", err
	}
	n, err := io.Copy(tmp, io.LimitReader(r, MaxUserDocument+1))
	if err == nil && n > MaxUserDocument {
		err = errors.New("document too large")
	}
	if err == nil {
		err = tmp.Sync()
	}
	tmp.Close()
	if err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := fsutil.MoveFile(tmp.Name(), target); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	a.RequestReindex()
	return name, nil
}

// DeleteUserDocument moves a document to user/documents/.deleted so it can
// be recovered from the drive.
func (a *App) DeleteUserDocument(name string) error {
	if a.Lib.ReadOnly {
		return library.ErrReadOnly
	}
	if err := fsutil.ValidateFileName(name); err != nil {
		return err
	}
	src := filepath.Join(a.Lib.Documents(), name)
	if _, err := os.Stat(src); err != nil {
		return errors.New("no such document")
	}
	trash := filepath.Join(a.Lib.Documents(), ".deleted")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, filepath.Join(trash, time.Now().UTC().Format("20060102-150405")+"-"+name)); err != nil {
		return err
	}
	a.RequestReindex()
	return nil
}

// UserDocumentPath resolves a user document for serving.
func (a *App) UserDocumentPath(name string) (string, error) {
	if err := fsutil.ValidateFileName(name); err != nil {
		return "", err
	}
	p := filepath.Join(a.Lib.Documents(), name)
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		return "", errors.New("no such document")
	}
	return p, nil
}

// ---------------------------------------------------------------- index

func (a *App) openDocsIndex() error {
	var err error
	if a.Lib.ReadOnly {
		a.Docs, err = docs.OpenIndex("")
		return err
	}
	p := filepath.Join(a.Lib.Index(), "documents.db")
	a.Docs, err = docs.OpenIndex(p)
	if err != nil {
		// The index is a cache: start over.
		for _, suffix := range []string{"", "-wal", "-shm"} {
			_ = os.Remove(p + suffix)
		}
		a.Docs, err = docs.OpenIndex(p)
	}
	return err
}

// RequestReindex schedules a document index refresh.
func (a *App) RequestReindex() {
	if a.reindex == nil {
		return
	}
	select {
	case a.reindex <- struct{}{}:
	default:
	}
}

func (a *App) reindexLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.reindex:
		}
		// Coalesce bursts of changes.
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
		if _, err := a.ReindexDocuments(ctx); err != nil && ctx.Err() == nil {
			a.Log.Warn("document index", "err", err)
		}
	}
}

// IndexableDocuments lists installed non-ZIM resource files and user
// documents.
func (a *App) IndexableDocuments(ctx context.Context) ([]docs.Doc, error) {
	var out []docs.Doc
	installed, err := a.Store.ListInstalled(ctx)
	if err != nil {
		return nil, err
	}
	for _, in := range installed {
		if in.Resource.Format == "zim" || in.Resource.Format == "pmtiles" || in.Resource.Format == "mbtiles" || in.Resource.Format == "arcroute" {
			continue
		}
		for _, f := range in.Files {
			name := filepath.Base(f.Path)
			if !docs.Supported(name) {
				continue
			}
			p, err := a.Content.FilePath(in, f)
			if err != nil {
				continue
			}
			st, err := os.Stat(p)
			if err != nil {
				continue
			}
			out = append(out, docs.Doc{Key: "res:" + in.ResourceID + "/" + name, Source: "resource", ResourceID: in.ResourceID,
				Name: name, Path: p, URL: docs.FileURL(in.ResourceID, name), Size: st.Size(), ModTime: st.ModTime().Unix()})
		}
	}
	userDocs, _ := a.UserDocuments()
	for _, d := range userDocs {
		if !d.Indexed {
			continue
		}
		p := filepath.Join(a.Lib.Documents(), d.Name)
		out = append(out, docs.Doc{Key: "user:" + d.Name, Source: "user", Name: d.Name, Path: p, URL: d.URL,
			Size: d.Size, ModTime: d.Modified.Unix()})
	}
	return out, nil
}

// ReindexDocuments brings the document index up to date.
func (a *App) ReindexDocuments(ctx context.Context) (docs.SyncResult, error) {
	list, err := a.IndexableDocuments(ctx)
	if err != nil {
		return docs.SyncResult{}, err
	}
	return a.Docs.Sync(ctx, list, nil)
}

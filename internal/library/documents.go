package library

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/fsutil"
)

// MaxDocumentDepth limits how deeply user documents may be nested in
// folders below user/documents.
const MaxDocumentDepth = 32

// ErrNoDocument is returned for a user document that does not exist (or is
// not a regular file reached without links).
var ErrNoDocument = errors.New("no such document")

// DocumentFile is a file in user/documents. Path is relative to that folder
// and slash-separated, such as "Radios/Yaesu/FT-991A.pdf".
type DocumentFile struct {
	Path    string
	Size    int64
	ModTime time.Time
}

// DocumentFiles lists the user's documents, including those in folders.
// Hidden entries (names starting with a dot, such as .deleted and uploads
// in progress), links, and names that are not valid on every file system
// are skipped, as are folders nested more than MaxDocumentDepth deep.
func (l Layout) DocumentFiles() ([]DocumentFile, error) {
	root := l.Documents()
	var out []DocumentFile
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil // An unreadable folder is left out.
		}
		if p == root {
			return nil
		}
		rel, rerr := fsutil.ToSlashRel(root, p)
		if rerr != nil || fsutil.ValidateFileName(d.Name()) != nil {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if strings.Count(rel, "/")+1 >= MaxDocumentDepth {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		out = append(out, DocumentFile{Path: rel, Size: info.Size(), ModTime: info.ModTime().UTC()})
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Path) < strings.ToLower(out[j].Path) })
	return out, err
}

// DocumentPath resolves a relative, slash-separated document path to a
// regular file inside user/documents. Every component must be a valid name,
// and none may be a link, so a document never leads outside the library.
func (l Layout) DocumentPath(rel string) (string, error) {
	p, err := fsutil.SafeJoin(l.Documents(), rel)
	if err != nil {
		return "", err
	}
	parts := strings.Split(rel, "/")
	if len(parts) > MaxDocumentDepth {
		return "", ErrNoDocument
	}
	cur := l.Documents()
	for i, part := range parts {
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if err != nil {
			return "", ErrNoDocument
		}
		if last := i == len(parts)-1; last && !st.Mode().IsRegular() || !last && !st.IsDir() {
			return "", ErrNoDocument
		}
	}
	return p, nil
}

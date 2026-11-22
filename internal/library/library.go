// Package library defines the on-drive layout of an Arcana library and
// manages opening, initializing and locking it.
//
// A library is a directory (normally "Arcana" at the root of an external
// drive) identified by the marker file arcana-library.json. Every path Arcana
// stores is relative to that directory, so the drive can change letter or
// mount point, or move between Windows, Linux and macOS, without losing state.
package library

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/KK4ODA/arcana/internal/buildinfo"
	"github.com/KK4ODA/arcana/internal/fsutil"
)

// MarkerFile identifies a directory as an Arcana library.
const MarkerFile = "arcana-library.json"

// FormatVersion is the newest library layout this build understands. A build
// opening a library with a newer format falls back to read-only mode rather
// than risk damaging data it does not understand.
const FormatVersion = 1

// Identity is the content of the marker file. It is written once at creation.
type Identity struct {
	Format        string    `json:"format"`
	FormatVersion int       `json:"formatVersion"`
	LibraryID     string    `json:"libraryId"`
	Label         string    `json:"label"`
	CreatedAt     time.Time `json:"createdAt"`
	CreatedBy     string    `json:"createdBy"`
}

// Layout maps the logical areas of a library onto directories. The
// separation is deliberate: downloaded reference data, staging, rebuildable
// indexes, user data and binaries never share a directory, so an update to one
// can never overwrite another.
type Layout struct {
	Root string
}

func (l Layout) path(parts ...string) string {
	return filepath.Join(append([]string{l.Root}, parts...)...)
}

// Marker is the identity file.
func (l Layout) Marker() string { return l.path(MarkerFile) }

// Content holds verified, published resources: content/<id>/<version>/<file>.
func (l Layout) Content() string { return l.path("content") }

// Staging holds downloads in progress. Same volume as Content so publication
// is an atomic rename.
func (l Layout) Staging() string { return l.path("staging") }

// State holds the metadata database. It is a cache: everything in it can be
// rebuilt from content sidecars and catalog caches.
func (l Layout) State() string { return l.path("state") }

// DB is the SQLite metadata database.
func (l Layout) DB() string { return l.path("state", "arcana.db") }

// LockFile guards against two Arcana processes writing the same library.
func (l Layout) LockFile() string { return l.path("state", "arcana.lock") }

// CatalogCache holds normalized catalog snapshots from each provider, so the
// catalog can be browsed offline.
func (l Layout) CatalogCache() string { return l.path("catalog") }

// Index holds search indexes. Rebuildable; never required for reading.
func (l Layout) Index() string { return l.path("index") }

// User holds settings and anything the user creates. Arcana never deletes or
// replaces files here as part of content installation or updates.
func (l Layout) User() string { return l.path("user") }

// Settings is the user's settings file.
func (l Layout) Settings() string { return l.path("user", "settings.json") }

// Documents holds user-imported documents.
func (l Layout) Documents() string { return l.path("user", "documents") }

// Logs holds diagnostic logs.
func (l Layout) Logs() string { return l.path("logs") }

// Bin holds per-platform executables: bin/<goos>-<goarch>/.
func (l Layout) Bin() string { return l.path("bin") }

func (l Layout) dirs() []string {
	return []string{l.Content(), l.Staging(), l.State(), l.CatalogCache(), l.Index(), l.User(),
		l.Documents(), l.Logs(), l.Bin()}
}

// Library is an opened library.
type Library struct {
	Layout
	Identity Identity
	// ReadOnly is set when the library cannot be modified: read-only media,
	// another Arcana process holds the lock, or the format is newer than this
	// build supports. ReadOnlyReason explains which.
	ReadOnly       bool
	ReadOnlyReason string
	Settings       *SettingsStore
	lock           *fsutil.Lock
}

// IsLibrary reports whether dir contains a library marker.
func IsLibrary(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, MarkerFile))
	return err == nil
}

// Find looks for a library in dir and up to maxUp parent directories. This
// lets the executable live in bin/<platform>/ or at the library root.
func Find(dir string, maxUp int) (string, bool) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", false
	}
	for i := 0; i <= maxUp; i++ {
		if IsLibrary(dir) {
			return dir, true
		}
		// Also accept a drive root containing an "Arcana" library folder.
		if IsLibrary(filepath.Join(dir, "Arcana")) {
			return filepath.Join(dir, "Arcana"), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

// Discover resolves which library to open. Order: explicit path, the
// ARCANA_LIBRARY environment variable, the executable's location, the
// working directory. It returns "" if none is found.
func Discover(explicit string) string {
	if explicit != "" {
		if root, ok := Find(explicit, 0); ok {
			return root
		}
		return ""
	}
	if env := os.Getenv("ARCANA_LIBRARY"); env != "" {
		if root, ok := Find(env, 0); ok {
			return root
		}
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		if root, ok := Find(filepath.Dir(exe), 3); ok {
			return root
		}
	}
	if wd, err := os.Getwd(); err == nil {
		if root, ok := Find(wd, 1); ok {
			return root
		}
	}
	return ""
}

// Init creates a new library in root. root may already exist (for example a
// release archive unpacked onto a drive) but must not already be a library.
func Init(root, label string) (*Identity, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if IsLibrary(root) {
		return nil, fmt.Errorf("%s is already an Arcana library", root)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	if !fsutil.IsWritableDir(root) {
		return nil, fmt.Errorf("%s is not writable", root)
	}
	l := Layout{Root: root}
	for _, d := range l.dirs() {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if label == "" {
		label = "Arcana Library"
	}
	id := Identity{
		Format:        "arcana-library",
		FormatVersion: FormatVersion,
		LibraryID:     newID(),
		Label:         label,
		CreatedAt:     time.Now().UTC().Truncate(time.Second),
		CreatedBy:     "arcana " + buildinfo.Version,
	}
	data, err := json.MarshalIndent(id, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeReadme(l); err != nil {
		return nil, err
	}
	// The marker is written last: a library is only recognized once its
	// directories exist.
	if err := fsutil.WriteFileAtomic(l.Marker(), append(data, '\n'), 0o644); err != nil {
		return nil, err
	}
	return &id, nil
}

func writeReadme(l Layout) error {
	const text = `This folder is an Arcana offline library.

  content/   Downloaded reference resources (managed by Arcana).
  staging/   Downloads in progress. Safe to delete when Arcana is not running.
  state/     Metadata database. Rebuilt automatically if lost.
  catalog/   Cached resource catalogs for offline browsing.
  index/     Search indexes. Rebuilt automatically if lost.
  user/      Your settings, notes and documents. Never modified by updates.
  logs/      Diagnostic logs.
  bin/       Arcana programs for each operating system.

Start Arcana with Arcana.exe (Windows) or arcana.sh (Linux, macOS).
Always eject the drive safely before unplugging it.
`
	return fsutil.WriteFileAtomic(filepath.Join(l.Root, "README.txt"), []byte(text), 0o644)
}

// OpenOptions control how a library is opened.
type OpenOptions struct {
	// ForceReadOnly opens the library without taking the write lock.
	ForceReadOnly bool
}

// Open opens an existing library.
func Open(root string, opts OpenOptions) (*Library, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(root, MarkerFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s is not an Arcana library (no %s)", root, MarkerFile)
		}
		return nil, err
	}
	var id Identity
	if err := json.Unmarshal(data, &id); err != nil {
		return nil, fmt.Errorf("library marker is damaged: %w", err)
	}
	if id.Format != "arcana-library" {
		return nil, fmt.Errorf("unrecognized library format %q", id.Format)
	}
	lib := &Library{Layout: Layout{Root: root}, Identity: id}

	switch {
	case opts.ForceReadOnly:
		lib.setReadOnly("opened read-only by request")
	case id.FormatVersion > FormatVersion:
		lib.setReadOnly(fmt.Sprintf("library format %d is newer than this version of Arcana supports (%d); update Arcana to modify it",
			id.FormatVersion, FormatVersion))
	case !fsutil.IsWritableDir(root):
		lib.setReadOnly("the drive is read-only")
	}

	if !lib.ReadOnly {
		for _, d := range lib.dirs() {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return nil, err
			}
		}
		lk, err := fsutil.TryLock(lib.LockFile())
		switch {
		case errors.Is(err, fsutil.ErrLocked):
			lib.setReadOnly("another Arcana process is using this library")
		case err != nil:
			return nil, fmt.Errorf("lock library: %w", err)
		default:
			lib.lock = lk
		}
	}

	settings, err := LoadSettings(lib.Layout.Settings(), lib.ReadOnly)
	if err != nil {
		lib.Close()
		return nil, err
	}
	lib.Settings = settings
	return lib, nil
}

func (l *Library) setReadOnly(reason string) {
	l.ReadOnly = true
	if l.ReadOnlyReason == "" {
		l.ReadOnlyReason = reason
	}
}

// Close releases the library lock.
func (l *Library) Close() error {
	return l.lock.Release()
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

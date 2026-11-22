// Package content manages published resources on the library drive.
//
// Layout: content/<resource-id>/<version>/<file> plus
// content/<resource-id>/installed.json. The sidecar is the commit point: a
// resource is installed exactly when its sidecar exists and names files that
// are present. Writing the sidecar atomically after the files have been moved
// into place means an interrupted installation leaves either the previous
// version or the new one, never a mixture.
package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/KK4ODA/arcana/internal/buildinfo"
	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/fsutil"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/store"
)

// SidecarName is the per-resource metadata file.
const SidecarName = "installed.json"

// SidecarFormat identifies sidecar files.
const SidecarFormat = "arcana-installed-resource"

// Manager publishes, removes and checks installed resources.
type Manager struct {
	Layout library.Layout
	Store  *store.Store
	Log    *slog.Logger
	// OnChange is called after the set of installed files changes, e.g. to
	// restart the ZIM server. It must not block for long.
	OnChange func()

	mu sync.Mutex
}

// ResourceDir returns content/<id>, validating id.
func (m *Manager) ResourceDir(id string) (string, error) {
	return fsutil.SafeJoin(m.Layout.Content(), id)
}

// FilePath returns the absolute path of an installed file.
func (m *Manager) FilePath(in store.Installed, f store.InstalledFile) (string, error) {
	dir, err := m.ResourceDir(in.ResourceID)
	if err != nil {
		return "", err
	}
	return fsutil.SafeJoin(dir, f.Path)
}

// StagedFile is a verified file waiting to be published.
type StagedFile struct {
	Path   string // absolute path in staging/
	Name   string // final file name
	Size   int64
	SHA256 string
}

// Publish moves verified files into content/ and commits the sidecar. Any
// previous version of the resource is removed only after the new sidecar is
// durable. Publish is idempotent, so it can be re-run after a crash during
// installation.
func (m *Manager) Publish(ctx context.Context, res catalog.Resource, staged []StagedFile, verification string, unverified bool) (store.Installed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := res.Validate(); err != nil {
		return store.Installed{}, err
	}
	resDir, err := m.ResourceDir(res.ID)
	if err != nil {
		return store.Installed{}, err
	}
	verDir, err := fsutil.SafeJoin(resDir, res.Version)
	if err != nil {
		return store.Installed{}, err
	}
	if err := os.MkdirAll(verDir, 0o755); err != nil {
		return store.Installed{}, err
	}
	rec := store.Installed{
		Format:        SidecarFormat,
		FormatVersion: 1,
		ResourceID:    res.ID,
		Version:       res.Version,
		InstalledAt:   time.Now().UTC().Truncate(time.Second),
		InstalledBy:   "arcana " + buildinfo.Version,
		Resource:      res,
		Verification:  verification,
		Unverified:    unverified,
	}
	for _, sf := range staged {
		if err := fsutil.ValidateFileName(sf.Name); err != nil {
			return store.Installed{}, err
		}
		target := filepath.Join(verDir, sf.Name)
		if _, err := os.Stat(sf.Path); err == nil {
			if err := fsutil.MoveFile(sf.Path, target); err != nil {
				return store.Installed{}, err
			}
		} else if st, err2 := os.Stat(target); err2 != nil || st.Size() != sf.Size {
			// Neither the staged file nor a complete published copy exists.
			return store.Installed{}, fmt.Errorf("staged file %s is missing: %w", sf.Name, err)
		}
		rec.Files = append(rec.Files, store.InstalledFile{Path: path.Join(res.Version, sf.Name), Size: sf.Size, SHA256: sf.SHA256})
	}
	if err := m.writeSidecar(resDir, rec); err != nil {
		return store.Installed{}, err
	}
	if err := m.Store.PutInstalled(ctx, rec); err != nil {
		// The sidecar is authoritative; the index is repaired on next start.
		m.log().Warn("index update failed after publish", "resource", res.ID, "err", err)
	}
	if m.OnChange != nil {
		m.OnChange()
	}
	m.removeOtherVersions(resDir, res.Version)
	return rec, nil
}

func (m *Manager) writeSidecar(resDir string, rec store.Installed) error {
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(filepath.Join(resDir, SidecarName), append(data, '\n'), 0o644)
}

// removeOtherVersions deletes version directories other than keep. Failures
// (for example a file still held open on Windows) are left for CollectGarbage.
func (m *Manager) removeOtherVersions(resDir, keep string) {
	entries, err := os.ReadDir(resDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && e.Name() != keep {
			if err := os.RemoveAll(filepath.Join(resDir, e.Name())); err != nil {
				m.log().Info("old version not removed yet", "dir", e.Name(), "err", err)
			}
		}
	}
}

// Remove uninstalls a resource. The sidecar is deleted first, so a crash
// part-way leaves an orphan directory (cleaned up later), never a resource
// that claims to be installed but is incomplete.
func (m *Manager) Remove(ctx context.Context, id string) error {
	m.mu.Lock()
	resDir, err := m.ResourceDir(id)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	if err := os.Remove(filepath.Join(resDir, SidecarName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		m.mu.Unlock()
		return err
	}
	_ = fsutil.SyncDir(resDir)
	_ = m.Store.DeleteInstalled(ctx, id)
	m.mu.Unlock()
	if m.OnChange != nil {
		m.OnChange() // let the ZIM server release the files first
	}
	if err := os.RemoveAll(resDir); err != nil {
		m.log().Info("resource files not removed yet; will retry at next start", "resource", id, "err", err)
	}
	return nil
}

// LoadSidecars reads every sidecar under content/. Sidecars that cannot be
// parsed or that reference unsafe paths are reported, not trusted.
func (m *Manager) LoadSidecars() ([]store.Installed, []Problem) {
	var out []store.Installed
	var problems []Problem
	entries, err := os.ReadDir(m.Layout.Content())
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			problems = append(problems, Problem{Kind: "unreadable", Detail: err.Error()})
		}
		return nil, problems
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		p := filepath.Join(m.Layout.Content(), id, SidecarName)
		data, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			problems = append(problems, Problem{ResourceID: id, Kind: "orphan",
				Detail: "directory has no installed.json (interrupted installation or removal)"})
			continue
		}
		if err != nil {
			problems = append(problems, Problem{ResourceID: id, Kind: "unreadable", Detail: err.Error()})
			continue
		}
		var rec store.Installed
		if err := json.Unmarshal(data, &rec); err != nil || rec.Format != SidecarFormat {
			problems = append(problems, Problem{ResourceID: id, Kind: "damaged-metadata", Detail: "installed.json is not valid"})
			continue
		}
		if rec.ResourceID != id {
			problems = append(problems, Problem{ResourceID: id, Kind: "damaged-metadata", Detail: "installed.json names a different resource"})
			continue
		}
		bad := false
		for _, f := range rec.Files {
			if _, err := fsutil.SafeJoin(filepath.Join(m.Layout.Content(), id), f.Path); err != nil {
				problems = append(problems, Problem{ResourceID: id, Kind: "damaged-metadata", Detail: err.Error()})
				bad = true
			}
		}
		if !bad {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ResourceID < out[j].ResourceID })
	return out, problems
}

// Rebuild replaces the database's installed index with the sidecars.
func (m *Manager) Rebuild(ctx context.Context) ([]Problem, error) {
	recs, problems := m.LoadSidecars()
	return problems, m.Store.ReplaceInstalled(ctx, recs)
}

// Problem is an integrity finding.
type Problem struct {
	ResourceID string `json:"resourceId,omitempty"`
	File       string `json:"file,omitempty"`
	// Kind: missing, size-mismatch, hash-mismatch, orphan, unreadable,
	// damaged-metadata.
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Report is the result of an integrity check.
type Report struct {
	CheckedAt time.Time `json:"checkedAt"`
	Full      bool      `json:"full"`
	Resources int       `json:"resources"`
	Files     int       `json:"files"`
	Bytes     int64     `json:"bytes"`
	Problems  []Problem `json:"problems"`
}

// OK reports whether no problems were found.
func (r Report) OK() bool { return len(r.Problems) == 0 }

// Check verifies installed resources. A quick check confirms that files
// exist with the recorded sizes; a full check re-hashes every byte.
func (m *Manager) Check(ctx context.Context, full bool) (Report, error) {
	rep := Report{CheckedAt: time.Now().UTC(), Full: full}
	recs, problems := m.LoadSidecars()
	rep.Problems = append(rep.Problems, problems...)
	for _, rec := range recs {
		rep.Resources++
		for _, f := range rec.Files {
			if err := ctx.Err(); err != nil {
				return rep, err
			}
			rep.Files++
			p, err := m.FilePath(rec, f)
			if err != nil {
				rep.Problems = append(rep.Problems, Problem{ResourceID: rec.ResourceID, File: f.Path, Kind: "damaged-metadata", Detail: err.Error()})
				continue
			}
			st, err := os.Stat(p)
			if err != nil {
				rep.Problems = append(rep.Problems, Problem{ResourceID: rec.ResourceID, File: f.Path, Kind: "missing", Detail: "file not found"})
				continue
			}
			rep.Bytes += st.Size()
			if st.Size() != f.Size {
				rep.Problems = append(rep.Problems, Problem{ResourceID: rec.ResourceID, File: f.Path, Kind: "size-mismatch",
					Detail: fmt.Sprintf("expected %d bytes, found %d", f.Size, st.Size())})
				continue
			}
			if full && f.SHA256 != "" {
				sum, err := HashFile(ctx, p)
				if err != nil {
					rep.Problems = append(rep.Problems, Problem{ResourceID: rec.ResourceID, File: f.Path, Kind: "unreadable", Detail: err.Error()})
					continue
				}
				if sum != f.SHA256 {
					rep.Problems = append(rep.Problems, Problem{ResourceID: rec.ResourceID, File: f.Path, Kind: "hash-mismatch",
						Detail: "content does not match the recorded SHA-256; the file is corrupted"})
				}
			}
		}
	}
	return rep, nil
}

// HashFile computes a file's SHA-256, honoring cancellation.
func HashFile(ctx context.Context, p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		h.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// CollectGarbage removes superseded version directories left behind when a
// file was locked at replacement time, and staging directories that belong
// to no active download. It never touches user/ and never deletes a resource
// directory that lacks a sidecar (that is reported by Check instead).
func (m *Manager) CollectGarbage(activeJobs map[string]bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	recs, _ := m.LoadSidecars()
	for _, rec := range recs {
		dir, err := m.ResourceDir(rec.ResourceID)
		if err == nil {
			m.removeOtherVersions(dir, rec.Version)
		}
	}
	entries, err := os.ReadDir(m.Layout.Staging())
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() && !activeJobs[e.Name()] {
			_ = os.RemoveAll(filepath.Join(m.Layout.Staging(), e.Name()))
		}
	}
}

// StagedBytes reports bytes currently held in staging/.
func (m *Manager) StagedBytes() int64 {
	n, _ := fsutil.DirSize(m.Layout.Staging())
	return n
}

func (m *Manager) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}
	return slog.Default()
}

// Package transfer copies verified resources between Arcana libraries
// without a network: export a collection to a folder or drive, import a
// collection (or another library's resources), and describe a library with a
// portable manifest.
//
// Every copy is hashed and compared with the SHA-256 recorded at
// installation, so silent corruption on either drive is detected. Imports go
// through the normal publication path (staging, verify, atomic commit) and
// never touch user data.
package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/buildinfo"
	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/content"
	"github.com/KK4ODA/arcana/internal/fsutil"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/storage"
	"github.com/KK4ODA/arcana/internal/store"
)

// ManifestName is the file written at the root of an export.
const ManifestName = "arcana-export.json"

// Manifest describes a set of resources with their files and hashes.
type Manifest struct {
	Format          string    `json:"format"` // "arcana-export" or "arcana-library-manifest"
	FormatVersion   int       `json:"formatVersion"`
	CreatedAt       time.Time `json:"createdAt"`
	CreatedBy       string    `json:"createdBy"`
	SourceLibraryID string    `json:"sourceLibraryId"`
	SourceLabel     string    `json:"sourceLabel"`
	Resources       []Entry   `json:"resources"`
	UserDocuments   []UserDoc `json:"userDocuments,omitempty"`
}

// Entry is one resource in a manifest.
type Entry struct {
	ResourceID string                `json:"resourceId"`
	Version    string                `json:"version"`
	Title      string                `json:"title"`
	License    catalog.License       `json:"license"`
	Files      []store.InstalledFile `json:"files"`
	Unverified bool                  `json:"unverified,omitempty"`
	// Installed is the full sidecar, so the destination keeps provenance.
	Installed store.Installed `json:"installed"`
}

// UserDoc is a user document included in an export.
type UserDoc struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Size sums an entry's files.
func (e Entry) Size() int64 {
	var n int64
	for _, f := range e.Files {
		n += f.Size
	}
	return n
}

// Progress reports bytes copied.
type Progress func(done, total int64, message string)

// LibraryManifest describes every installed resource of a library.
func LibraryManifest(ctx context.Context, lib *library.Library, st *store.Store) (Manifest, error) {
	installed, err := st.ListInstalled(ctx)
	if err != nil {
		return Manifest{}, err
	}
	m := newManifest("arcana-library-manifest", lib)
	for _, in := range installed {
		m.Resources = append(m.Resources, entryFor(in))
	}
	return m, nil
}

func newManifest(format string, lib *library.Library) Manifest {
	return Manifest{Format: format, FormatVersion: 1, CreatedAt: time.Now().UTC().Truncate(time.Second),
		CreatedBy: "arcana " + buildinfo.Version, SourceLibraryID: lib.Identity.LibraryID, SourceLabel: lib.Identity.Label}
}

func entryFor(in store.Installed) Entry {
	return Entry{ResourceID: in.ResourceID, Version: in.Version, Title: in.Resource.Title, License: in.Resource.License,
		Files: in.Files, Unverified: in.Unverified, Installed: in}
}

// ExportResult summarizes an export.
type ExportResult struct {
	Destination string   `json:"destination"`
	Resources   int      `json:"resources"`
	Bytes       int64    `json:"bytes"`
	Skipped     int      `json:"skipped"` // already present and identical at the destination
	Warnings    []string `json:"warnings,omitempty"`
}

// Export copies resources (and optionally user documents) to dest.
func Export(ctx context.Context, lib *library.Library, cm *content.Manager, st *store.Store, ids []string, dest string, withUserDocs bool, progress Progress) (ExportResult, error) {
	res := ExportResult{Destination: dest}
	dest, err := filepath.Abs(dest)
	if err != nil {
		return res, err
	}
	if inside(dest, lib.Root) {
		return res, errors.New("choose a destination outside this library")
	}
	m := newManifest("arcana-export", lib)
	var total int64
	for _, id := range ids {
		in, err := st.GetInstalled(ctx, id)
		if err != nil {
			return res, fmt.Errorf("%s is not installed", id)
		}
		e := entryFor(in)
		m.Resources = append(m.Resources, e)
		total += e.Size()
		if in.Resource.License.Redistribution != catalog.RedistPermitted {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: license %s (%s) — check the terms before giving copies to others",
				in.Resource.Title, in.Resource.License.ID, in.Resource.License.Redistribution))
		}
	}
	var userDocs []string
	if withUserDocs {
		entries, _ := os.ReadDir(lib.Documents())
		for _, e := range entries {
			if e.Type().IsRegular() && fsutil.ValidateFileName(e.Name()) == nil {
				userDocs = append(userDocs, e.Name())
				if info, err := e.Info(); err == nil {
					total += info.Size()
				}
			}
		}
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return res, err
	}
	if vol, err := storage.Stat(dest); err == nil {
		if int64(vol.AvailBytes) < total {
			return res, &storage.InsufficientSpaceError{Needed: total, Free: int64(vol.AvailBytes)}
		}
		for _, e := range m.Resources {
			for _, f := range e.Files {
				if ok, why := storage.CanStore(vol.FSType, f.Size); !ok {
					return res, errors.New(why)
				}
			}
		}
	}

	var done int64
	report := func(msg string) {
		if progress != nil {
			progress(done, total, msg)
		}
	}
	for _, e := range m.Resources {
		srcDir, err := cm.ResourceDir(e.ResourceID)
		if err != nil {
			return res, err
		}
		dstDir := filepath.Join(dest, "content", e.ResourceID)
		allSkipped := true
		for _, f := range e.Files {
			src, err := fsutil.SafeJoin(srcDir, f.Path)
			if err != nil {
				return res, err
			}
			dst, err := fsutil.SafeJoin(dstDir, f.Path)
			if err != nil {
				return res, err
			}
			report("Copying " + e.Title)
			skipped, err := copyVerified(ctx, src, dst, f.SHA256, func(n int64) { done += n; report("") })
			if err != nil {
				return res, fmt.Errorf("%s: %w", e.Title, err)
			}
			if !skipped {
				allSkipped = false
			}
			res.Bytes += f.Size
		}
		data, _ := json.MarshalIndent(e.Installed, "", "  ")
		if err := fsutil.WriteFileAtomic(filepath.Join(dstDir, content.SidecarName), append(data, '\n'), 0o644); err != nil {
			return res, err
		}
		res.Resources++
		if allSkipped {
			res.Skipped++
		}
	}
	for _, name := range userDocs {
		src := filepath.Join(lib.Documents(), name)
		dst := filepath.Join(dest, "user-documents", name)
		sum, err := hashFile(ctx, src)
		if err != nil {
			return res, err
		}
		if _, err := copyVerified(ctx, src, dst, sum, func(n int64) { done += n; report("") }); err != nil {
			return res, err
		}
		st, _ := os.Stat(src)
		m.UserDocuments = append(m.UserDocuments, UserDoc{Name: name, Size: st.Size(), SHA256: sum})
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return res, err
	}
	// The manifest is written last: an export is complete only when it
	// exists.
	return res, fsutil.WriteFileAtomic(filepath.Join(dest, ManifestName), append(data, '\n'), 0o644)
}

// copyVerified copies src to dst, verifying SHA-256 against want. If dst
// already holds identical content, nothing is copied and skipped is true.
func copyVerified(ctx context.Context, src, dst, want string, advance func(int64)) (skipped bool, err error) {
	if st, err := os.Stat(dst); err == nil {
		srcInfo, err2 := os.Stat(src)
		if err2 == nil && st.Size() == srcInfo.Size() {
			if sum, err := hashFile(ctx, dst); err == nil && sum == want {
				advance(st.Size())
				return true, nil
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, err
	}
	in, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer in.Close()
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return false, err
	}
	h := sha256.New()
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			out.Close()
			os.Remove(tmp)
			return false, err
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if _, err := out.Write(buf[:n]); err != nil {
				out.Close()
				os.Remove(tmp)
				return false, err
			}
			h.Write(buf[:n])
			advance(int64(n))
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.Close()
			os.Remove(tmp)
			return false, rerr
		}
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return false, err
	}
	out.Close()
	if got := hex.EncodeToString(h.Sum(nil)); want != "" && got != want {
		os.Remove(tmp)
		return false, fmt.Errorf("%s is damaged: its SHA-256 does not match the value recorded at installation", filepath.Base(src))
	}
	return false, fsutil.MoveFile(tmp, dst)
}

func hashFile(ctx context.Context, p string) (string, error) {
	return content.HashFile(ctx, p)
}

func inside(p, root string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ---------------------------------------------------------------- import

// ReadSource reads an export folder, or any Arcana library, as a manifest.
// Everything in it is validated as untrusted input.
func ReadSource(dir string) (Manifest, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if data, err := os.ReadFile(filepath.Join(dir, ManifestName)); err == nil {
		if err := json.Unmarshal(data, &m); err != nil || m.Format != "arcana-export" {
			return Manifest{}, errors.New("the export manifest is damaged")
		}
	} else {
		// Treat it as a library (or a bare content folder): read sidecars.
		lib := library.Layout{Root: dir}
		if root, ok := library.Find(dir, 0); ok {
			lib.Root = root
		}
		cm := &content.Manager{Layout: lib}
		recs, _ := cm.LoadSidecars()
		if len(recs) == 0 {
			return Manifest{}, errors.New("no Arcana export or library found in that folder")
		}
		m = Manifest{Format: "arcana-export", FormatVersion: 1, SourceLabel: filepath.Base(lib.Root)}
		if data, err := os.ReadFile(lib.Marker()); err == nil {
			var id library.Identity
			if json.Unmarshal(data, &id) == nil {
				m.SourceLibraryID, m.SourceLabel = id.LibraryID, id.Label
			}
		}
		for _, r := range recs {
			m.Resources = append(m.Resources, entryFor(r))
		}
		dir = lib.Root
	}
	var valid []Entry
	for _, e := range m.Resources {
		res := e.Installed.Resource
		if res.ID != e.ResourceID || res.Version != e.Version || res.Validate() != nil {
			continue
		}
		ok := len(e.Files) > 0
		for _, f := range e.Files {
			if _, err := fsutil.SafeJoin(filepath.Join(dir, "content", e.ResourceID), f.Path); err != nil || len(f.SHA256) != 64 {
				ok = false
			}
		}
		if ok {
			e.Title = res.Title
			valid = append(valid, e)
		}
	}
	m.Resources = valid
	sort.Slice(m.Resources, func(i, j int) bool { return m.Resources[i].Title < m.Resources[j].Title })
	return m, nil
}

// Plan states what importing each entry would do.
type Plan struct {
	ResourceID       string `json:"resourceId"`
	Title            string `json:"title"`
	Version          string `json:"version"`
	Size             int64  `json:"size"`
	InstalledVersion string `json:"installedVersion,omitempty"`
	// Action is "install", "update", "identical", "older".
	Action  string          `json:"action"`
	License catalog.License `json:"license"`
}

// PlanImport compares a source manifest with what is installed.
func PlanImport(ctx context.Context, st *store.Store, m Manifest) []Plan {
	var out []Plan
	for _, e := range m.Resources {
		p := Plan{ResourceID: e.ResourceID, Title: e.Title, Version: e.Version, Size: e.Size(), License: e.License, Action: "install"}
		if in, err := st.GetInstalled(ctx, e.ResourceID); err == nil {
			p.InstalledVersion = in.Version
			switch c := catalog.CompareVersions(e.Version, in.Version); {
			case c > 0:
				p.Action = "update"
			case c < 0:
				p.Action = "older"
			default:
				p.Action = "identical"
				if !sameFiles(in.Files, e.Files) {
					p.Action = "update" // same version label, different bytes
				}
			}
		}
		out = append(out, p)
	}
	return out
}

func sameFiles(a, b []store.InstalledFile) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].SHA256 != b[i].SHA256 || a[i].Size != b[i].Size {
			return false
		}
	}
	return true
}

// ImportResult summarizes an import.
type ImportResult struct {
	Installed []string `json:"installed"`
	Skipped   []string `json:"skipped"`
	UserDocs  int      `json:"userDocuments"`
}

// Import copies the selected resources from src into the library. Entries
// that are identical or older than what is installed are skipped.
func Import(ctx context.Context, lib *library.Library, cm *content.Manager, st *store.Store, src string, ids []string, withUserDocs bool, reserve int64, progress Progress) (ImportResult, error) {
	var res ImportResult
	if lib.ReadOnly {
		return res, library.ErrReadOnly
	}
	src, err := filepath.Abs(src)
	if err != nil {
		return res, err
	}
	m, err := ReadSource(src)
	if err != nil {
		return res, err
	}
	srcRoot := src
	if root, ok := library.Find(src, 0); ok {
		srcRoot = root
	}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	plans := map[string]Plan{}
	for _, p := range PlanImport(ctx, st, m) {
		plans[p.ResourceID] = p
	}
	var selected []Entry
	var total int64
	for _, e := range m.Resources {
		if len(ids) > 0 && !want[e.ResourceID] {
			continue
		}
		if a := plans[e.ResourceID].Action; a == "identical" || a == "older" {
			res.Skipped = append(res.Skipped, e.ResourceID)
			continue
		}
		selected = append(selected, e)
		total += e.Size()
	}
	vol, err := storage.Stat(lib.Root)
	if err != nil {
		return res, err
	}
	if err := storage.CheckWrite(vol.AvailBytes, total, 0, reserve); err != nil {
		return res, err
	}
	var done int64
	for _, e := range selected {
		stageDir := filepath.Join(lib.Staging(), "transfer-"+e.ResourceID)
		if err := os.MkdirAll(stageDir, 0o755); err != nil {
			return res, err
		}
		var staged []content.StagedFile
		for _, f := range e.Files {
			from, err := fsutil.SafeJoin(filepath.Join(srcRoot, "content", e.ResourceID), f.Path)
			if err != nil {
				return res, err
			}
			name := path.Base(f.Path)
			to := filepath.Join(stageDir, name+".part")
			if progress != nil {
				progress(done, total, "Copying "+e.Title)
			}
			if _, err := copyVerified(ctx, from, to, f.SHA256, func(n int64) {
				done += n
				if progress != nil {
					progress(done, total, "")
				}
			}); err != nil {
				os.RemoveAll(stageDir)
				return res, fmt.Errorf("%s: %w", e.Title, err)
			}
			staged = append(staged, content.StagedFile{Path: to, Name: name, Size: f.Size, SHA256: f.SHA256})
		}
		verification := fmt.Sprintf("copied from library %q; SHA-256 matched the value recorded there", m.SourceLabel)
		if e.Installed.Verification != "" {
			verification += " (originally: " + e.Installed.Verification + ")"
		}
		if _, err := cm.Publish(ctx, e.Installed.Resource, staged, verification, e.Unverified); err != nil {
			os.RemoveAll(stageDir)
			return res, err
		}
		os.RemoveAll(stageDir)
		res.Installed = append(res.Installed, e.ResourceID)
	}
	if withUserDocs {
		n, err := importUserDocs(ctx, lib, src, m)
		res.UserDocs = n
		if err != nil {
			return res, err
		}
	}
	return res, nil
}

// importUserDocs merges exported user documents. Existing files are never
// overwritten; a differing file with the same name is saved as "name (2).ext".
func importUserDocs(ctx context.Context, lib *library.Library, src string, m Manifest) (int, error) {
	n := 0
	if err := os.MkdirAll(lib.Documents(), 0o755); err != nil {
		return 0, err
	}
	for _, d := range m.UserDocuments {
		if fsutil.ValidateFileName(d.Name) != nil || len(d.SHA256) != 64 {
			continue
		}
		from := filepath.Join(src, "user-documents", d.Name)
		target := filepath.Join(lib.Documents(), d.Name)
		if sum, err := hashFile(ctx, target); err == nil && sum == d.SHA256 {
			continue // already present
		}
		for i := 2; ; i++ {
			if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
				break
			}
			ext := filepath.Ext(d.Name)
			target = filepath.Join(lib.Documents(), fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(d.Name, ext), i, ext))
		}
		if _, err := copyVerified(ctx, from, target, d.SHA256, func(int64) {}); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

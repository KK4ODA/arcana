// Package appupdate installs new versions of Arcana's own programs on a
// library drive. Updates are never automatic and never required.
//
// A release is described by release.json, signed with an Ed25519 key that
// is held offline by the maintainers; the matching public key is compiled
// into Arcana. Files are staged, verified against the signed SHA-256 values,
// and then swapped into place by rename, keeping the previous files for
// rollback. The same update can be applied from a folder copied from another
// drive, with no network.
package appupdate

import (
	"archive/zip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/buildinfo"
	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/fsutil"
	"github.com/KK4ODA/arcana/internal/library"
)

// Names of the release metadata files.
const (
	ManifestFile  = "release.json"
	SignatureFile = "release.json.sig"
)

// Manifest is the signed description of a release.
type Manifest struct {
	Format           string `json:"format"` // "arcana-release"
	FormatVersion    int    `json:"formatVersion"`
	Version          string `json:"version"`
	Released         string `json:"released"`
	Notes            string `json:"notes"`
	MinLibraryFormat int    `json:"minLibraryFormat"`
	MaxLibraryFormat int    `json:"maxLibraryFormat"`
	Files            []File `json:"files"`
	// Archive, when set, is the zip containing the files (for online
	// updates). Paths inside it are prefixed with ArchiveRoot.
	Archive     *File  `json:"archive,omitempty"`
	ArchiveRoot string `json:"archiveRoot,omitempty"`
}

// File is one program file, relative to the library root.
type File struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	Executable bool   `json:"executable,omitempty"`
}

// Errors.
var (
	ErrNoKeys       = errors.New("this build has no update signing key, so updates cannot be verified; download new versions manually")
	ErrBadSignature = errors.New("the release signature is not valid for any trusted key; the update was rejected")
)

// TrustedKeys parses the public keys compiled into this build.
func TrustedKeys() []ed25519.PublicKey {
	var out []ed25519.PublicKey
	for _, s := range strings.Split(buildinfo.UpdateKeys, ",") {
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err == nil && len(b) == ed25519.PublicKeySize {
			out = append(out, ed25519.PublicKey(b))
		}
	}
	return out
}

// Verify checks the signature and parses the manifest. Signature files hold
// the base64 encoding of the Ed25519 signature over the exact manifest
// bytes.
func Verify(manifest, sig []byte, keys []ed25519.PublicKey) (Manifest, error) {
	if len(keys) == 0 {
		return Manifest{}, ErrNoKeys
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return Manifest{}, ErrBadSignature
	}
	ok := false
	for _, k := range keys {
		if ed25519.Verify(k, manifest, raw) {
			ok = true
			break
		}
	}
	if !ok {
		return Manifest{}, ErrBadSignature
	}
	var m Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		return Manifest{}, fmt.Errorf("release manifest: %w", err)
	}
	if m.Format != "arcana-release" || m.FormatVersion != 1 {
		return Manifest{}, errors.New("unsupported release manifest")
	}
	// Files outside the program areas are skipped, not fatal: a later
	// release may add folders this version does not know, and must still
	// be installable. Unsafe paths and library data areas reject the
	// whole manifest.
	kept := m.Files[:0]
	for _, f := range m.Files {
		ok, err := programPath(f.Path)
		if err != nil || len(f.SHA256) != 64 || f.Size < 0 {
			return Manifest{}, fmt.Errorf("release manifest lists an invalid file %q", f.Path)
		}
		if ok {
			kept = append(kept, f)
		}
	}
	m.Files = kept
	return m, nil
}

// programPath reports whether p is a program location an update may write:
// the top-level launchers and readme, and files under bin/, licenses/ and
// appliance/. A path that escapes the library, or points into its data
// (content/, user/, state/, staging/, index/, catalog/, manifest/, logs/),
// is an error. Anything else is ignored.
func programPath(p string) (bool, error) {
	if _, err := fsutil.SafeJoin("/x", p); err != nil || p == "" {
		return false, errors.New("unsafe path")
	}
	switch p {
	case "Arcana.exe", "arcana.sh", "README-FIRST.txt":
		return true, nil
	}
	top, _, _ := strings.Cut(p, "/")
	switch top {
	case "bin", "licenses", "appliance":
		return true, nil
	case "content", "user", "state", "staging", "index", "catalog", "manifest", "logs", library.MarkerFile:
		return false, errors.New("library data path")
	}
	return false, nil
}

// Sign signs manifest bytes (used by tools/release-sign and tests).
func Sign(manifest []byte, key ed25519.PrivateKey) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, manifest)) + "\n")
}

// Newer reports whether the manifest's version is newer than this build.
func (m Manifest) Newer() bool {
	return catalog.CompareVersions(m.Version, strings.TrimPrefix(buildinfo.Version, "v")) > 0
}

// Status is persisted in state/appupdate.json.
type Status struct {
	Staged          *Manifest `json:"staged,omitempty"`
	AppliedVersion  string    `json:"appliedVersion,omitempty"`
	PreviousVersion string    `json:"previousVersion,omitempty"`
	AppliedAt       time.Time `json:"appliedAt,omitempty"`
	AppliedFiles    []string  `json:"appliedFiles,omitempty"`
	RolledBack      bool      `json:"rolledBack,omitempty"`
}

// Updater stages and applies updates for one library.
type Updater struct {
	Lib  *library.Library
	Keys []ed25519.PublicKey
}

func (u *Updater) stageDir(version string) string {
	return filepath.Join(u.Lib.Staging(), "appupdate", version)
}

func (u *Updater) statusPath() string { return filepath.Join(u.Lib.State(), "appupdate.json") }

// LoadStatus reads the persisted status.
func (u *Updater) LoadStatus() Status {
	var s Status
	if data, err := os.ReadFile(u.statusPath()); err == nil {
		_ = json.Unmarshal(data, &s)
	}
	return s
}

func (u *Updater) saveStatus(s Status) error {
	data, _ := json.MarshalIndent(s, "", "  ")
	return fsutil.WriteFileAtomic(u.statusPath(), append(data, '\n'), 0o644)
}

// relevant reports whether a release file belongs on this drive: top-level
// launchers, and files for platforms whose bin/ folder exists. A drive
// trimmed to one platform stays trimmed.
func (u *Updater) relevant(p string) bool {
	if !strings.HasPrefix(p, "bin/") {
		return true
	}
	parts := strings.SplitN(p, "/", 3)
	if len(parts) < 3 {
		return false
	}
	_, err := os.Stat(filepath.Join(u.Lib.Bin(), parts[1]))
	return err == nil
}

func (u *Updater) checkCompatible(m Manifest) error {
	f := u.Lib.Identity.FormatVersion
	if m.MinLibraryFormat > 0 && f < m.MinLibraryFormat {
		return fmt.Errorf("version %s needs library format %d or newer", m.Version, m.MinLibraryFormat)
	}
	if m.MaxLibraryFormat > 0 && f > m.MaxLibraryFormat {
		return fmt.Errorf("version %s cannot open this library (format %d)", m.Version, f)
	}
	return nil
}

// StageFromDir verifies an update folder (an unpacked release, e.g. copied
// from another drive) and copies its files into staging.
func (u *Updater) StageFromDir(ctx context.Context, dir string) (Manifest, error) {
	if u.Lib.ReadOnly {
		return Manifest{}, library.ErrReadOnly
	}
	mdata, err := os.ReadFile(filepath.Join(dir, ManifestFile))
	if err != nil {
		return Manifest{}, fmt.Errorf("no %s in %s", ManifestFile, dir)
	}
	sig, err := os.ReadFile(filepath.Join(dir, SignatureFile))
	if err != nil {
		return Manifest{}, fmt.Errorf("no %s in %s: unsigned updates are not accepted", SignatureFile, dir)
	}
	m, err := Verify(mdata, sig, u.Keys)
	if err != nil {
		return Manifest{}, err
	}
	if err := u.checkCompatible(m); err != nil {
		return m, err
	}
	stage := u.stageDir(m.Version)
	for _, f := range m.Files {
		if !u.relevant(f.Path) {
			continue
		}
		src, err := fsutil.SafeJoin(dir, f.Path)
		if err != nil {
			return m, err
		}
		dst, err := fsutil.SafeJoin(stage, f.Path)
		if err != nil {
			return m, err
		}
		in, err := os.Open(src)
		if err != nil {
			return m, fmt.Errorf("update folder is missing %s", f.Path)
		}
		err = writeVerified(ctx, in, dst, f)
		in.Close()
		if err != nil {
			return m, err
		}
	}
	return m, u.markStaged(m)
}

// StageFromArchive verifies a release zip (downloaded online) against the
// signed manifest and extracts only the listed files.
func (u *Updater) StageFromArchive(ctx context.Context, m Manifest, zipPath string) error {
	if m.Archive == nil {
		return errors.New("release has no archive")
	}
	f, err := os.Open(zipPath)
	if err != nil {
		return err
	}
	sum := sha256.New()
	_, err = io.Copy(sum, f)
	f.Close()
	if err != nil {
		return err
	}
	if hex.EncodeToString(sum.Sum(nil)) != m.Archive.SHA256 {
		return errors.New("downloaded release archive does not match the signed manifest")
	}
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()
	entries := map[string]*zip.File{}
	for _, zf := range zr.File {
		entries[zf.Name] = zf
	}
	stage := u.stageDir(m.Version)
	for _, file := range m.Files {
		if !u.relevant(file.Path) {
			continue
		}
		zf, ok := entries[path.Join(m.ArchiveRoot, file.Path)]
		if !ok {
			return fmt.Errorf("release archive is missing %s", file.Path)
		}
		rc, err := zf.Open()
		if err != nil {
			return err
		}
		dst, err := fsutil.SafeJoin(stage, file.Path)
		if err != nil {
			rc.Close()
			return err
		}
		err = writeVerified(ctx, rc, dst, file)
		rc.Close()
		if err != nil {
			return err
		}
	}
	return u.markStaged(m)
}

func (u *Updater) markStaged(m Manifest) error {
	s := u.LoadStatus()
	s.Staged = &m
	return u.saveStatus(s)
}

func writeVerified(ctx context.Context, r io.Reader, dst string, f File) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(r, f.Size+1))
	if err == nil {
		err = out.Sync()
	}
	out.Close()
	if err != nil {
		os.Remove(tmp)
		return err
	}
	if ctx.Err() != nil {
		os.Remove(tmp)
		return ctx.Err()
	}
	if n != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		os.Remove(tmp)
		return fmt.Errorf("%s does not match the signed release manifest", f.Path)
	}
	mode := os.FileMode(0o644)
	if f.Executable {
		mode = 0o755
	}
	_ = os.Chmod(tmp, mode)
	return os.Rename(tmp, dst)
}

// Apply swaps staged files into place. The current files are kept as
// <name>.previous so Rollback can restore them. Arcana must be restarted to
// run the new version. On Windows, a running program can be renamed but
// not overwritten, which is why every replacement is a pair of renames.
func (u *Updater) Apply() (Status, error) {
	if u.Lib.ReadOnly {
		return Status{}, library.ErrReadOnly
	}
	s := u.LoadStatus()
	if s.Staged == nil {
		return s, errors.New("no update is staged")
	}
	m := *s.Staged
	stage := u.stageDir(m.Version)
	var applied []string
	for _, f := range m.Files {
		src, err := fsutil.SafeJoin(stage, f.Path)
		if err != nil {
			return s, err
		}
		if _, err := os.Stat(src); err != nil {
			continue // not relevant to this drive
		}
		dst, err := fsutil.SafeJoin(u.Lib.Root, f.Path)
		if err != nil {
			return s, err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return s, err
		}
		prev := dst + ".previous"
		_ = os.Remove(prev)
		if _, err := os.Stat(dst); err == nil {
			if err := os.Rename(dst, prev); err != nil {
				u.restore(applied)
				return s, fmt.Errorf("cannot replace %s: %w", f.Path, err)
			}
		}
		if err := os.Rename(src, dst); err != nil {
			_ = os.Rename(prev, dst)
			u.restore(applied)
			return s, fmt.Errorf("cannot install %s: %w", f.Path, err)
		}
		applied = append(applied, f.Path)
	}
	_ = fsutil.SyncDir(u.Lib.Root)
	s.PreviousVersion = buildinfo.Version
	s.AppliedVersion = m.Version
	s.AppliedAt = time.Now().UTC().Truncate(time.Second)
	s.AppliedFiles = applied
	s.Staged = nil
	s.RolledBack = false
	_ = os.RemoveAll(stage)
	return s, u.saveStatus(s)
}

// restore undoes a partially applied update.
func (u *Updater) restore(paths []string) {
	for _, p := range paths {
		dst := filepath.Join(u.Lib.Root, filepath.FromSlash(p))
		if _, err := os.Stat(dst + ".previous"); err == nil {
			_ = os.Rename(dst, dst+".failed")
			_ = os.Rename(dst+".previous", dst)
		}
	}
}

// Rollback restores the files replaced by the last applied update.
func (u *Updater) Rollback() (Status, error) {
	if u.Lib.ReadOnly {
		return Status{}, library.ErrReadOnly
	}
	s := u.LoadStatus()
	if len(s.AppliedFiles) == 0 || s.RolledBack {
		return s, errors.New("there is no applied update to roll back")
	}
	for _, p := range s.AppliedFiles {
		dst, err := fsutil.SafeJoin(u.Lib.Root, p)
		if err != nil {
			return s, err
		}
		prev := dst + ".previous"
		if _, err := os.Stat(prev); err != nil {
			continue // file was new in the update; leave it
		}
		_ = os.Remove(dst + ".rolledback")
		if err := os.Rename(dst, dst+".rolledback"); err != nil {
			return s, err
		}
		if err := os.Rename(prev, dst); err != nil {
			return s, err
		}
	}
	s.RolledBack = true
	return s, u.saveStatus(s)
}

// CurrentPlatformPath is where this platform's program lives in a library.
func CurrentPlatformPath() string {
	if runtime.GOOS == "windows" {
		return "Arcana.exe"
	}
	return "bin/" + buildinfo.Platform() + "/arcana"
}

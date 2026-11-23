// Package fsutil contains file-system helpers that Arcana relies on for
// durability on removable media: atomic replacement of small metadata files,
// directory syncing, and strict validation of names that originate from
// untrusted catalogs.
package fsutil

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"
)

// WriteFileAtomic writes data to path so that readers observe either the old
// content or the complete new content, never a partial file. The data is
// flushed to stable storage before the rename, and the parent directory is
// synced afterwards where the platform supports it.
func WriteFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(tmpName, perm)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return SyncDir(dir)
}

// SyncDir flushes directory metadata (new entries, renames) to disk. Windows
// does not support opening directories for sync, so it is a no-op there; NTFS
// and exFAT on Windows commit directory changes as part of the rename call.
func SyncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		// Some file systems (e.g. certain FUSE or exFAT drivers) reject
		// fsync on directories. That is not fatal: the rename has already
		// happened and the file content was synced.
		if errors.Is(err, os.ErrInvalid) || isNotSupported(err) {
			return nil
		}
		return err
	}
	return nil
}

func isNotSupported(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not supported") || strings.Contains(msg, "invalid argument")
}

// MoveFile renames src to dst. Both must be on the same volume; Arcana keeps
// staging and content on the library volume precisely so that publication of
// a verified download is a single rename.
func MoveFile(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("publish %s: %w", filepath.Base(dst), err)
	}
	return SyncDir(filepath.Dir(dst))
}

// CopyFile copies src to dst through a temporary file and atomically renames
// it into place. Used for cross-volume imports.
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".copy-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return MoveFile(tmpName, dst)
}

// windowsReserved lists device names that cannot be used as a file name
// (with or without an extension) on Windows.
var windowsReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// MaxNameLen is the longest file or directory name Arcana will create. exFAT
// and NTFS allow 255 UTF-16 code units; staying well below leaves room for
// suffixes such as ".part".
const MaxNameLen = 180

// ValidateFileName checks that name is a single, portable path component that
// is safe to create on Windows, macOS and Linux file systems. It rejects
// anything an attacker-controlled catalog could use to escape a directory.
func ValidateFileName(name string) error {
	if name == "" {
		return errors.New("empty file name")
	}
	if !utf8.ValidString(name) {
		return errors.New("file name is not valid UTF-8")
	}
	if len(name) > MaxNameLen {
		return fmt.Errorf("file name longer than %d bytes", MaxNameLen)
	}
	if name == "." || name == ".." {
		return errors.New("file name may not be . or ..")
	}
	if strings.HasPrefix(name, ".") {
		return errors.New("file name may not start with a dot")
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return errors.New("file name may not end with a dot or space")
	}
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f:
			return errors.New("file name contains control characters")
		case strings.ContainsRune(`<>:"/\|?*`, r):
			return fmt.Errorf("file name contains forbidden character %q", r)
		}
	}
	base := strings.ToLower(name)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	if windowsReserved[base] {
		return fmt.Errorf("file name %q is reserved on Windows", name)
	}
	return nil
}

// SanitizeFileName turns a name from another file system into one that
// passes ValidateFileName: characters not allowed on every file system
// become "_", trailing dots and spaces are removed, a Windows device name
// gets a "_" prefix, and a long name is shortened, keeping its extension. It
// returns "" when no valid name can be made, such as for a hidden name
// starting with a dot.
func SanitizeFileName(name string) string {
	name = strings.ToValidUTF8(name, "_")
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"/\|?*`, r) {
			r = '_'
		}
		b.WriteRune(r)
	}
	s := strings.TrimRight(b.String(), ". ")
	if len(s) > MaxNameLen {
		ext := filepath.Ext(s)
		if len(ext) > 16 {
			ext = ""
		}
		cut := MaxNameLen - len(ext)
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = strings.TrimRight(s[:cut], ". ") + ext
	}
	base := strings.ToLower(s)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	if windowsReserved[base] {
		s = "_" + s
	}
	if ValidateFileName(s) != nil {
		return ""
	}
	return s
}

// SafeJoin joins root with a relative, slash-separated path and guarantees the
// result stays inside root. Every component is validated with
// ValidateFileName, so absolute paths, drive letters, ".." and backslashes are
// all rejected.
func SafeJoin(root, rel string) (string, error) {
	if rel == "" {
		return "", errors.New("empty relative path")
	}
	if strings.HasPrefix(rel, "/") {
		return "", errors.New("relative path may not be absolute")
	}
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if err := ValidateFileName(p); err != nil {
			return "", fmt.Errorf("unsafe path %q: %w", rel, err)
		}
	}
	joined := filepath.Join(append([]string{root}, parts...)...)
	cleanRoot := filepath.Clean(root)
	relCheck, err := filepath.Rel(cleanRoot, joined)
	if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes %s", rel, root)
	}
	return joined, nil
}

// ToSlashRel returns path relative to root using forward slashes, which is the
// form Arcana stores in portable metadata.
func ToSlashRel(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s is outside %s", path, root)
	}
	return filepath.ToSlash(rel), nil
}

// DirSize returns the total size of regular files below dir.
func DirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.Type().IsRegular() {
			info, err := d.Info()
			if err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total, err
}

// IsWritableDir reports whether a file can be created in dir. It is used to
// detect read-only media (write-protect switches, read-only mounts, NTFS on
// macOS) before Arcana attempts any write.
func IsWritableDir(dir string) bool {
	f, err := os.CreateTemp(dir, ".arcana-write-probe-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	_ = os.Remove(name)
	return true
}

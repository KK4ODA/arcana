package appupdate

import (
	"archive/zip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/KK4ODA/arcana/internal/library"
)

func setup(t *testing.T) (*Updater, ed25519.PrivateKey, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Arcana")
	if _, err := library.Init(root, ""); err != nil {
		t.Fatal(err)
	}
	lib, err := library.Open(root, library.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lib.Close() })
	os.MkdirAll(filepath.Join(root, "bin", "linux-amd64"), 0o755)
	os.WriteFile(filepath.Join(root, "bin", "linux-amd64", "arcana"), []byte("old linux"), 0o755)
	os.WriteFile(filepath.Join(root, "Arcana.exe"), []byte("old windows"), 0o755)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return &Updater{Lib: lib, Keys: []ed25519.PublicKey{pub}}, priv, root
}

// release writes an update folder with the given files and signs it.
func release(t *testing.T, priv ed25519.PrivateKey, files map[string]string) (string, []byte) {
	dir := t.TempDir()
	m := Manifest{Format: "arcana-release", FormatVersion: 1, Version: "99.0.0", MinLibraryFormat: 1}
	for p, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte(body), 0o644)
		s := sha256.Sum256([]byte(body))
		m.Files = append(m.Files, File{Path: p, Size: int64(len(body)), SHA256: hex.EncodeToString(s[:]), Executable: true})
	}
	data, _ := json.Marshal(m)
	os.WriteFile(filepath.Join(dir, ManifestFile), data, 0o644)
	os.WriteFile(filepath.Join(dir, SignatureFile), Sign(data, priv), 0o644)
	return dir, data
}

func read(t *testing.T, p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

func TestStageApplyRollback(t *testing.T) {
	u, priv, root := setup(t)
	dir, _ := release(t, priv, map[string]string{
		"Arcana.exe":              "new windows",
		"bin/linux-amd64/arcana":  "new linux",
		"bin/darwin-arm64/arcana": "new mac", // platform not on this drive
	})
	m, err := u.StageFromDir(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Version != "99.0.0" || !m.Newer() {
		t.Fatalf("manifest %+v", m)
	}
	if read(t, filepath.Join(root, "Arcana.exe")) != "old windows" {
		t.Fatal("staging must not touch installed files")
	}
	s, err := u.Apply()
	if err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(root, "Arcana.exe")) != "new windows" || read(t, filepath.Join(root, "bin", "linux-amd64", "arcana")) != "new linux" {
		t.Fatal("update not applied")
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "darwin-arm64")); err == nil {
		t.Fatal("update added a platform the drive did not have")
	}
	if len(s.AppliedFiles) != 2 {
		t.Fatalf("applied %v", s.AppliedFiles)
	}
	if _, err := u.Rollback(); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(root, "Arcana.exe")) != "old windows" {
		t.Fatal("rollback did not restore the previous version")
	}
}

func TestRejectsTamperingAndUnsignedUpdates(t *testing.T) {
	u, priv, root := setup(t)
	dir, data := release(t, priv, map[string]string{"bin/linux-amd64/arcana": "new linux"})

	// Tampered file.
	os.WriteFile(filepath.Join(dir, "bin", "linux-amd64", "arcana"), []byte("evil linux"), 0o644)
	if _, err := u.StageFromDir(context.Background(), dir); err == nil {
		t.Fatal("tampered file accepted")
	}
	// Manifest changed after signing.
	os.WriteFile(filepath.Join(dir, ManifestFile), append(data, ' '), 0o644)
	if _, err := u.StageFromDir(context.Background(), dir); err != ErrBadSignature {
		t.Fatalf("modified manifest: %v", err)
	}
	// Signed by another key.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	os.WriteFile(filepath.Join(dir, ManifestFile), data, 0o644)
	os.WriteFile(filepath.Join(dir, SignatureFile), Sign(data, other), 0o644)
	if _, err := u.StageFromDir(context.Background(), dir); err != ErrBadSignature {
		t.Fatalf("foreign key: %v", err)
	}
	// Missing signature.
	os.Remove(filepath.Join(dir, SignatureFile))
	if _, err := u.StageFromDir(context.Background(), dir); err == nil {
		t.Fatal("unsigned update accepted")
	}
	// No trusted keys in this build.
	u.Keys = nil
	os.WriteFile(filepath.Join(dir, SignatureFile), Sign(data, priv), 0o644)
	if _, err := u.StageFromDir(context.Background(), dir); err != ErrNoKeys {
		t.Fatalf("no keys: %v", err)
	}
	if read(t, filepath.Join(root, "bin", "linux-amd64", "arcana")) != "old linux" {
		t.Fatal("rejected update changed files")
	}
}

func TestManifestCannotTargetContentOrUserData(t *testing.T) {
	_, priv, _ := setup(t)
	for _, p := range []string{"user/settings.json", "content/x/installed.json", "../outside", "bin/../user/x", "arcana-library.json"} {
		m := Manifest{Format: "arcana-release", FormatVersion: 1, Version: "9", Files: []File{{Path: p, Size: 1, SHA256: hex.EncodeToString(make([]byte, 32))}}}
		data, _ := json.Marshal(m)
		pub := priv.Public().(ed25519.PublicKey)
		if _, err := Verify(data, Sign(data, priv), []ed25519.PublicKey{pub}); err == nil {
			t.Errorf("manifest path %q accepted", p)
		}
	}
}

func TestStageFromArchive(t *testing.T) {
	u, priv, root := setup(t)
	zipPath := filepath.Join(t.TempDir(), "release.zip")
	zf, _ := os.Create(zipPath)
	zw := zip.NewWriter(zf)
	w, _ := zw.Create("Arcana/bin/linux-amd64/arcana")
	w.Write([]byte("zipped linux"))
	zw.Close()
	zf.Close()
	zdata, _ := os.ReadFile(zipPath)
	zs := sha256.Sum256(zdata)
	fs := sha256.Sum256([]byte("zipped linux"))
	m := Manifest{Format: "arcana-release", FormatVersion: 1, Version: "99.1.0", ArchiveRoot: "Arcana",
		Archive: &File{Path: "bin/arcana.zip", Size: int64(len(zdata)), SHA256: hex.EncodeToString(zs[:])},
		Files:   []File{{Path: "bin/linux-amd64/arcana", Size: 12, SHA256: hex.EncodeToString(fs[:])}}}
	data, _ := json.Marshal(m)
	vm, err := Verify(data, Sign(data, priv), u.Keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := u.StageFromArchive(context.Background(), vm, zipPath); err != nil {
		t.Fatal(err)
	}
	if _, err := u.Apply(); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(root, "bin", "linux-amd64", "arcana")) != "zipped linux" {
		t.Fatal("archive update not applied")
	}
}

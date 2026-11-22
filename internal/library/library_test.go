package library

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitOpenAndRelocate(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "driveA", "Arcana")
	id, err := Init(root, "Test")
	if err != nil {
		t.Fatal(err)
	}
	lib, err := Open(root, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if lib.ReadOnly {
		t.Fatalf("unexpected read-only: %s", lib.ReadOnlyReason)
	}
	if _, err := lib.Settings.Update(func(s *Settings) error { s.ReserveBytes = 123; return nil }); err != nil {
		t.Fatal(err)
	}
	lib.Close()

	// Simulate the drive appearing under a different mount point / letter.
	moved := filepath.Join(base, "driveB", "Arcana")
	os.MkdirAll(filepath.Dir(moved), 0o755)
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	lib2, err := Open(moved, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer lib2.Close()
	if lib2.Identity.LibraryID != id.LibraryID {
		t.Fatal("library identity changed after move")
	}
	if lib2.Settings.Get().ReserveBytes != 123 {
		t.Fatal("settings lost after move")
	}
}

func TestSecondProcessIsReadOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Arcana")
	if _, err := Init(root, ""); err != nil {
		t.Fatal(err)
	}
	a, err := Open(root, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := Open(root, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if !b.ReadOnly || !strings.Contains(b.ReadOnlyReason, "another Arcana") {
		t.Fatalf("second open should be read-only, got %v %q", b.ReadOnly, b.ReadOnlyReason)
	}
	if _, err := b.Settings.Update(func(*Settings) error { return nil }); err != ErrReadOnly {
		t.Fatalf("expected ErrReadOnly, got %v", err)
	}
}

func TestNewerFormatOpensReadOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Arcana")
	if _, err := Init(root, ""); err != nil {
		t.Fatal(err)
	}
	var id Identity
	data, _ := os.ReadFile(filepath.Join(root, MarkerFile))
	json.Unmarshal(data, &id)
	id.FormatVersion = FormatVersion + 1
	data, _ = json.Marshal(id)
	os.WriteFile(filepath.Join(root, MarkerFile), data, 0o644)
	lib, err := Open(root, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	if !lib.ReadOnly {
		t.Fatal("expected read-only for newer format")
	}
}

func TestSettingsPreserveUnknownKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{"futureFeature":{"x":1},"maxConcurrentDownloads":3}`), 0o644)
	s, err := LoadSettings(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.Get().MaxConcurrentDownloads != 3 {
		t.Fatal("did not load known key")
	}
	if _, err := s.Update(func(v *Settings) error { v.BandwidthLimit = 1000; return nil }); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "futureFeature") {
		t.Fatalf("unknown key dropped: %s", data)
	}
}

func TestDamagedSettingsFallBackToDefaults(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	os.WriteFile(p, []byte(`{"maxConcurrentDownloads": 3,`), 0o644) // truncated write
	s, err := LoadSettings(p, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.Get().MaxConcurrentDownloads != 2 {
		t.Fatal("expected defaults")
	}
	if _, err := os.Stat(p + ".damaged"); err != nil {
		t.Fatal("damaged file should be kept for diagnosis")
	}
}

func TestFindFromBinDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Arcana")
	if _, err := Init(root, ""); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "bin", "linux-amd64")
	os.MkdirAll(bin, 0o755)
	got, ok := Find(bin, 3)
	if !ok || got != root {
		t.Fatalf("Find = %q %v", got, ok)
	}
	// Drive root containing Arcana/.
	got, ok = Find(filepath.Dir(root), 0)
	if !ok || got != root {
		t.Fatalf("Find from drive root = %q %v", got, ok)
	}
}

package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateFileName(t *testing.T) {
	good := []string{"wikipedia_en_all_maxi_2026-10.zim", "a", "Report (2024).pdf", "données.epub"}
	for _, n := range good {
		if err := ValidateFileName(n); err != nil {
			t.Errorf("ValidateFileName(%q) = %v, want nil", n, err)
		}
	}
	bad := []string{"", ".", "..", "../x", "a/b", `a\b`, "C:evil", "con", "CON.txt", "lpt1.zim",
		"trailing.", "trailing ", ".hidden", "nul\x00", "tab\tname", string(make([]byte, 300))}
	for _, n := range bad {
		if err := ValidateFileName(n); err == nil {
			t.Errorf("ValidateFileName(%q) = nil, want error", n)
		}
	}
}

func TestSafeJoin(t *testing.T) {
	root := t.TempDir()
	p, err := SafeJoin(root, "content/x/file.zim")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "content", "x", "file.zim"); p != want {
		t.Fatalf("got %s want %s", p, want)
	}
	for _, rel := range []string{"../etc/passwd", "/etc/passwd", "a/../../b", `..\..\x`, "a//b", "C:/x"} {
		if _, err := SafeJoin(root, rel); err == nil {
			t.Errorf("SafeJoin(%q) succeeded, want error", rel)
		}
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")
	if err := WriteFileAtomic(p, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(p, []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "two" {
		t.Fatalf("got %q", b)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
}

func TestLockExclusive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lock")
	l1, err := TryLock(p)
	if err != nil {
		t.Fatal(err)
	}
	// flock locks are per open file description, so a second open in the
	// same process must also fail.
	if _, err := TryLock(p); err != ErrLocked {
		t.Fatalf("second lock: got %v, want ErrLocked", err)
	}
	if err := l1.Release(); err != nil {
		t.Fatal(err)
	}
	l2, err := TryLock(p)
	if err != nil {
		t.Fatalf("relock after release: %v", err)
	}
	l2.Release()
}

package library

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDocumentFilesAndPaths(t *testing.T) {
	l := Layout{Root: t.TempDir()}
	docs := l.Documents()
	write := func(rel, body string) {
		p := filepath.Join(docs, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if files, err := l.DocumentFiles(); err != nil || len(files) != 0 {
		t.Fatalf("missing folder: %v %v", files, err)
	}
	write("plan.md", "a")
	write("Radios/Yaesu/FT-991A.pdf", "bb")
	write("Radios/icom.pdf", "ccc")
	write(".deleted/20260101-000000-old.pdf", "x")
	write("Radios/.upload-123", "x")
	write(".hidden/inside.pdf", "x")
	deep := strings.Repeat("d/", MaxDocumentDepth) + "deep.pdf"
	write(deep, "x")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	os.WriteFile(outside, []byte("secret"), 0o644)
	links := runtime.GOOS != "windows"
	if links {
		os.Symlink(outside, filepath.Join(docs, "link.txt"))
		os.Symlink(filepath.Dir(outside), filepath.Join(docs, "Linked"))
	}

	files, err := l.DocumentFiles()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.Path)
	}
	if strings.Join(got, ",") != "plan.md,Radios/icom.pdf,Radios/Yaesu/FT-991A.pdf" {
		t.Errorf("listed %v", got)
	}
	if files[2].Size != 2 {
		t.Errorf("size %d", files[2].Size)
	}

	if p, err := l.DocumentPath("Radios/Yaesu/FT-991A.pdf"); err != nil || p != filepath.Join(docs, "Radios", "Yaesu", "FT-991A.pdf") {
		t.Errorf("nested path: %q %v", p, err)
	}
	bad := []string{"", "/etc/passwd", "../secret.txt", "Radios/../plan.md", "Radios", "Radios/missing.pdf", `Radios\icom.pdf`, ".deleted/20260101-000000-old.pdf"}
	if links {
		bad = append(bad, "link.txt", "Linked/secret.txt")
	}
	for _, rel := range bad {
		if p, err := l.DocumentPath(rel); err == nil {
			t.Errorf("%q resolved to %q", rel, p)
		}
	}
	if _, err := l.DocumentPath("Radios/missing.pdf"); !errors.Is(err, ErrNoDocument) {
		t.Errorf("missing: %v", err)
	}
}

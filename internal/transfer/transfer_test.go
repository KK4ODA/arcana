package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/content"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/store"
)

type lib struct {
	l  *library.Library
	st *store.Store
	cm *content.Manager
}

func newLib(t *testing.T, name string) lib {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if _, err := library.Init(root, name); err != nil {
		t.Fatal(err)
	}
	l, err := library.Open(root, library.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(l.DB())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(); l.Close() })
	return lib{l, st, &content.Manager{Layout: l.Layout, Store: st}}
}

func (x lib) install(t *testing.T, id, version string, data []byte, lic string) {
	t.Helper()
	p := filepath.Join(x.l.Staging(), id+".part")
	os.WriteFile(p, data, 0o644)
	s := sha256.Sum256(data)
	res := catalog.Resource{ID: id, Provider: "test", Title: "Title " + id, Version: version, Format: "pdf",
		License: catalog.License{ID: lic, Redistribution: map[string]string{"CC0-1.0": "permitted", "CC-BY-NC-SA-3.0": "noncommercial"}[lic], Verified: true},
		Files:   []catalog.File{{Name: "doc.pdf", Size: int64(len(data)), Mirrors: []catalog.Mirror{{URL: "https://x/doc.pdf"}}}}}
	if _, err := x.cm.Publish(context.Background(), res, []content.StagedFile{{Path: p, Name: "doc.pdf", Size: int64(len(data)), SHA256: hex.EncodeToString(s[:])}}, "test", false); err != nil {
		t.Fatal(err)
	}
}

func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	a := newLib(t, "A")
	b := newLib(t, "B")
	a.install(t, "test.one", "1", []byte("first resource"), "CC0-1.0")
	a.install(t, "test.two", "2", []byte("second resource"), "CC-BY-NC-SA-3.0")
	os.WriteFile(filepath.Join(a.l.Documents(), "plan.txt"), []byte("meeting point: north gate"), 0o644)
	os.WriteFile(filepath.Join(b.l.Documents(), "plan.txt"), []byte("a different plan"), 0o644)
	// Documents in folders keep their folders, and a clash is resolved in
	// the same folder.
	for _, l := range []*library.Library{a.l, b.l} {
		os.MkdirAll(filepath.Join(l.Documents(), "Radios", "Yaesu"), 0o755)
	}
	os.WriteFile(filepath.Join(a.l.Documents(), "Radios", "Yaesu", "manual.pdf"), []byte("FT-991A"), 0o644)
	os.WriteFile(filepath.Join(a.l.Documents(), "Radios", "band plan.txt"), []byte("2 m: 144-148"), 0o644)
	os.WriteFile(filepath.Join(b.l.Documents(), "Radios", "Yaesu", "manual.pdf"), []byte("FT-710"), 0o644)

	exportDir := filepath.Join(t.TempDir(), "usb", "ArcanaExport")
	res, err := Export(ctx, a.l, a.cm, a.st, []string{"test.one", "test.two"}, exportDir, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Resources != 2 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "noncommercial") {
		t.Fatalf("export: %+v", res)
	}
	// Re-exporting to the same place copies nothing new.
	res2, _ := Export(ctx, a.l, a.cm, a.st, []string{"test.one", "test.two"}, exportDir, true, nil)
	if res2.Skipped != 2 {
		t.Fatalf("re-export should skip identical files: %+v", res2)
	}

	m, err := ReadSource(exportDir)
	if err != nil || len(m.Resources) != 2 {
		t.Fatalf("read source: %+v %v", m, err)
	}
	ir, err := Import(ctx, b.l, b.cm, b.st, exportDir, nil, true, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ir.Installed) != 2 || ir.UserDocs != 3 {
		t.Fatalf("import: %+v", ir)
	}
	in, err := b.st.GetInstalled(ctx, "test.one")
	if err != nil || !strings.Contains(in.Verification, `library "A"`) {
		t.Fatalf("provenance: %+v %v", in, err)
	}
	// The user's own document was not overwritten.
	own, _ := os.ReadFile(filepath.Join(b.l.Documents(), "plan.txt"))
	merged, _ := os.ReadFile(filepath.Join(b.l.Documents(), "plan (2).txt"))
	if string(own) != "a different plan" || string(merged) != "meeting point: north gate" {
		t.Fatalf("user documents: own=%q merged=%q", own, merged)
	}
	for rel, want := range map[string]string{
		"Radios/Yaesu/manual.pdf": "FT-710", "Radios/Yaesu/manual (2).pdf": "FT-991A", "Radios/band plan.txt": "2 m: 144-148",
	} {
		if got, _ := os.ReadFile(filepath.Join(b.l.Documents(), filepath.FromSlash(rel))); string(got) != want {
			t.Errorf("%s: %q, want %q", rel, got, want)
		}
	}
	// Importing again skips identical resources.
	ir2, _ := Import(ctx, b.l, b.cm, b.st, exportDir, nil, false, 0, nil)
	if len(ir2.Installed) != 0 || len(ir2.Skipped) != 2 {
		t.Fatalf("second import: %+v", ir2)
	}
}

func TestImportDirectlyFromLibraryAndDetectCorruption(t *testing.T) {
	ctx := context.Background()
	a := newLib(t, "A")
	b := newLib(t, "B")
	a.install(t, "test.one", "1", []byte("original bytes"), "CC0-1.0")
	plans := PlanImport(ctx, b.st, mustRead(t, a.l.Root))
	if len(plans) != 1 || plans[0].Action != "install" {
		t.Fatalf("plan: %+v", plans)
	}
	// Corrupt the source file (same size).
	in, _ := a.st.GetInstalled(ctx, "test.one")
	p, _ := a.cm.FilePath(in, in.Files[0])
	os.WriteFile(p, []byte("ORIGINAL BYTES"), 0o644)
	if _, err := Import(ctx, b.l, b.cm, b.st, a.l.Root, nil, false, 0, nil); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("expected corruption to be detected, got %v", err)
	}
	if _, err := b.st.GetInstalled(ctx, "test.one"); err == nil {
		t.Fatal("corrupted copy was installed")
	}
}

func TestHostileExportRejected(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ManifestName), []byte(`{"format":"arcana-export","formatVersion":1,"resources":[
		{"resourceId":"test.x","version":"1","files":[{"path":"../../../etc/passwd","size":1,"sha256":"`+strings.Repeat("a", 64)+`"}],
		 "installed":{"resource":{"id":"test.x","provider":"test","title":"x","version":"1","format":"pdf","files":[{"name":"x.pdf","mirrors":[{"url":"https://x"}]}]}}}]}`), 0o644)
	m, err := ReadSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Resources) != 0 {
		t.Fatalf("traversal entry accepted: %+v", m.Resources)
	}
}

func mustRead(t *testing.T, dir string) Manifest {
	m, err := ReadSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

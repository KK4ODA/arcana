package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KK4ODA/arcana/internal/library"
)

func openTestApp(t *testing.T) *App {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Arcana")
	if _, err := library.Init(root, ""); err != nil {
		t.Fatal(err)
	}
	a, err := Open(root, Options{DisableKiwix: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}

const sampleZIM = "../../testdata/sample/arcana-sample_en_2026-11.zim"

func TestImportVerifiesAgainstCatalog(t *testing.T) {
	a := openTestApp(t)
	ctx := context.Background()
	res, err := a.ImportFile(ctx, sampleZIM, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.ID != "arcana.sample" {
		t.Fatalf("matched %s", res.ID)
	}
	in, err := a.Store.GetInstalled(ctx, "arcana.sample")
	if err != nil || in.Unverified || !strings.Contains(in.Verification, "imported") {
		t.Fatalf("installed: %+v %v", in, err)
	}
	if _, err := os.Stat(sampleZIM); err != nil {
		t.Fatal("import must copy, not move, the source")
	}
}

func TestImportRejectsDamagedCopy(t *testing.T) {
	a := openTestApp(t)
	data, _ := os.ReadFile(sampleZIM)
	data[len(data)/2] ^= 0xFF
	p := filepath.Join(t.TempDir(), "arcana-sample_en_2026-11.zim")
	os.WriteFile(p, data, 0o644)
	if _, err := a.ImportFile(context.Background(), p, ImportOptions{}); err == nil || !strings.Contains(err.Error(), "verification") {
		t.Fatalf("expected verification failure, got %v", err)
	}
	if _, err := a.Store.GetInstalled(context.Background(), "arcana.sample"); err == nil {
		t.Fatal("damaged copy was installed")
	}
}

func TestImportUnknownFile(t *testing.T) {
	a := openTestApp(t)
	p := filepath.Join(t.TempDir(), "mystery.zim")
	os.WriteFile(p, []byte("x"), 0o644)
	if _, err := a.ImportFile(context.Background(), p, ImportOptions{}); err == nil {
		t.Fatal("unknown file should not import")
	}
}

func TestBundledCatalogAvailableOffline(t *testing.T) {
	a := openTestApp(t)
	r, err := a.Store.GetResource(context.Background(), "arcana.us-cfr47-part97")
	if err != nil || r.Format != "pdf" {
		t.Fatalf("bundled catalog not indexed at startup: %v", err)
	}
}

func TestReadOnlyLibraryRebuildsIndexInMemory(t *testing.T) {
	a := openTestApp(t)
	if _, err := a.ImportFile(context.Background(), sampleZIM, ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	root := a.Lib.Root
	a.Close()
	ro, err := Open(root, Options{DisableKiwix: true, ForceReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	list, _ := ro.Store.ListInstalled(context.Background())
	if len(list) != 1 || !ro.Lib.ReadOnly {
		t.Fatalf("read-only open: %d installed, readOnly=%v", len(list), ro.Lib.ReadOnly)
	}
	if _, err := ro.ImportFile(context.Background(), sampleZIM, ImportOptions{}); err == nil {
		t.Fatal("read-only library accepted a change")
	}
}

package content

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/store"
)

func setup(t *testing.T) (*Manager, library.Layout) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Arcana")
	if _, err := library.Init(root, ""); err != nil {
		t.Fatal(err)
	}
	l := library.Layout{Root: root}
	st, err := store.Open(l.DB())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Manager{Layout: l, Store: st}, l
}

func publish(t *testing.T, m *Manager, l library.Layout, id, version string, data []byte) store.Installed {
	t.Helper()
	sp := filepath.Join(l.Staging(), "job", "f.zim.part")
	os.MkdirAll(filepath.Dir(sp), 0o755)
	os.WriteFile(sp, data, 0o644)
	s := sha256.Sum256(data)
	res := catalog.Resource{ID: id, Provider: "test", Title: "T", Version: version, Format: "zim",
		Files: []catalog.File{{Name: "f.zim", Size: int64(len(data)), Mirrors: []catalog.Mirror{{URL: "https://x/f"}}}}}
	rec, err := m.Publish(context.Background(), res, []StagedFile{{Path: sp, Name: "f.zim", Size: int64(len(data)), SHA256: hex.EncodeToString(s[:])}}, "test", false)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestCheckDetectsProblems(t *testing.T) {
	m, l := setup(t)
	ctx := context.Background()
	publish(t, m, l, "test.ok", "1", []byte("hello world"))
	corrupt := publish(t, m, l, "test.corrupt", "1", []byte("original data"))
	missing := publish(t, m, l, "test.missing", "1", []byte("will vanish"))

	p, _ := m.FilePath(corrupt, corrupt.Files[0])
	os.WriteFile(p, []byte("ORIGINAL DATA"), 0o644) // same size, different bytes
	p2, _ := m.FilePath(missing, missing.Files[0])
	os.Remove(p2)
	os.MkdirAll(filepath.Join(l.Content(), "test.orphan", "1"), 0o755)

	quick, err := m.Check(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, pr := range quick.Problems {
		kinds[pr.ResourceID] = pr.Kind
	}
	if kinds["test.missing"] != "missing" || kinds["test.orphan"] != "orphan" || kinds["test.corrupt"] != "" {
		t.Fatalf("quick check: %+v", quick.Problems)
	}
	full, _ := m.Check(ctx, true)
	found := false
	for _, pr := range full.Problems {
		if pr.ResourceID == "test.corrupt" && pr.Kind == "hash-mismatch" {
			found = true
		}
	}
	if !found {
		t.Fatalf("full check must detect same-size corruption: %+v", full.Problems)
	}
}

func TestRebuildFromSidecars(t *testing.T) {
	m, l := setup(t)
	ctx := context.Background()
	publish(t, m, l, "test.a", "1", []byte("aaa"))
	publish(t, m, l, "test.b", "2", []byte("bbb"))
	// Lose the database entirely.
	mem, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	m.Store = mem
	if _, err := m.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	all, _ := mem.ListInstalled(ctx)
	if len(all) != 2 {
		t.Fatalf("rebuilt %d resources", len(all))
	}
}

func TestHostileSidecarRejected(t *testing.T) {
	m, l := setup(t)
	dir := filepath.Join(l.Content(), "test.evil")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, SidecarName), []byte(`{"format":"arcana-installed-resource","resourceId":"test.evil",
		"files":[{"path":"../../user/settings.json","size":1}]}`), 0o644)
	recs, problems := m.LoadSidecars()
	if len(recs) != 0 || len(problems) != 1 || problems[0].Kind != "damaged-metadata" {
		t.Fatalf("recs %v problems %v", recs, problems)
	}
}

func TestRemoveAndGarbageCollection(t *testing.T) {
	m, l := setup(t)
	ctx := context.Background()
	publish(t, m, l, "test.a", "1", []byte("aaa"))
	// A superseded version left behind (e.g. locked on Windows at update).
	stale := filepath.Join(l.Content(), "test.a", "0")
	os.MkdirAll(stale, 0o755)
	os.MkdirAll(filepath.Join(l.Staging(), "deadjob"), 0o755)
	os.MkdirAll(filepath.Join(l.Staging(), "livejob"), 0o755)
	m.CollectGarbage(map[string]bool{"livejob": true})
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("stale version not collected")
	}
	if _, err := os.Stat(filepath.Join(l.Staging(), "deadjob")); err == nil {
		t.Fatal("dead staging not collected")
	}
	if _, err := os.Stat(filepath.Join(l.Staging(), "livejob")); err != nil {
		t.Fatal("active staging deleted")
	}
	if err := m.Remove(ctx, "test.a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(l.Content(), "test.a")); err == nil {
		t.Fatal("resource directory not removed")
	}
	if _, err := m.Store.GetInstalled(ctx, "test.a"); err == nil {
		t.Fatal("index still lists removed resource")
	}
}

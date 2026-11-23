package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// radioFolder makes a folder like a real collection of radio manuals:
// nested folders, a manual saved twice, a file already in the library,
// names other file systems reject, files of other types, system clutter,
// an empty file and a link.
func radioFolder(t *testing.T, a *App) string {
	t.Helper()
	src := filepath.Join(t.TempDir(), "Manuals")
	files := map[string]string{
		"Yaesu/FT-991A.pdf":         "%PDF FT-991A operating manual",
		"Yaesu/copy of FT-991A.pdf": "%PDF FT-991A operating manual",
		"Icom/IC-7300.pdf":          "%PDF IC-7300 basic manual",
		"NIFOG.pdf":                 "%PDF NIFOG 2.02",
		"notes: tuner.txt":          "Tune at 10 W into the dummy load first.",
		"frequencies.csv":           "name,frequency\nrepeater offset,600 kHz\n",
		"schematic.docx":            "word",
		"Icom/photo.jpg":            "jpeg",
		".DS_Store":                 "x",
		"__MACOSX/._NIFOG.pdf":      "x",
		"empty.pdf":                 "",
	}
	for rel, body := range files {
		p := filepath.Join(src, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Date(2019, 5, 1, 12, 0, 0, 0, time.UTC)
	os.Chtimes(filepath.Join(src, "Yaesu", "FT-991A.pdf"), old, old)
	if runtime.GOOS != "windows" {
		os.Symlink(filepath.Join(src, "NIFOG.pdf"), filepath.Join(src, "link.pdf"))
	}
	// The NIFOG is already in the library, under another name.
	os.MkdirAll(a.Lib.Documents(), 0o755)
	os.WriteFile(filepath.Join(a.Lib.Documents(), "nifog-2.02.pdf"), []byte("%PDF NIFOG 2.02"), 0o644)
	return src
}

func skipped(p FolderPlan) map[string]int {
	out := map[string]int{}
	for _, g := range p.Skipped {
		out[g.Reason] = g.Count
	}
	return out
}

func TestAddFolder(t *testing.T) {
	a := openTestApp(t)
	src := radioFolder(t, a)
	ctx := context.Background()

	plan, err := a.PlanFolder(ctx, src, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Into != "Manuals" || plan.Files != 4 || plan.Duplicates != 2 || plan.Renamed != 1 || !plan.Fits {
		t.Fatalf("plan: %+v", plan)
	}
	if plan.Documents[".pdf"] != 5 || plan.Documents[".txt"] != 1 || plan.Documents[".csv"] != 1 {
		t.Errorf("documents by type: %v", plan.Documents)
	}
	sk := skipped(plan)
	if sk["types Arcana cannot search"] != 2 || sk["hidden or system files"] != 2 || sk["empty files"] != 1 {
		t.Errorf("skipped: %+v", plan.Skipped)
	}
	if runtime.GOOS != "windows" && sk["links (not followed)"] != 1 {
		t.Errorf("link not skipped: %+v", plan.Skipped)
	}
	if !strings.Contains(strings.Join(plan.Skipped[0].Examples, ","), "1 .docx") {
		t.Errorf("examples: %+v", plan.Skipped[0])
	}

	res, err := a.AddFolder(ctx, src, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Copied != 4 || res.Duplicates != 2 || res.Renamed != 1 || len(res.Failed) != 0 {
		t.Fatalf("result: %+v", res)
	}
	docs := a.Lib.Documents()
	for rel, want := range map[string]string{
		"Manuals/Yaesu/FT-991A.pdf": "%PDF FT-991A operating manual",
		"Manuals/Icom/IC-7300.pdf":  "%PDF IC-7300 basic manual",
		"Manuals/notes_ tuner.txt":  "Tune at 10 W into the dummy load first.",
	} {
		if got, err := os.ReadFile(filepath.Join(docs, filepath.FromSlash(rel))); err != nil || string(got) != want {
			t.Errorf("%s: %q %v", rel, got, err)
		}
	}
	for _, rel := range []string{"Manuals/Yaesu/copy of FT-991A.pdf", "Manuals/NIFOG.pdf", "Manuals/schematic.docx", "Manuals/link.pdf", "Manuals/empty.pdf"} {
		if _, err := os.Lstat(filepath.Join(docs, filepath.FromSlash(rel))); err == nil {
			t.Errorf("%s should not have been copied", rel)
		}
	}
	if st, _ := os.Stat(filepath.Join(docs, "Manuals", "Yaesu", "FT-991A.pdf")); st == nil || !st.ModTime().Equal(time.Date(2019, 5, 1, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("modification time not kept: %v", st)
	}
	if left, _ := filepath.Glob(filepath.Join(docs, "Manuals", "*", ".import-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}

	// The copies are listed and searchable by their folder paths.
	if _, err := a.ReindexDocuments(ctx); err != nil {
		t.Fatal(err)
	}
	hits, _, err := a.Docs.Search(ctx, "repeater offset", 10)
	if err != nil || len(hits) != 1 || hits[0].Key != "user:Manuals/frequencies.csv" {
		t.Errorf("search: %+v %v", hits, err)
	}

	// Adding the folder again copies nothing; a manual that changed is
	// kept beside the old one.
	if res, _ = a.AddFolder(ctx, src, "", nil, nil); res.Copied != 0 || res.Duplicates != 6 {
		t.Errorf("again: %+v", res)
	}
	os.WriteFile(filepath.Join(src, "Icom", "IC-7300.pdf"), []byte("%PDF IC-7300 full manual, revised"), 0o644)
	if res, _ = a.AddFolder(ctx, src, "", nil, nil); res.Copied != 1 {
		t.Errorf("changed manual: %+v", res)
	}
	if got, _ := os.ReadFile(filepath.Join(docs, "Manuals", "Icom", "IC-7300 (2).pdf")); string(got) != "%PDF IC-7300 full manual, revised" {
		t.Errorf("changed manual not kept beside the old one: %q", got)
	}

	// Only some types, into a folder of the person's choosing. Every PDF
	// is in the library by now, wherever it is stored.
	plan, _ = a.PlanFolder(ctx, src, "Radio: manuals", []string{".PDF"})
	if plan.Into != "Radio_ manuals" || plan.Files != 0 || plan.Duplicates != 4 || skipped(plan)["types you left out"] != 2 {
		t.Errorf("types: %+v", plan)
	}
}

func TestAddFolderRefusals(t *testing.T) {
	a := openTestApp(t)
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "file.pdf")
	os.WriteFile(file, []byte("x"), 0o644)
	for _, src := range []string{"relative/folder", file, filepath.Join(t.TempDir(), "missing"), a.Lib.Documents(), a.Lib.Root} {
		if _, err := a.PlanFolder(ctx, src, "", nil); err == nil {
			t.Errorf("%s accepted", src)
		}
	}
	// A folder that holds the library: the library is left out.
	parent := filepath.Dir(a.Lib.Root)
	os.WriteFile(filepath.Join(parent, "band plan.txt"), []byte("2 m"), 0o644)
	os.MkdirAll(a.Lib.Documents(), 0o755)
	os.WriteFile(filepath.Join(a.Lib.Documents(), "inside.txt"), []byte("already here"), 0o644)
	plan, err := a.PlanFolder(ctx, parent, "", nil)
	if err != nil || plan.Files != 1 || plan.Documents[".txt"] != 1 {
		t.Errorf("library inside the folder: %+v %v", plan, err)
	}
	// Cancelling stops the copy.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := a.AddFolder(cctx, parent, "", nil, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
}

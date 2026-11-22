package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KK4ODA/arcana/internal/appupdate"
	"github.com/KK4ODA/arcana/internal/buildinfo"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/tasks"
)

// fakeRelease serves a signed release (manifest, signature, archive) like
// a GitHub release, with one program file for this platform.
func fakeRelease(t *testing.T, priv ed25519.PrivateKey, version string, program []byte) *httptest.Server {
	t.Helper()
	progPath := appupdate.CurrentPlatformPath()
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, _ := zw.Create("Arcana/" + progPath)
	w.Write(program)
	zw.Close()
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	archive := "arcana-" + version + "-portable.zip"
	m := appupdate.Manifest{Format: "arcana-release", FormatVersion: 1, Version: version, Released: "2026-12-01T00:00:00Z",
		Notes: "Faster maps.", MinLibraryFormat: 1, MaxLibraryFormat: library.FormatVersion, ArchiveRoot: "Arcana",
		Files:   []appupdate.File{{Path: progPath, Size: int64(len(program)), SHA256: sum(program), Executable: true}},
		Archive: &appupdate.File{Path: archive, Size: int64(zbuf.Len()), SHA256: sum(zbuf.Bytes())}}
	data, _ := json.Marshal(m)
	files := map[string][]byte{"/release.json": data, "/release.json.sig": appupdate.Sign(data, priv), "/" + archive: zbuf.Bytes()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func waitTask(t *testing.T, a *App, id string) tasks.Task {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if tk, ok := a.Tasks.Get(id); ok && tk.State != tasks.Running {
			return tk
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("task did not finish")
	return tasks.Task{}
}

func TestCheckAndInstallUpdate(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	a := openTestApp(t)
	a.Updater.Keys = []ed25519.PublicKey{pub}
	// A real library has a bin/ folder per platform it carries; the updater
	// only installs files for those.
	prog := filepath.Join(a.Lib.Root, filepath.FromSlash(appupdate.CurrentPlatformPath()))
	if err := os.MkdirAll(filepath.Dir(prog), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prog, []byte("old program"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := fakeRelease(t, priv, "99.0.0", []byte("new program"))
	a.Lib.Settings.Update(func(s *library.Settings) error {
		s.Updates.URL = srv.URL
		s.AllowPrivateNetworkDownloads = true // the test server is on loopback
		return nil
	})
	if n := a.UpdateNotice(); n.Available != "" || !n.AutoCheck || n.CheckedAt != nil {
		t.Fatalf("fresh notice %+v", n)
	}

	// The daily check finds the version and remembers it.
	ctx, cancel := context.WithCancel(context.Background())
	go a.appUpdateLoop(ctx, 10*time.Millisecond)
	deadline := time.Now().Add(10 * time.Second)
	for a.UpdateNotice().Available == "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	n := a.UpdateNotice()
	if n.Available != "99.0.0" || n.Notes != "Faster maps." || n.CheckedAt == nil {
		t.Fatalf("notice after check %+v", n)
	}
	// It is kept on the drive, so it is shown after an offline restart.
	if c := a.LastUpdateCheck(); c.Available == nil || c.Available.Version != "99.0.0" {
		t.Fatalf("stored check %+v", c)
	}
	// This test binary is not the library's program, so it cannot restart
	// itself; the notice says why instead of offering a button.
	if n.SelfUpdate.Possible || n.SelfUpdate.Reason == "" {
		t.Fatalf("self update %+v", n.SelfUpdate)
	}

	// One step: download, verify, install.
	restarted := false
	a.RequestRestart = func(SelfUpdate) { restarted = true }
	task, err := a.StartAppUpdateInstall()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.StartAppUpdateInstall(); err == nil {
		t.Fatal("a second install started while one was running")
	}
	tk := waitTask(t, a, task.ID)
	if tk.State != tasks.Succeeded {
		t.Fatalf("install: %s %s", tk.State, tk.Error)
	}
	res := tk.Result.(map[string]any)
	if res["version"] != "99.0.0" || res["restarting"] != false || !strings.Contains(res["message"].(string), "start it again") {
		t.Fatalf("result %v", res)
	}
	got, err := os.ReadFile(prog)
	if err != nil || string(got) != "new program" {
		t.Fatalf("program not installed: %q %v", got, err)
	}
	if st := a.Updater.LoadStatus(); st.AppliedVersion != "99.0.0" || st.PreviousVersion != buildinfo.Version {
		t.Fatalf("status %+v", st)
	}
	// Until Arcana restarts, the notice says the version is installed and a
	// second install is refused: it would replace the kept previous program
	// (the one still running) with a version that never ran.
	if n := a.UpdateNotice(); n.Installed != "99.0.0" || n.Available != "" {
		t.Fatalf("notice after install %+v", n)
	}
	if _, err := a.StartAppUpdateInstall(); err == nil || !strings.Contains(err.Error(), "start it again") {
		t.Fatalf("second install: %v", err)
	}
	time.Sleep(1700 * time.Millisecond)
	if restarted {
		t.Fatal("restart requested although this copy cannot restart itself")
	}
}

// TestInstallRestartsIntoNewProgram covers a copy running from the library
// folder. Installing renames the running program to .previous, and the
// system then reports that file as the process's program (Linux follows the
// file, not the name), so the restart must be planned before installing.
func TestInstallRestartsIntoNewProgram(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	a := openTestApp(t)
	a.Updater.Keys = []ed25519.PublicKey{pub}
	prog := filepath.Join(a.Lib.Root, filepath.FromSlash(appupdate.CurrentPlatformPath()))
	os.MkdirAll(filepath.Dir(prog), 0o755)
	if err := os.WriteFile(prog, []byte("old program"), 0o755); err != nil {
		t.Fatal(err)
	}
	running, _ := os.Stat(prog)
	a.executable = func() (string, error) { // like /proc/self/exe
		for _, p := range []string{prog, prog + ".previous"} {
			if st, err := os.Stat(p); err == nil && os.SameFile(st, running) {
				return p, nil
			}
		}
		return "", os.ErrNotExist
	}
	if su := a.SelfUpdate(); !su.Possible || su.Copy {
		t.Fatalf("self update %+v", su)
	}
	srv := fakeRelease(t, priv, "99.0.0", []byte("new program"))
	a.Lib.Settings.Update(func(s *library.Settings) error {
		s.Updates.URL, s.AllowPrivateNetworkDownloads = srv.URL, true
		return nil
	})
	restart := make(chan SelfUpdate, 1)
	a.RequestRestart = func(su SelfUpdate) { restart <- su }
	task, err := a.StartAppUpdateInstall()
	if err != nil {
		t.Fatal(err)
	}
	tk := waitTask(t, a, task.ID)
	if tk.State != tasks.Succeeded || tk.Result.(map[string]any)["restarting"] != true {
		t.Fatalf("install: %s %s %v", tk.State, tk.Error, tk.Result)
	}
	select {
	case su := <-restart:
		if !su.Possible || su.Exe != prog {
			t.Fatalf("restart plan %+v", su)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no restart was requested")
	}
	if got, _ := os.ReadFile(prog); string(got) != "new program" {
		t.Fatalf("restart would run %q", got)
	}
}

func TestTamperedUpdateRefused(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	a := openTestApp(t)
	a.Updater.Keys = []ed25519.PublicKey{other} // the release is signed by a key we do not trust
	srv := fakeRelease(t, priv, "99.0.0", []byte("evil program"))
	a.Lib.Settings.Update(func(s *library.Settings) error {
		s.Updates.URL, s.AllowPrivateNetworkDownloads = srv.URL, true
		return nil
	})
	task, err := a.StartAppUpdateInstall()
	if err != nil {
		t.Fatal(err)
	}
	tk := waitTask(t, a, task.ID)
	if tk.State != tasks.Failed || !strings.Contains(tk.Error, "signature") {
		t.Fatalf("untrusted release: %s %s", tk.State, tk.Error)
	}
	if _, err := os.Stat(filepath.Join(a.Lib.Root, filepath.FromSlash(appupdate.CurrentPlatformPath()))); err == nil {
		t.Fatal("untrusted program was installed")
	}
	if n := a.UpdateNotice(); n.Available != "" {
		t.Fatalf("untrusted release offered: %+v", n)
	}
}

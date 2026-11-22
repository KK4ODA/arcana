package download

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/content"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/storage"
	"github.com/KK4ODA/arcana/internal/store"
)

// fixture is a library with a manager whose network policy allows the
// loopback test servers.
type fixture struct {
	t       *testing.T
	lib     library.Layout
	st      *store.Store
	content *content.Manager
	mgr     *Manager
	avail   atomic.Int64
	cancel  context.CancelFunc
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Arcana")
	if _, err := library.Init(root, "test"); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, lib: library.Layout{Root: root}}
	f.avail.Store(100 << 30)
	st, err := store.Open(f.lib.DB())
	if err != nil {
		t.Fatal(err)
	}
	f.st = st
	f.content = &content.Manager{Layout: f.lib, Store: st}
	t.Cleanup(func() {
		if f.mgr != nil {
			f.mgr.Stop()
		}
		st.Close()
	})
	return f
}

func (f *fixture) start() {
	f.t.Helper()
	settings := library.DefaultSettings()
	settings.AllowPrivateNetworkDownloads = true
	settings.ReserveBytes = 1 << 20
	f.mgr = New(Options{
		Store:              f.st,
		Layout:             f.lib,
		Content:            f.content,
		Settings:           func() library.Settings { return settings },
		Volume:             func() (storage.Volume, error) { return storage.Volume{TotalBytes: 200 << 30, AvailBytes: uint64(f.avail.Load()), FSType: "exfat"}, nil },
		CheckpointBytes:    64 << 10,
		CheckpointInterval: time.Hour,
		RetryBase:          10 * time.Millisecond,
		RetryMax:           50 * time.Millisecond,
		MaxHTTPAttempts:    4,
		StallTimeout:       2 * time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	if err := f.mgr.Start(ctx); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) stop() {
	f.mgr.Stop()
	f.cancel()
	f.mgr = nil
}

func (f *fixture) wait(id string, states ...string) *store.Job {
	f.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		j, err := f.st.GetJob(context.Background(), id)
		if err == nil {
			for _, s := range states {
				if j.State == s {
					return j
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	j, _ := f.st.GetJob(context.Background(), id)
	f.t.Fatalf("job %s did not reach %v; state %s error %q", id, states, j.State, j.Error)
	return nil
}

func payload(n int, seed byte) []byte {
	b := make([]byte, n)
	x := uint32(seed) + 1
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x >> 24)
	}
	return b
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func resource(id, version string, data []byte, urls ...string) catalog.Resource {
	var mirrors []catalog.Mirror
	for _, u := range urls {
		mirrors = append(mirrors, catalog.Mirror{URL: u})
	}
	return catalog.Resource{
		ID: "test." + id, Provider: "test", Title: "Test " + id, Version: version, Format: "zim",
		Languages: []string{"en"},
		Files: []catalog.File{{Name: id + "_" + version + ".zim", Size: int64(len(data)),
			Hashes: map[string]string{"sha256": sum(data)}, Mirrors: mirrors}},
	}
}

// rangeServer serves data with full Range/If-Range support and records the
// Range headers it receives.
type rangeServer struct {
	*httptest.Server
	mu     sync.Mutex
	ranges []string
	data   []byte
	etag   string
	// dropAfter closes the connection after sending this many body bytes
	// on the first request only.
	dropAfter int
	requests  atomic.Int32
}

func newRangeServer(t *testing.T, data []byte) *rangeServer {
	rs := &rangeServer{data: data, etag: `"v1"`}
	rs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := rs.requests.Add(1)
		rs.mu.Lock()
		rs.ranges = append(rs.ranges, r.Header.Get("Range"))
		etag, d, drop := rs.etag, rs.data, rs.dropAfter
		rs.mu.Unlock()
		w.Header().Set("ETag", etag)
		if n == 1 && drop > 0 {
			w.Header().Set("Content-Length", fmt.Sprint(len(d)))
			w.WriteHeader(200)
			w.Write(d[:drop])
			w.(http.Flusher).Flush()
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		http.ServeContent(w, r, "f.zim", time.Unix(0, 0), bytes.NewReader(d))
	}))
	t.Cleanup(rs.Close)
	return rs
}

func (f *fixture) installedFile(id string) []byte {
	f.t.Helper()
	in, err := f.st.GetInstalled(context.Background(), id)
	if err != nil {
		f.t.Fatalf("not installed: %v", err)
	}
	p, err := f.content.FilePath(in, in.Files[0])
	if err != nil {
		f.t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func TestDownloadVerifyInstall(t *testing.T) {
	f := newFixture(t)
	data := payload(300<<10, 1)
	srv := newRangeServer(t, data)
	f.start()
	job, err := f.mgr.Enqueue(context.Background(), resource("a", "2026-10", data, srv.URL+"/a.zim"))
	if err != nil {
		t.Fatal(err)
	}
	f.wait(job.ID, store.JobCompleted)
	if got := f.installedFile("test.a"); !bytes.Equal(got, data) {
		t.Fatal("installed content differs")
	}
	if _, err := os.Stat(filepath.Join(f.lib.Content(), "test.a", content.SidecarName)); err != nil {
		t.Fatal("sidecar missing")
	}
	if entries, _ := os.ReadDir(f.lib.Staging()); len(entries) != 0 {
		t.Fatalf("staging not cleaned: %v", entries)
	}
	if _, err := f.mgr.Enqueue(context.Background(), resource("a", "2026-10", data, srv.URL+"/a.zim")); err != ErrAlreadyInstalled {
		t.Fatalf("re-enqueue same version: %v", err)
	}
}

func TestResumeAfterConnectionDrop(t *testing.T) {
	f := newFixture(t)
	data := payload(1<<20, 2)
	srv := newRangeServer(t, data)
	srv.dropAfter = 400 << 10
	f.start()
	job, err := f.mgr.Enqueue(context.Background(), resource("b", "1", data, srv.URL+"/b.zim"))
	if err != nil {
		t.Fatal(err)
	}
	f.wait(job.ID, store.JobCompleted)
	if !bytes.Equal(f.installedFile("test.b"), data) {
		t.Fatal("content differs after resume")
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if len(srv.ranges) < 2 || !strings.HasPrefix(srv.ranges[1], "bytes=") || srv.ranges[1] == "bytes=0-" {
		t.Fatalf("second request should resume with a range, got %q", srv.ranges)
	}
}

// TestResumeAfterCrash constructs the state a power loss leaves behind: a
// checkpoint in the database, and a staging file with extra bytes written
// after that checkpoint. The manager must discard the extra bytes, restore
// the hash state, and request only the remainder.
func TestResumeAfterCrash(t *testing.T) {
	f := newFixture(t)
	data := payload(2<<20, 3)
	srv := newRangeServer(t, data)
	res := resource("c", "1", data, srv.URL+"/c.zim")
	if err := res.Validate(); err != nil {
		t.Fatal(err)
	}

	const checkpoint = 700 << 10
	h := sha256.New()
	h.Write(data[:checkpoint])
	state, _ := (hashSet{"sha256": h}).marshal()
	job := &store.Job{ID: "crashjob", ResourceID: res.ID, Version: res.Version, State: store.JobRunning,
		Resource: res, Resolved: true, Offset: checkpoint, HashState: state}
	if err := f.st.PutJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(f.lib.Staging(), job.ID)
	os.MkdirAll(staging, 0o755)
	garbage := append(append([]byte{}, data[:checkpoint]...), bytes.Repeat([]byte{0xEE}, 123456)...)
	os.WriteFile(filepath.Join(staging, res.Files[0].Name+".part"), garbage, 0o644)

	f.start()
	f.wait(job.ID, store.JobCompleted)
	if !bytes.Equal(f.installedFile(res.ID), data) {
		t.Fatal("content differs after crash recovery")
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.ranges[0] != fmt.Sprintf("bytes=%d-", checkpoint) {
		t.Fatalf("expected resume from checkpoint, got %q", srv.ranges)
	}
}

func TestServerWithoutRangeSupport(t *testing.T) {
	f := newFixture(t)
	data := payload(512<<10, 4)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ignores Range entirely and drops the first connection midway.
		n := calls.Add(1)
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.WriteHeader(200)
		if n == 1 {
			w.Write(data[:200<<10])
			w.(http.Flusher).Flush()
			c, _, _ := w.(http.Hijacker).Hijack()
			c.Close()
			return
		}
		w.Write(data)
	}))
	defer srv.Close()
	f.start()
	job, err := f.mgr.Enqueue(context.Background(), resource("d", "1", data, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	f.wait(job.ID, store.JobCompleted)
	if !bytes.Equal(f.installedFile("test.d"), data) {
		t.Fatal("content differs: partial data was not discarded when the server ignored Range")
	}
}

func TestMirrorFallback(t *testing.T) {
	f := newFixture(t)
	data := payload(100<<10, 5)
	missing := httptest.NewServer(http.NotFoundHandler())
	defer missing.Close()
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "overloaded", http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	good := newRangeServer(t, data)
	f.start()
	job, err := f.mgr.Enqueue(context.Background(), resource("e", "1", data, missing.URL, failing.URL, good.URL))
	if err != nil {
		t.Fatal(err)
	}
	f.wait(job.ID, store.JobCompleted)
}

func TestCorruptDownloadNeverInstalled(t *testing.T) {
	f := newFixture(t)
	good := payload(200<<10, 6)
	bad := append([]byte{}, good...)
	bad[1000] ^= 0xFF
	srv := newRangeServer(t, bad)
	f.start()
	job, err := f.mgr.Enqueue(context.Background(), resource("f", "1", good, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	j := f.wait(job.ID, store.JobFailed)
	if !strings.Contains(j.Error, "verification") {
		t.Fatalf("error should explain verification failure: %q", j.Error)
	}
	if _, err := f.st.GetInstalled(context.Background(), "test.f"); err == nil {
		t.Fatal("corrupted download was installed")
	}
	if _, err := os.Stat(filepath.Join(f.lib.Content(), "test.f")); err == nil {
		t.Fatal("corrupted data reached content/")
	}
}

func TestUpdateReplacesOnlyAfterVerification(t *testing.T) {
	f := newFixture(t)
	v1 := payload(150<<10, 7)
	v2 := payload(160<<10, 8)
	s1 := newRangeServer(t, v1)
	f.start()
	j1, err := f.mgr.Enqueue(context.Background(), resource("g", "2026-01", v1, s1.URL))
	if err != nil {
		t.Fatal(err)
	}
	f.wait(j1.ID, store.JobCompleted)

	// A corrupted v2 must leave v1 fully usable.
	corrupt := append([]byte{}, v2...)
	corrupt[5] ^= 1
	s2bad := newRangeServer(t, corrupt)
	j2, err := f.mgr.Enqueue(context.Background(), resource("g", "2026-02", v2, s2bad.URL))
	if err != nil {
		t.Fatal(err)
	}
	if j2.ReplacesVersion != "2026-01" {
		t.Fatalf("ReplacesVersion = %q", j2.ReplacesVersion)
	}
	f.wait(j2.ID, store.JobFailed)
	if !bytes.Equal(f.installedFile("test.g"), v1) {
		t.Fatal("v1 damaged by failed update")
	}

	s2 := newRangeServer(t, v2)
	j3, err := f.mgr.Enqueue(context.Background(), resource("g", "2026-02", v2, s2.URL))
	if err != nil {
		t.Fatal(err)
	}
	f.wait(j3.ID, store.JobCompleted)
	if !bytes.Equal(f.installedFile("test.g"), v2) {
		t.Fatal("update not installed")
	}
	if _, err := os.Stat(filepath.Join(f.lib.Content(), "test.g", "2026-01")); err == nil {
		t.Fatal("old version directory should be removed after a verified update")
	}
}

func TestInsufficientSpaceRefused(t *testing.T) {
	f := newFixture(t)
	f.avail.Store(2 << 20) // 2 MiB free, 1 MiB reserve
	data := payload(1500<<10, 9)
	f.start()
	_, err := f.mgr.Enqueue(context.Background(), resource("h", "1", data, "http://127.0.0.1:1/x"))
	if _, ok := err.(*storage.InsufficientSpaceError); !ok {
		t.Fatalf("expected InsufficientSpaceError, got %v", err)
	}
}

func TestFAT32LimitRefused(t *testing.T) {
	f := newFixture(t)
	f.start()
	f.mgr.opts.Volume = func() (storage.Volume, error) {
		return storage.Volume{TotalBytes: 64 << 30, AvailBytes: 60 << 30, FSType: "fat32"}, nil
	}
	r := resource("big", "1", []byte("x"), "http://127.0.0.1:1/x")
	r.Files[0].Size = 5 << 30
	r.DownloadSize = 5 << 30
	if _, err := f.mgr.Enqueue(context.Background(), r); err == nil || !strings.Contains(err.Error(), "FAT32") {
		t.Fatalf("expected FAT32 refusal, got %v", err)
	}
}

func TestPauseResumeCancel(t *testing.T) {
	f := newFixture(t)
	data := payload(2<<20, 10)
	srv := newRangeServer(t, data)
	f.start()
	// Throttle so the job is still running when paused.
	f.mgr.applyBandwidth(256 << 10)
	job, err := f.mgr.Enqueue(context.Background(), resource("i", "1", data, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	f.wait(job.ID, store.JobRunning)
	time.Sleep(300 * time.Millisecond)
	if err := f.mgr.Pause(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	paused := f.wait(job.ID, store.JobPaused)
	if paused.Offset == 0 {
		t.Fatal("pause should keep a checkpoint")
	}
	f.mgr.applyBandwidth(0)
	if err := f.mgr.Resume(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	f.wait(job.ID, store.JobCompleted)

	// Cancel removes staging data.
	data2 := payload(2<<20, 11)
	srv2 := newRangeServer(t, data2)
	f.mgr.applyBandwidth(128 << 10)
	job2, err := f.mgr.Enqueue(context.Background(), resource("j", "1", data2, srv2.URL))
	if err != nil {
		t.Fatal(err)
	}
	f.wait(job2.ID, store.JobRunning)
	time.Sleep(200 * time.Millisecond)
	if err := f.mgr.Cancel(context.Background(), job2.ID); err != nil {
		t.Fatal(err)
	}
	f.wait(job2.ID, store.JobCancelled)
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(f.lib.Staging(), job2.ID)); err == nil {
		t.Fatal("cancel should delete staging data")
	}
}

func TestShutdownAndRestartResumes(t *testing.T) {
	f := newFixture(t)
	data := payload(3<<20, 12)
	srv := newRangeServer(t, data)
	f.start()
	f.mgr.applyBandwidth(512 << 10)
	job, err := f.mgr.Enqueue(context.Background(), resource("k", "1", data, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	f.wait(job.ID, store.JobRunning)
	time.Sleep(500 * time.Millisecond)
	f.stop()
	j, _ := f.st.GetJob(context.Background(), job.ID)
	if j.State != store.JobQueued || j.Offset == 0 {
		t.Fatalf("after shutdown: state %s offset %d", j.State, j.Offset)
	}
	f.start()
	f.wait(job.ID, store.JobCompleted)
	if !bytes.Equal(f.installedFile("test.k"), data) {
		t.Fatal("content differs after restart")
	}
}

func TestPrivateAddressesBlockedByDefault(t *testing.T) {
	c := NewClient(func() bool { return false })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "secret") }))
	defer srv.Close()
	t.Setenv("HTTP_PROXY", "")
	_, err := c.Get(srv.URL)
	if err == nil || !strings.Contains(err.Error(), "local or private network") {
		t.Fatalf("expected block, got %v", err)
	}
}

func TestParseContentRange(t *testing.T) {
	s, total, ok := parseContentRange("bytes 100-199/1000")
	if !ok || s != 100 || total != 1000 {
		t.Fatal("parse failed")
	}
	if _, _, ok := parseContentRange("bytes */1000"); ok {
		t.Fatal("unsatisfied range should not parse as a start")
	}
}

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/store"
)

func TestInWindow(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 11, 23, h, 30, 0, 0, time.Local) }
	cases := []struct {
		h, start, end int
		want          bool
	}{{2, 1, 6, true}, {6, 1, 6, false}, {0, 1, 6, false}, {23, 22, 6, true}, {3, 22, 6, true}, {12, 22, 6, false}, {9, 5, 5, true}}
	for _, c := range cases {
		if got := InWindow(at(c.h), c.start, c.end); got != c.want {
			t.Errorf("InWindow(%d:30, %d, %d) = %v", c.h, c.start, c.end, got)
		}
	}
	if n := NextWindow(at(12), 22, 6); n.Hour() != 22 || n.Day() != 23 {
		t.Errorf("next window from noon: %v", n)
	}
	if n := NextWindow(at(23), 1, 6); n.Hour() != 1 || n.Day() != 24 {
		t.Errorf("next window from 23:30: %v", n)
	}
}

// A feed offers a document; once installed, the feed publishes a newer
// edition. The schedule queues it inside the window, pauses it outside
// and resumes it inside again.
func TestScheduledContentUpdates(t *testing.T) {
	v1, v2 := "first edition\n", strings.Repeat("second edition\n", 2000)
	sum := func(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
	var version atomic.Int32
	version.Store(1)
	release := make(chan struct{})
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/feed.json":
			v, body := int(version.Load()), v1
			if v == 2 {
				body = v2
			}
			feed := map[string]any{"format": "arcana-catalog", "formatVersion": 1, "title": "Test", "resources": []any{map[string]any{
				"id": "test.doc", "provider": "test", "title": "Test document", "category": "reference", "version": fmt.Sprint(v),
				"languages": []string{"en"}, "format": "text", "updateStrategy": "snapshot-replace",
				"license": map[string]any{"id": "CC0-1.0", "redistribution": "unknown"},
				"files": []any{map[string]any{"name": "doc.txt", "size": len(body), "hashes": map[string]string{"sha256": sum(body)},
					"mirrors": []any{map[string]any{"url": fmt.Sprintf("%s/doc-%d.txt", srv.URL, v)}}}},
			}}}
			json.NewEncoder(w).Encode(feed)
		case "/doc-1.txt":
			w.Write([]byte(v1))
		case "/doc-2.txt":
			// Hold the second edition until the test lets it through, so
			// the job is still running when the window closes.
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			w.Write([]byte(v2))
		}
	}))
	defer srv.Close()
	defer close(release)

	a := openTestApp(t)
	if _, err := a.Lib.Settings.Update(func(s *library.Settings) error {
		s.AllowPrivateNetworkDownloads = true
		s.Catalog.KiwixEnabled, s.Catalog.OpenFDAEnabled, s.Maps.Enabled = false, false, false
		s.Catalog.Feeds = []library.FeedSource{{ID: "test", Name: "Test", URL: srv.URL + "/feed.json", Enabled: true}}
		s.ContentUpdates = library.ContentUpdateSettings{Enabled: true, WindowStart: 1, WindowEnd: 6, EveryDays: 7, Only: "all", BandwidthLimit: 50_000}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := a.Downloads.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RefreshCatalog(ctx); err != nil {
		t.Fatal(err)
	}
	if r := a.EnqueueByID(ctx, []string{"test.doc"}); r["test.doc"] != "queued" {
		t.Fatalf("enqueue: %v", r)
	}
	// A document is recorded as installed a moment before its download is
	// marked completed, so wait for the queue to be empty as well.
	idle := func() bool {
		jobs, err := a.Store.ListJobs(ctx)
		if err != nil {
			return false
		}
		for _, j := range jobs {
			if j.Active() {
				return false
			}
		}
		return true
	}
	waitFor(t, func() bool { _, err := a.Store.GetInstalled(ctx, "test.doc"); return err == nil && idle() })

	version.Store(2)
	clock := time.Date(2026, 11, 23, 12, 0, 0, 0, time.Local)
	a.contentUpd.now = func() time.Time { return clock }

	// Outside the window nothing happens.
	if ev := a.contentUpdateTick(ctx, false); ev.Message != "" {
		t.Fatalf("ran outside the window: %+v", ev)
	}
	clock = time.Date(2026, 11, 24, 2, 0, 0, 0, time.Local)
	ev := a.contentUpdateTick(ctx, false)
	if len(ev.Queued) != 1 || ev.Queued[0] != "Test document" {
		t.Fatalf("inside the window: %+v", ev)
	}
	st := a.contentState()
	if len(st.Jobs) != 1 || !a.contentUpd.active.Load() || a.downloadSettings().BandwidthLimit != 50_000 {
		t.Fatalf("state %+v active=%v limit=%d", st, a.contentUpd.active.Load(), a.downloadSettings().BandwidthLimit)
	}
	// Checked recently: the next tick does not check again.
	if ev := a.contentUpdateTick(ctx, false); ev.Message != "" {
		t.Fatalf("checked again: %+v", ev)
	}
	jobState := func() string { j, _ := a.Store.GetJob(ctx, st.Jobs[0]); return j.State }
	// The window closes: the download is paused and the cap lifted.
	clock = time.Date(2026, 11, 24, 7, 0, 0, 0, time.Local)
	a.contentUpdateTick(ctx, false)
	waitFor(t, func() bool { return jobState() == store.JobPaused })
	if a.contentUpd.active.Load() || a.downloadSettings().BandwidthLimit != 0 {
		t.Fatal("cap still applied outside the window")
	}
	// It opens again: resumed, and finishes once the server answers.
	clock = time.Date(2026, 11, 25, 1, 30, 0, 0, time.Local)
	a.contentUpdateTick(ctx, false)
	if s := jobState(); s == store.JobPaused {
		t.Fatalf("not resumed: %s", s)
	}
	release <- struct{}{}
	waitFor(t, func() bool {
		in, err := a.Store.GetInstalled(ctx, "test.doc")
		return err == nil && in.Version == "2" && idle()
	})
	a.contentUpdateTick(ctx, false)
	if st := a.ContentUpdateStatus(); len(st.State.Jobs) != 0 || st.Active || len(st.State.Log) != 1 {
		t.Fatalf("after install: %+v", st)
	}
	// "Check now" works outside the window and reports nothing to do.
	clock = time.Date(2026, 11, 25, 15, 0, 0, 0, time.Local)
	if ev := a.RunContentUpdatesNow(ctx); !strings.Contains(ev.Message, "up to date") {
		t.Fatalf("check now: %+v", ev)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

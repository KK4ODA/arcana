package maps

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallCachesPlanAndVerifies(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "planet.pmtiles")
	writeTestArchive(t, src, 6)
	srv := serveArchive(t, src, `"b1"`)
	p := &Provider{BaseURL: srv.URL}
	res, err := p.CustomArea("20260101", "Test area", BBox{-20, -20, 30, 25}, 6, 1)
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "area.pmtiles")
	stop := errors.New("not enough space")
	var planned int64
	_, _, err = Install(context.Background(), InstallRequest{Resource: res, Out: out,
		Planned: func(n int64) error { planned = n; return stop }})
	if !errors.Is(err, stop) || planned <= 0 {
		t.Fatalf("planned %d, err %v", planned, err)
	}
	if _, err := os.Stat(out + ".plan"); err != nil {
		t.Fatal("plan not cached for the next attempt")
	}
	var progress int64
	size, sum, err := Install(context.Background(), InstallRequest{Resource: res, Out: out,
		Planned: func(n int64) error {
			if n != planned {
				t.Errorf("cached plan size %d, first %d", n, planned)
			}
			return nil
		},
		Progress: func(d int64) { progress = d }})
	if err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(out)
	if size != planned || st.Size() != size || progress != size || len(sum) != 64 {
		t.Fatalf("size %d planned %d file %d progress %d", size, planned, st.Size(), progress)
	}
	if _, err := os.Stat(out + ".plan"); !os.IsNotExist(err) {
		t.Fatal("plan left behind")
	}
	a, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	a.Close()

	// A build that has been deleted asks for a restart from the newest.
	gone := httptest.NewServer(http.NotFoundHandler())
	defer gone.Close()
	res2, _ := (&Provider{BaseURL: gone.URL}).CustomArea("20250101", "Old", BBox{0, 0, 1, 1}, 6, 1)
	if _, _, err := Install(context.Background(), InstallRequest{Resource: res2, Out: filepath.Join(dir, "old.pmtiles")}); !errors.Is(err, ErrSourceGone) {
		t.Fatalf("expected ErrSourceGone, got %v", err)
	}
	// Only extraction resources are accepted.
	res2.ProviderRef = map[string]string{"method": "other"}
	if _, _, err := Install(context.Background(), InstallRequest{Resource: res2, Out: out}); err == nil {
		t.Fatal("non-extraction resource accepted")
	}
}

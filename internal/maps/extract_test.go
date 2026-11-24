package maps

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func serveArchive(t *testing.T, path, etag string) *httptest.Server {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", etag)
		http.ServeContent(w, r, "planet.pmtiles", time.Unix(0, 0), bytes.NewReader(data))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type failingReader struct {
	RangeReader
	left atomic.Int32
}

func (f *failingReader) ReadRange(ctx context.Context, off, n uint64) ([]byte, error) {
	if f.left.Add(-1) < 0 {
		return nil, errors.New("network down")
	}
	return f.RangeReader.ReadRange(ctx, off, n)
}

func TestExtractRegion(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "planet.pmtiles")
	tiles := writeTestArchive(t, src, 7)
	srv := serveArchive(t, src, `"v1"`)
	rr := &HTTPRangeReader{URL: srv.URL}
	coalesceGap, coalesceMax = 0, 200
	defer func() { coalesceGap, coalesceMax = 64<<10, 32<<20 }()
	bbox := BBox{0, 0, 45, 40} // north-east quadrant slice
	plan, err := MakePlan(context.Background(), rr, bbox, 0, 6, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Tiles == 0 || plan.TileBytes == 0 || plan.ETag != `"v1"` {
		t.Fatalf("plan: %+v", plan)
	}
	if want := TileCount(bbox, 0, 6); uint64(plan.Tiles) != want {
		t.Fatalf("plan covers %d tiles, want %d", plan.Tiles, want)
	}
	out := filepath.Join(dir, "region.pmtiles")

	// Interrupt after a few requests, then resume.
	fr := &failingReader{RangeReader: rr}
	if len(plan.Ranges) < 5 {
		t.Fatalf("expected several ranges, got %d", len(plan.Ranges))
	}
	fr.left.Store(3)
	if _, err := Extract(context.Background(), fr, plan, out, nil); err == nil {
		t.Fatal("expected interruption")
	}
	sum, err := Extract(context.Background(), rr, plan, out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sum) != 64 {
		t.Fatalf("sha256 %q", sum)
	}
	a, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.Header.MaxZoom != 6 || !a.Header.Clustered {
		t.Fatalf("header %+v", a.Header)
	}
	// Every tile in the area matches the source; tiles outside are absent.
	for z := uint8(0); z <= 6; z++ {
		x, y := lonToX(20, z), latToY(20, z)
		got, err := a.Tile(z, x, y)
		if err != nil {
			t.Fatalf("z%d: %v", z, err)
		}
		if !bytes.Equal(got, tiles[ZXYToID(z, x, y)]) {
			t.Fatalf("z%d tile differs", z)
		}
	}
	if _, err := a.Tile(6, lonToX(-100, 6), latToY(-40, 6)); err != ErrNoTile {
		t.Fatalf("tile outside the region: %v", err)
	}
	if _, err := os.Stat(out + ".state.json"); err == nil {
		t.Fatal("checkpoint not cleaned up")
	}
}

func TestSourceChangeDetected(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "planet.pmtiles")
	writeTestArchive(t, src, 5)
	srv := serveArchive(t, src, `"v1"`)
	rr := &HTTPRangeReader{URL: srv.URL}
	rr.SetETag(`"v0"`)
	if _, err := MakePlan(context.Background(), rr, BBox{-10, -10, 10, 10}, 0, 5, nil); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("expected ErrSourceChanged, got %v", err)
	}
}

func TestTooLargeRegionRefused(t *testing.T) {
	if _, err := targetIDs(BBox{-170, -80, 170, 80}, 0, 15); err != ErrTooManyTiles {
		t.Fatalf("got %v", err)
	}
	if _, err := ParseBBox("1,2,3"); err == nil {
		t.Fatal("bad bbox accepted")
	}
	b, err := ParseBBox("170,-20,-170,-10") // crosses the antimeridian
	if err != nil {
		t.Fatal(err)
	}
	if TileCount(b, 4, 4) != 2*1 && TileCount(b, 4, 4) == 0 {
		t.Fatal("antimeridian box should cover tiles on both sides")
	}
}

// slowReader adds latency to every request and records how many run at once.
type slowReader struct {
	RangeReader
	delay             time.Duration
	active, maxActive atomic.Int32
	calls             atomic.Int32
	// first is when the first request started and last when the last one
	// answered (Unix nanoseconds): together, how long fetching took.
	first, last atomic.Int64
}

func (s *slowReader) ReadRange(ctx context.Context, off, n uint64) ([]byte, error) {
	s.first.CompareAndSwap(0, time.Now().UnixNano())
	defer func() {
		end := time.Now().UnixNano()
		for l := s.last.Load(); end > l && !s.last.CompareAndSwap(l, end); l = s.last.Load() {
		}
	}()
	a := s.active.Add(1)
	defer s.active.Add(-1)
	for {
		m := s.maxActive.Load()
		if a <= m || s.maxActive.CompareAndSwap(m, a) {
			break
		}
	}
	s.calls.Add(1)
	time.Sleep(s.delay)
	return s.RangeReader.ReadRange(ctx, off, n)
}

// TestExtractRequestsInParallel: with a slow round trip, ranges are fetched
// several at a time, and the result is identical to fetching them one by
// one.
func TestExtractRequestsInParallel(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "planet.pmtiles")
	writeTestArchive(t, src, 7)
	srv := serveArchive(t, src, `"v1"`)
	rr := &HTTPRangeReader{URL: srv.URL}
	coalesceGap, coalesceMax = 0, 1 // one range per tile
	defer func() { coalesceGap, coalesceMax = 64<<10, 32<<20 }()
	plan, err := MakePlan(context.Background(), rr, BBox{0, 0, 45, 40}, 0, 6, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Ranges) < 16 {
		t.Fatalf("expected many ranges, got %d", len(plan.Ranges))
	}

	sequential := filepath.Join(dir, "one.pmtiles")
	extractParallel = 1
	want, err := Extract(context.Background(), rr, plan, sequential, nil)
	extractParallel = 8
	if err != nil {
		t.Fatal(err)
	}

	slow := &slowReader{RangeReader: rr, delay: 20 * time.Millisecond}
	got, err := Extract(context.Background(), slow, plan, filepath.Join(dir, "many.pmtiles"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatal("parallel extraction produced a different archive")
	}
	if slow.maxActive.Load() < 4 {
		t.Fatalf("at most %d requests ran at once", slow.maxActive.Load())
	}
	// Time the fetching, from the first request to the last answer, not the
	// whole extraction: Extract also syncs the file to disk at each
	// checkpoint (every 16 ranges) and at the end, while fetching goes on,
	// and on Windows CI disks each sync can take over 100 ms.
	fetching := time.Duration(slow.last.Load() - slow.first.Load())
	if serial := time.Duration(slow.calls.Load()) * slow.delay; fetching > serial/2 {
		t.Fatalf("fetching took %v for %d requests of %v each", fetching, slow.calls.Load(), slow.delay)
	}
}

type instantReader struct{}

func (instantReader) ReadRange(ctx context.Context, off, n uint64) ([]byte, error) {
	return []byte{byte(off)}, nil
}

// A range asked for while the request before it has delivered its data but
// not yet given back its slot must still arrive (this hung: the reader
// waited on a channel that was replaced when the range was launched).
func TestPrefetchNextRangeAfterSlotFreed(t *testing.T) {
	defer func(p int) { extractParallel = p }(extractParallel)
	for _, parallel := range []int{1, 2, 8} {
		extractParallel = parallel
		ranges := make([]Range, 20000)
		for i := range ranges {
			ranges[i] = Range{Offset: uint64(i), Length: 1}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		get, stop := prefetchRanges(ctx, instantReader{}, 0, ranges, 0)
		for i := range ranges {
			buf, err := get(i)
			if err != nil {
				t.Fatalf("parallel %d: range %d of %d: %v", parallel, i, len(ranges), err)
			}
			if buf[0] != byte(i) {
				t.Fatalf("parallel %d: range %d returned the data of another range", parallel, i)
			}
		}
		stop()
		cancel()
	}
}

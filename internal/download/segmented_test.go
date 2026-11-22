package download

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KK4ODA/arcana/internal/store"
)

// segServer is a mirror for segmented downloads with adjustable faults.
type segServer struct {
	*httptest.Server
	data []byte
	// delay is added before each response.
	delay time.Duration
	// status, when set, is returned instead of the file.
	status int
	// noRange sends the whole file whatever the request asks for.
	noRange bool
	// corrupt flips a byte in every response.
	corrupt bool
	// size, when set, is the total size the server claims.
	size int64

	mu        sync.Mutex
	ranges    []string
	active    int
	maxActive int
	served    atomic.Int64 // bytes sent in partial responses
}

func newSegServer(t *testing.T, data []byte, setup func(*segServer)) *segServer {
	s := &segServer{data: data}
	if setup != nil {
		setup(s)
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.ranges = append(s.ranges, r.Header.Get("Range"))
		s.active++
		s.maxActive = max(s.maxActive, s.active)
		s.mu.Unlock()
		defer func() {
			s.mu.Lock()
			s.active--
			s.mu.Unlock()
		}()
		if s.delay > 0 {
			time.Sleep(s.delay)
		}
		if s.status != 0 {
			http.Error(w, http.StatusText(s.status), s.status)
			return
		}
		if s.noRange {
			w.Write(s.data)
			return
		}
		var a, b int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &a, &b); err != nil {
			// An open range ("bytes=N-") or none: serve the rest of the file.
			http.ServeContent(w, r, "f.zim", time.Unix(0, 0), bytes.NewReader(s.data))
			return
		}
		part := append([]byte(nil), s.data[a:b+1]...)
		if s.corrupt {
			part[len(part)/2] ^= 0xFF
		}
		total := int64(len(s.data))
		if s.size != 0 {
			total = s.size
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", a, b, total))
		w.Header().Set("Content-Length", fmt.Sprint(len(part)))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(part)
		s.served.Add(int64(len(part)))
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *segServer) closedRanges() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, r := range s.ranges {
		if strings.HasPrefix(r, "bytes=") && !strings.HasSuffix(r, "-") {
			n++
		}
	}
	return n
}

// segFixture uses small pieces so that test files are split into many.
func segFixture(t *testing.T) *fixture {
	f := newFixture(t)
	f.opts = func(o *Options) {
		o.Connections = 4
		o.ConnectionsPerMirror = 2
		o.SegmentSize = 64 << 10
		o.SegmentWindow = 8
	}
	return f
}

func TestSegmentedAcrossMirrors(t *testing.T) {
	f := segFixture(t)
	data := payload(2<<20, 11)
	var srvs []*segServer
	var urls []string
	for range 3 {
		s := newSegServer(t, data, nil)
		srvs = append(srvs, s)
		urls = append(urls, s.URL+"/f.zim")
	}
	f.start()
	job, err := f.mgr.Enqueue(context.Background(), resource("seg", "1", data, urls...))
	if err != nil {
		t.Fatal(err)
	}
	f.wait(job.ID, store.JobCompleted)
	if !bytes.Equal(f.installedFile("test.seg"), data) {
		t.Fatal("assembled content differs")
	}
	total := 0
	for i, s := range srvs {
		n := s.closedRanges()
		total += n
		if n == 0 {
			t.Errorf("mirror %d served no pieces", i)
		}
		if s.maxActive > 2 {
			t.Errorf("mirror %d had %d connections at once; the cap is 2", i, s.maxActive)
		}
	}
	if total < 32 {
		t.Fatalf("%d piece requests for 32 pieces", total)
	}
}

func TestSegmentedPrefersFastMirror(t *testing.T) {
	f := segFixture(t)
	data := payload(2<<20, 12)
	slow := newSegServer(t, data, func(s *segServer) { s.delay = 150 * time.Millisecond })
	fast := newSegServer(t, data, nil)
	f.start()
	job, err := f.mgr.Enqueue(context.Background(), resource("fast", "1", data, slow.URL+"/f.zim", fast.URL+"/f.zim"))
	if err != nil {
		t.Fatal(err)
	}
	f.wait(job.ID, store.JobCompleted)
	if !bytes.Equal(f.installedFile("test.fast"), data) {
		t.Fatal("assembled content differs")
	}
	if s, q := slow.served.Load(), fast.served.Load(); q < 3*s {
		t.Fatalf("fast mirror served %d bytes, slow mirror %d: the fast one should do most of the work", q, s)
	}
}

func TestSegmentedDropsBadMirrors(t *testing.T) {
	f := segFixture(t)
	data := payload(1<<20, 13)
	missing := newSegServer(t, data, func(s *segServer) { s.status = http.StatusNotFound })
	other := newSegServer(t, data, func(s *segServer) { s.size = int64(len(data)) + 1 })
	failing := newSegServer(t, data, func(s *segServer) { s.status = http.StatusInternalServerError })
	good := newSegServer(t, data, nil)
	f.start()
	job, err := f.mgr.Enqueue(context.Background(), resource("bad", "1", data,
		missing.URL+"/f.zim", other.URL+"/f.zim", failing.URL+"/f.zim", good.URL+"/f.zim"))
	if err != nil {
		t.Fatal(err)
	}
	j := f.wait(job.ID, store.JobCompleted)
	if !bytes.Equal(f.installedFile("test.bad"), data) {
		t.Fatal("assembled content differs")
	}
	bad := strings.Join(j.BadMirrors, " ")
	if !strings.Contains(bad, missing.URL) || !strings.Contains(bad, other.URL) {
		t.Fatalf("404 and wrong-size mirrors should be recorded as unusable: %q", j.BadMirrors)
	}
	if strings.Contains(bad, failing.URL) || strings.Contains(bad, good.URL) {
		t.Fatalf("a server error is not a reason to give up on a mirror for good: %q", j.BadMirrors)
	}
}

func TestSegmentedWithoutRangesFallsBack(t *testing.T) {
	f := segFixture(t)
	data := payload(1<<20, 14)
	plain := newSegServer(t, data, func(s *segServer) { s.noRange = true })
	f.start()
	job, err := f.mgr.Enqueue(context.Background(), resource("plain", "1", data, plain.URL+"/f.zim"))
	if err != nil {
		t.Fatal(err)
	}
	f.wait(job.ID, store.JobCompleted)
	if !bytes.Equal(f.installedFile("test.plain"), data) {
		t.Fatal("content differs")
	}
}

// TestSegmentedResumeAfterCrash: after a power loss, a segmented download
// continues from the checkpoint like a single-connection one.
func TestSegmentedResumeAfterCrash(t *testing.T) {
	f := segFixture(t)
	data := payload(2<<20, 15)
	srv := newSegServer(t, data, nil)
	res := resource("segcrash", "1", data, srv.URL+"/f.zim")
	const checkpoint = 640 << 10
	h := sha256.New()
	h.Write(data[:checkpoint])
	state, _ := (hashSet{"sha256": h}).marshal()
	job := &store.Job{ID: "segcrash", ResourceID: res.ID, Version: res.Version, State: store.JobRunning,
		Resource: res, Resolved: true, Offset: checkpoint, HashState: state}
	if err := f.st.PutJob(context.Background(), job); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(f.lib.Staging(), job.ID)
	os.MkdirAll(staging, 0o755)
	garbage := append(append([]byte{}, data[:checkpoint]...), bytes.Repeat([]byte{0xEE}, 99999)...)
	os.WriteFile(filepath.Join(staging, res.Files[0].Name+".part"), garbage, 0o644)

	f.start()
	f.wait(job.ID, store.JobCompleted)
	if !bytes.Equal(f.installedFile(res.ID), data) {
		t.Fatal("content differs after crash recovery")
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	for _, r := range srv.ranges {
		var a, b int64
		if _, err := fmt.Sscanf(r, "bytes=%d-%d", &a, &b); err != nil || a < checkpoint {
			t.Fatalf("request %q: everything before the checkpoint was already on the drive", r)
		}
	}
}

// TestSegmentedCorruptMirror: a mirror that serves damaged data makes the
// assembled file fail its check; the retry goes mirror by mirror and the
// download still completes.
func TestSegmentedCorruptMirror(t *testing.T) {
	f := segFixture(t)
	data := payload(1<<20, 16)
	bad := newSegServer(t, data, func(s *segServer) { s.corrupt = true })
	good := newSegServer(t, data, nil)
	f.start()
	job, err := f.mgr.Enqueue(context.Background(), resource("corrupt", "1", data, bad.URL+"/f.zim", good.URL+"/f.zim"))
	if err != nil {
		t.Fatal(err)
	}
	j := f.wait(job.ID, store.JobCompleted, store.JobFailed)
	if j.State != store.JobCompleted {
		t.Fatalf("job %s: %s", j.State, j.Error)
	}
	if !j.MixedIntegrityFailure || j.IntegrityFailures == 0 {
		t.Fatalf("expected a failed check of the mixed download first (failures %d)", j.IntegrityFailures)
	}
	if !bytes.Equal(f.installedFile("test.corrupt"), data) {
		t.Fatal("content differs")
	}
}

// TestSmallFilesUseOneConnection keeps small downloads simple.
func TestSmallFilesUseOneConnection(t *testing.T) {
	f := segFixture(t)
	data := payload(100<<10, 17)
	srv := newSegServer(t, data, nil)
	f.start()
	job, err := f.mgr.Enqueue(context.Background(), resource("small", "1", data, srv.URL+"/f.zim"))
	if err != nil {
		t.Fatal(err)
	}
	f.wait(job.ID, store.JobCompleted)
	if n := srv.closedRanges(); n != 0 {
		t.Fatalf("%d piece requests for a small file", n)
	}
}

// TestSegmentClientUsesHTTP1 guards against piece requests being sent over
// HTTP/2 (one shared connection) or misreading a server that negotiated it.
func TestSegmentClientUsesHTTP1(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, r.Proto)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()
	base := srv.Client()
	if resp, err := base.Get(srv.URL); err != nil || resp.ProtoMajor != 2 {
		t.Fatalf("test server should speak HTTP/2 to an ordinary client: %v", err)
	} else {
		resp.Body.Close()
	}
	c := segmentClient(base)
	for range 3 {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.ProtoMajor != 1 {
			t.Fatalf("piece request used %s", resp.Proto)
		}
	}
}

package download

// Segmented transfers. One connection to one mirror is often far slower
// than the user's line: many mirrors limit each connection, and the mirror
// ranked first for a location is not always the fastest (measured on the
// Kiwix mirrors: 5 to 80 MB/s for the same file). A large file with a
// publisher hash is therefore fetched as pieces over several connections
// and mirrors at once, and each new piece goes to the fastest mirror
// measured so far.
//
// Pieces are kept in memory and written and hashed strictly in order, so
// the staging file and its checkpoint are exactly those of a
// single-connection download: resuming, pausing and verification work the
// same way. Memory use is bounded by the window (SegmentWindow pieces).

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptrace"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/store"
)

// errNoRanges reports that no mirror serves byte ranges, so the file must
// be fetched in one piece.
var errNoRanges = errors.New("no mirror supports range requests")

// useSegments decides whether a file is fetched in pieces. A publisher
// hash is required because pieces may come from different mirrors: only
// the final hash proves the assembled file. Copies from another Arcana on
// the local network stay on one connection.
func (m *Manager) useSegments(j *store.Job, f catalog.File) bool {
	return m.opts.Connections > 1 &&
		f.Size >= 4*m.opts.SegmentSize &&
		hasHash(f) &&
		j.IntegrityFailures == 0 && // after a failed check, one mirror at a time
		j.Resource.ProviderRef["via"] != "peer"
}

type segState int

const (
	segPending segState = iota
	segActive
	segDone
)

type segment struct {
	start, end int64 // [start, end)
	state      segState
	data       []byte
	attempts   []*segAttempt
}

type segAttempt struct {
	mirror *segMirror
	cancel context.CancelFunc
	begin  time.Time
	got    atomic.Int64 // bytes received so far
	ready  atomic.Int64 // when the connection was ready (UnixNano), 0 before
}

type segMirror struct {
	url     string
	host    string
	active  int
	rate    float64 // bytes per second per connection; 0 until measured
	fails   int     // consecutive failures
	retryAt time.Time
	off     bool // not used again for this transfer
	noRange bool
	lastErr error
	bytes   int64 // delivered pieces
}

type segTransfer struct {
	m        *Manager
	j        *store.Job
	f        catalog.File
	mirrors  []*segMirror
	received atomic.Int64 // bytes received, for the speed display

	mu      sync.Mutex
	changed chan struct{} // closed and replaced on every state change
	segs    []*segment
	next    int // index of the next segment to write
	bad     []string
}

const (
	// exploreMirrors is how many mirrors, in the publisher's order, are
	// tried before the fastest measured ones take over.
	exploreMirrors = 6
)

func (t *segTransfer) notifyLocked() {
	close(t.changed)
	t.changed = make(chan struct{})
}

// fetchSegmented downloads the rest of f (from j.Offset) into fh.
func (m *Manager) fetchSegmented(ctx context.Context, j *store.Job, rs *runState, f catalog.File, fh *os.File,
	hs hashSet, baseDone int64, persist func()) error {

	t := &segTransfer{m: m, j: j, f: f, changed: make(chan struct{})}
	bad := map[string]bool{}
	for _, b := range j.BadMirrors {
		bad[b] = true
	}
	for _, mi := range f.Mirrors {
		if !bad[mi.URL] {
			t.mirrors = append(t.mirrors, &segMirror{url: mi.URL, host: hostOf(mi.URL)})
		}
	}
	if len(t.mirrors) == 0 {
		return fail(kindFatal, "no mirror could provide %s", f.Name)
	}
	for s := j.Offset; s < f.Size; s += m.opts.SegmentSize {
		e := min(s+m.opts.SegmentSize, f.Size)
		t.segs = append(t.segs, &segment{start: s, end: e})
	}

	wctx, stopWorkers := context.WithCancel(ctx)
	var wg sync.WaitGroup
	for range m.opts.Connections {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t.worker(wctx)
		}()
	}
	defer func() {
		stopWorkers()
		wg.Wait()
	}()

	written := j.Offset
	lastCheckpointBytes := written
	lastCheckpoint := time.Now()
	lastSample, lastSampleBytes := time.Now(), t.received.Load()
	for t.next < len(t.segs) {
		t.mu.Lock()
		seg := t.segs[t.next]
		if seg.state != segDone {
			if err := t.deadLocked(); err != nil {
				t.mu.Unlock()
				t.recordBad(j)
				m.checkpoint(fh, j, hs, persist)
				return err
			}
			ch := t.changed
			t.mu.Unlock()
			select {
			case <-ch:
			case <-ctx.Done():
				t.recordBad(j)
				m.checkpoint(fh, j, hs, persist)
				return ctx.Err()
			case <-time.After(time.Second):
			}
			t.sample(rs, &lastSample, &lastSampleBytes)
			continue
		}
		data := seg.data
		seg.data = nil
		t.next++
		buffered := t.bufferedLocked()
		t.notifyLocked() // the window moved
		t.mu.Unlock()

		if _, err := fh.Write(data); err != nil {
			if isNoSpace(err) {
				return &jobError{kind: kindSpace, err: errors.New("the drive is full")}
			}
			return fail(kindFatal, "write to drive: %v", err)
		}
		hs.Write(data)
		written += int64(len(data))
		rs.bytesDone.Store(baseDone + written + buffered)
		t.sample(rs, &lastSample, &lastSampleBytes)
		if written-lastCheckpointBytes >= m.opts.CheckpointBytes ||
			(time.Since(lastCheckpoint) >= m.opts.CheckpointInterval && written > lastCheckpointBytes) {
			t.recordBad(j)
			m.checkpoint(fh, j, hs, persist)
			lastCheckpointBytes, lastCheckpoint = written, time.Now()
			if err := m.checkSpace(ctx, j, remainingBytes(j, -1)); err != nil {
				return err
			}
		}
	}
	t.recordBad(j)
	t.logSources()
	return nil
}

// logSources records which mirrors delivered the file, and how fast.
func (t *segTransfer) logSources() {
	t.mu.Lock()
	defer t.mu.Unlock()
	var parts []string
	for _, mi := range t.mirrors {
		if mi.bytes > 0 {
			parts = append(parts, fmt.Sprintf("%s %d MB at %.1f MB/s per connection", mi.host, mi.bytes>>20, mi.rate/1e6))
		}
	}
	t.m.log.Info("downloaded in pieces", "file", t.f.Name, "sources", strings.Join(parts, "; "))
}

// sample updates the speed and mirror shown for the job, about once a
// second.
func (t *segTransfer) sample(rs *runState, last *time.Time, lastBytes *int64) {
	now := time.Now()
	if now.Sub(*last) < time.Second {
		return
	}
	got := t.received.Load()
	inst := float64(got-*lastBytes) / now.Sub(*last).Seconds()
	prev := float64(rs.speed.Load())
	if prev == 0 {
		prev = inst
	}
	rs.speed.Store(int64(0.7*prev + 0.3*inst))
	*last, *lastBytes = now, got

	t.mu.Lock()
	var used []*segMirror
	for _, mi := range t.mirrors {
		if mi.active > 0 {
			used = append(used, mi)
		}
	}
	t.mu.Unlock()
	sort.SliceStable(used, func(a, b int) bool { return used[a].rate > used[b].rate })
	switch len(used) {
	case 0:
	case 1:
		rs.mirror.Store(used[0].host)
	default:
		rs.mirror.Store(fmt.Sprintf("%s and %d more", used[0].host, len(used)-1))
	}
}

// bufferedLocked counts bytes received for pieces not yet written.
func (t *segTransfer) bufferedLocked() int64 {
	var n int64
	for i := t.next; i < len(t.segs) && i < t.next+t.m.opts.SegmentWindow; i++ {
		if s := t.segs[i]; s.state == segDone {
			n += s.end - s.start
		}
	}
	return n
}

// recordBad copies the mirrors found unable to serve the file into the job
// (only the writer goroutine touches the job).
func (t *segTransfer) recordBad(j *store.Job) {
	t.mu.Lock()
	defer t.mu.Unlock()
	have := map[string]bool{}
	for _, b := range j.BadMirrors {
		have[b] = true
	}
	for _, b := range t.bad {
		if !have[b] {
			j.BadMirrors = append(j.BadMirrors, b)
			have[b] = true
		}
	}
}

// deadLocked returns an error when no mirror can be used any more.
func (t *segTransfer) deadLocked() error {
	var lastErr error
	allNoRange, network := true, false
	for _, mi := range t.mirrors {
		if !mi.off {
			return nil
		}
		if !mi.noRange {
			allNoRange = false
		}
		var je *jobError
		if mi.lastErr != nil && (!errors.As(mi.lastErr, &je) || je.kind != kindFatal) {
			network = true
		}
		if mi.lastErr != nil {
			lastErr = mi.lastErr
		}
	}
	switch {
	case allNoRange:
		return errNoRanges
	case network:
		// At least one mirror failed for a reason that may pass: retry the
		// job later from the checkpoint.
		return lastErr
	default:
		return fail(kindFatal, "no mirror could provide %s: %v", t.f.Name, lastErr)
	}
}

func (t *segTransfer) worker(ctx context.Context) {
	for {
		t.mu.Lock()
		if t.next >= len(t.segs) || t.deadLocked() != nil {
			t.mu.Unlock()
			return
		}
		seg, mi, wait := t.pickLocked()
		if seg == nil {
			ch := t.changed
			t.mu.Unlock()
			select {
			case <-ch:
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
			continue
		}
		actx, cancel := context.WithCancel(ctx)
		att := &segAttempt{mirror: mi, cancel: cancel, begin: time.Now()}
		seg.attempts = append(seg.attempts, att)
		if seg.state == segPending {
			seg.state = segActive
		}
		mi.active++
		t.mu.Unlock()

		data, err := t.fetchSegment(actx, seg, att)
		cancel()

		t.mu.Lock()
		mi.active--
		for i, a := range seg.attempts {
			if a == att {
				seg.attempts = append(seg.attempts[:i], seg.attempts[i+1:]...)
				break
			}
		}
		switch {
		case err == nil && seg.state != segDone:
			seg.data, seg.state = data, segDone
			for _, a := range seg.attempts {
				a.cancel() // a slower copy of the same piece
			}
			mi.fails = 0
			mi.bytes += int64(len(data))
			// The rate counts from the moment the connection was ready: the
			// wait for each response is part of a piece's cost, but setting
			// up a connection (TCP, TLS, proxy) is paid once, not per piece.
			from := att.begin
			if ns := att.ready.Load(); ns != 0 {
				from = time.Unix(0, ns)
			}
			r := float64(len(data)) / max(time.Since(from).Seconds(), 1e-3)
			if mi.rate == 0 {
				mi.rate = r
			} else {
				mi.rate = 0.5*mi.rate + 0.5*r
			}
		case err == nil, seg.state == segDone:
			// Another attempt delivered this piece first.
		case ctx.Err() != nil:
			if len(seg.attempts) == 0 {
				seg.state = segPending
			}
		default:
			if len(seg.attempts) == 0 {
				seg.state = segPending
			}
			t.mirrorFailedLocked(mi, err)
		}
		t.notifyLocked()
		t.mu.Unlock()
	}
}

// mirrorFailedLocked records a failed request.
func (t *segTransfer) mirrorFailedLocked(mi *segMirror, err error) {
	mi.lastErr = err
	var je *jobError
	switch {
	case errors.Is(err, errNoRanges):
		mi.off, mi.noRange = true, true
		t.m.log.Info("mirror does not serve byte ranges", "mirror", mi.host)
	case errors.As(err, &je) && je.kind == kindFatal:
		mi.off = true
		t.bad = append(t.bad, mi.url)
		t.m.log.Warn("mirror rejected", "mirror", mi.host, "err", err)
	default:
		mi.fails++
		if mi.fails >= 6 {
			mi.off = true
			t.m.log.Warn("mirror keeps failing; not using it for this file", "mirror", mi.host, "err", err)
			return
		}
		mi.retryAt = time.Now().Add(min(time.Second<<mi.fails, 30*time.Second))
	}
}

// pickLocked chooses the next piece to request and the mirror to request it
// from, or how long to wait before trying again.
func (t *segTransfer) pickLocked() (*segment, *segMirror, time.Duration) {
	now := time.Now()
	limit := min(len(t.segs), t.next+t.m.opts.SegmentWindow)
	best := 0.0
	for _, mi := range t.mirrors {
		if !mi.off && mi.rate > best {
			best = mi.rate
		}
	}
	// choose returns the mirror for a new request: first each of the
	// publisher's leading mirrors once (to measure them), then the fastest
	// measured mirror with a free connection. Mirrors much slower than the
	// best are left alone, since their pieces would hold up all the others;
	// further unmeasured mirrors are used only when nothing better is free.
	choose := func(exclude *segMirror, minRate float64) *segMirror {
		var pick, spare *segMirror
		for i, mi := range t.mirrors {
			if mi == exclude || mi.off || now.Before(mi.retryAt) || mi.active >= t.m.opts.ConnectionsPerMirror {
				continue
			}
			if mi.rate == 0 {
				if mi.active == 0 && minRate == 0 {
					if i < exploreMirrors {
						return mi
					}
					if spare == nil {
						spare = mi
					}
				}
				continue
			}
			if mi.rate < best/4 || mi.rate < minRate {
				continue
			}
			if pick == nil || mi.rate > pick.rate {
				pick = mi
			}
		}
		if pick == nil {
			return spare
		}
		return pick
	}
	for i := t.next; i < limit; i++ {
		if s := t.segs[i]; s.state == segPending {
			if mi := choose(nil, 0); mi != nil {
				return s, mi, 0
			}
			return nil, nil, t.waitLocked(now)
		}
	}
	// Nothing new to start in the window: pieces still in flight hold up
	// the writing of all later ones. Request one again from a faster mirror
	// when that would finish it in less than half the time; whichever copy
	// arrives first is used.
	for i := t.next; i < limit; i++ {
		s := t.segs[i]
		if s.state != segActive || len(s.attempts) != 1 {
			continue
		}
		cur := s.attempts[0]
		elapsed := now.Sub(cur.begin).Seconds()
		if elapsed < 0.2 {
			continue
		}
		mi := choose(cur.mirror, 1) // measured mirrors only
		if mi == nil {
			break
		}
		size := float64(s.end - s.start)
		got := float64(cur.got.Load())
		curRate := cur.mirror.rate
		if curRate == 0 {
			curRate = got / elapsed
		}
		curETA := math.Inf(1)
		if curRate > 0 {
			curETA = (size - got) / curRate
		}
		if size/mi.rate < curETA/2 {
			return s, mi, 0
		}
	}
	return nil, nil, t.waitLocked(now)
}

// waitLocked is how long an idle worker waits before looking again.
func (t *segTransfer) waitLocked(now time.Time) time.Duration {
	wait := time.Second
	for _, mi := range t.mirrors {
		if !mi.off && mi.retryAt.After(now) && mi.retryAt.Sub(now) < wait {
			wait = mi.retryAt.Sub(now)
		}
	}
	return max(wait, 10*time.Millisecond)
}

// fetchSegment requests one piece from one mirror.
func (t *segTransfer) fetchSegment(ctx context.Context, seg *segment, att *segAttempt) ([]byte, error) {
	m := t.m
	mi := att.mirror
	reqCtx, cancelReq := context.WithCancel(ctx)
	defer cancelReq()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, mi.url, nil)
	if err != nil {
		return nil, fail(kindFatal, "bad URL: %v", err)
	}
	req.Header.Set("User-Agent", catalog.UserAgent())
	if m.opts.RequestHook != nil {
		m.opts.RequestHook(req)
	}
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", seg.start, seg.end-1))

	trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) {
		att.ready.Store(time.Now().UnixNano())
	}}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := m.segClient.Do(req)
	if err != nil {
		return nil, classifyNetErr(err, "connect to "+mi.host)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusPartialContent:
		start, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start != seg.start {
			return nil, fail(kindServer, "%s returned an unexpected range (%q)", mi.host, resp.Header.Get("Content-Range"))
		}
		if total >= 0 && total != t.f.Size {
			return nil, fail(kindFatal, "%s has a %d-byte file, catalog states %d", mi.host, total, t.f.Size)
		}
	case http.StatusOK:
		return nil, errNoRanges
	case http.StatusNotFound, http.StatusGone, http.StatusForbidden, http.StatusUnauthorized:
		return nil, fail(kindFatal, "%s: %s", mi.host, resp.Status)
	case http.StatusTooManyRequests:
		return nil, fail(kindServer, "%s is rate limiting downloads", mi.host)
	default:
		if resp.StatusCode >= 500 {
			return nil, fail(kindServer, "%s: %s", mi.host, resp.Status)
		}
		return nil, fail(kindFatal, "%s: unexpected %s", mi.host, resp.Status)
	}

	stall := time.AfterFunc(m.opts.StallTimeout, cancelReq)
	defer stall.Stop()
	buf := make([]byte, seg.end-seg.start)
	n := 0
	for n < len(buf) {
		k, rerr := resp.Body.Read(buf[n:min(len(buf), n+256<<10)])
		if k > 0 {
			stall.Reset(m.opts.StallTimeout)
			if err := m.throttle(ctx, k); err != nil {
				return nil, err
			}
			n += k
			att.got.Store(int64(n))
			t.received.Add(int64(k))
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if reqCtx.Err() != nil {
				return nil, fail(kindNetwork, "no data received from %s for %s", mi.host, m.opts.StallTimeout)
			}
			return nil, classifyNetErr(rerr, "download from "+mi.host+" interrupted")
		}
	}
	if n != len(buf) {
		return nil, fail(kindNetwork, "%s sent %d of %d bytes", mi.host, n, len(buf))
	}
	// A server that sends more than the range asked for is not trusted.
	if extra, _ := resp.Body.Read(make([]byte, 1)); extra > 0 {
		return nil, fail(kindServer, "%s sent more data than requested", mi.host)
	}
	return buf, nil
}

// segmentClient returns a client for piece requests that opens separate
// HTTP/1.1 connections: over HTTP/2, parallel requests to one mirror would
// share a single connection and its per-connection limit.
func segmentClient(c *http.Client) *http.Client {
	var reg *registryAuth
	base := c.Transport
	if ra, ok := base.(*registryAuth); ok {
		reg, base = ra, ra.base
	}
	tr, ok := base.(*http.Transport)
	if !ok {
		return c
	}
	tr = tr.Clone()
	tr.ForceAttemptHTTP2 = false
	tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	// Offer only HTTP/1.1 in the TLS handshake: a cloned configuration can
	// still offer "h2", and a server that accepts it answers in HTTP/2.
	if tr.TLSClientConfig != nil {
		tr.TLSClientConfig = tr.TLSClientConfig.Clone()
	} else {
		tr.TLSClientConfig = &tls.Config{}
	}
	tr.TLSClientConfig.NextProtos = []string{"http/1.1"}
	tr.MaxIdleConnsPerHost = 8
	cc := *c
	cc.Transport = tr
	if reg != nil {
		cc.Transport = &registryAuth{base: tr}
	}
	return &cc
}

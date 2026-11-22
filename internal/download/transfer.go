package download

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/content"
	"github.com/KK4ODA/arcana/internal/storage"
	"github.com/KK4ODA/arcana/internal/store"
)

// errorKind classifies failures for retry policy.
type errorKind int

const (
	kindNetwork   errorKind = iota // retry indefinitely with capped backoff
	kindServer                     // retry a bounded number of times
	kindIntegrity                  // restart from zero once, then fail
	kindFatal                      // fail now
	kindSpace                      // pause until the user frees space
)

type jobError struct {
	kind errorKind
	err  error
}

func (e *jobError) Error() string { return e.err.Error() }
func (e *jobError) Unwrap() error { return e.err }

func fail(kind errorKind, format string, args ...any) error {
	return &jobError{kind: kind, err: fmt.Errorf(format, args...)}
}

// run drives one job until it completes, fails, or is interrupted.
func (m *Manager) run(ctx context.Context, j *store.Job, rs *runState) {
	log := m.log.With("job", j.ID, "resource", j.ResourceID)
	persist := func() {
		// Persist with a background context so an interruption can still
		// be recorded.
		if err := m.opts.Store.PutJob(context.WithoutCancel(ctx), j); err != nil {
			log.Error("persist job", "err", err)
		}
	}

	var doneBytes int64
	for _, f := range j.Done {
		doneBytes += f.Size
	}
	rs.bytesDone.Store(doneBytes + j.Offset)

	err := m.execute(ctx, j, rs, persist)
	if err == nil {
		return
	}

	cause := context.Cause(ctx)
	switch {
	case errors.Is(cause, errPaused):
		j.State = store.JobPaused
		j.Error = ""
		persist()
		log.Info("paused", "offset", j.Offset)
		return
	case errors.Is(cause, errCancelled):
		j.State = store.JobCancelled
		j.Error = ""
		persist()
		m.removeStaging(j.ID)
		log.Info("cancelled")
		return
	case errors.Is(cause, errShutdown) || (ctx.Err() != nil && cause == ctx.Err()):
		// Shutdown: leave the job queued so it resumes next time.
		if j.State != store.JobInstalling {
			j.State = store.JobQueued
		}
		persist()
		return
	}

	var je *jobError
	if !errors.As(err, &je) {
		je = &jobError{kind: kindFatal, err: err}
	}
	j.Error = je.Error()
	switch je.kind {
	case kindSpace:
		j.State = store.JobPaused
		log.Warn("paused: insufficient space", "err", err)
	case kindFatal:
		j.State = store.JobFailed
		m.removeStaging(j.ID)
		log.Error("failed", "err", err)
	case kindIntegrity:
		j.IntegrityFailures++
		limit := 2
		if j.MixedIntegrityFailure {
			limit = 3
		}
		if j.IntegrityFailures >= limit {
			j.State = store.JobFailed
			m.removeStaging(j.ID)
			log.Error("failed integrity verification twice", "err", err)
		} else {
			m.scheduleRetry(j)
			log.Warn("integrity check failed; restarting from another mirror", "err", err)
		}
	case kindServer:
		j.Attempts++
		if j.Attempts >= m.opts.MaxHTTPAttempts {
			j.State = store.JobFailed
			log.Error("failed after retries", "attempts", j.Attempts, "err", err)
		} else {
			m.scheduleRetry(j)
			log.Warn("server error; will retry", "attempt", j.Attempts, "err", err)
		}
	default: // kindNetwork
		j.Attempts++
		m.scheduleRetry(j)
		j.Error = "Waiting for network: " + je.Error()
		log.Warn("network error; will retry", "attempt", j.Attempts, "next", j.NextAttempt, "err", err)
	}
	persist()
}

// scheduleRetry sets exponential backoff with jitter.
func (m *Manager) scheduleRetry(j *store.Job) {
	n := j.Attempts
	if n < 1 {
		n = 1
	}
	if n > 16 {
		n = 16
	}
	d := m.opts.RetryBase << (n - 1)
	if d > m.opts.RetryMax || d <= 0 {
		d = m.opts.RetryMax
	}
	d = d/2 + time.Duration(rand.Int64N(int64(d/2)+1))
	j.State = store.JobWaiting
	j.NextAttempt = time.Now().Add(d)
}

func (m *Manager) execute(ctx context.Context, j *store.Job, rs *runState, persist func()) error {
	if j.State == store.JobInstalling {
		return m.install(ctx, j, persist)
	}
	if !j.Resolved {
		j.State = store.JobResolving
		persist()
		res := j.Resource
		r, ok := m.opts.Resolvers[res.Provider]
		if m.opts.ResolverFor != nil {
			r = m.opts.ResolverFor(res.Provider)
			ok = r != nil
		}
		if res.ProviderRef["via"] == "peer" {
			// Copied from another Arcana: its locations and checksums are
			// already final; resolving would point back at the internet.
			ok = false
		}
		if ok {
			resolved, err := r.Resolve(ctx, m.opts.Fetcher, res)
			if err != nil {
				return classifyNetErr(err, "resolve download locations")
			}
			res = resolved
		}
		if res.Version != j.Version || res.ID != j.ResourceID {
			return fail(kindFatal, "resolved resource does not match the queued version")
		}
		for _, f := range res.Files {
			if f.Size <= 0 && !hasHash(f) && res.ProviderRef["method"] == "" {
				// Without a size or hash nothing can be verified; accept
				// only with a warning recorded at install.
				m.log.Warn("file has no published size or hash", "file", f.Name)
			}
		}
		j.Resource = res
		j.Resolved = true
		if err := m.checkSpace(ctx, j, res.DownloadSize); err != nil {
			return err
		}
	}
	j.State = store.JobRunning
	persist()

	ex, method := m.extractor(j.Resource)
	if method != "" && ex == nil {
		return fail(kindFatal, "this version of Arcana cannot install resources of type %q", method)
	}
	for j.FileIndex < len(j.Resource.Files) {
		f := j.Resource.Files[j.FileIndex]
		var done store.InstalledFile
		var err error
		if ex != nil {
			done, err = m.extractFile(ctx, j, rs, ex, persist)
		} else {
			done, err = m.transferFile(ctx, j, rs, f, persist)
		}
		if err != nil {
			return err
		}
		j.Done = append(j.Done, done)
		j.FileIndex++
		j.Offset = 0
		j.HashState = nil
		j.Validator = ""
		j.MirrorIndex = 0
		persist()
	}
	j.State = store.JobInstalling
	persist()
	return m.install(ctx, j, persist)
}

func (m *Manager) install(ctx context.Context, j *store.Job, persist func()) error {
	var staged []content.StagedFile
	verification := "sha256 computed during download"
	unverified := false
	for i, f := range j.Resource.Files {
		if i >= len(j.Done) {
			return fail(kindFatal, "installation state is inconsistent; please download again")
		}
		staged = append(staged, content.StagedFile{
			Path:   m.partPath(j.ID, f.Name),
			Name:   f.Name,
			Size:   j.Done[i].Size,
			SHA256: j.Done[i].SHA256,
		})
		algo, _ := verifyAlgo(f)
		if ref := j.Resource.ProviderRef; ref["via"] == "peer" {
			verification = "sha256 matched the value recorded by the library it was copied from (" + ref["peerName"] + ")"
			if ref["peerUnverified"] == "true" {
				unverified = true
				verification += "; that library had no publisher checksum either"
			}
			continue
		}
		if j.Resource.ProviderRef["method"] != "" {
			unverified = true
			verification = "built by Arcana from a single version of the source (" + hostOf(f.Mirrors[0].URL) +
				"); structure checked; no publisher checksum exists, SHA-256 recorded for future integrity checks"
			continue
		}
		switch algo {
		case "sha256", "sha512":
			verification = fmt.Sprintf("%s matched the value published by %s", algo, j.Resource.Provider)
		case "sha1", "md5":
			verification = fmt.Sprintf("%s matched the value published by %s (weak hash)", algo, j.Resource.Provider)
		default:
			unverified = true
			verification = "source published no checksum; SHA-256 recorded for future integrity checks"
		}
	}
	rec, err := m.opts.Content.Publish(context.WithoutCancel(ctx), j.Resource, staged, verification, unverified)
	if err != nil {
		return fail(kindFatal, "install: %v", err)
	}
	j.State = store.JobCompleted
	j.Error = ""
	persist()
	m.removeStaging(j.ID)
	m.log.Info("installed", "resource", rec.ResourceID, "version", rec.Version, "bytes", rec.TotalSize())
	if m.OnInstalled != nil {
		m.OnInstalled(rec)
	}
	return nil
}

func (m *Manager) checkSpace(ctx context.Context, j *store.Job, need int64) error {
	vol, err := m.opts.Volume()
	if err != nil {
		return fail(kindFatal, "cannot determine free space: %v", err)
	}
	jobs, err := m.opts.Store.ListJobs(ctx)
	if err != nil {
		return err
	}
	var others []*store.Job
	for _, o := range jobs {
		if o.ID != j.ID {
			others = append(others, o)
		}
	}
	remaining := remainingBytes(j, -1)
	if need < remaining {
		remaining = need
	}
	if err := storage.CheckWrite(vol.AvailBytes, remaining, m.promisedBytes(others), m.reserve(vol)); err != nil {
		return &jobError{kind: kindSpace, err: err}
	}
	return nil
}

// ---------------------------------------------------------------- transfer

func (m *Manager) stagingDir(jobID string) string {
	return filepath.Join(m.opts.Layout.Staging(), jobID)
}

func (m *Manager) partPath(jobID, name string) string {
	return filepath.Join(m.stagingDir(jobID), name+".part")
}

func (m *Manager) removeStaging(jobID string) {
	if jobID == "" || strings.ContainsAny(jobID, `/\.`) {
		return
	}
	_ = os.RemoveAll(m.stagingDir(jobID))
}

func hasHash(f catalog.File) bool {
	algo, _ := verifyAlgo(f)
	return algo != ""
}

// verifyAlgo picks the strongest published hash.
func verifyAlgo(f catalog.File) (string, string) {
	for _, a := range []string{"sha256", "sha512", "sha1", "md5"} {
		if v := f.Hashes[a]; v != "" {
			return a, v
		}
	}
	return "", ""
}

func newHash(algo string) hash.Hash {
	switch algo {
	case "sha256":
		return sha256.New()
	case "sha512":
		return sha512.New()
	case "sha1":
		return sha1.New()
	case "md5":
		return md5.New()
	}
	return nil
}

// hashSet always computes SHA-256 (recorded for future integrity checks)
// plus the publisher's algorithm if different.
type hashSet map[string]hash.Hash

func newHashSet(verify string) hashSet {
	hs := hashSet{"sha256": sha256.New()}
	if verify != "" && verify != "sha256" {
		hs[verify] = newHash(verify)
	}
	return hs
}

func (hs hashSet) Write(p []byte) {
	for _, h := range hs {
		h.Write(p)
	}
}

func (hs hashSet) marshal() (map[string][]byte, error) {
	out := map[string][]byte{}
	for k, h := range hs {
		bm, ok := h.(encoding.BinaryMarshaler)
		if !ok {
			return nil, errors.New("hash state cannot be saved")
		}
		b, err := bm.MarshalBinary()
		if err != nil {
			return nil, err
		}
		out[k] = b
	}
	return out, nil
}

func (hs hashSet) restore(state map[string][]byte) error {
	for k, h := range hs {
		b, ok := state[k]
		if !ok {
			return fmt.Errorf("missing %s state", k)
		}
		if err := h.(encoding.BinaryUnmarshaler).UnmarshalBinary(b); err != nil {
			return err
		}
	}
	return nil
}

// transferFile downloads one file into staging, resuming from the job's
// checkpoint, and verifies it.
func (m *Manager) transferFile(ctx context.Context, j *store.Job, rs *runState, f catalog.File, persist func()) (store.InstalledFile, error) {
	if err := os.MkdirAll(m.stagingDir(j.ID), 0o755); err != nil {
		return store.InstalledFile{}, fail(kindFatal, "create staging directory: %v", err)
	}
	partPath := m.partPath(j.ID, f.Name)
	fh, err := os.OpenFile(partPath, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return store.InstalledFile{}, fail(kindFatal, "open staging file: %v", err)
	}
	defer fh.Close()

	algo, want := verifyAlgo(f)
	hs := newHashSet(algo)
	offset := j.Offset
	if offset > 0 {
		if err := hs.restore(j.HashState); err != nil {
			m.log.Warn("checkpoint unusable; restarting file", "err", err)
			offset = 0
			hs = newHashSet(algo)
		}
	}
	if offset == 0 {
		j.Validator = ""
	}
	// Bytes beyond the checkpoint were written but never made durable
	// together with the hash state; discard them.
	if err := fh.Truncate(offset); err != nil {
		return store.InstalledFile{}, fail(kindFatal, "truncate staging file: %v", err)
	}
	if _, err := fh.Seek(offset, io.SeekStart); err != nil {
		return store.InstalledFile{}, fail(kindFatal, "seek: %v", err)
	}
	j.Offset = offset

	var baseDone int64
	for _, d := range j.Done {
		baseDone += d.Size
	}
	rs.bytesDone.Store(baseDone + offset)

	complete := f.Size > 0 && offset == f.Size
	if !complete && m.useSegments(j, f) {
		err := m.fetchSegmented(ctx, j, rs, f, fh, hs, baseDone, persist)
		switch {
		case err == nil:
			complete = true
		case errors.Is(err, errNoRanges):
			m.log.Info("mirrors do not serve byte ranges; downloading over one connection", "file", f.Name)
		default:
			return store.InstalledFile{}, err
		}
	}
	var lastErr error
	if !complete {
		mirrors := f.Mirrors
		if len(mirrors) == 0 {
			return store.InstalledFile{}, fail(kindFatal, "no download location for %s", f.Name)
		}
		bad := map[string]bool{}
		for _, b := range j.BadMirrors {
			bad[b] = true
		}
		tried := 0
		for i := 0; i < len(mirrors) && !complete; i++ {
			idx := (j.MirrorIndex + i) % len(mirrors)
			mirror := mirrors[idx]
			if bad[mirror.URL] {
				continue
			}
			tried++
			rs.mirror.Store(hostOf(mirror.URL))
			err := m.fetch(ctx, j, rs, f, fh, hs, mirror.URL, baseDone, persist)
			if err == nil {
				j.MirrorIndex = idx
				complete = true
				break
			}
			if ctx.Err() != nil {
				m.checkpoint(fh, j, hs, persist)
				return store.InstalledFile{}, err
			}
			var je *jobError
			if errors.As(err, &je) && je.kind == kindFatal {
				// This mirror cannot serve the file; try the next one.
				j.BadMirrors = append(j.BadMirrors, mirror.URL)
				bad[mirror.URL] = true
				m.log.Warn("mirror rejected", "mirror", hostOf(mirror.URL), "err", err)
				lastErr = err
				continue
			}
			if errors.As(err, &je) && je.kind == kindSpace {
				m.checkpoint(fh, j, hs, persist)
				return store.InstalledFile{}, err
			}
			// Transient: keep progress, remember the mirror, try the next.
			m.checkpoint(fh, j, hs, persist)
			j.MirrorIndex = (idx + 1) % len(mirrors)
			lastErr = err
		}
		if !complete {
			if tried == 0 || len(j.BadMirrors) >= len(mirrors) {
				return store.InstalledFile{}, fail(kindFatal, "no mirror could provide %s: %v", f.Name, lastErr)
			}
			return store.InstalledFile{}, lastErr
		}
	}

	if err := fh.Sync(); err != nil {
		return store.InstalledFile{}, fail(kindNetwork, "flush to drive: %v", err)
	}
	size, err := fh.Seek(0, io.SeekEnd)
	if err != nil {
		return store.InstalledFile{}, fail(kindFatal, "size: %v", err)
	}
	j.State = store.JobVerifying
	if f.Size > 0 && size != f.Size {
		m.resetFile(fh, j)
		return store.InstalledFile{}, fail(kindIntegrity, "%s is %d bytes, expected %d", f.Name, size, f.Size)
	}
	got := map[string]string{}
	for k, h := range hs {
		got[k] = hex.EncodeToString(h.Sum(nil))
	}
	if algo != "" && got[algo] != want {
		if m.useSegments(j, f) {
			j.MixedIntegrityFailure = true
		}
		m.resetFile(fh, j)
		return store.InstalledFile{}, fail(kindIntegrity,
			"%s failed verification: %s is %s, publisher states %s", f.Name, algo, got[algo], want)
	}
	return store.InstalledFile{Path: path.Join(j.Version, f.Name), Size: size, SHA256: got["sha256"]}, nil
}

// resetFile discards a file that failed verification and moves to the next
// mirror for the retry.
func (m *Manager) resetFile(fh *os.File, j *store.Job) {
	_ = fh.Truncate(0)
	j.Offset = 0
	j.HashState = nil
	j.Validator = ""
	j.MirrorIndex++
}

// checkpoint makes the current offset durable: data first, then metadata.
func (m *Manager) checkpoint(fh *os.File, j *store.Job, hs hashSet, persist func()) {
	pos, err := fh.Seek(0, io.SeekCurrent)
	if err != nil {
		return
	}
	if err := fh.Sync(); err != nil {
		m.log.Warn("sync staging file", "err", err)
		return
	}
	state, err := hs.marshal()
	if err != nil {
		return
	}
	j.Offset = pos
	j.HashState = state
	persist()
}

// fetch performs one HTTP request against one mirror and appends the body
// to fh.
func (m *Manager) fetch(ctx context.Context, j *store.Job, rs *runState, f catalog.File, fh *os.File,
	hs hashSet, url string, baseDone int64, persist func()) error {

	reqCtx, cancelReq := context.WithCancel(ctx)
	defer cancelReq()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return fail(kindFatal, "bad URL: %v", err)
	}
	req.Header.Set("User-Agent", catalog.UserAgent())
	if m.opts.RequestHook != nil {
		m.opts.RequestHook(req)
	}
	// Identity encoding: transparent compression would make byte offsets
	// meaningless for range requests.
	req.Header.Set("Accept-Encoding", "identity")
	offset := j.Offset
	_, publisherHash := verifyAlgo(f)
	if offset > 0 {
		switch {
		case publisherHash != "":
			// The final hash check proves the assembled file is correct,
			// so resuming from any mirror is safe. If-Range is not sent
			// because ETags differ between mirrors of the same file.
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		case j.Validator != "":
			// No published hash: only resume if the server confirms it is
			// still the same file.
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
			req.Header.Set("If-Range", j.Validator)
		default:
			// Nothing can prove the partial data matches the server's
			// file: restarting is the only safe option.
			offset = 0
		}
	}
	if offset == 0 && j.Offset != 0 {
		m.restartFile(fh, j, hs)
	}

	resp, err := m.opts.Client.Do(req)
	if err != nil {
		return classifyNetErr(err, "connect to "+hostOf(url))
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusPartialContent:
		start, total, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || start != offset {
			return fail(kindServer, "server returned an unexpected range (%q)", resp.Header.Get("Content-Range"))
		}
		if f.Size > 0 && total >= 0 && total != f.Size {
			return fail(kindFatal, "server file is %d bytes, catalog states %d", total, f.Size)
		}
	case http.StatusOK:
		if offset > 0 {
			// Range ignored or the file changed (If-Range failed): the
			// server is sending the whole file, so start over.
			m.log.Info("server cannot resume; restarting file", "mirror", hostOf(url))
			m.restartFile(fh, j, hs)
			offset = 0
		}
		if f.Size > 0 && resp.ContentLength >= 0 && resp.ContentLength != f.Size {
			return fail(kindFatal, "server file is %d bytes, catalog states %d", resp.ContentLength, f.Size)
		}
	case http.StatusRequestedRangeNotSatisfiable:
		m.restartFile(fh, j, hs)
		return fail(kindServer, "server rejected the resume request; restarting")
	case http.StatusNotFound, http.StatusGone, http.StatusForbidden, http.StatusUnauthorized:
		return fail(kindFatal, "%s: %s", hostOf(url), resp.Status)
	case http.StatusTooManyRequests:
		return fail(kindServer, "%s is rate limiting downloads", hostOf(url))
	default:
		if resp.StatusCode >= 500 {
			return fail(kindServer, "%s: %s", hostOf(url), resp.Status)
		}
		return fail(kindFatal, "%s: unexpected %s", hostOf(url), resp.Status)
	}

	if v := resp.Header.Get("ETag"); v != "" && !strings.HasPrefix(v, "W/") {
		j.Validator = v
	} else if v := resp.Header.Get("Last-Modified"); v != "" {
		j.Validator = v
	}

	// Stall watchdog: cancel the request if no data arrives for a while.
	stall := time.AfterFunc(m.opts.StallTimeout, cancelReq)
	defer stall.Stop()

	buf := make([]byte, 256<<10)
	written := offset
	lastCheckpointBytes := written
	lastCheckpoint := time.Now()
	lastSample := time.Now()
	lastSampleBytes := written
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			stall.Reset(m.opts.StallTimeout)
			if f.Size > 0 && written+int64(n) > f.Size {
				return fail(kindFatal, "server sent more data than expected")
			}
			if err := m.throttle(ctx, n); err != nil {
				return err
			}
			if _, err := fh.Write(buf[:n]); err != nil {
				if isNoSpace(err) {
					return &jobError{kind: kindSpace, err: errors.New("the drive is full")}
				}
				return fail(kindFatal, "write to drive: %v", err)
			}
			hs.Write(buf[:n])
			written += int64(n)
			rs.bytesDone.Store(baseDone + written)

			if now := time.Now(); now.Sub(lastSample) >= time.Second {
				inst := float64(written-lastSampleBytes) / now.Sub(lastSample).Seconds()
				prev := float64(rs.speed.Load())
				if prev == 0 {
					prev = inst
				}
				rs.speed.Store(int64(0.7*prev + 0.3*inst))
				lastSample, lastSampleBytes = now, written
			}
			if written-lastCheckpointBytes >= m.opts.CheckpointBytes ||
				(time.Since(lastCheckpoint) >= m.opts.CheckpointInterval && written > lastCheckpointBytes) {
				m.checkpoint(fh, j, hs, persist)
				lastCheckpointBytes, lastCheckpoint = written, time.Now()
				if err := m.checkSpace(ctx, j, remainingBytes(j, -1)); err != nil {
					return err
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if reqCtx.Err() != nil {
				return fail(kindNetwork, "no data received for %s", m.opts.StallTimeout)
			}
			return classifyNetErr(rerr, "download interrupted")
		}
	}
	if f.Size > 0 && written < f.Size {
		return fail(kindNetwork, "connection closed early (%d of %d bytes)", written, f.Size)
	}
	if resp.ContentLength >= 0 && written-offset != resp.ContentLength {
		return fail(kindNetwork, "connection closed early")
	}
	return nil
}

// throttle waits for bandwidth tokens in burst-sized chunks.
func (m *Manager) throttle(ctx context.Context, n int) error {
	for n > 0 {
		chunk := n
		if b := m.limiter.Burst(); chunk > b && b > 0 {
			chunk = b
		}
		if err := m.limiter.WaitN(ctx, chunk); err != nil {
			return err
		}
		n -= chunk
	}
	return nil
}

func (m *Manager) restartFile(fh *os.File, j *store.Job, hs hashSet) {
	_ = fh.Truncate(0)
	_, _ = fh.Seek(0, io.SeekStart)
	for k := range hs {
		hs[k].Reset()
	}
	j.Offset = 0
	j.HashState = nil
	j.Validator = ""
}

// parseContentRange parses "bytes start-end/total".
func parseContentRange(h string) (start, total int64, ok bool) {
	h = strings.TrimSpace(h)
	if !strings.HasPrefix(h, "bytes ") {
		return 0, 0, false
	}
	rangePart, totalPart, found := strings.Cut(strings.TrimPrefix(h, "bytes "), "/")
	if !found {
		return 0, 0, false
	}
	s, _, found := strings.Cut(rangePart, "-")
	if !found {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, 0, false
	}
	total = -1
	if totalPart != "*" {
		total, err = strconv.ParseInt(totalPart, 10, 64)
		if err != nil {
			return 0, 0, false
		}
	}
	return start, total, true
}

func classifyNetErr(err error, what string) error {
	var je *jobError
	if errors.As(err, &je) {
		return err
	}
	if errors.Is(err, errBlockedAddress) {
		return &jobError{kind: kindFatal, err: fmt.Errorf("%s: %w", what, err)}
	}
	var ne net.Error
	var dnsErr *net.DNSError
	var opErr *net.OpError
	switch {
	case errors.As(err, &dnsErr), errors.As(err, &opErr), errors.As(err, &ne),
		errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, syscall.ECONNRESET):
		return &jobError{kind: kindNetwork, err: fmt.Errorf("%s: %v", what, err)}
	}
	if strings.Contains(err.Error(), ": 5") || strings.Contains(err.Error(), "404") {
		return &jobError{kind: kindServer, err: fmt.Errorf("%s: %v", what, err)}
	}
	return &jobError{kind: kindNetwork, err: fmt.Errorf("%s: %v", what, err)}
}

func isNoSpace(err error) bool {
	return errors.Is(err, syscall.ENOSPC) || strings.Contains(strings.ToLower(err.Error()), "not enough space") ||
		strings.Contains(strings.ToLower(err.Error()), "disk full")
}

func hostOf(u string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

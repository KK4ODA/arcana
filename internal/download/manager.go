// Package download implements Arcana's persistent download queue.
//
// Design goals, in order: never publish a file that failed verification;
// never lose more than one checkpoint of progress to a crash, power loss or
// network failure; never fill the drive past the user's reserve.
//
// Each job downloads into staging/<job-id>/<file>.part. Every
// CheckpointBytes the file is fsynced and the byte offset, together with the
// serialized state of the running hash, is committed to the database. On
// restart the partial file is truncated back to the last checkpoint, the hash
// state is restored, and the transfer resumes with an HTTP range request
// guarded by If-Range, so the partial file never needs to be re-read.
package download

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/content"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/storage"
	"github.com/KK4ODA/arcana/internal/store"
)

// Options configure a Manager.
type Options struct {
	Store   *store.Store
	Layout  library.Layout
	Content *content.Manager
	// Settings returns the current library settings (reserve, concurrency,
	// bandwidth, private-network policy).
	Settings func() library.Settings
	// Resolvers refine resources before download, keyed by provider ID.
	Resolvers map[string]catalog.Resolver
	// ResolverFor, when set, is consulted instead of Resolvers so that
	// providers enabled after startup are honored.
	ResolverFor func(provider string) catalog.Resolver
	// Fetcher is used by resolvers. Defaults to one using Client.
	Fetcher catalog.Fetcher
	// Client performs transfers. Defaults to NewClient(settings).
	Client *http.Client
	// Volume reports free space on the library drive.
	Volume func() (storage.Volume, error)
	Log    *slog.Logger

	CheckpointBytes    int64
	CheckpointInterval time.Duration
	RetryBase          time.Duration
	RetryMax           time.Duration
	// MaxHTTPAttempts bounds retries for server errors. Network outages
	// (no route, DNS failure, timeouts) are retried indefinitely with capped
	// backoff, so a queue resumes by itself when connectivity returns.
	MaxHTTPAttempts int
	// StallTimeout aborts a transfer that receives no data for this long.
	StallTimeout time.Duration
	// ReadOnly disables all writes.
	ReadOnly bool
}

// Manager runs the download queue.
type Manager struct {
	opts    Options
	log     *slog.Logger
	limiter *rate.Limiter

	mu      sync.Mutex
	running map[string]*runState
	wake    chan struct{}
	wg      sync.WaitGroup
	cancel  context.CancelFunc
	started bool
	// OnInstalled is called after a job publishes its resource.
	OnInstalled func(store.Installed)
}

type runState struct {
	cancel    context.CancelCauseFunc
	bytesDone atomic.Int64 // bytes of the whole job written so far
	speed     atomic.Int64 // bytes per second (smoothed)
	mirror    atomic.Value // string
}

var (
	errPaused    = errors.New("paused")
	errCancelled = errors.New("cancelled")
	errShutdown  = errors.New("shutting down")
)

// New creates a Manager. Call Start to begin processing.
func New(opts Options) *Manager {
	if opts.CheckpointBytes <= 0 {
		opts.CheckpointBytes = 16 << 20
	}
	if opts.CheckpointInterval <= 0 {
		opts.CheckpointInterval = 10 * time.Second
	}
	if opts.RetryBase <= 0 {
		opts.RetryBase = 2 * time.Second
	}
	if opts.RetryMax <= 0 {
		opts.RetryMax = 5 * time.Minute
	}
	if opts.MaxHTTPAttempts <= 0 {
		opts.MaxHTTPAttempts = 8
	}
	if opts.StallTimeout <= 0 {
		opts.StallTimeout = 90 * time.Second
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.Volume == nil {
		root := opts.Layout.Root
		opts.Volume = func() (storage.Volume, error) { return storage.Stat(root) }
	}
	if opts.Settings == nil {
		def := library.DefaultSettings()
		opts.Settings = func() library.Settings { return def }
	}
	if opts.Client == nil {
		opts.Client = NewClient(func() bool { return opts.Settings().AllowPrivateNetworkDownloads })
	}
	if opts.Fetcher == nil {
		opts.Fetcher = catalog.NewHTTPFetcher(opts.Client)
	}
	m := &Manager{
		opts:    opts,
		log:     opts.Log,
		running: map[string]*runState{},
		wake:    make(chan struct{}, 1),
		limiter: rate.NewLimiter(rate.Inf, 256<<10),
	}
	m.applyBandwidth(opts.Settings().BandwidthLimit)
	return m
}

// ApplySettings re-reads bandwidth and concurrency settings.
func (m *Manager) ApplySettings() {
	m.applyBandwidth(m.opts.Settings().BandwidthLimit)
	m.poke()
}

func (m *Manager) applyBandwidth(bps int64) {
	if bps <= 0 {
		m.limiter.SetLimit(rate.Inf)
		return
	}
	m.limiter.SetLimit(rate.Limit(bps))
	burst := int(bps)
	if burst > 256<<10 {
		burst = 256 << 10
	}
	if burst < 16<<10 {
		burst = 16 << 10
	}
	m.limiter.SetBurst(burst)
}

// Start recovers interrupted jobs and begins processing the queue.
func (m *Manager) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	m.cancel = cancel
	m.started = true
	m.mu.Unlock()

	jobs, err := m.opts.Store.ListJobs(ctx)
	if err != nil {
		cancel()
		return err
	}
	active := map[string]bool{}
	for _, j := range jobs {
		switch j.State {
		case store.JobResolving, store.JobRunning, store.JobVerifying:
			// Interrupted mid-transfer: requeue. The transfer resumes from
			// the last durable checkpoint.
			j.State = store.JobQueued
			if err := m.opts.Store.PutJob(ctx, j); err != nil {
				cancel()
				return err
			}
		}
		if j.Active() {
			active[j.ID] = true
		}
	}
	if m.opts.Content != nil && !m.opts.ReadOnly {
		m.opts.Content.CollectGarbage(active)
	}
	_ = m.opts.Store.PruneJobs(ctx, 30*24*time.Hour)

	m.wg.Add(1)
	go m.loop(ctx)
	return nil
}

// Stop pauses running transfers at a checkpoint and waits for them.
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.cancel != nil {
		for _, rs := range m.running {
			rs.cancel(errShutdown)
		}
		m.cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func (m *Manager) poke() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) loop(ctx context.Context) {
	defer m.wg.Done()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		m.schedule(ctx)
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
		case <-tick.C:
		}
	}
}

// schedule starts eligible jobs up to the concurrency limit.
func (m *Manager) schedule(ctx context.Context) {
	if m.opts.ReadOnly || ctx.Err() != nil {
		return
	}
	limit := m.opts.Settings().MaxConcurrentDownloads
	if limit <= 0 {
		limit = 2
	}
	jobs, err := m.opts.Store.ListJobs(ctx)
	if err != nil {
		if ctx.Err() == nil {
			m.log.Error("list jobs", "err", err)
		}
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for _, j := range jobs {
		if len(m.running) >= limit {
			return
		}
		if _, busy := m.running[j.ID]; busy {
			continue
		}
		eligible := j.State == store.JobQueued || j.State == store.JobInstalling ||
			(j.State == store.JobWaiting && !now.Before(j.NextAttempt))
		if !eligible {
			continue
		}
		jctx, cancel := context.WithCancelCause(ctx)
		rs := &runState{cancel: cancel}
		rs.mirror.Store("")
		m.running[j.ID] = rs
		m.wg.Add(1)
		go func(j *store.Job) {
			defer m.wg.Done()
			m.run(jctx, j, rs)
			cancel(nil)
			m.mu.Lock()
			delete(m.running, j.ID)
			m.mu.Unlock()
			m.poke()
		}(j)
	}
}

// ---------------------------------------------------------------- API

// ErrAlreadyQueued is returned when a resource already has an active job.
var ErrAlreadyQueued = errors.New("this resource is already in the download queue")

// ErrAlreadyInstalled is returned when the same version is installed.
var ErrAlreadyInstalled = errors.New("this version is already installed")

// Enqueue validates a catalog resource and adds it to the queue.
func (m *Manager) Enqueue(ctx context.Context, res catalog.Resource) (*store.Job, error) {
	if m.opts.ReadOnly {
		return nil, library.ErrReadOnly
	}
	if err := res.Validate(); err != nil {
		return nil, err
	}
	jobs, err := m.opts.Store.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if j.ResourceID == res.ID && j.Active() {
			return nil, ErrAlreadyQueued
		}
	}
	replaces := ""
	if in, err := m.opts.Store.GetInstalled(ctx, res.ID); err == nil {
		if in.Version == res.Version {
			return nil, ErrAlreadyInstalled
		}
		replaces = in.Version
	}
	vol, err := m.opts.Volume()
	if err != nil {
		return nil, fmt.Errorf("cannot determine free space: %w", err)
	}
	if vol.ReadOnly {
		return nil, library.ErrReadOnly
	}
	for _, f := range res.Files {
		size := f.Size
		if size == 0 {
			size = res.DownloadSize
		}
		if ok, why := storage.CanStore(vol.FSType, size); !ok {
			return nil, errors.New(why)
		}
	}
	promised := m.promisedBytes(jobs)
	if err := storage.CheckWrite(vol.AvailBytes, res.DownloadSize, promised, m.reserve(vol)); err != nil {
		return nil, err
	}
	j := &store.Job{
		ID:              newJobID(),
		ResourceID:      res.ID,
		Version:         res.Version,
		State:           store.JobQueued,
		Resource:        res,
		ReplacesVersion: replaces,
	}
	if err := m.opts.Store.PutJob(ctx, j); err != nil {
		return nil, err
	}
	m.log.Info("queued", "job", j.ID, "resource", res.ID, "version", res.Version, "bytes", res.DownloadSize)
	m.poke()
	return j, nil
}

// Reserve returns the effective free-space reserve for vol.
func (m *Manager) Reserve(vol storage.Volume) int64 { return m.reserve(vol) }

func (m *Manager) reserve(vol storage.Volume) int64 {
	r := m.opts.Settings().ReserveBytes
	if r <= 0 {
		r = library.DefaultReserve(int64(vol.TotalBytes))
	}
	return r
}

// promisedBytes is what active jobs still need to write.
func (m *Manager) promisedBytes(jobs []*store.Job) int64 {
	var total int64
	for _, j := range jobs {
		if !j.Active() {
			continue
		}
		total += remainingBytes(j, m.liveDone(j.ID))
	}
	return total
}

func remainingBytes(j *store.Job, live int64) int64 {
	done := live
	if done < 0 {
		done = j.Offset
		for _, f := range j.Done {
			done += f.Size
		}
	}
	r := j.Resource.DownloadSize - done
	if r < 0 {
		return 0
	}
	return r
}

func (m *Manager) liveDone(id string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rs, ok := m.running[id]; ok {
		return rs.bytesDone.Load()
	}
	return -1
}

// Pause stops a job at its next checkpoint. Paused jobs keep their partial
// data.
func (m *Manager) Pause(ctx context.Context, id string) error {
	return m.control(ctx, id, errPaused, func(j *store.Job) error {
		if !j.Active() {
			return fmt.Errorf("job is %s", j.State)
		}
		j.State = store.JobPaused
		return nil
	})
}

// Resume requeues a paused, waiting or failed job.
func (m *Manager) Resume(ctx context.Context, id string) error {
	err := m.control(ctx, id, nil, func(j *store.Job) error {
		switch j.State {
		case store.JobPaused, store.JobWaiting, store.JobFailed:
			j.State = store.JobQueued
			j.Error = ""
			j.Attempts = 0
			j.NextAttempt = time.Time{}
			return nil
		}
		return fmt.Errorf("job is %s", j.State)
	})
	m.poke()
	return err
}

// Cancel stops a job and deletes its partial data. Installed content is not
// affected.
func (m *Manager) Cancel(ctx context.Context, id string) error {
	err := m.control(ctx, id, errCancelled, func(j *store.Job) error {
		if j.State == store.JobCompleted || j.State == store.JobInstalling {
			return fmt.Errorf("job is %s", j.State)
		}
		j.State = store.JobCancelled
		return nil
	})
	if err == nil {
		m.removeStaging(id)
	}
	return err
}

// Remove deletes a finished job record from the history.
func (m *Manager) Remove(ctx context.Context, id string) error {
	j, err := m.opts.Store.GetJob(ctx, id)
	if err != nil {
		return err
	}
	if j.Active() {
		return errors.New("cancel the download before removing it")
	}
	m.removeStaging(id)
	return m.opts.Store.DeleteJob(ctx, id)
}

// control applies a state change; running jobs are interrupted with cause
// and record the state themselves.
func (m *Manager) control(ctx context.Context, id string, cause error, mutate func(*store.Job) error) error {
	if m.opts.ReadOnly {
		return library.ErrReadOnly
	}
	m.mu.Lock()
	rs, running := m.running[id]
	m.mu.Unlock()
	if running && cause != nil {
		rs.cancel(cause)
		return nil
	}
	j, err := m.opts.Store.GetJob(ctx, id)
	if err != nil {
		return err
	}
	if err := mutate(j); err != nil {
		return err
	}
	return m.opts.Store.PutJob(ctx, j)
}

// View is the public representation of a job.
type View struct {
	ID          string     `json:"id"`
	ResourceID  string     `json:"resourceId"`
	Title       string     `json:"title"`
	Version     string     `json:"version"`
	Replaces    string     `json:"replaces,omitempty"`
	State       string     `json:"state"`
	Error       string     `json:"error,omitempty"`
	BytesDone   int64      `json:"bytesDone"`
	BytesTotal  int64      `json:"bytesTotal"`
	Speed       int64      `json:"speed"`
	ETASeconds  int64      `json:"etaSeconds"`
	Attempts    int        `json:"attempts"`
	NextAttempt *time.Time `json:"nextAttempt,omitempty"`
	Mirror      string     `json:"mirror,omitempty"`
	Unverified  bool       `json:"unverified"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

// List returns all jobs, newest active first.
func (m *Manager) List(ctx context.Context) ([]View, error) {
	jobs, err := m.opts.Store.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(jobs))
	for _, j := range jobs {
		v := View{
			ID: j.ID, ResourceID: j.ResourceID, Title: j.Resource.Title, Version: j.Version,
			Replaces: j.ReplacesVersion, State: j.State, Error: j.Error, Attempts: j.Attempts,
			BytesTotal: j.Resource.DownloadSize, Unverified: j.Resource.Unverifiable(),
			CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt,
		}
		if !j.NextAttempt.IsZero() && j.State == store.JobWaiting {
			t := j.NextAttempt
			v.NextAttempt = &t
		}
		v.BytesDone = j.Offset
		for _, f := range j.Done {
			v.BytesDone += f.Size
		}
		m.mu.Lock()
		if rs, ok := m.running[j.ID]; ok {
			v.BytesDone = rs.bytesDone.Load()
			v.Speed = rs.speed.Load()
			v.Mirror, _ = rs.mirror.Load().(string)
		}
		m.mu.Unlock()
		if j.State == store.JobCompleted {
			v.BytesDone = v.BytesTotal
		}
		if v.Speed > 0 && v.BytesTotal > v.BytesDone {
			v.ETASeconds = (v.BytesTotal - v.BytesDone) / v.Speed
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, k int) bool {
		ai, ak := isActive(out[i].State), isActive(out[k].State)
		if ai != ak {
			return ai
		}
		if ai {
			return out[i].CreatedAt.Before(out[k].CreatedAt)
		}
		return out[i].UpdatedAt.After(out[k].UpdatedAt)
	})
	return out, nil
}

func isActive(state string) bool {
	return (&store.Job{State: state}).Active()
}

// Pending reports bytes still to be downloaded by active jobs.
func (m *Manager) Pending(ctx context.Context) (int64, error) {
	jobs, err := m.opts.Store.ListJobs(ctx)
	if err != nil {
		return 0, err
	}
	return m.promisedBytes(jobs), nil
}

func newJobID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:4])
}

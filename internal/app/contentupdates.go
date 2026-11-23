package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/KK4ODA/arcana/internal/fsutil"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/store"
)

// Scheduled content updates: off by default. While on, and only inside the
// owner's time window, Arcana refreshes the catalog at the chosen interval,
// queues newer editions of installed resources that fit on the drive
// (medical and regulatory material first), caps the bandwidth those
// downloads use, and pauses them when the window closes. As with any
// update, the installed edition is replaced only once the new one is
// verified.

// ContentUpdateEvent is one entry of the schedule's log.
type ContentUpdateEvent struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
	Queued  []string  `json:"queued,omitempty"`
	Skipped []string  `json:"skipped,omitempty"`
}

// ContentUpdateState is kept in state/content-updates.json.
type ContentUpdateState struct {
	LastCheck   time.Time `json:"lastCheck"`
	LastAttempt time.Time `json:"lastAttempt"`
	// Jobs are downloads the schedule queued (paused outside the window).
	Jobs []string             `json:"jobs"`
	Log  []ContentUpdateEvent `json:"log"`
}

type contentUpdater struct {
	mu     sync.Mutex
	state  ContentUpdateState
	loaded bool
	// active is set while scheduled downloads run inside the window.
	active atomic.Bool
	now    func() time.Time
}

func (a *App) contentStatePath() string {
	return filepath.Join(a.Lib.Layout.State(), "content-updates.json")
}

func (a *App) contentState() ContentUpdateState {
	cu := &a.contentUpd
	cu.mu.Lock()
	defer cu.mu.Unlock()
	a.loadContentStateLocked()
	st := cu.state
	st.Jobs = append([]string(nil), st.Jobs...)
	st.Log = append([]ContentUpdateEvent(nil), st.Log...)
	return st
}

func (a *App) loadContentStateLocked() {
	cu := &a.contentUpd
	if cu.loaded {
		return
	}
	cu.loaded = true
	if b, err := os.ReadFile(a.contentStatePath()); err == nil {
		_ = json.Unmarshal(b, &cu.state)
	}
}

func (a *App) saveContentStateLocked() {
	if a.Lib.ReadOnly {
		return
	}
	b, err := json.MarshalIndent(a.contentUpd.state, "", "  ")
	if err == nil {
		err = fsutil.WriteFileAtomic(a.contentStatePath(), b, 0o644)
	}
	if err != nil {
		a.Log.Warn("save content update state", "err", err)
	}
}

func (a *App) logContentUpdateLocked(ev ContentUpdateEvent) {
	st := &a.contentUpd.state
	st.Log = append([]ContentUpdateEvent{ev}, st.Log...)
	if len(st.Log) > 30 {
		st.Log = st.Log[:30]
	}
	a.Log.Info("scheduled content update", "message", ev.Message, "queued", len(ev.Queued))
}

func (a *App) clock() time.Time {
	if a.contentUpd.now != nil {
		return a.contentUpd.now()
	}
	return time.Now()
}

// InWindow reports whether t (local time) falls in [start, end) hours; a
// window may wrap past midnight (22 to 6). Equal hours mean all day.
func InWindow(t time.Time, start, end int) bool {
	h := t.Hour()
	switch {
	case start == end:
		return true
	case start < end:
		return h >= start && h < end
	default:
		return h >= start || h < end
	}
}

// NextWindow returns when the window next opens (t itself if open now).
func NextWindow(t time.Time, start, end int) time.Time {
	if InWindow(t, start, end) {
		return t
	}
	n := time.Date(t.Year(), t.Month(), t.Day(), start, 0, 0, 0, t.Location())
	if !n.After(t) {
		n = n.Add(24 * time.Hour)
	}
	return n
}

// downloadSettings are the settings the download manager uses: the
// scheduled-update bandwidth cap applies while scheduled downloads run.
func (a *App) downloadSettings() library.Settings {
	s := a.Lib.Settings.Get()
	if a.contentUpd.active.Load() {
		if c := s.ContentUpdates.BandwidthLimit; c > 0 && (s.BandwidthLimit <= 0 || c < s.BandwidthLimit) {
			s.BandwidthLimit = c
		}
	}
	return s
}

func (a *App) setScheduledActive(on bool) {
	if a.contentUpd.active.Swap(on) != on && a.Downloads != nil {
		a.Downloads.ApplySettings()
	}
}

func (a *App) contentUpdateLoop(ctx context.Context) {
	if a.Lib.ReadOnly {
		return
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		a.contentUpdateTick(ctx, false)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// RunContentUpdatesNow checks for updates and queues them at once,
// ignoring the window and interval (the "Check now" button).
func (a *App) RunContentUpdatesNow(ctx context.Context) ContentUpdateEvent {
	return a.contentUpdateTick(ctx, true)
}

func (a *App) contentUpdateTick(ctx context.Context, force bool) ContentUpdateEvent {
	s := a.Lib.Settings.Get().ContentUpdates
	cu := &a.contentUpd
	cu.mu.Lock()
	defer cu.mu.Unlock()
	a.loadContentStateLocked()
	now := a.clock()
	in := InWindow(now, s.WindowStart, s.WindowEnd)
	jobs := a.ourJobsLocked(ctx)

	if !s.Enabled && !force {
		a.setScheduledActive(false)
		return ContentUpdateEvent{}
	}
	// Outside the window our downloads wait; inside they continue.
	for _, j := range jobs {
		switch {
		case !in && !force && j.State != store.JobPaused:
			_ = a.Downloads.Pause(ctx, j.ID)
		case in && j.State == store.JobPaused:
			_ = a.Downloads.Resume(ctx, j.ID)
		}
	}
	a.setScheduledActive(in && len(jobs) > 0)
	if !force && !in {
		return ContentUpdateEvent{}
	}
	every := time.Duration(max(s.EveryDays, 1)) * 24 * time.Hour
	if !force && (now.Sub(cu.state.LastCheck) < every || now.Sub(cu.state.LastAttempt) < 30*time.Minute) {
		return ContentUpdateEvent{}
	}
	cu.state.LastAttempt = now
	ev := ContentUpdateEvent{Time: now}
	// The catalog is refreshed without holding the state lock for long
	// network waits would block status reads; the refresh has its own lock.
	cu.mu.Unlock()
	rctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	results, err := a.RefreshCatalog(rctx)
	cancel()
	cu.mu.Lock()
	reached := 0
	var failed []string
	for _, r := range results {
		if r.Error == "" && r.Provider != "arcana" {
			reached++
		} else if r.Error != "" {
			failed = append(failed, r.Name)
		}
	}
	if err != nil || reached == 0 {
		ev.Message = "The catalogs could not be reached (no internet connection?). Arcana will try again in 30 minutes while the window is open."
		a.logContentUpdateLocked(ev)
		a.saveContentStateLocked()
		return ev
	}
	cu.state.LastCheck = now
	ups, err := a.Updates(ctx)
	if err != nil {
		ev.Message = "Could not compare editions: " + err.Error()
		a.logContentUpdateLocked(ev)
		a.saveContentStateLocked()
		return ev
	}
	var cands []Update
	for _, u := range ups {
		if u.Queued || (s.Only == "important" && u.Priority != "high") {
			continue
		}
		cands = append(cands, u)
	}
	// Medical and regulatory first, then the smallest, so more updates fit.
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Priority != cands[j].Priority {
			return cands[i].Priority == "high"
		}
		return cands[i].DownloadSize < cands[j].DownloadSize
	})
	var chosen []string
	noSpace := 0
	for _, u := range cands {
		rep, err := a.Storage(ctx, append(append([]string(nil), chosen...), u.ResourceID))
		if err != nil || rep.Plan.Headroom < 0 {
			ev.Skipped = append(ev.Skipped, u.Title+" (not enough space)")
			noSpace++
			continue
		}
		res, err := a.Store.GetResource(ctx, u.ResourceID)
		if err != nil {
			continue
		}
		j, err := a.Downloads.Enqueue(ctx, res)
		if err != nil {
			ev.Skipped = append(ev.Skipped, u.Title+" ("+err.Error()+")")
			continue
		}
		chosen = append(chosen, u.ResourceID)
		ev.Queued = append(ev.Queued, u.Title)
		cu.state.Jobs = append(cu.state.Jobs, j.ID)
	}
	switch {
	case len(ev.Queued) > 0:
		ev.Message = fmt.Sprintf("Queued %d update(s).", len(ev.Queued))
	case len(cands) == 0:
		ev.Message = "Everything installed is up to date."
	case noSpace == len(ev.Skipped):
		ev.Message = "Updates are available but none fit in the free space above your reserve."
	default:
		ev.Message = "Updates are available but none could be queued."
	}
	if len(failed) > 0 {
		ev.Message += " Some catalogs could not be reached: " + strings.Join(failed, ", ") + "."
	}
	a.logContentUpdateLocked(ev)
	a.saveContentStateLocked()
	a.setScheduledActive((in || force) && len(ev.Queued) > 0)
	return ev
}

// ourJobsLocked returns the schedule's downloads that are still active and
// forgets finished ones.
func (a *App) ourJobsLocked(ctx context.Context) []store.Job {
	cu := &a.contentUpd
	if len(cu.state.Jobs) == 0 {
		return nil
	}
	all, err := a.Store.ListJobs(ctx)
	if err != nil {
		return nil
	}
	byID := map[string]store.Job{}
	for _, j := range all {
		byID[j.ID] = *j
	}
	var keep []string
	var out []store.Job
	for _, id := range cu.state.Jobs {
		if j, ok := byID[id]; ok && j.Active() {
			keep = append(keep, id)
			out = append(out, j)
		}
	}
	if len(keep) != len(cu.state.Jobs) {
		cu.state.Jobs = keep
		a.saveContentStateLocked()
	}
	return out
}

// ContentUpdateStatus is reported to the Updates view.
type ContentUpdateStatus struct {
	Settings   library.ContentUpdateSettings `json:"settings"`
	State      ContentUpdateState            `json:"state"`
	InWindow   bool                          `json:"inWindow"`
	NextWindow time.Time                     `json:"nextWindow"`
	NextCheck  time.Time                     `json:"nextCheck"`
	Active     bool                          `json:"active"`
}

// ContentUpdateStatus describes the schedule.
func (a *App) ContentUpdateStatus() ContentUpdateStatus {
	s := a.Lib.Settings.Get().ContentUpdates
	st := a.contentState()
	now := a.clock()
	next := st.LastCheck.Add(time.Duration(max(s.EveryDays, 1)) * 24 * time.Hour)
	if w := NextWindow(next, s.WindowStart, s.WindowEnd); w.After(next) {
		next = w
	}
	if next.Before(now) {
		next = NextWindow(now, s.WindowStart, s.WindowEnd)
	}
	if st.Jobs == nil {
		st.Jobs = []string{}
	}
	if st.Log == nil {
		st.Log = []ContentUpdateEvent{}
	}
	return ContentUpdateStatus{Settings: s, State: st, InWindow: InWindow(now, s.WindowStart, s.WindowEnd),
		NextWindow: NextWindow(now, s.WindowStart, s.WindowEnd), NextCheck: next, Active: a.contentUpd.active.Load()}
}

// ApplyContentUpdateSettings acts on changed settings at once.
func (a *App) ApplyContentUpdateSettings() {
	if a.Lib.ReadOnly || a.runCtx == nil {
		return
	}
	a.contentUpdateTick(a.runCtx, false)
}

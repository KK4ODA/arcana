package apps

import (
	"errors"
	"log/slog"
	"path/filepath"
	"sort"
	"sync"
)

// Installed is an app's resource as installed in the library.
type Installed struct {
	// ResourceID is "arcana.app.<id>".
	ResourceID string
	Version    string
	Files      []Packed
}

// Status describes one app for the interface.
type Status struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Kind    string `json:"kind"`
	Version string `json:"version"`
	// State is "preparing" (unpacking on this computer), "ready",
	// "unavailable" (no build for this computer, or a read-only library
	// for a program that keeps data) or "failed".
	State   string `json:"state"`
	Problem string `json:"problem,omitempty"`
	// Platforms the app's files cover; empty means any.
	Platforms     []string `json:"platforms"`
	SecureContext string   `json:"secureContext,omitempty"`
	Slot          int      `json:"-"`
}

// Manager prepares the installed apps on this computer and serves them.
type Manager struct {
	Log *slog.Logger
	// Cache is where apps unpack: a folder on this computer.
	Cache string
	// Platform is this computer's GOOS-GOARCH.
	Platform string
	// ReadOnly is set for a library Arcana may not write: programs that
	// keep data on the drive do not run.
	ReadOnly bool
	// Data returns the folder on the drive where an app keeps its data.
	Data func(id string) string

	mu     sync.Mutex
	apps   map[string]*entry
	closed bool
	// onChange is called, without the lock, after a sync changed the set
	// of installed apps.
	onChange func()
}

// SetOnChange sets the function called after the set of installed apps
// changed (the server opens and closes their ports).
func (m *Manager) SetOnChange(f func()) {
	m.mu.Lock()
	m.onChange = f
	m.mu.Unlock()
}

type entry struct {
	spec      *Spec
	version   string
	files     []Packed // the files this computer needs
	platforms []string
	dir       string // unpacked runtime, once ready
	key       string // identifies files, to notice new versions
	preparing bool
	problem   string
	state     string
}

// Sync matches the prepared apps to the installed resources: new and
// updated apps unpack in the background; removed apps stop.
func (m *Manager) Sync(installed []Installed) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	if m.apps == nil {
		m.apps = map[string]*entry{}
	}
	seen := map[string]bool{}
	changed := false
	var prepare []*entry
	for _, in := range installed {
		spec, ok := Lookup(in.ResourceID)
		if !ok {
			continue
		}
		seen[spec.ID] = true
		key := filesKey(in.Files)
		e := m.apps[spec.ID]
		if e != nil && e.key == key {
			continue
		}
		changed = true
		e = &entry{spec: spec, version: in.Version, key: key, platforms: Platforms(in.Files)}
		m.apps[spec.ID] = e
		files, err := Select(in.Files, m.Platform)
		switch {
		case errors.Is(err, ErrNotForPlatform):
			e.state, e.problem = "unavailable", spec.Title+" publishes no build for this computer ("+m.Platform+")."
		case err != nil:
			e.state, e.problem = "failed", err.Error()
		case spec.Kind == KindService && m.ReadOnly:
			e.state, e.problem = "unavailable", spec.Title+" keeps its data on the drive, and this library is read-only."
		default:
			e.files, e.state, e.preparing = files, "preparing", true
			prepare = append(prepare, e)
		}
	}
	for id := range m.apps {
		if !seen[id] {
			changed = true
			delete(m.apps, id)
		}
	}
	onChange := m.onChange
	m.mu.Unlock()
	for _, e := range prepare {
		go m.prepare(e)
	}
	if changed && onChange != nil {
		onChange()
	}
}

func filesKey(files []Packed) string {
	var k string
	for _, f := range files {
		k += f.Name + "\x00" + f.SHA256 + "\x00"
	}
	return k
}

// prepare unpacks an app and marks it ready.
func (m *Manager) prepare(e *entry) {
	dir, err := Unpack(e.spec, e.files, m.Cache)
	m.mu.Lock()
	if m.apps[e.spec.ID] != e {
		m.mu.Unlock()
		return // replaced or removed meanwhile
	}
	e.preparing = false
	if err != nil {
		e.state, e.problem = "failed", e.spec.Title+" could not be prepared on this computer: "+err.Error()
		m.mu.Unlock()
		m.log().Warn("app unpack", "app", e.spec.ID, "err", err)
		return
	}
	e.dir, e.state, e.problem = dir, "ready", ""
	m.mu.Unlock()
	CleanOld(e.spec, m.Cache, dir)
	m.log().Info("app ready", "app", e.spec.ID, "dir", dir)
}

// Status lists the installed apps.
func (m *Manager) Status() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.apps))
	for _, e := range m.apps {
		st := Status{ID: e.spec.ID, Title: e.spec.Title, Summary: e.spec.Summary, Kind: e.spec.Kind,
			Version: e.version, State: e.state, Problem: e.problem, Platforms: e.platforms,
			SecureContext: e.spec.SecureContext, Slot: e.spec.Slot}
		if st.Platforms == nil {
			st.Platforms = []string{}
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

// Installed reports whether an app is installed, and its spec.
func (m *Manager) Installed(id string) (*Spec, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.apps[id]
	if e == nil {
		return nil, false
	}
	return e.spec, true
}

// ready returns an app's unpacked folder, or why there is none.
func (m *Manager) ready(id string) (dir string, st Status, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.apps[id]
	if e == nil {
		return "", Status{State: "unavailable", Problem: "This app is not installed."}, false
	}
	st = Status{ID: id, Title: e.spec.Title, State: e.state, Problem: e.problem}
	return e.dir, st, e.state == "ready"
}

// Close stops preparing apps.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
}

func (m *Manager) log() *slog.Logger {
	if m.Log == nil {
		return slog.Default()
	}
	return m.Log
}

// dataDir is where an app keeps its data on the drive.
func (m *Manager) dataDir(id string) string {
	if m.Data != nil {
		return m.Data(id)
	}
	return filepath.Join("user", "apps", id)
}

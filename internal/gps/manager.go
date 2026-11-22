package gps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Settings select the position source (mirrors library.GPSSettings so this
// package does not depend on the library).
type Settings struct {
	Source         string
	SerialPort     string
	BaudRate       int
	UDPPort        int
	Address        string
	SimulatorTrack string
	SimulatorSpeed float64
	ManualLat      float64
	ManualLon      float64
	RecordTrack    bool
}

// Status is the receiver state shown to users.
type Status struct {
	// State is off, connecting, waiting (connected, no data), searching
	// (data but no fix), current, stale, lost, manual or error.
	State     string  `json:"state"`
	Source    string  `json:"source"`
	Detail    string  `json:"detail,omitempty"`
	Fix       *Fix    `json:"fix,omitempty"`
	Age       float64 `json:"age,omitempty"` // seconds since the last fix
	Error     string  `json:"error,omitempty"`
	Sentences int64   `json:"sentences"`
	Rejected  int64   `json:"rejected"`
	Recording bool    `json:"recording"`
	Track     string  `json:"track,omitempty"`
	Points    int     `json:"points,omitempty"`
}

// Freshness thresholds.
const (
	staleAfter = 5 * time.Second
	lostAfter  = 60 * time.Second
)

// Manager runs one position source and records tracks.
type Manager struct {
	// Dir is the user folder (tracks and waypoints are stored under it).
	Dir      string
	ReadOnly bool
	Log      *slog.Logger
	// SimCenter suggests where the simulator's default loop is placed
	// (for example the centre of an installed map).
	SimCenter func() (lat, lon float64, ok bool)

	now func() time.Time
	t   tracker

	mu        sync.Mutex
	settings  Settings
	cancel    context.CancelFunc
	done      chan struct{}
	connected bool
	connErr   string
	detail    string
	subs      map[chan Status]struct{}
	rec       *recorder
	wmu       sync.Mutex // serializes waypoint file updates
}

// NewManager returns a stopped manager.
func NewManager(dir string, readOnly bool, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{Dir: dir, ReadOnly: readOnly, Log: log, now: time.Now, subs: map[chan Status]struct{}{}}
	m.t.now = func() time.Time { return m.now() }
	m.t.onFix = m.onFix
	return m
}

// Apply (re)starts the configured source. It returns immediately; failures
// show up in Status and are retried with backoff.
func (m *Manager) Apply(ctx context.Context, s Settings) {
	m.Stop()
	m.mu.Lock()
	m.settings = s
	m.connected, m.connErr, m.detail = false, "", ""
	m.t.reset()
	if s.RecordTrack && !m.ReadOnly && s.Source != "" && s.Source != "manual" {
		m.rec = newRecorder(filepath.Join(m.Dir, "tracks"), m.now())
	}
	if s.Source == "" {
		m.mu.Unlock()
		m.broadcast()
		return
	}
	if s.Source == "manual" {
		m.detail = "fixed position"
		m.mu.Unlock()
		if validCoord(s.ManualLat, s.ManualLon) {
			m.t.setManual(s.ManualLat, s.ManualLon)
		}
		m.broadcast()
		return
	}
	src, detail, err := m.makeSource(s)
	m.detail = detail
	if err != nil {
		m.connErr = err.Error()
		m.mu.Unlock()
		m.broadcast()
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	m.done = make(chan struct{})
	done := m.done
	m.mu.Unlock()
	go m.run(ctx, src, done)
}

func (m *Manager) makeSource(s Settings) (source, string, error) {
	switch s.Source {
	case "serial":
		baud := s.BaudRate
		if baud <= 0 {
			baud = 4800
		}
		return serialSource(s.SerialPort, baud), fmt.Sprintf("%s at %d baud", s.SerialPort, baud), nil
	case "udp":
		port := s.UDPPort
		if port <= 0 || port > 65535 {
			port = 10110
		}
		return udpSource(port), fmt.Sprintf("UDP port %d", port), nil
	case "tcp":
		return tcpSource(s.Address, false), s.Address, nil
	case "gpsd":
		addr := s.Address
		if addr == "" {
			addr = "127.0.0.1:2947"
		}
		return tcpSource(addr, true), "gpsd at " + addr, nil
	case "simulator":
		track, name, err := m.simTrack(s)
		if err != nil {
			return nil, "", err
		}
		return simSource(track, s.SimulatorSpeed, m.now), "replaying " + name, nil
	}
	return nil, "", fmt.Errorf("unknown position source %q", s.Source)
}

func (m *Manager) simTrack(s Settings) ([]Point, string, error) {
	if s.SimulatorTrack != "" {
		if filepath.Base(s.SimulatorTrack) != s.SimulatorTrack {
			return nil, "", errors.New("the simulator track must be a file name in the user folder")
		}
		for _, dir := range []string{filepath.Join(m.Dir, "tracks"), filepath.Join(m.Dir, "documents"), m.Dir} {
			f, err := os.Open(filepath.Join(dir, s.SimulatorTrack))
			if err != nil {
				continue
			}
			g, err := ReadGPX(f)
			f.Close()
			if err != nil {
				return nil, "", err
			}
			var pts []Point
			for _, t := range g.Tracks {
				for _, seg := range t.Segments {
					pts = append(pts, seg...)
				}
			}
			return pts, s.SimulatorTrack, nil
		}
		return nil, "", fmt.Errorf("simulator track %s not found", s.SimulatorTrack)
	}
	lat, lon := s.ManualLat, s.ManualLon
	if (lat == 0 && lon == 0) && m.SimCenter != nil {
		if la, lo, ok := m.SimCenter(); ok {
			lat, lon = la, lo
		}
	}
	return circleTrack(lat, lon), "a 1 km loop", nil
}

func (m *Manager) run(ctx context.Context, src source, done chan struct{}) {
	defer close(done)
	backoff := time.Second
	for {
		start := m.now()
		err := src(ctx, m.t.handleLine, func() {
			m.mu.Lock()
			m.connected, m.connErr = true, ""
			m.mu.Unlock()
			m.broadcast()
		})
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		m.connected = false
		if err != nil {
			m.connErr = err.Error()
		}
		m.mu.Unlock()
		m.broadcast()
		m.Log.Info("position source disconnected", "err", err)
		if m.now().Sub(start) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// Stop ends the current source and flushes the track.
func (m *Manager) Stop() {
	m.mu.Lock()
	cancel, done, rec := m.cancel, m.done, m.rec
	m.cancel, m.done, m.rec = nil, nil, nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	if rec != nil {
		if err := rec.flush(); err != nil {
			m.Log.Warn("save track", "err", err)
		}
	}
}

func (m *Manager) onFix(f Fix) {
	m.mu.Lock()
	rec := m.rec
	m.mu.Unlock()
	if rec != nil && f.Mode != "manual" {
		if err := rec.add(f); err != nil {
			m.Log.Warn("record track", "err", err)
		}
	}
	m.broadcast()
}

// Status reports the current state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	s := m.settings
	st := Status{Source: s.Source, Detail: m.detail, Error: m.connErr}
	connected := m.connected
	if m.rec != nil {
		st.Recording = true
		st.Track, st.Points = m.rec.status()
	}
	m.mu.Unlock()

	m.t.mu.Lock()
	now := m.now()
	st.Sentences, st.Rejected = m.t.sentences, m.t.bad
	if m.t.fix != nil {
		f := *m.t.fix
		st.Fix = &f
		st.Age = now.Sub(f.Received).Seconds()
	}
	lastData := m.t.lastData
	m.t.mu.Unlock()

	switch {
	case s.Source == "":
		st.State = "off"
	case s.Source == "manual":
		st.State = "manual"
		if st.Fix == nil {
			st.State, st.Error = "error", "set a latitude and longitude"
		}
		st.Age = 0
	case st.Fix != nil && st.Age <= staleAfter.Seconds():
		st.State = "current"
	case st.Fix != nil && st.Age <= lostAfter.Seconds():
		st.State = "stale"
	case st.Fix != nil:
		st.State = "lost"
	case !connected && st.Error != "":
		st.State = "error"
	case !connected:
		st.State = "connecting"
	case lastData.IsZero() || now.Sub(lastData) > 10*time.Second:
		st.State = "waiting"
	default:
		st.State = "searching"
	}
	return st
}

// Subscribe returns a channel receiving status changes (at most one pending
// update is kept for slow readers) and a function to unsubscribe.
func (m *Manager) Subscribe() (<-chan Status, func()) {
	ch := make(chan Status, 1)
	m.mu.Lock()
	m.subs[ch] = struct{}{}
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		delete(m.subs, ch)
		m.mu.Unlock()
	}
}

func (m *Manager) broadcast() {
	st := m.Status()
	m.mu.Lock()
	defer m.mu.Unlock()
	for ch := range m.subs {
		select {
		case ch <- st:
		default:
			// Replace the stale pending update.
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- st:
			default:
			}
		}
	}
}

package gps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
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
	// CheckSource is a second source read alongside Source. Its position
	// is shown and compared with the first, but never drives the map,
	// tracks or guidance. Empty for none.
	CheckSource string
	// StarlinkAddress is the dish's gRPC address (default
	// 192.168.100.1:9200).
	StarlinkAddress string
	// BrowserID names the browser whose location services feed the
	// "browser" source; positions from other browsers are refused.
	BrowserID string
}

// Status is the receiver state shown to users.
type Status struct {
	// State is off, connecting, waiting (connected, no data), searching
	// (data but no fix), current, stale, lost, manual or error.
	State     string  `json:"state"`
	Source    string  `json:"source"`
	Detail    string  `json:"detail,omitempty"`
	Basis     string  `json:"basis,omitempty"` // where the source says the position comes from
	Fix       *Fix    `json:"fix,omitempty"`
	Age       float64 `json:"age,omitempty"` // seconds since the last fix
	Error     string  `json:"error,omitempty"`
	Sentences int64   `json:"sentences"`
	Rejected  int64   `json:"rejected"`
	Recording bool    `json:"recording"`
	Track     string  `json:"track,omitempty"`
	Points    int     `json:"points,omitempty"`
	// Check is the second source's state, when one is set.
	Check *Status `json:"check,omitempty"`
	// Apart is the distance in metres between the two sources' positions
	// when both are current, and Disagree says it is more than their
	// stated accuracy allows.
	Apart    float64 `json:"apart,omitempty"`
	Disagree bool    `json:"disagree,omitempty"`
}

// Freshness thresholds.
const (
	staleAfter = 5 * time.Second
	lostAfter  = 60 * time.Second
)

// feed is one running position source with its own tracker. Its fields
// other than t are guarded by Manager.mu.
type feed struct {
	kind      string
	t         tracker
	connected bool
	connErr   string
	detail    string
	basis     string
	cancel    context.CancelFunc
	done      chan struct{}
}

// Manager runs the position source (and an optional second source to
// compare with) and records tracks.
type Manager struct {
	// Dir is the user folder (tracks, waypoints and routes are stored
	// under it).
	Dir      string
	ReadOnly bool
	Log      *slog.Logger
	// SimCenter suggests where the simulator's default loop is placed
	// (for example the centre of an installed map).
	SimCenter func() (lat, lon float64, ok bool)
	// HTTPClient talks to a Starlink dish; nil uses one that speaks
	// unencrypted HTTP/2 and ignores proxy settings (the dish is on the
	// local network).
	HTTPClient *http.Client

	now func() time.Time

	mu       sync.Mutex
	settings Settings
	main     feed // drives the map, tracks and guidance
	alt      feed // the check source
	browser  chan BrowserFix
	subs     map[chan Status]struct{}
	rec      *recorder
	wmu      sync.Mutex // serializes waypoint file updates
	rmu      sync.Mutex // serializes route file updates
}

// NewManager returns a stopped manager.
func NewManager(dir string, readOnly bool, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{Dir: dir, ReadOnly: readOnly, Log: log, now: time.Now, subs: map[chan Status]struct{}{}}
	for _, f := range []*feed{&m.main, &m.alt} {
		f.t.now = func() time.Time { return m.now() }
	}
	m.main.t.onFix = m.onFix
	m.alt.t.onFix = func(Fix) { m.broadcast() }
	return m
}

// Apply (re)starts the configured sources. It returns immediately; failures
// show up in Status and are retried with backoff.
func (m *Manager) Apply(ctx context.Context, s Settings) {
	m.Stop()
	if s.CheckSource == s.Source || s.CheckSource == "manual" || s.CheckSource == "simulator" {
		s.CheckSource = ""
	}
	m.mu.Lock()
	m.settings = s
	m.browser = nil
	if s.Source == "browser" || s.CheckSource == "browser" {
		m.browser = make(chan BrowserFix, 1)
	}
	if s.RecordTrack && !m.ReadOnly && s.Source != "" && s.Source != "manual" {
		m.rec = newRecorder(filepath.Join(m.Dir, "tracks"), m.now())
	}
	m.mu.Unlock()
	m.start(ctx, &m.main, s.Source, s)
	m.start(ctx, &m.alt, s.CheckSource, s)
	m.broadcast()
}

func (m *Manager) start(ctx context.Context, f *feed, kind string, s Settings) {
	m.mu.Lock()
	f.kind = kind
	f.connected, f.connErr, f.detail, f.basis = false, "", "", ""
	f.t.reset()
	switch kind {
	case "":
		m.mu.Unlock()
		return
	case "manual":
		f.detail = "fixed position"
		m.mu.Unlock()
		if validCoord(s.ManualLat, s.ManualLon) {
			f.t.setManual(s.ManualLat, s.ManualLon)
		}
		return
	}
	src, detail, err := m.makeSource(s, kind, f)
	f.detail = detail
	if err != nil {
		f.connErr = err.Error()
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	f.cancel = cancel
	f.done = make(chan struct{})
	done := f.done
	m.mu.Unlock()
	go m.run(ctx, f, src, done)
}

// makeSource is called with m.mu held.
func (m *Manager) makeSource(s Settings, kind string, f *feed) (source, string, error) {
	switch kind {
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
	case "starlink":
		addr := s.StarlinkAddress
		if addr == "" {
			addr = StarlinkDefaultAddress
		}
		client := m.HTTPClient
		if client == nil {
			client = starlinkClient()
		}
		basis := func(b string) { m.setBasis(f, b) }
		return starlinkSource(addr, client, basis, starlinkPoll, m.now), "Starlink dish at " + addr, nil
	case "browser":
		return browserSource(m.browser, m.now), "location services of the browser chosen in Settings", nil
	}
	return nil, "", fmt.Errorf("unknown position source %q", kind)
}

func (m *Manager) setBasis(f *feed, b string) {
	m.mu.Lock()
	changed := f.basis != b
	f.basis = b
	m.mu.Unlock()
	if changed {
		m.broadcast()
	}
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

func (m *Manager) run(ctx context.Context, f *feed, src source, done chan struct{}) {
	defer close(done)
	backoff := time.Second
	for {
		start := m.now()
		err := src(ctx, f.t.handleLine, func() {
			m.mu.Lock()
			f.connected, f.connErr = true, ""
			m.mu.Unlock()
			m.broadcast()
		})
		if ctx.Err() != nil {
			return
		}
		m.mu.Lock()
		f.connected = false
		if err != nil {
			f.connErr = err.Error()
		}
		m.mu.Unlock()
		m.broadcast()
		m.Log.Info("position source disconnected", "source", f.kind, "err", err)
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

// Stop ends the current sources and flushes the track.
func (m *Manager) Stop() {
	m.mu.Lock()
	var cancels []context.CancelFunc
	var dones []chan struct{}
	for _, f := range []*feed{&m.main, &m.alt} {
		if f.cancel != nil {
			cancels = append(cancels, f.cancel)
			dones = append(dones, f.done)
		}
		f.cancel, f.done = nil, nil
	}
	rec := m.rec
	m.rec = nil
	m.mu.Unlock()
	for _, c := range cancels {
		c()
	}
	for _, d := range dones {
		<-d
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

// feedSnap is a copy of a feed's connection state taken under m.mu.
type feedSnap struct {
	kind, detail, basis, err string
	connected                bool
}

func snapOf(f *feed) feedSnap {
	return feedSnap{kind: f.kind, detail: f.detail, basis: f.basis, err: f.connErr, connected: f.connected}
}

// Status reports the current state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	main, alt := snapOf(&m.main), snapOf(&m.alt)
	var recording bool
	var track string
	var points int
	if m.rec != nil {
		recording = true
		track, points = m.rec.status()
	}
	m.mu.Unlock()

	now := m.now()
	st := feedStatus(main, &m.main.t, now)
	st.Recording, st.Track, st.Points = recording, track, points
	if alt.kind != "" {
		c := feedStatus(alt, &m.alt.t, now)
		st.Check = &c
		if live(st) && live(c) {
			st.Apart, st.Disagree = disagreement(st.Fix, c.Fix)
		}
	}
	return st
}

func live(s Status) bool { return s.Fix != nil && (s.State == "current" || s.State == "manual") }

// errorRadius is a fix's horizontal uncertainty in metres: the stated
// accuracy, else a rough figure from HDOP, else a typical consumer
// receiver's.
func errorRadius(f *Fix) float64 {
	switch {
	case f.Accuracy > 0:
		return f.Accuracy
	case f.Mode == "manual":
		return 10
	case f.HDOP > 0:
		return f.HDOP * 5
	}
	return 15
}

// disagreement says how far apart two fixes are and whether that is more
// than their uncertainty allows. The tolerance is deliberately wide (three
// times the combined error radii, at least 100 m): the point is to notice
// a jammed or spoofed receiver that is kilometres out, not to referee
// metres.
func disagreement(a, b *Fix) (float64, bool) {
	d := Distance(a.Lat, a.Lon, b.Lat, b.Lon)
	tol := math.Max(100, 3*(errorRadius(a)+errorRadius(b)))
	return math.Round(d), d > tol
}

func feedStatus(sn feedSnap, t *tracker, now time.Time) Status {
	st := Status{Source: sn.kind, Detail: sn.detail, Basis: sn.basis, Error: sn.err}
	t.mu.Lock()
	st.Sentences, st.Rejected = t.sentences, t.bad
	if t.fix != nil {
		f := *t.fix
		st.Fix = &f
		st.Age = now.Sub(f.Received).Seconds()
	}
	lastData := t.lastData
	t.mu.Unlock()

	switch {
	case sn.kind == "":
		st.State = "off"
	case sn.kind == "manual":
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
	case !sn.connected && st.Error != "":
		st.State = "error"
	case !sn.connected:
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

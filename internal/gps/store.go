package gps

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/KK4ODA/arcana/internal/fsutil"
)

// ErrReadOnly is returned when the library cannot be written.
var ErrReadOnly = errors.New("the library is read-only")

// recorder appends fixes to a GPX track file, rewriting it atomically at
// most every 30 seconds so an unplugged drive loses little.
type recorder struct {
	mu        sync.Mutex
	path      string
	name      string
	segments  [][]Point
	last      *Point
	length    float64 // metres, summed within segments
	dirty     bool
	lastFlush time.Time
}

const (
	minTrackDistance = 5.0 // metres between recorded points
	maxTrackGap      = 60 * time.Second
	flushEvery       = 30 * time.Second
)

func newRecorder(dir string, now time.Time) *recorder {
	base := "track-" + now.UTC().Format("20060102-150405")
	name := base + ".gpx"
	// Recording stopped and started again within a second must not
	// overwrite the track just saved.
	for n := 2; ; n++ {
		if _, err := os.Lstat(filepath.Join(dir, name)); err != nil {
			break
		}
		name = fmt.Sprintf("%s-%d.gpx", base, n)
	}
	return &recorder{path: filepath.Join(dir, name), name: name, lastFlush: now}
}

func (r *recorder) add(f Fix) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := Point{Lat: f.Lat, Lon: f.Lon, Ele: Alt2Ele(f.Alt), Time: f.Received.UTC()}
	if !f.Time.IsZero() {
		p.Time = f.Time
	}
	if r.last != nil {
		if p.Time.Sub(r.last.Time) < maxTrackGap &&
			Distance(r.last.Lat, r.last.Lon, p.Lat, p.Lon) < minTrackDistance {
			return nil
		}
		if p.Time.Sub(r.last.Time) >= maxTrackGap {
			r.segments = append(r.segments, nil)
		} else {
			r.length += Distance(r.last.Lat, r.last.Lon, p.Lat, p.Lon)
		}
	}
	if len(r.segments) == 0 {
		r.segments = append(r.segments, nil)
	}
	r.segments[len(r.segments)-1] = append(r.segments[len(r.segments)-1], p)
	r.last = &p
	r.dirty = true
	if time.Since(r.lastFlush) >= flushEvery {
		return r.flushLocked()
	}
	return nil
}

// Alt2Ele copies an optional altitude.
func Alt2Ele(a *float64) *float64 {
	if a == nil {
		return nil
	}
	v := math.Round(*a*10) / 10
	return &v
}

func (r *recorder) status() (name string, points int, length float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.segments {
		points += len(s)
	}
	return r.name, points, r.length
}

// snapshot copies the track recorded so far when it is the one named.
func (r *recorder) snapshot(name string) (Track, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name != r.name {
		return Track{}, false
	}
	t := Track{Name: strings.TrimSuffix(r.name, ".gpx")}
	for _, s := range r.segments {
		if len(s) > 0 {
			t.Segments = append(t.Segments, append([]Point(nil), s...))
		}
	}
	return t, true
}

func (r *recorder) flush() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushLocked()
}

func (r *recorder) flushLocked() error {
	r.lastFlush = time.Now()
	if !r.dirty {
		return nil
	}
	var segs [][]Point
	for _, s := range r.segments {
		if len(s) > 0 {
			segs = append(segs, s)
		}
	}
	if len(segs) == 0 {
		return nil
	}
	var buf bytes.Buffer
	if err := WriteGPX(&buf, GPX{Tracks: []Track{{Name: strings.TrimSuffix(r.name, ".gpx"), Segments: segs}}}); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(r.path, buf.Bytes(), 0o644); err != nil {
		return err
	}
	r.dirty = false
	return nil
}

// ---------------------------------------------------------------- tracks

// TrackInfo summarizes a stored track.
type TrackInfo struct {
	Name     string    `json:"name"`
	Points   int       `json:"points"`
	Length   float64   `json:"length"` // metres
	Start    time.Time `json:"start,omitempty"`
	End      time.Time `json:"end,omitempty"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	// Recording marks the track still being recorded.
	Recording bool `json:"recording,omitempty"`
}

var trackNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._ -]{0,120}\.gpx$`)

func (m *Manager) tracksDir() string { return filepath.Join(m.Dir, "tracks") }

func (m *Manager) trackPath(name string) (string, error) {
	if !trackNameRe.MatchString(name) || strings.Contains(name, "..") {
		return "", errors.New("invalid track name")
	}
	return filepath.Join(m.tracksDir(), name), nil
}

// Tracks lists stored GPX tracks, newest first.
func (m *Manager) Tracks() ([]TrackInfo, error) {
	recording := m.flushRecording()
	entries, err := os.ReadDir(m.tracksDir())
	if errors.Is(err, os.ErrNotExist) {
		return []TrackInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []TrackInfo{}
	for _, e := range entries {
		if e.IsDir() || !trackNameRe.MatchString(e.Name()) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		info := TrackInfo{Name: e.Name(), Size: fi.Size(), Modified: fi.ModTime().UTC(), Recording: e.Name() == recording}
		if t, err := m.Track(e.Name()); err == nil {
			info.Points, info.Length = t.Points(), math.Round(t.Length())
			if n := t.Points(); n > 0 {
				first, last := t.Segments[0][0], t.Segments[len(t.Segments)-1]
				info.Start, info.End = first.Time, last[len(last)-1].Time
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// flushRecording writes the track being recorded and returns its file name
// ("" when nothing is recorded).
func (m *Manager) flushRecording() string {
	m.mu.Lock()
	rec := m.rec
	m.mu.Unlock()
	if rec == nil {
		return ""
	}
	_ = rec.flush()
	return rec.name
}

// Track reads a stored track, merging all tracks in the file. The track
// being recorded is read from memory, so it is complete up to the latest
// point although its file is rewritten only every 30 seconds.
func (m *Manager) Track(name string) (Track, error) {
	p, err := m.trackPath(name)
	if err != nil {
		return Track{}, err
	}
	m.mu.Lock()
	rec := m.rec
	m.mu.Unlock()
	if rec != nil {
		if t, ok := rec.snapshot(name); ok {
			return t, nil
		}
	}
	f, err := os.Open(p)
	if err != nil {
		return Track{}, err
	}
	defer f.Close()
	g, err := ReadGPX(f)
	if err != nil {
		return Track{}, err
	}
	t := Track{Name: strings.TrimSuffix(name, ".gpx")}
	for _, tr := range g.Tracks {
		t.Segments = append(t.Segments, tr.Segments...)
	}
	return t, nil
}

// DeleteTrack removes a stored track.
func (m *Manager) DeleteTrack(name string) error {
	if m.ReadOnly {
		return ErrReadOnly
	}
	p, err := m.trackPath(name)
	if err != nil {
		return err
	}
	m.mu.Lock()
	recording := m.rec != nil && m.rec.name == name
	m.mu.Unlock()
	if recording {
		return errors.New("this track is being recorded; stop recording first")
	}
	return os.Remove(p)
}

// ---------------------------------------------------------------- waypoints

func (m *Manager) waypointsPath() string { return filepath.Join(m.Dir, "waypoints.json") }

type waypointFile struct {
	Format    string     `json:"format"`
	Waypoints []Waypoint `json:"waypoints"`
}

// Waypoints lists saved waypoints.
func (m *Manager) Waypoints() ([]Waypoint, error) {
	data, err := os.ReadFile(m.waypointsPath())
	if errors.Is(err, os.ErrNotExist) {
		return []Waypoint{}, nil
	}
	if err != nil {
		return nil, err
	}
	var wf waypointFile
	if err := json.Unmarshal(data, &wf); err != nil {
		return nil, fmt.Errorf("waypoints.json is damaged: %w", err)
	}
	out := []Waypoint{}
	for _, w := range wf.Waypoints {
		if validCoord(w.Lat, w.Lon) {
			out = append(out, w)
		}
	}
	return out, nil
}

func (m *Manager) saveWaypoints(ws []Waypoint) error {
	data, err := json.MarshalIndent(waypointFile{Format: "arcana-waypoints", Waypoints: ws}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(m.waypointsPath(), append(data, '\n'), 0o644)
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func cleanWaypoint(w Waypoint) (Waypoint, error) {
	w.Name = strings.TrimSpace(clip(w.Name, 200))
	w.Note = clip(w.Note, 2000)
	w.Symbol = clip(w.Symbol, 60)
	if w.Name == "" {
		return w, errors.New("a waypoint needs a name")
	}
	if !validCoord(w.Lat, w.Lon) {
		return w, errors.New("latitude must be -90..90 and longitude -180..180")
	}
	w.Lat, w.Lon = round7(w.Lat), round7(w.Lon)
	return w, nil
}

// AddWaypoint saves a new waypoint.
func (m *Manager) AddWaypoint(w Waypoint) (Waypoint, error) {
	if m.ReadOnly {
		return w, ErrReadOnly
	}
	w, err := cleanWaypoint(w)
	if err != nil {
		return w, err
	}
	m.wmu.Lock()
	defer m.wmu.Unlock()
	ws, err := m.Waypoints()
	if err != nil {
		return w, err
	}
	if len(ws) >= 10000 {
		return w, errors.New("too many waypoints")
	}
	w.ID = newID()
	if w.Created.IsZero() {
		w.Created = m.now().UTC().Truncate(time.Second)
	}
	return w, m.saveWaypoints(append(ws, w))
}

// DeleteWaypoint removes a waypoint.
func (m *Manager) DeleteWaypoint(id string) error {
	if m.ReadOnly {
		return ErrReadOnly
	}
	m.wmu.Lock()
	defer m.wmu.Unlock()
	ws, err := m.Waypoints()
	if err != nil {
		return err
	}
	out := ws[:0]
	found := false
	for _, w := range ws {
		if w.ID == id {
			found = true
			continue
		}
		out = append(out, w)
	}
	if !found {
		return os.ErrNotExist
	}
	return m.saveWaypoints(out)
}

// ImportResult reports a GPX import.
type ImportResult struct {
	Waypoints int      `json:"waypoints"`
	Tracks    []string `json:"tracks"`
	Skipped   int      `json:"skipped"`
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._ -]+`)

// ImportGPX adds a GPX file's waypoints (skipping duplicates) and saves its
// tracks as separate files.
func (m *Manager) ImportGPX(r io.Reader, fileName string) (ImportResult, error) {
	var res ImportResult
	if m.ReadOnly {
		return res, ErrReadOnly
	}
	g, err := ReadGPX(r)
	if err != nil {
		return res, err
	}
	m.wmu.Lock()
	ws, err := m.Waypoints()
	if err != nil {
		m.wmu.Unlock()
		return res, err
	}
	seen := map[string]bool{}
	key := func(w Waypoint) string { return fmt.Sprintf("%s|%.5f|%.5f", strings.ToLower(w.Name), w.Lat, w.Lon) }
	for _, w := range ws {
		seen[key(w)] = true
	}
	for _, w := range g.Waypoints {
		if w.Name == "" {
			w.Name = fmt.Sprintf("Waypoint %.5f, %.5f", w.Lat, w.Lon)
		}
		cw, err := cleanWaypoint(w)
		if err != nil || seen[key(cw)] || len(ws) >= 10000 {
			res.Skipped++
			continue
		}
		seen[key(cw)] = true
		cw.ID = newID()
		if cw.Created.IsZero() {
			cw.Created = m.now().UTC().Truncate(time.Second)
		}
		ws = append(ws, cw)
		res.Waypoints++
	}
	if res.Waypoints > 0 {
		if err := m.saveWaypoints(ws); err != nil {
			m.wmu.Unlock()
			return res, err
		}
	}
	m.wmu.Unlock()

	base := strings.TrimSuffix(filepath.Base(fileName), filepath.Ext(fileName))
	base = strings.Trim(unsafeName.ReplaceAllString(base, "-"), "-. ")
	if base == "" {
		base = "imported"
	}
	if len(base) > 80 {
		base = base[:80]
	}
	if err := os.MkdirAll(m.tracksDir(), 0o755); err != nil {
		return res, err
	}
	for i, t := range g.Tracks {
		name := base + ".gpx"
		if len(g.Tracks) > 1 {
			name = fmt.Sprintf("%s-%d.gpx", base, i+1)
		}
		for n := 2; ; n++ {
			if _, err := os.Stat(filepath.Join(m.tracksDir(), name)); errors.Is(err, os.ErrNotExist) {
				break
			}
			name = fmt.Sprintf("%s-%d.gpx", strings.TrimSuffix(name, ".gpx"), n)
		}
		var buf bytes.Buffer
		if err := WriteGPX(&buf, GPX{Tracks: []Track{t}}); err != nil {
			return res, err
		}
		if err := fsutil.WriteFileAtomic(filepath.Join(m.tracksDir(), name), buf.Bytes(), 0o644); err != nil {
			return res, err
		}
		res.Tracks = append(res.Tracks, name)
	}
	return res, nil
}

// ExportGPX writes all waypoints and, optionally, the named tracks.
func (m *Manager) ExportGPX(w io.Writer, tracks []string) error {
	ws, err := m.Waypoints()
	if err != nil {
		return err
	}
	g := GPX{Waypoints: ws}
	for _, name := range tracks {
		t, err := m.Track(name)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		g.Tracks = append(g.Tracks, t)
	}
	return WriteGPX(w, g)
}

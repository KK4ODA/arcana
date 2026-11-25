package gps

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/fsutil"
)

// Saved routes keep a computed route (geometry and maneuvers, as GET
// /api/route returned it) in user/routes/<id>.json, so it can be opened
// and followed later without recomputing it: the road network may have
// been removed, or the traveller may be somewhere else when they plan.

// RouteEnd is a route's start or destination.
type RouteEnd struct {
	Lat   float64 `json:"lat"`
	Lon   float64 `json:"lon"`
	Label string  `json:"label,omitempty"`
}

// RouteStep mirrors routing.Step (this package does not depend on the
// router).
type RouteStep struct {
	Type     string     `json:"type"`
	Modifier string     `json:"modifier,omitempty"`
	Name     string     `json:"name,omitempty"`
	Text     string     `json:"text"`
	Exit     int        `json:"exit,omitempty"`
	Distance float64    `json:"distance"`
	Duration float64    `json:"duration"`
	Location [2]float64 `json:"location"`
	Index    int        `json:"index"`
}

// SavedRoute is a route kept in the user folder.
type SavedRoute struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Created  time.Time    `json:"created"`
	Mode     string       `json:"mode"`
	From     RouteEnd     `json:"from"`
	To       RouteEnd     `json:"to"`
	Distance float64      `json:"distance"`
	Duration float64      `json:"duration"`
	Network  string       `json:"network,omitempty"`
	Warnings []string     `json:"warnings"`
	Geometry [][2]float64 `json:"geometry"`
	Steps    []RouteStep  `json:"steps"`
}

// RouteInfo is a saved route in a list.
type RouteInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Created  time.Time `json:"created"`
	Mode     string    `json:"mode"`
	From     RouteEnd  `json:"from"`
	To       RouteEnd  `json:"to"`
	Distance float64   `json:"distance"`
	Duration float64   `json:"duration"`
}

// Limits on saved routes. A 1,000 km car route has some tens of thousands
// of points.
const (
	MaxRouteBytes   = 32 << 20
	maxRoutePoints  = 500000
	maxRouteSteps   = 20000
	maxSavedRoutes  = 1000
	routeFileFormat = "arcana-route"
)

var routeIDPattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

func (m *Manager) routesDir() string { return filepath.Join(m.Dir, "routes") }

func (m *Manager) routePath(id string) (string, error) {
	if !routeIDPattern.MatchString(id) {
		return "", os.ErrNotExist
	}
	return filepath.Join(m.routesDir(), id+".json"), nil
}

type routeFile struct {
	Format string `json:"format"`
	SavedRoute
}

func validNumber(v float64) bool { return finite(v) && v >= 0 && v < 1e9 }

func cleanText(s string, n int) string { return strings.TrimSpace(clip(s, n)) }

// cleanRoute validates a route from a client or a file.
func cleanRoute(r SavedRoute) (SavedRoute, error) {
	switch r.Mode {
	case "car", "bike", "foot":
	default:
		return r, errors.New("unknown travel mode")
	}
	if len(r.Geometry) < 2 || len(r.Geometry) > maxRoutePoints {
		return r, errors.New("a route needs between 2 and 500,000 points")
	}
	for i, c := range r.Geometry {
		if !validCoord(c[1], c[0]) {
			return r, fmt.Errorf("point %d is not a valid position", i+1)
		}
	}
	for _, e := range []*RouteEnd{&r.From, &r.To} {
		if !validCoord(e.Lat, e.Lon) {
			return r, errors.New("the start or destination is not a valid position")
		}
		e.Label = cleanText(e.Label, 200)
	}
	if len(r.Steps) == 0 || len(r.Steps) > maxRouteSteps {
		return r, errors.New("a route needs between 1 and 20,000 steps")
	}
	for i := range r.Steps {
		s := &r.Steps[i]
		if s.Index < 0 || s.Index >= len(r.Geometry) || !validCoord(s.Location[1], s.Location[0]) ||
			!validNumber(s.Distance) || !validNumber(s.Duration) || s.Exit < 0 || s.Exit > 100 {
			return r, fmt.Errorf("step %d is not valid", i+1)
		}
		s.Type, s.Modifier = cleanText(s.Type, 40), cleanText(s.Modifier, 40)
		s.Name, s.Text = cleanText(s.Name, 200), cleanText(s.Text, 500)
	}
	if !validNumber(r.Distance) || !validNumber(r.Duration) {
		return r, errors.New("the route's distance or duration is not valid")
	}
	if len(r.Warnings) > 20 {
		r.Warnings = r.Warnings[:20]
	}
	for i := range r.Warnings {
		r.Warnings[i] = cleanText(r.Warnings[i], 500)
	}
	if r.Warnings == nil {
		r.Warnings = []string{}
	}
	r.Network = cleanText(r.Network, 200)
	r.Name = cleanText(r.Name, 200)
	if r.Name == "" {
		to := r.To.Label
		if to == "" {
			to = fmt.Sprintf("%.5f, %.5f", r.To.Lat, r.To.Lon)
		}
		r.Name = "Route to " + to
	}
	return r, nil
}

// Routes lists saved routes, newest first. Damaged files are skipped.
func (m *Manager) Routes() ([]RouteInfo, error) {
	out := []RouteInfo{}
	ents, err := os.ReadDir(m.routesDir())
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !routeIDPattern.MatchString(id) {
			continue
		}
		r, err := m.Route(id)
		if err != nil {
			m.Log.Warn("saved route unreadable", "file", e.Name(), "err", err)
			continue
		}
		out = append(out, RouteInfo{ID: r.ID, Name: r.Name, Created: r.Created, Mode: r.Mode, From: r.From, To: r.To,
			Distance: r.Distance, Duration: r.Duration})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// Route reads a saved route.
func (m *Manager) Route(id string) (SavedRoute, error) {
	p, err := m.routePath(id)
	if err != nil {
		return SavedRoute{}, err
	}
	f, err := os.Open(p)
	if err != nil {
		return SavedRoute{}, err
	}
	defer f.Close()
	var rf routeFile
	if err := json.NewDecoder(io.LimitReader(f, MaxRouteBytes)).Decode(&rf); err != nil {
		return SavedRoute{}, fmt.Errorf("the saved route is damaged: %w", err)
	}
	if rf.Format != routeFileFormat {
		return SavedRoute{}, errors.New("not a saved route")
	}
	r, err := cleanRoute(rf.SavedRoute)
	if err != nil {
		return SavedRoute{}, fmt.Errorf("the saved route is damaged: %w", err)
	}
	r.ID = id
	return r, nil
}

// SaveRoute stores a new route and returns it with its identifier.
func (m *Manager) SaveRoute(r SavedRoute) (SavedRoute, error) {
	if m.ReadOnly {
		return r, ErrReadOnly
	}
	r, err := cleanRoute(r)
	if err != nil {
		return r, err
	}
	m.rmu.Lock()
	defer m.rmu.Unlock()
	if ents, _ := os.ReadDir(m.routesDir()); len(ents) >= maxSavedRoutes {
		return r, errors.New("too many saved routes; delete some first")
	}
	r.ID = newID()
	r.Created = m.now().UTC().Truncate(time.Second)
	data, err := json.Marshal(routeFile{Format: routeFileFormat, SavedRoute: r})
	if err != nil {
		return r, err
	}
	if err := os.MkdirAll(m.routesDir(), 0o755); err != nil {
		return r, err
	}
	p, _ := m.routePath(r.ID)
	return r, fsutil.WriteFileAtomic(p, append(data, '\n'), 0o644)
}

// DeleteRoute removes a saved route.
func (m *Manager) DeleteRoute(id string) error {
	if m.ReadOnly {
		return ErrReadOnly
	}
	p, err := m.routePath(id)
	if err != nil {
		return err
	}
	m.rmu.Lock()
	defer m.rmu.Unlock()
	return os.Remove(p)
}

// RouteGPX writes a saved route as GPX: a route whose points are the
// maneuvers (for GPS units and apps that navigate GPX routes), and a track
// with the full line.
func (m *Manager) RouteGPX(w io.Writer, id string) error {
	r, err := m.Route(id)
	if err != nil {
		return err
	}
	g := GPX{}
	gr := GPXRoute{Name: r.Name}
	for _, s := range r.Steps {
		gr.Points = append(gr.Points, Waypoint{Name: clip(s.Text, 200), Lat: s.Location[1], Lon: s.Location[0]})
	}
	g.Routes = append(g.Routes, gr)
	line := make([]Point, len(r.Geometry))
	for i, c := range r.Geometry {
		line[i] = Point{Lat: c[1], Lon: c[0]}
	}
	g.Tracks = append(g.Tracks, Track{Name: r.Name, Segments: [][]Point{line}})
	return WriteGPX(w, g)
}

package routing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// NetworkSpec is an installed road network.
type NetworkSpec struct {
	ResourceID string
	Title      string
	Version    string
	Path       string
	// BBox is the region the network covers (west, south, east, north).
	BBox []float64
}

// NetworkInfo describes a network for the UI.
type NetworkInfo struct {
	ResourceID string    `json:"resourceId"`
	Title      string    `json:"title"`
	Version    string    `json:"version"`
	BBox       []float64 `json:"bbox"`
	Loaded     bool      `json:"loaded"`
	Error      string    `json:"error,omitempty"`
	Edges      int       `json:"edges,omitempty"`
	Source     string    `json:"source,omitempty"`
}

// maxLoaded bounds how many networks are kept in memory.
const maxLoaded = 2

type network struct {
	spec    NetworkSpec
	router  *Router
	err     error
	used    time.Time
	loading chan struct{}
}

// Service routes over the installed road networks, loading each one the
// first time it is needed.
type Service struct {
	mu   sync.Mutex
	nets map[string]*network
}

// NewService returns an empty service.
func NewService() *Service { return &Service{nets: map[string]*network{}} }

// Load replaces the set of installed networks. Networks whose file did not
// change stay loaded.
func (s *Service) Load(specs []NetworkSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := map[string]*network{}
	for _, sp := range specs {
		if old, ok := s.nets[sp.ResourceID]; ok && old.spec.Path == sp.Path && old.spec.Version == sp.Version {
			old.spec = sp
			next[sp.ResourceID] = old
			continue
		}
		next[sp.ResourceID] = &network{spec: sp}
	}
	s.nets = next
}

// Networks lists installed networks.
func (s *Service) Networks() []NetworkInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]NetworkInfo, 0, len(s.nets))
	for _, n := range s.nets {
		info := NetworkInfo{ResourceID: n.spec.ResourceID, Title: n.spec.Title, Version: n.spec.Version, BBox: n.spec.BBox}
		if n.router != nil {
			info.Loaded = true
			info.Edges = n.router.g.NumEdges()
			info.Source = n.router.g.Source
		}
		if n.err != nil {
			info.Error = n.err.Error()
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out
}

func contains4(b []float64, p Point, margin float64) bool {
	if len(b) != 4 {
		return false
	}
	w, so, e, n := b[0]-margin, b[1]-margin, b[2]+margin, b[3]+margin
	if p.Lat < so || p.Lat > n {
		return false
	}
	if w <= e {
		return p.Lon >= w && p.Lon <= e
	}
	return p.Lon >= w || p.Lon <= e // crosses the antimeridian
}

func area4(b []float64) float64 {
	if len(b) != 4 {
		return 1e9
	}
	w := b[2] - b[0]
	if w < 0 {
		w += 360
	}
	return w * (b[3] - b[1])
}

// ErrNoNetwork means no installed road network covers the points.
var ErrNoNetwork = errors.New("no installed road network covers these places")

// router loads (once) and returns a network's router.
func (s *Service) router(n *network) (*Router, error) {
	s.mu.Lock()
	if n.router != nil || n.err != nil {
		n.used = time.Now()
		r, err := n.router, n.err
		s.mu.Unlock()
		return r, err
	}
	if n.loading != nil {
		ch := n.loading
		s.mu.Unlock()
		<-ch
		s.mu.Lock()
		r, err := n.router, n.err
		s.mu.Unlock()
		return r, err
	}
	n.loading = make(chan struct{})
	s.mu.Unlock()

	g, err := Load(n.spec.Path)
	var r *Router
	if err == nil {
		r = NewRouter(g)
	}

	s.mu.Lock()
	n.router, n.err, n.used = r, err, time.Now()
	close(n.loading)
	n.loading = nil
	// Keep memory bounded: unload the least recently used networks.
	var loaded []*network
	for _, o := range s.nets {
		if o.router != nil {
			loaded = append(loaded, o)
		}
	}
	sort.Slice(loaded, func(i, j int) bool { return loaded[i].used.After(loaded[j].used) })
	for _, o := range loaded[min(len(loaded), maxLoaded):] {
		o.router = nil
	}
	s.mu.Unlock()
	return r, err
}

// Route finds a route through the points on the smallest installed network
// that covers them all and can join them to its roads.
func (s *Service) Route(ctx context.Context, pts []Point, m Mode) (*Route, NetworkInfo, error) {
	s.mu.Lock()
	var cands []*network
	var titles []string
	for _, n := range s.nets {
		titles = append(titles, n.spec.Title)
		ok := true
		for _, p := range pts {
			if !contains4(n.spec.BBox, p, 0.05) {
				ok = false
				break
			}
		}
		if ok {
			cands = append(cands, n)
		}
	}
	s.mu.Unlock()
	if len(titles) == 0 {
		return nil, NetworkInfo{}, errors.New("no road network is installed: get one for your area under Discover → Maps and navigation")
	}
	if len(cands) == 0 {
		sort.Strings(titles)
		return nil, NetworkInfo{}, fmt.Errorf("%w (installed: %s). Directions need every point inside one road network; get a larger region's network for trips between regions", ErrNoNetwork, strings.Join(titles, ", "))
	}
	sort.Slice(cands, func(i, j int) bool { return area4(cands[i].spec.BBox) < area4(cands[j].spec.BBox) })
	var firstErr error
	for _, n := range cands {
		r, err := s.router(n)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", n.spec.Title, err)
			}
			continue
		}
		rt, err := r.Route(ctx, pts, m)
		info := NetworkInfo{ResourceID: n.spec.ResourceID, Title: n.spec.Title, Version: n.spec.Version, BBox: n.spec.BBox, Loaded: true, Source: r.g.Source}
		if err == nil {
			return rt, info, nil
		}
		if firstErr == nil || errors.Is(firstErr, ErrNoNetwork) {
			firstErr = err
		}
		if ctx.Err() != nil {
			break
		}
	}
	return nil, NetworkInfo{}, firstErr
}

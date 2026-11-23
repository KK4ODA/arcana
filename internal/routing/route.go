package routing

// Shortest-time routes by A* over directed edges ("edge-based"), which lets
// turn restrictions, U-turn rules and turn costs be applied exactly: the
// cost of entering an edge depends on the edge you came from.

import (
	"context"
	"errors"
	"math"
	"sort"
	"sync"
)

// ErrNoRoute means the two places are not connected for the chosen mode.
var ErrNoRoute = errors.New("no route: the places are not connected by roads or paths open to this mode of travel in the installed road network")

const noParent = math.MaxUint32

// Router computes routes on a graph. It is safe for concurrent use.
type Router struct {
	g        *Graph
	maxSpeed [numModes]float64 // m/s, for the A* estimate
	pool     sync.Pool
}

// NewRouter prepares a router.
func NewRouter(g *Graph) *Router {
	r := &Router{g: g}
	for e := 0; e < len(g.From); e++ {
		for m := Mode(0); m < numModes; m++ {
			if g.Access[e]&(fwdBit(m)|bwdBit(m)) == 0 {
				continue
			}
			i := e*int(numModes) + int(m)
			// Speed after the avoidance factor, so the estimate never
			// exceeds a real cost.
			v := float64(g.Speed[i]) / 3.6 * 10 / math.Max(1, float64(g.Penalty[i]))
			if v > r.maxSpeed[m] {
				r.maxSpeed[m] = v
			}
		}
	}
	for m := range r.maxSpeed {
		if r.maxSpeed[m] == 0 {
			r.maxSpeed[m] = 1
		}
	}
	return r
}

// Graph returns the router's graph.
func (r *Router) Graph() *Graph { return r.g }

type search struct {
	dist   []float32
	parent []uint32
	stamp  []uint32
	gen    uint32
	heap   []item
}

type item struct {
	f float32
	s uint32
}

const finishItem = math.MaxUint32

func (r *Router) getSearch() *search {
	if s, ok := r.pool.Get().(*search); ok {
		s.gen++
		if s.gen == 0 {
			clear(s.stamp)
			s.gen = 1
		}
		s.heap = s.heap[:0]
		return s
	}
	n := 2 * len(r.g.From)
	return &search{dist: make([]float32, n), parent: make([]uint32, n), stamp: make([]uint32, n), gen: 1}
}

func (s *search) get(st uint32) (float32, bool) {
	if s.stamp[st] != s.gen {
		return 0, false
	}
	return s.dist[st], true
}

func (s *search) set(st uint32, d float32, parent uint32) {
	s.stamp[st] = s.gen
	s.dist[st] = d
	s.parent[st] = parent
}

func (s *search) push(it item) {
	s.heap = append(s.heap, it)
	i := len(s.heap) - 1
	for i > 0 {
		p := (i - 1) / 2
		if s.heap[p].f <= it.f {
			break
		}
		s.heap[i] = s.heap[p]
		i = p
	}
	s.heap[i] = it
}

func (s *search) pop() item {
	top := s.heap[0]
	last := s.heap[len(s.heap)-1]
	s.heap = s.heap[:len(s.heap)-1]
	n := len(s.heap)
	if n == 0 {
		return top
	}
	i := 0
	for {
		c := 2*i + 1
		if c >= n {
			break
		}
		if c+1 < n && s.heap[c+1].f < s.heap[c].f {
			c++
		}
		if last.f <= s.heap[c].f {
			break
		}
		s.heap[i] = s.heap[c]
		i = c
	}
	s.heap[i] = last
	return top
}

// state helpers: a state is edge<<1 | 1 when travelled To→From.
func stEdge(s uint32) uint32 { return s >> 1 }
func stRev(s uint32) bool    { return s&1 == 1 }

func (g *Graph) head(s uint32) uint32 {
	if stRev(s) {
		return g.From[stEdge(s)]
	}
	return g.To[stEdge(s)]
}

func (g *Graph) tail(s uint32) uint32 {
	if stRev(s) {
		return g.To[stEdge(s)]
	}
	return g.From[stEdge(s)]
}

func (g *Graph) allowed(s uint32, m Mode) bool {
	if stRev(s) {
		return g.Access[stEdge(s)]&bwdBit(m) != 0
	}
	return g.Access[stEdge(s)]&fwdBit(m) != 0
}

// cost is the time in seconds to travel a whole edge.
func (g *Graph) cost(e uint32, m Mode) float64 {
	i := int(e)*int(numModes) + int(m)
	v := float64(g.Speed[i]) / 3.6
	t := float64(g.Length[e]) / v * float64(g.Penalty[i]) / 10
	switch m {
	case Car:
		t += 8 * float64(g.Signals[e])
	case Bike:
		t += 4 * float64(g.Signals[e])
	}
	return t
}

// travelTime is the time without avoidance penalties, for display.
func (g *Graph) travelTime(e uint32, m Mode, frac float64) float64 {
	i := int(e)*int(numModes) + int(m)
	v := float64(g.Speed[i]) / 3.6
	t := float64(g.Length[e]) * frac / v
	if m == Car {
		t += 8 * float64(g.Signals[e]) * frac
	}
	return t
}

// inBearing is the direction of travel arriving at the head of state s.
func (g *Graph) inBearing(s uint32) uint8 {
	if stRev(s) {
		return g.bearStart[stEdge(s)] + 128
	}
	return g.bearEnd[stEdge(s)]
}

// outBearing is the direction of travel leaving the tail of state s.
func (g *Graph) outBearing(s uint32) uint8 {
	if stRev(s) {
		return g.bearEnd[stEdge(s)] + 128
	}
	return g.bearStart[stEdge(s)]
}

// turnAngle is the turn from state a into state b in degrees, -180..180,
// positive to the right.
func (g *Graph) turnAngle(a, b uint32) float64 {
	d := int8(g.outBearing(b) - g.inBearing(a))
	return float64(d) * 360 / 256
}

func turnCost(angle float64, m Mode) float64 {
	a := math.Abs(angle)
	switch m {
	case Car:
		switch {
		case a <= 25:
			return 0
		case a <= 60:
			return 1
		case a <= 120:
			return 4
		default:
			return 8
		}
	case Bike:
		switch {
		case a <= 60:
			return 0
		case a <= 120:
			return 1.5
		default:
			return 3
		}
	}
	return 0
}

// restrictions returns the turns out of edge e at node v that are
// mandatory (only) and forbidden for mode m.
func (g *Graph) restrictions(e, v uint32, m Mode, only, forbidden []uint32) ([]uint32, []uint32) {
	i := sort.Search(len(g.RFrom), func(i int) bool { return g.RFrom[i] >= e })
	for ; i < len(g.RFrom) && g.RFrom[i] == e; i++ {
		if g.RVia[i] != v || g.RKind[i]&(2<<m) == 0 {
			continue
		}
		if g.RKind[i]&1 == 1 {
			only = append(only, g.RTo[i])
		} else {
			forbidden = append(forbidden, g.RTo[i])
		}
	}
	return only, forbidden
}

func contains(list []uint32, v uint32) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// path is a route between two snapped points.
type path struct {
	from, to Snap
	states   []uint32 // directed edges in order; the first and last are partial
	direct   bool     // both points on one edge, no full edge between
	dirRev   bool     // direction on that edge when direct
	cost     float64
}

// fraction of edge e travelled when entering in direction rev up to along.
func frac(g *Graph, e uint32, along float64, rev bool) float64 {
	l := float64(g.Length[e])
	if l <= 0 {
		return 0
	}
	if rev {
		return (l - along) / l
	}
	return along / l
}

// shortest finds the quickest path from a to b.
func (r *Router) shortest(ctx context.Context, a, b Snap, m Mode) (path, error) {
	g := r.g
	s := r.getSearch()
	defer r.pool.Put(s)
	target := pt{b.Point.lat, b.Point.lon}
	h := func(node uint32) float32 {
		return float32(haversineE7(g.nodePt(node), target) / r.maxSpeed[m])
	}
	best := math.Inf(1)
	var bestPred uint32 = noParent
	bestRev := false
	direct := false

	// Seeds: leave the start point in either allowed direction.
	for _, rev := range []bool{false, true} {
		st := a.Edge<<1 | b2u(rev)
		if !g.allowed(st, m) {
			continue
		}
		rest := 1 - frac(g, a.Edge, a.Along, rev)
		c := float32(rest * g.cost(a.Edge, m))
		s.set(st, c, noParent)
		s.push(item{c + h(g.head(st)), st})
		// Both points on this edge, the target ahead.
		if a.Edge == b.Edge && ((!rev && b.Along >= a.Along) || (rev && b.Along <= a.Along)) {
			d := math.Abs(b.Along-a.Along) / math.Max(float64(g.Length[a.Edge]), 1e-9) * g.cost(a.Edge, m)
			if d < best {
				best, bestPred, bestRev, direct = d, noParent, rev, true
				s.push(item{float32(d), finishItem})
			}
		}
	}

	var only, forbidden []uint32
	pops := 0
	for len(s.heap) > 0 {
		it := s.pop()
		if it.s == finishItem {
			if float64(it.f) <= best+1e-3 {
				return r.collect(s, a, b, bestPred, bestRev, direct, best), nil
			}
			continue
		}
		cur := it.s
		gcur, _ := s.get(cur)
		if it.f > gcur+h(g.head(cur))+1e-3 {
			continue // stale
		}
		if pops++; pops&0xffff == 0 && ctx.Err() != nil {
			return path{}, ctx.Err()
		}
		v := g.head(cur)
		if g.NodeBlock[v]&(1<<m) != 0 {
			continue // a barrier closes the way on
		}
		e := stEdge(cur)
		only, forbidden = g.restrictions(e, v, m, only[:0], forbidden[:0])
		adj := g.Adj[g.First[v]:g.First[v+1]]
		deadEnd := true
		for _, a2 := range adj {
			if a2 != cur^1 && g.allowed(a2, m) {
				deadEnd = false
				break
			}
		}
		for _, next := range adj {
			ne := stEdge(next)
			if !g.allowed(next, m) {
				continue
			}
			if len(only) > 0 && !contains(only, ne) {
				continue
			}
			if contains(forbidden, ne) {
				continue
			}
			var tc float64
			if next == cur^1 {
				if !deadEnd {
					continue
				}
				tc = 20
				if m == Foot {
					tc = 0
				}
			} else {
				tc = turnCost(g.turnAngle(cur, next), m)
			}
			base := float64(gcur) + tc
			if ne == b.Edge {
				f := base + frac(g, ne, b.Along, stRev(next))*g.cost(ne, m)
				if f < best {
					best, bestPred, bestRev, direct = f, cur, stRev(next), false
					s.push(item{float32(f), finishItem})
				}
			}
			nc := float32(base + g.cost(ne, m))
			if old, ok := s.get(next); ok && old <= nc {
				continue
			}
			s.set(next, nc, cur)
			s.push(item{nc + h(g.head(next)), next})
		}
	}
	if !math.IsInf(best, 1) {
		return r.collect(s, a, b, bestPred, bestRev, direct, best), nil
	}
	return path{}, ErrNoRoute
}

func b2u(b bool) uint32 {
	if b {
		return 1
	}
	return 0
}

func (r *Router) collect(s *search, a, b Snap, pred uint32, lastRev, direct bool, cost float64) path {
	p := path{from: a, to: b, direct: direct, dirRev: lastRev, cost: cost}
	if direct {
		return p
	}
	for st := pred; st != noParent; st = s.parent[st] {
		p.states = append(p.states, st)
	}
	for i, j := 0, len(p.states)-1; i < j; i, j = i+1, j-1 {
		p.states[i], p.states[j] = p.states[j], p.states[i]
	}
	p.states = append(p.states, b.Edge<<1|b2u(lastRev))
	return p
}

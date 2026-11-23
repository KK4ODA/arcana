package routing

// Building a road network from an OSM extract, in two passes over the file:
//  1. ways and relations: keep the roads and paths any mode may use, and
//     turn restrictions;
//  2. nodes: keep the coordinates (and barrier and signal tags) of nodes
//     those roads use.
// Roads are then cut at junctions, barriers and ends into edges.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"
)

// BuildOptions tunes Build.
type BuildOptions struct {
	// Source is recorded in the file (for example the extract's URL).
	Source string
	// Progress reports the phase ("roads", "nodes", "network") and the
	// fraction of the input read in that phase.
	Progress func(phase string, frac float64)
	Workers  int
}

// BuildStats summarises a build.
type BuildStats struct {
	Ways, Nodes, Edges, Restrictions int
	MissingNodes                     int
}

type restriction struct {
	from, to int64 // way IDs
	via      int64 // node ID
	only     bool
	modes    uint8 // bit per mode
}

// Source opens the extract; Build reads it twice.
type Source func() (io.ReadCloser, int64, error)

// Build reads an OSM extract and returns the road network.
func Build(ctx context.Context, open Source, opt BuildOptions) (*Graph, BuildStats, error) {
	var st BuildStats
	progress := func(phase string, size int64) func(int64) {
		return func(n int64) {
			if opt.Progress != nil && size > 0 {
				opt.Progress(phase, math.Min(1, float64(n)/float64(size)))
			}
		}
	}

	// ------------------------------------------------ pass 1: roads
	attrIndex := map[wayAttrs]uint32{}
	var attrs []wayAttrs
	var wayIDs []int64
	var wayAttr []uint32
	var wayStart []uint32
	var refs []int64
	var restr []restriction
	rc, size, err := open()
	if err != nil {
		return nil, st, err
	}
	err = ReadPBF(ctx, rc, Handlers{
		Workers:  opt.Workers,
		Progress: progress("roads", size),
		Way: func(w *Way) {
			if len(w.Refs) < 2 {
				return
			}
			a, ok := wayRouting(w.Tags)
			if !ok {
				return
			}
			idx, found := attrIndex[a]
			if !found {
				idx = uint32(len(attrs))
				attrIndex[a] = idx
				attrs = append(attrs, a)
			}
			wayIDs = append(wayIDs, w.ID)
			wayAttr = append(wayAttr, idx)
			wayStart = append(wayStart, uint32(len(refs)))
			refs = append(refs, w.Refs...)
		},
		Relation: func(r *Relation) {
			if res, ok := parseRestriction(r); ok {
				restr = append(restr, res)
			}
		},
	})
	rc.Close()
	if err != nil {
		return nil, st, err
	}
	if len(wayIDs) == 0 {
		return nil, st, errors.New("the extract contains no roads")
	}
	if len(refs) >= math.MaxUint32 {
		return nil, st, errors.New("the extract is too large for one road network")
	}
	wayStart = append(wayStart, uint32(len(refs)))
	st.Ways = len(wayIDs)

	// Every node the roads use, and how often.
	ids := slices.Clone(refs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	idx := func(id int64) int {
		i, ok := slices.BinarySearch(ids, id)
		if !ok {
			return -1
		}
		return i
	}
	const (
		usedTwice uint8 = 1 << iota
		usedOnce
		endpoint
		barrierCar
		barrierBike
		barrierFoot
		signal
		present
	)
	flags := make([]uint8, len(ids))
	for w := 0; w < len(wayIDs); w++ {
		rs := refs[wayStart[w]:wayStart[w+1]]
		for j, id := range rs {
			i := idx(id)
			if flags[i]&usedOnce != 0 {
				flags[i] |= usedTwice
			}
			flags[i] |= usedOnce
			if j == 0 || j == len(rs)-1 {
				flags[i] |= endpoint
			}
		}
	}
	for _, r := range restr {
		if i := idx(r.via); i >= 0 {
			flags[i] |= endpoint
		}
	}

	// ------------------------------------------------ pass 2: nodes
	lat := make([]int32, len(ids))
	lon := make([]int32, len(ids))
	rc, size, err = open()
	if err != nil {
		return nil, st, err
	}
	err = ReadPBF(ctx, rc, Handlers{
		Workers:  opt.Workers,
		Progress: progress("nodes", size),
		NodeTags: true,
		Nodes: func(b *NodeBatch) {
			for k, id := range b.IDs {
				i := idx(id)
				if i < 0 {
					continue
				}
				lat[i], lon[i] = b.Lat[k], b.Lon[k]
				flags[i] |= present
				if tags := b.Tags[k]; tags != nil {
					if barrierBlocks(tags, Car) {
						flags[i] |= barrierCar
					}
					if barrierBlocks(tags, Bike) {
						flags[i] |= barrierBike
					}
					if barrierBlocks(tags, Foot) {
						flags[i] |= barrierFoot
					}
					if tagValue(tags, "highway") == "traffic_signals" {
						flags[i] |= signal
					}
				}
			}
		},
	})
	rc.Close()
	if err != nil {
		return nil, st, err
	}
	if opt.Progress != nil {
		opt.Progress("network", 0)
	}

	// ------------------------------------------------ edges
	g := &Graph{Source: opt.Source, Strings: []string{""}}
	strIndex := map[string]uint32{"": 0}
	intern := func(s string) uint32 {
		if i, ok := strIndex[s]; ok {
			return i
		}
		i := uint32(len(g.Strings))
		strIndex[s] = i
		g.Strings = append(g.Strings, s)
		return i
	}
	graphID := make([]int32, len(ids))
	for i := range graphID {
		graphID[i] = -1
	}
	nodeOf := func(i int) uint32 {
		if graphID[i] < 0 {
			graphID[i] = int32(len(g.Lat))
			g.Lat = append(g.Lat, lat[i])
			g.Lon = append(g.Lon, lon[i])
			var blk uint8
			if flags[i]&barrierCar != 0 {
				blk |= 1 << Car
			}
			if flags[i]&barrierBike != 0 {
				blk |= 1 << Bike
			}
			if flags[i]&barrierFoot != 0 {
				blk |= 1 << Foot
			}
			g.NodeBlock = append(g.NodeBlock, blk)
		}
		return uint32(graphID[i])
	}
	isJunction := func(i int) bool {
		return flags[i]&(usedTwice|endpoint|barrierCar|barrierBike|barrierFoot) != 0
	}
	wayEdges := make([][]uint32, len(wayIDs)) // only filled for ways in restrictions
	inRestr := map[int64]bool{}
	for _, r := range restr {
		inRestr[r.from], inRestr[r.to] = true, true
	}
	g.ShapeFirst = append(g.ShapeFirst, 0)
	for w := 0; w < len(wayIDs); w++ {
		if err := ctx.Err(); err != nil {
			return nil, st, err
		}
		a := attrs[wayAttr[w]]
		name, dest := intern(a.name), intern(a.dest)
		rs := refs[wayStart[w]:wayStart[w+1]]
		start := -1 // index in rs of the current edge's first node
		var shape []int
		length := 0.0
		signals := 0
		prev := -1
		flush := func(end int) {
			ei := uint32(len(g.From))
			g.From = append(g.From, nodeOf(idx(rs[start])))
			g.To = append(g.To, nodeOf(idx(rs[end])))
			g.Length = append(g.Length, float32(length))
			g.Class = append(g.Class, a.class)
			g.Access = append(g.Access, a.access)
			g.Flags = append(g.Flags, a.flags)
			g.Signals = append(g.Signals, uint8(min(signals, 255)))
			g.Speed = append(g.Speed, a.speed[:]...)
			g.Penalty = append(g.Penalty, a.penalty[:]...)
			g.Name = append(g.Name, name)
			g.Dest = append(g.Dest, dest)
			for _, s := range shape {
				g.ShapeLat = append(g.ShapeLat, lat[s])
				g.ShapeLon = append(g.ShapeLon, lon[s])
			}
			g.ShapeFirst = append(g.ShapeFirst, uint32(len(g.ShapeLat)))
			if inRestr[wayIDs[w]] {
				wayEdges[w] = append(wayEdges[w], ei)
			}
		}
		for j := range rs {
			i := idx(rs[j])
			if flags[i]&present == 0 {
				// A node outside the extract: the road is cut here.
				st.MissingNodes++
				if start >= 0 && prev >= 0 && prev != start {
					flags[idx(rs[prev])] |= endpoint
					flush(prev)
				}
				start, prev, shape, length, signals = -1, -1, shape[:0], 0, 0
				continue
			}
			if start < 0 {
				start, prev = j, j
				flags[i] |= endpoint // an end of a piece is a node of the network
				continue
			}
			if rs[j] == rs[prev] {
				continue // repeated node
			}
			length += haversineE7(pt{lat[idx(rs[prev])], lon[idx(rs[prev])]}, pt{lat[i], lon[i]})
			if flags[i]&signal != 0 {
				signals++
			}
			prev = j
			if isJunction(i) || j == len(rs)-1 {
				flush(j)
				start, shape, length, signals = j, shape[:0], 0, 0
				continue
			}
			shape = append(shape, i)
		}
		if start >= 0 && prev > start {
			flush(prev) // the way ended on a repeated node
		}
	}
	if len(g.From) == 0 {
		return nil, st, errors.New("the extract contains no usable roads")
	}
	if len(g.Lat) >= math.MaxUint32/2 || len(g.From) >= math.MaxUint32/2 {
		return nil, st, errors.New("the extract is too large for one road network")
	}

	// Turn restrictions.
	wayIndex := map[int64]int{}
	for w, id := range wayIDs {
		if inRestr[id] {
			wayIndex[id] = w
		}
	}
	type rrec struct {
		from, via, to uint32
		kind          uint8
	}
	var rr []rrec
	for _, r := range restr {
		fw, ok1 := wayIndex[r.from]
		tw, ok2 := wayIndex[r.to]
		vi := idx(r.via)
		if !ok1 || !ok2 || vi < 0 || graphID[vi] < 0 {
			continue
		}
		via := uint32(graphID[vi])
		kind := r.modes << 1
		if r.only {
			kind |= 1
		}
		for _, fe := range wayEdges[fw] {
			if g.From[fe] != via && g.To[fe] != via {
				continue
			}
			for _, te := range wayEdges[tw] {
				if (g.From[te] == via || g.To[te] == via) && (te != fe || r.from == r.to) {
					rr = append(rr, rrec{fe, via, te, kind})
				}
			}
		}
	}
	sort.Slice(rr, func(i, j int) bool { return rr[i].from < rr[j].from })
	for _, r := range rr {
		g.RFrom = append(g.RFrom, r.from)
		g.RVia = append(g.RVia, r.via)
		g.RTo = append(g.RTo, r.to)
		g.RKind = append(g.RKind, r.kind)
	}

	// What follows needs only the graph: let the collector have the
	// per-node working arrays, which are as large as the graph itself.
	refs, ids, flags, lat, lon, graphID, wayEdges, wayIDs, wayStart = nil, nil, nil, nil, nil, nil, nil, nil, nil
	attrIndex, attrs, strIndex = nil, nil, nil

	// Adjacency (CSR).
	n := len(g.Lat)
	deg := make([]uint32, n+1)
	for e := range g.From {
		deg[g.From[e]]++
		deg[g.To[e]]++
	}
	g.First = make([]uint32, n+1)
	for i := 0; i < n; i++ {
		g.First[i+1] = g.First[i] + deg[i]
	}
	g.Adj = make([]uint32, 2*len(g.From))
	fill := slices.Clone(g.First[:n])
	for e := range g.From {
		f, t := g.From[e], g.To[e]
		g.Adj[fill[f]] = uint32(e) << 1
		fill[f]++
		g.Adj[fill[t]] = uint32(e)<<1 | 1
		fill[t]++
	}

	markSmallNetworks(g)

	// Bounding box.
	w, s, e, nn := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for i := range g.Lat {
		la, lo := float64(g.Lat[i])*e7, float64(g.Lon[i])*e7
		w, e = math.Min(w, lo), math.Max(e, lo)
		s, nn = math.Min(s, la), math.Max(nn, la)
	}
	g.BBox = [4]float64{w, s, e, nn}
	st.Nodes, st.Edges, st.Restrictions = len(g.Lat), len(g.From), len(g.RFrom)
	return g, st, nil
}

// smallNetwork is the size (in edges) below which a network not connected
// to anything larger is marked small.
const smallNetwork = 50

// markSmallNetworks flags edges in small connected components.
func markSmallNetworks(g *Graph) {
	n := len(g.Lat)
	comp := make([]int32, n)
	for i := range comp {
		comp[i] = -1
	}
	var sizes []int
	stack := []uint32{}
	for s := 0; s < n; s++ {
		if comp[s] >= 0 {
			continue
		}
		c := int32(len(sizes))
		size := 0
		comp[s] = c
		stack = append(stack[:0], uint32(s))
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, a := range g.Adj[g.First[v]:g.First[v+1]] {
				size++
				e := a >> 1
				o := g.To[e]
				if a&1 == 1 {
					o = g.From[e]
				}
				if comp[o] < 0 {
					comp[o] = c
					stack = append(stack, o)
				}
			}
		}
		sizes = append(sizes, size/2)
	}
	g.Small = make([]uint8, len(g.From))
	for e := range g.From {
		if sizes[comp[g.From[e]]] < smallNetwork {
			g.Small[e] = 1
		}
	}
}

// parseRestriction reads a turn restriction relation with a via node.
func parseRestriction(r *Relation) (restriction, bool) {
	if tagValue(r.Tags, "type") != "restriction" {
		return restriction{}, false
	}
	var res restriction
	value := tagValue(r.Tags, "restriction")
	res.modes = 1<<Car | 1<<Bike
	if value == "" {
		for _, k := range []string{"restriction:motorcar", "restriction:motor_vehicle"} {
			if v := tagValue(r.Tags, k); v != "" {
				value, res.modes = v, 1<<Car
			}
		}
		if v := tagValue(r.Tags, "restriction:bicycle"); v != "" && value == "" {
			value, res.modes = v, 1<<Bike
		}
	}
	switch {
	case strings.HasPrefix(value, "no_"):
	case strings.HasPrefix(value, "only_"):
		res.only = true
	default:
		return res, false
	}
	for _, ex := range strings.Split(tagValue(r.Tags, "except"), ";") {
		switch strings.TrimSpace(ex) {
		case "bicycle":
			res.modes &^= 1 << Bike
		case "motorcar", "motor_vehicle":
			res.modes &^= 1 << Car
		}
	}
	var froms, tos, vias int
	for _, m := range r.Members {
		switch {
		case m.Role == "from" && m.Type == 1:
			res.from = m.Ref
			froms++
		case m.Role == "to" && m.Type == 1:
			res.to = m.Ref
			tos++
		case m.Role == "via" && m.Type == 0:
			res.via = m.Ref
			vias++
		case m.Role == "via":
			return res, false // via ways are not supported
		}
	}
	if froms != 1 || tos != 1 || vias != 1 || res.modes == 0 {
		return res, false
	}
	return res, true
}

// String describes the stats for logs.
func (s BuildStats) String() string {
	return fmt.Sprintf("%d roads, %d junctions, %d road sections, %d turn restrictions", s.Ways, s.Nodes, s.Edges, s.Restrictions)
}

package routing

import (
	"math"
	"sort"
)

// spatialGrid finds edges near a point: a sorted list of grid cells with
// the edges whose segments cross each cell.
type spatialGrid struct {
	keys  []uint64
	start []uint32
	edges []uint32
}

// cellE7 is the grid cell size: 0.005° (about 550 m north–south).
const cellE7 = 50000

func cellOf(v int32) int32 {
	if v >= 0 {
		return v / cellE7
	}
	return (v - cellE7 + 1) / cellE7
}

func cellKey(y, x int32) uint64 { return uint64(uint32(y))<<32 | uint64(uint32(x)) }

func buildGrid(g *Graph) *spatialGrid {
	type pair struct {
		key  uint64
		edge uint32
	}
	var pairs []pair
	var buf []pt
	for e := uint32(0); e < uint32(len(g.From)); e++ {
		buf = g.points(e, buf[:0])
		for i := 1; i < len(buf); i++ {
			a, b := buf[i-1], buf[i]
			y0, y1 := cellOf(min(a.lat, b.lat)), cellOf(max(a.lat, b.lat))
			x0, x1 := cellOf(min(a.lon, b.lon)), cellOf(max(a.lon, b.lon))
			if (y1-y0+1)*(x1-x0+1) > 64 {
				// A very long straight segment (a ferry-like line or bad
				// data): index its ends only.
				pairs = append(pairs, pair{cellKey(cellOf(a.lat), cellOf(a.lon)), e}, pair{cellKey(cellOf(b.lat), cellOf(b.lon)), e})
				continue
			}
			for y := y0; y <= y1; y++ {
				for x := x0; x <= x1; x++ {
					pairs = append(pairs, pair{cellKey(y, x), e})
				}
			}
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].key != pairs[j].key {
			return pairs[i].key < pairs[j].key
		}
		return pairs[i].edge < pairs[j].edge
	})
	sg := &spatialGrid{}
	for i, p := range pairs {
		if i > 0 && p == pairs[i-1] {
			continue
		}
		if len(sg.keys) == 0 || sg.keys[len(sg.keys)-1] != p.key {
			sg.keys = append(sg.keys, p.key)
			sg.start = append(sg.start, uint32(len(sg.edges)))
		}
		sg.edges = append(sg.edges, p.edge)
	}
	sg.start = append(sg.start, uint32(len(sg.edges)))
	return sg
}

func (sg *spatialGrid) cell(y, x int32) []uint32 {
	k := cellKey(y, x)
	i := sort.Search(len(sg.keys), func(i int) bool { return sg.keys[i] >= k })
	if i == len(sg.keys) || sg.keys[i] != k {
		return nil
	}
	return sg.edges[sg.start[i]:sg.start[i+1]]
}

// Snap is a point on the road network.
type Snap struct {
	Edge uint32
	// Along is the distance in metres from the edge's From node.
	Along float64
	// Seg is the index of the segment holding the point (0 = first).
	Seg int
	// Point is the snapped position.
	Point pt
	// Dist is how far the original position was from the road, in metres.
	Dist float64
}

// projection converts degrees×1e7 to local metres around a latitude.
type projection struct{ kx, ky float64 }

func newProjection(lat int32) projection {
	ky := earthRadius * math.Pi / 180 * e7
	return projection{kx: ky * math.Cos(toRad(lat)), ky: ky}
}

// SnapPoint finds the nearest point on an edge the mode may use, within
// maxDist metres. Edges in small isolated networks (a car park, a closed
// estate) are passed over when a connected road is nearly as close, so a
// route is not attempted from a place it cannot leave.
func (g *Graph) SnapPoint(p pt, m Mode, maxDist float64) (Snap, bool) {
	pr := newProjection(p.lat)
	cy, cx := cellOf(p.lat), cellOf(p.lon)
	cellM := float64(cellE7) * pr.ky
	if pr.kx < pr.ky {
		cellM = float64(cellE7) * pr.kx
	}
	maxRing := int(maxDist/cellM) + 1
	best := Snap{Dist: math.Inf(1)}
	bestBig := Snap{Dist: math.Inf(1)}
	seen := map[uint32]bool{}
	var buf []pt
	bits := fwdBit(m) | bwdBit(m)
	for r := 0; r <= maxRing; r++ {
		for y := cy - int32(r); y <= cy+int32(r); y++ {
			for x := cx - int32(r); x <= cx+int32(r); x++ {
				if r > 0 && y != cy-int32(r) && y != cy+int32(r) && x != cx-int32(r) && x != cx+int32(r) {
					continue // inner cells were searched in earlier rings
				}
				for _, e := range g.grid.cell(y, x) {
					if seen[e] || g.Access[e]&bits == 0 {
						continue
					}
					seen[e] = true
					buf = g.points(e, buf[:0])
					along := 0.0
					for i := 1; i < len(buf); i++ {
						a, b := buf[i-1], buf[i]
						ax, ay := float64(a.lon-p.lon)*pr.kx, float64(a.lat-p.lat)*pr.ky
						bx, by := float64(b.lon-p.lon)*pr.kx, float64(b.lat-p.lat)*pr.ky
						dx, dy := bx-ax, by-ay
						l2 := dx*dx + dy*dy
						t := 0.0
						if l2 > 0 {
							t = math.Max(0, math.Min(1, -(ax*dx+ay*dy)/l2))
						}
						d := math.Hypot(ax+t*dx, ay+t*dy)
						seg := haversineE7(a, b)
						if d < best.Dist || (g.Small[e] == 0 && d < bestBig.Dist) {
							s := Snap{Edge: e, Along: along + t*seg, Seg: i - 1, Dist: d,
								Point: pt{a.lat + int32(math.Round(t*float64(b.lat-a.lat))), a.lon + int32(math.Round(t*float64(b.lon-a.lon)))}}
							if d < best.Dist {
								best = s
							}
							if g.Small[e] == 0 && d < bestBig.Dist {
								bestBig = s
							}
						}
						along += seg
					}
				}
			}
		}
		// Cells beyond ring r are at least r cells away.
		reach := float64(r) * cellM
		if bestBig.Dist <= reach || (best.Dist <= reach && reach > best.Dist+smallNetworkSlack) || reach > maxDist {
			break
		}
	}
	if !math.IsInf(bestBig.Dist, 1) && bestBig.Dist <= maxDist && bestBig.Dist <= best.Dist+smallNetworkSlack {
		return clampAlong(g, bestBig), true
	}
	if best.Dist <= maxDist {
		return clampAlong(g, best), true
	}
	return Snap{}, false
}

// smallNetworkSlack is how much farther a connected road may be than a
// road in a small isolated network and still be preferred.
const smallNetworkSlack = 250.0

func clampAlong(g *Graph, s Snap) Snap {
	l := float64(g.Length[s.Edge])
	s.Along = math.Max(0, math.Min(l, s.Along))
	return s
}

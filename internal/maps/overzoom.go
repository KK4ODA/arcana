package maps

import (
	"container/list"
	"encoding/binary"
	"errors"
	"math"
	"sync"
)

// Overzoom derives vector tile (z, x, y) from the uncompressed MVT of its
// ancestor at zoom pz: geometry is scaled into the child's coordinate space
// and clipped to the child tile plus a small buffer. It lets a low-detail
// map (a world overview) fill areas that a more detailed map set does not
// cover when the viewer zooms past the overview's own detail level.
//
// Only geometry is rewritten. Each layer's key and value tables, feature IDs
// and attribute tags are copied unchanged, so no attribute decoding (and no
// general protobuf library) is needed. ADR 0015 records why this small
// codec exists instead of a dependency.
func Overzoom(data []byte, pz, z uint8, x, y uint32) ([]byte, error) {
	if pz > z || z-pz > 16 {
		return nil, errors.New("invalid overzoom")
	}
	dz := z - pz
	if dz == 0 {
		return data, nil
	}
	scale := int64(1) << dz
	ox := int64(x - (x>>dz)<<dz)
	oy := int64(y - (y>>dz)<<dz)
	p := pbuf{b: data}
	var out []byte
	for !p.eof() {
		start := p.i
		f, w, err := p.key()
		if err != nil {
			return nil, err
		}
		if f != 3 || w != 2 {
			if err := p.skip(w); err != nil {
				return nil, err
			}
			out = append(out, data[start:p.i]...)
			continue
		}
		lb, err := p.bytes()
		if err != nil {
			return nil, err
		}
		layer, err := overzoomLayer(lb, scale, ox, oy)
		if err != nil {
			return nil, err
		}
		if layer != nil {
			out = appendBytesField(out, 3, layer)
		}
	}
	return out, nil
}

// overzoomLayer rewrites one layer; it returns nil when no feature remains.
func overzoomLayer(b []byte, scale, ox, oy int64) ([]byte, error) {
	// The extent is needed before features are transformed.
	extent := int64(4096)
	p := pbuf{b: b}
	for !p.eof() {
		f, w, err := p.key()
		if err != nil {
			return nil, err
		}
		if f == 5 && w == 0 {
			v, err := p.varint()
			if err != nil {
				return nil, err
			}
			if v == 0 || v > 1<<16 {
				return nil, errTile
			}
			extent = int64(v)
			continue
		}
		if err := p.skip(w); err != nil {
			return nil, err
		}
	}
	buf := extent / 16
	c := clipper{min: -buf, max: extent + buf, scale: scale, dx: ox * extent, dy: oy * extent}

	p = pbuf{b: b}
	var out []byte
	kept := 0
	for !p.eof() {
		start := p.i
		f, w, err := p.key()
		if err != nil {
			return nil, err
		}
		if f != 2 || w != 2 {
			if err := p.skip(w); err != nil {
				return nil, err
			}
			out = append(out, b[start:p.i]...)
			continue
		}
		fb, err := p.bytes()
		if err != nil {
			return nil, err
		}
		feat, err := c.feature(fb)
		if err != nil {
			return nil, err
		}
		if feat != nil {
			out = appendBytesField(out, 2, feat)
			kept++
		}
	}
	if kept == 0 {
		return nil, nil
	}
	return out, nil
}

type clipper struct {
	min, max      int64
	scale, dx, dy int64
}

// feature rewrites one feature's geometry; nil means it was clipped away.
func (c *clipper) feature(b []byte) ([]byte, error) {
	p := pbuf{b: b}
	var other []byte
	var geom []uint32
	var typ uint64
	for !p.eof() {
		start := p.i
		f, w, err := p.key()
		if err != nil {
			return nil, err
		}
		switch {
		case f == 3 && w == 0:
			if typ, err = p.varint(); err != nil {
				return nil, err
			}
		case f == 4 && w == 2:
			gb, err := p.bytes()
			if err != nil {
				return nil, err
			}
			if geom, err = packed(gb); err != nil {
				return nil, err
			}
		default:
			if err := p.skip(w); err != nil {
				return nil, err
			}
			other = append(other, b[start:p.i]...)
		}
	}
	parts, err := decodeGeometry(geom)
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		for i := range part {
			part[i] = pt{part[i].x*c.scale - c.dx, part[i].y*c.scale - c.dy}
		}
	}
	var enc []uint32
	switch typ {
	case 1:
		enc = c.points(parts)
	case 2:
		enc = c.lines(parts)
	case 3:
		enc = c.polygons(parts)
	default:
		return nil, nil
	}
	if len(enc) == 0 {
		return nil, nil
	}
	out := other
	out = binary.AppendUvarint(out, 3<<3|0)
	out = binary.AppendUvarint(out, typ)
	var packedGeom []byte
	for _, v := range enc {
		packedGeom = binary.AppendUvarint(packedGeom, uint64(v))
	}
	return appendBytesField(out, 4, packedGeom), nil
}

type pt struct{ x, y int64 }

// decodeGeometry splits an MVT command stream into parts: each MoveTo starts
// a new part (for points every point is its own part).
func decodeGeometry(g []uint32) ([][]pt, error) {
	var parts [][]pt
	var cx, cy int64
	for i := 0; i < len(g); {
		cmd, count := g[i]&7, int(g[i]>>3)
		i++
		switch cmd {
		case 1, 2:
			if count > (len(g)-i)/2 {
				return nil, errTile
			}
			for k := 0; k < count; k++ {
				cx += zigzag(g[i])
				cy += zigzag(g[i+1])
				i += 2
				if cmd == 1 {
					parts = append(parts, []pt{{cx, cy}})
				} else {
					if len(parts) == 0 {
						return nil, errTile
					}
					parts[len(parts)-1] = append(parts[len(parts)-1], pt{cx, cy})
				}
			}
		case 7:
			// ClosePath: rings are implicitly closed below.
		default:
			return nil, errTile
		}
	}
	return parts, nil
}

type encoder struct {
	out    []uint32
	cx, cy int64
}

func (e *encoder) cmd(id, count int) { e.out = append(e.out, uint32(id&7|count<<3)) }

func (e *encoder) point(p pt) {
	e.out = append(e.out, zz(p.x-e.cx), zz(p.y-e.cy))
	e.cx, e.cy = p.x, p.y
}

func zz(v int64) uint32 { return uint32((v << 1) ^ (v >> 63)) }

func (c *clipper) inside(p pt) bool {
	return p.x >= c.min && p.x <= c.max && p.y >= c.min && p.y <= c.max
}

func (c *clipper) points(parts [][]pt) []uint32 {
	var keep []pt
	for _, part := range parts {
		for _, p := range part {
			if c.inside(p) {
				keep = append(keep, p)
			}
		}
	}
	if len(keep) == 0 {
		return nil
	}
	var e encoder
	e.cmd(1, len(keep))
	for _, p := range keep {
		e.point(p)
	}
	return e.out
}

// clipSegment clips a segment to the box (Liang-Barsky).
func (c *clipper) clipSegment(a, b pt) (pt, pt, bool) {
	t0, t1 := 0.0, 1.0
	dx, dy := float64(b.x-a.x), float64(b.y-a.y)
	lo, hi := float64(c.min), float64(c.max)
	ax, ay := float64(a.x), float64(a.y)
	for _, e := range [4][2]float64{{-dx, ax - lo}, {dx, hi - ax}, {-dy, ay - lo}, {dy, hi - ay}} {
		pp, q := e[0], e[1]
		if pp == 0 {
			if q < 0 {
				return a, b, false
			}
			continue
		}
		r := q / pp
		if pp < 0 {
			if r > t1 {
				return a, b, false
			}
			if r > t0 {
				t0 = r
			}
		} else {
			if r < t0 {
				return a, b, false
			}
			if r < t1 {
				t1 = r
			}
		}
	}
	at := func(t float64) pt {
		return pt{int64(math.Round(ax + t*dx)), int64(math.Round(ay + t*dy))}
	}
	return at(t0), at(t1), true
}

func (c *clipper) lines(parts [][]pt) []uint32 {
	var out [][]pt
	for _, line := range parts {
		var cur []pt
		for i := 0; i+1 < len(line); i++ {
			a, b, ok := c.clipSegment(line[i], line[i+1])
			if !ok {
				if len(cur) > 1 {
					out = append(out, cur)
				}
				cur = nil
				continue
			}
			if len(cur) == 0 || cur[len(cur)-1] != a {
				if len(cur) > 1 {
					out = append(out, cur)
				}
				cur = []pt{a}
			}
			if b != cur[len(cur)-1] {
				cur = append(cur, b)
			}
			// The segment left the box: end this part.
			if b != line[i+1] {
				if len(cur) > 1 {
					out = append(out, cur)
				}
				cur = nil
			}
		}
		if len(cur) > 1 {
			out = append(out, cur)
		}
	}
	var e encoder
	for _, l := range out {
		e.cmd(1, 1)
		e.point(l[0])
		e.cmd(2, len(l)-1)
		for _, p := range l[1:] {
			e.point(p)
		}
	}
	return e.out
}

func ringArea(r []pt) int64 {
	var a int64
	for i := range r {
		j := (i + 1) % len(r)
		a += r[i].x*r[j].y - r[j].x*r[i].y
	}
	return a
}

// clipRing clips a ring to the box (Sutherland-Hodgman); orientation is
// preserved.
func (c *clipper) clipRing(r []pt) []pt {
	if len(r) > 1 && r[0] == r[len(r)-1] {
		r = r[:len(r)-1]
	}
	lo, hi := float64(c.min), float64(c.max)
	type fp struct{ x, y float64 }
	in := make([]fp, len(r))
	for i, p := range r {
		in[i] = fp{float64(p.x), float64(p.y)}
	}
	edges := []struct {
		inside func(fp) bool
		cross  func(a, b fp) fp
	}{
		{func(p fp) bool { return p.x >= lo }, func(a, b fp) fp { return fp{lo, a.y + (b.y-a.y)*(lo-a.x)/(b.x-a.x)} }},
		{func(p fp) bool { return p.x <= hi }, func(a, b fp) fp { return fp{hi, a.y + (b.y-a.y)*(hi-a.x)/(b.x-a.x)} }},
		{func(p fp) bool { return p.y >= lo }, func(a, b fp) fp { return fp{a.x + (b.x-a.x)*(lo-a.y)/(b.y-a.y), lo} }},
		{func(p fp) bool { return p.y <= hi }, func(a, b fp) fp { return fp{a.x + (b.x-a.x)*(hi-a.y)/(b.y-a.y), hi} }},
	}
	for _, e := range edges {
		if len(in) == 0 {
			break
		}
		var next []fp
		prev := in[len(in)-1]
		for _, cur := range in {
			ci, pi := e.inside(cur), e.inside(prev)
			if ci {
				if !pi {
					next = append(next, e.cross(prev, cur))
				}
				next = append(next, cur)
			} else if pi {
				next = append(next, e.cross(prev, cur))
			}
			prev = cur
		}
		in = next
	}
	var out []pt
	for _, p := range in {
		q := pt{int64(math.Round(p.x)), int64(math.Round(p.y))}
		if len(out) == 0 || out[len(out)-1] != q {
			out = append(out, q)
		}
	}
	for len(out) > 1 && out[0] == out[len(out)-1] {
		out = out[:len(out)-1]
	}
	return out
}

func (c *clipper) polygons(rings [][]pt) []uint32 {
	var e encoder
	exteriorKept := false
	for _, r := range rings {
		a := ringArea(r)
		if a == 0 {
			continue
		}
		exterior := a > 0
		if !exterior && !exteriorKept {
			continue // its polygon was clipped away
		}
		cr := c.clipRing(r)
		ok := len(cr) >= 3
		if ok {
			ca := ringArea(cr)
			ok = ca != 0 && (ca > 0) == exterior
		}
		if exterior {
			exteriorKept = ok
		}
		if !ok {
			continue
		}
		e.cmd(1, 1)
		e.point(cr[0])
		e.cmd(2, len(cr)-1)
		for _, p := range cr[1:] {
			e.point(p)
		}
		e.cmd(7, 1)
	}
	return e.out
}

func appendBytesField(out []byte, field int, b []byte) []byte {
	out = binary.AppendUvarint(out, uint64(field)<<3|2)
	out = binary.AppendUvarint(out, uint64(len(b)))
	return append(out, b...)
}

// tileCache is a small LRU of derived tiles.
type tileCache struct {
	mu    sync.Mutex
	max   int
	ll    *list.List
	items map[string]*list.Element
}

type tileCacheEntry struct {
	key  string
	data []byte
}

func newTileCache(max int) *tileCache {
	return &tileCache{max: max, ll: list.New(), items: map[string]*list.Element{}}
}

func (c *tileCache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		c.ll.MoveToFront(e)
		return e.Value.(*tileCacheEntry).data, true
	}
	return nil, false
}

func (c *tileCache) put(key string, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[key]; ok {
		e.Value.(*tileCacheEntry).data = data
		c.ll.MoveToFront(e)
		return
	}
	c.items[key] = c.ll.PushFront(&tileCacheEntry{key: key, data: data})
	for c.ll.Len() > c.max {
		e := c.ll.Back()
		c.ll.Remove(e)
		delete(c.items, e.Value.(*tileCacheEntry).key)
	}
}

func (c *tileCache) clear() {
	c.mu.Lock()
	c.ll.Init()
	c.items = map[string]*list.Element{}
	c.mu.Unlock()
}

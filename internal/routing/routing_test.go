package routing

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ---------------------------------------------------------------- a PBF writer for tests

type tnode struct {
	id       int64
	lat, lon float64
	tags     map[string]string
}

type tway struct {
	id   int64
	refs []int64
	tags map[string]string
}

type trel struct {
	id      int64
	members []Member
	tags    map[string]string
}

type enc struct{ b []byte }

func (e *enc) key(f, wire int) { e.b = binary.AppendUvarint(e.b, uint64(f<<3|wire)) }
func (e *enc) varint(f int, v uint64) {
	e.key(f, 0)
	e.b = binary.AppendUvarint(e.b, v)
}
func (e *enc) bytes(f int, v []byte) {
	e.key(f, 2)
	e.b = binary.AppendUvarint(e.b, uint64(len(v)))
	e.b = append(e.b, v...)
}
func (e *enc) packed(f int, vs []uint64) {
	var p []byte
	for _, v := range vs {
		p = binary.AppendUvarint(p, v)
	}
	e.bytes(f, p)
}
func zz(v int64) uint64 { return uint64(v<<1) ^ uint64(v>>63) }

type strtab struct {
	idx  map[string]uint64
	list []string
}

func (s *strtab) id(v string) uint64 {
	if s.idx == nil {
		s.idx = map[string]uint64{"": 0}
		s.list = []string{""}
	}
	if i, ok := s.idx[v]; ok {
		return i
	}
	i := uint64(len(s.list))
	s.idx[v] = i
	s.list = append(s.list, v)
	return i
}

func sortedKeys(m map[string]string) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func block(st *strtab, group []byte) []byte {
	var stEnc enc
	for _, s := range st.list {
		stEnc.bytes(1, []byte(s))
	}
	var b enc
	b.bytes(1, stEnc.b)
	b.bytes(2, group)
	return b.b
}

func blob(kind string, data []byte, compress bool) []byte {
	var bl enc
	if compress {
		var z bytes.Buffer
		w := zlib.NewWriter(&z)
		w.Write(data)
		w.Close()
		bl.varint(2, uint64(len(data)))
		bl.bytes(3, z.Bytes())
	} else {
		bl.bytes(1, data)
	}
	var hd enc
	hd.bytes(1, []byte(kind))
	hd.varint(3, uint64(len(bl.b)))
	var out []byte
	out = binary.BigEndian.AppendUint32(out, uint32(len(hd.b)))
	out = append(out, hd.b...)
	return append(out, bl.b...)
}

func writePBF(nodes []tnode, ways []tway, rels []trel) []byte {
	var hdr enc
	hdr.bytes(4, []byte("OsmSchema-V0.6"))
	hdr.bytes(4, []byte("DenseNodes"))
	out := blob("OSMHeader", hdr.b, false)

	// Dense nodes (with tags).
	st := &strtab{}
	st.id("")
	var ids, lats, lons, kv []uint64
	var pid, plat, plon int64
	for _, n := range nodes {
		lat, lon := int64(math.Round(n.lat*1e7)), int64(math.Round(n.lon*1e7))
		ids = append(ids, zz(n.id-pid))
		lats = append(lats, zz(lat-plat))
		lons = append(lons, zz(lon-plon))
		pid, plat, plon = n.id, lat, lon
		for _, k := range sortedKeys(n.tags) {
			kv = append(kv, st.id(k), st.id(n.tags[k]))
		}
		kv = append(kv, 0)
	}
	var dense enc
	dense.packed(1, ids)
	dense.packed(8, lats)
	dense.packed(9, lons)
	dense.packed(10, kv)
	var grp enc
	grp.bytes(2, dense.b)
	out = append(out, blob("OSMData", block(st, grp.b), true)...)

	// Ways.
	st = &strtab{}
	st.id("")
	grp = enc{}
	for _, w := range ways {
		var we enc
		we.varint(1, uint64(w.id))
		var ks, vs []uint64
		for _, k := range sortedKeys(w.tags) {
			ks = append(ks, st.id(k))
			vs = append(vs, st.id(w.tags[k]))
		}
		we.packed(2, ks)
		we.packed(3, vs)
		var refs []uint64
		var prev int64
		for _, r := range w.refs {
			refs = append(refs, zz(r-prev))
			prev = r
		}
		we.packed(8, refs)
		grp.bytes(3, we.b)
	}
	out = append(out, blob("OSMData", block(st, grp.b), false)...)

	// Relations.
	if len(rels) > 0 {
		st = &strtab{}
		st.id("")
		grp = enc{}
		for _, r := range rels {
			var re enc
			re.varint(1, uint64(r.id))
			var ks, vs []uint64
			for _, k := range sortedKeys(r.tags) {
				ks = append(ks, st.id(k))
				vs = append(vs, st.id(r.tags[k]))
			}
			re.packed(2, ks)
			re.packed(3, vs)
			var roles, mids, types []uint64
			var prev int64
			for _, m := range r.members {
				roles = append(roles, st.id(m.Role))
				mids = append(mids, zz(m.Ref-prev))
				prev = m.Ref
				types = append(types, uint64(m.Type))
			}
			re.packed(8, roles)
			re.packed(9, mids)
			re.packed(10, types)
			grp.bytes(4, re.b)
		}
		out = append(out, blob("OSMData", block(st, grp.b), true)...)
	}
	return out
}

// ---------------------------------------------------------------- a test town
//
// A grid of streets about 100 m apart (0.0009° of latitude, 0.0013° of
// longitude at 46°N). Node ids are 100*row + col; row 0 is the south edge.
//
//	row 2:  201 ---- 202 ---- 203      "North Street"
//	         |        |        |
//	row 1:  101 ---- 102 ---- 103      "Middle Street"
//	         |        |        |
//	row 0:    1 ----   2 ----   3      "South Street"
//	      "West Ave" "Centre Ave" "East Ave"

const (
	dLat = 0.0009
	dLon = 0.0013
	lat0 = 46.0
	lon0 = 7.0
)

func gridNode(id int64) tnode {
	row, col := id/100, id%100
	return tnode{id: id, lat: lat0 + float64(row)*dLat, lon: lon0 + float64(col-1)*dLon}
}

func pos(id int64) Point {
	n := gridNode(id)
	return Point{n.lat, n.lon}
}

type town struct {
	nodes []tnode
	ways  []tway
	rels  []trel
}

func newTown() *town {
	t := &town{}
	for _, row := range []int64{0, 100, 200} {
		for col := int64(1); col <= 3; col++ {
			t.nodes = append(t.nodes, gridNode(row+col))
		}
	}
	res := map[string]string{"highway": "residential"}
	with := func(m map[string]string, kv ...string) map[string]string {
		out := map[string]string{}
		for k, v := range m {
			out[k] = v
		}
		for i := 0; i+1 < len(kv); i += 2 {
			out[kv[i]] = kv[i+1]
		}
		return out
	}
	t.ways = []tway{
		{1, []int64{1, 2, 3}, with(res, "name", "South Street")},
		{2, []int64{101, 102, 103}, with(res, "name", "Middle Street")},
		{3, []int64{201, 202, 203}, with(res, "name", "North Street")},
		{4, []int64{1, 101, 201}, with(res, "name", "West Avenue")},
		{5, []int64{2, 102, 202}, with(res, "name", "Centre Avenue")},
		{6, []int64{3, 103, 203}, with(res, "name", "East Avenue")},
	}
	return t
}

func (t *town) way(id int64, tags ...string) *tway {
	for i := range t.ways {
		if t.ways[i].id == id {
			for j := 0; j+1 < len(tags); j += 2 {
				t.ways[i].tags[tags[j]] = tags[j+1]
			}
			return &t.ways[i]
		}
	}
	panic("no way")
}

func (t *town) node(id int64, tags ...string) {
	for i := range t.nodes {
		if t.nodes[i].id == id {
			if t.nodes[i].tags == nil {
				t.nodes[i].tags = map[string]string{}
			}
			for j := 0; j+1 < len(tags); j += 2 {
				t.nodes[i].tags[tags[j]] = tags[j+1]
			}
			return
		}
	}
	panic("no node")
}

func (t *town) build(tb testing.TB) *Router {
	tb.Helper()
	data := writePBF(t.nodes, t.ways, t.rels)
	g, _, err := Build(context.Background(), func() (io.ReadCloser, int64, error) {
		return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
	}, BuildOptions{Source: "test"})
	if err != nil {
		tb.Fatal(err)
	}
	// Always go through the file format, as Arcana does.
	var buf bytes.Buffer
	if _, err := g.WriteTo(&buf); err != nil {
		tb.Fatal(err)
	}
	p := filepath.Join(tb.TempDir(), "t.arcroute")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		tb.Fatal(err)
	}
	g2, err := Load(p)
	if err != nil {
		tb.Fatal(err)
	}
	return NewRouter(g2)
}

func route(t *testing.T, r *Router, m Mode, pts ...Point) *Route {
	t.Helper()
	rt, err := r.Route(context.Background(), pts, m)
	if err != nil {
		t.Fatalf("%s route: %v", m, err)
	}
	return rt
}

func texts(rt *Route) []string {
	var out []string
	for _, s := range rt.Steps {
		out = append(out, s.Text)
	}
	return out
}

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

// ---------------------------------------------------------------- tests

func TestStraightRouteAndInstructions(t *testing.T) {
	r := newTown().build(t)
	// From the south-west corner to the north-east corner.
	rt := route(t, r, Car, pos(1), pos(203))
	// Two blocks east and two north: 2 × 100.4 m + 2 × 100.1 m.
	if !near(rt.Distance, 401, 3) {
		t.Errorf("distance %.0f", rt.Distance)
	}
	if rt.Duration <= 0 || rt.Steps[0].Type != "depart" || rt.Steps[len(rt.Steps)-1].Type != "arrive" {
		t.Fatalf("steps %q", texts(rt))
	}
	turns := 0
	for _, s := range rt.Steps {
		if s.Type == "turn" {
			turns++
			if !strings.HasPrefix(s.Text, "Turn left onto ") && !strings.HasPrefix(s.Text, "Turn right onto ") {
				t.Errorf("turn text %q", s.Text)
			}
		}
	}
	if turns < 1 {
		t.Errorf("no turn in %q", texts(rt))
	}
	// The geometry starts and ends at the requested corners.
	g0, gN := rt.Geometry[0], rt.Geometry[len(rt.Geometry)-1]
	if !near(g0[1], lat0, 1e-6) || !near(gN[1], lat0+2*dLat, 1e-6) {
		t.Errorf("geometry ends %v %v", g0, gN)
	}
	// Step distances add up to the route.
	sum := 0.0
	for _, s := range rt.Steps {
		sum += s.Distance
	}
	if !near(sum, rt.Distance, 3) {
		t.Errorf("steps sum %.0f, route %.0f", sum, rt.Distance)
	}
}

func TestMidBlockStartAndSameStreet(t *testing.T) {
	r := newTown().build(t)
	// Both points on South Street between nodes 1 and 2.
	a := Point{lat0 + 0.00002, lon0 + 0.0003}
	b := Point{lat0 - 0.0001, lon0 + 0.0010} // 11 m south of the street
	rt := route(t, r, Foot, a, b)
	want := 0.0007 * 111320 * math.Cos(lat0*math.Pi/180)
	if !near(rt.Distance, want, 2) {
		t.Errorf("same-street distance %.1f, want %.1f", rt.Distance, want)
	}
	if rt.Steps[0].Text != "Head east on South Street" {
		t.Errorf("depart %q", rt.Steps[0].Text)
	}
	if !strings.Contains(rt.Steps[len(rt.Steps)-1].Text, "on the right") {
		t.Errorf("arrive %q (the destination is south of an eastbound street)", rt.Steps[len(rt.Steps)-1].Text)
	}
}

func TestOneWay(t *testing.T) {
	tw := newTown()
	// South Street is one-way westbound, but bicycles may use it both ways.
	tw.way(1, "oneway", "-1", "oneway:bicycle", "no")
	r := tw.build(t)
	car := route(t, r, Car, pos(1), pos(3))
	bike := route(t, r, Bike, pos(1), pos(3))
	walk := route(t, r, Foot, pos(1), pos(3))
	if car.Distance < 400 {
		t.Errorf("car should detour around the one-way street: %.0f m", car.Distance)
	}
	if !near(bike.Distance, 2*dLon*111320*math.Cos(lat0*math.Pi/180), 3) || !near(walk.Distance, bike.Distance, 1) {
		t.Errorf("bike %.0f, walk %.0f should use South Street", bike.Distance, walk.Distance)
	}
	// Westbound is allowed for cars.
	back := route(t, r, Car, pos(3), pos(1))
	if !near(back.Distance, bike.Distance, 1) {
		t.Errorf("westbound car %.0f", back.Distance)
	}
}

func TestTurnRestriction(t *testing.T) {
	tw := newTown()
	// Coming north up Centre Avenue, no left turn into Middle Street west.
	tw.rels = append(tw.rels, trel{id: 1, tags: map[string]string{"type": "restriction", "restriction": "no_left_turn"},
		members: []Member{{1, 5, "from"}, {0, 102, "via"}, {1, 2, "to"}}})
	r := tw.build(t)
	// From South Street just east of node 2 heading to Middle Street just
	// west of node 102 the quickest way is up Centre Avenue and left.
	from := Point{lat0, lon0 + dLon*1.2}
	to := Point{lat0 + dLat, lon0 + dLon*0.8}
	car := route(t, r, Car, pos(2), to)
	walk := route(t, r, Foot, pos(2), to)
	if walk.Distance > 160 {
		t.Fatalf("walking should turn left directly: %.0f m", walk.Distance)
	}
	if car.Distance < walk.Distance+100 {
		t.Errorf("car must not turn left at 102: car %.0f m, walk %.0f m: %q", car.Distance, walk.Distance, texts(car))
	}
	_ = from
}

func TestOnlyRestriction(t *testing.T) {
	tw := newTown()
	tw.rels = append(tw.rels, trel{id: 1, tags: map[string]string{"type": "restriction", "restriction": "only_right_turn"},
		members: []Member{{1, 5, "from"}, {0, 102, "via"}, {1, 2, "to"}}})
	r := tw.build(t)
	// Going north on Centre Avenue, the only way on at 102 is right (east).
	car := route(t, r, Car, pos(2), pos(202))
	if car.Distance < 300 {
		t.Errorf("car went straight through an only-right junction: %.0f m %q", car.Distance, texts(car))
	}
}

func TestBarrier(t *testing.T) {
	tw := newTown()
	tw.node(102, "barrier", "bollard")
	r := tw.build(t)
	car := route(t, r, Car, pos(2), pos(202))
	walk := route(t, r, Foot, pos(2), pos(202))
	if !near(walk.Distance, 2*dLat*111195, 3) {
		t.Errorf("walk %.0f", walk.Distance)
	}
	if car.Distance < walk.Distance+200 {
		t.Errorf("car passed a bollard: %.0f m", car.Distance)
	}
}

func TestRoundaboutExit(t *testing.T) {
	tw := &town{}
	// A roundabout around (46, 7) with four arms; enter from the south and
	// leave by the second exit (north).
	c := tnode{id: 0, lat: 46, lon: 7}
	r := 0.0002
	ring := []int64{10, 11, 12, 13} // S, E, N, W points of the ring
	ang := []float64{-math.Pi / 2, 0, math.Pi / 2, math.Pi}
	for i, id := range ring {
		tw.nodes = append(tw.nodes, tnode{id: id, lat: c.lat + r*math.Sin(ang[i]), lon: c.lon + r*1.4*math.Cos(ang[i])})
	}
	arms := []int64{20, 21, 22, 23}
	for i, id := range arms {
		tw.nodes = append(tw.nodes, tnode{id: id, lat: c.lat + 5*r*math.Sin(ang[i]), lon: c.lon + 5*r*1.4*math.Cos(ang[i])})
	}
	// Counter-clockwise: S -> E -> N -> W -> S.
	tw.ways = append(tw.ways, tway{1, []int64{10, 11, 12, 13, 10}, map[string]string{"highway": "primary", "junction": "roundabout"}})
	names := []string{"South Road", "East Road", "North Road", "West Road"}
	for i := range arms {
		tw.ways = append(tw.ways, tway{int64(10 + i), []int64{arms[i], ring[i]}, map[string]string{"highway": "primary", "name": names[i]}})
	}
	rt := route(t, tw.build(t), Car, Point{tw.nodes[4].lat, tw.nodes[4].lon}, Point{tw.nodes[6].lat, tw.nodes[6].lon})
	found := false
	for _, s := range rt.Steps {
		if s.Type == "roundabout" {
			found = true
			if s.Exit != 2 || s.Text != "At the roundabout, take the 2nd exit onto North Road" {
				t.Errorf("roundabout step %+v", s)
			}
		}
	}
	if !found {
		t.Errorf("no roundabout step: %q", texts(rt))
	}
}

func TestMotorwayExit(t *testing.T) {
	tw := &town{}
	for i := int64(0); i <= 10; i++ {
		tw.nodes = append(tw.nodes, tnode{id: i, lat: 46, lon: 7 + float64(i)*0.002})
	}
	tw.nodes = append(tw.nodes, tnode{id: 100, lat: 45.9993, lon: 7.0115}, tnode{id: 101, lat: 45.998, lon: 7.013})
	tw.ways = []tway{
		{1, []int64{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}, map[string]string{"highway": "motorway", "ref": "A1"}},
		{2, []int64{5, 100}, map[string]string{"highway": "motorway_link", "destination": "Lakeside;Airport"}},
		{3, []int64{100, 101}, map[string]string{"highway": "secondary", "name": "Lake Road"}},
	}
	rt := route(t, tw.build(t), Car, Point{46, 7}, Point{45.998, 7.013})
	if len(rt.Steps) < 3 || rt.Steps[1].Type != "ramp" || rt.Steps[1].Text != "Take the exit on the right toward Lakeside, Airport" {
		t.Errorf("steps %q", texts(rt))
	}
	// The motorway is one-way: a car cannot go back west on it.
	if _, err := tw.build(t).Route(context.Background(), []Point{{46, 7.02}, {46, 7}}, Car); !errors.Is(err, ErrNoRoute) {
		t.Errorf("wrong-way motorway: %v", err)
	}
}

func TestViaPoints(t *testing.T) {
	r := newTown().build(t)
	rt := route(t, r, Foot, pos(1), pos(203), pos(3))
	n := 0
	for _, s := range rt.Steps {
		if s.Type == "waypoint" {
			n++
		}
	}
	if n != 1 || len(rt.Snapped) != 3 {
		t.Errorf("stops %d, snapped %d: %q", n, len(rt.Snapped), texts(rt))
	}
	if !near(rt.Distance, 601, 3) { // 401 m to the far corner, 200 m back south
		t.Errorf("distance %.0f", rt.Distance)
	}
}

func TestNoRouteAndFarAway(t *testing.T) {
	tw := newTown()
	// An island street far from the grid.
	tw.nodes = append(tw.nodes, tnode{id: 900, lat: 46.01, lon: 7.01}, tnode{id: 901, lat: 46.01, lon: 7.011})
	tw.ways = append(tw.ways, tway{900, []int64{900, 901}, map[string]string{"highway": "residential"}})
	r := tw.build(t)
	if _, err := r.Route(context.Background(), []Point{pos(1), {46.01, 7.0105}}, Car); !errors.Is(err, ErrNoRoute) {
		t.Errorf("island: %v", err)
	}
	if _, err := r.Route(context.Background(), []Point{pos(1), {46.5, 7.5}}, Car); err == nil || !strings.Contains(err.Error(), "destination is more than 2 km") {
		t.Errorf("far destination: %v", err)
	}
}

func TestDamagedFileRefused(t *testing.T) {
	data := writePBF(newTown().nodes, newTown().ways, nil)
	g, _, err := Build(context.Background(), func() (io.ReadCloser, int64, error) {
		return io.NopCloser(bytes.NewReader(data)), int64(len(data)), nil
	}, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	g.WriteTo(&buf)
	dir := t.TempDir()
	good := buf.Bytes()
	for name, b := range map[string][]byte{
		"truncated": good[:len(good)-10],
		"flipped":   func() []byte { c := bytes.Clone(good); c[len(c)/2] ^= 0xff; return c }(),
		"empty":     {},
		"other":     []byte("not a road network at all, just text that is long enough to have a header......................."),
	} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, b, 0o644)
		if _, err := Load(p); err == nil {
			t.Errorf("%s: damaged file accepted", name)
		}
	}
}

func TestReadPBFRejectsGarbage(t *testing.T) {
	for name, b := range map[string][]byte{
		"empty":   {},
		"text":    []byte("hello, this is not a PBF file"),
		"partial": writePBF(newTown().nodes, newTown().ways, nil)[:60],
	} {
		err := ReadPBF(context.Background(), bytes.NewReader(b), Handlers{Way: func(*Way) {}, Nodes: func(*NodeBatch) {}})
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestWayRouting(t *testing.T) {
	tags := func(kv ...string) []Tag {
		var out []Tag
		for i := 0; i+1 < len(kv); i += 2 {
			out = append(out, Tag{kv[i], kv[i+1]})
		}
		return out
	}
	cases := []struct {
		name   string
		tags   []Tag
		access uint8
	}{
		{"residential", tags("highway", "residential"), carFwd | carBwd | bikeFwd | bikeBwd | footFwd | footBwd},
		{"motorway", tags("highway", "motorway"), carFwd},
		{"footway", tags("highway", "footway"), bikeFwd | bikeBwd | footFwd | footBwd}, // bicycles pushed
		{"no bicycles", tags("highway", "footway", "bicycle", "no"), footFwd | footBwd},
		{"private", tags("highway", "service", "access", "private"), carFwd | carBwd | bikeFwd | bikeBwd | footFwd | footBwd},
		{"closed", tags("highway", "residential", "access", "no"), 0},
		{"closed but walkable", tags("highway", "residential", "access", "no", "foot", "yes"), footFwd | footBwd},
		{"track for cars", tags("highway", "track"), bikeFwd | bikeBwd | footFwd | footBwd},
		{"roundabout", tags("highway", "primary", "junction", "roundabout"), carFwd | bikeFwd | footFwd | footBwd},
		{"contraflow", tags("highway", "residential", "oneway", "yes", "cycleway", "opposite_lane"), carFwd | bikeFwd | bikeBwd | footFwd | footBwd},
		{"building", tags("building", "yes"), 0},
		{"pedestrian area", tags("highway", "pedestrian", "area", "yes"), 0},
	}
	for _, c := range cases {
		a, ok := wayRouting(c.tags)
		if ok != (c.access != 0) || a.access != c.access {
			t.Errorf("%s: access %06b ok %v, want %06b", c.name, a.access, ok, c.access)
		}
	}
	a, _ := wayRouting(tags("highway", "primary", "maxspeed", "30 mph"))
	if a.speed[Car] != clamp8(30*1.609344*0.8) {
		t.Errorf("mph speed %d", a.speed[Car])
	}
}

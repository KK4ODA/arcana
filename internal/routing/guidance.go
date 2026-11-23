package routing

// Turning a path into a route: its line, distance and time, and the
// maneuvers someone following it needs ("Turn left onto Rue de Hollerich").

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
)

// Point is a position in degrees.
type Point struct {
	Lat float64 `json:"lat"`
	Lon float64 `json:"lon"`
}

func (p Point) e7() pt { return pt{int32(math.Round(p.Lat / e7)), int32(math.Round(p.Lon / e7))} }

// Route is the answer to a directions request.
type Route struct {
	Mode     string  `json:"mode"`
	Distance float64 `json:"distance"` // metres
	Duration float64 `json:"duration"` // seconds
	// Geometry is the route line as [lon, lat] pairs.
	Geometry [][2]float64 `json:"geometry"`
	Steps    []Step       `json:"steps"`
	// Snapped is where each requested point joined the road network.
	Snapped [][2]float64 `json:"snapped"`
	// SnapDistance is how far each requested point was from that road, in
	// metres.
	SnapDistance []float64 `json:"snapDistance"`
	Warnings     []string  `json:"warnings"`
}

// Step is one maneuver and the stretch of road that follows it.
type Step struct {
	// Type is depart, turn, continue, fork, ramp, merge, roundabout,
	// uturn, waypoint or arrive.
	Type string `json:"type"`
	// Modifier is straight, slight left, left, sharp left (and right), or
	// uturn.
	Modifier string `json:"modifier,omitempty"`
	Name     string `json:"name,omitempty"`
	Text     string `json:"text"`
	// Exit is the roundabout exit to take.
	Exit int `json:"exit,omitempty"`
	// Distance and Duration cover the stretch after the maneuver, up to
	// the next one.
	Distance float64 `json:"distance"`
	Duration float64 `json:"duration"`
	// Location is the maneuver's position as [lon, lat]; Index is its
	// position in Route.Geometry.
	Location [2]float64 `json:"location"`
	Index    int        `json:"index"`
}

// Limits for joining a requested point to the road network.
const (
	maxSnapCar  = 2000.0
	maxSnapSlow = 1000.0
)

var modeWords = [numModes]string{"cars", "bicycles", "walking"}

// Route finds the quickest route through the points in order.
func (r *Router) Route(ctx context.Context, pts []Point, m Mode) (*Route, error) {
	if len(pts) < 2 {
		return nil, errors.New("choose a start and a destination")
	}
	if len(pts) > 25 {
		return nil, errors.New("a route can have at most 25 points")
	}
	g := r.g
	maxSnap := maxSnapSlow
	if m == Car {
		maxSnap = maxSnapCar
	}
	snaps := make([]Snap, len(pts))
	for i, p := range pts {
		if p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 || math.IsNaN(p.Lat) || math.IsNaN(p.Lon) {
			return nil, errors.New("a point is not a valid position")
		}
		s, ok := g.SnapPoint(p.e7(), m, maxSnap)
		if !ok {
			what := "The destination"
			switch {
			case i == 0:
				what = "The start"
			case i < len(pts)-1:
				what = fmt.Sprintf("Stop %d", i)
			}
			return nil, fmt.Errorf("%s is more than %s from any road or path open to %s in the installed road network", what, distWords(maxSnap), modeWords[m])
		}
		snaps[i] = s
	}
	out := &Route{Mode: m.String(), Warnings: []string{}}
	for _, s := range snaps {
		out.Snapped = append(out.Snapped, lonlat(s.Point))
		out.SnapDistance = append(out.SnapDistance, math.Round(s.Dist))
	}
	b := &builder{g: g, m: m, out: out}
	for i := 1; i < len(snaps); i++ {
		p, err := r.shortest(ctx, snaps[i-1], snaps[i], m)
		if err != nil {
			return nil, err
		}
		b.leg(p, i == len(snaps)-1, i, pts[i])
	}
	b.finish(snaps, pts)
	return out, nil
}

func lonlat(p pt) [2]float64 {
	return [2]float64{math.Round(float64(p.lon)*e7*1e6) / 1e6, math.Round(float64(p.lat)*e7*1e6) / 1e6}
}

func distWords(m float64) string {
	if m >= 1000 {
		return fmt.Sprintf("%g km", m/1000)
	}
	return fmt.Sprintf("%g m", m)
}

// piece is a directed edge, or part of one, along the route.
type piece struct {
	state  uint32
	pts    []pt
	length float64
	time   float64
}

type builder struct {
	g     *Graph
	m     Mode
	out   *Route
	cur   *Step
	flags uint8
	restr bool
	// roundabout in progress
	inRound bool
	exits   int
	// slip lane in progress: the state before it
	inSlip bool
	slip   uint32
}

func reversed(p []pt) []pt {
	out := make([]pt, len(p))
	for i := range p {
		out[i] = p[len(p)-1-i]
	}
	return out
}

func (b *builder) pieces(p path) []piece {
	g := b.g
	if p.direct {
		e := p.from.Edge
		all := g.points(e, nil)
		var pts []pt
		if !p.dirRev {
			pts = append(append([]pt{p.from.Point}, all[p.from.Seg+1:p.to.Seg+1]...), p.to.Point)
		} else {
			pts = append(append([]pt{p.from.Point}, reversed(all[p.to.Seg+1:p.from.Seg+1])...), p.to.Point)
		}
		f := math.Abs(p.to.Along-p.from.Along) / math.Max(float64(g.Length[e]), 1e-9)
		return []piece{{e<<1 | b2u(p.dirRev), pts, math.Abs(p.to.Along - p.from.Along), g.travelTime(e, b.m, f)}}
	}
	out := make([]piece, 0, len(p.states))
	for i, st := range p.states {
		e, rev := stEdge(st), stRev(st)
		all := g.points(e, nil)
		l := float64(g.Length[e])
		pc := piece{state: st}
		switch {
		case i == 0:
			if !rev {
				pc.pts = append([]pt{p.from.Point}, all[p.from.Seg+1:]...)
				pc.length = l - p.from.Along
			} else {
				pc.pts = append([]pt{p.from.Point}, reversed(all[:p.from.Seg+1])...)
				pc.length = p.from.Along
			}
		case i == len(p.states)-1:
			if !rev {
				pc.pts = append(append([]pt{}, all[:p.to.Seg+1]...), p.to.Point)
				pc.length = p.to.Along
			} else {
				pc.pts = append(reversed(all[p.to.Seg+1:]), p.to.Point)
				pc.length = l - p.to.Along
			}
		default:
			pc.pts = all
			if rev {
				pc.pts = reversed(all)
			}
			pc.length = l
		}
		f := 0.0
		if l > 0 {
			f = pc.length / l
		}
		pc.time = g.travelTime(e, b.m, f)
		out = append(out, pc)
	}
	return out
}

// addPoints appends a piece's line to the route geometry, returning the
// index of its first point.
func (b *builder) addPoints(pts []pt) int {
	start := len(b.out.Geometry)
	for i, p := range pts {
		ll := lonlat(p)
		if i == 0 && start > 0 && b.out.Geometry[start-1] == ll {
			start--
			continue
		}
		b.out.Geometry = append(b.out.Geometry, ll)
	}
	return start
}

// name is the road's name, or its route numbers ("I 16 / GA 540").
func (b *builder) name(st uint32) string {
	n := b.g.Strings[b.g.Name[stEdge(st)]]
	if strings.Contains(n, ";") {
		parts := strings.Split(n, ";")
		for i := range parts {
			parts[i] = strings.TrimSpace(parts[i])
		}
		n = strings.Join(parts, " / ")
	}
	return n
}

// roadWords names an unnamed road by its kind, for instructions.
func (b *builder) roadWords(st uint32) string {
	if n := b.name(st); n != "" {
		return n
	}
	switch b.g.Class[stEdge(st)] {
	case clFootway, clPedestrian:
		return "the footpath"
	case clCycleway:
		return "the cycle path"
	case clPath:
		return "the path"
	case clSteps:
		return "the steps"
	case clTrack:
		return "the track"
	}
	return ""
}

func (b *builder) newStep(s Step) {
	b.out.Steps = append(b.out.Steps, s)
	b.cur = &b.out.Steps[len(b.out.Steps)-1]
}

func (b *builder) leg(p path, last bool, legNo int, dest Point) {
	g := b.g
	ps := b.pieces(p)
	for i, pc := range ps {
		e := stEdge(pc.state)
		b.flags |= g.Flags[e]
		if g.Penalty[int(e)*int(numModes)+int(b.m)] >= 40 {
			b.restr = true
		}
		idx := b.addPoints(pc.pts)
		loc := lonlat(pc.pts[0])
		switch {
		case b.cur == nil:
			text := "Head " + compassWord(firstBearing(pc.pts))
			if w := b.roadWords(pc.state); w != "" {
				text += " on " + w
			}
			b.newStep(Step{Type: "depart", Name: b.name(pc.state), Text: text, Location: loc, Index: idx})
		case i == 0:
			// The first piece of a later leg: the waypoint step was added
			// when the previous leg ended.
		default:
			b.junction(ps[i-1].state, pc.state, loc, idx)
		}
		b.cur.Distance += pc.length
		b.cur.Duration += pc.time
		b.out.Distance += pc.length
		b.out.Duration += pc.time
	}
	end := ps[len(ps)-1]
	loc := lonlat(end.pts[len(end.pts)-1])
	idx := len(b.out.Geometry) - 1
	if last {
		text := "Arrive at your destination"
		if side := b.side(end.pts, dest); side != "" {
			text += ", on the " + side
		}
		b.newStep(Step{Type: "arrive", Text: text, Location: loc, Index: idx})
	} else {
		b.newStep(Step{Type: "waypoint", Text: fmt.Sprintf("Arrive at stop %d", legNo), Location: loc, Index: idx})
	}
	b.inRound, b.inSlip = false, false
}

// side tells whether the requested point lies left or right of the road at
// the end of the route.
func (b *builder) side(pts []pt, dest Point) string {
	if len(pts) < 2 {
		return ""
	}
	a, c := pts[len(pts)-2], pts[len(pts)-1]
	d := dest.e7()
	if haversineE7(c, d) < 5 {
		return ""
	}
	pr := newProjection(c.lat)
	dx, dy := float64(c.lon-a.lon)*pr.kx, float64(c.lat-a.lat)*pr.ky
	vx, vy := float64(d.lon-c.lon)*pr.kx, float64(d.lat-c.lat)*pr.ky
	cross := dx*vy - dy*vx
	switch {
	case cross > 0:
		return "left"
	case cross < 0:
		return "right"
	}
	return ""
}

func modifier(angle float64) string {
	a := math.Abs(angle)
	dir := "right"
	if angle < 0 {
		dir = "left"
	}
	switch {
	case a < 25:
		return "straight"
	case a < 60:
		return "slight " + dir
	case a < 140:
		return dir
	case a < 175:
		return "sharp " + dir
	}
	return "uturn"
}

var ordinals = []string{"", "1st", "2nd", "3rd", "4th", "5th", "6th", "7th", "8th", "9th", "10th"}

func ordinal(n int) string {
	if n < len(ordinals) {
		return ordinals[n]
	}
	return fmt.Sprintf("%dth", n)
}

// junction decides whether moving from state a to state c needs an
// instruction, and adds it.
func (b *builder) junction(a, c uint32, loc [2]float64, idx int) {
	g, m := b.g, b.m
	v := g.head(a)
	ea, ec := stEdge(a), stEdge(c)
	// Slip lanes and connectors between ordinary roads (primary_link and
	// smaller) are part of the turn they make: say nothing on entering,
	// and describe the turn from the road before them when leaving.
	if g.Flags[ec]&flLink != 0 && g.Class[ec] > clTrunk {
		if !b.inSlip && g.Flags[ea]&flLink == 0 {
			b.inSlip, b.slip = true, a
		}
		if b.inSlip {
			return
		}
	}
	aSide := a // the state used for the angle and the name before the junction
	if b.inSlip {
		b.inSlip = false
		aSide = b.slip
		ea = stEdge(aSide)
	}
	roundA, roundC := g.Flags[ea]&flRoundabout != 0, g.Flags[ec]&flRoundabout != 0
	angle := g.turnAngle(aSide, c)
	if a == c^1 {
		b.newStep(Step{Type: "uturn", Modifier: "uturn", Name: b.name(c), Text: "Make a U-turn", Location: loc, Index: idx})
		return
	}

	// Other ways on from this junction.
	var others []uint32
	for _, o := range g.Adj[g.First[v]:g.First[v+1]] {
		if o != c && o != a^1 && g.allowed(o, m) {
			others = append(others, o)
		}
	}

	// Roundabouts: one instruction on entry, naming the exit.
	if roundC && !roundA {
		b.inRound, b.exits = true, 0
		b.newStep(Step{Type: "roundabout", Text: "Enter the roundabout", Location: loc, Index: idx})
		return
	}
	if roundA && roundC {
		for _, o := range others {
			if g.Flags[stEdge(o)]&flRoundabout == 0 {
				b.exits++
				break
			}
		}
		return
	}
	if roundA && !roundC {
		if b.inRound && b.cur != nil && b.cur.Type == "roundabout" {
			b.exits++
			b.cur.Exit = b.exits
			b.cur.Name = b.name(c)
			b.cur.Text = fmt.Sprintf("At the roundabout, take the %s exit", ordinal(b.exits))
			if w := b.roadWords(c); w != "" {
				b.cur.Text += " onto " + w
			}
			b.inRound = false
			// The stretch after the roundabout belongs to this step.
			return
		}
		b.inRound = false
	}

	nameA, nameC := b.name(aSide), b.name(c)
	changed := nameA != nameC
	onto := ""
	if w := b.roadWords(c); w != "" {
		onto = " onto " + w
	}
	mod := modifier(angle)
	linkA, linkC := g.Flags[ea]&flLink != 0, g.Flags[ec]&flLink != 0
	major := func(e uint32) bool { return g.Class[e] <= clTrunk }

	switch {
	case len(others) == 0:
		// No choice: say only when the road's name changes.
		if !changed || nameC == "" {
			return
		}
		if mod == "straight" || strings.HasPrefix(mod, "slight") {
			b.newStep(Step{Type: "continue", Modifier: mod, Name: nameC, Text: "Continue onto " + nameC, Location: loc, Index: idx})
		} else {
			b.newStep(Step{Type: "turn", Modifier: mod, Name: nameC, Text: "Turn " + mod + onto, Location: loc, Index: idx})
		}
	case linkC && !linkA && major(ea):
		side := "right"
		if angle < 0 {
			side = "left"
		}
		text := "Take the exit on the " + side
		if d := signpost(g.Strings[g.Dest[ec]]); d != "" {
			text += " toward " + d
		} else if nameC != "" && nameC != nameA {
			text += onto
		}
		b.newStep(Step{Type: "ramp", Modifier: "slight " + side, Name: nameC, Text: text, Location: loc, Index: idx})
	case linkA && !linkC && major(ec):
		text := "Merge"
		if nameC != "" {
			text += " onto " + nameC
		}
		b.newStep(Step{Type: "merge", Modifier: mod, Name: nameC, Text: text, Location: loc, Index: idx})
	case linkC && !linkA:
		side := "right"
		if angle < 0 {
			side = "left"
		}
		text := "Take the ramp on the " + side
		if d := signpost(g.Strings[g.Dest[ec]]); d != "" {
			text += " toward " + d
		}
		b.newStep(Step{Type: "ramp", Modifier: "slight " + side, Name: nameC, Text: text, Location: loc, Index: idx})
	case mod == "straight":
		// A fork: another way of the same kind also goes nearly straight
		// on. Exits passed on a motorway, or side roads leaving the road
		// being followed, are not forks.
		fork := false
		same := !changed && nameC != ""
		for _, o := range others {
			eo := stEdge(o)
			if math.Abs(g.turnAngle(aSide, o)) >= 35 || g.Flags[eo]&flLink != g.Flags[ec]&flLink {
				continue
			}
			if same && g.Class[eo] != g.Class[ec] {
				continue
			}
			if g.Class[eo] <= g.Class[ec]+1 {
				fork = true
			}
		}
		switch {
		case fork:
			side := "right"
			if angle < 0 {
				side = "left"
			}
			text := "Keep " + side + onto
			if same {
				text = "Keep " + side + " to stay on " + nameC
			}
			b.newStep(Step{Type: "fork", Modifier: "slight " + side, Name: nameC, Text: text, Location: loc, Index: idx})
		case changed && nameC != "":
			b.newStep(Step{Type: "continue", Modifier: "straight", Name: nameC, Text: "Continue onto " + nameC, Location: loc, Index: idx})
		}
	default:
		if !changed && math.Abs(angle) < 60 {
			// The road bends; only minor ways leave it here.
			minor := true
			for _, o := range others {
				if g.Class[stEdge(o)] <= g.Class[ec] {
					minor = false
				}
			}
			if minor {
				return
			}
		}
		if mod == "uturn" {
			b.newStep(Step{Type: "uturn", Modifier: "uturn", Name: nameC, Text: "Make a U-turn" + onto, Location: loc, Index: idx})
			return
		}
		b.newStep(Step{Type: "turn", Modifier: mod, Name: nameC, Text: "Turn " + mod + onto, Location: loc, Index: idx})
	}
}

// signpost formats an OSM destination value ("A;B") for reading.
func signpost(d string) string {
	parts := strings.Split(d, ";")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, ", ")
}

// shortContinue is the length below which a "continue onto" step, where
// a road only changes its name, is folded into the step before it.
const shortContinue = 40.0

// tidy folds trivial steps into the step before them.
func tidy(steps []Step) []Step {
	out := steps[:0]
	for _, s := range steps {
		if len(out) > 0 && s.Type == "continue" {
			prev := &out[len(out)-1]
			if s.Distance < shortContinue || (s.Name == prev.Name && prev.Type != "arrive") {
				prev.Distance += s.Distance
				prev.Duration += s.Duration
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

func (b *builder) finish(snaps []Snap, pts []Point) {
	o := b.out
	o.Steps = tidy(o.Steps)
	o.Distance = math.Round(o.Distance)
	o.Duration = math.Round(o.Duration)
	for i := range o.Steps {
		o.Steps[i].Distance = math.Round(o.Steps[i].Distance)
		o.Steps[i].Duration = math.Round(o.Steps[i].Duration)
	}
	if b.flags&flToll != 0 && b.m == Car {
		o.Warnings = append(o.Warnings, "The route uses toll roads.")
	}
	if b.flags&flUnpaved != 0 && b.m != Foot {
		o.Warnings = append(o.Warnings, "The route includes unpaved roads.")
	}
	if b.restr {
		o.Warnings = append(o.Warnings, "The route uses roads marked private or for access only; check signs.")
	}
	for i, s := range snaps {
		if s.Dist > 100 {
			what := "The destination"
			switch {
			case i == 0:
				what = "The start"
			case i < len(snaps)-1:
				what = fmt.Sprintf("Stop %d", i)
			}
			o.Warnings = append(o.Warnings, fmt.Sprintf("%s is %s from the nearest road or path; the route begins or ends where it meets that road.", what, distWords(math.Round(s.Dist/10)*10)))
		}
	}
}

var compass = []string{"north", "northeast", "east", "southeast", "south", "southwest", "west", "northwest"}

func compassWord(b float64) string {
	return compass[int(math.Round(b/45))%8]
}

// firstBearing is the direction of the first segment of a line that has a
// length (a snapped start can coincide with the next point).
func firstBearing(pts []pt) float64 {
	for i := 1; i < len(pts); i++ {
		if pts[i] != pts[0] {
			return bearingE7(pts[0], pts[i])
		}
	}
	return 0
}

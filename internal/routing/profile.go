package routing

// How OSM tags become routing attributes for the three travel modes. The
// rules follow common OSM routing practice (the OSM wiki's access and
// oneway conventions, and defaults close to OSRM's and GraphHopper's
// profiles), simplified: no country-specific access defaults, no
// conditional restrictions, no ferries.

import (
	"strconv"
	"strings"
)

// Mode is a way of travelling.
type Mode uint8

const (
	Car Mode = iota
	Bike
	Foot
	numModes
)

var modeNames = [numModes]string{"car", "bike", "foot"}

func (m Mode) String() string { return modeNames[m] }

// ParseMode reads "car", "bike" or "foot".
func ParseMode(s string) (Mode, bool) {
	for i, n := range modeNames {
		if s == n {
			return Mode(i), true
		}
	}
	return 0, false
}

// Road classes, ordered from most to least important.
const (
	clMotorway uint8 = iota
	clTrunk
	clPrimary
	clSecondary
	clTertiary
	clUnclassified
	clResidential
	clLivingStreet
	clService
	clTrack
	clCycleway
	clPath
	clFootway
	clPedestrian
	clSteps
	clOther
)

var classOf = map[string]uint8{
	"motorway": clMotorway, "motorway_link": clMotorway,
	"trunk": clTrunk, "trunk_link": clTrunk,
	"primary": clPrimary, "primary_link": clPrimary,
	"secondary": clSecondary, "secondary_link": clSecondary,
	"tertiary": clTertiary, "tertiary_link": clTertiary,
	"unclassified": clUnclassified, "road": clUnclassified,
	"residential": clResidential, "living_street": clLivingStreet,
	"service": clService, "track": clTrack,
	"cycleway": clCycleway, "path": clPath, "bridleway": clPath,
	"footway": clFootway, "pedestrian": clPedestrian, "steps": clSteps,
	"corridor": clFootway,
}

// Car speeds in km/h when no usable maxspeed is tagged.
var carSpeed = map[string]float64{
	"motorway": 110, "motorway_link": 60, "trunk": 90, "trunk_link": 50,
	"primary": 70, "primary_link": 40, "secondary": 60, "secondary_link": 40,
	"tertiary": 50, "tertiary_link": 35, "unclassified": 40, "road": 30,
	"residential": 30, "living_street": 10, "service": 15, "track": 10,
}

// Edge access bits: forward and backward for each mode.
const (
	carFwd uint8 = 1 << iota
	carBwd
	bikeFwd
	bikeBwd
	footFwd
	footBwd
)

func fwdBit(m Mode) uint8 { return 1 << (2 * m) }
func bwdBit(m Mode) uint8 { return 2 << (2 * m) }

// Edge flags.
const (
	flRoundabout uint8 = 1 << iota
	flLink
	flToll
	flUnpaved
)

// wayAttrs is what routing keeps of a way.
type wayAttrs struct {
	class   uint8
	access  uint8
	flags   uint8
	speed   [numModes]uint8 // km/h
	penalty [numModes]uint8 // cost factor ×10 (10 = none)
	name    string          // name, or ref when unnamed
	ref     string
	dest    string // destination signposting on links
}

func yes(v string) bool {
	switch v {
	case "yes", "true", "1", "designated", "permissive", "official":
		return true
	}
	return false
}

// accessFor evaluates the access tags that apply to a mode, most specific
// last: "", allowed, denied, or restricted (destination only, private and
// similar: usable, but avoided).
func accessFor(tags []Tag, keys []string) string {
	res := ""
	for _, k := range keys {
		v := tagValue(tags, k)
		if v == "" {
			continue
		}
		switch v {
		case "no", "agricultural", "forestry", "use_sidepath", "discouraged", "military", "emergency", "psv", "bus":
			res = "no"
		case "private", "destination", "delivery", "customers", "permit":
			res = "restricted"
		case "dismount":
			res = "dismount"
		default:
			if yes(v) {
				res = "yes"
			}
		}
	}
	return res
}

var accessKeys = [numModes][]string{
	Car:  {"access", "vehicle", "motor_vehicle", "motorcar"},
	Bike: {"access", "vehicle", "bicycle"},
	Foot: {"access", "foot"},
}

// parseSpeed reads a maxspeed value in km/h: "50", "50 mph", "walk".
func parseSpeed(v string) (float64, bool) {
	v = strings.TrimSpace(v)
	if v == "" || v == "none" || v == "signals" || v == "variable" {
		return 0, false
	}
	if v == "walk" {
		return 6, true
	}
	mph := strings.HasSuffix(v, "mph")
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(v, "mph"), "km/h"))
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || n <= 0 || n > 200 {
		return 0, false
	}
	if mph {
		n *= 1.609344
	}
	return n, true
}

func clamp8(v float64) uint8 {
	if v < 1 {
		return 1
	}
	if v > 250 {
		return 250
	}
	return uint8(v + 0.5)
}

// wayRouting interprets a way's tags. ok is false when no mode may use it.
func wayRouting(tags []Tag) (a wayAttrs, ok bool) {
	hw := tagValue(tags, "highway")
	cls, known := classOf[hw]
	if !known {
		return a, false
	}
	if tagValue(tags, "area") == "yes" || yes(tagValue(tags, "impassable")) ||
		tagValue(tags, "access") == "no" && tagValue(tags, "foot") == "" && tagValue(tags, "bicycle") == "" && tagValue(tags, "motor_vehicle") == "" && tagValue(tags, "motorcar") == "" && tagValue(tags, "vehicle") == "" {
		return a, false
	}
	a.class = cls
	a.name = tagValue(tags, "name")
	a.ref = tagValue(tags, "ref")
	if a.name == "" {
		a.name = a.ref
	}
	if strings.HasSuffix(hw, "_link") {
		a.flags |= flLink
		a.dest = tagValue(tags, "destination")
		if a.dest == "" {
			a.dest = tagValue(tags, "destination:ref")
		}
	}
	junction := tagValue(tags, "junction")
	if junction == "roundabout" || junction == "circular" {
		a.flags |= flRoundabout
	}
	if yes(tagValue(tags, "toll")) {
		a.flags |= flToll
	}
	surface := tagValue(tags, "surface")
	switch surface {
	case "unpaved", "gravel", "fine_gravel", "dirt", "earth", "ground", "grass", "mud", "sand", "compacted", "pebblestone", "woodchips":
		a.flags |= flUnpaved
	}
	motorroad := tagValue(tags, "motorroad") == "yes"

	// Car.
	car := accessFor(tags, accessKeys[Car])
	if _, ok := carSpeed[hw]; ok && car != "no" && (hw != "track" || car == "yes" || car == "restricted") {
		sp := carSpeed[hw]
		if ms, ok := parseSpeed(tagValue(tags, "maxspeed")); ok {
			// Traffic, junctions and curves keep average speeds below
			// the limit.
			f := 0.8
			if cls <= clTrunk {
				f = 0.9
			}
			sp = ms * f
		}
		if a.flags&flUnpaved != 0 && sp > 30 {
			sp = 30
		}
		if svc := tagValue(tags, "service"); svc == "parking_aisle" || svc == "driveway" {
			sp = min(sp, 10)
		}
		a.speed[Car] = clamp8(sp)
		a.penalty[Car] = 10
		if car == "restricted" {
			a.penalty[Car] = 40
		}
		a.access |= carFwd | carBwd
	}

	// Bicycle.
	bike := accessFor(tags, accessKeys[Bike])
	bikeOK := false
	bsp, bpen := 16.0, 10.0
	switch cls {
	case clMotorway:
		bikeOK = bike == "yes"
	case clTrunk:
		bikeOK = !motorroad && bike != "no"
		bpen = 25
	case clPrimary:
		bikeOK = !motorroad && bike != "no"
		bpen = 18
	case clSecondary:
		bikeOK = bike != "no"
		bpen = 13
	case clCycleway:
		bikeOK = bike != "no"
		bsp = 18
	case clPath, clTrack:
		bikeOK = bike != "no"
		bsp = 12
	case clFootway, clPedestrian:
		// Allowed only where signed; otherwise walk the bicycle.
		bikeOK = bike != "no"
		if bike != "yes" {
			bsp = 5
		}
	case clSteps:
		bikeOK = bike == "yes"
		bsp = 2
	default:
		bikeOK = bike != "no"
	}
	if bike == "dismount" {
		bsp = 5
	}
	if bikeOK {
		if a.flags&flUnpaved != 0 {
			bsp = min(bsp, 10)
		}
		if bike == "restricted" {
			bpen *= 4
		}
		a.speed[Bike] = clamp8(bsp)
		a.penalty[Bike] = clamp8(bpen)
		a.access |= bikeFwd | bikeBwd
	}

	// Foot.
	foot := accessFor(tags, accessKeys[Foot])
	footOK := foot != "no"
	fpen := 10.0
	switch cls {
	case clMotorway:
		footOK = foot == "yes"
	case clTrunk:
		footOK = footOK && !motorroad
		fpen = 30
	case clPrimary:
		fpen = 15
	}
	if tagValue(tags, "sidewalk") == "no" && cls <= clSecondary && foot != "yes" {
		fpen *= 1.5
	}
	if footOK {
		fsp := 5.0
		if cls == clSteps {
			fsp = 3
		}
		if foot == "restricted" {
			fpen *= 4
		}
		a.speed[Foot] = clamp8(fsp)
		a.penalty[Foot] = clamp8(fpen)
		a.access |= footFwd | footBwd
	}
	if a.access == 0 {
		return a, false
	}

	// One-way streets: cars and bicycles; walkers only with oneway:foot.
	ow := tagValue(tags, "oneway")
	implied := cls == clMotorway && hw == "motorway" || a.flags&flRoundabout != 0
	dir := 0 // 1 forward only, -1 backward only
	switch {
	case ow == "yes" || ow == "true" || ow == "1":
		dir = 1
	case ow == "-1" || ow == "reverse":
		dir = -1
	case ow == "" && implied:
		dir = 1
	}
	applyOneway := func(m Mode, d int) {
		switch d {
		case 1:
			a.access &^= bwdBit(m)
		case -1:
			a.access &^= fwdBit(m)
		}
	}
	applyOneway(Car, dir)
	bdir := dir
	switch tagValue(tags, "oneway:bicycle") {
	case "no":
		bdir = 0
	case "yes":
		bdir = 1
	case "-1":
		bdir = -1
	}
	if strings.HasPrefix(tagValue(tags, "cycleway"), "opposite") {
		bdir = 0
	}
	if cls == clFootway || cls == clPedestrian || cls == clPath {
		// Bicycles pushed along footways may go either way.
		if a.speed[Bike] <= 5 {
			bdir = 0
		}
	}
	applyOneway(Bike, bdir)
	switch tagValue(tags, "oneway:foot") {
	case "yes":
		applyOneway(Foot, 1)
	case "-1":
		applyOneway(Foot, -1)
	}
	if a.access == 0 {
		return a, false
	}
	return a, true
}

// barrierBlocks reports whether a node's tags close it to a mode.
func barrierBlocks(tags []Tag, m Mode) bool {
	b := tagValue(tags, "barrier")
	if b == "" {
		return false
	}
	acc := accessFor(tags, accessKeys[m])
	if acc == "yes" || acc == "restricted" {
		return false
	}
	if acc == "no" {
		return true
	}
	switch m {
	case Car:
		switch b {
		case "bollard", "block", "jersey_barrier", "chain", "kissing_gate", "stile", "turnstile", "full-height_turnstile",
			"cycle_barrier", "motorcycle_barrier", "log", "planter", "fence", "wall", "kerb", "horse_stile", "swing_gate":
			return true
		}
	case Bike:
		switch b {
		case "stile", "turnstile", "full-height_turnstile", "fence", "wall", "horse_stile", "jersey_barrier":
			return true
		}
	case Foot:
		switch b {
		case "fence", "wall", "jersey_barrier":
			return true
		}
	}
	return false
}

// Package gps reads positions from GNSS receivers (NMEA 0183 over serial,
// UDP or TCP, or gpsd), replays GPX files as a simulator, records tracks and
// keeps waypoints. Arcana never transmits positions anywhere; LAN clients see
// them only when the host enables sharing.
package gps

import (
	"strings"
	"sync"
	"time"

	nmea "github.com/adrianmo/go-nmea"
)

// Fix is the latest position solution.
type Fix struct {
	Lat        float64   `json:"lat"`
	Lon        float64   `json:"lon"`
	Alt        *float64  `json:"alt,omitempty"`
	Speed      *float64  `json:"speed,omitempty"`  // metres per second
	Course     *float64  `json:"course,omitempty"` // degrees true
	Mode       string    `json:"mode"`             // 2d, 3d, dgps, rtk, estimated, manual
	Satellites int       `json:"satellites,omitempty"`
	InView     int       `json:"inView,omitempty"`
	HDOP       float64   `json:"hdop,omitempty"`
	Time       time.Time `json:"time,omitempty"` // receiver UTC time
	Received   time.Time `json:"received"`
}

// tracker turns NMEA sentences into a Fix. Sentences arrive in bursts per
// epoch (RMC, GGA, GSA, GSV...); every valid position sentence updates the
// fix immediately, so a receiver that sends only RMC or only GGA works.
type tracker struct {
	mu          sync.Mutex
	fix         *Fix
	fixType     string // from GSA: "1" none, "2" 2D, "3" 3D
	inView      int
	lastData    time.Time
	lastNoFix   time.Time
	sentences   int64
	bad         int64
	lastBadText string
	now         func() time.Time
	onFix       func(Fix)
}

const maxSentence = 512

const knots = 0.514444

func (t *tracker) handleLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	if len(line) > maxSentence || (line[0] != '$' && line[0] != '!') {
		t.mu.Lock()
		t.bad++
		t.mu.Unlock()
		return
	}
	s, err := nmea.Parse(line)
	t.mu.Lock()
	now := t.now()
	t.lastData = now
	if err != nil {
		t.bad++
		t.lastBadText = err.Error()
		t.mu.Unlock()
		return
	}
	t.sentences++
	var updated bool
	switch v := s.(type) {
	case nmea.RMC:
		if v.Validity != nmea.ValidRMC || v.FFAMode == "N" || !validCoord(v.Latitude, v.Longitude) {
			t.lastNoFix = now
			break
		}
		f := t.next(now)
		f.Lat, f.Lon = v.Latitude, v.Longitude
		switch v.FFAMode {
		case "S":
			f.Mode = "simulated"
		case "E":
			f.Mode = "estimated"
		}
		sp := v.Speed * knots
		f.Speed = &sp
		if v.Speed > 0.5 { // course is meaningless when stationary
			c := v.Course
			f.Course = &c
		}
		if v.Date.Valid && v.Time.Valid {
			f.Time = time.Date(2000+v.Date.YY, time.Month(v.Date.MM), v.Date.DD, v.Time.Hour, v.Time.Minute,
				v.Time.Second, v.Time.Millisecond*int(time.Millisecond), time.UTC)
		}
		updated = true
	case nmea.GGA:
		if v.FixQuality == nmea.Invalid || !validCoord(v.Latitude, v.Longitude) {
			t.lastNoFix = now
			break
		}
		f := t.next(now)
		f.Lat, f.Lon = v.Latitude, v.Longitude
		// An empty altitude field means unknown, not sea level.
		if len(v.Fields) > 8 && v.Fields[8] != "" {
			alt := v.Altitude
			f.Alt = &alt
		}
		f.Satellites = int(v.NumSatellites)
		f.HDOP = v.HDOP
		switch v.FixQuality {
		case nmea.DGPS:
			f.Mode = "dgps"
		case nmea.RTK, nmea.FRTK:
			f.Mode = "rtk"
		case nmea.EST:
			f.Mode = "estimated"
		}
		updated = true
	case nmea.GSA:
		t.fixType = v.FixType
		if t.fix != nil && t.fix.Mode != "dgps" && t.fix.Mode != "rtk" && t.fix.Mode != "simulated" {
			switch v.FixType {
			case "2":
				t.fix.Mode = "2d"
			case "3":
				t.fix.Mode = "3d"
			}
		}
	case nmea.GSV:
		t.inView = int(v.NumberSVsInView)
	case nmea.VTG:
		if t.fix != nil {
			sp := v.GroundSpeedKPH / 3.6
			t.fix.Speed = &sp
			if sp > 0.25 {
				c := v.TrueTrack
				t.fix.Course = &c
			}
		}
	}
	var f Fix
	if updated && t.fix != nil {
		t.fix.InView = t.inView
		f = *t.fix
	}
	cb := t.onFix
	t.mu.Unlock()
	if updated && cb != nil {
		cb(f)
	}
}

// next returns the fix to update, keeping fields from the same epoch.
func (t *tracker) next(now time.Time) *Fix {
	if t.fix == nil || now.Sub(t.fix.Received) > 2*time.Second {
		f := &Fix{Mode: "2d"}
		if t.fixType == "3" {
			f.Mode = "3d"
		}
		if t.fix != nil {
			// Carry slow-changing fields across epochs.
			f.Satellites, f.HDOP, f.Alt, f.Mode = t.fix.Satellites, t.fix.HDOP, t.fix.Alt, t.fix.Mode
		}
		t.fix = f
	}
	t.fix.Received = now
	return t.fix
}

// setManual installs a fixed position.
func (t *tracker) setManual(lat, lon float64) {
	t.mu.Lock()
	now := t.now()
	t.fix = &Fix{Lat: lat, Lon: lon, Mode: "manual", Received: now}
	f := *t.fix
	cb := t.onFix
	t.mu.Unlock()
	if cb != nil {
		cb(f)
	}
}

func (t *tracker) reset() {
	t.mu.Lock()
	t.fix = nil
	t.fixType = ""
	t.inView = 0
	t.lastData = time.Time{}
	t.lastNoFix = time.Time{}
	t.sentences, t.bad = 0, 0
	t.lastBadText = ""
	t.mu.Unlock()
}

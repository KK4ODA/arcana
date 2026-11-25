package gps

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

// GPX 1.1 reading and writing (https://www.topografix.com/GPX/1/1/). Input
// is untrusted: sizes are bounded by the caller and every coordinate is
// range-checked.

type gpxDoc struct {
	XMLName xml.Name   `xml:"gpx"`
	Version string     `xml:"version,attr"`
	Creator string     `xml:"creator,attr"`
	XMLNS   string     `xml:"xmlns,attr,omitempty"`
	Wpts    []gpxPoint `xml:"wpt"`
	Rtes    []gpxRoute `xml:"rte"`
	Trks    []gpxTrack `xml:"trk"`
}

type gpxPoint struct {
	Lat  float64  `xml:"lat,attr"`
	Lon  float64  `xml:"lon,attr"`
	Ele  *float64 `xml:"ele,omitempty"`
	Time string   `xml:"time,omitempty"`
	Name string   `xml:"name,omitempty"`
	Desc string   `xml:"desc,omitempty"`
	Sym  string   `xml:"sym,omitempty"`
}

type gpxRoute struct {
	Name   string     `xml:"name,omitempty"`
	Points []gpxPoint `xml:"rtept"`
}

type gpxTrack struct {
	Name     string       `xml:"name,omitempty"`
	Segments []gpxSegment `xml:"trkseg"`
}

type gpxSegment struct {
	Points []gpxPoint `xml:"trkpt"`
}

// Point is a position with optional elevation and time.
type Point struct {
	Lat  float64   `json:"lat"`
	Lon  float64   `json:"lon"`
	Ele  *float64  `json:"ele,omitempty"`
	Time time.Time `json:"time,omitempty"`
}

// Track is a named list of segments.
type Track struct {
	Name     string    `json:"name"`
	Segments [][]Point `json:"segments"`
}

// Waypoint is a named position.
type Waypoint struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Lat     float64   `json:"lat"`
	Lon     float64   `json:"lon"`
	Ele     *float64  `json:"ele,omitempty"`
	Note    string    `json:"note,omitempty"`
	Symbol  string    `json:"symbol,omitempty"`
	Created time.Time `json:"created"`
}

// GPX is the content of a GPX file. Routes are written only (reading
// returns routes as tracks).
type GPX struct {
	Waypoints []Waypoint
	Routes    []GPXRoute
	Tracks    []Track
}

// GPXRoute is a GPX route: named points to pass in order (for a saved
// route, its maneuvers).
type GPXRoute struct {
	Name   string
	Points []Waypoint
}

// MaxGPXBytes bounds GPX input.
const MaxGPXBytes = 64 << 20

func validCoord(lat, lon float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lon) && lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}

func toPoint(p gpxPoint) (Point, bool) {
	if !validCoord(p.Lat, p.Lon) {
		return Point{}, false
	}
	pt := Point{Lat: p.Lat, Lon: p.Lon}
	if p.Ele != nil && !math.IsNaN(*p.Ele) && math.Abs(*p.Ele) < 20000 {
		e := *p.Ele
		pt.Ele = &e
	}
	if t, err := time.Parse(time.RFC3339Nano, p.Time); err == nil {
		pt.Time = t.UTC()
	}
	return pt, true
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ReadGPX parses GPX. Routes are returned as tracks.
func ReadGPX(r io.Reader) (GPX, error) {
	var doc gpxDoc
	dec := xml.NewDecoder(io.LimitReader(r, MaxGPXBytes))
	dec.Strict = true
	if err := dec.Decode(&doc); err != nil {
		return GPX{}, fmt.Errorf("not a readable GPX file: %w", err)
	}
	var out GPX
	for _, w := range doc.Wpts {
		p, ok := toPoint(w)
		if !ok {
			continue
		}
		out.Waypoints = append(out.Waypoints, Waypoint{Name: clip(w.Name, 200), Lat: p.Lat, Lon: p.Lon, Ele: p.Ele,
			Note: clip(w.Desc, 2000), Symbol: clip(w.Sym, 60), Created: p.Time})
	}
	add := func(name string, segs [][]gpxPoint) {
		t := Track{Name: clip(name, 200)}
		for _, s := range segs {
			var seg []Point
			for _, gp := range s {
				if p, ok := toPoint(gp); ok {
					seg = append(seg, p)
				}
			}
			if len(seg) > 0 {
				t.Segments = append(t.Segments, seg)
			}
		}
		if len(t.Segments) > 0 {
			out.Tracks = append(out.Tracks, t)
		}
	}
	for _, t := range doc.Trks {
		var segs [][]gpxPoint
		for _, s := range t.Segments {
			segs = append(segs, s.Points)
		}
		add(t.Name, segs)
	}
	for _, r := range doc.Rtes {
		add(r.Name, [][]gpxPoint{r.Points})
	}
	if len(out.Waypoints) == 0 && len(out.Tracks) == 0 {
		return out, errors.New("the GPX file contains no waypoints, routes or tracks")
	}
	return out, nil
}

func fromPoint(p Point) gpxPoint {
	g := gpxPoint{Lat: round7(p.Lat), Lon: round7(p.Lon), Ele: p.Ele}
	if !p.Time.IsZero() {
		g.Time = p.Time.UTC().Format(time.RFC3339)
	}
	return g
}

func round7(v float64) float64 { return math.Round(v*1e7) / 1e7 }

// WriteGPX encodes waypoints and tracks as GPX 1.1.
func WriteGPX(w io.Writer, g GPX) error {
	doc := gpxDoc{Version: "1.1", Creator: "Arcana", XMLNS: "http://www.topografix.com/GPX/1/1"}
	for _, wp := range g.Waypoints {
		p := fromPoint(Point{Lat: wp.Lat, Lon: wp.Lon, Ele: wp.Ele, Time: wp.Created})
		p.Name, p.Desc, p.Sym = wp.Name, wp.Note, wp.Symbol
		doc.Wpts = append(doc.Wpts, p)
	}
	for _, r := range g.Routes {
		gr := gpxRoute{Name: r.Name}
		for _, wp := range r.Points {
			p := fromPoint(Point{Lat: wp.Lat, Lon: wp.Lon, Ele: wp.Ele})
			p.Name, p.Desc = wp.Name, wp.Note
			gr.Points = append(gr.Points, p)
		}
		doc.Rtes = append(doc.Rtes, gr)
	}
	for _, t := range g.Tracks {
		gt := gpxTrack{Name: t.Name}
		for _, s := range t.Segments {
			var seg gpxSegment
			for _, p := range s {
				seg.Points = append(seg.Points, fromPoint(p))
			}
			gt.Segments = append(gt.Segments, seg)
		}
		doc.Trks = append(doc.Trks, gt)
	}
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", " ")
	if err := enc.Encode(doc); err != nil {
		return err
	}
	buf.WriteString("\n")
	_, err := w.Write(buf.Bytes())
	return err
}

// Distance returns the great-circle distance in metres.
func Distance(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371008.8
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := p2-p1, (lon2-lon1)*math.Pi/180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}

// Bearing returns the initial bearing in degrees from point 1 to point 2.
func Bearing(lat1, lon1, lat2, lon2 float64) float64 {
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dl := (lon2 - lon1) * math.Pi / 180
	y := math.Sin(dl) * math.Cos(p2)
	x := math.Cos(p1)*math.Sin(p2) - math.Sin(p1)*math.Cos(p2)*math.Cos(dl)
	return math.Mod(math.Atan2(y, x)*180/math.Pi+360, 360)
}

// Length is a track's length in metres.
func (t Track) Length() float64 {
	var d float64
	for _, s := range t.Segments {
		for i := 1; i < len(s); i++ {
			d += Distance(s[i-1].Lat, s[i-1].Lon, s[i].Lat, s[i].Lon)
		}
	}
	return d
}

// Points counts a track's points.
func (t Track) Points() int {
	n := 0
	for _, s := range t.Segments {
		n += len(s)
	}
	return n
}

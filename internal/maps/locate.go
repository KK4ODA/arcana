package maps

import (
	"context"
	"fmt"
	"math"
)

// Maidenhead returns the 6-character Maidenhead locator used by radio
// amateurs (for example "JN39bt").
func Maidenhead(lat, lon float64) string {
	const A = "ABCDEFGHIJKLMNOPQRSTUVWX"
	x := math.Min(lon+180, 359.99999)
	y := math.Min(lat+90, 179.99999)
	x, y = math.Max(x, 0), math.Max(y, 0)
	f1 := string(A[int(x/20)]) + string(A[int(y/10)])
	x, y = math.Mod(x, 20), math.Mod(y, 10)
	f2 := fmt.Sprintf("%d%d", int(x/2), int(y))
	x, y = math.Mod(x, 2), math.Mod(y, 1)
	f3 := string(A[int(x*12)]+32) + string(A[int(y*24)]+32)
	return f1 + f2 + f3
}

// DMS formats a coordinate as degrees, minutes and seconds.
func DMS(v float64, pos, neg string) string {
	h := pos
	if v < 0 {
		h, v = neg, -v
	}
	d := math.Floor(v)
	mf := (v - d) * 60
	m := math.Floor(mf)
	s := (mf - m) * 60
	if s >= 59.95 {
		s, m = 0, m+1
	}
	if m >= 60 {
		m, d = 0, d+1
	}
	return fmt.Sprintf("%d°%02d′%04.1f″%s", int(d), int(m), s, h)
}

// DDM formats a coordinate as degrees and decimal minutes.
func DDM(v float64, pos, neg string) string {
	h := pos
	if v < 0 {
		h, v = neg, -v
	}
	d := math.Floor(v)
	return fmt.Sprintf("%d°%06.3f′%s", int(d), (v-d)*60, h)
}

// NearbyPlace is a place with its distance and bearing from a point.
type NearbyPlace struct {
	Place
	Distance float64 `json:"distance"` // metres
	Bearing  float64 `json:"bearing"`  // degrees from the point to the place
}

// Nearest returns indexed places within radius metres of a point, closest
// first, larger places breaking ties.
func (g *Gazetteer) Nearest(ctx context.Context, lat, lon, radius float64, limit int) ([]NearbyPlace, error) {
	if limit <= 0 || limit > 50 {
		limit = 5
	}
	dLat := radius / 111320
	dLon := radius / (111320 * math.Max(0.01, math.Cos(lat*math.Pi/180)))
	g.mu.Lock()
	defer g.mu.Unlock()
	rows, err := g.db.QueryContext(ctx, `SELECT name, kind, lon, lat, mapset, rank FROM places
		WHERE lat BETWEEN ? AND ? AND lon BETWEEN ? AND ? LIMIT 5000`, lat-dLat, lat+dLat, lon-dLon, lon+dLon)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	var out []NearbyPlace
	for rows.Next() {
		var p NearbyPlace
		if err := rows.Scan(&p.Name, &p.Kind, &p.Lon, &p.Lat, &p.MapSet, &p.Rank); err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%s|%.2f|%.2f", p.Name, p.Lon, p.Lat)
		if seen[key] {
			continue
		}
		seen[key] = true
		p.Distance = haversine(lat, lon, p.Lat, p.Lon)
		if p.Distance > radius {
			continue
		}
		p.Bearing = bearing(lat, lon, p.Lat, p.Lon)
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sortNearby(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func sortNearby(ps []NearbyPlace) {
	for i := 1; i < len(ps); i++ {
		for j := i; j > 0 && less(ps[j], ps[j-1]); j-- {
			ps[j], ps[j-1] = ps[j-1], ps[j]
		}
	}
}

func less(a, b NearbyPlace) bool {
	if math.Abs(a.Distance-b.Distance) > 1 {
		return a.Distance < b.Distance
	}
	return a.Rank > b.Rank
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371008.8
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dp, dl := p2-p1, (lon2-lon1)*math.Pi/180
	a := math.Sin(dp/2)*math.Sin(dp/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dl/2)*math.Sin(dl/2)
	return 2 * r * math.Asin(math.Min(1, math.Sqrt(a)))
}

func bearing(lat1, lon1, lat2, lon2 float64) float64 {
	p1, p2 := lat1*math.Pi/180, lat2*math.Pi/180
	dl := (lon2 - lon1) * math.Pi / 180
	y := math.Sin(dl) * math.Cos(p2)
	x := math.Cos(p1)*math.Sin(p2) - math.Sin(p1)*math.Cos(p2)*math.Cos(dl)
	return math.Mod(math.Atan2(y, x)*180/math.Pi+360, 360)
}

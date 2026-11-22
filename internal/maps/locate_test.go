package maps

import (
	"context"
	"testing"
)

func TestLocatorsAndNearest(t *testing.T) {
	for _, c := range []struct {
		lat, lon float64
		want     string
	}{
		{49.61, 6.13, "JN39bo"}, {38.8977, -77.0365, "FM18lv"}, {-33.8688, 151.2093, "QF56od"}, {90, 180, "RR99xx"}, {-90, -180, "AA00aa"},
	} {
		if got := Maidenhead(c.lat, c.lon); got != c.want {
			t.Errorf("Maidenhead(%v,%v) = %s, want %s", c.lat, c.lon, got, c.want)
		}
	}
	if got := DMS(-33.8688, "N", "S"); got != "33°52′07.7″S" {
		t.Errorf("DMS %s", got)
	}
	if got := DDM(151.2093, "E", "W"); got != "151°12.558′E" {
		t.Errorf("DDM %s", got)
	}
	g, err := OpenGazetteer("")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	ctx := context.Background()
	for _, p := range []Place{{"Esch-sur-Alzette", "locality", 5.985, 49.496, "a", 7, ""}, {"Luxembourg", "locality", 6.13, 49.611, "a", 9, ""},
		{"Luxembourg", "locality", 6.13, 49.611, "b", 9, ""}, {"Paris", "locality", 2.35, 48.85, "a", 13, ""}} {
		g.db.ExecContext(ctx, "INSERT INTO places (name, kind, lon, lat, mapset, rank) VALUES (?,?,?,?,?,?)", p.Name, p.Kind, p.Lon, p.Lat, p.MapSet, p.Rank)
	}
	near, err := g.Nearest(ctx, 49.60, 6.12, 30000, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(near) != 2 || near[0].Name != "Luxembourg" || near[0].Distance > 2000 || near[1].Name != "Esch-sur-Alzette" {
		t.Fatalf("nearest %+v", near)
	}
	if near[1].Bearing < 180 || near[1].Bearing > 270 {
		t.Fatalf("bearing to Esch %v", near[1].Bearing)
	}
}

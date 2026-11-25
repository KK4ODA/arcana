package gps

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func sampleRoute() SavedRoute {
	return SavedRoute{
		Mode: "foot", From: RouteEnd{Lat: 49.61, Lon: 6.13, Label: "My position"}, To: RouteEnd{Lat: 49.62, Lon: 6.14, Label: "Station"},
		Distance: 1500, Duration: 1100, Network: "Luxembourg",
		Geometry: [][2]float64{{6.13, 49.61}, {6.135, 49.615}, {6.14, 49.62}},
		Steps: []RouteStep{
			{Type: "depart", Text: "Head north", Location: [2]float64{6.13, 49.61}, Index: 0, Distance: 700},
			{Type: "arrive", Text: "Arrive at <Station>", Location: [2]float64{6.14, 49.62}, Index: 2},
		},
	}
}

func TestSavedRoutes(t *testing.T) {
	m := NewManager(t.TempDir(), false, nil)
	saved, err := m.SaveRoute(sampleRoute())
	if err != nil {
		t.Fatal(err)
	}
	if saved.Name != "Route to Station" || saved.ID == "" {
		t.Fatalf("saved %+v", saved)
	}
	list, err := m.Routes()
	if err != nil || len(list) != 1 || list[0].ID != saved.ID || list[0].Distance != 1500 {
		t.Fatalf("list %+v %v", list, err)
	}
	got, err := m.Route(saved.ID)
	if err != nil || len(got.Geometry) != 3 || got.Steps[1].Text != "Arrive at <Station>" {
		t.Fatalf("route %+v %v", got, err)
	}
	var b strings.Builder
	if err := m.RouteGPX(&b, saved.ID); err != nil {
		t.Fatal(err)
	}
	g, err := ReadGPX(strings.NewReader(b.String()))
	if err != nil || !strings.Contains(b.String(), "<rtept") || !strings.Contains(b.String(), "Arrive at &lt;Station&gt;") {
		t.Fatalf("gpx %v\n%s", err, b.String())
	}
	if len(g.Tracks) != 2 { // the route and the track, both read back as tracks
		t.Fatalf("read back %d tracks", len(g.Tracks))
	}
	if err := m.DeleteRoute(saved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Route(saved.ID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("after delete: %v", err)
	}
	if _, err := m.Route("../waypoints"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("path id: %v", err)
	}
}

func TestSaveRouteValidates(t *testing.T) {
	tests := []struct {
		name   string
		change func(*SavedRoute)
	}{
		{"mode", func(r *SavedRoute) { r.Mode = "plane" }},
		{"one point", func(r *SavedRoute) { r.Geometry = r.Geometry[:1] }},
		{"bad point", func(r *SavedRoute) { r.Geometry[1] = [2]float64{200, 10} }},
		{"no steps", func(r *SavedRoute) { r.Steps = nil }},
		{"step index", func(r *SavedRoute) { r.Steps[1].Index = 3 }},
		{"bad destination", func(r *SavedRoute) { r.To.Lat = 100 }},
		{"negative distance", func(r *SavedRoute) { r.Distance = -1 }},
	}
	m := NewManager(t.TempDir(), false, nil)
	for _, tc := range tests {
		r := sampleRoute()
		tc.change(&r)
		if _, err := m.SaveRoute(r); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
	ro := NewManager(t.TempDir(), true, nil)
	if _, err := ro.SaveRoute(sampleRoute()); !errors.Is(err, ErrReadOnly) {
		t.Fatalf("read-only: %v", err)
	}
}

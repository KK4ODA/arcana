package server

import "testing"

func TestParsePoints(t *testing.T) {
	pts, err := parsePoints("49.61,6.13; 49.49,5.98")
	if err != nil || len(pts) != 2 || pts[1].Lat != 49.49 || pts[1].Lon != 5.98 {
		t.Fatalf("%v %+v", err, pts)
	}
	for _, bad := range []string{"", "49.61", "49.61,6.13;x,y", "1,2,3"} {
		if _, err := parsePoints(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

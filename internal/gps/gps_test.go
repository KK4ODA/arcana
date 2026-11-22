package gps

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTracker() *tracker {
	return &tracker{now: time.Now}
}

func TestTrackerParsesReceiverOutput(t *testing.T) {
	tr := newTracker()
	var got []Fix
	tr.onFix = func(f Fix) { got = append(got, f) }
	// One epoch from a u-blox style receiver.
	for _, s := range []string{
		withChecksum("GNRMC,203415.000,A,6325.6138,N,01021.4290,E,0.02,31.66,280511,,,A"),
		"$GNGGA,203415.000,6325.6138,N,01021.4290,E,1,8,2.42,72.5,M,41.5,M,,*7C",
		withChecksum("GNGSA,A,3,03,22,06,19,11,14,32,01,,,,,2.62,2.42,1.00"),
		withChecksum("GPGSV,3,1,11,03,03,111,00,04,15,270,00,06,01,010,00,13,06,292,00"),
		"$GPRMC,BROKEN*00",
		"garbage line",
	} {
		tr.handleLine(s)
	}
	if len(got) != 2 {
		t.Fatalf("got %d fixes", len(got))
	}
	f := got[len(got)-1]
	if math.Abs(f.Lat-63.42690) > 1e-4 || math.Abs(f.Lon-10.35715) > 1e-4 {
		t.Fatalf("position %v,%v", f.Lat, f.Lon)
	}
	if f.Alt == nil || *f.Alt != 72.5 || f.Satellites != 8 || f.HDOP != 2.42 {
		t.Fatalf("fix %+v", f)
	}
	if f.Time.Year() != 2011 || f.Time.Hour() != 20 {
		t.Fatalf("time %v", f.Time)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.fix.Mode != "3d" || tr.inView != 11 || tr.sentences != 4 || tr.bad != 2 {
		t.Fatalf("mode %s inView %d sentences %d bad %d", tr.fix.Mode, tr.inView, tr.sentences, tr.bad)
	}
}

func TestTrackerNoFix(t *testing.T) {
	tr := newTracker()
	tr.handleLine(withChecksum("GPRMC,123519,V,,,,,,,230394,,,N"))
	tr.handleLine(withChecksum("GPGGA,123519,,,,,0,00,,,M,,M,,"))
	if tr.fix != nil {
		t.Fatal("no-fix sentences produced a position")
	}
	if tr.sentences == 0 || tr.lastNoFix.IsZero() {
		t.Fatalf("sentences %d bad %d (%s)", tr.sentences, tr.bad, tr.lastBadText)
	}
}

func TestGeneratedSentencesRoundTrip(t *testing.T) {
	tr := newTracker()
	ts := time.Date(2026, 11, 22, 4, 5, 6, 0, time.UTC)
	for _, c := range [][2]float64{{49.6116, 6.1319}, {-33.8688, 151.2093}, {40.7128, -74.0060}, {-22.9, -43.2}} {
		tr.handleLine(rmcSentence(ts, c[0], c[1], 1.5, 90))
		ele := 120.0
		tr.handleLine(ggaSentence(ts, c[0], c[1], &ele))
		if tr.fix == nil || math.Abs(tr.fix.Lat-c[0]) > 1e-5 || math.Abs(tr.fix.Lon-c[1]) > 1e-5 {
			t.Fatalf("%v -> %+v (bad %d: %s)", c, tr.fix, tr.bad, tr.lastBadText)
		}
		if tr.fix.Mode != "simulated" || tr.fix.Speed == nil || math.Abs(*tr.fix.Speed-1.5) > 0.01 || tr.fix.Alt == nil || *tr.fix.Alt != 120 {
			t.Fatalf("fix %+v", tr.fix)
		}
	}
}

func waitState(t *testing.T, m *Manager, want string) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st := m.Status()
		if st.State == want {
			return st
		}
		time.Sleep(20 * time.Millisecond)
	}
	st := m.Status()
	t.Fatalf("state %s, want %s (error %q)", st.State, want, st.Error)
	return st
}

func TestSimulatorAndRecording(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, false, nil)
	m.SimCenter = func() (float64, float64, bool) { return 49.61, 6.13, true }
	ch, unsub := m.Subscribe()
	defer unsub()
	m.Apply(context.Background(), Settings{Source: "simulator", SimulatorSpeed: 60, RecordTrack: true})
	st := waitState(t, m, "current")
	if st.Fix == nil || math.Abs(st.Fix.Lat-49.61) > 0.01 || st.Fix.Mode != "simulated" || !st.Recording {
		t.Fatalf("status %+v", st)
	}
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("no status update delivered")
	}
	time.Sleep(2500 * time.Millisecond)
	m.Stop()
	tracks, err := m.Tracks()
	if err != nil || len(tracks) != 1 || tracks[0].Points < 2 || tracks[0].Length < 100 {
		t.Fatalf("tracks %+v %v", tracks, err)
	}
	if st := m.Status(); st.State != "lost" && st.State != "stale" && st.State != "current" {
		t.Fatalf("after stop: %s", st.State)
	}
}

func TestSimulatorReplaysGPX(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "tracks"), 0o755)
	gpx := `<?xml version="1.0"?><gpx version="1.1" creator="test" xmlns="http://www.topografix.com/GPX/1/1"><trk><name>t</name><trkseg>
<trkpt lat="10.0" lon="20.0"><time>2026-01-01T00:00:00Z</time></trkpt>
<trkpt lat="10.001" lon="20.001"><time>2026-01-01T00:01:00Z</time></trkpt></trkseg></trk></gpx>`
	os.WriteFile(filepath.Join(dir, "tracks", "walk.gpx"), []byte(gpx), 0o644)
	m := NewManager(dir, false, nil)
	m.Apply(context.Background(), Settings{Source: "simulator", SimulatorTrack: "walk.gpx"})
	defer m.Stop()
	st := waitState(t, m, "current")
	if math.Abs(st.Fix.Lat-10) > 0.002 || !strings.Contains(st.Detail, "walk.gpx") {
		t.Fatalf("status %+v", st)
	}
	m.Apply(context.Background(), Settings{Source: "simulator", SimulatorTrack: "../escape.gpx"})
	if st := m.Status(); st.State != "error" {
		t.Fatalf("path escape accepted: %+v", st)
	}
}

func freePort(t *testing.T) int {
	l, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.LocalAddr().(*net.UDPAddr).Port
}

func TestUDPSource(t *testing.T) {
	port := freePort(t)
	m := NewManager(t.TempDir(), false, nil)
	m.Apply(context.Background(), Settings{Source: "udp", UDPPort: port})
	defer m.Stop()
	waitState(t, m, "waiting")
	conn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	now := time.Now().UTC()
	fmt.Fprintf(conn, "%s\r\n%s\r\n", rmcSentence(now, 51.5, -0.12, 0, 0), ggaSentence(now, 51.5, -0.12, nil))
	st := waitState(t, m, "current")
	if math.Abs(st.Fix.Lon+0.12) > 1e-5 || st.Fix.Alt != nil {
		t.Fatalf("fix %+v", st.Fix)
	}
}

func TestTCPAndGPSDSources(t *testing.T) {
	for _, gpsd := range []bool{false, true} {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		watched := make(chan string, 1)
		go func() {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer c.Close()
			if gpsd {
				fmt.Fprintln(c, `{"class":"VERSION","release":"3.25"}`)
				line, _ := bufio.NewReader(c).ReadString('\n')
				watched <- line
			}
			for i := 0; i < 50; i++ {
				now := time.Now().UTC()
				fmt.Fprintf(c, "%s\r\n%s\r\n", rmcSentence(now, -41.29, 174.78, 2, 45), ggaSentence(now, -41.29, 174.78, nil))
				time.Sleep(100 * time.Millisecond)
			}
		}()
		m := NewManager(t.TempDir(), false, nil)
		src := "tcp"
		if gpsd {
			src = "gpsd"
		}
		m.Apply(context.Background(), Settings{Source: src, Address: l.Addr().String()})
		st := waitState(t, m, "current")
		if math.Abs(st.Fix.Lat+41.29) > 1e-5 {
			t.Fatalf("%s fix %+v", src, st.Fix)
		}
		if gpsd {
			if w := <-watched; !strings.Contains(w, `"nmea":true`) {
				t.Fatalf("gpsd watch command %q", w)
			}
		}
		m.Stop()
		l.Close()
	}
}

func TestConnectionErrorsAreReported(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	m := NewManager(t.TempDir(), false, nil)
	m.Apply(context.Background(), Settings{Source: "tcp", Address: addr})
	defer m.Stop()
	st := waitState(t, m, "error")
	if st.Error == "" {
		t.Fatal("no error text")
	}
	m.Apply(context.Background(), Settings{Source: "serial", SerialPort: "/dev/arcana-does-not-exist"})
	waitState(t, m, "error")
	m.Apply(context.Background(), Settings{Source: "manual", ManualLat: 12.5, ManualLon: -70})
	if st := m.Status(); st.State != "manual" || st.Fix.Lat != 12.5 {
		t.Fatalf("manual %+v", st)
	}
	m.Apply(context.Background(), Settings{})
	if st := m.Status(); st.State != "off" || st.Fix != nil {
		t.Fatalf("off %+v", st)
	}
}

func TestStaleAndLost(t *testing.T) {
	m := NewManager(t.TempDir(), false, nil)
	clock := time.Now()
	m.now = func() time.Time { return clock }
	m.settings.Source = "udp"
	m.connected = true
	m.t.handleLine(rmcSentence(clock, 1, 1, 0, 0))
	if st := m.Status(); st.State != "current" {
		t.Fatal(st.State)
	}
	clock = clock.Add(10 * time.Second)
	if st := m.Status(); st.State != "stale" {
		t.Fatal(st.State)
	}
	clock = clock.Add(2 * time.Minute)
	if st := m.Status(); st.State != "lost" {
		t.Fatal(st.State)
	}
}

func TestWaypointsAndGPX(t *testing.T) {
	dir := t.TempDir()
	m := NewManager(dir, false, nil)
	w, err := m.AddWaypoint(Waypoint{Name: "Water source", Lat: 46.5, Lon: 7.98, Note: "spring"})
	if err != nil || w.ID == "" {
		t.Fatal(w, err)
	}
	if _, err := m.AddWaypoint(Waypoint{Name: "Bad", Lat: 91, Lon: 0}); err == nil {
		t.Fatal("invalid latitude accepted")
	}
	gpx := `<?xml version="1.0" encoding="UTF-8"?>
<gpx version="1.1" creator="x" xmlns="http://www.topografix.com/GPX/1/1">
 <wpt lat="46.5" lon="7.98"><name>Water source</name></wpt>
 <wpt lat="46.6" lon="7.99"><ele>2100</ele><name>Hut</name><sym>Lodge</sym></wpt>
 <wpt lat="999" lon="7.99"><name>Broken</name></wpt>
 <rte><name>Route</name><rtept lat="46.5" lon="7.9"/><rtept lat="46.6" lon="7.91"/></rte>
 <trk><name>Hike</name><trkseg><trkpt lat="46.5" lon="7.98"><ele>1500</ele><time>2026-07-01T08:00:00Z</time></trkpt>
 <trkpt lat="46.51" lon="7.99"><time>2026-07-01T08:30:00Z</time></trkpt></trkseg></trk>
</gpx>`
	res, err := m.ImportGPX(strings.NewReader(gpx), "../../My Hike!.gpx")
	if err != nil {
		t.Fatal(err)
	}
	if res.Waypoints != 1 || res.Skipped != 1 || len(res.Tracks) != 2 || res.Tracks[0] != "My Hike-1.gpx" {
		t.Fatalf("import %+v", res)
	}
	ws, _ := m.Waypoints()
	if len(ws) != 2 || ws[1].Ele == nil || *ws[1].Ele != 2100 || ws[1].Symbol != "Lodge" {
		t.Fatalf("waypoints %+v", ws)
	}
	tracks, _ := m.Tracks()
	if len(tracks) != 2 {
		t.Fatalf("tracks %+v", tracks)
	}
	var buf bytes.Buffer
	if err := m.ExportGPX(&buf, []string{"My Hike-1.gpx"}); err != nil {
		t.Fatal(err)
	}
	back, err := ReadGPX(&buf)
	if err != nil || len(back.Waypoints) != 2 || len(back.Tracks) != 1 || back.Tracks[0].Points() != 2 {
		t.Fatalf("round trip %+v %v", back, err)
	}
	if !back.Tracks[0].Segments[0][0].Time.Equal(time.Date(2026, 7, 1, 8, 0, 0, 0, time.UTC)) {
		t.Fatal("track time lost")
	}
	if err := m.DeleteWaypoint(w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Track("../waypoints.json"); err == nil {
		t.Fatal("track path escape")
	}
	if err := m.DeleteTrack("My Hike-2.gpx"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadGPX(strings.NewReader("<gpx></gpx>")); err == nil {
		t.Fatal("empty GPX accepted")
	}
	ro := NewManager(dir, true, nil)
	if _, err := ro.AddWaypoint(Waypoint{Name: "x", Lat: 1, Lon: 1}); err != ErrReadOnly {
		t.Fatal("read-only write allowed")
	}
}

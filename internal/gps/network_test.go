package gps

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNetworkSentencesRoundTrip(t *testing.T) {
	alt, speed, course := 312.5, 12.0, 270.0
	tests := []struct {
		name string
		in   netFix
	}{
		{"full", netFix{Lat: 33.749, Lon: -84.388, Alt: &alt, Speed: &speed, Course: &course, Accuracy: 7.5}},
		{"position only", netFix{Lat: -33.8688, Lon: 151.2093}},
		{"coarse", netFix{Lat: 64.1466, Lon: -21.9426, Accuracy: 1500}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := newTracker()
			for _, s := range networkSentences(time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC), tc.in) {
				tr.handleLine(s)
			}
			if tr.bad != 0 {
				t.Fatalf("rejected sentences: %s", tr.lastBadText)
			}
			f := tr.fix
			if f == nil || math.Abs(f.Lat-tc.in.Lat) > 1e-5 || math.Abs(f.Lon-tc.in.Lon) > 1e-5 {
				t.Fatalf("fix %+v", f)
			}
			if math.Abs(f.Accuracy-tc.in.Accuracy) > 0.05 {
				t.Fatalf("accuracy %v, want %v", f.Accuracy, tc.in.Accuracy)
			}
			if (tc.in.Alt == nil) != (f.Alt == nil) || (f.Alt != nil && *f.Alt != *tc.in.Alt) {
				t.Fatalf("altitude %v", f.Alt)
			}
			if tc.in.Speed == nil && f.Speed != nil {
				t.Fatalf("unknown speed reported as %v", *f.Speed)
			}
			if tc.in.Speed != nil && (f.Speed == nil || math.Abs(*f.Speed-*tc.in.Speed) > 0.01 || f.Course == nil || *f.Course != course) {
				t.Fatalf("speed %v course %v", f.Speed, f.Course)
			}
		})
	}
}

func TestReceiverGSTSetsAccuracy(t *testing.T) {
	tr := newTracker()
	tr.handleLine(withChecksum("GPRMC,203415.000,A,6325.6138,N,01021.4290,E,0.02,31.66,280511,,,A"))
	tr.handleLine(withChecksum("GPGST,203415.00,1.2,3.0,2.0,45.0,2.5,4.0,6.0"))
	if tr.fix.Accuracy != 4 {
		t.Fatalf("accuracy %v", tr.fix.Accuracy)
	}
	tr.handleLine(withChecksum("GPGST,203416.00,,,,,,,"))
	if tr.bad != 0 || tr.fix.Accuracy != 4 {
		t.Fatalf("empty GST: bad %d accuracy %v", tr.bad, tr.fix.Accuracy)
	}
}

// Protobuf encoding for the fake dish.
func pbKey(b []byte, num uint64, wt int) []byte { return binary.AppendUvarint(b, num<<3|uint64(wt)) }
func pbDouble(b []byte, num uint64, v float64) []byte {
	return binary.LittleEndian.AppendUint64(pbKey(b, num, 1), math.Float64bits(v))
}
func pbVarint(b []byte, num, v uint64) []byte { return binary.AppendUvarint(pbKey(b, num, 0), v) }
func pbBytes(b []byte, num uint64, d []byte) []byte {
	return append(binary.AppendUvarint(pbKey(b, num, 2), uint64(len(d))), d...)
}

func locationResponse(lat, lon, alt, sigma float64, source uint64, withLLA bool) []byte {
	var loc []byte
	if withLLA {
		lla := pbDouble(pbDouble(pbDouble(nil, 1, lat), 2, lon), 3, alt)
		loc = pbBytes(loc, 1, lla)
	}
	loc = pbVarint(loc, 3, source)
	loc = pbDouble(loc, 4, sigma)
	loc = pbDouble(loc, 5, 0.3)
	resp := pbVarint(nil, 3, 42) // api_version, ignored
	return pbBytes(resp, 1017, loc)
}

// fakeDish serves the dish's gRPC endpoint over unencrypted HTTP/2, like
// the real one. answer returns the response message, or a gRPC status.
func fakeDish(t *testing.T, answer func() (msg []byte, status string)) (string, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != starlinkPath || r.ProtoMajor != 2 || r.Header.Get("Content-Type") != "application/grpc" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Equal(body, getLocationRequest) {
			t.Errorf("request body %x", body)
		}
		msg, status := answer()
		w.Header().Set("Content-Type", "application/grpc")
		if msg == nil {
			// Trailers-only response.
			w.Header().Set("Grpc-Status", status)
			w.Header().Set("Grpc-Message", "Permission%20denied")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
		w.WriteHeader(http.StatusOK)
		frame := binary.BigEndian.AppendUint32([]byte{0}, uint32(len(msg)))
		w.Write(append(frame, msg...))
		w.Header().Set("Grpc-Status", status)
		w.Header().Set("Grpc-Message", "")
	})
	srv := httptest.NewUnstartedServer(h)
	srv.Config.Protocols = new(http.Protocols)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Config.Protocols.SetHTTP1(true)
	srv.Start()
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://"), &calls
}

func TestStarlinkSource(t *testing.T) {
	permissionInBody := pbBytes(nil, 2, pbBytes(pbVarint(nil, 1, 7), 2, []byte("location not allowed")))
	tests := []struct {
		name      string
		msg       []byte
		status    string
		wantState string
		wantErr   string
		wantBasis string
	}{
		{"position from satellites", locationResponse(48.8584, 2.2945, 35, 6.5, 5, true), "0", "current", "", "Starlink satellites"},
		{"position from the dish's GPS", locationResponse(48.8584, 2.2945, 35, 3, 4, true), "0", "current", "", "GPS receiver"},
		{"no position yet", locationResponse(0, 0, 0, 0, 1, false), "0", "searching", "", ""},
		{"location not allowed", nil, "7", "error", "Allow access on local network", ""},
		{"not allowed, status in body", permissionInBody, "0", "error", "Allow access on local network", ""},
		{"old firmware", nil, "12", "error", "does not offer its location", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			addr, calls := fakeDish(t, func() ([]byte, string) { return tc.msg, tc.status })
			m := NewManager(t.TempDir(), false, nil)
			m.Apply(context.Background(), Settings{Source: "starlink", StarlinkAddress: addr})
			defer m.Stop()
			st := waitState(t, m, tc.wantState)
			if !strings.Contains(st.Error, tc.wantErr) || !strings.Contains(st.Basis, tc.wantBasis) {
				t.Fatalf("status %+v", st)
			}
			if tc.wantState == "current" {
				if math.Abs(st.Fix.Lat-48.8584) > 1e-5 || st.Fix.Accuracy == 0 || st.Fix.Alt == nil || *st.Fix.Alt != 35 {
					t.Fatalf("fix %+v", st.Fix)
				}
			}
			if calls.Load() == 0 {
				t.Fatal("the dish was never asked")
			}
		})
	}
}

func TestStarlinkUnreachable(t *testing.T) {
	addr, _ := fakeDish(t, func() ([]byte, string) { return nil, "0" })
	m := NewManager(t.TempDir(), false, nil)
	m.Apply(context.Background(), Settings{Source: "starlink", StarlinkAddress: addr + "0"}) // wrong port
	defer m.Stop()
	if st := waitState(t, m, "error"); !strings.Contains(st.Error, "cannot reach the Starlink dish") {
		t.Fatalf("error %q", st.Error)
	}
}

func TestDecodeLocationResponseRejectsGarbage(t *testing.T) {
	for _, b := range [][]byte{{0xff}, {0x0a, 0x05, 0x01}, pbBytes(nil, 1017, []byte{0x0a, 0x10})} {
		if _, err := decodeLocationResponse(b); err == nil {
			t.Fatalf("%x accepted", b)
		}
	}
	if _, err := decodeLocationResponse(pbVarint(nil, 3, 1)); err == nil {
		t.Fatal("a response without a location was accepted")
	}
}

func TestBrowserSource(t *testing.T) {
	m := NewManager(t.TempDir(), false, nil)
	if err := m.PushBrowser(BrowserFix{Lat: 1, Lon: 1}); !errors.Is(err, ErrNoBrowserSource) {
		t.Fatalf("no source: %v", err)
	}
	m.Apply(context.Background(), Settings{Source: "browser", BrowserID: "phone-1"})
	defer m.Stop()
	if !m.WantsBrowser("phone-1") || m.WantsBrowser("laptop-2") {
		t.Fatal("WantsBrowser")
	}
	tests := []struct {
		name string
		fix  BrowserFix
		want error
	}{
		{"other browser", BrowserFix{Lat: 10, Lon: 10, ClientID: "laptop-2"}, ErrOtherBrowser},
		{"bad latitude", BrowserFix{Lat: 91, Lon: 10, ClientID: "phone-1"}, errors.New("not a valid position")},
		{"bad accuracy", BrowserFix{Lat: 10, Lon: 10, Accuracy: math.Inf(1), ClientID: "phone-1"}, errors.New("not a valid position")},
		{"good", BrowserFix{Lat: 51.5007, Lon: -0.1246, Accuracy: 24, ClientID: "phone-1"}, nil},
	}
	for _, tc := range tests {
		err := m.PushBrowser(tc.fix)
		if (err == nil) != (tc.want == nil) || (err != nil && err.Error() != tc.want.Error()) {
			t.Fatalf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
	st := waitState(t, m, "current")
	if math.Abs(st.Fix.Lat-51.5007) > 1e-5 || st.Fix.Accuracy != 24 {
		t.Fatalf("fix %+v", st.Fix)
	}
}

func TestCheckSourceDisagreement(t *testing.T) {
	tests := []struct {
		name     string
		lat, lon float64
		acc      float64
		disagree bool
	}{
		{"same place", 40.0001, -105.0001, 20, false},
		{"within accuracy", 40.004, -105.0, 300, false},
		{"kilometres apart", 40.2, -105.0, 20, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(t.TempDir(), false, nil)
			m.Apply(context.Background(), Settings{Source: "manual", ManualLat: 40, ManualLon: -105, CheckSource: "browser"})
			defer m.Stop()
			if err := m.PushBrowser(BrowserFix{Lat: tc.lat, Lon: tc.lon, Accuracy: tc.acc}); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(5 * time.Second)
			var st Status
			for time.Now().Before(deadline) {
				if st = m.Status(); st.Check != nil && st.Check.State == "current" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if st.Check == nil || st.Check.State != "current" || st.Check.Source != "browser" {
				t.Fatalf("check %+v", st.Check)
			}
			if st.Disagree != tc.disagree || st.Apart <= 0 && tc.disagree {
				t.Fatalf("apart %v disagree %v", st.Apart, st.Disagree)
			}
			if st.Fix.Lat != 40 {
				t.Fatal("the check source moved the position")
			}
		})
	}
}

func TestCheckSourceIgnoredWhenSameOrStatic(t *testing.T) {
	m := NewManager(t.TempDir(), false, nil)
	for _, cs := range []string{"manual", "simulator", "manual"} {
		m.Apply(context.Background(), Settings{Source: "manual", ManualLat: 1, ManualLon: 1, CheckSource: cs})
		if st := m.Status(); st.Check != nil {
			t.Fatalf("check %s: %+v", cs, st.Check)
		}
	}
}

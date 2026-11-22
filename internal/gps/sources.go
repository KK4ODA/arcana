package gps

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"time"

	"go.bug.st/serial"
)

// A source reads NMEA lines until ctx ends or the connection fails. It calls
// connected once data can flow.
type source func(ctx context.Context, line func(string), connected func()) error

// readLines splits a stream into lines, discarding overlong ones (NMEA
// sentences are at most 82 characters; anything far longer is noise).
func readLines(ctx context.Context, r io.Reader, line func(string)) error {
	br := bufio.NewReaderSize(r, 4096)
	var cur []byte
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		b, err := br.ReadByte()
		if err != nil {
			return err
		}
		switch b {
		case '\n', '\r':
			if len(cur) > 0 && len(cur) <= maxSentence {
				line(string(cur))
			}
			cur = cur[:0]
		default:
			if len(cur) <= maxSentence {
				cur = append(cur, b)
			}
		}
	}
}

// deadlineReader turns read timeouts into periodic context checks.
type deadlineReader struct {
	ctx  context.Context
	conn net.Conn
	idle time.Duration
	last time.Time
}

func (d *deadlineReader) Read(p []byte) (int, error) {
	for {
		if err := d.ctx.Err(); err != nil {
			return 0, err
		}
		d.conn.SetReadDeadline(time.Now().Add(time.Second))
		n, err := d.conn.Read(p)
		if n > 0 {
			d.last = time.Now()
			return n, nil
		}
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			if d.idle > 0 && time.Since(d.last) > d.idle {
				return 0, fmt.Errorf("no data for %s", d.idle)
			}
			continue
		}
		return n, err
	}
}

func serialSource(port string, baud int) source {
	return func(ctx context.Context, line func(string), connected func()) error {
		if port == "" {
			return errors.New("no serial port selected")
		}
		p, err := serial.Open(port, &serial.Mode{BaudRate: baud})
		if err != nil {
			return fmt.Errorf("open %s: %w", port, err)
		}
		defer p.Close()
		if err := p.SetReadTimeout(time.Second); err != nil {
			return err
		}
		connected()
		go func() {
			<-ctx.Done()
			p.Close()
		}()
		return readLines(ctx, &serialReader{ctx: ctx, p: p}, line)
	}
}

// serialReader retries timed-out reads (which return 0 bytes) until data
// arrives or ctx ends.
type serialReader struct {
	ctx context.Context
	p   serial.Port
}

func (s *serialReader) Read(b []byte) (int, error) {
	for {
		if err := s.ctx.Err(); err != nil {
			return 0, err
		}
		n, err := s.p.Read(b)
		if n > 0 || err != nil {
			return n, err
		}
	}
}

func udpSource(port int) source {
	return func(ctx context.Context, line func(string), connected func()) error {
		pc, err := net.ListenPacket("udp", fmt.Sprintf(":%d", port))
		if err != nil {
			return fmt.Errorf("listen on UDP port %d: %w", port, err)
		}
		defer pc.Close()
		connected()
		buf := make([]byte, 65536)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			pc.SetReadDeadline(time.Now().Add(time.Second))
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					continue
				}
				return err
			}
			for _, l := range strings.FieldsFunc(string(buf[:n]), func(r rune) bool { return r == '\n' || r == '\r' }) {
				if len(l) <= maxSentence {
					line(l)
				}
			}
		}
	}
}

func tcpSource(addr string, gpsd bool) source {
	return func(ctx context.Context, line func(string), connected func()) error {
		if addr == "" {
			return errors.New("no address set")
		}
		if _, _, err := net.SplitHostPort(addr); err != nil {
			return fmt.Errorf("address must be host:port: %w", err)
		}
		d := net.Dialer{Timeout: 10 * time.Second}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err != nil {
			return err
		}
		defer conn.Close()
		if gpsd {
			// Ask gpsd for NMEA so every source shares one parser.
			if _, err := io.WriteString(conn, `?WATCH={"enable":true,"nmea":true};`+"\n"); err != nil {
				return err
			}
		}
		connected()
		return readLines(ctx, &deadlineReader{ctx: ctx, conn: conn, idle: 30 * time.Second, last: time.Now()}, func(s string) {
			if len(s) > 0 && (s[0] == '$' || s[0] == '!') {
				line(s)
			}
		})
	}
}

// simSource replays a track as NMEA RMC and GGA sentences once per second
// (scaled by speed), looping at the end. Generating real sentences means
// the simulator exercises the same parser as a receiver.
func simSource(track []Point, speed float64, now func() time.Time) source {
	return func(ctx context.Context, line func(string), connected func()) error {
		if len(track) < 2 {
			return errors.New("the simulator needs a track with at least two points")
		}
		if speed <= 0 || speed > 100 {
			speed = 1
		}
		pts := timed(track)
		total := pts[len(pts)-1].Time.Sub(pts[0].Time)
		connected()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		var elapsed time.Duration
		for {
			t := pts[0].Time.Add(elapsed % (total + time.Second))
			lat, lon, ele, course, sp := interpolate(pts, t)
			ts := now().UTC()
			for _, s := range []string{rmcSentence(ts, lat, lon, sp, course), ggaSentence(ts, lat, lon, ele)} {
				line(s)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-tick.C:
			}
			elapsed += time.Duration(float64(time.Second) * speed)
		}
	}
}

// timed fills in missing timestamps at walking pace (1.4 m/s).
func timed(track []Point) []Point {
	pts := append([]Point(nil), track...)
	ok := true
	for i := range pts {
		if pts[i].Time.IsZero() || (i > 0 && !pts[i].Time.After(pts[i-1].Time)) {
			ok = false
			break
		}
	}
	if ok {
		return pts
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	pts[0].Time = start
	for i := 1; i < len(pts); i++ {
		d := Distance(pts[i-1].Lat, pts[i-1].Lon, pts[i].Lat, pts[i].Lon)
		pts[i].Time = pts[i-1].Time.Add(time.Duration(d/1.4*float64(time.Second)) + time.Millisecond)
	}
	return pts
}

func interpolate(pts []Point, t time.Time) (lat, lon float64, ele *float64, course, speed float64) {
	i := 1
	for i < len(pts)-1 && pts[i].Time.Before(t) {
		i++
	}
	a, b := pts[i-1], pts[i]
	span := b.Time.Sub(a.Time).Seconds()
	f := 0.0
	if span > 0 {
		f = math.Max(0, math.Min(1, t.Sub(a.Time).Seconds()/span))
	}
	lat = a.Lat + (b.Lat-a.Lat)*f
	lon = a.Lon + (b.Lon-a.Lon)*f
	if a.Ele != nil && b.Ele != nil {
		e := *a.Ele + (*b.Ele-*a.Ele)*f
		ele = &e
	}
	course = Bearing(a.Lat, a.Lon, b.Lat, b.Lon)
	if span > 0 {
		speed = Distance(a.Lat, a.Lon, b.Lat, b.Lon) / span
	}
	return
}

func nmeaCoord(v float64, pos, neg string, degWidth int) (string, string) {
	h := pos
	if v < 0 {
		h, v = neg, -v
	}
	deg := math.Floor(v)
	min := (v - deg) * 60
	return fmt.Sprintf("%0*d%07.4f", degWidth, int(deg), min), h
}

func withChecksum(body string) string {
	var cs byte
	for i := 0; i < len(body); i++ {
		cs ^= body[i]
	}
	return fmt.Sprintf("$%s*%02X", body, cs)
}

func rmcSentence(t time.Time, lat, lon, speed, course float64) string {
	la, lh := nmeaCoord(lat, "N", "S", 2)
	lo, oh := nmeaCoord(lon, "E", "W", 3)
	return withChecksum(fmt.Sprintf("GPRMC,%s,A,%s,%s,%s,%s,%.2f,%.1f,%s,,,S",
		t.Format("150405.00"), la, lh, lo, oh, speed/knots, course, t.Format("020106")))
}

func ggaSentence(t time.Time, lat, lon float64, ele *float64) string {
	la, lh := nmeaCoord(lat, "N", "S", 2)
	lo, oh := nmeaCoord(lon, "E", "W", 3)
	alt := ""
	if ele != nil {
		alt = fmt.Sprintf("%.1f", *ele)
	}
	return withChecksum(fmt.Sprintf("GPGGA,%s,%s,%s,%s,%s,1,10,0.9,%s,M,,M,,",
		t.Format("150405.00"), la, lh, lo, oh, alt))
}

// circleTrack is the simulator's default route: a 1 km loop.
func circleTrack(lat, lon float64) []Point {
	var pts []Point
	const r = 500.0 // metres
	for i := 0; i <= 72; i++ {
		a := float64(i) * 5 * math.Pi / 180
		dlat := r * math.Cos(a) / 111320
		dlon := r * math.Sin(a) / (111320 * math.Max(0.01, math.Cos(lat*math.Pi/180)))
		pts = append(pts, Point{Lat: lat + dlat, Lon: lon + dlon})
	}
	return pts
}

// SerialPorts lists serial devices.
func SerialPorts() ([]string, error) {
	ports, err := serial.GetPortsList()
	if ports == nil {
		ports = []string{}
	}
	return ports, err
}

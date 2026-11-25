package gps

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// Network position sources (ADR-0026) report positions, not NMEA. They are
// turned into RMC, GGA and GST sentences here and read by the same tracker
// as a receiver (ADR-0016), so freshness, tracks, the map and guidance
// treat them exactly like a GPS.

// netFix is a position from a network source.
type netFix struct {
	Lat, Lon float64
	Alt      *float64
	Speed    *float64 // metres per second
	Course   *float64 // degrees true
	Accuracy float64  // metres; 0 when unknown
}

// networkSentences encodes a position. GST comes first so the tracker has
// the accuracy when the position sentences arrive.
func networkSentences(t time.Time, p netFix) []string {
	t = t.UTC()
	la, lh := nmeaCoord(p.Lat, "N", "S", 2)
	lo, oh := nmeaCoord(p.Lon, "E", "W", 3)
	var out []string
	if p.Accuracy > 0 {
		a := fmt.Sprintf("%.1f", math.Min(p.Accuracy, 9999999))
		out = append(out, withChecksum(fmt.Sprintf("GPGST,%s,,%s,%s,0.0,%s,%s,", t.Format("150405.00"), a, a, a, a)))
	}
	speed, course := "", ""
	if p.Speed != nil && *p.Speed >= 0 && !math.IsInf(*p.Speed, 0) {
		speed = fmt.Sprintf("%.2f", *p.Speed/knots)
	}
	if p.Course != nil && *p.Course >= 0 && *p.Course <= 360 {
		course = fmt.Sprintf("%.1f", *p.Course)
	}
	out = append(out, withChecksum(fmt.Sprintf("GPRMC,%s,A,%s,%s,%s,%s,%s,%s,%s,,,A",
		t.Format("150405.00"), la, lh, lo, oh, speed, course, t.Format("020106"))))
	alt := ""
	if p.Alt != nil && math.Abs(*p.Alt) < 100000 {
		alt = fmt.Sprintf("%.1f", *p.Alt)
	}
	out = append(out, withChecksum(fmt.Sprintf("GPGGA,%s,%s,%s,%s,%s,1,00,,%s,M,,M,,",
		t.Format("150405.00"), la, lh, lo, oh, alt)))
	return out
}

// noFixSentence says a source is answering but has no position.
func noFixSentence(t time.Time) string {
	t = t.UTC()
	return withChecksum(fmt.Sprintf("GPRMC,%s,V,,,,,,,%s,,,N", t.Format("150405.00"), t.Format("020106")))
}

// BrowserFix is a position from the Geolocation API of a browser showing
// Arcana, posted by that browser.
type BrowserFix struct {
	Lat      float64  `json:"lat"`
	Lon      float64  `json:"lon"`
	Accuracy float64  `json:"accuracy"` // metres (95% confidence, per the W3C API)
	Altitude *float64 `json:"altitude"`
	Speed    *float64 `json:"speed"`
	Heading  *float64 `json:"heading"`
	// ClientID is the random identifier the browser keeps; only the
	// browser chosen in Settings may feed the source.
	ClientID string `json:"clientId"`
}

// Errors from PushBrowser.
var (
	ErrNoBrowserSource = errors.New("this browser's location is not a position source; choose it in Settings")
	ErrOtherBrowser    = errors.New("another browser was chosen as the position source in Settings")
)

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// PushBrowser hands a browser's position to the "browser" source.
func (m *Manager) PushBrowser(p BrowserFix) error {
	m.mu.Lock()
	ch, want := m.browser, m.settings.BrowserID
	m.mu.Unlock()
	if ch == nil {
		return ErrNoBrowserSource
	}
	if want != "" && p.ClientID != want {
		return ErrOtherBrowser
	}
	if !validCoord(p.Lat, p.Lon) || !finite(p.Accuracy) || p.Accuracy < 0 || p.Accuracy > 1e7 {
		return errors.New("not a valid position")
	}
	for _, v := range []**float64{&p.Altitude, &p.Speed, &p.Heading} {
		if *v != nil && !finite(**v) {
			*v = nil
		}
	}
	select {
	case ch <- p:
	default:
		// Keep only the newest position.
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- p:
		default:
		}
	}
	return nil
}

// WantsBrowser says whether the browser with this identifier should send
// its position.
func (m *Manager) WantsBrowser(clientID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.browser != nil && (m.settings.BrowserID == "" || m.settings.BrowserID == clientID)
}

// browserSource turns posted browser positions into sentences. The
// browser keeps posting while the page is open, so freshness works as for a
// receiver: if the page closes, the position goes stale and then lost.
func browserSource(ch <-chan BrowserFix, now func() time.Time) source {
	return func(ctx context.Context, line func(string), connected func()) error {
		if ch == nil {
			return errors.New("no browser channel")
		}
		connected()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case p := <-ch:
				nf := netFix{Lat: p.Lat, Lon: p.Lon, Alt: p.Altitude, Speed: p.Speed, Accuracy: p.Accuracy}
				if p.Speed != nil && *p.Speed > 0.5 {
					nf.Course = p.Heading
				}
				for _, s := range networkSentences(now(), nf) {
					line(s)
				}
			}
		}
	}
}

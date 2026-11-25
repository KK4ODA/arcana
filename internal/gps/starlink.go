package gps

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The Starlink dish answers gRPC on the local network at
// 192.168.100.1:9200 (service SpaceX.API.Device.Device, method Handle). Its
// location is only given out after the owner turns on "Allow access on
// local network" for Starlink location in the Starlink app, and some
// service plans no longer offer that at all.
//
// One request type and one response field are needed, so the call is made
// with the standard library (unencrypted HTTP/2, which net/http supports)
// and a few lines of protobuf decoding instead of the gRPC and protobuf
// modules. Field numbers are from the dish's own reflection service as
// published by the open-source starlink-grpc-tools and
// starlink-grpc-golang projects:
//
//	Request             { GetLocationRequest get_location = 1017; }
//	Response            { Status status = 2; GetLocationResponse get_location = 1017; }
//	Status              { int32 code = 1; string message = 2; }
//	GetLocationResponse { LLAPosition lla = 1; PositionSource source = 3;
//	                      double sigma_m = 4; double horizontal_speed_mps = 5; }
//	LLAPosition         { double lat = 1; double lon = 2; double alt = 3; }

// StarlinkDefaultAddress is where the dish listens on its own network.
const StarlinkDefaultAddress = "192.168.100.1:9200"

// starlinkPoll is how often the dish is asked; well inside the 5 s after
// which a position counts as stale.
const starlinkPoll = 2 * time.Second

const starlinkPath = "/SpaceX.API.Device.Device/Handle"

// getLocationRequest is the framed protobuf Request{get_location: {}}:
// field 1017, wire type 2 (tag varint 0xCA 0x3F), length 0.
var getLocationRequest = []byte{0, 0, 0, 0, 3, 0xCA, 0x3F, 0x00}

// ErrStarlinkDenied means the dish will not share its location.
var ErrStarlinkDenied = errors.New("the Starlink dish refused to share its location. In the Starlink app, find Starlink location (with the debug data settings) and turn on “Allow access on local network”. Some service plans no longer offer this")

// starlinkClient speaks gRPC's unencrypted HTTP/2 and never uses a proxy:
// the dish is on the local network.
func starlinkClient() *http.Client {
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	return &http.Client{
		Timeout:   5 * time.Second,
		Transport: &http.Transport{Protocols: p, Proxy: nil, DialContext: (&net.Dialer{Timeout: 4 * time.Second}).DialContext},
	}
}

type starlinkLocation struct {
	Lat, Lon, Alt float64
	HasLLA        bool
	Source        uint64
	Sigma         float64
	HSpeed        float64
}

// starlinkBasis describes the dish's PositionSource enum.
func starlinkBasis(src uint64) string {
	switch src {
	case 2:
		return "position from the dish's stored settings"
	case 3:
		return "position from an external source"
	case 4:
		return "position from the dish's GPS receiver"
	case 5:
		return "position from Starlink satellites (independent of GPS)"
	case 6, 7, 8, 9, 10:
		return "position from the dish's navigation system"
	}
	return ""
}

func starlinkGetLocation(ctx context.Context, c *http.Client, addr string) (starlinkLocation, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+starlinkPath, bytes.NewReader(getLocationRequest))
	if err != nil {
		return starlinkLocation{}, err
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")
	resp, err := c.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return starlinkLocation{}, ctx.Err()
		}
		return starlinkLocation{}, fmt.Errorf("cannot reach the Starlink dish at %s (is this computer on the Starlink network?): %w", addr, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return starlinkLocation{}, fmt.Errorf("reading from the Starlink dish: %w", err)
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/grpc") {
		return starlinkLocation{}, fmt.Errorf("%s does not answer like a Starlink dish (HTTP %d)", addr, resp.StatusCode)
	}
	// A call that fails at once carries its status in the headers
	// ("trailers-only"); otherwise in the trailers.
	code, msg := resp.Trailer.Get("Grpc-Status"), resp.Trailer.Get("Grpc-Message")
	if code == "" {
		code, msg = resp.Header.Get("Grpc-Status"), resp.Header.Get("Grpc-Message")
	}
	if code != "" && code != "0" {
		return starlinkLocation{}, grpcError(code, msg)
	}
	if len(body) < 5 {
		return starlinkLocation{}, errors.New("the Starlink dish sent an empty answer")
	}
	if body[0]&1 != 0 {
		return starlinkLocation{}, errors.New("the Starlink dish sent a compressed answer, which Arcana does not read")
	}
	n := binary.BigEndian.Uint32(body[1:5])
	if uint64(n) > uint64(len(body)-5) {
		return starlinkLocation{}, errors.New("the Starlink dish's answer is cut short")
	}
	return decodeLocationResponse(body[5 : 5+n])
}

func grpcError(code, msg string) error {
	if m, err := url.PathUnescape(msg); err == nil {
		msg = m
	}
	switch code {
	case "7": // PERMISSION_DENIED
		return ErrStarlinkDenied
	case "12": // UNIMPLEMENTED
		return errors.New("this Starlink dish does not offer its location")
	}
	if msg == "" {
		msg = "gRPC status " + code
	}
	return fmt.Errorf("the Starlink dish answered: %s", msg)
}

func decodeLocationResponse(b []byte) (starlinkLocation, error) {
	var loc starlinkLocation
	var statusCode uint64
	var statusMsg string
	var found bool
	err := pbFields(b, func(num uint64, wt int, v uint64, data []byte) error {
		switch {
		case num == 2 && wt == 2:
			return pbFields(data, func(n uint64, wt int, v uint64, d []byte) error {
				if n == 1 && wt == 0 {
					statusCode = v
				} else if n == 2 && wt == 2 {
					statusMsg = string(d)
				}
				return nil
			})
		case num == 1017 && wt == 2:
			found = true
			return pbFields(data, func(n uint64, wt int, v uint64, d []byte) error {
				switch {
				case n == 1 && wt == 2:
					loc.HasLLA = true
					return pbFields(d, func(n uint64, wt int, v uint64, _ []byte) error {
						if wt != 1 {
							return nil
						}
						switch n {
						case 1:
							loc.Lat = math.Float64frombits(v)
						case 2:
							loc.Lon = math.Float64frombits(v)
						case 3:
							loc.Alt = math.Float64frombits(v)
						}
						return nil
					})
				case n == 3 && wt == 0:
					loc.Source = v
				case n == 4 && wt == 1:
					loc.Sigma = math.Float64frombits(v)
				case n == 5 && wt == 1:
					loc.HSpeed = math.Float64frombits(v)
				}
				return nil
			})
		}
		return nil
	})
	if err != nil {
		return loc, fmt.Errorf("cannot read the Starlink dish's answer: %w", err)
	}
	if statusCode == 7 {
		return loc, ErrStarlinkDenied
	}
	if statusCode != 0 {
		return loc, grpcError(strconv.FormatUint(statusCode, 10), statusMsg)
	}
	if !found {
		return loc, errors.New("the Starlink dish's answer has no location")
	}
	return loc, nil
}

// pbFields walks a protobuf message, calling fn for each field with its
// number, wire type, and value (varint or fixed bits in v, bytes in data).
func pbFields(b []byte, fn func(num uint64, wt int, v uint64, data []byte) error) error {
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 {
			return errors.New("bad field key")
		}
		b = b[n:]
		num, wt := key>>3, int(key&7)
		var v uint64
		var data []byte
		switch wt {
		case 0:
			v, n = binary.Uvarint(b)
			if n <= 0 {
				return errors.New("bad varint")
			}
			b = b[n:]
		case 1:
			if len(b) < 8 {
				return errors.New("short fixed64")
			}
			v, b = binary.LittleEndian.Uint64(b), b[8:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || l > uint64(len(b)-n) {
				return errors.New("bad length")
			}
			data, b = b[n:n+int(l)], b[n+int(l):]
		case 5:
			if len(b) < 4 {
				return errors.New("short fixed32")
			}
			v, b = uint64(binary.LittleEndian.Uint32(b)), b[4:]
		default:
			return fmt.Errorf("unsupported wire type %d", wt)
		}
		if err := fn(num, wt, v, data); err != nil {
			return err
		}
	}
	return nil
}

// starlinkSource asks the dish for its location every interval and feeds
// the answer through the NMEA path.
func starlinkSource(addr string, c *http.Client, basis func(string), every time.Duration, now func() time.Time) source {
	return func(ctx context.Context, line func(string), connected func()) error {
		if _, _, err := net.SplitHostPort(addr); err != nil {
			return fmt.Errorf("the Starlink address must be host:port: %w", err)
		}
		tick := time.NewTicker(every)
		defer tick.Stop()
		first := true
		for {
			loc, err := starlinkGetLocation(ctx, c, addr)
			if err != nil {
				return err
			}
			if first {
				connected()
				first = false
			}
			basis(starlinkBasis(loc.Source))
			if !loc.HasLLA || loc.Source == 1 || !validCoord(loc.Lat, loc.Lon) || (loc.Lat == 0 && loc.Lon == 0) {
				line(noFixSentence(now()))
			} else {
				nf := netFix{Lat: loc.Lat, Lon: loc.Lon, Accuracy: loc.Sigma}
				if finite(loc.Alt) && loc.Alt != 0 {
					a := loc.Alt
					nf.Alt = &a
				}
				if finite(loc.HSpeed) && loc.HSpeed >= 0 {
					s := loc.HSpeed
					nf.Speed = &s
				}
				for _, s := range networkSentences(now(), nf) {
					line(s)
				}
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-tick.C:
			}
		}
	}
}

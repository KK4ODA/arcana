// Package mesh reads a Meshtastic radio: the nodes it has heard, their
// positions, and text messages on its channels. Arcana only listens; it
// never transmits on the mesh. It talks to the radio over the Meshtastic
// "stream API" (TCP port 4403 for radios on Wi-Fi or Ethernet, or USB
// serial), decoding the few protobuf messages it needs by field number
// (meshtastic/protobufs mesh.proto and portnums.proto).
package mesh

import (
	"encoding/binary"
	"errors"
	"math"
)

var errTruncated = errors.New("truncated protobuf")

// field is one protobuf field.
type field struct {
	num  int
	wire int
	u    uint64 // varint, fixed32, fixed64
	b    []byte // length-delimited
}

func uvarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * uint(i))
		if b[i] < 0x80 {
			return v, i + 1
		}
	}
	return 0, 0
}

// fields iterates over a message's fields; unknown wire types end it.
func fields(b []byte, fn func(f field)) error {
	for len(b) > 0 {
		key, n := uvarint(b)
		if n == 0 {
			return errTruncated
		}
		b = b[n:]
		f := field{num: int(key >> 3), wire: int(key & 7)}
		switch f.wire {
		case 0:
			v, n := uvarint(b)
			if n == 0 {
				return errTruncated
			}
			f.u, b = v, b[n:]
		case 1:
			if len(b) < 8 {
				return errTruncated
			}
			f.u, b = binary.LittleEndian.Uint64(b), b[8:]
		case 2:
			l, n := uvarint(b)
			if n == 0 || uint64(len(b)-n) < l {
				return errTruncated
			}
			f.b, b = b[n:n+int(l)], b[n+int(l):]
		case 5:
			if len(b) < 4 {
				return errTruncated
			}
			f.u, b = uint64(binary.LittleEndian.Uint32(b)), b[4:]
		default:
			return errors.New("unsupported protobuf wire type")
		}
		fn(f)
	}
	return nil
}

// Position is a node's reported position.
type Position struct {
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
	Alt  int32   `json:"alt,omitempty"`
	Time int64   `json:"time,omitempty"` // Unix seconds, when the node took it
}

func decodePosition(b []byte) (Position, bool) {
	var p Position
	var hasLat, hasLon bool
	err := fields(b, func(f field) {
		switch f.num {
		case 1:
			p.Lat, hasLat = float64(int32(uint32(f.u)))*1e-7, true
		case 2:
			p.Lon, hasLon = float64(int32(uint32(f.u)))*1e-7, true
		case 3:
			p.Alt = int32(uint32(f.u))
		case 4:
			p.Time = int64(uint32(f.u))
		}
	})
	// 0,0 means "no position" in Meshtastic.
	ok := err == nil && hasLat && hasLon && !(p.Lat == 0 && p.Lon == 0) &&
		p.Lat >= -90 && p.Lat <= 90 && p.Lon >= -180 && p.Lon <= 180
	return p, ok
}

// User is a node's identity.
type User struct {
	ID         string `json:"id"`
	LongName   string `json:"longName"`
	ShortName  string `json:"shortName"`
	HWModel    int    `json:"hwModel,omitempty"`
	IsLicensed bool   `json:"isLicensed,omitempty"`
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

func decodeUser(b []byte) (User, bool) {
	var u User
	err := fields(b, func(f field) {
		switch f.num {
		case 1:
			u.ID = clip(string(f.b), 16)
		case 2:
			u.LongName = clip(string(f.b), 40)
		case 3:
			u.ShortName = clip(string(f.b), 8)
		case 5:
			u.HWModel = int(f.u)
		case 6:
			u.IsLicensed = f.u != 0
		}
	})
	return u, err == nil
}

// nodeInfo is FromRadio.node_info.
type nodeInfo struct {
	num       uint32
	user      *User
	pos       *Position
	snr       float32
	lastHeard int64
	hops      int
	hasHops   bool
}

func decodeNodeInfo(b []byte) (nodeInfo, bool) {
	var n nodeInfo
	err := fields(b, func(f field) {
		switch f.num {
		case 1:
			n.num = uint32(f.u)
		case 2:
			if u, ok := decodeUser(f.b); ok {
				n.user = &u
			}
		case 3:
			if p, ok := decodePosition(f.b); ok {
				n.pos = &p
			}
		case 4:
			n.snr = math.Float32frombits(uint32(f.u))
		case 5:
			n.lastHeard = int64(uint32(f.u))
		case 9:
			n.hops, n.hasHops = int(f.u), true
		}
	})
	return n, err == nil && n.num != 0
}

// Port numbers (portnums.proto).
const (
	portText     = 1
	portPosition = 3
	portNodeInfo = 4
)

// packet is a decoded MeshPacket.
type packet struct {
	from, to uint32
	channel  uint32
	rxTime   int64
	snr      float32
	rssi     int32
	hopLimit int
	hopStart int
	port     int
	payload  []byte
	decoded  bool
}

func decodePacket(b []byte) (packet, bool) {
	var p packet
	err := fields(b, func(f field) {
		switch f.num {
		case 1:
			p.from = uint32(f.u)
		case 2:
			p.to = uint32(f.u)
		case 3:
			p.channel = uint32(f.u)
		case 4:
			p.decoded = true
			_ = fields(f.b, func(d field) {
				switch d.num {
				case 1:
					p.port = int(d.u)
				case 2:
					p.payload = d.b
				}
			})
		case 7:
			p.rxTime = int64(uint32(f.u))
		case 8:
			p.snr = math.Float32frombits(uint32(f.u))
		case 9:
			p.hopLimit = int(f.u)
		case 12:
			p.rssi = int32(uint32(f.u))
		case 15:
			p.hopStart = int(f.u)
		}
	})
	return p, err == nil
}

// ------------------------------------------------------------ encoding

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func appendKey(b []byte, num, wire int) []byte { return appendVarint(b, uint64(num<<3|wire)) }

func appendBytes(b []byte, num int, v []byte) []byte {
	b = appendKey(b, num, 2)
	b = appendVarint(b, uint64(len(v)))
	return append(b, v...)
}

// wantConfig is ToRadio{want_config_id}: ask the radio for its node list
// and settings, then live packets.
func wantConfig(id uint32) []byte {
	return appendVarint(appendKey(nil, 3, 0), uint64(id))
}

// heartbeat is ToRadio{heartbeat{nonce}}: keeps the connection open.
func heartbeat(nonce uint32) []byte {
	inner := appendVarint(appendKey(nil, 1, 0), uint64(nonce))
	return appendBytes(nil, 7, inner)
}

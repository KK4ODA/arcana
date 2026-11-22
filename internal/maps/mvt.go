package maps

import (
	"encoding/binary"
	"errors"
	"math"
)

// A minimal Mapbox Vector Tile (MVT 2.1) decoder: enough to read named
// point features (places) for the offline gazetteer. Tiles are untrusted;
// the decoder bounds every length and never allocates more than the input.

// Feature is a decoded point feature.
type Feature struct {
	Layer string
	Props map[string]any
	// X, Y are tile-local coordinates of the first point (0..Extent).
	X, Y   int64
	Extent uint32
	Type   uint32 // 1 point, 2 line, 3 polygon
}

var errTile = errors.New("malformed vector tile")

type pbuf struct {
	b []byte
	i int
}

func (p *pbuf) eof() bool { return p.i >= len(p.b) }

func (p *pbuf) varint() (uint64, error) {
	v, n := binary.Uvarint(p.b[p.i:])
	if n <= 0 {
		return 0, errTile
	}
	p.i += n
	return v, nil
}

func (p *pbuf) key() (field int, wire int, err error) {
	k, err := p.varint()
	if err != nil {
		return 0, 0, err
	}
	return int(k >> 3), int(k & 7), nil
}

func (p *pbuf) bytes() ([]byte, error) {
	n, err := p.varint()
	if err != nil {
		return nil, err
	}
	if n > uint64(len(p.b)-p.i) {
		return nil, errTile
	}
	b := p.b[p.i : p.i+int(n)]
	p.i += int(n)
	return b, nil
}

func (p *pbuf) skip(wire int) error {
	switch wire {
	case 0:
		_, err := p.varint()
		return err
	case 1:
		p.i += 8
	case 2:
		_, err := p.bytes()
		return err
	case 5:
		p.i += 4
	default:
		return errTile
	}
	if p.i > len(p.b) {
		return errTile
	}
	return nil
}

func packed(b []byte) ([]uint32, error) {
	p := pbuf{b: b}
	var out []uint32
	for !p.eof() {
		v, err := p.varint()
		if err != nil {
			return nil, err
		}
		out = append(out, uint32(v))
	}
	return out, nil
}

func zigzag(v uint32) int64 { return int64(v>>1) ^ -int64(v&1) }

func decodeValue(b []byte) (any, error) {
	p := pbuf{b: b}
	for !p.eof() {
		f, w, err := p.key()
		if err != nil {
			return nil, err
		}
		switch {
		case f == 1 && w == 2:
			s, err := p.bytes()
			return string(s), err
		case f == 2 && w == 5:
			if p.i+4 > len(p.b) {
				return nil, errTile
			}
			v := math.Float32frombits(binary.LittleEndian.Uint32(p.b[p.i:]))
			return float64(v), nil
		case f == 3 && w == 1:
			if p.i+8 > len(p.b) {
				return nil, errTile
			}
			return math.Float64frombits(binary.LittleEndian.Uint64(p.b[p.i:])), nil
		case (f == 4 || f == 5) && w == 0:
			v, err := p.varint()
			return float64(int64(v)), err
		case f == 6 && w == 0:
			v, err := p.varint()
			return float64(zigzag(uint32(v))), err
		case f == 7 && w == 0:
			v, err := p.varint()
			return v != 0, err
		default:
			if err := p.skip(w); err != nil {
				return nil, err
			}
		}
	}
	return nil, nil
}

// DecodeLayer returns features of one layer (decompressed tile bytes).
// Only features with a first point are returned.
func DecodeLayer(tile []byte, want string) ([]Feature, error) {
	p := pbuf{b: tile}
	var out []Feature
	for !p.eof() {
		f, w, err := p.key()
		if err != nil {
			return nil, err
		}
		if f != 3 || w != 2 {
			if err := p.skip(w); err != nil {
				return nil, err
			}
			continue
		}
		layer, err := p.bytes()
		if err != nil {
			return nil, err
		}
		feats, err := decodeLayer(layer, want)
		if err != nil {
			return nil, err
		}
		out = append(out, feats...)
	}
	return out, nil
}

func decodeLayer(b []byte, want string) ([]Feature, error) {
	p := pbuf{b: b}
	var name string
	var keys []string
	var values []any
	var rawFeatures [][]byte
	extent := uint32(4096)
	for !p.eof() {
		f, w, err := p.key()
		if err != nil {
			return nil, err
		}
		switch {
		case f == 1 && w == 2:
			s, err := p.bytes()
			if err != nil {
				return nil, err
			}
			name = string(s)
			if name != want {
				return nil, nil
			}
		case f == 2 && w == 2:
			fb, err := p.bytes()
			if err != nil {
				return nil, err
			}
			rawFeatures = append(rawFeatures, fb)
		case f == 3 && w == 2:
			s, err := p.bytes()
			if err != nil {
				return nil, err
			}
			keys = append(keys, string(s))
		case f == 4 && w == 2:
			vb, err := p.bytes()
			if err != nil {
				return nil, err
			}
			v, err := decodeValue(vb)
			if err != nil {
				return nil, err
			}
			values = append(values, v)
		case f == 5 && w == 0:
			v, err := p.varint()
			if err != nil {
				return nil, err
			}
			if v > 0 && v < 1<<20 {
				extent = uint32(v)
			}
		default:
			if err := p.skip(w); err != nil {
				return nil, err
			}
		}
	}
	if name != want {
		return nil, nil
	}
	out := make([]Feature, 0, len(rawFeatures))
	for _, fb := range rawFeatures {
		ft := Feature{Layer: name, Extent: extent, Props: map[string]any{}}
		fp := pbuf{b: fb}
		var geom []uint32
		for !fp.eof() {
			f, w, err := fp.key()
			if err != nil {
				return nil, err
			}
			switch {
			case f == 2 && w == 2:
				tb, err := fp.bytes()
				if err != nil {
					return nil, err
				}
				tags, err := packed(tb)
				if err != nil {
					return nil, err
				}
				for i := 0; i+1 < len(tags); i += 2 {
					if int(tags[i]) < len(keys) && int(tags[i+1]) < len(values) {
						ft.Props[keys[tags[i]]] = values[tags[i+1]]
					}
				}
			case f == 3 && w == 0:
				v, err := fp.varint()
				if err != nil {
					return nil, err
				}
				ft.Type = uint32(v)
			case f == 4 && w == 2:
				gb, err := fp.bytes()
				if err != nil {
					return nil, err
				}
				if geom, err = packed(gb); err != nil {
					return nil, err
				}
			default:
				if err := fp.skip(w); err != nil {
					return nil, err
				}
			}
		}
		// First command must be MoveTo (id 1) with at least one point.
		if len(geom) >= 3 && geom[0]&7 == 1 && geom[0]>>3 >= 1 {
			ft.X, ft.Y = zigzag(geom[1]), zigzag(geom[2])
			out = append(out, ft)
		}
	}
	return out, nil
}

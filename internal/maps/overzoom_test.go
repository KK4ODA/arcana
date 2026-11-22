package maps

import (
	"encoding/binary"
	"os"
	"testing"
)

// buildTile encodes a single-layer tile whose features are given as
// (type, geometry commands) pairs; each feature gets tag 0=0.
func buildTile(name string, feats [][]uint32, types []uint64) []byte {
	var layer []byte
	layer = binary.AppendUvarint(layer, 15<<3|0)
	layer = binary.AppendUvarint(layer, 2)
	layer = appendBytesField(layer, 1, []byte(name))
	for i, g := range feats {
		var f []byte
		f = binary.AppendUvarint(f, 1<<3|0)
		f = binary.AppendUvarint(f, uint64(i+1))
		f = appendBytesField(f, 2, []byte{0, 0})
		f = binary.AppendUvarint(f, 3<<3|0)
		f = binary.AppendUvarint(f, types[i])
		var pg []byte
		for _, v := range g {
			pg = binary.AppendUvarint(pg, uint64(v))
		}
		f = appendBytesField(f, 4, pg)
		layer = appendBytesField(layer, 2, f)
	}
	layer = appendBytesField(layer, 3, []byte("kind"))
	layer = appendBytesField(layer, 4, appendBytesField(nil, 1, []byte("water")))
	layer = binary.AppendUvarint(layer, 5<<3|0)
	layer = binary.AppendUvarint(layer, 4096)
	return appendBytesField(nil, 3, layer)
}

func geom(cmds ...any) []uint32 {
	var e encoder
	for i := 0; i < len(cmds); {
		switch c := cmds[i].(type) {
		case string:
			switch c {
			case "M":
				pts := cmds[i+1].([]pt)
				e.cmd(1, len(pts))
				for _, p := range pts {
					e.point(p)
				}
				i += 2
			case "L":
				pts := cmds[i+1].([]pt)
				e.cmd(2, len(pts))
				for _, p := range pts {
					e.point(p)
				}
				i += 2
			case "Z":
				e.cmd(7, 1)
				i++
			}
		}
	}
	return e.out
}

type decodedFeature struct {
	typ   uint64
	parts [][]pt
}

func decodeAll(t *testing.T, tile []byte) (string, []decodedFeature, int) {
	t.Helper()
	p := pbuf{b: tile}
	var name string
	var out []decodedFeature
	others := 0
	for !p.eof() {
		f, w, err := p.key()
		if err != nil || f != 3 || w != 2 {
			t.Fatalf("unexpected tile field %d/%d %v", f, w, err)
		}
		lb, _ := p.bytes()
		lp := pbuf{b: lb}
		for !lp.eof() {
			f, w, _ := lp.key()
			switch {
			case f == 1 && w == 2:
				b, _ := lp.bytes()
				name = string(b)
			case f == 2 && w == 2:
				fb, _ := lp.bytes()
				fp := pbuf{b: fb}
				var df decodedFeature
				for !fp.eof() {
					ff, fw, _ := fp.key()
					switch {
					case ff == 3 && fw == 0:
						df.typ, _ = fp.varint()
					case ff == 4 && fw == 2:
						gb, _ := fp.bytes()
						g, _ := packed(gb)
						df.parts, err = decodeGeometry(g)
						if err != nil {
							t.Fatal(err)
						}
					default:
						fp.skip(fw)
					}
				}
				out = append(out, df)
			default:
				lp.skip(w)
				others++
			}
		}
	}
	return name, out, others
}

func TestOverzoom(t *testing.T) {
	square := geom("M", []pt{{0, 0}}, "L", []pt{{4096, 0}, {4096, 4096}, {0, 4096}}, "Z")
	line := geom("M", []pt{{0, 2048}}, "L", []pt{{4096, 2048}})
	points := geom("M", []pt{{100, 100}, {3000, 1000}})
	tile := buildTile("water", [][]uint32{square, line, points}, []uint64{3, 2, 1})

	// Child (1, 0) of tile (0, 0) at one zoom level deeper.
	out, err := Overzoom(tile, 0, 1, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	name, feats, others := decodeAll(t, out)
	if name != "water" || len(feats) != 3 || others < 4 {
		t.Fatalf("name %q, %d features, %d other fields", name, len(feats), others)
	}
	poly := feats[0].parts[0]
	if ringArea(poly) <= 0 {
		t.Fatalf("polygon orientation lost: %v", poly)
	}
	for _, p := range poly {
		if p.x < -256 || p.x > 4352 || p.y < -256 || p.y > 4352 {
			t.Fatalf("polygon not clipped: %v", poly)
		}
	}
	// The line y=2048 maps to y=4096 in the child and is kept within the
	// buffer, clipped on the left.
	l := feats[1].parts[0]
	if len(l) != 2 || l[0] != (pt{-256, 4096}) || l[1] != (pt{4096, 4096}) {
		t.Fatalf("line = %v", l)
	}
	// (100,100) maps outside; (3000,1000) maps to (1904,2000).
	if len(feats[2].parts) != 1 || feats[2].parts[0][0] != (pt{1904, 2000}) {
		t.Fatalf("points = %v", feats[2].parts)
	}

	// A child far from every feature drops the layer entirely.
	tiny := buildTile("dots", [][]uint32{geom("M", []pt{{10, 10}})}, []uint64{1})
	out, err = Overzoom(tiny, 0, 2, 3, 3)
	if err != nil || len(out) != 0 {
		t.Fatalf("expected empty tile, got %d bytes, %v", len(out), err)
	}

	// Malformed input fails cleanly.
	if _, err := Overzoom([]byte{0x1a, 0x05, 0x12, 0x03, 0x22, 0xff}, 0, 1, 0, 0); err == nil {
		t.Fatal("malformed tile accepted")
	}
}

func TestClipHoles(t *testing.T) {
	c := clipper{min: 0, max: 100, scale: 1}
	outer := []pt{{-50, -50}, {150, -50}, {150, 150}, {-50, 150}}
	hole := []pt{{200, 200}, {200, 250}, {250, 250}, {250, 200}} // outside: dropped
	inner := []pt{{40, 40}, {40, 60}, {60, 60}, {60, 40}}
	if ringArea(outer) <= 0 || ringArea(inner) >= 0 {
		t.Fatal("test rings have wrong orientation")
	}
	g := c.polygons([][]pt{outer, hole, inner})
	parts, err := decodeGeometry(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 || ringArea(parts[0]) <= 0 || ringArea(parts[1]) >= 0 {
		t.Fatalf("parts = %v", parts)
	}
	// An exterior clipped away takes its holes with it.
	far := []pt{{500, 500}, {600, 500}, {600, 600}, {500, 600}}
	if g := c.polygons([][]pt{far, inner}); len(g) != 0 {
		t.Fatalf("orphan hole kept: %v", g)
	}
}

// TestOverzoomReal derives tiles from a real archive (set
// ARCANA_TEST_PMTILES) and checks each layer survives.
func TestOverzoomReal(t *testing.T) {
	path := os.Getenv("ARCANA_TEST_PMTILES")
	if path == "" {
		t.Skip("ARCANA_TEST_PMTILES not set")
	}
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	h := a.Header
	b := h.Bounds()
	z := uint8(10)
	x, y := lonToX((b[0]+b[2])/2, z), latToY((b[1]+b[3])/2, z)
	data, err := a.Tile(z, x, y)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := Decompress(data, h.TileCompression)
	if err != nil {
		t.Fatal(err)
	}
	for _, dz := range []uint8{1, 3, 5} {
		cz := z + dz
		cx, cy := x<<dz+(1<<dz)/2, y<<dz+(1<<dz)/2
		out, err := Overzoom(raw, z, cz, cx, cy)
		if err != nil {
			t.Fatal(err)
		}
		earth, err := DecodeLayer(out, "earth")
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("z%d tile %d bytes -> z%d child %d bytes, earth features %d", z, len(raw), cz, len(out), len(earth))
		if len(out) == 0 {
			t.Fatal("derived tile is empty")
		}
	}
}

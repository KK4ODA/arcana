//go:build acceptance && linux

package acceptance

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"math"
	"os"
	"sort"

	"github.com/KK4ODA/arcana/internal/maps"
)

// sampleTown is the one named place in the synthetic test map.
const (
	sampleTown    = "Arcana Test Town"
	sampleTownLon = 6.13
	sampleTownLat = 49.61
)

func pbBytes(out []byte, field int, b []byte) []byte {
	out = binary.AppendUvarint(out, uint64(field)<<3|2)
	out = binary.AppendUvarint(out, uint64(len(b)))
	return append(out, b...)
}

func pbVarint(out []byte, field int, v uint64) []byte {
	out = binary.AppendUvarint(out, uint64(field)<<3)
	return binary.AppendUvarint(out, v)
}

func packed(vs ...uint32) []byte {
	var b []byte
	for _, v := range vs {
		b = binary.AppendUvarint(b, uint64(v))
	}
	return b
}

func zz(v int32) uint32 { return uint32((v << 1) ^ (v >> 31)) }

// sampleTile builds a vector tile with an "earth" polygon covering the tile
// and, when the town falls inside, a "places" point named sampleTown.
func sampleTile(z uint8, x, y uint32) []byte {
	n := math.Exp2(float64(z))
	fx := (sampleTownLon + 180) / 360 * n
	lr := sampleTownLat * math.Pi / 180
	fy := (1 - math.Log(math.Tan(lr)+1/math.Cos(lr))/math.Pi) / 2 * n
	px, py := int32((fx-float64(x))*4096), int32((fy-float64(y))*4096)

	var earthFeat []byte
	earthFeat = pbVarint(earthFeat, 3, 3) // polygon
	earthFeat = pbBytes(earthFeat, 4, packed(1<<3|1, zz(0), zz(0), 3<<3|2, zz(4096), zz(0), zz(0), zz(4096), zz(-4096), zz(0), 1<<3|7))
	var earth []byte
	earth = pbVarint(earth, 15, 2)
	earth = pbBytes(earth, 1, []byte("earth"))
	earth = pbBytes(earth, 2, earthFeat)
	earth = pbVarint(earth, 5, 4096)
	tile := pbBytes(nil, 3, earth)

	if px >= 0 && px < 4096 && py >= 0 && py < 4096 {
		var feat []byte
		feat = pbVarint(feat, 1, 1)
		feat = pbBytes(feat, 2, packed(0, 0, 1, 1))
		feat = pbVarint(feat, 3, 1) // point
		feat = pbBytes(feat, 4, packed(1<<3|1, zz(px), zz(py)))
		var places []byte
		places = pbVarint(places, 15, 2)
		places = pbBytes(places, 1, []byte("places"))
		places = pbBytes(places, 2, feat)
		places = pbBytes(places, 3, []byte("name"))
		places = pbBytes(places, 3, []byte("kind"))
		places = pbBytes(places, 4, pbBytes(nil, 1, []byte(sampleTown)))
		places = pbBytes(places, 4, pbBytes(nil, 1, []byte("locality")))
		places = pbVarint(places, 5, 4096)
		tile = pbBytes(tile, 3, places)
	}
	return tile
}

func gz(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

// writeSampleMap writes a small PMTiles archive (zoom 0-10) around the
// sample town, using Arcana's own writer.
func writeSampleMap(path string) error {
	const maxZoom = 10
	type tile struct {
		id   uint64
		data []byte
	}
	var tiles []tile
	for z := uint8(0); z <= maxZoom; z++ {
		n := math.Exp2(float64(z))
		x := uint32((sampleTownLon + 180) / 360 * n)
		lr := sampleTownLat * math.Pi / 180
		y := uint32((1 - math.Log(math.Tan(lr)+1/math.Cos(lr))/math.Pi) / 2 * n)
		tiles = append(tiles, tile{maps.ZXYToID(z, x, y), gz(sampleTile(z, x, y))})
	}
	sort.Slice(tiles, func(i, j int) bool { return tiles[i].id < tiles[j].id })
	var data []byte
	var entries []maps.Entry
	for _, t := range tiles {
		entries = append(entries, maps.Entry{TileID: t.id, Offset: uint64(len(data)), Length: uint32(len(t.data)), RunLength: 1})
		data = append(data, t.data...)
	}
	h := maps.Header{TileType: maps.TileTypeMVT, TileCompression: maps.CompressionGzip, Clustered: true,
		MinZoom: 0, MaxZoom: maxZoom, MinLonE7: 57000000, MinLatE7: 494000000, MaxLonE7: 65000000, MaxLatE7: 502000000,
		CenterZoom: 8, CenterLonE7: int32(sampleTownLon * 1e7), CenterLatE7: int32(sampleTownLat * 1e7)}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return maps.WriteArchive(f, h, gz([]byte(`{"name":"Arcana acceptance test map"}`)), entries, bytes.NewReader(data), uint64(len(data)))
}

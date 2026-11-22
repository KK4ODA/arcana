package maps

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestTileIDs(t *testing.T) {
	cases := []struct {
		z    uint8
		x, y uint32
		id   uint64
	}{
		{0, 0, 0, 0}, {1, 0, 0, 1}, {1, 0, 1, 2}, {1, 1, 1, 3}, {1, 1, 0, 4}, {2, 0, 0, 5},
	}
	for _, c := range cases {
		if got := ZXYToID(c.z, c.x, c.y); got != c.id {
			t.Errorf("ZXYToID(%d,%d,%d) = %d want %d", c.z, c.x, c.y, got, c.id)
		}
	}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		z := uint8(r.Intn(20))
		x, y := uint32(r.Int63n(1<<z)), uint32(r.Int63n(1<<z))
		gz, gx, gy := IDToZXY(ZXYToID(z, x, y))
		if gz != z || gx != x || gy != y {
			t.Fatalf("round trip %d/%d/%d -> %d/%d/%d", z, x, y, gz, gx, gy)
		}
	}
}

func TestDirectoryRoundTrip(t *testing.T) {
	entries := []Entry{{TileID: 0, Offset: 0, Length: 10, RunLength: 1}, {TileID: 1, Offset: 10, Length: 5, RunLength: 3},
		{TileID: 9, Offset: 0, Length: 10, RunLength: 1}, {TileID: 100, Offset: 15, Length: 7, RunLength: 0}}
	got, err := DeserializeEntries(SerializeEntries(entries))
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(got) != fmt.Sprint(entries) {
		t.Fatalf("got %v want %v", got, entries)
	}
	// Hostile input: huge count.
	var b bytes.Buffer
	var tmp [10]byte
	b.Write(tmp[:binary.PutUvarint(tmp[:], 1<<40)])
	if _, err := DeserializeEntries(b.Bytes()); err == nil {
		t.Fatal("implausible directory accepted")
	}
}

// writeTestArchive builds an archive with distinct tiles for zooms 0..maxz,
// with an ocean-like duplicate run, enough entries to need leaf directories.
func writeTestArchive(t *testing.T, path string, maxz uint8) map[uint64][]byte {
	t.Helper()
	var data bytes.Buffer
	var entries []Entry
	tiles := map[uint64][]byte{}
	ocean := []byte("ocean")
	oceanOff := uint64(0)
	data.Write(ocean)
	for z := uint8(0); z <= maxz; z++ {
		n := uint32(1) << z
		for x := uint32(0); x < n; x++ {
			for y := uint32(0); y < n; y++ {
				id := ZXYToID(z, x, y)
				if z >= 3 && x%2 == 0 {
					entries = append(entries, Entry{TileID: id, Offset: oceanOff, Length: uint32(len(ocean)), RunLength: 1})
					tiles[id] = ocean
					continue
				}
				body := []byte(fmt.Sprintf("tile %d/%d/%d", z, x, y))
				entries = append(entries, Entry{TileID: id, Offset: uint64(data.Len()), Length: uint32(len(body)), RunLength: 1})
				data.Write(body)
				tiles[id] = body
			}
		}
	}
	sortEntries(entries)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := Header{TileType: TileTypeMVT, TileCompression: CompressionNone, MinZoom: 0, MaxZoom: maxz, Clustered: false,
		MinLonE7: -1800000000, MinLatE7: -850511287, MaxLonE7: 1800000000, MaxLatE7: 850511287}
	meta := gzipBytes([]byte(`{"name":"test"}`))
	if err := WriteArchive(f, h, meta, entries, bytes.NewReader(data.Bytes()), uint64(data.Len())); err != nil {
		t.Fatal(err)
	}
	return tiles
}

func TestWriteAndReadArchive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.pmtiles")
	tiles := writeTestArchive(t, p, 7) // 21845 tiles -> leaf directories needed
	a, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.Header.LeafLength == 0 {
		t.Fatal("expected leaf directories for a large archive")
	}
	for _, c := range [][3]uint32{{0, 0, 0}, {5, 3, 17}, {7, 127, 127}, {7, 64, 1}} {
		got, err := a.Tile(uint8(c[0]), c[1], c[2])
		if err != nil {
			t.Fatalf("%v: %v", c, err)
		}
		if !bytes.Equal(got, tiles[ZXYToID(uint8(c[0]), c[1], c[2])]) {
			t.Fatalf("%v: got %q", c, got)
		}
	}
	if _, err := a.Tile(8, 0, 0); err != ErrNoTile {
		t.Fatalf("beyond maxzoom: %v", err)
	}
	meta, _ := a.Metadata()
	if string(meta) != `{"name":"test"}` {
		t.Fatalf("metadata %q", meta)
	}
	n := 0
	a.AllEntries(func(e Entry) bool { n += int(e.RunLength); return true })
	if n != len(tiles) {
		t.Fatalf("walked %d tiles, want %d", n, len(tiles))
	}
}

func TestTruncatedArchiveRejected(t *testing.T) {
	p := filepath.Join(t.TempDir(), "t.pmtiles")
	writeTestArchive(t, p, 4)
	data, _ := os.ReadFile(p)
	os.WriteFile(p, data[:len(data)/2], 0o644)
	if _, err := Open(p); err == nil {
		t.Fatal("truncated archive accepted")
	}
}

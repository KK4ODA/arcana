// Package maps implements Arcana's offline map support: a PMTiles v3 reader
// and writer, regional extraction from a remote PMTiles archive using HTTP
// range requests, a merged tile server over all installed map sets, and a
// place-name index built from the installed tiles.
//
// PMTiles is a public-domain (CC0) specification:
// https://github.com/protomaps/PMTiles/blob/main/spec/v3/spec.md. This is an
// independent implementation of the parts Arcana needs; it avoids the cloud
// storage SDKs that the reference Go package depends on.
package maps

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

// HeaderSize is the fixed size of a PMTiles v3 header.
const HeaderSize = 127

// Compression values.
const (
	CompressionUnknown = 0
	CompressionNone    = 1
	CompressionGzip    = 2
	CompressionBrotli  = 3
	CompressionZstd    = 4
)

// Tile types.
const (
	TileTypeUnknown = 0
	TileTypeMVT     = 1
	TileTypePNG     = 2
	TileTypeJPEG    = 3
	TileTypeWebP    = 4
	TileTypeAVIF    = 5
)

// Header is a PMTiles v3 header.
type Header struct {
	RootOffset, RootLength         uint64
	MetadataOffset, MetadataLength uint64
	LeafOffset, LeafLength         uint64
	DataOffset, DataLength         uint64
	AddressedTiles                 uint64
	TileEntries                    uint64
	TileContents                   uint64
	Clustered                      bool
	InternalCompression            uint8
	TileCompression                uint8
	TileType                       uint8
	MinZoom, MaxZoom               uint8
	MinLonE7, MinLatE7             int32
	MaxLonE7, MaxLatE7             int32
	CenterZoom                     uint8
	CenterLonE7, CenterLatE7       int32
}

// ErrNotPMTiles is returned for files without the PMTiles v3 magic.
var ErrNotPMTiles = errors.New("not a PMTiles version 3 archive")

// ParseHeader decodes the 127-byte header.
func ParseHeader(b []byte) (Header, error) {
	if len(b) < HeaderSize || string(b[0:7]) != "PMTiles" || b[7] != 3 {
		return Header{}, ErrNotPMTiles
	}
	le := binary.LittleEndian
	h := Header{
		RootOffset: le.Uint64(b[8:]), RootLength: le.Uint64(b[16:]),
		MetadataOffset: le.Uint64(b[24:]), MetadataLength: le.Uint64(b[32:]),
		LeafOffset: le.Uint64(b[40:]), LeafLength: le.Uint64(b[48:]),
		DataOffset: le.Uint64(b[56:]), DataLength: le.Uint64(b[64:]),
		AddressedTiles: le.Uint64(b[72:]), TileEntries: le.Uint64(b[80:]), TileContents: le.Uint64(b[88:]),
		Clustered: b[96] == 1, InternalCompression: b[97], TileCompression: b[98], TileType: b[99],
		MinZoom: b[100], MaxZoom: b[101],
		MinLonE7: int32(le.Uint32(b[102:])), MinLatE7: int32(le.Uint32(b[106:])),
		MaxLonE7: int32(le.Uint32(b[110:])), MaxLatE7: int32(le.Uint32(b[114:])),
		CenterZoom: b[118], CenterLonE7: int32(le.Uint32(b[119:])), CenterLatE7: int32(le.Uint32(b[123:])),
	}
	if h.MaxZoom > 30 || h.MinZoom > h.MaxZoom {
		return Header{}, fmt.Errorf("%w: invalid zoom range", ErrNotPMTiles)
	}
	return h, nil
}

// Bytes encodes the header.
func (h Header) Bytes() []byte {
	b := make([]byte, HeaderSize)
	copy(b, "PMTiles")
	b[7] = 3
	le := binary.LittleEndian
	le.PutUint64(b[8:], h.RootOffset)
	le.PutUint64(b[16:], h.RootLength)
	le.PutUint64(b[24:], h.MetadataOffset)
	le.PutUint64(b[32:], h.MetadataLength)
	le.PutUint64(b[40:], h.LeafOffset)
	le.PutUint64(b[48:], h.LeafLength)
	le.PutUint64(b[56:], h.DataOffset)
	le.PutUint64(b[64:], h.DataLength)
	le.PutUint64(b[72:], h.AddressedTiles)
	le.PutUint64(b[80:], h.TileEntries)
	le.PutUint64(b[88:], h.TileContents)
	if h.Clustered {
		b[96] = 1
	}
	b[97], b[98], b[99] = h.InternalCompression, h.TileCompression, h.TileType
	b[100], b[101] = h.MinZoom, h.MaxZoom
	le.PutUint32(b[102:], uint32(h.MinLonE7))
	le.PutUint32(b[106:], uint32(h.MinLatE7))
	le.PutUint32(b[110:], uint32(h.MaxLonE7))
	le.PutUint32(b[114:], uint32(h.MaxLatE7))
	b[118] = h.CenterZoom
	le.PutUint32(b[119:], uint32(h.CenterLonE7))
	le.PutUint32(b[123:], uint32(h.CenterLatE7))
	return b
}

// Bounds returns [west, south, east, north] in degrees.
func (h Header) Bounds() [4]float64 {
	return [4]float64{float64(h.MinLonE7) / 1e7, float64(h.MinLatE7) / 1e7, float64(h.MaxLonE7) / 1e7, float64(h.MaxLatE7) / 1e7}
}

// Entry is a directory entry. RunLength 0 marks a pointer to a leaf
// directory (Offset/Length relative to the leaf section); otherwise Offset is
// relative to the tile data section and the same tile repeats for RunLength
// consecutive tile IDs.
type Entry struct {
	TileID    uint64
	Offset    uint64
	Length    uint32
	RunLength uint32
}

// ---------------------------------------------------------------- tile IDs

// ZXYToID converts tile coordinates to a PMTiles tile ID (Hilbert order
// within each zoom level, zoom levels concatenated).
func ZXYToID(z uint8, x, y uint32) uint64 {
	var acc uint64
	if z > 0 {
		acc = (uint64(1)<<(2*uint(z)) - 1) / 3
	}
	n := uint64(1) << z
	xx, yy := uint64(x), uint64(y)
	var d uint64
	for s := n / 2; s > 0; s /= 2 {
		var rx, ry uint64
		if xx&s > 0 {
			rx = 1
		}
		if yy&s > 0 {
			ry = 1
		}
		d += s * s * ((3 * rx) ^ ry)
		xx, yy = rotate(n, xx, yy, rx, ry)
	}
	return acc + d
}

func rotate(n, x, y, rx, ry uint64) (uint64, uint64) {
	if ry == 0 {
		if rx == 1 {
			x = n - 1 - x
			y = n - 1 - y
		}
		x, y = y, x
	}
	return x, y
}

// IDToZXY converts a tile ID back to coordinates.
func IDToZXY(id uint64) (uint8, uint32, uint32) {
	var acc uint64
	var z uint8
	for ; z < 32; z++ {
		num := uint64(1) << (2 * uint(z))
		if acc+num > id {
			break
		}
		acc += num
	}
	n := uint64(1) << z
	t := id - acc
	var x, y uint64
	for s := uint64(1); s < n; s *= 2 {
		rx := 1 & (t / 2)
		ry := 1 & (t ^ rx)
		x, y = rotate(s, x, y, rx, ry)
		x += s * rx
		y += s * ry
		t /= 4
	}
	return z, uint32(x), uint32(y)
}

// ---------------------------------------------------------------- directories

func putUvarint(buf *bytes.Buffer, v uint64) {
	var tmp [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(tmp[:], v)
	buf.Write(tmp[:n])
}

// SerializeEntries encodes a directory (uncompressed).
func SerializeEntries(entries []Entry) []byte {
	var buf bytes.Buffer
	putUvarint(&buf, uint64(len(entries)))
	var last uint64
	for _, e := range entries {
		putUvarint(&buf, e.TileID-last)
		last = e.TileID
	}
	for _, e := range entries {
		putUvarint(&buf, uint64(e.RunLength))
	}
	for _, e := range entries {
		putUvarint(&buf, uint64(e.Length))
	}
	for i, e := range entries {
		if i > 0 && e.Offset == entries[i-1].Offset+uint64(entries[i-1].Length) {
			putUvarint(&buf, 0)
		} else {
			putUvarint(&buf, e.Offset+1)
		}
	}
	return buf.Bytes()
}

// maxDirEntries bounds directories decoded from untrusted archives.
const maxDirEntries = 50_000_000

// DeserializeEntries decodes a directory (uncompressed).
func DeserializeEntries(b []byte) ([]Entry, error) {
	r := bytes.NewReader(b)
	n, err := binary.ReadUvarint(r)
	if err != nil {
		return nil, fmt.Errorf("directory: %w", err)
	}
	if n > maxDirEntries || n > uint64(len(b)) {
		return nil, errors.New("directory: implausible entry count")
	}
	entries := make([]Entry, n)
	var last uint64
	for i := range entries {
		v, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, fmt.Errorf("directory: %w", err)
		}
		last += v
		entries[i].TileID = last
	}
	for i := range entries {
		v, err := binary.ReadUvarint(r)
		if err != nil || v > 1<<32-1 {
			return nil, errors.New("directory: bad run length")
		}
		entries[i].RunLength = uint32(v)
	}
	for i := range entries {
		v, err := binary.ReadUvarint(r)
		if err != nil || v > 1<<32-1 {
			return nil, errors.New("directory: bad length")
		}
		entries[i].Length = uint32(v)
	}
	for i := range entries {
		v, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, errors.New("directory: bad offset")
		}
		if v == 0 && i > 0 {
			entries[i].Offset = entries[i-1].Offset + uint64(entries[i-1].Length)
		} else {
			entries[i].Offset = v - 1
		}
	}
	return entries, nil
}

// FindTile locates tileID in a sorted directory. It returns the entry and
// whether it was found (the entry may be a leaf pointer).
func FindTile(entries []Entry, tileID uint64) (Entry, bool) {
	i := sort.Search(len(entries), func(i int) bool { return entries[i].TileID > tileID }) - 1
	if i < 0 {
		return Entry{}, false
	}
	e := entries[i]
	if e.RunLength == 0 {
		return e, true // leaf directory
	}
	if tileID < e.TileID+uint64(e.RunLength) {
		return e, true
	}
	return Entry{}, false
}

// Decompress applies the archive's internal compression.
func Decompress(b []byte, compression uint8) ([]byte, error) {
	switch compression {
	case CompressionNone, CompressionUnknown:
		return b, nil
	case CompressionGzip:
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		defer zr.Close()
		return io.ReadAll(io.LimitReader(zr, 512<<20))
	}
	return nil, fmt.Errorf("unsupported compression %d", compression)
}

func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

// BuildDirectories splits entries into a root directory and leaf
// directories so the root fits in the first 16 KiB of the file.
func BuildDirectories(entries []Entry) (root []byte, leaves []byte) {
	rootBytes := gzipBytes(SerializeEntries(entries))
	if len(rootBytes) <= 16384-HeaderSize {
		return rootBytes, nil
	}
	for leafSize := 4096; ; leafSize *= 2 {
		var rootEntries []Entry
		var leafBuf bytes.Buffer
		for i := 0; i < len(entries); i += leafSize {
			end := i + leafSize
			if end > len(entries) {
				end = len(entries)
			}
			leaf := gzipBytes(SerializeEntries(entries[i:end]))
			rootEntries = append(rootEntries, Entry{TileID: entries[i].TileID, Offset: uint64(leafBuf.Len()), Length: uint32(len(leaf))})
			leafBuf.Write(leaf)
		}
		rootBytes = gzipBytes(SerializeEntries(rootEntries))
		if len(rootBytes) <= 16384-HeaderSize {
			return rootBytes, leafBuf.Bytes()
		}
	}
}

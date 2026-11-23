package routing

// The road network file (".arcroute"): a header followed by fixed-size
// little-endian arrays, so it loads with a few large reads. The whole file
// is checked by its SHA-256 when installed; the header records a version
// and the array lengths, which Load validates.

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
)

const (
	fileMagic   = "ARCROUTE"
	fileVersion = 1
)

// Graph is a road network. Nodes are junctions, dead ends and barriers;
// edges are the stretches of road between them, with their shape.
type Graph struct {
	// Source describes where the data came from (extract URL and date).
	Source string
	BBox   [4]float64 // west, south, east, north

	// Per node, in 1e-7 degrees.
	Lat, Lon []int32
	// NodeBlock has a bit per mode for which a barrier closes the node.
	NodeBlock []uint8
	// First indexes Adj: node n's edges are Adj[First[n]:First[n+1]], each
	// edge<<1 | 1 when the edge leaves n backwards (n is its To).
	First []uint32
	Adj   []uint32

	// Per edge.
	From, To   []uint32
	Length     []float32 // metres
	Class      []uint8
	Access     []uint8
	Flags      []uint8
	Signals    []uint8
	Speed      []uint8 // numModes per edge, km/h
	Penalty    []uint8 // numModes per edge, factor ×10
	Name       []uint32
	Dest       []uint32
	ShapeFirst []uint32 // into ShapeLat/ShapeLon; len = edges+1
	Small      []uint8  // 1 when the edge is in a small isolated network

	ShapeLat, ShapeLon []int32

	// Strings is the name table; index 0 is "".
	Strings []string

	// Turn restrictions, sorted by RFrom.
	RFrom, RVia, RTo []uint32
	RKind            []uint8 // bit 0: "only"; bits 1..: modes (1<<(1+mode))

	// Derived at load time.
	bearStart, bearEnd []uint8 // initial bearing at From, final bearing at To (×256/360)
	grid               *spatialGrid
}

// NumEdges returns the number of edges.
func (g *Graph) NumEdges() int { return len(g.From) }

// NumNodes returns the number of nodes.
func (g *Graph) NumNodes() int { return len(g.Lat) }

type header struct {
	Magic      [8]byte
	Version    uint32
	Nodes      uint32
	Edges      uint32
	AdjLen     uint32
	ShapeLen   uint32
	Strings    uint32
	StringData uint32
	Restr      uint32
	SourceLen  uint32
	BBox       [4]float64
}

// WriteTo writes the graph in the .arcroute format.
func (g *Graph) WriteTo(w io.Writer) (int64, error) {
	cw := &countWriter{w: bufio.NewWriterSize(w, 1<<20), crc: crc32.NewIEEE()}
	strData := 0
	for _, s := range g.Strings {
		strData += len(s)
	}
	h := header{
		Version: fileVersion, Nodes: uint32(len(g.Lat)), Edges: uint32(len(g.From)), AdjLen: uint32(len(g.Adj)),
		ShapeLen: uint32(len(g.ShapeLat)), Strings: uint32(len(g.Strings)), StringData: uint32(strData),
		Restr: uint32(len(g.RFrom)), SourceLen: uint32(len(g.Source)), BBox: g.BBox,
	}
	copy(h.Magic[:], fileMagic)
	parts := []any{h, []byte(g.Source),
		g.Lat, g.Lon, g.NodeBlock, g.First, g.Adj,
		g.From, g.To, g.Length, g.Class, g.Access, g.Flags, g.Signals, g.Speed, g.Penalty, g.Name, g.Dest, g.ShapeFirst, g.Small,
		g.ShapeLat, g.ShapeLon,
		g.RFrom, g.RVia, g.RTo, g.RKind}
	for _, p := range parts {
		if err := binary.Write(cw, binary.LittleEndian, p); err != nil {
			return cw.n, err
		}
	}
	lens := make([]uint32, len(g.Strings))
	for i, s := range g.Strings {
		lens[i] = uint32(len(s))
	}
	if err := binary.Write(cw, binary.LittleEndian, lens); err != nil {
		return cw.n, err
	}
	for _, s := range g.Strings {
		if _, err := io.WriteString(cw, s); err != nil {
			return cw.n, err
		}
	}
	// Trailer: CRC-32 of everything before it, so a truncated or damaged
	// file is refused instead of giving wrong directions.
	if err := binary.Write(cw.w, binary.LittleEndian, cw.crc.Sum32()); err != nil {
		return cw.n, err
	}
	cw.n += 4
	return cw.n, cw.w.(*bufio.Writer).Flush()
}

type countWriter struct {
	w   io.Writer
	n   int64
	crc interface {
		io.Writer
		Sum32() uint32
	}
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	c.crc.Write(p[:n])
	return n, err
}

// ErrBadFile means a road network file is damaged or from an incompatible
// version of Arcana.
var ErrBadFile = errors.New("the road network file is damaged or was made by an incompatible version of Arcana")

// Load reads a .arcroute file.
func Load(path string) (*Graph, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	crc := crc32.NewIEEE()
	r := bufio.NewReaderSize(io.TeeReader(io.LimitReader(f, st.Size()-4), crc), 1<<20)
	var h header
	if err := binary.Read(r, binary.LittleEndian, &h); err != nil {
		return nil, ErrBadFile
	}
	if string(h.Magic[:]) != fileMagic {
		return nil, ErrBadFile
	}
	if h.Version != fileVersion {
		return nil, fmt.Errorf("%w (format %d; this Arcana reads %d)", ErrBadFile, h.Version, fileVersion)
	}
	// Sizes must be consistent with the file before anything is allocated.
	need := uint64(binary.Size(h)) + uint64(h.SourceLen) +
		uint64(h.Nodes)*(4+4+1+4) + 4 + uint64(h.AdjLen)*4 +
		uint64(h.Edges)*(4+4+4+1+1+1+1+uint64(numModes)*2+4+4+4+1) + 4 +
		uint64(h.ShapeLen)*8 + uint64(h.Restr)*(4+4+4+1) +
		uint64(h.Strings)*4 + uint64(h.StringData) + 4
	if need != uint64(st.Size()) || h.AdjLen != 2*h.Edges {
		return nil, ErrBadFile
	}
	g := &Graph{BBox: h.BBox}
	src := make([]byte, h.SourceLen)
	n, e, s, rc := int(h.Nodes), int(h.Edges), int(h.ShapeLen), int(h.Restr)
	g.Lat, g.Lon, g.NodeBlock = make([]int32, n), make([]int32, n), make([]uint8, n)
	g.First, g.Adj = make([]uint32, n+1), make([]uint32, h.AdjLen)
	g.From, g.To, g.Length = make([]uint32, e), make([]uint32, e), make([]float32, e)
	g.Class, g.Access, g.Flags, g.Signals = make([]uint8, e), make([]uint8, e), make([]uint8, e), make([]uint8, e)
	g.Speed, g.Penalty = make([]uint8, e*int(numModes)), make([]uint8, e*int(numModes))
	g.Name, g.Dest, g.ShapeFirst, g.Small = make([]uint32, e), make([]uint32, e), make([]uint32, e+1), make([]uint8, e)
	g.ShapeLat, g.ShapeLon = make([]int32, s), make([]int32, s)
	g.RFrom, g.RVia, g.RTo, g.RKind = make([]uint32, rc), make([]uint32, rc), make([]uint32, rc), make([]uint8, rc)
	parts := []any{src, g.Lat, g.Lon, g.NodeBlock, g.First, g.Adj,
		g.From, g.To, g.Length, g.Class, g.Access, g.Flags, g.Signals, g.Speed, g.Penalty, g.Name, g.Dest, g.ShapeFirst, g.Small,
		g.ShapeLat, g.ShapeLon, g.RFrom, g.RVia, g.RTo, g.RKind}
	for _, p := range parts {
		if err := binary.Read(r, binary.LittleEndian, p); err != nil {
			return nil, ErrBadFile
		}
	}
	g.Source = string(src)
	lens := make([]uint32, h.Strings)
	if err := binary.Read(r, binary.LittleEndian, lens); err != nil {
		return nil, ErrBadFile
	}
	data := make([]byte, h.StringData)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, ErrBadFile
	}
	g.Strings = make([]string, len(lens))
	off := uint64(0)
	for i, l := range lens {
		if off+uint64(l) > uint64(len(data)) {
			return nil, ErrBadFile
		}
		g.Strings[i] = string(data[off : off+uint64(l)])
		off += uint64(l)
	}
	var sum uint32
	if err := binary.Read(f, binary.LittleEndian, &sum); err != nil || sum != crc.Sum32() {
		return nil, ErrBadFile
	}
	if err := g.validate(); err != nil {
		return nil, err
	}
	g.prepare()
	return g, nil
}

// validate checks every index so a damaged file cannot cause a panic.
func (g *Graph) validate() error {
	n, e := uint32(len(g.Lat)), uint32(len(g.From))
	if len(g.Strings) == 0 || g.First[0] != 0 || g.First[n] != uint32(len(g.Adj)) || g.ShapeFirst[0] != 0 || g.ShapeFirst[e] != uint32(len(g.ShapeLat)) {
		return ErrBadFile
	}
	for i := uint32(0); i < n; i++ {
		if g.First[i] > g.First[i+1] {
			return ErrBadFile
		}
	}
	for _, a := range g.Adj {
		if a>>1 >= e {
			return ErrBadFile
		}
	}
	ns := uint32(len(g.Strings))
	for i := uint32(0); i < e; i++ {
		if g.From[i] >= n || g.To[i] >= n || g.Name[i] >= ns || g.Dest[i] >= ns || g.ShapeFirst[i] > g.ShapeFirst[i+1] ||
			math.IsNaN(float64(g.Length[i])) || g.Length[i] < 0 {
			return ErrBadFile
		}
	}
	for i := range g.RFrom {
		if g.RFrom[i] >= e || g.RTo[i] >= e || g.RVia[i] >= n || (i > 0 && g.RFrom[i] < g.RFrom[i-1]) {
			return ErrBadFile
		}
	}
	return nil
}

// prepare computes what is cheaper to derive than to store.
func (g *Graph) prepare() {
	e := len(g.From)
	g.bearStart, g.bearEnd = make([]uint8, e), make([]uint8, e)
	for i := 0; i < e; i++ {
		a, b := g.firstSeg(uint32(i))
		g.bearStart[i] = quantize(bearingE7(a, b))
		c, d := g.lastSeg(uint32(i))
		g.bearEnd[i] = quantize(bearingE7(c, d))
	}
	g.grid = buildGrid(g)
}

type pt struct{ lat, lon int32 }

func (g *Graph) nodePt(n uint32) pt { return pt{g.Lat[n], g.Lon[n]} }

// points returns an edge's full geometry from From to To.
func (g *Graph) points(e uint32, dst []pt) []pt {
	dst = append(dst, g.nodePt(g.From[e]))
	for i := g.ShapeFirst[e]; i < g.ShapeFirst[e+1]; i++ {
		dst = append(dst, pt{g.ShapeLat[i], g.ShapeLon[i]})
	}
	return append(dst, g.nodePt(g.To[e]))
}

func (g *Graph) firstSeg(e uint32) (pt, pt) {
	a := g.nodePt(g.From[e])
	if g.ShapeFirst[e] < g.ShapeFirst[e+1] {
		i := g.ShapeFirst[e]
		return a, pt{g.ShapeLat[i], g.ShapeLon[i]}
	}
	return a, g.nodePt(g.To[e])
}

func (g *Graph) lastSeg(e uint32) (pt, pt) {
	b := g.nodePt(g.To[e])
	if g.ShapeFirst[e] < g.ShapeFirst[e+1] {
		i := g.ShapeFirst[e+1] - 1
		return pt{g.ShapeLat[i], g.ShapeLon[i]}, b
	}
	return g.nodePt(g.From[e]), b
}

func quantize(deg float64) uint8 { return uint8(int(math.Round(deg*256/360)) & 255) }

func dequantize(q uint8) float64 { return float64(q) * 360 / 256 }

const e7 = 1e-7

func toRad(v int32) float64 { return float64(v) * e7 * math.Pi / 180 }

// haversineE7 is the great-circle distance in metres between two points.
func haversineE7(a, b pt) float64 {
	φ1, φ2 := toRad(a.lat), toRad(b.lat)
	dφ := φ2 - φ1
	dλ := toRad(b.lon) - toRad(a.lon)
	s := math.Sin(dφ/2)*math.Sin(dφ/2) + math.Cos(φ1)*math.Cos(φ2)*math.Sin(dλ/2)*math.Sin(dλ/2)
	return 2 * earthRadius * math.Asin(math.Min(1, math.Sqrt(s)))
}

const earthRadius = 6371008.8

// bearingE7 is the initial bearing in degrees from a to b.
func bearingE7(a, b pt) float64 {
	φ1, φ2 := toRad(a.lat), toRad(b.lat)
	dλ := toRad(b.lon) - toRad(a.lon)
	y := math.Sin(dλ) * math.Cos(φ2)
	x := math.Cos(φ1)*math.Sin(φ2) - math.Sin(φ1)*math.Cos(φ2)*math.Cos(dλ)
	return math.Mod(math.Atan2(y, x)*180/math.Pi+360, 360)
}

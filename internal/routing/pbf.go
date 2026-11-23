package routing

// A reader for OpenStreetMap PBF files (https://wiki.openstreetmap.org/wiki/PBF_Format),
// written for this package so Arcana keeps its few dependencies. It decodes
// only what routing needs: nodes (with tags on request), ways and
// relations. Blobs are decompressed and decoded on several goroutines;
// handlers are called from one goroutine, in no particular order.

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
)

// Tag is an OSM key/value pair.
type Tag struct{ Key, Val string }

// tagValue returns the value of key, or "".
func tagValue(tags []Tag, key string) string {
	for _, t := range tags {
		if t.Key == key {
			return t.Val
		}
	}
	return ""
}

// NodeBatch holds the nodes of one block. Lat and Lon are in 1e-7 degrees.
type NodeBatch struct {
	IDs      []int64
	Lat, Lon []int32
	// Tags maps an index in IDs to that node's tags; only filled when
	// node tags were asked for, and only for nodes that have tags.
	Tags map[int][]Tag
}

// Way is an OSM way.
type Way struct {
	ID   int64
	Tags []Tag
	Refs []int64
}

// Member is a relation member. Type is 0 node, 1 way, 2 relation.
type Member struct {
	Type uint8
	Ref  int64
	Role string
}

// Relation is an OSM relation.
type Relation struct {
	ID      int64
	Tags    []Tag
	Members []Member
}

// Handlers receive decoded elements. Nil handlers skip decoding that kind.
type Handlers struct {
	Nodes    func(*NodeBatch)
	NodeTags bool
	Way      func(*Way)
	Relation func(*Relation)
	Progress func(bytesRead int64)
	Workers  int
}

const (
	maxHeaderSize = 64 << 10
	maxBlobSize   = 32 << 20
)

var supportedFeatures = map[string]bool{"OsmSchema-V0.6": true, "DenseNodes": true}

type rawBlob struct {
	kind string
	data []byte
}

type decoded struct {
	nodes *NodeBatch
	ways  []Way
	rels  []Relation
	err   error
}

// ReadPBF reads an OSM PBF stream and calls the handlers for its elements.
func ReadPBF(ctx context.Context, r io.Reader, h Handlers) error {
	workers := h.Workers
	if workers <= 0 {
		workers = max(1, min(8, runtime.NumCPU()))
	}
	blobs := make(chan rawBlob, workers*2)
	out := make(chan decoded, workers*2)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var readErr error
	go func() {
		defer close(blobs)
		readErr = readBlobs(ctx, r, h.Progress, blobs)
	}()
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for b := range blobs {
				d := decodeBlob(b, &h)
				select {
				case out <- d:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() { wg.Wait(); close(out) }()

	var firstErr error
	for d := range out {
		if firstErr != nil {
			continue
		}
		if d.err != nil {
			firstErr = d.err
			cancel()
			continue
		}
		if d.nodes != nil && len(d.nodes.IDs) > 0 && h.Nodes != nil {
			h.Nodes(d.nodes)
		}
		for i := range d.ways {
			h.Way(&d.ways[i])
		}
		for i := range d.rels {
			h.Relation(&d.rels[i])
		}
	}
	if firstErr != nil {
		return firstErr
	}
	if readErr != nil {
		return readErr
	}
	return ctx.Err()
}

func readBlobs(ctx context.Context, r io.Reader, progress func(int64), out chan<- rawBlob) error {
	var lenBuf [4]byte
	var total int64
	sawHeader := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
			if err == io.EOF {
				if !sawHeader {
					return errors.New("not an OpenStreetMap PBF file (empty)")
				}
				return nil
			}
			return fmt.Errorf("reading OSM data: %w", err)
		}
		hl := binary.BigEndian.Uint32(lenBuf[:])
		if hl == 0 || hl > maxHeaderSize {
			return errors.New("not an OpenStreetMap PBF file (bad block header)")
		}
		hdr := make([]byte, hl)
		if _, err := io.ReadFull(r, hdr); err != nil {
			return fmt.Errorf("reading OSM data: %w", err)
		}
		kind, size, err := parseBlobHeader(hdr)
		if err != nil {
			return err
		}
		if size < 0 || size > maxBlobSize {
			return errors.New("OSM data block is too large")
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(r, data); err != nil {
			return fmt.Errorf("reading OSM data: %w", err)
		}
		total += int64(4 + int(hl) + int(size))
		if progress != nil {
			progress(total)
		}
		switch kind {
		case "OSMHeader":
			raw, err := blobData(data)
			if err != nil {
				return err
			}
			if err := checkHeader(raw); err != nil {
				return err
			}
			sawHeader = true
		case "OSMData":
			if !sawHeader {
				return errors.New("not an OpenStreetMap PBF file (no header block)")
			}
			select {
			case out <- rawBlob{kind, data}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// ---------------------------------------------------------------- protobuf

// pb decodes protocol buffer wire format. The first error is kept and every
// later read returns zero values, so callers check err once.
type pb struct {
	b   []byte
	err error
}

var errCorrupt = errors.New("OSM data is damaged (invalid encoding)")

func (d *pb) fail() {
	if d.err == nil {
		d.err = errCorrupt
	}
	d.b = nil
}

func (d *pb) uvarint() uint64 {
	v, n := binary.Uvarint(d.b)
	if n <= 0 {
		d.fail()
		return 0
	}
	d.b = d.b[n:]
	return v
}

func (d *pb) next() (field int, wire int, ok bool) {
	if d.err != nil || len(d.b) == 0 {
		return 0, 0, false
	}
	k := d.uvarint()
	return int(k >> 3), int(k & 7), d.err == nil
}

func (d *pb) bytes() []byte {
	n := d.uvarint()
	if d.err != nil || n > uint64(len(d.b)) {
		d.fail()
		return nil
	}
	v := d.b[:n]
	d.b = d.b[n:]
	return v
}

func (d *pb) skip(wire int) {
	switch wire {
	case 0:
		d.uvarint()
	case 1:
		if len(d.b) < 8 {
			d.fail()
			return
		}
		d.b = d.b[8:]
	case 2:
		d.bytes()
	case 5:
		if len(d.b) < 4 {
			d.fail()
			return
		}
		d.b = d.b[4:]
	default:
		d.fail()
	}
}

func zigzag(v uint64) int64 { return int64(v>>1) ^ -int64(v&1) }

// packed returns the varints of a packed field (wire type 2) or the single
// varint of an unpacked one.
func (d *pb) packed(wire int, fn func(uint64)) {
	if wire == 0 {
		fn(d.uvarint())
		return
	}
	if wire != 2 {
		d.skip(wire)
		return
	}
	sub := pb{b: d.bytes()}
	for len(sub.b) > 0 && sub.err == nil {
		fn(sub.uvarint())
	}
	if sub.err != nil {
		d.fail()
	}
}

// ---------------------------------------------------------------- blocks

func parseBlobHeader(b []byte) (kind string, size int32, err error) {
	d := pb{b: b}
	size = -1
	for {
		f, w, ok := d.next()
		if !ok {
			break
		}
		switch {
		case f == 1 && w == 2:
			kind = string(d.bytes())
		case f == 3 && w == 0:
			size = int32(d.uvarint())
		default:
			d.skip(w)
		}
	}
	if d.err != nil || kind == "" || size < 0 {
		return "", 0, errors.New("not an OpenStreetMap PBF file (bad block header)")
	}
	return kind, size, nil
}

func blobData(b []byte) ([]byte, error) {
	d := pb{b: b}
	var raw, z []byte
	rawSize := -1
	for {
		f, w, ok := d.next()
		if !ok {
			break
		}
		switch {
		case f == 1 && w == 2:
			raw = d.bytes()
		case f == 2 && w == 0:
			rawSize = int(d.uvarint())
		case f == 3 && w == 2:
			z = d.bytes()
		case f >= 4 && f <= 7:
			return nil, errors.New("OSM data uses a compression Arcana cannot read (only zlib is supported)")
		default:
			d.skip(w)
		}
	}
	if d.err != nil {
		return nil, d.err
	}
	if raw != nil {
		return raw, nil
	}
	if z == nil {
		return nil, errCorrupt
	}
	if rawSize < 0 || rawSize > maxBlobSize {
		return nil, errors.New("OSM data block is too large")
	}
	zr, err := zlib.NewReader(bytes.NewReader(z))
	if err != nil {
		return nil, errCorrupt
	}
	out := make([]byte, rawSize)
	if _, err := io.ReadFull(zr, out); err != nil {
		return nil, errCorrupt
	}
	return out, nil
}

func checkHeader(b []byte) error {
	d := pb{b: b}
	for {
		f, w, ok := d.next()
		if !ok {
			break
		}
		if f == 4 && w == 2 {
			feat := string(d.bytes())
			if !supportedFeatures[feat] {
				return fmt.Errorf("OSM data needs a feature Arcana does not support: %s", feat)
			}
			continue
		}
		d.skip(w)
	}
	return d.err
}

type blockCtx struct {
	strings           []string
	granularity       int64
	latOffset, lonOff int64
}

func (c *blockCtx) str(i uint64) string {
	if i < uint64(len(c.strings)) {
		return c.strings[i]
	}
	return ""
}

// coord converts a raw coordinate to 1e-7 degrees.
func (c *blockCtx) coord(v, offset int64) int32 {
	return int32((offset + c.granularity*v) / 100)
}

func decodeBlob(rb rawBlob, h *Handlers) decoded {
	raw, err := blobData(rb.data)
	if err != nil {
		return decoded{err: err}
	}
	d := pb{b: raw}
	c := blockCtx{granularity: 100}
	var groups [][]byte
	for {
		f, w, ok := d.next()
		if !ok {
			break
		}
		switch {
		case f == 1 && w == 2:
			st := pb{b: d.bytes()}
			for {
				f2, w2, ok := st.next()
				if !ok {
					break
				}
				if f2 == 1 && w2 == 2 {
					c.strings = append(c.strings, string(st.bytes()))
				} else {
					st.skip(w2)
				}
			}
			if st.err != nil {
				return decoded{err: st.err}
			}
		case f == 2 && w == 2:
			groups = append(groups, d.bytes())
		case f == 17 && w == 0:
			c.granularity = int64(d.uvarint())
		case f == 19 && w == 0:
			c.latOffset = int64(d.uvarint())
		case f == 20 && w == 0:
			c.lonOff = int64(d.uvarint())
		default:
			d.skip(w)
		}
	}
	if d.err != nil {
		return decoded{err: d.err}
	}
	var out decoded
	for _, g := range groups {
		if err := decodeGroup(g, &c, h, &out); err != nil {
			return decoded{err: err}
		}
	}
	return out
}

func decodeGroup(b []byte, c *blockCtx, h *Handlers, out *decoded) error {
	d := pb{b: b}
	for {
		f, w, ok := d.next()
		if !ok {
			break
		}
		switch {
		case f == 1 && w == 2 && h.Nodes != nil:
			decodeNode(d.bytes(), c, h.NodeTags, out)
		case f == 2 && w == 2 && h.Nodes != nil:
			if err := decodeDense(d.bytes(), c, h.NodeTags, out); err != nil {
				return err
			}
		case f == 3 && w == 2 && h.Way != nil:
			way, err := decodeWay(d.bytes(), c)
			if err != nil {
				return err
			}
			out.ways = append(out.ways, way)
		case f == 4 && w == 2 && h.Relation != nil:
			r, err := decodeRelation(d.bytes(), c)
			if err != nil {
				return err
			}
			out.rels = append(out.rels, r)
		default:
			d.skip(w)
		}
	}
	return d.err
}

func batch(out *decoded) *NodeBatch {
	if out.nodes == nil {
		out.nodes = &NodeBatch{}
	}
	return out.nodes
}

func decodeNode(b []byte, c *blockCtx, wantTags bool, out *decoded) {
	d := pb{b: b}
	var id, lat, lon int64
	var keys, vals []uint64
	for {
		f, w, ok := d.next()
		if !ok {
			break
		}
		switch f {
		case 1:
			id = zigzag(d.uvarint())
		case 2:
			d.packed(w, func(v uint64) { keys = append(keys, v) })
		case 3:
			d.packed(w, func(v uint64) { vals = append(vals, v) })
		case 8:
			lat = zigzag(d.uvarint())
		case 9:
			lon = zigzag(d.uvarint())
		default:
			d.skip(w)
		}
	}
	if d.err != nil {
		return
	}
	nb := batch(out)
	nb.IDs = append(nb.IDs, id)
	nb.Lat = append(nb.Lat, c.coord(lat, c.latOffset))
	nb.Lon = append(nb.Lon, c.coord(lon, c.lonOff))
	if wantTags && len(keys) > 0 && len(keys) == len(vals) {
		tags := make([]Tag, len(keys))
		for i := range keys {
			tags[i] = Tag{c.str(keys[i]), c.str(vals[i])}
		}
		if nb.Tags == nil {
			nb.Tags = map[int][]Tag{}
		}
		nb.Tags[len(nb.IDs)-1] = tags
	}
}

func decodeDense(b []byte, c *blockCtx, wantTags bool, out *decoded) error {
	d := pb{b: b}
	var ids, lats, lons []int64
	var kv []uint64
	for {
		f, w, ok := d.next()
		if !ok {
			break
		}
		switch f {
		case 1:
			var acc int64
			d.packed(w, func(v uint64) { acc += zigzag(v); ids = append(ids, acc) })
		case 8:
			var acc int64
			d.packed(w, func(v uint64) { acc += zigzag(v); lats = append(lats, acc) })
		case 9:
			var acc int64
			d.packed(w, func(v uint64) { acc += zigzag(v); lons = append(lons, acc) })
		case 10:
			if wantTags {
				d.packed(w, func(v uint64) { kv = append(kv, v) })
			} else {
				d.skip(w)
			}
		default:
			d.skip(w)
		}
	}
	if d.err != nil {
		return d.err
	}
	if len(lats) != len(ids) || len(lons) != len(ids) {
		return errCorrupt
	}
	nb := batch(out)
	base := len(nb.IDs)
	nb.IDs = append(nb.IDs, ids...)
	for i := range ids {
		nb.Lat = append(nb.Lat, c.coord(lats[i], c.latOffset))
		nb.Lon = append(nb.Lon, c.coord(lons[i], c.lonOff))
	}
	if wantTags && len(kv) > 0 {
		// keys_vals: for each node, key/value string indexes, then 0.
		n := 0
		var tags []Tag
		for i := 0; i < len(kv) && n < len(ids); i++ {
			if kv[i] == 0 {
				if len(tags) > 0 {
					if nb.Tags == nil {
						nb.Tags = map[int][]Tag{}
					}
					nb.Tags[base+n] = tags
					tags = nil
				}
				n++
				continue
			}
			if i+1 >= len(kv) {
				return errCorrupt
			}
			tags = append(tags, Tag{c.str(kv[i]), c.str(kv[i+1])})
			i++
		}
	}
	return nil
}

func decodeTags(keys, vals []uint64, c *blockCtx) ([]Tag, error) {
	if len(keys) != len(vals) {
		return nil, errCorrupt
	}
	if len(keys) == 0 {
		return nil, nil
	}
	tags := make([]Tag, len(keys))
	for i := range keys {
		tags[i] = Tag{c.str(keys[i]), c.str(vals[i])}
	}
	return tags, nil
}

func decodeWay(b []byte, c *blockCtx) (Way, error) {
	d := pb{b: b}
	var w Way
	var keys, vals []uint64
	for {
		f, wt, ok := d.next()
		if !ok {
			break
		}
		switch f {
		case 1:
			w.ID = int64(d.uvarint())
		case 2:
			d.packed(wt, func(v uint64) { keys = append(keys, v) })
		case 3:
			d.packed(wt, func(v uint64) { vals = append(vals, v) })
		case 8:
			var acc int64
			d.packed(wt, func(v uint64) { acc += zigzag(v); w.Refs = append(w.Refs, acc) })
		default:
			d.skip(wt)
		}
	}
	if d.err != nil {
		return w, d.err
	}
	var err error
	w.Tags, err = decodeTags(keys, vals, c)
	return w, err
}

func decodeRelation(b []byte, c *blockCtx) (Relation, error) {
	d := pb{b: b}
	var r Relation
	var keys, vals, roles, types []uint64
	var ids []int64
	for {
		f, wt, ok := d.next()
		if !ok {
			break
		}
		switch f {
		case 1:
			r.ID = int64(d.uvarint())
		case 2:
			d.packed(wt, func(v uint64) { keys = append(keys, v) })
		case 3:
			d.packed(wt, func(v uint64) { vals = append(vals, v) })
		case 8:
			d.packed(wt, func(v uint64) { roles = append(roles, v) })
		case 9:
			var acc int64
			d.packed(wt, func(v uint64) { acc += zigzag(v); ids = append(ids, acc) })
		case 10:
			d.packed(wt, func(v uint64) { types = append(types, v) })
		default:
			d.skip(wt)
		}
	}
	if d.err != nil {
		return r, d.err
	}
	if len(roles) != len(ids) || len(types) != len(ids) {
		return r, errCorrupt
	}
	var err error
	if r.Tags, err = decodeTags(keys, vals, c); err != nil {
		return r, err
	}
	r.Members = make([]Member, len(ids))
	for i := range ids {
		r.Members[i] = Member{Type: uint8(types[i]), Ref: ids[i], Role: c.str(roles[i])}
	}
	return r, nil
}

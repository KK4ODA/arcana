package maps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// RangeReader reads byte ranges of a remote archive.
type RangeReader interface {
	ReadRange(ctx context.Context, offset, length uint64) ([]byte, error)
}

// HTTPRangeReader reads ranges over HTTP and refuses to mix data from two
// different versions of the source: every response must carry the ETag seen
// first.
type HTTPRangeReader struct {
	URL       string
	Client    *http.Client
	UserAgent string

	mu   sync.Mutex
	etag string
}

// ErrSourceChanged means the remote archive was replaced during extraction.
var ErrSourceChanged = errors.New("the map source changed during extraction; it will be restarted")

// ETag returns the source version seen so far.
func (h *HTTPRangeReader) ETag() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.etag
}

// SetETag pins the expected version (when resuming).
func (h *HTTPRangeReader) SetETag(e string) {
	h.mu.Lock()
	h.etag = e
	h.mu.Unlock()
}

// ReadRange implements RangeReader.
func (h *HTTPRangeReader) ReadRange(ctx context.Context, offset, length uint64) ([]byte, error) {
	if length == 0 {
		return nil, nil
	}
	if length > 256<<20 {
		return nil, errors.New("range too large")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Range", "bytes="+strconv.FormatUint(offset, 10)+"-"+strconv.FormatUint(offset+length-1, 10))
	req.Header.Set("Accept-Encoding", "identity")
	if h.UserAgent != "" {
		req.Header.Set("User-Agent", h.UserAgent)
	}
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("map source: %s (range requests are required)", resp.Status)
	}
	if et := resp.Header.Get("ETag"); et != "" {
		h.mu.Lock()
		if h.etag == "" {
			h.etag = et
		}
		same := h.etag == et
		h.mu.Unlock()
		if !same {
			return nil, ErrSourceChanged
		}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, int64(length)+1))
	if err != nil {
		return nil, err
	}
	if uint64(len(b)) != length {
		return nil, fmt.Errorf("map source returned %d bytes, expected %d", len(b), length)
	}
	return b, nil
}

// BBox is [west, south, east, north] in degrees. West greater than east
// means the box crosses the antimeridian.
type BBox [4]float64

// Valid reports whether the box is usable.
func (b BBox) Valid() bool {
	return b[0] >= -180 && b[0] <= 180 && b[2] >= -180 && b[2] <= 180 && b[1] >= -85.06 && b[3] <= 85.06 && b[1] < b[3]
}

// lonToX and latToY convert to tile coordinates at zoom z (Web Mercator).
func lonToX(lon float64, z uint8) uint32 {
	n := float64(uint64(1) << z)
	x := math.Floor((lon + 180) / 360 * n)
	return uint32(math.Max(0, math.Min(n-1, x)))
}

func latToY(lat float64, z uint8) uint32 {
	n := float64(uint64(1) << z)
	lat = math.Max(-85.0511, math.Min(85.0511, lat))
	r := lat * math.Pi / 180
	y := math.Floor((1 - math.Log(math.Tan(r)+1/math.Cos(r))/math.Pi) / 2 * n)
	return uint32(math.Max(0, math.Min(n-1, y)))
}

// TileToLonLat returns the north-west corner of a tile.
func TileToLonLat(z uint8, x, y float64) (float64, float64) {
	n := float64(uint64(1) << z)
	lon := x/n*360 - 180
	lat := math.Atan(math.Sinh(math.Pi*(1-2*y/n))) * 180 / math.Pi
	return lon, lat
}

// Tile data reads are coalesced: nearby tiles separated by less than
// coalesceGap bytes are fetched in one request of at most coalesceMax bytes.
var (
	coalesceGap uint64 = 64 << 10
	coalesceMax uint64 = 32 << 20
)

// MaxPlanTiles bounds the number of tiles one extraction may address.
const MaxPlanTiles = 40_000_000

// ErrTooManyTiles is returned when a region is too large for the detail level.
var ErrTooManyTiles = errors.New("this area is too large at this detail level; choose a smaller area or a lower detail level")

// TileCount returns the number of tiles a box covers over a zoom range.
func TileCount(b BBox, minz, maxz uint8) uint64 {
	var n uint64
	for z := minz; z <= maxz; z++ {
		y0, y1 := latToY(b[3], z), latToY(b[1], z)
		for _, xr := range xRanges(b, z) {
			n += uint64(xr[1]-xr[0]+1) * uint64(y1-y0+1)
		}
	}
	return n
}

func xRanges(b BBox, z uint8) [][2]uint32 {
	if b[0] <= b[2] {
		return [][2]uint32{{lonToX(b[0], z), lonToX(b[2], z)}}
	}
	return [][2]uint32{{lonToX(b[0], z), lonToX(180, z)}, {lonToX(-180, z), lonToX(b[2], z)}}
}

func targetIDs(b BBox, minz, maxz uint8) ([]uint64, error) {
	if c := TileCount(b, minz, maxz); c > MaxPlanTiles {
		return nil, ErrTooManyTiles
	}
	var ids []uint64
	for z := minz; z <= maxz; z++ {
		y0, y1 := latToY(b[3], z), latToY(b[1], z)
		for _, xr := range xRanges(b, z) {
			for x := xr[0]; x <= xr[1]; x++ {
				for y := y0; y <= y1; y++ {
					ids = append(ids, ZXYToID(z, x, y))
				}
			}
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	// Remove duplicates (the two antimeridian ranges can overlap at z0).
	out := ids[:0]
	for i, id := range ids {
		if i == 0 || id != ids[i-1] {
			out = append(out, id)
		}
	}
	return out, nil
}

// Range is a contiguous span of the source's tile data section.
type Range struct {
	Offset, Length uint64
}

// Plan describes an extraction before any tile data is downloaded.
type Plan struct {
	ETag     string  `json:"etag"`
	Header   Header  `json:"-"`
	Metadata []byte  `json:"-"`
	Entries  []Entry `json:"-"` // source offsets, one per tile ID
	Ranges   []Range `json:"-"`
	BBox     BBox    `json:"bbox"`
	MinZoom  uint8   `json:"minZoom"`
	MaxZoom  uint8   `json:"maxZoom"`
	// Tiles is the number of tiles present in the area.
	Tiles int `json:"tiles"`
	// UniqueTiles is the number of distinct tile contents.
	UniqueTiles int `json:"uniqueTiles"`
	// TileBytes is the size of the tile data that will be stored.
	TileBytes uint64 `json:"tileBytes"`
	// DownloadBytes includes small gaps fetched to reduce request count.
	DownloadBytes uint64 `json:"downloadBytes"`
	// DirectoryBytes is what planning itself downloaded.
	DirectoryBytes uint64 `json:"directoryBytes"`
}

// MakePlan reads the source's header and the directories covering b, and
// lists the tile data to fetch. Only directories are downloaded.
func MakePlan(ctx context.Context, rr RangeReader, b BBox, minz, maxz uint8, progress func(string)) (*Plan, error) {
	if !b.Valid() || minz > maxz || maxz > 22 {
		return nil, errors.New("invalid map area or zoom range")
	}
	head, err := rr.ReadRange(ctx, 0, 16384)
	if err != nil {
		return nil, err
	}
	h, err := ParseHeader(head)
	if err != nil {
		return nil, err
	}
	if h.RootOffset+h.RootLength > 16384 {
		return nil, errors.New("map source root directory is not in the first 16 KiB")
	}
	if maxz > h.MaxZoom {
		maxz = h.MaxZoom
	}
	if minz < h.MinZoom {
		minz = h.MinZoom
	}
	p := &Plan{Header: h, BBox: b, MinZoom: minz, MaxZoom: maxz, DirectoryBytes: 16384}
	if hr, ok := rr.(*HTTPRangeReader); ok {
		p.ETag = hr.ETag()
	}
	targets, err := targetIDs(b, minz, maxz)
	if err != nil {
		return nil, err
	}
	rootRaw, err := Decompress(head[h.RootOffset:h.RootOffset+h.RootLength], h.InternalCompression)
	if err != nil {
		return nil, err
	}
	root, err := DeserializeEntries(rootRaw)
	if err != nil {
		return nil, err
	}
	if h.MetadataLength > 0 && h.MetadataLength < 16<<20 {
		if p.Metadata, err = rr.ReadRange(ctx, h.MetadataOffset, h.MetadataLength); err != nil {
			return nil, err
		}
	}
	hasTarget := func(lo, hi uint64) bool { // any target in [lo, hi)
		i := sort.Search(len(targets), func(i int) bool { return targets[i] >= lo })
		return i < len(targets) && targets[i] < hi
	}
	var out []Entry
	level := root
	for depth := 0; depth < 4 && len(level) > 0; depth++ {
		var leaves []Entry
		for i, e := range level {
			hi := uint64(math.MaxUint64)
			if i+1 < len(level) {
				hi = level[i+1].TileID
			}
			if e.RunLength == 0 {
				if hasTarget(e.TileID, hi) {
					leaves = append(leaves, e)
				}
				continue
			}
			end := e.TileID + uint64(e.RunLength)
			j := sort.Search(len(targets), func(k int) bool { return targets[k] >= e.TileID })
			for ; j < len(targets) && targets[j] < end; j++ {
				out = append(out, Entry{TileID: targets[j], Offset: e.Offset, Length: e.Length, RunLength: 1})
			}
		}
		if len(leaves) == 0 {
			break
		}
		if progress != nil {
			progress(fmt.Sprintf("Reading %d map directories", len(leaves)))
		}
		next, n, err := fetchLeaves(ctx, rr, h, leaves)
		if err != nil {
			return nil, err
		}
		p.DirectoryBytes += n
		level = next
	}
	sortEntries(out)
	p.Entries = out
	p.Tiles = len(out)
	// Distinct contents, in source order, coalesced into ranges.
	uniq := map[uint64]uint32{}
	for _, e := range out {
		uniq[e.Offset] = e.Length
	}
	offs := make([]uint64, 0, len(uniq))
	for o := range uniq {
		offs = append(offs, o)
	}
	sort.Slice(offs, func(i, j int) bool { return offs[i] < offs[j] })
	p.UniqueTiles = len(offs)
	maxGap, maxRange := coalesceGap, coalesceMax
	for _, o := range offs {
		l := uint64(uniq[o])
		p.TileBytes += l
		if n := len(p.Ranges); n > 0 {
			last := &p.Ranges[n-1]
			end := last.Offset + last.Length
			if o >= end && o-end <= maxGap && o+l-last.Offset <= maxRange {
				last.Length = o + l - last.Offset
				continue
			}
			if o < end { // overlapping/identical content
				if o+l > end {
					last.Length = o + l - last.Offset
				}
				continue
			}
		}
		p.Ranges = append(p.Ranges, Range{Offset: o, Length: l})
	}
	for _, r := range p.Ranges {
		p.DownloadBytes += r.Length
	}
	return p, nil
}

// fetchLeaves downloads leaf directories, coalescing nearby reads, and
// returns their concatenated entries in tile-ID order.
func fetchLeaves(ctx context.Context, rr RangeReader, h Header, leaves []Entry) ([]Entry, uint64, error) {
	sort.Slice(leaves, func(i, j int) bool { return leaves[i].Offset < leaves[j].Offset })
	type group struct {
		off, len uint64
		members  []Entry
	}
	var groups []group
	for _, l := range leaves {
		if n := len(groups); n > 0 {
			g := &groups[n-1]
			end := g.off + g.len
			if l.Offset >= end && l.Offset-end < 256<<10 && l.Offset+uint64(l.Length)-g.off < 16<<20 {
				g.len = l.Offset + uint64(l.Length) - g.off
				g.members = append(g.members, l)
				continue
			}
		}
		groups = append(groups, group{off: l.Offset, len: uint64(l.Length), members: []Entry{l}})
	}
	var out []Entry
	var total uint64
	var mu sync.Mutex
	errc := make(chan error, len(groups))
	sem := make(chan struct{}, 6)
	var wg sync.WaitGroup
	for _, g := range groups {
		if g.off+g.len > h.LeafLength {
			return nil, 0, errors.New("leaf directory out of range")
		}
		wg.Add(1)
		go func(g group) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			raw, err := rr.ReadRange(ctx, h.LeafOffset+g.off, g.len)
			if err != nil {
				errc <- err
				return
			}
			var local []Entry
			for _, m := range g.members {
				dec, err := Decompress(raw[m.Offset-g.off:m.Offset-g.off+uint64(m.Length)], h.InternalCompression)
				if err != nil {
					errc <- err
					return
				}
				entries, err := DeserializeEntries(dec)
				if err != nil {
					errc <- err
					return
				}
				local = append(local, entries...)
			}
			mu.Lock()
			out = append(out, local...)
			total += g.len
			mu.Unlock()
		}(g)
	}
	wg.Wait()
	close(errc)
	if err := <-errc; err != nil {
		return nil, 0, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TileID < out[j].TileID })
	return out, total, nil
}

type extractState struct {
	ETag       string `json:"etag"`
	Signature  string `json:"signature"`
	RangesDone int    `json:"rangesDone"`
	DataBytes  int64  `json:"dataBytes"`
}

func (p *Plan) signature() string {
	return fmt.Sprintf("%v|%d|%d|%d|%d", p.BBox, p.MinZoom, p.MaxZoom, len(p.Ranges), p.TileBytes)
}

// layout computes the output archive's header, directories and the new
// offset of every distinct tile, before any tile data is downloaded.
func (p *Plan) layout() (prefix []byte, offs []uint64, uniq map[uint64]uint32, total uint64) {
	uniq = map[uint64]uint32{}
	for _, e := range p.Entries {
		uniq[e.Offset] = e.Length
	}
	offs = make([]uint64, 0, len(uniq))
	for o := range uniq {
		offs = append(offs, o)
	}
	sort.Slice(offs, func(i, j int) bool { return offs[i] < offs[j] })
	newOffset := make(map[uint64]uint64, len(offs))
	for _, o := range offs {
		newOffset[o] = total
		total += uint64(uniq[o])
	}
	entries := make([]Entry, len(p.Entries))
	for i, e := range p.Entries {
		entries[i] = Entry{TileID: e.TileID, Offset: newOffset[e.Offset], Length: e.Length, RunLength: 1}
	}
	entries = runLength(entries)
	tmpl := Header{TileType: p.Header.TileType, TileCompression: p.Header.TileCompression, Clustered: true,
		MinZoom: p.MinZoom, MaxZoom: p.MaxZoom}
	w, s, e, n := p.BBox[0], p.BBox[1], p.BBox[2], p.BBox[3]
	if w > e { // antimeridian: record the whole longitude span
		w, e = -180, 180
	}
	tmpl.MinLonE7, tmpl.MinLatE7 = int32(w*1e7), int32(s*1e7)
	tmpl.MaxLonE7, tmpl.MaxLatE7 = int32(e*1e7), int32(n*1e7)
	tmpl.CenterLonE7, tmpl.CenterLatE7 = int32((w+e)/2*1e7), int32((s+n)/2*1e7)
	tmpl.CenterZoom = p.MinZoom + (p.MaxZoom-p.MinZoom)/3
	meta := p.Metadata
	if p.Header.InternalCompression != CompressionGzip {
		raw, err := Decompress(meta, p.Header.InternalCompression)
		if err != nil {
			raw = []byte("{}")
		}
		meta = gzipBytes(raw)
	}
	if len(meta) == 0 {
		meta = gzipBytes([]byte("{}"))
	}
	return assemblePrefix(tmpl, meta, entries, total), offs, uniq, total
}

// OutputSize is the exact size of the archive the plan will produce.
func (p *Plan) OutputSize() uint64 {
	prefix, _, _, total := p.layout()
	return uint64(len(prefix)) + total
}

// Extract downloads the planned tiles straight into the output archive (its
// header and directories are computed first), checkpointing progress next
// to it so an interrupted extraction resumes while the source is unchanged.
// It returns the output's SHA-256.
func Extract(ctx context.Context, rr RangeReader, p *Plan, out string, progress func(done, total int64)) (string, error) {
	statePath := out + ".state.json"
	prefix, offs, uniq, total := p.layout()
	var st extractState
	if b, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	f, err := os.OpenFile(out, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if st.ETag != p.ETag || st.Signature != p.signature() || !hasPrefix(f, prefix) {
		st = extractState{ETag: p.ETag, Signature: p.signature()}
		if err := f.Truncate(0); err != nil {
			return "", err
		}
		if _, err := f.WriteAt(prefix, 0); err != nil {
			return "", err
		}
	}
	base := int64(len(prefix))
	if err := f.Truncate(base + st.DataBytes); err != nil {
		return "", err
	}
	if _, err := f.Seek(base+st.DataBytes, io.SeekStart); err != nil {
		return "", err
	}
	saveState := func() error {
		if err := f.Sync(); err != nil {
			return err
		}
		b, _ := json.Marshal(st)
		return os.WriteFile(statePath, b, 0o644)
	}
	done := st.DataBytes
	// First distinct tile not yet written.
	var written uint64
	oi := 0
	for oi < len(offs) && written+uint64(uniq[offs[oi]]) <= uint64(st.DataBytes) {
		written += uint64(uniq[offs[oi]])
		oi++
	}
	for ri := st.RangesDone; ri < len(p.Ranges); ri++ {
		r := p.Ranges[ri]
		buf, err := rr.ReadRange(ctx, p.Header.DataOffset+r.Offset, r.Length)
		if err != nil {
			_ = saveState()
			return "", err
		}
		for oi < len(offs) && offs[oi] < r.Offset+r.Length {
			o := offs[oi]
			l := uint64(uniq[o])
			if o < r.Offset || o+l > r.Offset+r.Length {
				return "", errors.New("internal error: tile outside planned range")
			}
			if _, err := f.Write(buf[o-r.Offset : o-r.Offset+l]); err != nil {
				return "", err
			}
			done += int64(l)
			oi++
		}
		st.RangesDone = ri + 1
		st.DataBytes = done
		if progress != nil {
			progress(done, int64(total))
		}
		if ri%16 == 15 {
			if err := saveState(); err != nil {
				return "", err
			}
		}
	}
	if uint64(done) != total {
		return "", errors.New("tile data incomplete")
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	f.Close()
	os.Remove(statePath)
	return hashFile(out)
}

func hasPrefix(f *os.File, prefix []byte) bool {
	b := make([]byte, len(prefix))
	n, _ := f.ReadAt(b, 0)
	return n == len(prefix) && string(b) == string(prefix)
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ParseBBox parses "west,south,east,north".
func ParseBBox(s string) (BBox, error) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return BBox{}, errors.New("bbox needs four numbers")
	}
	var b BBox
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return BBox{}, errors.New("invalid bbox")
		}
		b[i] = v
	}
	if !b.Valid() {
		return BBox{}, errors.New("bbox out of range")
	}
	return b, nil
}

package maps

import (
	"container/list"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

// Archive reads tiles from a local PMTiles file. It is safe for concurrent
// use and caches decoded directories.
type Archive struct {
	f      *os.File
	Header Header
	root   []Entry

	mu    sync.Mutex
	cache map[uint64]*list.Element // leaf offset -> element
	lru   *list.List
}

type leafCacheEntry struct {
	offset  uint64
	entries []Entry
}

// maxLeafCache bounds memory used for decoded leaf directories per archive.
const maxLeafCache = 64

// Open opens a PMTiles archive.
func Open(path string) (*Archive, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	a, err := openFile(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return a, nil
}

func openFile(f *os.File) (*Archive, error) {
	b := make([]byte, HeaderSize)
	if _, err := f.ReadAt(b, 0); err != nil {
		return nil, err
	}
	h, err := ParseHeader(b)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := uint64(st.Size())
	for _, sec := range [][2]uint64{{h.RootOffset, h.RootLength}, {h.LeafOffset, h.LeafLength}, {h.DataOffset, h.DataLength}, {h.MetadataOffset, h.MetadataLength}} {
		if sec[0]+sec[1] > size || sec[0]+sec[1] < sec[0] {
			return nil, errors.New("archive is truncated or damaged")
		}
	}
	if h.RootLength > 16<<20 {
		return nil, errors.New("root directory too large")
	}
	raw := make([]byte, h.RootLength)
	if _, err := f.ReadAt(raw, int64(h.RootOffset)); err != nil {
		return nil, err
	}
	dec, err := Decompress(raw, h.InternalCompression)
	if err != nil {
		return nil, err
	}
	root, err := DeserializeEntries(dec)
	if err != nil {
		return nil, err
	}
	return &Archive{f: f, Header: h, root: root, cache: map[uint64]*list.Element{}, lru: list.New()}, nil
}

// Close closes the file.
func (a *Archive) Close() error { return a.f.Close() }

func (a *Archive) leaf(offset uint64, length uint32) ([]Entry, error) {
	a.mu.Lock()
	if el, ok := a.cache[offset]; ok {
		a.lru.MoveToFront(el)
		entries := el.Value.(*leafCacheEntry).entries
		a.mu.Unlock()
		return entries, nil
	}
	a.mu.Unlock()
	if offset+uint64(length) > a.Header.LeafLength || length > 64<<20 {
		return nil, errors.New("leaf directory out of range")
	}
	raw := make([]byte, length)
	if _, err := a.f.ReadAt(raw, int64(a.Header.LeafOffset+offset)); err != nil {
		return nil, err
	}
	dec, err := Decompress(raw, a.Header.InternalCompression)
	if err != nil {
		return nil, err
	}
	entries, err := DeserializeEntries(dec)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.cache[offset] = a.lru.PushFront(&leafCacheEntry{offset: offset, entries: entries})
	for a.lru.Len() > maxLeafCache {
		old := a.lru.Back()
		a.lru.Remove(old)
		delete(a.cache, old.Value.(*leafCacheEntry).offset)
	}
	a.mu.Unlock()
	return entries, nil
}

// ErrNoTile means the archive has no tile at those coordinates.
var ErrNoTile = errors.New("tile not found")

// Tile returns the raw (still compressed, as stored) tile bytes.
func (a *Archive) Tile(z uint8, x, y uint32) ([]byte, error) {
	if z < a.Header.MinZoom || z > a.Header.MaxZoom || x >= 1<<z || y >= 1<<z {
		return nil, ErrNoTile
	}
	id := ZXYToID(z, x, y)
	entries := a.root
	for depth := 0; depth < 4; depth++ {
		e, ok := FindTile(entries, id)
		if !ok {
			return nil, ErrNoTile
		}
		if e.RunLength > 0 {
			if e.Offset+uint64(e.Length) > a.Header.DataLength || e.Length > 32<<20 {
				return nil, errors.New("tile out of range")
			}
			b := make([]byte, e.Length)
			if _, err := a.f.ReadAt(b, int64(a.Header.DataOffset+e.Offset)); err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
			return b, nil
		}
		next, err := a.leaf(e.Offset, e.Length)
		if err != nil {
			return nil, err
		}
		entries = next
	}
	return nil, errors.New("directory nesting too deep")
}

// Metadata returns the decompressed JSON metadata.
func (a *Archive) Metadata() ([]byte, error) {
	if a.Header.MetadataLength > 16<<20 {
		return nil, errors.New("metadata too large")
	}
	raw := make([]byte, a.Header.MetadataLength)
	if _, err := a.f.ReadAt(raw, int64(a.Header.MetadataOffset)); err != nil {
		return nil, err
	}
	return Decompress(raw, a.Header.InternalCompression)
}

// AllEntries walks every tile entry in ID order (used to build the place
// index). fn returning false stops the walk.
func (a *Archive) AllEntries(fn func(Entry) bool) error {
	var walk func(entries []Entry, depth int) (bool, error)
	walk = func(entries []Entry, depth int) (bool, error) {
		if depth > 3 {
			return false, errors.New("directory nesting too deep")
		}
		for _, e := range entries {
			if e.RunLength > 0 {
				if !fn(e) {
					return false, nil
				}
				continue
			}
			leaf, err := a.leaf(e.Offset, e.Length)
			if err != nil {
				return false, err
			}
			if cont, err := walk(leaf, depth+1); !cont || err != nil {
				return cont, err
			}
		}
		return true, nil
	}
	_, err := walk(a.root, 0)
	return err
}

// ReadTileData reads raw bytes for an entry.
func (a *Archive) ReadTileData(e Entry) ([]byte, error) {
	if e.Offset+uint64(e.Length) > a.Header.DataLength || e.Length > 32<<20 {
		return nil, errors.New("tile out of range")
	}
	b := make([]byte, e.Length)
	_, err := a.f.ReadAt(b, int64(a.Header.DataOffset+e.Offset))
	return b, err
}

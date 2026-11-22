package maps

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// SetSpec identifies an installed map archive.
type SetSpec struct {
	ResourceID string
	Title      string
	Version    string
	Path       string
}

// SetInfo describes a loaded map set for the UI.
type SetInfo struct {
	ResourceID string     `json:"resourceId"`
	Title      string     `json:"title"`
	Version    string     `json:"version"`
	MinZoom    uint8      `json:"minZoom"`
	MaxZoom    uint8      `json:"maxZoom"`
	Bounds     [4]float64 `json:"bounds"`
	TileType   uint8      `json:"tileType"`
	Error      string     `json:"error,omitempty"`
}

type loadedSet struct {
	SetSpec
	a   *Archive
	err error
}

// Service serves tiles from every installed map set through one merged tile
// pyramid: for each tile, the most detailed set that covers it answers. This
// lets a world overview, a country and a detailed city coexist seamlessly.
type Service struct {
	mu      sync.RWMutex
	sets    []*loadedSet
	derived *tileCache
	gen     atomic.Uint64
}

// Generation changes whenever the set of loaded archives changes; it is
// used to validate cached tiles.
func (s *Service) Generation() uint64 { return s.gen.Load() }

// NewService returns an empty service.
func NewService() *Service { return &Service{derived: newTileCache(512)} }

// Load opens the given archives, closing ones no longer listed.
func (s *Service) Load(specs []SetSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keep := map[string]*loadedSet{}
	for _, ls := range s.sets {
		keep[ls.Path] = ls
	}
	var next []*loadedSet
	for _, spec := range specs {
		if ls, ok := keep[spec.Path]; ok && ls.err == nil {
			ls.SetSpec = spec
			next = append(next, ls)
			delete(keep, spec.Path)
			continue
		}
		a, err := Open(spec.Path)
		next = append(next, &loadedSet{SetSpec: spec, a: a, err: err})
	}
	for _, ls := range keep {
		if ls.a != nil {
			ls.a.Close()
		}
	}
	// Most detailed first; among equals, the smallest area first (a city
	// extract before its country).
	sort.SliceStable(next, func(i, j int) bool {
		ai, aj := next[i].a, next[j].a
		if ai == nil || aj == nil {
			return ai != nil
		}
		if ai.Header.MaxZoom != aj.Header.MaxZoom {
			return ai.Header.MaxZoom > aj.Header.MaxZoom
		}
		return area(ai.Header.Bounds()) < area(aj.Header.Bounds())
	})
	s.sets = next
	s.derived.clear()
	s.gen.Add(1)
}

func area(b [4]float64) float64 { return (b[2] - b[0]) * (b[3] - b[1]) }

// Close closes every archive.
func (s *Service) Close() {
	s.Load(nil)
}

// Sets lists loaded map sets.
func (s *Service) Sets() []SetInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SetInfo, 0, len(s.sets))
	for _, ls := range s.sets {
		info := SetInfo{ResourceID: ls.ResourceID, Title: ls.Title, Version: ls.Version}
		if ls.err != nil {
			info.Error = ls.err.Error()
		} else {
			h := ls.a.Header
			info.MinZoom, info.MaxZoom, info.Bounds, info.TileType = h.MinZoom, h.MaxZoom, h.Bounds(), h.TileType
		}
		out = append(out, info)
	}
	return out
}

// Tile returns a tile's stored bytes and their compression.
func (s *Service) Tile(z uint8, x, y uint32) ([]byte, uint8, error) {
	data, h, err := s.find(z, x, y)
	return data, h.TileCompression, err
}

// maxOverzoom bounds how far a tile may be derived from an ancestor.
const maxOverzoom = 10

// TileOrDerived returns the tile at (z, x, y), or, when no installed set has
// it, derives a vector tile from the nearest ancestor that exists (see
// Overzoom). Derived tiles are returned uncompressed.
func (s *Service) TileOrDerived(z uint8, x, y uint32) ([]byte, uint8, error) {
	data, h, err := s.find(z, x, y)
	if !errors.Is(err, ErrNoTile) {
		return data, h.TileCompression, err
	}
	key := fmt.Sprintf("%d/%d/%d", z, x, y)
	if d, ok := s.derived.get(key); ok {
		if d == nil {
			return nil, 0, ErrNoTile
		}
		return d, CompressionNone, nil
	}
	for dz := uint8(1); dz <= maxOverzoom && dz <= z; dz++ {
		pz := z - dz
		data, h, err := s.find(pz, x>>dz, y>>dz)
		if errors.Is(err, ErrNoTile) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		if h.TileType != TileTypeMVT {
			break
		}
		raw, err := Decompress(data, h.TileCompression)
		if err != nil {
			return nil, 0, err
		}
		out, err := Overzoom(raw, pz, z, x, y)
		if err != nil {
			return nil, 0, err
		}
		s.derived.put(key, out)
		return out, CompressionNone, nil
	}
	s.derived.put(key, nil)
	return nil, 0, ErrNoTile
}

func (s *Service) find(z uint8, x, y uint32) ([]byte, Header, error) {
	if z > 24 || x >= 1<<z || y >= 1<<z {
		return nil, Header{}, ErrNoTile
	}
	w, n := TileToLonLat(z, float64(x), float64(y))
	e, so := TileToLonLat(z, float64(x+1), float64(y+1))
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ls := range s.sets {
		if ls.a == nil {
			continue
		}
		h := ls.a.Header
		if z < h.MinZoom || z > h.MaxZoom {
			continue
		}
		b := h.Bounds()
		if e < b[0] || w > b[2] || n < b[1] || so > b[3] {
			continue
		}
		data, err := ls.a.Tile(z, x, y)
		if errors.Is(err, ErrNoTile) {
			continue
		}
		if err != nil {
			return nil, Header{}, err
		}
		return data, h, nil
	}
	return nil, Header{}, ErrNoTile
}

// Archive returns the open archive for a resource (for indexing).
func (s *Service) Archive(resourceID string) *Archive {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, ls := range s.sets {
		if ls.ResourceID == resourceID {
			return ls.a
		}
	}
	return nil
}

// MaxZoom is the highest zoom across loaded sets.
func (s *Service) MaxZoom() uint8 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var m uint8
	for _, ls := range s.sets {
		if ls.a != nil && ls.a.Header.MaxZoom > m {
			m = ls.a.Header.MaxZoom
		}
	}
	return m
}

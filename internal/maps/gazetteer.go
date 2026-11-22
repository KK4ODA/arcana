package maps

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// Place is a named location found in installed map tiles.
type Place struct {
	Name    string  `json:"name"`
	Kind    string  `json:"kind"`
	Lon     float64 `json:"lon"`
	Lat     float64 `json:"lat"`
	MapSet  string  `json:"mapSet"`
	Rank    int     `json:"rank"`
	Country string  `json:"country,omitempty"`
}

// Gazetteer is an offline place-name index (SQLite FTS5) built from the
// "places" layer of installed Protomaps tiles. It is a rebuildable cache
// stored in index/places.db. Because it is derived from OpenStreetMap data,
// it is a Derivative Database under the ODbL.
type Gazetteer struct {
	db *sql.DB
	mu sync.Mutex
}

// OpenGazetteer opens or creates the index ("" for memory).
func OpenGazetteer(path string) (*Gazetteer, error) {
	dsn := "file::memory:"
	if path != "" {
		dsn = "file:" + filepath.ToSlash(path) + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS sets (id TEXT PRIMARY KEY, indexed TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS places (rowid INTEGER PRIMARY KEY, name TEXT, kind TEXT, lon REAL, lat REAL, mapset TEXT, rank INTEGER);
CREATE INDEX IF NOT EXISTS places_set ON places(mapset);
CREATE VIRTUAL TABLE IF NOT EXISTS places_fts USING fts5(name, content='places', content_rowid='rowid',
	tokenize = 'unicode61 remove_diacritics 2');`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("place index: %w", err)
	}
	return &Gazetteer{db: db}, nil
}

// Close closes the index.
func (g *Gazetteer) Close() error { return g.db.Close() }

// Indexed reports which map sets are indexed and at which version.
func (g *Gazetteer) Indexed(ctx context.Context) map[string]string {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := map[string]string{}
	rows, err := g.db.QueryContext(ctx, "SELECT id, indexed FROM sets")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id, v string
		if rows.Scan(&id, &v) == nil {
			out[id] = v
		}
	}
	return out
}

// Remove drops a map set's places.
func (g *Gazetteer) Remove(ctx context.Context, set string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.removeLocked(ctx, set)
}

func (g *Gazetteer) removeLocked(ctx context.Context, set string) error {
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO places_fts(places_fts, rowid, name) SELECT 'delete', rowid, name FROM places WHERE mapset = ?`, set); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM places WHERE mapset = ?", set); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sets WHERE id = ?", set); err != nil {
		return err
	}
	return tx.Commit()
}

// placeZoom is the zoom level whose "places" layer is indexed: at z12 the
// Protomaps basemap includes towns, villages and neighbourhoods.
const placeZoom = 12

// IndexArchive reads the places layer of every tile at placeZoom (or the
// archive's maximum zoom, if lower) and replaces the set's entries.
func (g *Gazetteer) IndexArchive(ctx context.Context, set, version string, a *Archive, progress func(done, total int)) (int, error) {
	z := uint8(placeZoom)
	if a.Header.MaxZoom < z {
		z = a.Header.MaxZoom
	}
	if a.Header.TileType != TileTypeMVT {
		return 0, nil
	}
	lo := ZXYToID(z, 0, 0)
	hi := ZXYToID(z+1, 0, 0)
	var entries []Entry
	if err := a.AllEntries(func(e Entry) bool {
		if e.TileID+uint64(e.RunLength) <= lo {
			return true
		}
		if e.TileID >= hi {
			return false
		}
		entries = append(entries, e)
		return true
	}); err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	var places []Place
	for i, e := range entries {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if progress != nil && i%200 == 0 {
			progress(i, len(entries))
		}
		raw, err := a.ReadTileData(e)
		if err != nil {
			continue
		}
		data, err := Decompress(raw, a.Header.TileCompression)
		if err != nil {
			continue
		}
		feats, err := DecodeLayer(data, "places")
		if err != nil {
			continue
		}
		// A run covers several tiles with identical content (rare for
		// places); use the first tile's position.
		tz, tx, ty := IDToZXY(e.TileID)
		for _, f := range feats {
			name, _ := f.Props["name"].(string)
			if name == "" || f.Type != 1 {
				continue
			}
			kind, _ := f.Props["kind"].(string)
			if kind == "" {
				kind, _ = f.Props["pmap:kind"].(string)
			}
			lon, lat := TileToLonLat(tz, float64(tx)+float64(f.X)/float64(f.Extent), float64(ty)+float64(f.Y)/float64(f.Extent))
			key := fmt.Sprintf("%s|%.2f|%.2f", name, lon, lat)
			if seen[key] {
				continue
			}
			seen[key] = true
			rank := 0
			if r, ok := f.Props["population_rank"].(float64); ok {
				rank = int(r)
			} else if r, ok := f.Props["pmap:population_rank"].(float64); ok {
				rank = int(r)
			}
			places = append(places, Place{Name: name, Kind: kind, Lon: lon, Lat: lat, MapSet: set, Rank: rank})
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if err := g.removeLocked(ctx, set); err != nil {
		return 0, err
	}
	tx, err := g.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	ins, err := tx.PrepareContext(ctx, "INSERT INTO places (name, kind, lon, lat, mapset, rank) VALUES (?,?,?,?,?,?)")
	if err != nil {
		return 0, err
	}
	defer ins.Close()
	fts, err := tx.PrepareContext(ctx, "INSERT INTO places_fts (rowid, name) VALUES (?,?)")
	if err != nil {
		return 0, err
	}
	defer fts.Close()
	for _, p := range places {
		res, err := ins.ExecContext(ctx, p.Name, p.Kind, p.Lon, p.Lat, p.MapSet, p.Rank)
		if err != nil {
			return 0, err
		}
		id, _ := res.LastInsertId()
		if _, err := fts.ExecContext(ctx, id, p.Name); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT OR REPLACE INTO sets (id, indexed) VALUES (?,?)", set, version); err != nil {
		return 0, err
	}
	return len(places), tx.Commit()
}

// Search finds places by name, larger places first.
func (g *Gazetteer) Search(ctx context.Context, q string, limit int) ([]Place, error) {
	var terms []string
	for _, f := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127)
	}) {
		if len(terms) < 6 {
			terms = append(terms, `"`+f+`"*`)
		}
	}
	if len(terms) == 0 {
		return nil, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	rows, err := g.db.QueryContext(ctx, `SELECT p.name, p.kind, p.lon, p.lat, p.mapset, p.rank FROM places_fts f
		JOIN places p ON p.rowid = f.rowid WHERE places_fts MATCH ? ORDER BY p.rank DESC, length(p.name) LIMIT ?`,
		strings.Join(terms, " "), limit*4)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Place
	// Overlapping map sets (a world overview and a country) index the
	// same places; keep one of each.
	seen := map[string]bool{}
	for rows.Next() {
		var p Place
		if err := rows.Scan(&p.Name, &p.Kind, &p.Lon, &p.Lat, &p.MapSet, &p.Rank); err != nil {
			return nil, err
		}
		key := fmt.Sprintf("%s|%.2f|%.2f", strings.ToLower(p.Name), p.Lon, p.Lat)
		if seen[key] || len(out) >= limit {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	return out, rows.Err()
}

package docs

import (
	"context"
	"database/sql"
	"fmt"
	"html"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

// Doc identifies a document to index.
type Doc struct {
	// Key is unique: "res:<resource-id>/<file>" or "user:<file>".
	Key        string
	Source     string // "resource" or "user"
	ResourceID string
	Name       string
	// Path is the absolute file path (not stored in the index).
	Path string
	// URL is the path on the content origin that opens the document.
	URL     string
	Size    int64
	ModTime int64
}

// Hit is a search result.
type Hit struct {
	Key        string `json:"key"`
	Source     string `json:"source"`
	ResourceID string `json:"resourceId,omitempty"`
	Name       string `json:"name"`
	Title      string `json:"title"`
	// Snippet is HTML-escaped text with <b> around matches.
	Snippet string `json:"snippet"`
	URL     string `json:"url"`
}

// Index is a rebuildable full-text index stored in index/documents.db.
type Index struct {
	db *sql.DB
	mu sync.Mutex
}

// OpenIndex opens or creates the index database at path ("" for memory).
func OpenIndex(path string) (*Index, error) {
	dsn := "file::memory:"
	if path != "" {
		dsn = "file:" + filepath.ToSlash(path) + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	ix := &Index{db: db}
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS docs (key TEXT PRIMARY KEY, source TEXT NOT NULL, resource_id TEXT NOT NULL,
	name TEXT NOT NULL, url TEXT NOT NULL, title TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL,
	error TEXT NOT NULL DEFAULT '');
CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(key UNINDEXED, title, body,
	tokenize = 'unicode61 remove_diacritics 2');`)
	if err != nil {
		db.Close()
		// The index is a cache: if it is damaged, the caller deletes it
		// and opens a new one.
		return nil, fmt.Errorf("document index: %w", err)
	}
	return ix, nil
}

// Close closes the database.
func (ix *Index) Close() error { return ix.db.Close() }

// SyncResult summarizes a Sync.
type SyncResult struct {
	Indexed int      `json:"indexed"`
	Removed int      `json:"removed"`
	Failed  []string `json:"failed,omitempty"`
}

// Sync makes the index match docs: new or changed files (by size and
// modification time) are extracted and indexed, missing ones removed.
func (ix *Index) Sync(ctx context.Context, docs []Doc, progress func(done, total int)) (SyncResult, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	var res SyncResult
	existing := map[string][2]int64{}
	rows, err := ix.db.QueryContext(ctx, "SELECT key, size, mtime FROM docs")
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var k string
		var s, m int64
		if err := rows.Scan(&k, &s, &m); err == nil {
			existing[k] = [2]int64{s, m}
		}
	}
	rows.Close()
	want := map[string]bool{}
	for i, d := range docs {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if progress != nil {
			progress(i, len(docs))
		}
		want[d.Key] = true
		if e, ok := existing[d.Key]; ok && e[0] == d.Size && e[1] == d.ModTime {
			continue
		}
		ex, xerr := Extract(ctx, d.Path)
		errText := ""
		if xerr != nil {
			errText = xerr.Error()
			res.Failed = append(res.Failed, d.Name+": "+errText)
			ex = Extracted{Title: strings.TrimSuffix(d.Name, filepath.Ext(d.Name))}
		}
		if err := ix.upsert(ctx, d, ex, errText); err != nil {
			return res, err
		}
		res.Indexed++
	}
	for k := range existing {
		if !want[k] {
			if _, err := ix.db.ExecContext(ctx, "DELETE FROM docs WHERE key = ?", k); err != nil {
				return res, err
			}
			if _, err := ix.db.ExecContext(ctx, "DELETE FROM docs_fts WHERE key = ?", k); err != nil {
				return res, err
			}
			res.Removed++
		}
	}
	return res, nil
}

func (ix *Index) upsert(ctx context.Context, d Doc, ex Extracted, errText string) error {
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM docs_fts WHERE key = ?", d.Key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO docs (key, source, resource_id, name, url, title, size, mtime, error)
		VALUES (?,?,?,?,?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET source=excluded.source, resource_id=excluded.resource_id,
		name=excluded.name, url=excluded.url, title=excluded.title, size=excluded.size, mtime=excluded.mtime, error=excluded.error`,
		d.Key, d.Source, d.ResourceID, d.Name, d.URL, ex.Title, d.Size, d.ModTime, errText); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO docs_fts (key, title, body) VALUES (?,?,?)", d.Key, ex.Title, ex.Text); err != nil {
		return err
	}
	return tx.Commit()
}

// Search runs a full-text query.
func (ix *Index) Search(ctx context.Context, query string, limit int) ([]Hit, int, error) {
	return ix.SearchIn(ctx, query, "", 0, limit)
}

// UserDocuments, passed to SearchIn as a resource ID, limits a search to the
// person's own documents.
const UserDocuments = "@user"

// SharedDocuments, passed to SearchIn as a resource ID, limits a search to
// installed resources' documents, leaving out the person's own: what may
// be shown to devices on the network.
const SharedDocuments = "@shared"

// SearchIn runs a full-text query limited to one installed resource's
// documents (UserDocuments for the person's own, SharedDocuments for every
// resource's, or every document when resourceID is empty), returning
// results from offset on.
func (ix *Index) SearchIn(ctx context.Context, query, resourceID string, offset, limit int) ([]Hit, int, error) {
	q := ftsQuery(query)
	if q == "" {
		return nil, 0, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	var total int
	if err := ix.db.QueryRowContext(ctx, `SELECT count(*) FROM docs_fts f JOIN docs d ON d.key = f.key
		WHERE docs_fts MATCH ? AND (? = '' OR (? = '@user' AND d.source = 'user') OR (? = '@shared' AND d.source = 'resource') OR (d.source = 'resource' AND d.resource_id = ?))`,
		q, resourceID, resourceID, resourceID, resourceID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := ix.db.QueryContext(ctx, `SELECT d.key, d.source, d.resource_id, d.name, d.url, d.title,
		snippet(docs_fts, 2, char(1), char(2), '…', 14)
		FROM docs_fts f JOIN docs d ON d.key = f.key
		WHERE docs_fts MATCH ? AND (? = '' OR (? = '@user' AND d.source = 'user') OR (? = '@shared' AND d.source = 'resource') OR (d.source = 'resource' AND d.resource_id = ?))
		ORDER BY bm25(docs_fts, 0, 8.0, 1.0) LIMIT ? OFFSET ?`, q, resourceID, resourceID, resourceID, resourceID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		var snip string
		if err := rows.Scan(&h.Key, &h.Source, &h.ResourceID, &h.Name, &h.URL, &h.Title, &snip); err != nil {
			return nil, 0, err
		}
		h.Snippet = highlight(snip)
		out = append(out, h)
	}
	return out, total, rows.Err()
}

// SearchAmong runs a full-text query limited to the documents of the given
// installed resources and, with own, the person's own documents, best
// first. With neither it finds nothing.
func (ix *Index) SearchAmong(ctx context.Context, query string, resourceIDs []string, own bool, limit int) ([]Hit, error) {
	q := ftsQuery(query)
	if q == "" || (len(resourceIDs) == 0 && !own) {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	var where []string
	args := []any{q}
	if len(resourceIDs) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(resourceIDs)), ",")
		where = append(where, "(d.source = 'resource' AND d.resource_id IN ("+marks+"))")
		for _, id := range resourceIDs {
			args = append(args, id)
		}
	}
	if own {
		where = append(where, "d.source = 'user'")
	}
	args = append(args, limit)
	ix.mu.Lock()
	defer ix.mu.Unlock()
	rows, err := ix.db.QueryContext(ctx, `SELECT d.key, d.source, d.resource_id, d.name, d.url, d.title,
		snippet(docs_fts, 2, char(1), char(2), '…', 14)
		FROM docs_fts f JOIN docs d ON d.key = f.key
		WHERE docs_fts MATCH ? AND (`+strings.Join(where, " OR ")+`)
		ORDER BY bm25(docs_fts, 0, 8.0, 1.0) LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Hit
	for rows.Next() {
		var h Hit
		var snip string
		if err := rows.Scan(&h.Key, &h.Source, &h.ResourceID, &h.Name, &h.URL, &h.Title, &snip); err != nil {
			return nil, err
		}
		h.Snippet = highlight(snip)
		out = append(out, h)
	}
	return out, rows.Err()
}

// Holders lists the installed resources that have indexed documents, and
// reports whether the person has documents of their own.
func (ix *Index) Holders(ctx context.Context) (resourceIDs []string, own bool, err error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	rows, err := ix.db.QueryContext(ctx, `SELECT DISTINCT source, resource_id FROM docs`)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var source, id string
		if err := rows.Scan(&source, &id); err != nil {
			return nil, false, err
		}
		switch source {
		case "user":
			own = true
		case "resource":
			resourceIDs = append(resourceIDs, id)
		}
	}
	return resourceIDs, own, rows.Err()
}

// Text returns a document's extracted title and text (for previews).
func (ix *Index) Text(ctx context.Context, key string, max int) (title, text, url string, err error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	err = ix.db.QueryRowContext(ctx, `SELECT f.title, substr(f.body, 1, ?), d.url FROM docs_fts f JOIN docs d ON d.key = f.key WHERE f.key = ?`,
		max, key).Scan(&title, &text, &url)
	return
}

// Count returns the number of indexed documents.
func (ix *Index) Count(ctx context.Context) int {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	var n int
	_ = ix.db.QueryRowContext(ctx, "SELECT count(*) FROM docs").Scan(&n)
	return n
}

func highlight(s string) string {
	s = html.EscapeString(strings.Join(strings.Fields(s), " "))
	return strings.NewReplacer("\x01", "<b>", "\x02", "</b>").Replace(s)
}

// ftsQuery reduces user input to quoted prefix terms.
func ftsQuery(text string) string {
	var terms []string
	for _, f := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127)
	}) {
		if len(terms) == 8 {
			break
		}
		terms = append(terms, `"`+f+`"*`)
	}
	return strings.Join(terms, " ")
}

// FileURL builds the content-origin URL for a resource file.
func FileURL(resourceID, name string) string {
	return "/files/" + url.PathEscape(resourceID) + "/" + url.PathEscape(name)
}

// UserDocURL builds the content-origin URL for a user document.
func UserDocURL(name string) string {
	return "/userdocs/" + url.PathEscape(name)
}

package docs

import (
	"context"
	"database/sql"
	"errors"
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
	// Key is unique: "res:<resource-id>/<file>" or "user:<path>".
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

// Hit is a search result: a document, with the pages (or sections) where
// the words were found.
type Hit struct {
	Key        string `json:"key"`
	Source     string `json:"source"`
	ResourceID string `json:"resourceId,omitempty"`
	Name       string `json:"name"`
	Title      string `json:"title"`
	// Snippet is HTML-escaped text with <b> around matches.
	Snippet string `json:"snippet"`
	URL     string `json:"url"`
	// Paged is true when sections are the pages of a PDF.
	Paged bool `json:"paged,omitempty"`
	// Pages are the best matching pages or sections, at most pagesPerHit.
	Pages []PageHit `json:"pages,omitempty"`
	// PageMatches counts the pages or sections that match.
	PageMatches int `json:"pageMatches,omitempty"`
}

// PageHit is a page (of a PDF) or section (of another document) that
// matches a search. N counts from 1.
type PageHit struct {
	N       int    `json:"n"`
	Heading string `json:"heading,omitempty"`
	Snippet string `json:"snippet"`
}

// pagesPerHit is how many matching pages each search result shows.
const pagesPerHit = 3

// Index is a rebuildable full-text index stored in index/documents.db.
//
// Two FTS5 tables work together:
//   - docs_fts has one row per document (title and all text). It decides
//     which documents match and in what order. It is contentless, so the
//     text is not stored twice.
//   - pages_fts has one row per page or section (heading and text). It says
//     where in a document the words are, and holds the text for previews.
//
// A document's pages are consecutive rows in pages_fts, from first_row to
// last_row, so finding its matching pages is a rowid range lookup.
type Index struct {
	db *sql.DB
	mu sync.Mutex
}

// indexLayout is the database layout, stored as PRAGMA user_version. An
// index with an older layout is emptied and rebuilt by the next sync.
//
// 3: pages_fts and a contentless docs_fts.
const indexLayout = 3

const schema = `
CREATE TABLE IF NOT EXISTS docs (id INTEGER PRIMARY KEY, key TEXT NOT NULL UNIQUE,
	source TEXT NOT NULL, resource_id TEXT NOT NULL, name TEXT NOT NULL, url TEXT NOT NULL,
	title TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL,
	error TEXT NOT NULL DEFAULT '', ver INTEGER NOT NULL DEFAULT 0, paged INTEGER NOT NULL DEFAULT 0,
	first_row INTEGER NOT NULL DEFAULT 0, last_row INTEGER NOT NULL DEFAULT -1);
CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(title, body, content='', contentless_delete=1,
	tokenize = 'unicode61 remove_diacritics 2');
CREATE VIRTUAL TABLE IF NOT EXISTS pages_fts USING fts5(n UNINDEXED, heading, body,
	tokenize = 'unicode61 remove_diacritics 2');`

// The docs columns: error holds why a document cannot be searched (fully),
// an extraction error or an Extracted.Problem such as a scanned PDF; ver is
// the ExtractorVersion that produced the row.

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
	if err := migrate(db); err != nil {
		db.Close()
		// The index is a cache: if it is damaged, the caller deletes it
		// and opens a new one.
		return nil, fmt.Errorf("document index: %w", err)
	}
	return &Index{db: db}, nil
}

func migrate(db *sql.DB) error {
	var layout int
	if err := db.QueryRow("PRAGMA user_version").Scan(&layout); err != nil {
		return err
	}
	if layout < indexLayout {
		// Earlier layouts are rebuilt from the documents themselves.
		if _, err := db.Exec(`DROP TABLE IF EXISTS docs; DROP TABLE IF EXISTS docs_fts; DROP TABLE IF EXISTS pages_fts;`); err != nil {
			return err
		}
	}
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	_, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", indexLayout))
	return err
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
// modification time) and files indexed by an older ExtractorVersion are
// extracted and indexed, missing ones removed.
func (ix *Index) Sync(ctx context.Context, docs []Doc, progress func(done, total int)) (SyncResult, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	var res SyncResult
	existing := map[string][3]int64{}
	rows, err := ix.db.QueryContext(ctx, "SELECT key, size, mtime, ver FROM docs")
	if err != nil {
		return res, err
	}
	for rows.Next() {
		var k string
		var s, m, v int64
		if err := rows.Scan(&k, &s, &m, &v); err == nil {
			existing[k] = [3]int64{s, m, v}
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
		if e, ok := existing[d.Key]; ok && e[0] == d.Size && e[1] == d.ModTime && e[2] >= ExtractorVersion {
			continue
		}
		ex, xerr := Extract(ctx, d.Path)
		errText := ex.Problem
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
			if err := ix.remove(ctx, k); err != nil {
				return res, err
			}
			res.Removed++
		}
	}
	return res, nil
}

// remove deletes a document and its pages.
func (ix *Index) remove(ctx context.Context, key string) error {
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deleteRows(ctx, tx, key); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM docs WHERE key = ?", key); err != nil {
		return err
	}
	return tx.Commit()
}

// deleteRows removes a document's rows from both full-text tables.
func deleteRows(ctx context.Context, tx *sql.Tx, key string) error {
	var id, first, last int64
	err := tx.QueryRowContext(ctx, "SELECT id, first_row, last_row FROM docs WHERE key = ?", key).Scan(&id, &first, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM docs_fts WHERE rowid = ?", id); err != nil {
		return err
	}
	if last >= first {
		if _, err := tx.ExecContext(ctx, "DELETE FROM pages_fts WHERE rowid BETWEEN ? AND ?", first, last); err != nil {
			return err
		}
	}
	return nil
}

func (ix *Index) upsert(ctx context.Context, d Doc, ex Extracted, errText string) error {
	tx, err := ix.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deleteRows(ctx, tx, d.Key); err != nil {
		return err
	}
	// New page rows go after every existing row, so this document's pages
	// are consecutive.
	var next int64
	if err := tx.QueryRowContext(ctx, "SELECT coalesce(max(rowid), 0) + 1 FROM pages_fts").Scan(&next); err != nil {
		return err
	}
	first, last := next, next-1
	var headings []string
	seen := map[string]bool{}
	for i, s := range ex.Sections {
		last = next + int64(i)
		if _, err := tx.ExecContext(ctx, "INSERT INTO pages_fts (rowid, n, heading, body) VALUES (?,?,?,?)",
			last, i+1, s.Heading, s.Text); err != nil {
			return err
		}
		if s.Heading != "" && !seen[s.Heading] {
			seen[s.Heading] = true
			headings = append(headings, s.Heading)
		}
	}
	if len(ex.Sections) == 0 {
		first, last = 0, -1
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO docs (key, source, resource_id, name, url, title, size, mtime, error, ver, paged, first_row, last_row)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET source=excluded.source, resource_id=excluded.resource_id,
		name=excluded.name, url=excluded.url, title=excluded.title, size=excluded.size, mtime=excluded.mtime, error=excluded.error,
		ver=excluded.ver, paged=excluded.paged, first_row=excluded.first_row, last_row=excluded.last_row`,
		d.Key, d.Source, d.ResourceID, d.Name, d.URL, ex.Title, d.Size, d.ModTime, errText, ExtractorVersion, ex.Paged, first, last); err != nil {
		return err
	}
	var id int64
	if err := tx.QueryRowContext(ctx, "SELECT id FROM docs WHERE key = ?", d.Key).Scan(&id); err != nil {
		return err
	}
	// Headings (such as PDF bookmarks) need not appear in the text itself.
	body := ex.Text
	if len(headings) > 0 {
		body += "\n\n" + strings.Join(headings, "\n")
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO docs_fts (rowid, title, body) VALUES (?,?,?)", id, ex.Title, body); err != nil {
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

// SearchIn runs a full-text query limited to one installed resource's
// documents (UserDocuments for the person's own, or every document when
// resourceID is empty), returning results from offset on. Documents are
// matched and ranked as wholes; each result then lists its best pages.
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
	const where = `docs_fts MATCH ? AND (? = '' OR (? = '@user' AND d.source = 'user') OR (d.source = 'resource' AND d.resource_id = ?))`
	var total int
	if err := ix.db.QueryRowContext(ctx, `SELECT count(*) FROM docs_fts f JOIN docs d ON d.id = f.rowid WHERE `+where,
		q, resourceID, resourceID, resourceID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := ix.db.QueryContext(ctx, `SELECT d.key, d.source, d.resource_id, d.name, d.url, d.title, d.paged, d.first_row, d.last_row
		FROM docs_fts f JOIN docs d ON d.id = f.rowid WHERE `+where+`
		ORDER BY bm25(docs_fts, 8.0, 1.0) LIMIT ? OFFSET ?`, q, resourceID, resourceID, resourceID, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	type found struct {
		hit         Hit
		first, last int64
	}
	var hits []found
	for rows.Next() {
		var f found
		if err := rows.Scan(&f.hit.Key, &f.hit.Source, &f.hit.ResourceID, &f.hit.Name, &f.hit.URL, &f.hit.Title,
			&f.hit.Paged, &f.first, &f.last); err != nil {
			rows.Close()
			return nil, 0, err
		}
		hits = append(hits, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	out := make([]Hit, len(hits))
	for i, f := range hits {
		if err := ix.findPages(ctx, query, &f.hit, f.first, f.last); err != nil {
			return nil, 0, err
		}
		out[i] = f.hit
	}
	return out, total, nil
}

// findPages fills in a hit's best pages and its snippet. It first looks for
// pages that hold every word; when the words are spread over several pages,
// it shows the pages holding any of them.
func (ix *Index) findPages(ctx context.Context, query string, h *Hit, first, last int64) error {
	if last < first {
		return nil
	}
	for _, q := range []string{ftsQuery(query), ftsAnyQuery(query)} {
		if q == "" {
			continue
		}
		rows, err := ix.db.QueryContext(ctx, `SELECT n, heading, snippet(pages_fts, 2, char(1), char(2), '…', 14)
			FROM pages_fts WHERE pages_fts MATCH ? AND rowid BETWEEN ? AND ?
			ORDER BY bm25(pages_fts, 0.0, 3.0, 1.0) LIMIT ?`, q, first, last, pagesPerHit)
		if err != nil {
			return err
		}
		for rows.Next() {
			var p PageHit
			var snip string
			if err := rows.Scan(&p.N, &p.Heading, &snip); err != nil {
				rows.Close()
				return err
			}
			p.Snippet = highlight(snip)
			h.Pages = append(h.Pages, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		if len(h.Pages) > 0 {
			if err := ix.db.QueryRowContext(ctx, `SELECT count(*) FROM pages_fts WHERE pages_fts MATCH ? AND rowid BETWEEN ? AND ?`,
				q, first, last).Scan(&h.PageMatches); err != nil {
				return err
			}
			h.Snippet = h.Pages[0].Snippet
			return nil
		}
	}
	// Only the title matched: show how the document begins.
	var start string
	err := ix.db.QueryRowContext(ctx, `SELECT substr(body, 1, 240) FROM pages_fts
		WHERE rowid BETWEEN ? AND ? AND length(body) > 0 ORDER BY rowid LIMIT 1`, first, last).Scan(&start)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if start != "" {
		h.Snippet = highlight(start) + "…"
	}
	return nil
}

// Text returns a document's extracted title and text, its sections joined
// by blank lines, up to max bytes.
func (ix *Index) Text(ctx context.Context, key string, max int) (title, text, url string, err error) {
	d, err := ix.Sections(ctx, key, max)
	if err != nil {
		return "", "", "", err
	}
	parts := make([]string, len(d.Sections))
	for i, s := range d.Sections {
		parts[i] = s.Text
	}
	return d.Title, clip(strings.Join(parts, "\n\n"), max), d.URL, nil
}

// DocText is a document's extracted text, section by section.
type DocText struct {
	Title    string            `json:"title"`
	URL      string            `json:"url"`
	Paged    bool              `json:"paged"`
	Sections []NumberedSection `json:"sections"`
	// Truncated is true when the sections were cut at the size limit.
	Truncated bool `json:"truncated"`
}

// NumberedSection is a section with its number (the page number of a PDF).
type NumberedSection struct {
	N int `json:"n"`
	Section
}

// Sections returns a document's sections, up to max bytes of text. It
// returns sql.ErrNoRows for a document that is not indexed.
func (ix *Index) Sections(ctx context.Context, key string, max int) (DocText, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	var d DocText
	var first, last int64
	if err := ix.db.QueryRowContext(ctx, "SELECT title, url, paged, first_row, last_row FROM docs WHERE key = ?", key).
		Scan(&d.Title, &d.URL, &d.Paged, &first, &last); err != nil {
		return d, err
	}
	d.Sections = []NumberedSection{}
	rows, err := ix.db.QueryContext(ctx, "SELECT n, heading, body FROM pages_fts WHERE rowid BETWEEN ? AND ? ORDER BY rowid", first, last)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	size := 0
	for rows.Next() {
		var s NumberedSection
		if err := rows.Scan(&s.N, &s.Heading, &s.Text); err != nil {
			return d, err
		}
		if size+len(s.Text) > max {
			s.Text = clip(s.Text, max-size)
			d.Truncated = true
		}
		size += len(s.Text)
		d.Sections = append(d.Sections, s)
		if d.Truncated {
			break
		}
	}
	return d, rows.Err()
}

// Problems returns, for each indexed document that cannot be searched
// fully, why not: an extraction error, or a problem such as a scanned PDF.
func (ix *Index) Problems(ctx context.Context) (map[string]string, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	rows, err := ix.db.QueryContext(ctx, "SELECT key, error FROM docs WHERE error != ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, e string
		if err := rows.Scan(&k, &e); err != nil {
			return nil, err
		}
		out[k] = e
	}
	return out, rows.Err()
}

// Problem returns why one document cannot be searched fully, or "".
func (ix *Index) Problem(ctx context.Context, key string) string {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	var e string
	_ = ix.db.QueryRowContext(ctx, "SELECT error FROM docs WHERE key = ?", key).Scan(&e)
	return e
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

// queryTerms reduces user input to at most eight quoted prefix terms.
func queryTerms(text string) []string {
	var terms []string
	for _, f := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127)
	}) {
		if len(terms) == 8 {
			break
		}
		terms = append(terms, `"`+f+`"*`)
	}
	return terms
}

// ftsQuery matches rows holding every term.
func ftsQuery(text string) string { return strings.Join(queryTerms(text), " ") }

// ftsAnyQuery matches rows holding any of two or more terms; with one term
// it is empty, because ftsQuery already covers it.
func ftsAnyQuery(text string) string {
	terms := queryTerms(text)
	if len(terms) < 2 {
		return ""
	}
	return strings.Join(terms, " OR ")
}

// FileURL builds the content-origin URL for a resource file.
func FileURL(resourceID, name string) string {
	return "/files/" + url.PathEscape(resourceID) + "/" + url.PathEscape(name)
}

// UserDocURL builds the content-origin URL for a user document, given its
// slash-separated path below user/documents.
func UserDocURL(name string) string {
	parts := strings.Split(name, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return "/userdocs/" + strings.Join(parts, "/")
}

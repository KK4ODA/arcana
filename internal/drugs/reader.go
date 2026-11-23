package drugs

import (
	"bytes"
	"compress/flate"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"unicode"
)

// Reader queries a built reference. It opens the file read-only; the file
// never changes once installed.
type Reader struct {
	db   *sql.DB
	Meta map[string]string
}

// Open opens a reference database.
func Open(path string) (*Reader, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&immutable=1")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	r := &Reader{db: db, Meta: map[string]string{}}
	rows, err := db.Query(`SELECT key, value FROM meta`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("not a drug label reference: %w", err)
	}
	for rows.Next() {
		var k, v string
		if rows.Scan(&k, &v) == nil {
			r.Meta[k] = v
		}
	}
	rows.Close()
	if r.Meta["schema"] != SchemaVersion {
		db.Close()
		return nil, fmt.Errorf("drug label reference format %q is not supported by this version of Arcana", r.Meta["schema"])
	}
	return r, nil
}

// Close releases the database.
func (r *Reader) Close() error { return r.db.Close() }

// Product is one label as listed.
type Product struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	Generic      string   `json:"generic,omitempty"`
	Manufacturer string   `json:"manufacturer,omitempty"`
	Kind         string   `json:"kind"`
	Route        string   `json:"route,omitempty"`
	Effective    string   `json:"effective,omitempty"`
	Ingredients  []string `json:"ingredients"`
	SetID        string   `json:"setId"`
}

// Group is the products sharing the same active ingredients.
type Group struct {
	Key      string   `json:"key"`
	Title    string   `json:"title"`
	Kinds    []string `json:"kinds"`
	Products int      `json:"products"`
	Example  Product  `json:"example"`
}

// SearchOptions filter a search.
type SearchOptions struct {
	// Kind limits results to KindOTC or KindRx ("" for all).
	Kind string
	// Homeopathic includes homeopathic products (excluded by default:
	// the FDA does not evaluate them for effectiveness).
	Homeopathic   bool
	Limit, Offset int
}

func (o *SearchOptions) defaults() {
	if o.Limit <= 0 || o.Limit > 100 {
		o.Limit = 25
	}
	if o.Offset < 0 {
		o.Offset = 0
	}
}

const productCols = `l.id, l.name, l.generic, l.manufacturer, l.kind, l.route, l.effective, l.ingredients, l.set_id`

func scanProduct(sc interface{ Scan(...any) error }) (Product, error) {
	var p Product
	var gen, man, route, eff, ing sql.NullString
	if err := sc.Scan(&p.ID, &p.Name, &gen, &man, &p.Kind, &route, &eff, &ing, &p.SetID); err != nil {
		return p, err
	}
	p.Generic, p.Manufacturer, p.Route, p.Effective = gen.String, man.String, route.String, eff.String
	p.Ingredients = []string{}
	if ing.String != "" {
		p.Ingredients = strings.Split(ing.String, ", ")
	}
	return p, nil
}

// ftsPrefix turns typed words into an FTS5 query matching every word as a
// prefix ("tylen" finds Tylenol).
func ftsPrefix(q string) string {
	var terms []string
	for _, f := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		terms = append(terms, `"`+f+`"*`)
		if len(terms) == 8 {
			break
		}
	}
	return strings.Join(terms, " ")
}

func kindFilter(o SearchOptions) (string, []any) {
	where := ""
	var args []any
	if o.Kind != "" {
		where += " AND l.kind = ?"
		args = append(args, o.Kind)
	}
	if !o.Homeopathic && o.Kind != KindHomeopathic {
		where += " AND l.kind != 'homeopathic'"
	}
	return where, args
}

// Search finds products by brand or generic name, ingredient or
// manufacturer, grouped by active ingredients, best match first.
func (r *Reader) Search(ctx context.Context, q string, o SearchOptions) ([]Group, int, error) {
	o.defaults()
	fq := ftsPrefix(q)
	if fq == "" {
		return []Group{}, 0, nil
	}
	where, args := kindFilter(o)
	var total int
	countArgs := append([]any{fq}, args...)
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT l.group_key) FROM names JOIN labels l ON l.id = names.rowid
		WHERE names MATCH ?`+where, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}
	qargs := append(append([]any{fq}, args...), o.Limit, o.Offset)
	// MATERIALIZED keeps SQLite from folding bm25() into the aggregate,
	// where FTS5 cannot evaluate it.
	rows, err := r.db.QueryContext(ctx, `WITH m AS MATERIALIZED (SELECT rowid AS id, bm25(names, 10.0, 6.0, 6.0, 1.0) AS score FROM names WHERE names MATCH ?)
		SELECT l.group_key, COUNT(*), MIN(m.score) AS best, GROUP_CONCAT(DISTINCT l.kind)
		FROM m JOIN labels l ON l.id = m.id
		WHERE 1`+where+` GROUP BY l.group_key ORDER BY best LIMIT ? OFFSET ?`, qargs...)
	if err != nil {
		return nil, 0, err
	}
	groups, err := r.collectGroups(ctx, rows, o)
	return groups, total, err
}

func (r *Reader) collectGroups(ctx context.Context, rows *sql.Rows, o SearchOptions) ([]Group, error) {
	var out []Group
	for rows.Next() {
		var g Group
		var best float64
		var kinds string
		if err := rows.Scan(&g.Key, &g.Products, &best, &kinds); err != nil {
			rows.Close()
			return nil, err
		}
		g.Kinds = strings.Split(kinds, ",")
		out = append(out, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		ex, err := r.example(ctx, out[i].Key, o)
		if err != nil {
			return nil, err
		}
		out[i].Example = ex
		out[i].Title = groupTitle(out[i].Key, ex)
	}
	if out == nil {
		out = []Group{}
	}
	return out, nil
}

func groupTitle(key string, ex Product) string {
	if len(ex.Ingredients) > 0 {
		return titleCase(strings.ToUpper(strings.ReplaceAll(key, " + ", " + ")))
	}
	if ex.Generic != "" {
		return ex.Generic
	}
	return ex.Name
}

// example picks a representative product: the most recently revised
// label of the group (of the requested kind).
func (r *Reader) example(ctx context.Context, key string, o SearchOptions) (Product, error) {
	where, args := kindFilter(o)
	row := r.db.QueryRowContext(ctx, `SELECT `+productCols+` FROM labels l WHERE l.group_key = ?`+where+
		` ORDER BY l.effective DESC LIMIT 1`, append([]any{key}, args...)...)
	return scanProduct(row)
}

// GroupProducts lists the products of a group.
func (r *Reader) GroupProducts(ctx context.Context, key string, o SearchOptions) ([]Product, int, error) {
	o.defaults()
	where, args := kindFilter(o)
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM labels l WHERE l.group_key = ?`+where, append([]any{key}, args...)...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT `+productCols+` FROM labels l WHERE l.group_key = ?`+where+
		` ORDER BY l.name COLLATE NOCASE, l.effective DESC LIMIT ? OFFSET ?`, append(append([]any{key}, args...), o.Limit, o.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []Product{}
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

// SectionText is one part of a label.
type SectionText struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// ErrNotFound means no such label.
var ErrNotFound = errors.New("label not found")

// Label returns one label's product details and sections in display order.
func (r *Reader) Label(ctx context.Context, id int64) (Product, []SectionText, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+productCols+`, d.body FROM labels l JOIN docs d ON d.id = l.doc WHERE l.id = ?`, id)
	var p Product
	var gen, man, route, eff, ing sql.NullString
	var body []byte
	err := row.Scan(&p.ID, &p.Name, &gen, &man, &p.Kind, &route, &eff, &ing, &p.SetID, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil, ErrNotFound
	}
	if err != nil {
		return p, nil, err
	}
	p.Generic, p.Manufacturer, p.Route, p.Effective = gen.String, man.String, route.String, eff.String
	p.Ingredients = []string{}
	if ing.String != "" {
		p.Ingredients = strings.Split(ing.String, ", ")
	}
	data, err := io.ReadAll(io.LimitReader(flate.NewReader(bytes.NewReader(body)), 8<<20))
	if err != nil {
		return p, nil, fmt.Errorf("label text is damaged: %w", err)
	}
	var secs map[string]string
	if err := json.Unmarshal(data, &secs); err != nil {
		return p, nil, fmt.Errorf("label text is damaged: %w", err)
	}
	var out []SectionText
	for _, s := range Sections {
		if t := secs[s.Key]; t != "" {
			out = append(out, SectionText{Key: s.Key, Title: s.Title, Text: t})
		}
	}
	return p, out, nil
}

// Stats describes the installed reference.
type Stats struct {
	Labels   int               `json:"labels"`
	Distinct int               `json:"distinct"`
	ByKind   map[string]int    `json:"byKind"`
	Meta     map[string]string `json:"meta"`
}

// Stats counts labels.
func (r *Reader) Stats(ctx context.Context) (Stats, error) {
	st := Stats{ByKind: map[string]int{}, Meta: r.Meta}
	rows, err := r.db.QueryContext(ctx, `SELECT kind, COUNT(*) FROM labels GROUP BY kind`)
	if err != nil {
		return st, err
	}
	for rows.Next() {
		var k string
		var n int
		if rows.Scan(&k, &n) == nil {
			st.ByKind[k] = n
			st.Labels += n
		}
	}
	rows.Close()
	err = r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM docs`).Scan(&st.Distinct)
	return st, err
}

// SituationOptions filter BySituation.
type SituationOptions struct {
	// Kind defaults to KindOTC: situations are for choosing a medicine
	// without a prescription.
	Kind        string
	Homeopathic bool
	Limit       int
}

// SituationResult lists medicine groups whose labels cover situations.
type SituationResult struct {
	Situations []string `json:"situations"`
	Groups     []Group  `json:"groups"`
}

// BySituation finds medicines whose labels mention the situations: first
// those covering all of them (when more than one is given), then each
// situation on its own.
func (r *Reader) BySituation(ctx context.Context, slugs []string, o SituationOptions) ([]SituationResult, error) {
	if o.Kind == "" {
		o.Kind = KindOTC
	}
	if o.Kind == "all" {
		o.Kind = ""
	}
	if o.Limit <= 0 || o.Limit > 50 {
		o.Limit = 15
	}
	var sits []Situation
	for _, s := range slugs {
		if st, ok := SituationBySlug(s); ok {
			sits = append(sits, st)
		}
	}
	if len(sits) == 0 {
		return nil, errors.New("choose at least one situation")
	}
	so := SearchOptions{Kind: o.Kind, Homeopathic: o.Homeopathic, Limit: o.Limit}
	run := func(set []Situation) (SituationResult, error) {
		var parts []string
		res := SituationResult{}
		for _, s := range set {
			parts = append(parts, "("+s.ftsQuery()+")")
			res.Situations = append(res.Situations, s.Slug)
		}
		where, args := kindFilter(so)
		rows, err := r.db.QueryContext(ctx, `SELECT l.group_key, COUNT(*), 0.0, GROUP_CONCAT(DISTINCT l.kind) FROM labels l
			WHERE l.doc IN (SELECT rowid FROM uses WHERE uses MATCH ?)`+where+`
			GROUP BY l.group_key ORDER BY COUNT(*) DESC LIMIT ?`, append(append([]any{strings.Join(parts, " AND ")}, args...), o.Limit)...)
		if err != nil {
			return res, err
		}
		res.Groups, err = r.collectGroups(ctx, rows, so)
		return res, err
	}
	var out []SituationResult
	if len(sits) > 1 {
		all, err := run(sits)
		if err != nil {
			return nil, err
		}
		out = append(out, all)
	}
	for _, s := range sits {
		one, err := run([]Situation{s})
		if err != nil {
			return nil, err
		}
		out = append(out, one)
	}
	return out, nil
}

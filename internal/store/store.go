// Package store is Arcana's metadata database.
//
// The database is an index, not the source of truth. Installed resources are
// described by sidecar files next to their content, and catalogs are cached as
// JSON, so a lost or damaged database is rebuilt rather than restored. This
// matters on removable media, where a drive pulled mid-write or a USB bridge
// that ignores flush requests can damage any file.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite: no cgo, cross-compiles to every target

	"github.com/KK4ODA/arcana/internal/catalog"
)

// ErrCorrupt indicates the database failed its integrity check.
var ErrCorrupt = errors.New("metadata database is damaged")

// Store wraps the SQLite database.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (creating if needed) the database at path.
//
// Configuration: WAL journal with synchronous=FULL, so every committed
// transaction is durable once Commit returns, and readers (the HTTP server)
// never block the download manager's checkpoints. A clean shutdown
// checkpoints and truncates the WAL so the drive carries a single
// self-contained file.
func Open(path string) (*Store, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(FULL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Set("_txlock", "immediate")
	dsn := "file:" + filepath.ToSlash(path) + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	s := &Store{db: db, path: path}
	if err := s.check(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// OpenMemory opens a private in-memory database. Used for read-only
// libraries: the index is rebuilt from the drive's sidecars and catalog
// caches without writing anything.
func OpenMemory() (*Store, error) {
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // each connection would otherwise get its own database
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// OpenOrRecover opens the database; if it is damaged it is moved aside and a
// fresh one created. The second return value reports whether a rebuild from
// sidecars is needed.
func OpenOrRecover(path string) (*Store, bool, error) {
	_, statErr := os.Stat(path)
	fresh := errors.Is(statErr, os.ErrNotExist)
	s, err := Open(path)
	if err == nil {
		return s, fresh, nil
	}
	stamp := time.Now().UTC().Format("20060102T150405")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); err == nil {
			_ = os.Rename(path+suffix, path+".damaged-"+stamp+suffix)
		}
	}
	s, err2 := Open(path)
	if err2 != nil {
		return nil, false, fmt.Errorf("%v; recreating database also failed: %w", err, err2)
	}
	return s, true, nil
}

func (s *Store) check() error {
	var res string
	if err := s.db.QueryRow("PRAGMA quick_check").Scan(&res); err != nil {
		return fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	if res != "ok" {
		return fmt.Errorf("%w: %s", ErrCorrupt, res)
	}
	return nil
}

// Close checkpoints the WAL into the main file and closes the database.
func (s *Store) Close() error {
	if s.path != "" {
		_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	}
	return s.db.Close()
}

// DB exposes the handle for tests.
func (s *Store) DB() *sql.DB { return s.db }

var migrations = []string{
	// 1: initial schema.
	`
CREATE TABLE resources (
	id TEXT PRIMARY KEY,
	provider TEXT NOT NULL,
	family TEXT NOT NULL DEFAULT '',
	variant TEXT NOT NULL DEFAULT '',
	title TEXT NOT NULL,
	category TEXT NOT NULL,
	languages TEXT NOT NULL,
	version TEXT NOT NULL,
	published TEXT NOT NULL DEFAULT '',
	download_size INTEGER NOT NULL,
	format TEXT NOT NULL,
	redistribution TEXT NOT NULL,
	popularity INTEGER NOT NULL DEFAULT 0,
	data TEXT NOT NULL
);
CREATE INDEX resources_family ON resources(family);
CREATE INDEX resources_category ON resources(category);
CREATE INDEX resources_provider ON resources(provider);
CREATE VIRTUAL TABLE resources_fts USING fts5(id UNINDEXED, title, description, tags,
	tokenize = 'unicode61 remove_diacritics 2');
CREATE TABLE providers (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	fetched_at TEXT NOT NULL DEFAULT '',
	resource_count INTEGER NOT NULL DEFAULT 0,
	last_attempt TEXT NOT NULL DEFAULT '',
	last_error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE installed (
	resource_id TEXT PRIMARY KEY,
	version TEXT NOT NULL,
	format TEXT NOT NULL,
	size INTEGER NOT NULL,
	installed_at TEXT NOT NULL,
	data TEXT NOT NULL
);
CREATE TABLE jobs (
	id TEXT PRIMARY KEY,
	resource_id TEXT NOT NULL,
	state TEXT NOT NULL,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	data TEXT NOT NULL
);
CREATE INDEX jobs_state ON jobs(state);
`,
}

func (s *Store) migrate() error {
	var version int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("database schema %d is newer than this build supports (%d)", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// ---------------------------------------------------------------- catalog

// ProviderStatus records the last catalog refresh of a provider.
type ProviderStatus struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	FetchedAt     string `json:"fetchedAt"`
	ResourceCount int    `json:"resourceCount"`
	LastAttempt   string `json:"lastAttempt"`
	LastError     string `json:"lastError"`
}

// ReplaceProviderResources atomically swaps one provider's catalog entries.
func (s *Store) ReplaceProviderResources(ctx context.Context, providerID, name string, rs []catalog.Resource, fetchedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM resources_fts WHERE id IN (SELECT id FROM resources WHERE provider = ?)`, providerID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM resources WHERE provider = ?`, providerID); err != nil {
		return err
	}
	ins, err := tx.PrepareContext(ctx, `INSERT INTO resources
		(id, provider, family, variant, title, category, languages, version, published, download_size, format, redistribution, popularity, data)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	fts, err := tx.PrepareContext(ctx, `INSERT INTO resources_fts (id, title, description, tags) VALUES (?,?,?,?)`)
	if err != nil {
		return err
	}
	defer fts.Close()
	for _, r := range rs {
		data, err := json.Marshal(r)
		if err != nil {
			return err
		}
		langs := "," + strings.Join(r.Languages, ",") + ","
		if _, err := ins.ExecContext(ctx, r.ID, r.Provider, r.Family, r.Variant, r.Title, r.Category, langs, r.Version,
			r.Published, r.DownloadSize, r.Format, r.License.Redistribution, r.Popularity, string(data)); err != nil {
			return fmt.Errorf("insert %s: %w", r.ID, err)
		}
		tags := strings.Join(append(append([]string{}, r.Tags...), r.Family, r.Subcategory, r.Publisher), " ")
		if _, err := fts.ExecContext(ctx, r.ID, r.Title, r.Description, tags); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO providers (id, name, fetched_at, resource_count, last_attempt, last_error)
		VALUES (?,?,?,?,?, '')
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, fetched_at=excluded.fetched_at,
			resource_count=excluded.resource_count, last_attempt=excluded.last_attempt, last_error=''`,
		providerID, name, fetchedAt.UTC().Format(time.RFC3339), len(rs), now()); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordProviderError notes a failed refresh without touching cached entries.
func (s *Store) RecordProviderError(ctx context.Context, providerID, name, msg string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO providers (id, name, last_attempt, last_error) VALUES (?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET last_attempt=excluded.last_attempt, last_error=excluded.last_error`,
		providerID, name, now(), msg)
	return err
}

// Providers lists provider refresh status.
func (s *Store) Providers(ctx context.Context) ([]ProviderStatus, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, fetched_at, resource_count, last_attempt, last_error FROM providers ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProviderStatus
	for rows.Next() {
		var p ProviderStatus
		if err := rows.Scan(&p.ID, &p.Name, &p.FetchedAt, &p.ResourceCount, &p.LastAttempt, &p.LastError); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Query filters the catalog.
type Query struct {
	Text     string
	Category string
	Language string
	Provider string
	Format   string
	Family   string
	// Sort is "relevance", "title", "size", "size-desc", "date".
	Sort   string
	Limit  int
	Offset int
}

// SearchResources returns matching resources and the total match count.
func (s *Store) SearchResources(ctx context.Context, q Query) ([]catalog.Resource, int, error) {
	var where []string
	var args []any
	from := "resources r"
	match := ftsQuery(q.Text)
	if match != "" {
		from = "resources r JOIN resources_fts f ON f.id = r.id"
		where = append(where, "resources_fts MATCH ?")
		args = append(args, match)
	}
	if q.Category != "" {
		where = append(where, "r.category = ?")
		args = append(args, q.Category)
	}
	if q.Language != "" {
		where = append(where, "(r.languages LIKE ? OR r.languages LIKE ?)")
		args = append(args, "%,"+q.Language+",%", "%,"+q.Language+"-%")
	}
	if q.Provider != "" {
		where = append(where, "r.provider = ?")
		args = append(args, q.Provider)
	}
	if q.Format != "" {
		where = append(where, "r.format = ?")
		args = append(args, q.Format)
	}
	if q.Family != "" {
		where = append(where, "r.family = ?")
		args = append(args, q.Family)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM "+from+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "r.title COLLATE NOCASE"
	switch q.Sort {
	case "size":
		order = "r.download_size ASC"
	case "size-desc":
		order = "r.download_size DESC"
	case "date":
		order = "r.published DESC, r.title COLLATE NOCASE"
	case "relevance", "":
		if match != "" {
			order = "bm25(resources_fts, 0, 10.0, 2.0, 1.0)"
		}
	}
	limit := q.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, "SELECT r.data FROM "+from+cond+" ORDER BY "+order+" LIMIT ? OFFSET ?",
		append(args, limit, max(q.Offset, 0))...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, err := scanResources(rows)
	return out, total, err
}

// ftsQuery turns user input into a safe FTS5 prefix query. Only letters and
// digits survive, so FTS5 syntax cannot be injected.
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

func scanResources(rows *sql.Rows) ([]catalog.Resource, error) {
	var out []catalog.Resource
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var r catalog.Resource
		if err := json.Unmarshal([]byte(data), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetResource returns one catalog entry.
func (s *Store) GetResource(ctx context.Context, id string) (catalog.Resource, error) {
	var data string
	err := s.db.QueryRowContext(ctx, "SELECT data FROM resources WHERE id = ?", id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.Resource{}, catalog.ErrNotFound
	}
	if err != nil {
		return catalog.Resource{}, err
	}
	var r catalog.Resource
	err = json.Unmarshal([]byte(data), &r)
	return r, err
}

// FamilyVariants returns every variant of the given families.
func (s *Store) FamilyVariants(ctx context.Context, families []string) (map[string][]catalog.Resource, error) {
	out := map[string][]catalog.Resource{}
	if len(families) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(families)), ",")
	args := make([]any, len(families))
	for i, f := range families {
		args[i] = f
	}
	rows, err := s.db.QueryContext(ctx, "SELECT data FROM resources WHERE family IN ("+ph+") ORDER BY download_size DESC", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rs, err := scanResources(rows)
	if err != nil {
		return nil, err
	}
	for _, r := range rs {
		out[r.Family] = append(out[r.Family], r)
	}
	return out, nil
}

// Count is a facet value.
type Count struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// CategoryCounts counts resources per category, optionally for a language.
func (s *Store) CategoryCounts(ctx context.Context, lang string) ([]Count, error) {
	q := "SELECT category, count(*) FROM resources"
	var args []any
	if lang != "" {
		q += " WHERE (languages LIKE ? OR languages LIKE ?)"
		args = append(args, "%,"+lang+",%", "%,"+lang+"-%")
	}
	return s.counts(ctx, q+" GROUP BY category", args...)
}

// LanguageCounts counts resources by primary language.
func (s *Store) LanguageCounts(ctx context.Context) ([]Count, error) {
	return s.counts(ctx, `SELECT substr(languages, 2, instr(substr(languages, 2), ',') - 1) AS lang, count(*)
		FROM resources GROUP BY lang ORDER BY count(*) DESC`)
}

func (s *Store) counts(ctx context.Context, q string, args ...any) ([]Count, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Count
	for rows.Next() {
		var c Count
		if err := rows.Scan(&c.Key, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- installed

// Installed describes a published resource. The same structure is written to
// the resource's sidecar file (content/<id>/installed.json).
type Installed struct {
	Format        string           `json:"format"`
	FormatVersion int              `json:"formatVersion"`
	ResourceID    string           `json:"resourceId"`
	Version       string           `json:"version"`
	Files         []InstalledFile  `json:"files"`
	InstalledAt   time.Time        `json:"installedAt"`
	InstalledBy   string           `json:"installedBy"`
	Resource      catalog.Resource `json:"resource"`
	// Verification records how integrity was established, e.g.
	// "sha256 matched publisher metalink".
	Verification string `json:"verification"`
	// Unverified is true when the source published no hash: the file is
	// recorded with the hash Arcana computed, but its authenticity could not
	// be checked.
	Unverified bool `json:"unverified,omitempty"`
}

// InstalledFile is one published file. Path is relative to the resource
// directory and uses forward slashes.
type InstalledFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// TotalSize sums the installed files.
func (i Installed) TotalSize() int64 {
	var n int64
	for _, f := range i.Files {
		n += f.Size
	}
	return n
}

// PutInstalled records or replaces an installed resource.
func (s *Store) PutInstalled(ctx context.Context, in Installed) error {
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO installed (resource_id, version, format, size, installed_at, data)
		VALUES (?,?,?,?,?,?) ON CONFLICT(resource_id) DO UPDATE SET version=excluded.version, format=excluded.format,
		size=excluded.size, installed_at=excluded.installed_at, data=excluded.data`,
		in.ResourceID, in.Version, in.Resource.Format, in.TotalSize(), in.InstalledAt.UTC().Format(time.RFC3339), string(data))
	return err
}

// DeleteInstalled removes an installed record.
func (s *Store) DeleteInstalled(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM installed WHERE resource_id = ?", id)
	return err
}

// ListInstalled returns all installed resources ordered by title.
func (s *Store) ListInstalled(ctx context.Context) ([]Installed, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT data FROM installed")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Installed
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var in Installed
		if err := json.Unmarshal([]byte(data), &in); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// GetInstalled returns one installed resource.
func (s *Store) GetInstalled(ctx context.Context, id string) (Installed, error) {
	var data string
	err := s.db.QueryRowContext(ctx, "SELECT data FROM installed WHERE resource_id = ?", id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return Installed{}, catalog.ErrNotFound
	}
	if err != nil {
		return Installed{}, err
	}
	var in Installed
	err = json.Unmarshal([]byte(data), &in)
	return in, err
}

// ReplaceInstalled replaces the whole installed table (used when rebuilding
// from sidecars).
func (s *Store) ReplaceInstalled(ctx context.Context, all []Installed) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM installed"); err != nil {
		return err
	}
	for _, in := range all {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO installed (resource_id, version, format, size, installed_at, data) VALUES (?,?,?,?,?,?)`,
			in.ResourceID, in.Version, in.Resource.Format, in.TotalSize(), in.InstalledAt.UTC().Format(time.RFC3339), string(data)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---------------------------------------------------------------- jobs

// Job states.
const (
	JobQueued     = "queued"
	JobResolving  = "resolving"
	JobRunning    = "downloading"
	JobWaiting    = "waiting"
	JobPaused     = "paused"
	JobVerifying  = "verifying"
	JobInstalling = "installing"
	JobCompleted  = "completed"
	JobFailed     = "failed"
	JobCancelled  = "cancelled"
)

// Job is a persistent download-and-install request.
type Job struct {
	ID         string           `json:"id"`
	ResourceID string           `json:"resourceId"`
	Version    string           `json:"version"`
	State      string           `json:"state"`
	Resource   catalog.Resource `json:"resource"`
	// Resolved is set once metalinks have been resolved, so a restart does
	// not need the network to learn hashes it already knows.
	Resolved bool `json:"resolved"`
	// FileIndex is the file currently being transferred.
	FileIndex int `json:"fileIndex"`
	// Offset is the last durable checkpoint within the current file.
	Offset int64 `json:"offset"`
	// HashState holds marshalled hash states at Offset, keyed by algorithm.
	HashState map[string][]byte `json:"hashState,omitempty"`
	// Validator is the ETag or Last-Modified value used for If-Range.
	Validator   string   `json:"validator,omitempty"`
	MirrorIndex int      `json:"mirrorIndex"`
	BadMirrors  []string `json:"badMirrors,omitempty"`
	Attempts    int      `json:"attempts"`
	// IntegrityFailures counts verification failures; a second failure
	// fails the job rather than retrying forever.
	IntegrityFailures int       `json:"integrityFailures,omitempty"`
	Error             string    `json:"error,omitempty"`
	NextAttempt       time.Time `json:"nextAttempt,omitempty"`
	// Done holds per-file results for files already completed.
	Done      []InstalledFile `json:"done,omitempty"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
	// ReplacesVersion is the installed version this job will replace.
	ReplacesVersion string `json:"replacesVersion,omitempty"`
}

// Active reports whether the job still needs work.
func (j *Job) Active() bool {
	switch j.State {
	case JobCompleted, JobFailed, JobCancelled:
		return false
	}
	return true
}

// PutJob inserts or updates a job. Called at every checkpoint, so it is the
// unit of crash recovery for downloads.
func (s *Store) PutJob(ctx context.Context, j *Job) error {
	j.UpdatedAt = time.Now().UTC()
	if j.CreatedAt.IsZero() {
		j.CreatedAt = j.UpdatedAt
	}
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO jobs (id, resource_id, state, created_at, updated_at, data) VALUES (?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET state=excluded.state, updated_at=excluded.updated_at, data=excluded.data`,
		j.ID, j.ResourceID, j.State, j.CreatedAt.Format(time.RFC3339Nano), j.UpdatedAt.Format(time.RFC3339Nano), string(data))
	return err
}

// GetJob loads one job.
func (s *Store) GetJob(ctx context.Context, id string) (*Job, error) {
	var data string
	err := s.db.QueryRowContext(ctx, "SELECT data FROM jobs WHERE id = ?", id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, catalog.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var j Job
	err = json.Unmarshal([]byte(data), &j)
	return &j, err
}

// ListJobs returns jobs in creation order.
func (s *Store) ListJobs(ctx context.Context) ([]*Job, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT data FROM jobs ORDER BY created_at")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Job
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var j Job
		if err := json.Unmarshal([]byte(data), &j); err != nil {
			return nil, err
		}
		out = append(out, &j)
	}
	return out, rows.Err()
}

// DeleteJob removes a job record.
func (s *Store) DeleteJob(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM jobs WHERE id = ?", id)
	return err
}

// PruneJobs removes finished jobs older than the given age.
func (s *Store) PruneJobs(ctx context.Context, olderThan time.Duration) error {
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `DELETE FROM jobs WHERE state IN (?,?,?) AND updated_at < ?`,
		JobCompleted, JobCancelled, JobFailed, cutoff)
	return err
}

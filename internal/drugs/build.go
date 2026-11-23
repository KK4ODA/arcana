package drugs

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// SchemaVersion is stored in the database; readers refuse other versions.
const SchemaVersion = "1"

const schema = `
CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT NOT NULL);
-- One row per distinct label text (repackagers often publish identical
-- labels); body is deflate-compressed JSON {section: text}.
CREATE TABLE docs(id INTEGER PRIMARY KEY, hash BLOB NOT NULL UNIQUE, body BLOB NOT NULL);
CREATE TABLE labels(
	id INTEGER PRIMARY KEY,
	set_id TEXT NOT NULL,
	version TEXT,
	effective TEXT,
	name TEXT NOT NULL,
	generic TEXT,
	manufacturer TEXT,
	kind TEXT NOT NULL,
	route TEXT,
	ingredients TEXT,
	group_key TEXT NOT NULL,
	doc INTEGER NOT NULL REFERENCES docs(id)
);
CREATE VIRTUAL TABLE names USING fts5(name, generic, ingredients, manufacturer,
	content='labels', content_rowid='id', tokenize='unicode61 remove_diacritics 2');
-- Purpose and uses of each distinct label text, for "by situation".
CREATE VIRTUAL TABLE uses USING fts5(text, content='', tokenize='porter unicode61 remove_diacritics 2');
`

// BuildStats summarises a build.
type BuildStats struct {
	Records int `json:"records"`
	Labels  int `json:"labels"`
	Docs    int `json:"docs"`
	Skipped int `json:"skipped"`
}

// Build writes a reference database at out from openFDA label zip files.
// Each zip entry must be a JSON document with a "results" array. Zip
// entries are checked against their CRC-32 as they are read.
func Build(ctx context.Context, zips []string, out string, meta map[string]string) (BuildStats, error) {
	var st BuildStats
	tmp := out + ".building"
	for _, p := range []string{tmp, tmp + "-journal", tmp + "-wal", tmp + "-shm"} {
		os.Remove(p)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(tmp)+"?_pragma=journal_mode(OFF)&_pragma=synchronous(OFF)&_pragma=page_size(4096)")
	if err != nil {
		return st, err
	}
	db.SetMaxOpenConns(1)
	closed := false
	defer func() {
		if !closed {
			db.Close()
			os.Remove(tmp)
		}
	}()
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return st, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return st, err
	}
	defer tx.Rollback()
	insDoc, _ := tx.PrepareContext(ctx, `INSERT INTO docs(hash, body) VALUES(?, ?) ON CONFLICT(hash) DO NOTHING`)
	getDoc, _ := tx.PrepareContext(ctx, `SELECT id FROM docs WHERE hash = ?`)
	insUses, _ := tx.PrepareContext(ctx, `INSERT INTO uses(rowid, text) VALUES(?, ?)`)
	insLabel, _ := tx.PrepareContext(ctx, `INSERT INTO labels(set_id, version, effective, name, generic, manufacturer, kind, route, ingredients, group_key, doc)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`)
	seenSet := map[string]bool{}
	var fw *flate.Writer
	add := func(l Label) error {
		// openFDA can list a set twice when its case differs; keep one.
		if seenSet[l.SetID] {
			return nil
		}
		seenSet[l.SetID] = true
		body, _ := json.Marshal(l.Sections)
		sum := sha256.Sum256(body)
		var buf bytes.Buffer
		if fw == nil {
			fw, _ = flate.NewWriter(&buf, flate.BestCompression)
		} else {
			fw.Reset(&buf)
		}
		fw.Write(body)
		fw.Close()
		res, err := insDoc.ExecContext(ctx, sum[:], buf.Bytes())
		if err != nil {
			return err
		}
		var docID int64
		if n, _ := res.RowsAffected(); n == 1 {
			docID, _ = res.LastInsertId()
			st.Docs++
			if u := l.Uses(); u != "" {
				if _, err := insUses.ExecContext(ctx, docID, u); err != nil {
					return err
				}
			}
		} else if err := getDoc.QueryRowContext(ctx, sum[:]).Scan(&docID); err != nil {
			return err
		}
		_, err = insLabel.ExecContext(ctx, l.SetID, l.Version, l.Effective, l.Name, l.Generic, l.Manufacturer, l.Kind, l.Route,
			strings.Join(l.Ingredients, ", "), l.GroupKey(), docID)
		if err == nil {
			st.Labels++
		}
		return err
	}
	for _, zp := range zips {
		if err := eachRecord(ctx, zp, func(raw []byte) error {
			st.Records++
			l, ok := ParseRecord(raw)
			if !ok {
				st.Skipped++
				return nil
			}
			return add(l)
		}); err != nil {
			if errors.Is(err, ErrCorrupt) {
				return st, &PartError{Path: zp, Err: err}
			}
			return st, fmt.Errorf("%s: %w", filepath.Base(zp), err)
		}
	}
	if st.Labels == 0 {
		return st, errors.New("the source contained no usable drug labels")
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO names(names) VALUES('rebuild')`); err != nil {
		return st, err
	}
	if _, err := tx.ExecContext(ctx, `CREATE INDEX labels_group ON labels(group_key)`); err != nil {
		return st, err
	}
	meta["schema"] = SchemaVersion
	meta["built"] = time.Now().UTC().Format(time.RFC3339)
	meta["labels"] = fmt.Sprint(st.Labels)
	for k, v := range meta {
		if _, err := tx.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES(?, ?)`, k, v); err != nil {
			return st, err
		}
	}
	if err := tx.Commit(); err != nil {
		return st, err
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO names(names) VALUES('optimize'); INSERT INTO uses(uses) VALUES('optimize')`); err != nil {
		return st, err
	}
	if _, err := db.ExecContext(ctx, `VACUUM`); err != nil {
		return st, err
	}
	if err := db.Close(); err != nil {
		return st, err
	}
	closed = true
	if err := os.Rename(tmp, out); err != nil {
		os.Remove(tmp)
		return st, err
	}
	return st, nil
}

// ErrCorrupt means a downloaded part failed its CRC or could not be read
// as a zip; it should be downloaded again.
var ErrCorrupt = errors.New("downloaded data is damaged")

// PartError names the damaged part.
type PartError struct {
	Path string
	Err  error
}

func (e *PartError) Error() string { return filepath.Base(e.Path) + ": " + e.Err.Error() }
func (e *PartError) Unwrap() error { return e.Err }

// eachRecord streams the records of every JSON entry in a zip file.
func eachRecord(ctx context.Context, path string, fn func([]byte) error) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCorrupt, err)
	}
	defer zr.Close()
	for _, f := range zr.File {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".json") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		err = streamResults(ctx, rc, fn)
		// Reading to the end makes archive/zip compare the CRC-32.
		if err == nil {
			_, err = io.Copy(io.Discard, rc)
		}
		rc.Close()
		if err != nil {
			if errors.Is(err, zip.ErrChecksum) || errors.Is(err, io.ErrUnexpectedEOF) {
				return fmt.Errorf("%w: %v", ErrCorrupt, err)
			}
			return err
		}
	}
	return nil
}

// streamResults decodes {"meta": …, "results": [record, …]} one record at
// a time, so a 600 MB document never has to fit in memory.
func streamResults(ctx context.Context, r io.Reader, fn func([]byte) error) error {
	dec := json.NewDecoder(r)
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return fmt.Errorf("%w: not a JSON object", ErrCorrupt)
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
		if key, _ := t.(string); key != "results" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return fmt.Errorf("%w: %v", ErrCorrupt, err)
			}
			continue
		}
		if t, err := dec.Token(); err != nil || t != json.Delim('[') {
			return fmt.Errorf("%w: results is not a list", ErrCorrupt)
		}
		n := 0
		for dec.More() {
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				return fmt.Errorf("%w: %v", ErrCorrupt, err)
			}
			if err := fn(raw); err != nil {
				return err
			}
			if n++; n%500 == 0 && ctx.Err() != nil {
				return ctx.Err()
			}
		}
		if _, err := dec.Token(); err != nil {
			return fmt.Errorf("%w: %v", ErrCorrupt, err)
		}
	}
	return nil
}

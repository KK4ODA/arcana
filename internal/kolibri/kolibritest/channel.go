package kolibritest

import (
	"database/sql"
	"path/filepath"

	_ "modernc.org/sqlite" // SQLite driver
)

// Node IDs of the channel database WriteChannelDB makes.
const (
	ChannelID = "c1f2b7e6ac9f56a2bb44fa7a48b66dce" // Khan Academy (Español)
	Root      = "a0000000000000000000000000000000"
	Math      = "a1000000000000000000000000000000"
	Grade1    = "a1100000000000000000000000000000"
	Grade2    = "a1200000000000000000000000000000"
	Art       = "a2000000000000000000000000000000"
)

// WriteChannelDB writes a small channel database in Kolibri Studio's
// layout: Matemáticas (1° and 2° Primaria) and Arte, with one video shared
// by both grades (1,000 bytes), an exercise (10) and a painting video
// (500), plus an empty topic.
func WriteChannelDB(p, channelID string) error {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(p))
	if err != nil {
		return err
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE content_channelmetadata (id TEXT, name TEXT, version INTEGER, root_id TEXT)`,
		`CREATE TABLE content_contentnode (id TEXT, title TEXT, kind TEXT, content_id TEXT, tree_id INTEGER, lft INTEGER, rght INTEGER, parent_id TEXT)`,
		`CREATE TABLE content_file (id TEXT, contentnode_id TEXT, local_file_id TEXT)`,
		`CREATE TABLE content_localfile (id TEXT, file_size INTEGER)`,
		`INSERT INTO content_channelmetadata VALUES ('` + channelID + `', 'Khan Academy (Español)', 14, '` + Root + `')`,
		`INSERT INTO content_contentnode VALUES
			('` + Root + `', 'Khan Academy', 'topic', 'r', 1, 1, 20, NULL),
			('` + Math + `', 'Matemáticas', 'topic', 'm', 1, 2, 13, '` + Root + `'),
			('` + Grade1 + `', '1° Primaria', 'topic', 'g1', 1, 3, 8, '` + Math + `'),
			('b1', 'Contar', 'video', 'v1', 1, 4, 5, '` + Grade1 + `'),
			('b2', 'Sumar', 'exercise', 'e1', 1, 6, 7, '` + Grade1 + `'),
			('` + Grade2 + `', '2° Primaria', 'topic', 'g2', 1, 9, 12, '` + Math + `'),
			('b3', 'Sumar otra vez', 'video', 'v1', 1, 10, 11, '` + Grade2 + `'),
			('` + Art + `', 'Arte', 'topic', 'a', 1, 14, 17, '` + Root + `'),
			('b4', 'Pintura', 'video', 'v2', 1, 15, 16, '` + Art + `'),
			('a3000000000000000000000000000000', 'Vacío', 'topic', 'z', 1, 18, 19, '` + Root + `')`,
		`INSERT INTO content_file VALUES ('f1', 'b1', 'L1'), ('f2', 'b2', 'L2'), ('f3', 'b3', 'L1'), ('f4', 'b4', 'L3')`,
		`INSERT INTO content_localfile VALUES ('L1', 1000), ('L2', 10), ('L3', 500)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

package kolibri

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	tChannel = "c1f2b7e6ac9f56a2bb44fa7a48b66dce"
	tRoot    = "a0000000000000000000000000000000"
	tMath    = "a1000000000000000000000000000000"
	tGrade1  = "a1100000000000000000000000000000"
	tGrade2  = "a1200000000000000000000000000000"
	tArt     = "a2000000000000000000000000000000"
)

// makeChannelDB writes a small channel database in Kolibri's layout:
// Math (Grade 1, Grade 2) and Art, with one video shared by both grades.
func makeChannelDB(t *testing.T, p, channelID string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(p))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmts := []string{
		`CREATE TABLE content_channelmetadata (id TEXT, name TEXT, version INTEGER, root_id TEXT)`,
		`CREATE TABLE content_contentnode (id TEXT, title TEXT, kind TEXT, content_id TEXT, tree_id INTEGER, lft INTEGER, rght INTEGER, parent_id TEXT)`,
		`CREATE TABLE content_file (id TEXT, contentnode_id TEXT, local_file_id TEXT)`,
		`CREATE TABLE content_localfile (id TEXT, file_size INTEGER)`,
		`INSERT INTO content_channelmetadata VALUES ('` + channelID + `', 'Khan Academy (Español)', 14, '` + tRoot + `')`,
		`INSERT INTO content_contentnode VALUES
			('` + tRoot + `', 'Khan Academy', 'topic', 'r', 1, 1, 20, NULL),
			('` + tMath + `', 'Matemáticas', 'topic', 'm', 1, 2, 13, '` + tRoot + `'),
			('` + tGrade1 + `', '1° Primaria', 'topic', 'g1', 1, 3, 8, '` + tMath + `'),
			('b1', 'Contar', 'video', 'v1', 1, 4, 5, '` + tGrade1 + `'),
			('b2', 'Sumar', 'exercise', 'e1', 1, 6, 7, '` + tGrade1 + `'),
			('` + tGrade2 + `', '2° Primaria', 'topic', 'g2', 1, 9, 12, '` + tMath + `'),
			('b3', 'Sumar otra vez', 'video', 'v1', 1, 10, 11, '` + tGrade2 + `'),
			('` + tArt + `', 'Arte', 'topic', 'a', 1, 14, 17, '` + tRoot + `'),
			('b4', 'Pintura', 'video', 'v2', 1, 15, 16, '` + tArt + `'),
			('` + "a3000000000000000000000000000000" + `', 'Vacío', 'topic', 'z', 1, 18, 19, '` + tRoot + `')`,
		`INSERT INTO content_file VALUES ('f1', 'b1', 'L1'), ('f2', 'b2', 'L2'), ('f3', 'b3', 'L1'), ('f4', 'b4', 'L3')`,
		`INSERT INTO content_localfile VALUES ('L1', 1000), ('L2', 10), ('L3', 500)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatal(err)
		}
	}
}

func TestChannelTreeAndSizes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "es.sqlite3")
	makeChannelDB(t, p, tChannel)
	c, err := OpenChannelDB(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.ID != tChannel || c.Ver != 14 {
		t.Fatalf("channel %s v%d", c.ID, c.Ver)
	}
	ctx := context.Background()
	root, err := c.Tree(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	// The shared video counts once in the channel and in Math.
	if root.Size != 1510 || root.Resources != 3 || len(root.Children) != 2 {
		t.Fatalf("root %+v", root)
	}
	math := root.Children[0]
	if math.Title != "Matemáticas" || math.Size != 1010 || len(math.Children) != 2 || math.Children[1].Size != 1000 {
		t.Fatalf("math %+v", math)
	}
	// Both grades together: the shared video once.
	if size, n, err := c.SizeOf(ctx, []string{tGrade1, tGrade2, tGrade1}); err != nil || size != 1010 || n != 2 {
		t.Fatalf("grades %d %d %v", size, n, err)
	}
	if size, _, err := c.SizeOf(ctx, []string{tGrade2, tArt}); err != nil || size != 1500 {
		t.Fatalf("grade 2 and art %d %v", size, err)
	}
	if _, _, err := c.SizeOf(ctx, []string{"f0000000000000000000000000000000"}); err == nil {
		t.Fatal("a topic outside the channel was measured")
	}
	if err := c.Has(ctx, []string{tArt, tGrade1}); err != nil {
		t.Fatal(err)
	}
	if err := c.Has(ctx, []string{"f0000000000000000000000000000000"}); err == nil {
		t.Fatal("Has accepted a stranger")
	}
}

func TestFetchChannelDB(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "good.sqlite3")
	makeChannelDB(t, good, tChannel)
	other := filepath.Join(dir, "other.sqlite3")
	makeChannelDB(t, other, "d0000000000000000000000000000000")
	studio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/content/databases/" + tChannel + ".sqlite3":
			http.ServeFile(w, r, good)
		case "/content/databases/" + Channels[0].ID + ".sqlite3":
			http.ServeFile(w, r, other) // the wrong channel
		default:
			http.NotFound(w, r)
		}
	}))
	defer studio.Close()
	ctx := context.Background()
	dest := filepath.Join(dir, "cache", tChannel+".sqlite3")
	if err := FetchChannelDB(ctx, studio.Client(), studio.URL, tChannel, dest); err != nil {
		t.Fatal(err)
	}
	if StaleChannelDB(dest) {
		t.Fatal("just fetched, already stale")
	}
	wrong := filepath.Join(dir, "cache", "en.sqlite3")
	if err := FetchChannelDB(ctx, studio.Client(), studio.URL, Channels[0].ID, wrong); err == nil || !strings.Contains(err.Error(), "instead of") {
		t.Fatalf("wrong channel accepted: %v", err)
	}
	if _, err := os.Stat(wrong); !os.IsNotExist(err) {
		t.Fatal("wrong channel kept")
	}
	if err := FetchChannelDB(ctx, studio.Client(), studio.URL, "../../etc", wrong); err == nil {
		t.Fatal("a bad ID was fetched")
	}
}

package userdata

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBookmarksFavoritesRecent(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.AddBookmark("kiwix.wikipedia_en_all_maxi", "Water_purification", "Water purification")
	if err != nil {
		t.Fatal(err)
	}
	dup, _ := s.AddBookmark("kiwix.wikipedia_en_all_maxi", "Water_purification", "x")
	if dup.ID != b.ID {
		t.Fatal("duplicate bookmark created")
	}
	for _, bad := range [][2]string{{"../x", "p"}, {"kiwix.a", "../../etc"}, {"kiwix.a", "/abs"}, {"", "p"}, {"kiwix.a", "a\x00b"}} {
		if _, err := s.AddBookmark(bad[0], bad[1], "t"); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	if err := s.SetFavorite("arcana.sample", true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		s.RecordVisit("arcana.sample", "morse_code", "Morse")
	}
	s.RecordVisit("arcana.sample", "q_codes", "Q codes")
	rec, _ := s.Recent()
	if len(rec) != 2 || rec[0].Path != "q_codes" {
		t.Fatalf("recent: %+v", rec)
	}
	if err := s.RemoveBookmark(b.ID); err != nil {
		t.Fatal(err)
	}
	bs, _ := s.Bookmarks()
	if len(bs) != 0 {
		t.Fatal("bookmark not removed")
	}
	if _, err := os.Stat(filepath.Join(dir, "bookmarks.json.bak")); err != nil {
		t.Fatal("previous copy should be kept")
	}
}

func TestNotesAreReadableFilesAndRecoverable(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir, false)
	n, err := s.SaveNote(Note{ResourceID: "arcana.sample", Path: "morse_code", Title: "SOS", Text: "Practice SOS.\nThree short, three long."})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "notes", n.ID+".md"))
	if !strings.Contains(string(data), "resource: arcana.sample") || !strings.Contains(string(data), "Three short") {
		t.Fatalf("note file not human readable:\n%s", data)
	}
	n.Text = "Updated"
	n2, err := s.SaveNote(Note{ID: n.ID, Title: "SOS", Text: "Updated"})
	if err != nil || n2.CreatedAt != n.CreatedAt || n2.ResourceID != "arcana.sample" {
		t.Fatalf("update: %+v %v", n2, err)
	}
	list, _ := s.Notes("arcana.sample")
	if len(list) != 1 || list[0].Text != "Updated" {
		t.Fatalf("list: %+v", list)
	}
	if err := s.DeleteNote(n.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notes", "deleted", n.ID+".md")); err != nil {
		t.Fatal("deleted notes should be recoverable")
	}
	if _, err := s.SaveNote(Note{ID: "../../x", Text: "x"}); err == nil {
		t.Fatal("bad note id accepted")
	}
}

func TestReadOnly(t *testing.T) {
	s, _ := Open(t.TempDir(), true)
	if _, err := s.AddBookmark("a.b", "p", "t"); err != ErrReadOnly {
		t.Fatalf("got %v", err)
	}
}

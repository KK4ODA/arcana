// Package userdata stores what people create in Arcana: bookmarks, notes,
// favorites and reading history. It lives in the library's user/ folder as
// plain JSON and Markdown files, so it is readable without Arcana, travels
// with the drive, and is never touched by content installation, updates or
// garbage collection.
package userdata

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/KK4ODA/arcana/internal/fsutil"
)

// Bookmark points at a page inside an installed resource. Path is relative
// to the resource (e.g. "Ray_Charles"), not to a particular edition, so
// bookmarks keep working after the resource is updated.
type Bookmark struct {
	ID         string    `json:"id"`
	ResourceID string    `json:"resourceId"`
	Path       string    `json:"path"`
	Title      string    `json:"title"`
	CreatedAt  time.Time `json:"createdAt"`
}

// Visit is a reading-history entry.
type Visit struct {
	ResourceID string    `json:"resourceId"`
	Path       string    `json:"path"`
	Title      string    `json:"title"`
	At         time.Time `json:"at"`
}

// Note is a user note, optionally attached to a page.
type Note struct {
	ID         string    `json:"id"`
	ResourceID string    `json:"resourceId,omitempty"`
	Path       string    `json:"path,omitempty"`
	Title      string    `json:"title"`
	Text       string    `json:"text"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// ErrReadOnly is returned when the library cannot be modified.
var ErrReadOnly = errors.New("the library is read-only")

// Limits keep a misbehaving client from filling the drive.
const (
	maxBookmarks = 10000
	maxRecent    = 200
	maxNoteBytes = 1 << 20
	maxNotes     = 20000
)

// Store manages user data under dir (the library's user/ folder).
type Store struct {
	dir      string
	readOnly bool
	mu       sync.Mutex
}

// Open returns a store for dir.
func Open(dir string, readOnly bool) (*Store, error) {
	if !readOnly {
		if err := os.MkdirAll(filepath.Join(dir, "notes"), 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{dir: dir, readOnly: readOnly}, nil
}

type fileDoc[T any] struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Items   []T    `json:"items"`
}

func load[T any](path string) ([]T, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var doc fileDoc[T]
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s is damaged: %w", filepath.Base(path), err)
	}
	return doc.Items, nil
}

func save[T any](path, format string, items []T) error {
	data, err := json.MarshalIndent(fileDoc[T]{Format: format, Version: 1, Items: items}, "", "  ")
	if err != nil {
		return err
	}
	// Keep one previous copy: user data has no other source to rebuild from.
	if _, err := os.Stat(path); err == nil {
		_ = fsutil.CopyFile(path, path+".bak")
	}
	return fsutil.WriteFileAtomic(path, append(data, '\n'), 0o644)
}

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }

var (
	idRe       = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9][a-z0-9._-]*)?$`)
	noteIDRe   = regexp.MustCompile(`^[0-9a-f]{16}$`)
	badPathRe  = regexp.MustCompile(`[\x00-\x1f\x7f]`)
	maxPathLen = 2000
)

func validRef(resourceID, path string) error {
	if resourceID != "" && (!idRe.MatchString(resourceID) || len(resourceID) > 128) {
		return errors.New("invalid resource id")
	}
	if len(path) > maxPathLen || badPathRe.MatchString(path) || strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return errors.New("invalid page path")
	}
	return nil
}

func cleanTitle(s string) string {
	s = strings.TrimSpace(strings.ToValidUTF8(s, ""))
	s = badPathRe.ReplaceAllString(s, " ")
	if len(s) > 300 {
		cut := 300
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	return s
}

// ---------------------------------------------------------------- bookmarks

// Bookmarks returns all bookmarks, newest first.
func (s *Store) Bookmarks() ([]Bookmark, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := load[Bookmark](s.path("bookmarks.json"))
	sort.SliceStable(items, func(i, j int) bool { return items[i].CreatedAt.After(items[j].CreatedAt) })
	return items, err
}

// AddBookmark adds a bookmark unless the same page is already bookmarked.
func (s *Store) AddBookmark(resourceID, path, title string) (Bookmark, error) {
	if s.readOnly {
		return Bookmark{}, ErrReadOnly
	}
	if err := validRef(resourceID, path); err != nil || resourceID == "" {
		return Bookmark{}, errors.New("invalid bookmark")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := load[Bookmark](s.path("bookmarks.json"))
	if err != nil {
		return Bookmark{}, err
	}
	for _, b := range items {
		if b.ResourceID == resourceID && b.Path == path {
			return b, nil
		}
	}
	if len(items) >= maxBookmarks {
		return Bookmark{}, errors.New("too many bookmarks")
	}
	b := Bookmark{ID: newID(), ResourceID: resourceID, Path: path, Title: cleanTitle(title), CreatedAt: time.Now().UTC().Truncate(time.Second)}
	items = append(items, b)
	return b, save(s.path("bookmarks.json"), "arcana-bookmarks", items)
}

// RemoveBookmark deletes a bookmark.
func (s *Store) RemoveBookmark(id string) error {
	if s.readOnly {
		return ErrReadOnly
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := load[Bookmark](s.path("bookmarks.json"))
	if err != nil {
		return err
	}
	out := items[:0]
	found := false
	for _, b := range items {
		if b.ID == id {
			found = true
			continue
		}
		out = append(out, b)
	}
	if !found {
		return errors.New("no such bookmark")
	}
	return save(s.path("bookmarks.json"), "arcana-bookmarks", out)
}

// ---------------------------------------------------------------- history

// Recent returns reading history, newest first.
func (s *Store) Recent() ([]Visit, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return load[Visit](s.path("recent.json"))
}

// RecordVisit adds a page to the history, collapsing repeats.
func (s *Store) RecordVisit(resourceID, path, title string) error {
	if s.readOnly {
		return ErrReadOnly
	}
	if err := validRef(resourceID, path); err != nil || resourceID == "" {
		return errors.New("invalid page")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := load[Visit](s.path("recent.json"))
	if err != nil {
		items = nil // history is not precious; start over if damaged
	}
	v := Visit{ResourceID: resourceID, Path: path, Title: cleanTitle(title), At: time.Now().UTC().Truncate(time.Second)}
	out := []Visit{v}
	for _, it := range items {
		if it.ResourceID == resourceID && it.Path == path {
			continue
		}
		out = append(out, it)
		if len(out) >= maxRecent {
			break
		}
	}
	return save(s.path("recent.json"), "arcana-recent", out)
}

// ClearRecent empties the history.
func (s *Store) ClearRecent() error {
	if s.readOnly {
		return ErrReadOnly
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return save[Visit](s.path("recent.json"), "arcana-recent", nil)
}

// ---------------------------------------------------------------- favorites

// Favorites returns favorite resource IDs.
func (s *Store) Favorites() ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return load[string](s.path("favorites.json"))
}

// SetFavorite adds or removes a resource from favorites.
func (s *Store) SetFavorite(resourceID string, on bool) error {
	if s.readOnly {
		return ErrReadOnly
	}
	if err := validRef(resourceID, ""); err != nil || resourceID == "" {
		return errors.New("invalid resource id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := load[string](s.path("favorites.json"))
	if err != nil {
		return err
	}
	var out []string
	for _, id := range items {
		if id != resourceID {
			out = append(out, id)
		}
	}
	if on {
		out = append(out, resourceID)
	}
	return save(s.path("favorites.json"), "arcana-favorites", out)
}

// ---------------------------------------------------------------- notes

// Notes are stored one per file as user/notes/<id>.md with a small header,
// readable in any text editor.
func (s *Store) notePath(id string) string { return filepath.Join(s.dir, "notes", id+".md") }

// Notes lists notes, most recently updated first. resourceID filters when
// not empty.
func (s *Store) Notes(resourceID string) ([]Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(filepath.Join(s.dir, "notes"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Note
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".md") || !noteIDRe.MatchString(strings.TrimSuffix(name, ".md")) {
			continue
		}
		n, err := readNote(filepath.Join(s.dir, "notes", name))
		if err != nil {
			continue
		}
		if resourceID != "" && n.ResourceID != resourceID {
			continue
		}
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// SaveNote creates (empty ID) or updates a note.
func (s *Store) SaveNote(n Note) (Note, error) {
	if s.readOnly {
		return Note{}, ErrReadOnly
	}
	if err := validRef(n.ResourceID, n.Path); err != nil {
		return Note{}, err
	}
	if len(n.Text) > maxNoteBytes {
		return Note{}, errors.New("note is too long")
	}
	n.Title = cleanTitle(n.Title)
	n.Text = strings.ToValidUTF8(n.Text, "")
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Second)
	if n.ID == "" {
		entries, _ := os.ReadDir(filepath.Join(s.dir, "notes"))
		if len(entries) >= maxNotes {
			return Note{}, errors.New("too many notes")
		}
		n.ID = newID()
		n.CreatedAt = now
	} else {
		if !noteIDRe.MatchString(n.ID) {
			return Note{}, errors.New("invalid note id")
		}
		old, err := readNote(s.notePath(n.ID))
		if err != nil {
			return Note{}, errors.New("no such note")
		}
		n.CreatedAt = old.CreatedAt
		if n.ResourceID == "" {
			n.ResourceID, n.Path = old.ResourceID, old.Path
		}
	}
	n.UpdatedAt = now
	if err := fsutil.WriteFileAtomic(s.notePath(n.ID), encodeNote(n), 0o644); err != nil {
		return Note{}, err
	}
	return n, nil
}

// DeleteNote removes a note. The file is moved to notes/deleted/ rather than
// destroyed, so an accidental deletion can be recovered from the drive.
func (s *Store) DeleteNote(id string) error {
	if s.readOnly {
		return ErrReadOnly
	}
	if !noteIDRe.MatchString(id) {
		return errors.New("invalid note id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	trash := filepath.Join(s.dir, "notes", "deleted")
	if err := os.MkdirAll(trash, 0o755); err != nil {
		return err
	}
	return os.Rename(s.notePath(id), filepath.Join(trash, id+".md"))
}

func encodeNote(n Note) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", n.ID)
	if n.ResourceID != "" {
		fmt.Fprintf(&b, "resource: %s\n", n.ResourceID)
		fmt.Fprintf(&b, "page: %s\n", n.Path)
	}
	fmt.Fprintf(&b, "title: %s\n", strings.ReplaceAll(n.Title, "\n", " "))
	fmt.Fprintf(&b, "created: %s\n", n.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "updated: %s\n", n.UpdatedAt.Format(time.RFC3339))
	b.WriteString("---\n")
	b.WriteString(n.Text)
	if !strings.HasSuffix(n.Text, "\n") {
		b.WriteString("\n")
	}
	return []byte(b.String())
}

func readNote(p string) (Note, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return Note{}, err
	}
	s := string(data)
	if !strings.HasPrefix(s, "---\n") {
		return Note{}, errors.New("missing header")
	}
	end := strings.Index(s[4:], "\n---\n")
	if end < 0 {
		return Note{}, errors.New("unterminated header")
	}
	header, body := s[4:4+end], s[4+end+5:]
	n := Note{Text: strings.TrimSuffix(body, "\n")}
	for _, line := range strings.Split(header, "\n") {
		k, v, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		switch k {
		case "id":
			n.ID = v
		case "resource":
			n.ResourceID = v
		case "page":
			n.Path = v
		case "title":
			n.Title = v
		case "created":
			n.CreatedAt, _ = time.Parse(time.RFC3339, v)
		case "updated":
			n.UpdatedAt, _ = time.Parse(time.RFC3339, v)
		}
	}
	if !noteIDRe.MatchString(n.ID) {
		return Note{}, errors.New("invalid note id")
	}
	return n, nil
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

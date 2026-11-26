package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/content"
)

// An installed film opens from the content origin, and players can seek in
// it: range requests are answered with 206 and the video type.
func TestInstalledFilmPlays(t *testing.T) {
	e := newEnv(t)
	data := []byte("\x00\x00\x00\x18ftypmp42 not really a film, but bytes to seek in")
	sp := filepath.Join(e.app.Lib.Layout.Staging(), "job", "film.mp4.part")
	if err := os.MkdirAll(filepath.Dir(sp), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sp, data, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	res := catalog.Resource{ID: "arcana.film-test", Provider: "arcana", Title: "Test Film (1940)", Version: "1940",
		Format: "video", Category: catalog.CategoryEntertainment,
		Files: []catalog.File{{Name: "film.mp4", Size: int64(len(data)), Mirrors: []catalog.Mirror{{URL: "https://archive.org/download/x/film.mp4"}}}}}
	if _, err := e.app.Content.Publish(context.Background(), res, []content.StagedFile{{Path: sp, Name: "film.mp4", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}}, "test", false); err != nil {
		t.Fatal(err)
	}
	rec := e.do(e.appH, "GET", "/api/library", lanClient, "127.0.0.1:18470", nil, nil)
	var lib struct {
		Resources []installedView `json:"resources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &lib); err != nil {
		t.Fatal(err)
	}
	var open string
	for _, r := range lib.Resources {
		if r.ResourceID == res.ID {
			open = r.OpenPath
		}
	}
	if open != "/files/arcana.film-test/film.mp4" {
		t.Fatalf("open path %q", open)
	}
	rec = e.do(e.content, "GET", open, lanClient, "127.0.0.1:18471", map[string]string{"Range": "bytes=4-11"}, nil)
	if rec.Code != 206 || rec.Body.String() != "ftypmp42" || rec.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf("range request: %d %q %q", rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
	}
}

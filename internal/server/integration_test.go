package server

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/gps"
	"github.com/KK4ODA/arcana/internal/integration"
)

func TestIntegrationAPI(t *testing.T) {
	if kiwixBinary() == "" {
		t.Skip("kiwix-serve not available")
	}
	e := newEnv(t)
	host := "127.0.0.1:18470"
	if _, err := e.app.ImportFile(context.Background(), filepath.Join("..", "..", "testdata", "sample", "arcana-sample_en_2026-11.zim"), app.ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		if _, ok := e.app.Kiwix.Books()["arcana.sample"]; ok {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	get := func(url, tok, remote string) (int, map[string]any) {
		hdr := map[string]string{}
		if tok != "" {
			hdr["Authorization"] = "Bearer " + tok
		}
		rec := e.do(e.appH, "GET", url, remote, host, hdr, nil)
		var m map[string]any
		json.Unmarshal(rec.Body.Bytes(), &m)
		return rec.Code, m
	}

	// Discovery without a token; everything else needs one.
	if code, m := get("/api/integration/v1/", "", local); code != 200 || m["authenticated"] != false || m["version"] != float64(1) {
		t.Fatalf("info: %d %v", code, m)
	}
	if code, _ := get("/api/integration/v1/search?q=distress", "", local); code != 401 {
		t.Fatalf("no token: %d", code)
	}

	// Pairing: the program asks, the operator approves, the program collects.
	rec := e.do(e.appH, "POST", "/api/integration/v1/pair", lanClient, host, map[string]string{"Content-Type": "application/json"},
		strings.NewReader(`{"name":"RadioPost","scopes":["search:read","documents:read","maps:read"]}`))
	var pr struct{ ID, Code, Secret string }
	json.Unmarshal(rec.Body.Bytes(), &pr)
	if rec.Code != 202 || len(pr.Code) != 6 {
		t.Fatalf("pair: %d %s", rec.Code, rec.Body.String())
	}
	if code, m := get("/api/integration/v1/pair/"+pr.ID+"?secret="+pr.Secret, "", lanClient); code != 200 || m["status"] != "pending" {
		t.Fatalf("poll: %d %v", code, m)
	}
	var sess map[string]any
	json.Unmarshal(e.do(e.appH, "GET", "/api/session", local, host, nil, nil).Body.Bytes(), &sess)
	hostHdr := map[string]string{"X-Arcana-Token": sess["token"].(string), "Content-Type": "application/json"}
	if rec := e.do(e.appH, "POST", "/api/integrations/pair/"+pr.ID+"/approve", lanClient, host, hostHdr, nil); rec.Code != 403 {
		t.Fatalf("LAN approved its own request: %d", rec.Code)
	}
	if rec := e.do(e.appH, "POST", "/api/integrations/pair/"+pr.ID+"/approve", local, host, hostHdr, nil); rec.Code != 200 {
		t.Fatalf("approve: %d %s", rec.Code, rec.Body.String())
	}
	_, m := get("/api/integration/v1/pair/"+pr.ID+"?secret="+pr.Secret, "", lanClient)
	tok, _ := m["token"].(string)
	if m["status"] != "approved" || !strings.HasPrefix(tok, "arc1_") {
		t.Fatalf("collect: %v", m)
	}

	// Search returns citations.
	code, m := get("/api/integration/v1/search?q=distress", tok, lanClient)
	results, _ := m["results"].([]any)
	if code != 200 || len(results) == 0 {
		t.Fatalf("search: %d %v", code, m)
	}
	first := results[0].(map[string]any)
	cit := first["citation"].(map[string]any)
	if cit["resourceId"] != "arcana.sample" || cit["license"].(map[string]any)["id"] == "" || strings.Contains(first["snippet"].(string), "<b>") {
		t.Fatalf("result %v", first)
	}
	// Text and excerpt of that page, confined to its resource.
	p := first["path"].(string)
	code, m = get("/api/integration/v1/documents/arcana.sample/text?path="+p, tok, lanClient)
	art, _ := m["article"].(map[string]any)
	if code != 200 || art == nil || !strings.Contains(strings.ToLower(art["text"].(string)), "distress") || strings.Contains(art["text"].(string), "<") {
		t.Fatalf("text: %d %v", code, m)
	}
	code, m = get("/api/integration/v1/documents/arcana.sample/excerpt?max=200&path="+p, tok, lanClient)
	if code != 200 || len(m["excerpt"].(string)) > 210 || !strings.Contains(m["footer"].(string), "license") {
		t.Fatalf("excerpt: %d %v", code, m)
	}
	if code, _ := get("/api/integration/v1/documents/arcana.sample/text?path=/kiwix/content/other_book/A/x", tok, lanClient); code != 400 {
		t.Fatalf("path outside the resource: %d", code)
	}
	if code, _ := get("/api/integration/v1/documents/arcana.sample/text?path=/api/session", tok, lanClient); code != 400 {
		t.Fatalf("path to the API: %d", code)
	}
	// Coordinates.
	code, m = get("/api/integration/v1/maps/reference?lat=49.61&lon=6.13", tok, lanClient)
	if code != 200 || m["maidenhead"] != "JN39bo" {
		t.Fatalf("reference: %d %v", code, m)
	}
	// Scopes are enforced.
	if code, _ := get("/api/integration/v1/location", tok, lanClient); code != 403 {
		t.Fatalf("location without scope: %d", code)
	}
	if code, _ := get("/api/integration/v1/library", tok, lanClient); code != 403 {
		t.Fatalf("library without scope: %d", code)
	}
	// The bearer token is not a session for the internal API.
	rec = e.do(e.appH, "PUT", "/api/settings", lanClient, host, map[string]string{"Authorization": "Bearer " + tok, "X-Arcana-Token": tok}, strings.NewReader(`{}`))
	if rec.Code != 403 {
		t.Fatalf("token used for the internal API: %d", rec.Code)
	}

	// A peer token: library list and file copy with Range.
	_, peerTok, err := e.app.Integrations.Create("Field drive", []string{integration.ScopeLibrary, integration.ScopeLocation})
	if err != nil {
		t.Fatal(err)
	}
	code, m = get("/api/integration/v1/library", peerTok, lanClient)
	if code != 200 || len(m["resources"].([]any)) != 1 {
		t.Fatalf("library: %d %v", code, m)
	}
	pres := m["resources"].([]any)[0].(map[string]any)
	f := pres["files"].([]any)[0].(map[string]any)
	rec = e.do(e.appH, "GET", "/api/integration/v1/files/arcana.sample/"+f["name"].(string), lanClient, host,
		map[string]string{"Authorization": "Bearer " + peerTok, "Range": "bytes=0-3"}, nil)
	if rec.Code != 206 || rec.Body.String() != "ZIM\x04" || rec.Header().Get("X-Arcana-SHA256") != f["sha256"] {
		t.Fatalf("file range: %d %q", rec.Code, rec.Body.String())
	}
	// Location: 404 without a fix, then the fix.
	if code, _ := get("/api/integration/v1/location", peerTok, lanClient); code != 404 {
		t.Fatalf("location without fix: %d", code)
	}
	e.app.GPS.Apply(context.Background(), gps.Settings{Source: "manual", ManualLat: 12.5, ManualLon: -70})
	code, m = get("/api/integration/v1/location", peerTok, lanClient)
	if code != 200 || m["fix"].(map[string]any)["lat"] != 12.5 {
		t.Fatalf("location: %d %v", code, m)
	}
	// Revoked tokens stop working.
	for _, c := range e.app.Integrations.List() {
		e.app.Integrations.Revoke(c.ID)
	}
	if code, _ := get("/api/integration/v1/search?q=distress", tok, lanClient); code != 401 {
		t.Fatalf("revoked: %d", code)
	}
}

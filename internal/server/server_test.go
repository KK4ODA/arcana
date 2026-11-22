package server

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/buildinfo"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/web"
)

type env struct {
	t       *testing.T
	app     *app.App
	srv     *Server
	appH    http.Handler
	content http.Handler
}

func kiwixBinary() string {
	name := "kiwix-serve"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p, _ := filepath.Abs(filepath.Join("..", "..", "third_party", "kiwix", buildinfo.Platform(), "kiwix", name))
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Arcana")
	if _, err := library.Init(root, "Test"); err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(root, app.Options{KiwixBinary: kiwixBinary()})
	if err != nil {
		t.Fatal(err)
	}
	a.Lib.Settings.Update(func(s *library.Settings) error { s.AllowPrivateNetworkDownloads = true; return nil })
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	s := New(a, Config{Port: 18470})
	s.appPort, s.content = 18470, 18471
	return &env{t: t, app: a, srv: s, appH: s.appHandler(), content: s.contentHandler()}
}

func (e *env) do(h http.Handler, method, url, remote, host string, hdr map[string]string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, url, body)
	req.RemoteAddr = remote
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const local = "127.0.0.1:50000"
const lanClient = "192.168.1.50:50000"

func TestSessionAndAccessPolicy(t *testing.T) {
	e := newEnv(t)
	var sess map[string]any
	rec := e.do(e.appH, "GET", "/api/session", local, "127.0.0.1:18470", nil, nil)
	json.Unmarshal(rec.Body.Bytes(), &sess)
	token, _ := sess["token"].(string)
	if token == "" || sess["admin"] != true {
		t.Fatalf("local session: %v", sess)
	}
	rec = e.do(e.appH, "GET", "/api/session", lanClient, "127.0.0.1:18470", nil, nil)
	var remote map[string]any
	json.Unmarshal(rec.Body.Bytes(), &remote)
	if _, ok := remote["token"]; ok || remote["admin"] != false {
		t.Fatalf("network clients must not receive the token: %v", remote)
	}

	cases := []struct {
		name, method, url, remote, host string
		hdr                             map[string]string
		want                            int
	}{
		{"rebinding host rejected", "GET", "/api/status", local, "attacker.example:18470", nil, 421},
		{"local read", "GET", "/api/status", local, "localhost:18470", nil, 200},
		{"lan read allowed", "GET", "/api/library", lanClient, "127.0.0.1:18470", nil, 200},
		{"lan cannot read settings", "GET", "/api/settings", lanClient, "127.0.0.1:18470", nil, 403},
		{"lan cannot list drives", "GET", "/api/setup/volumes", lanClient, "127.0.0.1:18470", nil, 403},
		{"lan cannot mutate even with token", "POST", "/api/downloads", lanClient, "127.0.0.1:18470", map[string]string{"X-Arcana-Token": token}, 403},
		{"local mutation needs token", "POST", "/api/catalog/refresh", local, "127.0.0.1:18470", nil, 403},
		{"cross-site refused", "PUT", "/api/settings", local, "127.0.0.1:18470", map[string]string{"X-Arcana-Token": token, "Sec-Fetch-Site": "cross-site"}, 403},
		{"unknown api", "GET", "/api/nope", local, "127.0.0.1:18470", nil, 404},
	}
	for _, c := range cases {
		rec := e.do(e.appH, c.method, c.url, c.remote, c.host, c.hdr, strings.NewReader("{}"))
		if rec.Code != c.want {
			t.Errorf("%s: got %d want %d (%s)", c.name, rec.Code, c.want, rec.Body.String())
		}
	}
	rec = e.do(e.appH, "PUT", "/api/settings", local, "127.0.0.1:18470",
		map[string]string{"X-Arcana-Token": token, "Content-Type": "application/json"}, strings.NewReader(`{"maxConcurrentDownloads":3}`))
	if rec.Code != 200 || e.app.Lib.Settings.Get().MaxConcurrentDownloads != 3 {
		t.Fatalf("settings update: %d %s", rec.Code, rec.Body.String())
	}
	// Content origin is read-only and also host-checked.
	if rec := e.do(e.content, "POST", "/kiwix/", local, "127.0.0.1:18471", nil, nil); rec.Code != 405 {
		t.Errorf("content POST: %d", rec.Code)
	}
	if rec := e.do(e.content, "GET", "/kiwix/", local, "evil.example", nil, nil); rec.Code != 421 {
		t.Errorf("content host check: %d", rec.Code)
	}
	if csp := e.do(e.appH, "GET", "/", local, "127.0.0.1:18470", nil, nil).Header().Get("Content-Security-Policy"); !strings.Contains(csp, "frame-src http://127.0.0.1:18471") {
		t.Errorf("CSP: %q", csp)
	}
}

// TestInterfaceMakesNoExternalRequests scans the embedded UI for references
// to other hosts. Offline operation must never depend on a CDN, web font or
// remote script.
func TestInterfaceMakesNoExternalRequests(t *testing.T) {
	allowed := map[string]bool{
		"https://example.org/arcana-catalog.json": true, // input placeholder text
	}
	urlRe := regexp.MustCompile(`https?://[^\s'"` + "`" + `)<>]+`)
	err := fs.WalkDir(web.Static, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, _ := fs.ReadFile(web.Static, p)
		for _, u := range urlRe.FindAllString(string(data), -1) {
			if strings.HasPrefix(u, "http://www.w3.org/2000/svg") || allowed[u] {
				continue
			}
			t.Errorf("%s references external URL %s", p, u)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestEndToEndInstallAndRead runs the Phase 1 vertical slice in-process: a
// catalog feed and a download server stand in for the internet, the sample
// archive is downloaded, verified, installed, served and searched.
func TestEndToEndInstallAndRead(t *testing.T) {
	if kiwixBinary() == "" {
		t.Skip("kiwix-serve not available; run scripts/fetch-kiwix.sh")
	}
	sample, err := os.ReadFile(filepath.Join("..", "..", "testdata", "sample", "arcana-sample_en_2026-11.zim"))
	if err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "s.zim", time.Unix(0, 0), strings.NewReader(string(sample)))
	}))
	defer remote.Close()

	e := newEnv(t)
	ctx := context.Background()
	res, err := e.app.Store.GetResource(ctx, "arcana.sample")
	if err != nil {
		t.Fatal(err)
	}
	res.Files[0].Mirrors[0].URL = remote.URL + "/arcana-sample_en_2026-11.zim"
	job, err := e.app.Downloads.Enqueue(ctx, res)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		j, _ := e.app.Store.GetJob(ctx, job.ID)
		if j.State == "completed" {
			break
		}
		if j.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("job %s: %s", j.State, j.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Wait for the archive reader to pick up the new archive.
	var lib struct {
		Resources []installedView `json:"resources"`
	}
	for time.Now().Before(deadline) {
		rec := e.do(e.appH, "GET", "/api/library", local, "127.0.0.1:18470", nil, nil)
		json.Unmarshal(rec.Body.Bytes(), &lib)
		if len(lib.Resources) == 1 && lib.Resources[0].OpenPath != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(lib.Resources) != 1 || lib.Resources[0].OpenPath == "" {
		t.Fatalf("library: %+v", lib.Resources)
	}
	if !strings.Contains(lib.Resources[0].Verification, "sha256") {
		t.Errorf("verification: %q", lib.Resources[0].Verification)
	}
	rec := e.do(e.content, "GET", lib.Resources[0].OpenPath+"phonetic_alphabet", local, "127.0.0.1:18471", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Foxtrot") {
		t.Fatalf("article: %d", rec.Code)
	}
	rec = e.do(e.appH, "GET", "/api/search?q=foxtrot", local, "127.0.0.1:18470", nil, nil)
	var sr struct {
		Total   int `json:"total"`
		Results []struct {
			Title string `json:"title"`
		} `json:"results"`
	}
	json.Unmarshal(rec.Body.Bytes(), &sr)
	if sr.Total == 0 || !strings.Contains(sr.Results[0].Title, "alphabet") {
		t.Fatalf("search: %s", rec.Body.String())
	}
}

func (e *env) token() string {
	var sess map[string]any
	rec := e.do(e.appH, "GET", "/api/session", local, "127.0.0.1:18470", nil, nil)
	json.Unmarshal(rec.Body.Bytes(), &sess)
	tok, _ := sess["token"].(string)
	return tok
}

func TestUserDataAndDocumentsAPI(t *testing.T) {
	e := newEnv(t)
	tok := e.token()
	h := map[string]string{"X-Arcana-Token": tok, "Content-Type": "application/json"}
	// Bookmark from the host; network clients may read but not write, and
	// cannot read history or notes.
	rec := e.do(e.appH, "POST", "/api/user/bookmarks", local, "127.0.0.1:18470", h,
		strings.NewReader(`{"resourceId":"arcana.sample","path":"morse_code","title":"Morse"}`))
	if rec.Code != 200 {
		t.Fatalf("bookmark: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(e.appH, "GET", "/api/user/bookmarks", lanClient, "127.0.0.1:18470", nil, nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "morse_code") {
		t.Fatalf("LAN bookmark read: %d", rec.Code)
	}
	for _, p := range []string{"/api/user/recent", "/api/user/notes"} {
		if rec := e.do(e.appH, "GET", p, lanClient, "127.0.0.1:18470", nil, nil); rec.Code != 403 {
			t.Errorf("%s must be private to the host: %d", p, rec.Code)
		}
	}
	rec = e.do(e.appH, "POST", "/api/user/notes", local, "127.0.0.1:18470", h,
		strings.NewReader(`{"resourceId":"arcana.sample","path":"q_codes","title":"QSL cards","text":"Exchange after each contact."}`))
	if rec.Code != 200 {
		t.Fatalf("note: %d %s", rec.Code, rec.Body.String())
	}

	// Upload a user document; it becomes searchable.
	uh := map[string]string{"X-Arcana-Token": tok, "X-Filename": "evacuation%20plan.txt"}
	rec = e.do(e.appH, "POST", "/api/user/documents", local, "127.0.0.1:18470", uh,
		strings.NewReader("Assemble at the fire station on Elm Street."))
	if rec.Code != 200 {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(e.appH, "POST", "/api/user/documents", local, "127.0.0.1:18470",
		map[string]string{"X-Arcana-Token": tok, "X-Filename": "..%2F..%2Fescape.txt"}, strings.NewReader("x")); rec.Code == 200 {
		t.Fatal("traversal file name accepted")
	}
	var found bool
	for i := 0; i < 100 && !found; i++ {
		time.Sleep(50 * time.Millisecond)
		rec = e.do(e.appH, "GET", "/api/search?q=fire+station", local, "127.0.0.1:18470", nil, nil)
		found = strings.Contains(rec.Body.String(), "evacuation plan.txt")
	}
	if !found {
		t.Fatalf("uploaded document not searchable: %s", rec.Body.String())
	}
	// Served from the content origin with a sandbox CSP.
	rec = e.do(e.content, "GET", "/userdocs/evacuation%20plan.txt", local, "127.0.0.1:18471", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("serve user doc: %d %v", rec.Code, rec.Header())
	}
	// HTML user documents are downloaded, never rendered.
	e.do(e.appH, "POST", "/api/user/documents", local, "127.0.0.1:18470",
		map[string]string{"X-Arcana-Token": tok, "X-Filename": "page.html"}, strings.NewReader("<script>alert(1)</script>"))
	rec = e.do(e.content, "GET", "/userdocs/page.html", local, "127.0.0.1:18471", nil, nil)
	if rec.Header().Get("Content-Disposition") != "attachment" {
		t.Fatal("HTML document must not render inline")
	}
}

func TestArchivePagesReportLocationToReader(t *testing.T) {
	if kiwixBinary() == "" {
		t.Skip("kiwix-serve not available")
	}
	e := newEnv(t)
	if _, err := e.app.ImportFile(context.Background(), filepath.Join("..", "..", "testdata", "sample", "arcana-sample_en_2026-11.zim"), app.ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	var path string
	for i := 0; i < 100 && path == ""; i++ {
		time.Sleep(50 * time.Millisecond)
		if b, ok := e.app.Kiwix.Books()["arcana.sample"]; ok {
			path = b.ContentPath
		}
	}
	rec := e.do(e.content, "GET", path+"morse_code", local, "127.0.0.1:18471", nil, nil)
	if !strings.Contains(rec.Body.String(), `<script src="/_arcana/frame.js"></script></head>`) {
		t.Fatalf("reader script not injected: %s", rec.Body.String()[:300])
	}
	rec = e.do(e.content, "GET", "/_arcana/frame.js", local, "127.0.0.1:18471", nil, nil)
	if !strings.Contains(rec.Body.String(), "arcana:location") {
		t.Fatal("frame script missing")
	}
}

package apps

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseFileName(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want FileName
		bad  bool
	}{
		{in: "cyberchef_any_10.24.0.zip", want: FileName{"cyberchef", "any", "10.24.0.zip"}},
		{in: "pypi_linux-amd64_lxml-5.3.2-cp311-cp311-manylinux_2_17_x86_64.whl", want: FileName{"pypi", "linux-amd64", "lxml-5.3.2-cp311-cp311-manylinux_2_17_x86_64.whl"}},
		{in: "cyberchef-10.24.0.zip", bad: true},
		{in: "a__b", bad: true},
	} {
		got, err := ParseFileName(tc.in)
		if tc.bad {
			if err == nil {
				t.Errorf("%s: accepted", tc.in)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: got %+v, %v", tc.in, got, err)
		}
	}
}

func TestSelect(t *testing.T) {
	files := []Packed{
		{Name: "server_linux-amd64_1.tar.gz"}, {Name: "server_linux-arm64_1.tar.gz"},
		{Name: "site_any_1.tar.gz"}, {Name: "tool_linux-amd64_1.zip"},
	}
	got, err := Select(files, "linux-amd64")
	if err != nil || len(got) != 3 {
		t.Fatalf("linux-amd64: %v %v", got, err)
	}
	// linux-arm64 has a server but no tool.
	if _, err := Select(files, "linux-arm64"); !errors.Is(err, ErrNotForPlatform) {
		t.Fatalf("linux-arm64: %v", err)
	}
	if _, err := Select(files, "windows-amd64"); !errors.Is(err, ErrNotForPlatform) {
		t.Fatalf("windows: %v", err)
	}
	if p := Platforms(files); len(p) != 1 || p[0] != "linux-amd64" {
		t.Fatalf("platforms %v", p)
	}
	if p := Platforms([]Packed{{Name: "site_any_1.zip"}}); len(p) != 0 {
		t.Fatalf("platforms of a web app %v", p)
	}
}

type entryDef struct {
	name, body, link string
	typ              byte
	mode             int64
}

func writeTarGz(t *testing.T, path string, entries []entryDef) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Typeflag: typ, Linkname: e.link, Mode: mode, Size: int64(len(e.body))}); err != nil {
			t.Fatal(err)
		}
		io.WriteString(tw, e.body)
	}
	tw.Close()
	gz.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gzipped(s string) string {
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	io.WriteString(z, s)
	z.Close()
	return b.String()
}

func packed(t *testing.T, path, name string) Packed {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return Packed{Name: name, Path: path, SHA256: hex.EncodeToString(sum[:])}
}

func TestUnpack(t *testing.T) {
	src := t.TempDir()
	layer := filepath.Join(src, "layer.tar.gz")
	writeTarGz(t, layer, []entryDef{
		{name: "./usr/share/site/", typ: tar.TypeDir},
		{name: "./usr/share/site/index.html", body: "<h1>hi</h1>"},
		{name: "./usr/share/site/.wh.old.js"},
		{name: "./usr/share/site/app.js.gz", body: gzipped("console.log(1)")},
		{name: "./usr/share/site/bin", body: "#!/bin/sh", mode: 0o755},
		{name: "./usr/share/site/latest", typ: tar.TypeSymlink, link: "index.html"},
	})
	zipPath := filepath.Join(src, "tool.zip")
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("dist/tool.txt")
	io.WriteString(w, "tool")
	zw.Close()
	os.WriteFile(zipPath, zb.Bytes(), 0o644)
	whl := filepath.Join(src, "pkg.whl")
	os.WriteFile(whl, []byte("wheel"), 0o644)

	spec := &Spec{ID: "test", Parts: map[string]Part{"site": {Gunzip: true}, "pypi": {Copy: true}}}
	files := []Packed{packed(t, layer, "site_any_1.tar.gz"), packed(t, zipPath, "tool_any_1.zip"), packed(t, whl, "pypi_any_pkg-1-py3-none-any.whl")}
	root := t.TempDir()
	dir, err := Unpack(spec, files, root)
	if err != nil {
		t.Fatal(err)
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		return string(b)
	}
	if read("site/usr/share/site/index.html") != "<h1>hi</h1>" || read("site/usr/share/site/app.js") != "console.log(1)" ||
		read("tool/dist/tool.txt") != "tool" || read("pypi/pkg-1-py3-none-any.whl") != "wheel" {
		t.Fatal("unexpected contents")
	}
	if _, err := os.Stat(filepath.Join(dir, "site/usr/share/site/.wh.old.js")); err == nil {
		t.Error("whiteout entry was written")
	}
	if read("site/usr/share/site/latest") != "<h1>hi</h1>" {
		t.Error("symbolic link not kept")
	}
	if st, _ := os.Stat(filepath.Join(dir, "site/usr/share/site/bin")); st == nil || (st.Mode()&0o111 == 0 && os.PathSeparator == '/') {
		t.Error("executable bit lost")
	}
	// A second call reuses the folder.
	again, err := Unpack(spec, files, root)
	if err != nil || again != dir {
		t.Fatalf("second unpack: %s %v", again, err)
	}
	// A new version unpacks beside it, and CleanOld removes the old one.
	writeTarGz(t, layer, []entryDef{{name: "usr/share/site/index.html", body: "v2"}})
	files[0] = packed(t, layer, "site_any_2.tar.gz")
	dir2, err := Unpack(spec, files, root)
	if err != nil || dir2 == dir {
		t.Fatalf("new version: %s %v", dir2, err)
	}
	CleanOld(spec, root, dir2)
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("old version kept")
	}
}

func TestUnpackRefusesEscapes(t *testing.T) {
	for _, e := range []entryDef{
		{name: "../evil", body: "x"},
		{name: "/abs", body: "x"},
		{name: "a/link", typ: tar.TypeSymlink, link: "../../outside"},
		{name: "a/abslink", typ: tar.TypeSymlink, link: "/etc/passwd"},
		{name: "a/hard", typ: tar.TypeLink, link: "../x"},
	} {
		src := filepath.Join(t.TempDir(), "x.tar.gz")
		writeTarGz(t, src, []entryDef{e})
		spec := &Spec{ID: "bad"}
		root := t.TempDir()
		if _, err := Unpack(spec, []Packed{packed(t, src, "p_any_1.tar.gz")}, root); err == nil {
			t.Errorf("%s accepted", e.name)
		}
		if entries, _ := os.ReadDir(root); len(entries) != 0 {
			t.Errorf("%s left %v", e.name, entries)
		}
	}
}

func newReadyManager(t *testing.T, spec *Spec, site map[string]string) *Manager {
	t.Helper()
	src := filepath.Join(t.TempDir(), "site.tar.gz")
	var entries []entryDef
	for name, body := range site {
		entries = append(entries, entryDef{name: name, body: body})
	}
	writeTarGz(t, src, entries)
	registry[spec.ID] = spec
	t.Cleanup(func() { delete(registry, spec.ID) })
	m := &Manager{Cache: t.TempDir(), Platform: "linux-amd64"}
	m.Sync([]Installed{{ResourceID: spec.ResourceID(), Version: "1", Files: []Packed{packed(t, src, "site_any_1.tar.gz")}}})
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := m.Status()
		if len(st) == 1 && st[0].State == "ready" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not ready: %+v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return m
}

func TestWebHandler(t *testing.T) {
	spec := &Spec{ID: "webtest", Title: "Web test", Kind: KindWeb, Slot: 90, Parts: map[string]Part{"site": {}},
		Root: "site/dist", Index: "main.html", SPA: true}
	m := newReadyManager(t, spec, map[string]string{
		"dist/main.html": "<p>main</p>", "dist/app.js": "js", "secret.txt": "outside the root",
	})
	h := m.WebHandler(spec)
	get := func(method, p string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, p, nil))
		return rec
	}
	if r := get("GET", "/"); r.Code != 200 || !strings.Contains(r.Body.String(), "main") {
		t.Fatalf("index: %d %q", r.Code, r.Body.String())
	}
	if r := get("GET", "/main.html"); r.Code != 200 || r.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("index by name: %d %q", r.Code, r.Header().Get("Cache-Control"))
	}
	if r := get("GET", "/app.js"); r.Code != 200 || r.Body.String() != "js" {
		t.Fatalf("app.js: %d", r.Code)
	}
	r := get("GET", "/tools/uuid")
	if r.Code != 200 || !strings.Contains(r.Body.String(), "main") {
		t.Fatalf("browser route: %d", r.Code)
	}
	if csp := r.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "connect-src 'self'") || strings.Contains(csp, "https:") {
		t.Fatalf("CSP %q", csp)
	}
	for _, p := range []string{"/missing.js", "/../secret.txt", "/%2e%2e/secret.txt"} {
		if r := get("GET", p); r.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, r.Code)
		}
	}
	if r := get("POST", "/"); r.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", r.Code)
	}
}

func TestWebHandlerNotReady(t *testing.T) {
	spec := &Spec{ID: "absent", Title: "Absent", Kind: KindWeb, Slot: 91}
	m := &Manager{Cache: t.TempDir()}
	rec := httptest.NewRecorder()
	m.WebHandler(spec).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "not installed") {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestSyncMarksMissingPlatform(t *testing.T) {
	spec := &Spec{ID: "nativetest", Title: "Native", Kind: KindWeb, Slot: 92, Parts: map[string]Part{}}
	registry[spec.ID] = spec
	defer delete(registry, spec.ID)
	m := &Manager{Cache: t.TempDir(), Platform: "darwin-arm64"}
	m.Sync([]Installed{{ResourceID: spec.ResourceID(), Files: []Packed{{Name: "bin_linux-amd64_1.tar.gz"}}}})
	st := m.Status()
	if len(st) != 1 || st[0].State != "unavailable" || !strings.Contains(st[0].Problem, "darwin-arm64") {
		t.Fatalf("%+v", st)
	}
	m.Sync(nil)
	if len(m.Status()) != 0 {
		t.Fatal("removed app still listed")
	}
}

func TestRegistry(t *testing.T) {
	seen := map[int]string{}
	for _, s := range All() {
		if o, dup := seen[s.Slot]; dup {
			t.Fatalf("%s and %s share a slot", s.ID, o)
		}
		seen[s.Slot] = s.ID
		if s.Kind == KindWeb && s.Root == "" {
			t.Errorf("%s has no root", s.ID)
		}
		if _, ok := Lookup(s.ResourceID()); !ok {
			t.Errorf("%s: lookup by resource ID", s.ID)
		}
	}
}

func TestWebHandlerRewritesAndExtraFiles(t *testing.T) {
	spec := &Spec{ID: "rewritetest", Title: "Rewrite test", Kind: KindWeb, Slot: 97, Parts: map[string]Part{"site": {}},
		Root:     "site/web",
		Rewrites: []Rewrite{{Path: "/index.html", Old: "cdn.example", New: "/local"}},
		Extra:    map[string]string{"/fonts/a.woff2": "site/font.woff2"}}
	m := newReadyManager(t, spec, map[string]string{"web/index.html": "<script src=cdn.example/x.js>", "font.woff2": "font"})
	h := m.WebHandler(spec)
	for p, want := range map[string]string{"/": "<script src=/local/x.js>", "/index.html": "<script src=/local/x.js>", "/fonts/a.woff2": "font"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != 200 || rec.Body.String() != want {
			t.Errorf("%s: %d %q", p, rec.Code, rec.Body.String())
		}
	}
	// The file on disk is unchanged.
	dir, _, _ := m.ready(spec.ID)
	if b, _ := os.ReadFile(filepath.Join(dir, "site", "web", "index.html")); !strings.Contains(string(b), "cdn.example") {
		t.Error("file on disk was changed")
	}
}

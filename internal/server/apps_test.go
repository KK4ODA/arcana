package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/KK4ODA/arcana/internal/apps"
	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/content"
)

// installApp publishes a stand-in for an app's resource, built from files.
func installApp(t *testing.T, e *env, id, fileName string, files map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, _ := zw.Create(name)
		io.WriteString(w, body)
	}
	zw.Close()
	sp := filepath.Join(e.app.Lib.Layout.Staging(), "job-"+id, fileName+".part")
	os.MkdirAll(filepath.Dir(sp), 0o755)
	if err := os.WriteFile(sp, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	res := catalog.Resource{ID: apps.ResourcePrefix + id, Provider: "arcana", Title: id, Version: "1", Format: "app",
		Category: catalog.CategoryComputing,
		Files:    []catalog.File{{Name: fileName, Size: int64(buf.Len()), Mirrors: []catalog.Mirror{{URL: "https://example.org/" + fileName}}}}}
	if _, err := e.app.Content.Publish(context.Background(), res, []content.StagedFile{{Path: sp, Name: fileName, Size: int64(buf.Len()), SHA256: hex.EncodeToString(sum[:])}}, "test", false); err != nil {
		t.Fatal(err)
	}
}

func waitAppsReady(t *testing.T, e *env) []appView {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		rec := e.do(e.appH, "GET", "/api/apps", lanClient, "arcana.local:18470", nil, nil)
		var body struct {
			Apps []appView `json:"apps"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		ready := len(body.Apps) > 0
		for _, a := range body.Apps {
			ready = ready && a.State == "ready"
		}
		if ready {
			return body.Apps
		}
		if time.Now().After(deadline) {
			t.Fatalf("apps not ready: %+v", body.Apps)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// An installed web app is listed with its own address and served on its
// own origin, without Arcana's cookies, to known host names only.
func TestWebAppOrigin(t *testing.T) {
	e := newEnv(t)
	installApp(t, e, "cyberchef", "cyberchef_any_10.24.0.zip", map[string]string{
		"CyberChef_v10.24.0.html": "<title>CyberChef</title>", "assets/main.js": "js",
	})
	list := waitAppsReady(t, e)
	if len(list) != 1 || list[0].URL != "http://arcana.local:18473/" || list[0].Secure {
		t.Fatalf("listing %+v", list)
	}
	spec, _ := apps.Lookup("cyberchef")
	h := e.srv.appOriginHandler(spec)
	rec := e.do(h, "GET", "/", lanClient, "arcana.local:18473", map[string]string{"Cookie": sessionCookie + "=secret; other=1"}, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "CyberChef") {
		t.Fatalf("index: %d %q", rec.Code, rec.Body.String())
	}
	if csp := rec.Header().Get("Content-Security-Policy"); csp != apps.WebCSP {
		t.Fatalf("CSP %q", csp)
	}
	if rec := e.do(h, "GET", "/", lanClient, "evil.example:18473", nil, nil); rec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("unknown host: %d", rec.Code)
	}
	// Another app's origin answers that it is not installed.
	other, _ := apps.Lookup("excalidraw")
	if rec := e.do(e.srv.appOriginHandler(other), "GET", "/", local, "127.0.0.1:18475", nil, nil); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("absent app: %d", rec.Code)
	}

	// The real listener: opened while the app is installed, closed after.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := l.Addr().(*net.TCPAddr).Port
	l.Close()
	e.srv.appPort = base
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.srv.serveApps(ctx)
	url := "http://127.0.0.1:" + strconv.Itoa(spec.Port(base)) + "/assets/main.js"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("app port not open: %v", err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "js" {
		t.Fatalf("app port served %q", b)
	}
	if err := e.app.Content.Remove(context.Background(), spec.ResourceID()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		c, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(spec.Port(base)), time.Second)
		if err != nil {
			break
		}
		c.Close()
		if time.Now().After(deadline) {
			t.Fatal("app port still open after the app was removed")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

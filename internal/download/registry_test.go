package download

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestRegistryAuth(t *testing.T) {
	const digest = "sha256:9f511868bea49e4bf8086c51cbb5dd908a8ecea8fd98cb65c41881ff500a13d9"
	var tokens, blobs atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			tokens.Add(1)
			if r.URL.Query().Get("scope") != "repository:excalidraw/excalidraw:pull" || r.URL.Query().Get("service") != "registry.test" {
				http.Error(w, "bad scope", 400)
				return
			}
			io.WriteString(w, `{"token":"anon","expires_in":300}`)
		case r.URL.Path == "/v2/excalidraw/excalidraw/blobs/"+digest:
			blobs.Add(1)
			if r.Header.Get("Authorization") != "Bearer anon" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="`+srv.URL+`/token",service="registry.test",scope="repository:excalidraw/excalidraw:pull"`)
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			io.WriteString(w, "layer")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client := &http.Client{Transport: &registryAuth{base: srv.Client().Transport}}
	for i := 0; i < 2; i++ {
		resp, err := client.Get(srv.URL + "/v2/excalidraw/excalidraw/blobs/" + digest)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || string(body) != "layer" {
			t.Fatalf("got %d %q", resp.StatusCode, body)
		}
	}
	// One token, reused; the first request was refused once.
	if tokens.Load() != 1 || blobs.Load() != 3 {
		t.Fatalf("tokens %d, blob requests %d", tokens.Load(), blobs.Load())
	}
}

func TestParseBearer(t *testing.T) {
	for h, ok := range map[string]bool{
		`Bearer realm="https://auth.docker.io/token",service="registry.docker.io",scope="repository:a/b:pull"`: true,
		`Bearer realm="http://insecure/token",scope="repository:a/b:pull"`:                                     false,
		`Bearer realm="https://auth/token",scope="repository:a/b:push,pull"`:                                   false,
		`Bearer realm="https://auth/token",scope="repository:a/b:push"`:                                        false,
		`Basic realm="x"`: false,
	} {
		if _, got := parseBearer(h); got != ok {
			t.Errorf("%s: got %v", h, got)
		}
	}
	if !blobPath.MatchString("/v2/dani-garcia/vaultwarden/blobs/sha256:" + strings.Repeat("a", 64)) {
		t.Error("ghcr blob path refused")
	}
	if blobPath.MatchString("/v2/x/manifests/latest") {
		t.Error("manifest path accepted")
	}
}

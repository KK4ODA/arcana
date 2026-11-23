package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/translate"
	"github.com/klauspost/compress/zstd"
)

func TestTranslationFilesServedVerified(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	enc, _ := zstd.NewWriter(nil)
	plain := map[string][]byte{
		"engine": append([]byte("\x00asm\x01\x00\x00\x00"), bytes.Repeat([]byte{7}, 4000)...),
		"model":  bytes.Repeat([]byte("model "), 3000),
		"lex":    bytes.Repeat([]byte("lex "), 500),
		"vocab":  bytes.Repeat([]byte("vocab "), 300),
	}
	packed := map[string][]byte{}
	for k, v := range plain {
		packed[k] = enc.EncodeAll(v, nil)
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := packed[strings.TrimPrefix(r.URL.Path, "/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "f.zst", time.Unix(0, 0), bytes.NewReader(data))
	}))
	defer remote.Close()
	hash := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	file := func(name, key string) catalog.File {
		return catalog.File{Name: name, Size: int64(len(packed[key])), Hashes: map[string]string{"sha256": hash(packed[key])},
			Mirrors: []catalog.Mirror{{URL: remote.URL + "/" + key}}}
	}
	lic := catalog.License{ID: "MPL-2.0", Redistribution: "unknown"}
	engine := catalog.Resource{ID: translate.EngineResourceID, Provider: "arcana", Title: "Engine", Category: "reference", Version: "4.0",
		Languages: []string{"mul"}, Format: translate.EngineFormat, License: lic, Files: []catalog.File{file("engine.wasm.zst", "engine")},
		ProviderRef: map[string]string{"decompressedHash": hash(plain["engine"]), "decompressedSize": fmt.Sprint(len(plain["engine"]))}}
	var manifest []translate.ModelFile
	for _, k := range []string{"model", "lex", "vocab"} {
		manifest = append(manifest, translate.ModelFile{Name: k + ".enes.zst", FileType: k, From: "en", To: "es",
			DecompressedHash: hash(plain[k]), DecompressedSize: int64(len(plain[k])), Gemm: map[bool]string{true: "int8shiftAlphaAll"}[k == "model"]})
	}
	mj, _ := json.Marshal(manifest)
	pack := catalog.Resource{ID: "translations.es", Provider: "translations", Title: "Spanish", Category: "reference", Version: "3.0",
		Languages: []string{"es", "en"}, Format: translate.Format, License: lic,
		Files:       []catalog.File{file("model.enes.zst", "model"), file("lex.enes.zst", "lex"), file("vocab.enes.zst", "vocab")},
		ProviderRef: map[string]string{"files": string(mj), "directions": "en-es"}}
	for _, r := range []catalog.Resource{engine, pack} {
		if err := r.Validate(); err != nil {
			t.Fatal(err)
		}
		if _, err := e.app.Downloads.Enqueue(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, err1 := e.app.Store.GetInstalled(ctx, engine.ID)
		_, err2 := e.app.Store.GetInstalled(ctx, pack.ID)
		if err1 == nil && err2 == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not installed: %v %v", err1, err2)
		}
		time.Sleep(50 * time.Millisecond)
	}

	host := "127.0.0.1:18470"
	var info struct {
		Engine    bool   `json:"engine"`
		EngineURL string `json:"engineUrl"`
		Languages []struct {
			Code  string `json:"code"`
			Files []struct {
				URL  string `json:"url"`
				Gemm string `json:"gemm"`
			} `json:"files"`
		} `json:"languages"`
	}
	json.Unmarshal(e.do(e.appH, "GET", "/api/translate", lanClient, host, nil, nil).Body.Bytes(), &info)
	if !info.Engine || len(info.Languages) != 1 || info.Languages[0].Code != "es" || len(info.Languages[0].Files) != 3 || info.Languages[0].Files[0].Gemm == "" {
		t.Fatalf("info: %+v", info)
	}
	rec := e.do(e.appH, "GET", info.EngineURL, lanClient, host, nil, nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/wasm" || !bytes.Equal(rec.Body.Bytes(), plain["engine"]) {
		t.Fatalf("engine: %d %s %d bytes", rec.Code, rec.Header().Get("Content-Type"), rec.Body.Len())
	}
	if csp := rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "'wasm-unsafe-eval'") || strings.Contains(csp, "'unsafe-eval'") {
		t.Fatalf("CSP: %s", csp)
	}
	modelURL := info.Languages[0].Files[0].URL
	rec = e.do(e.appH, "GET", modelURL, lanClient, host, nil, nil)
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), plain["model"]) {
		t.Fatalf("model: %d", rec.Code)
	}
	rec = e.do(e.appH, "GET", modelURL, lanClient, host, map[string]string{"If-None-Match": rec.Header().Get("ETag")}, nil)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("conditional request: %d", rec.Code)
	}
	if rec := e.do(e.appH, "GET", "/translate/model/translations.es/other.zst", lanClient, host, nil, nil); rec.Code != 404 {
		t.Fatalf("unknown file: %d", rec.Code)
	}
	// A file damaged on the drive is refused, not served.
	in, _ := e.app.Store.GetInstalled(ctx, pack.ID)
	p, _ := e.app.Content.FilePath(in, in.Files[0])
	os.WriteFile(p, enc.EncodeAll([]byte("something else entirely"), nil), 0o644)
	if rec := e.do(e.appH, "GET", modelURL, lanClient, host, nil, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("damaged model served: %d", rec.Code)
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// cache keeps every network answer on disk, so a run can be repeated (and
// reviewed) without fetching the registry again.
type cache struct {
	dir    string
	client *http.Client
}

func (c *cache) path(parts ...string) string {
	p := filepath.Join(append([]string{c.dir}, parts...)...)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	return p
}

func (c *cache) readJSON(p string, v any) bool {
	b, err := os.ReadFile(p)
	return err == nil && json.Unmarshal(b, v) == nil
}

func (c *cache) writeJSON(p string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}

func (c *cache) get(ctx context.Context, url string, accept string, max int64) ([]byte, int, error) {
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt*attempt) * time.Second)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, 0, err
		}
		req.Header.Set("User-Agent", userAgent)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		resp, err := c.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 500 || resp.StatusCode == 429 {
			lastErr = fmt.Errorf("GET %s: %s", url, resp.Status)
			continue
		}
		if int64(len(b)) > max {
			return nil, resp.StatusCode, fmt.Errorf("GET %s: more than %d bytes", url, max)
		}
		return b, resp.StatusCode, nil
	}
	return nil, 0, lastErr
}

const userAgent = "arcana-ollama-models (+https://github.com/KK4ODA/arcana)"

// manifest is an OCI image manifest as the Ollama registry serves it.
type manifest struct {
	Layers []layer `json:"layers"`
}

type layer struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
	Size      int64  `json:"size"`
}

const registry = "https://registry.ollama.ai/v2/library/"

// fetchManifest returns a tag's manifest, or nil when the registry does not
// have it.
func (c *cache) fetchManifest(ctx context.Context, model, tag string) (*manifest, error) {
	p := c.path("manifests", model, tag+".json")
	var m manifest
	if c.readJSON(p, &m) {
		return &m, nil
	}
	b, status, err := c.get(ctx, registry+model+"/manifests/"+tag,
		"application/vnd.docker.distribution.manifest.v2+json", 8<<20)
	if err != nil {
		return nil, err
	}
	if status == 404 {
		return nil, nil
	}
	if status != 200 {
		return nil, fmt.Errorf("manifest %s:%s: HTTP %d", model, tag, status)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("manifest %s:%s: %w", model, tag, err)
	}
	return &m, os.WriteFile(p, b, 0o644)
}

func blobURL(model, digest string) string {
	return registry + model + "/blobs/" + digest
}

// fetchBlob returns a small blob (license, template, parameters).
func (c *cache) fetchBlob(ctx context.Context, model, digest string) ([]byte, error) {
	p := c.path("blobs", strings.TrimPrefix(digest, "sha256:"))
	if b, err := os.ReadFile(p); err == nil {
		return b, nil
	}
	b, status, err := c.get(ctx, blobURL(model, digest), "", 4<<20)
	if err != nil {
		return nil, err
	}
	if status != 200 {
		return nil, fmt.Errorf("blob %s: HTTP %d", digest, status)
	}
	return b, os.WriteFile(p, b, 0o644)
}

// fetchHeader streams the start of a model blob until its GGUF header is
// parsed, then closes the connection. The header bytes are kept for the load
// test, which rebuilds the file sparsely around them.
func (c *cache) fetchHeader(ctx context.Context, model, digest string) (*ggufInfo, error) {
	hex := strings.TrimPrefix(digest, "sha256:")
	p := c.path("heads", hex+".json")
	var g ggufInfo
	if c.readJSON(p, &g) {
		return &g, nil
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		info, raw, err := c.streamHeader(ctx, blobURL(model, digest))
		if err == nil {
			if err := os.WriteFile(c.path("heads", hex+".bin"), raw, 0o644); err != nil {
				return nil, err
			}
			return info, c.writeJSON(p, info)
		}
		lastErr = err
		if errors.Is(err, errNotGGUF) {
			break
		}
		time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
	}
	return nil, lastErr
}

var errNotGGUF = errors.New("not a GGUF file")

func (c *cache) streamHeader(ctx context.Context, url string) (*ggufInfo, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	var raw bytes.Buffer
	// Headers of the largest vocabularies are tens of megabytes.
	body := io.TeeReader(io.LimitReader(resp.Body, 256<<20), &raw)
	info, err := parseGGUF(body)
	if err != nil {
		if raw.Len() >= 4 && string(raw.Bytes()[:4]) != "GGUF" {
			return nil, nil, errNotGGUF
		}
		return nil, nil, err
	}
	if int64(raw.Len()) < info.DataOffset {
		// The parser's buffer read ahead less than the padding; read the rest.
		if _, err := io.CopyN(io.Discard, body, info.DataOffset-int64(raw.Len())); err != nil {
			return nil, nil, err
		}
	}
	return info, raw.Bytes()[:info.DataOffset], nil
}

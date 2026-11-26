package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// loadResult records whether llamafile loaded a model file.
type loadResult struct {
	Engine  string  `json:"engine"`
	OK      bool    `json:"ok"`
	Error   string  `json:"error,omitempty"`
	Seconds float64 `json:"seconds"`
}

// loadTest starts llamafile's server on a sparse copy of the model: the real
// GGUF header followed by zeros up to the real size. llama.cpp validates the
// architecture, every tensor's name, shape and type and the file's length
// while loading, so this answers "can this engine load the file" without
// downloading the weights. Warm-up is off because zero weights are not worth
// computing with; weights are memory-mapped and never read.
func loadTest(ctx context.Context, engine, engineVersion, header string, size int64, work string) (res loadResult) {
	res = loadResult{Engine: engineVersion}
	start := time.Now()
	defer func() { res.Seconds = time.Since(start).Seconds() }()
	raw, err := os.ReadFile(header)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		res.Error = err.Error()
		return res
	}
	work, err = filepath.Abs(work)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	model := filepath.Join(work, "model.gguf")
	defer os.Remove(model)
	if err := os.WriteFile(model, raw, 0o644); err != nil {
		res.Error = err.Error()
		return res
	}
	if err := os.Truncate(model, size); err != nil {
		res.Error = err.Error()
		return res
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		res.Error = err.Error()
		return res
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	// llamafile is an "actually portable executable"; where the kernel does
	// not recognise the format, the shell starts it.
	cmd := exec.CommandContext(ctx, "/bin/sh", engine, "--server", "-m", model, "--host", "127.0.0.1", "--port", strconv.Itoa(port),
		"-c", "4096", "--gpu", "disable", "--no-repack", "--nologo", "-np", "1", "--no-warmup")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stderr
	cmd.Dir = work
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		res.Error = err.Error()
		return res
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	defer func() {
		killGroup(cmd)
		<-done
	}()
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	for {
		select {
		case <-done:
			res.Error = lastLines(stderr.String(), 3)
			return res
		case <-ctx.Done():
			res.Error = "timed out loading: " + lastLines(stderr.String(), 2)
			return res
		case <-time.After(300 * time.Millisecond):
		}
		resp, err := client.Get(base + "/health")
		if err != nil {
			continue
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			continue
		}
		res.OK = true
		return res
	}
}

func lastLines(s string, n int) string {
	var keep []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		// llama.cpp's own reason, not the lines that repeat "failed".
		if strings.Contains(l, "error loading model") || strings.Contains(l, "unknown model architecture") ||
			strings.Contains(l, "wrong number of tensors") || strings.Contains(l, "missing tensor") || strings.Contains(l, "GGML_ASSERT") {
			keep = append(keep, l)
		}
	}
	if len(keep) == 0 {
		lines := strings.Split(strings.TrimSpace(s), "\n")
		if len(lines) > n {
			lines = lines[len(lines)-n:]
		}
		return strings.Join(lines, " | ")
	}
	if len(keep) > n {
		keep = keep[len(keep)-n:]
	}
	return fmt.Sprint(strings.Join(keep, " | "))
}

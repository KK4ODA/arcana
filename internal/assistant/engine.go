package assistant

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// EngineConfig selects the model the local engine runs.
type EngineConfig struct {
	// Binary is the llamafile runtime (a single portable executable).
	Binary string
	// Model is a GGUF model file.
	Model string
	// ContextSize in tokens.
	ContextSize int
	// Threads for generation; 0 lets the engine choose.
	Threads int
}

// Engine states.
const (
	EngineStopped  = "stopped"
	EngineStarting = "starting"
	EngineReady    = "ready"
	EngineFailed   = "failed"
)

// EngineStatus is reported to the interface.
type EngineStatus struct {
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	// Since is when the engine reached its state.
	Since time.Time `json:"since"`
}

// Engine supervises llamafile running a model in server mode on loopback,
// protected by a random API key so other programs on the computer cannot
// use it. It starts when an answer needs it and stops after IdleTimeout to
// give the memory back.
type Engine struct {
	Log         *slog.Logger
	IdleTimeout time.Duration
	// StartTimeout bounds loading a model (slow USB drives take minutes).
	StartTimeout time.Duration
	// command replaces exec.Command in tests.
	command func(name string, arg ...string) *exec.Cmd

	mu     sync.Mutex
	cfg    EngineConfig
	proc   *engineProc
	status EngineStatus
	users  int
	last   time.Time
	start  chan struct{} // closed when the current start attempt ends
	stopc  chan struct{}
}

type engineProc struct {
	cmd    *exec.Cmd
	base   string
	key    string
	done   chan struct{}
	stderr *tail
}

// NewEngine creates a stopped engine.
func NewEngine(log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	e := &Engine{Log: log, IdleTimeout: 10 * time.Minute, StartTimeout: 5 * time.Minute,
		status: EngineStatus{State: EngineStopped, Since: time.Now()}, stopc: make(chan struct{})}
	go e.idleLoop()
	return e
}

// Status reports the engine's state.
func (e *Engine) Status() EngineStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status
}

func (e *Engine) setStatus(state, msg string) {
	e.status = EngineStatus{State: state, Error: msg, Since: time.Now()}
}

// Acquire returns the address and key of a running engine for cfg,
// starting it (or restarting it with a different model) if needed. Call
// the returned release function when the request is finished.
func (e *Engine) Acquire(ctx context.Context, cfg EngineConfig) (base, key string, release func(), err error) {
	for {
		e.mu.Lock()
		if e.proc != nil && e.cfg == cfg {
			select {
			case <-e.proc.done:
				e.proc = nil // exited on its own; start again below
			default:
				e.users++
				p := e.proc
				e.mu.Unlock()
				return p.base, p.key, e.releaser(), nil
			}
		}
		if e.start != nil {
			// Another request is starting the engine; wait for it.
			ch := e.start
			e.mu.Unlock()
			select {
			case <-ch:
				continue
			case <-ctx.Done():
				return "", "", nil, ctx.Err()
			}
		}
		if e.proc != nil {
			e.stopLocked()
		}
		e.start = make(chan struct{})
		e.cfg = cfg
		e.setStatus(EngineStarting, "")
		e.mu.Unlock()

		p, err := e.launch(ctx, cfg)

		e.mu.Lock()
		close(e.start)
		e.start = nil
		if err != nil {
			e.setStatus(EngineFailed, err.Error())
			e.mu.Unlock()
			return "", "", nil, err
		}
		e.proc = p
		e.setStatus(EngineReady, "")
		e.last = time.Now()
		go e.watch(p)
		e.mu.Unlock()
	}
}

func (e *Engine) releaser() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			e.mu.Lock()
			e.users--
			e.last = time.Now()
			e.mu.Unlock()
		})
	}
}

func (e *Engine) watch(p *engineProc) {
	<-p.done
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.proc != p {
		return
	}
	e.proc = nil
	msg := "The model engine stopped unexpectedly."
	if t := p.stderr.String(); t != "" {
		msg += " " + lastLines(t, 3)
	}
	if st := p.cmd.ProcessState; st != nil && st.ExitCode() != -1 {
		msg += " (exit code " + strconv.Itoa(st.ExitCode()) + ")"
	}
	e.setStatus(EngineFailed, msg)
	e.Log.Warn("model engine exited", "detail", msg)
}

// Stop stops the engine (it starts again on the next question).
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopLocked()
	e.setStatus(EngineStopped, "")
}

// Close stops the engine and its idle timer.
func (e *Engine) Close() {
	e.Stop()
	e.mu.Lock()
	defer e.mu.Unlock()
	select {
	case <-e.stopc:
	default:
		close(e.stopc)
	}
}

func (e *Engine) stopLocked() {
	if e.proc == nil {
		return
	}
	p := e.proc
	e.proc = nil
	stopProcess(p.cmd)
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		_ = p.cmd.Process.Kill()
	}
}

func (e *Engine) idleLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-e.stopc:
			return
		case <-t.C:
		}
		e.mu.Lock()
		if e.proc != nil && e.users == 0 && e.IdleTimeout > 0 && time.Since(e.last) > e.IdleTimeout {
			e.Log.Info("stopping idle model engine")
			e.stopLocked()
			e.setStatus(EngineStopped, "")
		}
		e.mu.Unlock()
	}
}

func (e *Engine) launch(ctx context.Context, cfg EngineConfig) (*engineProc, error) {
	if _, err := os.Stat(cfg.Model); err != nil {
		return nil, fmt.Errorf("the model file is missing: %w", err)
	}
	bin, err := runnable(cfg.Binary)
	if err != nil {
		return nil, fmt.Errorf("the model engine cannot be run: %w", err)
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	key := randomKey()
	args := []string{"--server", "-m", cfg.Model, "--host", "127.0.0.1", "--port", strconv.Itoa(port),
		"-c", strconv.Itoa(cfg.ContextSize), "--api-key", key}
	if cfg.Threads > 0 {
		args = append(args, "-t", strconv.Itoa(cfg.Threads))
	}
	p, err := e.startProcess(bin, args)
	if err != nil {
		return nil, err
	}
	p.base = "http://127.0.0.1:" + strconv.Itoa(port)
	p.key = key
	wctx, cancel := context.WithTimeout(ctx, e.StartTimeout)
	defer cancel()
	if err := waitHealthy(wctx, p); err != nil {
		stopProcess(p.cmd)
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
			_ = p.cmd.Process.Kill()
		}
		if t := p.stderr.String(); t != "" {
			err = fmt.Errorf("%w: %s", err, lastLines(t, 4))
		}
		return nil, err
	}
	e.Log.Info("model engine ready", "model", filepath.Base(cfg.Model), "port", port)
	return p, nil
}

func (e *Engine) startProcess(bin string, args []string) (*engineProc, error) {
	mk := e.command
	if mk == nil {
		mk = exec.Command
	}
	run := func(name string, a ...string) (*engineProc, error) {
		cmd := mk(name, a...)
		cmd.Dir = filepath.Dir(bin)
		cmd.Stdin = nil
		cmd.Stdout = io.Discard
		st := &tail{max: 16 << 10}
		cmd.Stderr = st
		configureProcess(cmd)
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		p := &engineProc{cmd: cmd, done: make(chan struct{}), stderr: st}
		go func() { _ = cmd.Wait(); close(p.done) }()
		return p, nil
	}
	p, err := run(bin, args...)
	if err != nil && runtime.GOOS != "windows" && errors.Is(err, syscall.ENOEXEC) {
		// llamafile is an "actually portable executable": a file that is
		// also a shell script. Where the kernel does not recognise it, the
		// shell starts it.
		p, err = run("/bin/sh", append([]string{bin}, args...)...)
	}
	if err != nil {
		return nil, fmt.Errorf("starting the model engine: %w", err)
	}
	return p, nil
}

// waitHealthy polls llama.cpp's /health until the model is loaded.
func waitHealthy(ctx context.Context, p *engineProc) error {
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil}}
	t := time.NewTicker(300 * time.Millisecond)
	defer t.Stop()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/health", nil)
		req.Header.Set("Authorization", "Bearer "+p.key)
		if resp, err := client.Do(req); err == nil {
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-p.done:
			return errors.New("the model engine exited while loading the model")
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return errors.New("the model took too long to load")
			}
			return ctx.Err()
		case <-t.C:
		}
	}
}

// runnable returns a path the operating system can execute. Drives
// formatted exFAT or FAT32 have no execute permission, and Windows only
// runs files ending in .exe; in both cases a copy is made in the user's
// cache folder (reused while the file is unchanged).
func runnable(p string) (string, error) {
	st, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		if strings.EqualFold(filepath.Ext(p), ".exe") {
			return p, nil
		}
		return cachedCopy(p, filepath.Base(p)+".exe")
	}
	if st.Mode()&0o111 != 0 {
		return p, nil
	}
	if err := os.Chmod(p, st.Mode()|0o755); err == nil {
		if st2, err := os.Stat(p); err == nil && st2.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return cachedCopy(p, filepath.Base(p))
}

func cachedCopy(p, name string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	// Identify the file by size and modification time rather than hashing
	// tens of megabytes on every start; the download pipeline has already
	// verified its content.
	id := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", p, st.Size(), st.ModTime().UnixNano())))
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = os.TempDir()
	}
	dir := filepath.Join(cache, "arcana", "engine-"+hex.EncodeToString(id[:8]))
	dst := filepath.Join(dir, name)
	if st2, err := os.Stat(dst); err == nil && st2.Size() == st.Size() {
		return dst, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, f); err != nil {
		out.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, dst); err != nil {
		return "", err
	}
	return dst, nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func randomKey() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// tail keeps the end of a process's error output for diagnosis.
type tail struct {
	mu  sync.Mutex
	max int
	b   []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > t.max {
		t.b = t.b[len(t.b)-t.max:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.b))
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	out := strings.Join(lines, " / ")
	if len(out) > 400 {
		out = out[len(out)-400:]
	}
	return out
}

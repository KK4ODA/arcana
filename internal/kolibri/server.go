package kolibri

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ZipPrefix is the path under Arcana's content origin where Kolibri's zip
// content (channel HTML apps) is served (ADR-0024).
const ZipPrefix = "/kolibri-zip/"

// Options say how to run Kolibri.
type Options struct {
	Runtime Runtime
	// Home is KOLIBRI_HOME, on the library drive.
	Home string
	// ContentPort is Arcana's content origin port; Kolibri's pages load zip
	// content from it.
	ContentPort int
	// ProvisionFile, when set, provisions Kolibri at its first start
	// (WriteProvisionFile).
	ProvisionFile string
}

// Status describes the Kolibri process.
type Status struct {
	// State is "stopped", "starting", "running" or "failed".
	State     string    `json:"state"`
	Since     time.Time `json:"since"`
	LastError string    `json:"lastError,omitempty"`
	Restarts  int       `json:"restarts"`
	Version   string    `json:"version,omitempty"`
}

// Server runs and supervises one Kolibri process.
type Server struct {
	Log *slog.Logger
	// ReadyTimeout bounds the first start, which migrates Kolibri's
	// database (about a minute; longer on a Raspberry Pi).
	ReadyTimeout time.Duration

	mu     sync.Mutex
	proc   *process
	opts   Options
	status Status
	closed bool
	gen    int
}

type process struct {
	cmd     *exec.Cmd
	port    int
	zipPort int
	stdin   io.WriteCloser
	done    chan struct{}
	output  *tail
}

// NewServer returns a stopped supervisor.
func NewServer(log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{Log: log, ReadyTimeout: 15 * time.Minute, status: Status{State: "stopped", Since: time.Now()}}
}

// Status returns the current status.
func (s *Server) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Base returns Kolibri's loopback address while it is running.
func (s *Server) Base() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proc == nil || s.status.State != "running" {
		return "", false
	}
	return "http://127.0.0.1:" + strconv.Itoa(s.proc.port), true
}

// ZipBase returns the address of Kolibri's zip content server.
func (s *Server) ZipBase() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proc == nil || s.status.State != "running" {
		return "", false
	}
	return "http://127.0.0.1:" + strconv.Itoa(s.proc.zipPort), true
}

func (s *Server) setState(gen int, state, lastErr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.gen {
		return
	}
	if s.status.State != state {
		s.status.Since = time.Now()
	}
	s.status.State = state
	s.status.LastError = lastErr
}

// Start runs Kolibri in the background with opts, replacing a process
// started with other options. It returns at once; Status follows progress.
func (s *Server) Start(opts Options) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.proc != nil && s.opts == opts && (s.status.State == "running" || s.status.State == "starting") {
		s.mu.Unlock()
		return
	}
	old := s.proc
	s.proc = nil
	s.gen++
	gen := s.gen
	s.opts = opts
	s.status = Status{State: "starting", Since: time.Now(), Restarts: s.status.Restarts}
	s.mu.Unlock()
	stopProcess(old)
	go s.run(gen, opts)
}

func (s *Server) run(gen int, opts Options) {
	p, version, err := s.launch(gen, opts)
	if err != nil {
		s.Log.Warn("kolibri did not start", "err", err)
		s.setState(gen, "failed", err.Error())
		return
	}
	s.mu.Lock()
	if gen != s.gen || s.closed {
		s.mu.Unlock()
		stopProcess(p)
		return
	}
	s.proc = p
	s.status.State = "running"
	s.status.Since = time.Now()
	s.status.LastError = ""
	s.status.Version = version
	s.mu.Unlock()
	s.Log.Info("kolibri running", "port", p.port, "zipPort", p.zipPort, "version", version)
	go s.supervise(gen, p, opts)
}

// supervise restarts Kolibri if it stops by itself, a few times.
func (s *Server) supervise(gen int, p *process, opts Options) {
	<-p.done
	s.mu.Lock()
	if gen != s.gen || s.closed || s.proc != p {
		s.mu.Unlock()
		return
	}
	s.proc = nil
	s.status.Restarts++
	restarts := s.status.Restarts
	s.status.State = "failed"
	s.status.LastError = "Kolibri stopped unexpectedly: " + lastLine(p.output.String())
	s.mu.Unlock()
	s.Log.Warn("kolibri exited unexpectedly", "restarts", restarts, "output", lastLine(p.output.String()))
	if restarts > 5 {
		return
	}
	time.Sleep(time.Duration(restarts) * 2 * time.Second)
	s.mu.Lock()
	again := gen == s.gen && !s.closed && s.proc == nil
	s.mu.Unlock()
	if again {
		s.Start(opts)
	}
}

// Stop ends Kolibri and keeps it stopped until Start is called.
func (s *Server) Stop() {
	s.mu.Lock()
	p := s.proc
	s.proc = nil
	s.gen++
	s.status = Status{State: "stopped", Since: time.Now(), Restarts: s.status.Restarts}
	s.mu.Unlock()
	stopProcess(p)
}

// Close stops Kolibri for good.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.Stop()
}

// bootstrap runs Kolibri's command line, and ends Kolibri when its standard
// input closes: Arcana holds the other end, so Kolibri stops with Arcana
// even if Arcana is killed. Kolibri treats the interrupt as Ctrl-C and shuts
// down cleanly; if it has not within 20 seconds, it exits.
const bootstrap = `import os, sys, threading, _thread, runpy
sys.path.insert(0, os.environ["ARCANA_KOLIBRI_APP"])
def _watch():
    try:
        while sys.stdin.buffer.read(4096):
            pass
    except Exception:
        pass
    t = threading.Timer(20, lambda: os._exit(0))
    t.daemon = True
    t.start()
    _thread.interrupt_main()
threading.Thread(target=_watch, daemon=True).start()
sys.argv = ["kolibri"] + sys.argv[1:]
runpy.run_module("kolibri", run_name="__main__", alter_sys=True)
`

// command prepares a Kolibri command line ("start …"). Python
// runs isolated (-I: no user site, no PYTHON* variables, no current folder
// on the path); the bootstrap puts Kolibri's folder on the path itself.
func command(opts Options, args ...string) *exec.Cmd {
	cmd := exec.Command(opts.Runtime.Python, append([]string{"-I", "-X", "utf8", "-c", bootstrap}, args...)...)
	cmd.Dir = opts.Home
	cmd.Env = environment(opts)
	hideWindow(cmd)
	return cmd
}

// environment is Arcana's environment without Python's and Kolibri's own
// settings, plus the ones Kolibri runs with here.
func environment(opts Options) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		ku := strings.ToUpper(k)
		if strings.HasPrefix(ku, "PYTHON") || strings.HasPrefix(ku, "KOLIBRI_") || ku == "DJANGO_SETTINGS_MODULE" {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"ARCANA_KOLIBRI_APP="+opts.Runtime.App,
		"KOLIBRI_HOME="+opts.Home,
		"KOLIBRI_LISTEN_ADDRESS=127.0.0.1",
		"KOLIBRI_ZIP_CONTENT_ORIGIN="+strconv.Itoa(opts.ContentPort),
		"KOLIBRI_ZIP_CONTENT_URL_PATH_PREFIX="+strings.Trim(ZipPrefix, "/"),
		"KOLIBRI_DISABLE_PING=true",
		"KOLIBRI_ZEROCONF_ENABLED=false",
		"KOLIBRI_RUN_MODE=arcana",
	)
	if opts.ProvisionFile != "" {
		env = append(env, "KOLIBRI_AUTOMATIC_PROVISION_FILE="+opts.ProvisionFile)
	}
	return env
}

func (s *Server) launch(gen int, opts Options) (*process, string, error) {
	if err := os.MkdirAll(opts.Home, 0o755); err != nil {
		return nil, "", err
	}
	// Arcana holds the library lock, so no other Kolibri uses this home: a
	// PID file left by a computer that lost power would only stop the start.
	_ = os.Remove(filepath.Join(opts.Home, "server.pid"))
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		port, err := freePort()
		if err != nil {
			return nil, "", err
		}
		zipPort, err := freePort()
		if err != nil {
			return nil, "", err
		}
		if zipPort == port {
			continue
		}
		cmd := command(opts, "start", "--foreground", "--port="+strconv.Itoa(port), "--zip-port="+strconv.Itoa(zipPort))
		out := &tail{max: 64 << 10}
		cmd.Stdout, cmd.Stderr = out, out
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, "", err
		}
		if err := cmd.Start(); err != nil {
			return nil, "", fmt.Errorf("start Kolibri: %w", err)
		}
		p := &process{cmd: cmd, port: port, zipPort: zipPort, stdin: stdin, done: make(chan struct{}), output: out}
		go func() { _ = cmd.Wait(); close(p.done) }()
		version, err := s.waitReady(gen, p)
		if err == nil {
			return p, version, nil
		}
		stopProcess(p)
		lastErr = err
		if errors.Is(err, errSuperseded) || errors.Is(err, errNotReady) {
			return nil, "", err
		}
	}
	return nil, "", lastErr
}

var (
	errSuperseded = errors.New("superseded")
	errNotReady   = errors.New("Kolibri did not become ready in time")
)

// waitReady polls Kolibri's public description until it answers.
func (s *Server) waitReady(gen int, p *process) (string, error) {
	client := &Client{BaseURL: "http://127.0.0.1:" + strconv.Itoa(p.port), HTTP: &http.Client{Timeout: 5 * time.Second}}
	deadline := time.Now().Add(s.ReadyTimeout)
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			return "", fmt.Errorf("Kolibri stopped while starting: %s", lastLine(p.output.String()))
		case <-time.After(500 * time.Millisecond):
		}
		s.mu.Lock()
		current := gen == s.gen && !s.closed
		s.mu.Unlock()
		if !current {
			return "", errSuperseded
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		info, err := client.Info(ctx)
		cancel()
		if err == nil {
			return info.Version, nil
		}
	}
	return "", errNotReady
}

// stopProcess closes Kolibri's standard input, which makes it shut down,
// and kills it if it has not ended within 25 seconds.
func stopProcess(p *process) {
	if p == nil || p.cmd.Process == nil {
		return
	}
	p.stdin.Close()
	select {
	case <-p.done:
		return
	case <-time.After(25 * time.Second):
	}
	_ = p.cmd.Process.Kill()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// tail keeps the last max bytes written.
type tail struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tail) String() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// lastLine returns the last non-empty line of out, for messages.
func lastLine(out string) string {
	lines := bytes.Split(bytes.TrimSpace([]byte(out)), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(string(lines[i])); l != "" {
			if len(l) > 300 {
				l = l[:300] + "…"
			}
			return l
		}
	}
	return "no output"
}

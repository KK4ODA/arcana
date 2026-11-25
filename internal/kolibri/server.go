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
	"regexp"
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
	// Activity is Kolibri's latest message while it starts, to show that
	// it is working and on what.
	Activity string `json:"activity,omitempty"`
}

// Server runs and supervises one Kolibri process.
type Server struct {
	Log *slog.Logger
	// ReadyTimeout bounds the first start, which migrates Kolibri's
	// database (about a minute; longer on a Raspberry Pi).
	ReadyTimeout time.Duration
	// FailAfter is how long Kolibri may answer with internal errors while
	// starting before the start is given up: it runs, but is broken.
	FailAfter time.Duration

	mu   sync.Mutex
	proc *process
	// starting is the output of the process being started, for Activity.
	starting *tail
	opts     Options
	status   Status
	closed   bool
	gen      int
	// slot is held from a process's start until it has exited, so a new
	// Kolibri never shares KOLIBRI_HOME with one still shutting down.
	slot chan struct{}
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
	return &Server{Log: log, ReadyTimeout: 15 * time.Minute, FailAfter: 30 * time.Second,
		status: Status{State: "stopped", Since: time.Now()}, slot: make(chan struct{}, 1)}
}

// Status returns the current status.
func (s *Server) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	if st.State == "starting" {
		st.Activity = activityLine(s.starting.String())
	}
	return st
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
// Neither Start nor Stop waits for a process to end: the next one waits
// for the slot instead.
func (s *Server) Start(opts Options) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	// The provisioning file only matters at the first start; dropping it
	// later must not restart Kolibri.
	same := s.opts.Runtime == opts.Runtime && s.opts.Home == opts.Home && s.opts.ContentPort == opts.ContentPort
	if same && (s.status.State == "running" || s.status.State == "starting") {
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
	go stopProcess(old)
	go s.run(gen, opts)
}

func (s *Server) current(gen int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return gen == s.gen && !s.closed
}

func (s *Server) run(gen int, opts Options) {
	s.slot <- struct{}{}
	if !s.current(gen) {
		<-s.slot
		return
	}
	p, version, err := s.launch(gen, opts)
	s.mu.Lock()
	if gen == s.gen {
		s.starting = nil
	}
	s.mu.Unlock()
	if err != nil {
		<-s.slot
		if !errors.Is(err, errSuperseded) {
			s.Log.Warn("kolibri did not start", "err", err)
		}
		s.setState(gen, "failed", err.Error())
		return
	}
	s.mu.Lock()
	if gen != s.gen || s.closed {
		s.mu.Unlock()
		stopProcess(p)
		<-s.slot
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

// supervise frees the slot when Kolibri ends, and restarts it if it
// stopped by itself, a few times.
func (s *Server) supervise(gen int, p *process, opts Options) {
	<-p.done
	<-s.slot
	s.mu.Lock()
	if gen != s.gen || s.closed || s.proc != p {
		s.mu.Unlock()
		return
	}
	s.proc = nil
	s.status.Restarts++
	restarts := s.status.Restarts
	s.status.State = "failed"
	s.status.LastError = "Kolibri stopped unexpectedly: " + errorLine(p.output.String())
	s.mu.Unlock()
	s.Log.Warn("kolibri exited unexpectedly", "restarts", restarts, "output", errorLine(p.output.String()))
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

// Stop ends Kolibri and keeps it stopped until Start is called. It does
// not wait for Kolibri to exit.
func (s *Server) Stop() {
	go stopProcess(s.detach())
}

func (s *Server) detach() *process {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.proc
	s.proc = nil
	s.gen++
	s.status = Status{State: "stopped", Since: time.Now(), Restarts: s.status.Restarts}
	return p
}

// Close stops Kolibri for good and waits until it has exited (a start in
// progress notices within half a second), so the library can be released.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	stopProcess(s.detach())
	select {
	case s.slot <- struct{}{}:
		<-s.slot
	case <-time.After(40 * time.Second):
		s.Log.Warn("kolibri did not stop in time")
	}
}

// SettingsModule is the Django settings module Kolibri runs with: Kolibri's
// own, with one repair (see bootstrap). Kolibri uses DJANGO_SETTINGS_MODULE
// when it is set.
const SettingsModule = "arcana_kolibri_settings"

// bootstrap runs Kolibri's command line, and ends Kolibri when its standard
// input closes: Arcana holds the other end, so Kolibri stops with Arcana
// even if Arcana is killed. Kolibri treats the interrupt as Ctrl-C and shuts
// down cleanly; if it has not within 20 seconds, it exits.
//
// It also provides SettingsModule, without a file. With ZIP_CONTENT_ORIGIN
// set to a port (which Arcana's proxy needs), Kolibri 0.19.5 puts that port
// into its CSP_FRAME_SRC and CSP_MEDIA_SRC as a number, which django-csp
// cannot join: every response, /api/public/info/ included, failed with
// "TypeError: sequence item 3: expected str instance, int found". The module
// turns every CSP source into text; Arcana's proxy then replaces the bare
// port with the content origin (FixCSP).
const bootstrap = `import os, sys, threading, _thread, runpy, importlib.abc, importlib.util
sys.path.insert(0, os.environ["ARCANA_KOLIBRI_APP"])
_SETTINGS = """
from kolibri.deployment.default.settings.base import *  # noqa
for _k in [k for k in list(globals()) if k.startswith("CSP_")]:
    if isinstance(globals()[_k], (list, tuple)):
        globals()[_k] = tuple(str(s) for s in globals()[_k])
"""
class _Settings(importlib.abc.MetaPathFinder, importlib.abc.Loader):
    def find_spec(self, name, path=None, target=None):
        if name == "` + SettingsModule + `":
            return importlib.util.spec_from_loader(name, self)
        return None
    def create_module(self, spec):
        return None
    def exec_module(self, module):
        exec(_SETTINGS, module.__dict__)
sys.meta_path.insert(0, _Settings())
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
		"DJANGO_SETTINGS_MODULE="+SettingsModule,
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
		s.mu.Lock()
		if gen == s.gen {
			s.starting = out
		}
		s.mu.Unlock()
		version, err := s.waitReady(gen, p)
		if err == nil {
			return p, version, nil
		}
		stopProcess(p)
		lastErr = err
		if errors.Is(err, errSuperseded) || errors.Is(err, errNotReady) || errors.Is(err, errBroken) {
			return nil, "", err
		}
	}
	return nil, "", lastErr
}

var (
	errSuperseded = errors.New("superseded")
	errNotReady   = errors.New("Kolibri did not become ready in time")
	errBroken     = errors.New("Kolibri is running but fails on every request")
)

// waitReady polls Kolibri's public description until it answers.
func (s *Server) waitReady(gen int, p *process) (string, error) {
	client := &Client{BaseURL: "http://127.0.0.1:" + strconv.Itoa(p.port), HTTP: &http.Client{Timeout: 5 * time.Second}}
	deadline := time.Now().Add(s.ReadyTimeout)
	var failing time.Time // since when Kolibri answers with internal errors
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			return "", fmt.Errorf("Kolibri stopped while starting: %s", errorLine(p.output.String()))
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
		// Kolibri answers, but with an internal error: a fault in Kolibri
		// or in how it is set up, which waiting will not mend.
		var se *StatusError
		if !errors.As(err, &se) || se.Code != http.StatusInternalServerError {
			failing = time.Time{}
			continue
		}
		if failing.IsZero() {
			failing = time.Now()
		}
		if time.Since(failing) >= s.FailAfter {
			return "", fmt.Errorf("%w (%s): %s", errBroken, se.Status, errorLine(p.output.String()))
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

// Kolibri's console output: lines in colour (ANSI escapes), each
// "LEVEL date time message"; the web server logs each request the same way,
// as "127.0.0.1 - - "GET …"" (Arcana's own checks while it waits).
var (
	ansiCode  = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	logLine   = regexp.MustCompile(`^(?:DEBUG|INFO|WARNING|ERROR|CRITICAL)\s+\d{4}-\d{2}-\d{2}\s+[\d:,.]+\s+(.+)$`)
	accessLog = regexp.MustCompile(`^\S+ - \S+ (?:\[[^\]]*\] )?"`)
)

// activityLine returns Kolibri's latest log message, without its level,
// date and time, skipping the web server's request log.
func activityLine(out string) string {
	lines := strings.Split(strings.TrimSpace(ansiCode.ReplaceAllString(out, "")), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		m := logLine.FindStringSubmatch(strings.TrimSpace(lines[i]))
		if m == nil || accessLog.MatchString(m[1]) {
			continue
		}
		msg := m[1]
		if len(msg) > 200 {
			msg = msg[:200] + "…"
		}
		return msg
	}
	return ""
}

// pyError is the last line of a Python traceback: the exception and its
// message.
var pyError = regexp.MustCompile(`^[A-Za-z_][\w.]*(?:Error|Exception)\b`)

// errorLine returns the exception that ended the last traceback in out, or
// else its last line.
func errorLine(out string) string {
	out = ansiCode.ReplaceAllString(out, "")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); pyError.MatchString(l) {
			if len(l) > 300 {
				l = l[:300] + "…"
			}
			return l
		}
	}
	return lastLine(out)
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

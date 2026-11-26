package apps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ServiceSpec says how Arcana runs a native app.
type ServiceSpec struct {
	// Command returns the program to run. It may prepare files first (a
	// configuration the program reads, Python packages); env.Progress
	// reports what it is doing.
	Command func(env Env) (Command, error)
	// ReadyPath is requested until the program answers without a server
	// error.
	ReadyPath string
	// StartTimeout bounds a start (a first start creates databases).
	StartTimeout time.Duration
	// AdminUser, when set, is the administrator account Arcana creates at
	// the first start, with a random password shown to Arcana's
	// administrators (env.Password).
	AdminUser string
	// FirstUse tells people what to do at the first visit.
	FirstUse string
	// CrossOrigin lets other origins send changes. Only for programs whose
	// API authenticates each request with a token in a header, not a
	// cookie, and whose own clients (Bitwarden's browser extensions,
	// Jellyfin's apps) call it from elsewhere.
	CrossOrigin bool
}

// Env is what a service's Command gets.
type Env struct {
	// Runtime is the app's unpacked folder on this computer.
	Runtime string
	// Data is the app's folder on the drive (user/apps/<id>).
	Data string
	// Files is the folder shared between apps (user/files): File Browser
	// manages it, Jellyfin reads media from it.
	Files string
	// Cache is a folder on this computer for rebuildable data.
	Cache    string
	Platform string
	// Port is the loopback port the program must listen on.
	Port     int
	Password string
	Log      *slog.Logger
	Progress func(string)
}

// Exe adds ".exe" on Windows.
func (e Env) Exe(name string) string {
	if strings.HasPrefix(e.Platform, "windows-") {
		return name + ".exe"
	}
	return name
}

// Command is a program to run.
type Command struct {
	Path string
	Args []string
	// Env is added to Arcana's environment, without the variables whose
	// names start with one of DropEnv.
	Env     []string
	DropEnv []string
	Dir     string
}

// environment is Arcana's environment without the variables whose names
// start with a prefix in drop, plus add.
func environment(drop, add []string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		k = strings.ToUpper(k)
		keep := true
		for _, p := range drop {
			if strings.HasPrefix(k, p) {
				keep = false
			}
		}
		if keep {
			env = append(env, kv)
		}
	}
	return append(env, add...)
}

// ServiceStatus describes a native app's program.
type ServiceStatus struct {
	// State is "stopped", "starting", "running" or "failed".
	State     string    `json:"state"`
	Since     time.Time `json:"since"`
	LastError string    `json:"lastError,omitempty"`
	// Activity is what a start is doing.
	Activity string `json:"activity,omitempty"`
	Restarts int    `json:"restarts"`
}

// IdleStop is how long a program runs with no request before Arcana stops
// it, to give its memory back. The next request starts it again.
const IdleStop = 2 * time.Hour

// Supervisor runs one native app's program.
type Supervisor struct {
	spec *Spec
	env  Env
	log  *slog.Logger

	mu       sync.Mutex
	status   ServiceStatus
	proc     *process
	gen      int
	closed   bool
	lastUse  time.Time
	active   int
	activity string
	// slot is held from a start until the program has exited, so two
	// copies never share the app's data.
	slot chan struct{}
}

type process struct {
	cmd    *exec.Cmd
	port   int
	done   chan struct{}
	output *tail
	job    jobHandle
}

func newSupervisor(spec *Spec, env Env, log *slog.Logger) *Supervisor {
	return &Supervisor{spec: spec, env: env, log: log, slot: make(chan struct{}, 1),
		status: ServiceStatus{State: "stopped", Since: time.Now()}}
}

// Status returns the program's state.
func (s *Supervisor) Status() ServiceStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	if st.State == "starting" {
		st.Activity = s.activity
	}
	return st
}

// Base returns the program's loopback address while it runs.
func (s *Supervisor) Base() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proc == nil || s.status.State != "running" {
		return "", false
	}
	return "http://127.0.0.1:" + strconv.Itoa(s.proc.port), true
}

// retryAfter is how long a failed program is left before a request starts
// it again, so a program that cannot start is not restarted on every
// request.
const retryAfter = 30 * time.Second

// Ensure starts the program if it is stopped, or failed a while ago. It
// returns at once.
func (s *Supervisor) Ensure() {
	s.mu.Lock()
	s.lastUse = time.Now()
	st := s.status
	s.mu.Unlock()
	switch {
	case st.State == "stopped", st.State == "failed" && time.Since(st.Since) > retryAfter:
		s.Start()
	}
}

// Start starts the program, or restarts it after a failure.
func (s *Supervisor) Start() {
	s.mu.Lock()
	if s.closed || s.status.State == "starting" || s.status.State == "running" {
		s.mu.Unlock()
		return
	}
	s.gen++
	gen := s.gen
	s.status = ServiceStatus{State: "starting", Since: time.Now(), Restarts: s.status.Restarts}
	s.activity = ""
	s.lastUse = time.Now()
	s.mu.Unlock()
	go s.run(gen)
}

func (s *Supervisor) current(gen int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return gen == s.gen && !s.closed
}

func (s *Supervisor) setFailed(gen int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.gen {
		return
	}
	s.status.State, s.status.Since, s.status.LastError = "failed", time.Now(), err.Error()
}

func (s *Supervisor) run(gen int) {
	s.slot <- struct{}{}
	if !s.current(gen) {
		<-s.slot
		return
	}
	p, err := s.launch(gen)
	if err != nil {
		<-s.slot
		if !errors.Is(err, errSuperseded) {
			s.log.Warn("app did not start", "app", s.spec.ID, "err", err)
			s.setFailed(gen, err)
		}
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
	s.status.State, s.status.Since, s.status.LastError = "running", time.Now(), ""
	s.mu.Unlock()
	s.log.Info("app running", "app", s.spec.ID, "port", p.port)
	go s.supervise(gen, p)
}

// supervise frees the slot when the program ends, and stops it when it
// has been idle for IdleStop.
func (s *Supervisor) supervise(gen int, p *process) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-p.done:
			<-s.slot
			s.mu.Lock()
			if gen == s.gen && s.proc == p {
				s.proc = nil
				s.status.Restarts++
				s.status.State, s.status.Since = "failed", time.Now()
				s.status.LastError = s.spec.Title + " stopped unexpectedly: " + errorLines(p.output.String())
				s.log.Warn("app exited", "app", s.spec.ID, "output", lastLines(p.output.String(), 10))
			}
			s.mu.Unlock()
			return
		case <-t.C:
			s.mu.Lock()
			idle := s.proc == p && s.active == 0 && time.Since(s.lastUse) > IdleStop
			s.mu.Unlock()
			if idle {
				s.log.Info("app idle, stopping", "app", s.spec.ID)
				s.Stop()
			}
		}
	}
}

var errSuperseded = errors.New("superseded")

func (s *Supervisor) launch(gen int) (*process, error) {
	env := s.env
	env.Progress = func(msg string) {
		s.mu.Lock()
		if gen == s.gen {
			s.activity = msg
		}
		s.mu.Unlock()
	}
	for _, d := range []string{env.Data, env.Cache} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	if env.Files != "" {
		if err := os.MkdirAll(env.Files, 0o755); err != nil {
			return nil, err
		}
	}
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	env.Port = port
	c, err := s.spec.Service.Command(env)
	if err != nil {
		return nil, err
	}
	if !s.current(gen) {
		return nil, errSuperseded
	}
	env.Progress("Starting " + s.spec.Title)
	cmd := exec.Command(c.Path, c.Args...)
	cmd.Dir = c.Dir
	if cmd.Dir == "" {
		cmd.Dir = env.Data
	}
	cmd.Env = environment(c.DropEnv, c.Env)
	out := &tail{max: 64 << 10}
	cmd.Stdout, cmd.Stderr = out, out
	configureProcess(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", s.spec.Title, err)
	}
	p := &process{cmd: cmd, port: port, done: make(chan struct{}), output: out}
	p.job = attachJob(cmd)
	go func() { _ = cmd.Wait(); close(p.done) }()
	if err := s.waitReady(gen, p); err != nil {
		stopProcess(p)
		return nil, err
	}
	return p, nil
}

// waitReady polls the program until it answers.
func (s *Supervisor) waitReady(gen int, p *process) error {
	timeout := s.spec.Service.StartTimeout
	if timeout == 0 {
		timeout = 3 * time.Minute
	}
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	url := "http://127.0.0.1:" + strconv.Itoa(p.port) + s.spec.Service.ReadyPath
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			return fmt.Errorf("%s stopped while starting: %s", s.spec.Title, errorLines(p.output.String()))
		case <-time.After(300 * time.Millisecond):
		}
		if !s.current(gen) {
			return errSuperseded
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		resp, err := client.Do(req)
		cancel()
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
	}
	return fmt.Errorf("%s did not answer within %s: %s", s.spec.Title, timeout, errorLines(p.output.String()))
}

// Stop ends the program; it waits until it has exited.
func (s *Supervisor) Stop() {
	s.mu.Lock()
	p := s.proc
	s.proc = nil
	s.gen++
	s.status = ServiceStatus{State: "stopped", Since: time.Now(), Restarts: s.status.Restarts}
	s.mu.Unlock()
	stopProcess(p)
}

// Close stops the program for good and waits for it to exit.
func (s *Supervisor) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.Stop()
	select {
	case s.slot <- struct{}{}:
		<-s.slot
	case <-time.After(40 * time.Second):
		s.log.Warn("app did not stop in time", "app", s.spec.ID)
	}
}

// begin and end count requests in flight, so a long download or a video
// is not taken for idleness.
func (s *Supervisor) begin() {
	s.mu.Lock()
	s.active++
	s.lastUse = time.Now()
	s.mu.Unlock()
}

func (s *Supervisor) end() {
	s.mu.Lock()
	s.active--
	s.lastUse = time.Now()
	s.mu.Unlock()
}

// stopProcess asks the program to end, and kills it after 20 seconds.
func stopProcess(p *process) {
	if p == nil || p.cmd.Process == nil {
		return
	}
	terminate(p.cmd)
	select {
	case <-p.done:
	case <-time.After(20 * time.Second):
		_ = p.cmd.Process.Kill()
		select {
		case <-p.done:
		case <-time.After(5 * time.Second):
		}
	}
	p.job.close()
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
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

// errorLines picks a program's failure out of its output: the last line
// that reports a panic, an error or an exception, or else its last lines.
func errorLines(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if failureLine.MatchString(l) {
			if len(l) > 400 {
				l = l[:400] + "…"
			}
			return l
		}
	}
	return lastLines(out, 3)
}

var failureLine = regexp.MustCompile(`(?i)\b(panic|fatal|error|exception)\b`)

// lastLines returns the last n non-empty lines of out, for messages.
func lastLines(out string, n int) string {
	var keep []string
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0 && len(keep) < n; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			if len(l) > 300 {
				l = l[:300] + "…"
			}
			keep = append([]string{l}, keep...)
		}
	}
	if len(keep) == 0 {
		return "no output"
	}
	return strings.Join(keep, " | ")
}

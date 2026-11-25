package kolibri

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KK4ODA/arcana/internal/kolibri/kolibritest"
)

func waitState(t *testing.T, s *Server, want string) Status {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		st := s.Status()
		if st.State == want {
			return st
		}
		if st.State == "failed" && want != "failed" {
			t.Fatalf("Kolibri failed: %s", st.LastError)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("Kolibri did not reach %q: %+v", want, s.Status())
	return Status{}
}

func fakeOptions(t *testing.T) (Options, Setup) {
	t.Helper()
	t.Setenv(kolibritest.Env, "1")
	home := t.TempDir()
	setup := Setup{Facility: "Hill School", Username: AdminUsername, Password: NewPassword(), Language: "es"}
	prov, err := WriteProvisionFile(home, setup)
	if err != nil {
		t.Fatal(err)
	}
	return Options{Runtime: Runtime{Python: kolibritest.Python(), App: t.TempDir()}, Home: home, ContentPort: 8471, ProvisionFile: prov}, setup
}

func TestServerRunsProvisionsAndStops(t *testing.T) {
	opts, setup := fakeOptions(t)
	// Settings meant for another Python or Kolibri must not reach this one.
	t.Setenv("PYTHONHOME", "/nowhere")
	t.Setenv("KOLIBRI_HTTP_PORT", "1")
	// A PID file left by a computer that lost power must not stop the start.
	os.WriteFile(filepath.Join(opts.Home, "server.pid"), []byte("12345\n8080\n8081"), 0o644)

	s := NewServer(nil)
	defer s.Close()
	s.Start(opts)
	st := waitState(t, s, "running")
	if st.Version != "0.19.5-fake" {
		t.Fatalf("version %q", st.Version)
	}
	env, _ := os.ReadFile(filepath.Join(opts.Home, "fake-env.txt"))
	for _, want := range []string{"KOLIBRI_ZIP_CONTENT_ORIGIN=8471", "KOLIBRI_ZIP_CONTENT_URL_PATH_PREFIX=kolibri-zip",
		"KOLIBRI_DISABLE_PING=true", "KOLIBRI_ZEROCONF_ENABLED=false", "KOLIBRI_HOME=" + opts.Home,
		"ARCANA_KOLIBRI_APP=" + opts.Runtime.App, "KOLIBRI_AUTOMATIC_PROVISION_FILE=" + opts.ProvisionFile,
		"DJANGO_SETTINGS_MODULE=" + SettingsModule} {
		if !strings.Contains(string(env), want+"\n") && !strings.HasSuffix(string(env), want) {
			t.Errorf("environment lacks %s", want)
		}
	}
	for _, bad := range []string{"PYTHONHOME=", "KOLIBRI_HTTP_PORT="} {
		if strings.Contains(string(env), bad) {
			t.Errorf("environment passes %s on", bad)
		}
	}

	base, ok := s.Base()
	if !ok {
		t.Fatal("no base while running")
	}
	ctx := context.Background()
	if info, err := (&Client{BaseURL: base}).Info(ctx); err != nil || info.DeviceName != "Hill School" {
		t.Fatalf("info %+v %v", info, err)
	}

	// The administrator from the provisioning file signs in and imports.
	adm := &Admin{BaseURL: base, Username: setup.Username, Password: setup.Password}
	en := Channels[0].ID
	task, err := adm.Import(ctx, en, Channels[0].Title, []string{"0123456789abcdef0123456789abcdef"})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID == "" || task.ChannelID != en || task.Percentage != 0.25 || task.FileSize != 1000 || task.Transferred != 250 || task.Done() {
		t.Fatalf("task %+v", task)
	}
	tasks, err := adm.Tasks(ctx)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks %+v %v", tasks, err)
	}
	if err := adm.Cancel(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if tasks, _ = adm.Tasks(ctx); tasks[0].Status != "CANCELED" || !tasks[0].Done() {
		t.Fatalf("after cancel %+v", tasks)
	}
	if err := adm.Clear(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if tasks, _ = adm.Tasks(ctx); len(tasks) != 0 {
		t.Fatalf("after clear %+v", tasks)
	}
	if ch, err := adm.Channels(ctx); err != nil || len(ch) != 1 || ch[0].Resources != 42 || ch[0].Size != 123456 {
		t.Fatalf("channels %+v %v", ch, err)
	}
	if _, err := adm.Import(ctx, "not-hex", "x", nil); err == nil {
		t.Fatal("a bad channel ID was sent")
	}
	if err := (&Admin{BaseURL: base, Username: setup.Username, Password: "wrong"}).Check(ctx); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("wrong password: %v", err)
	}

	// The proxy.
	p := &Proxy{Server: s, ContentPort: 8471, ContentOrigin: func(r *http.Request) string { return "http://school.local:8471" }}
	front := httptest.NewServer(p.Handler())
	defer front.Close()
	resp, err := http.Get(front.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "frame-src 'self' data: blob: http://school.local:8471") || strings.Contains(csp, " 8471") {
		t.Fatalf("CSP not fixed: %s", csp)
	}
	if !strings.Contains(string(body), "host "+strings.TrimPrefix(front.URL, "http://")) {
		t.Fatalf("Kolibri did not see the browser's host: %s", body)
	}
	zip := httptest.NewServer(http.StripPrefix("", p.ZipHandler()))
	defer zip.Close()
	req, _ := http.NewRequest(http.MethodGet, zip.URL+"/kolibri-zip/zipcontent/abc.zip/index.html", nil)
	req.Header.Set("Cookie", "sessionid=secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != `zip zipcontent/abc.zip/index.html cookie=""` || resp.Header.Get("Set-Cookie") != "" {
		t.Fatalf("zip content: %q %v", body, resp.Header)
	}
	if resp, err := http.Post(zip.URL+"/kolibri-zip/x", "text/plain", nil); err != nil || resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("zip content accepted a POST: %v %v", resp, err)
	}

	// Stopping closes Kolibri's standard input, which ends it.
	start := time.Now()
	s.Stop()
	if time.Since(start) > 10*time.Second {
		t.Fatalf("stopping took %s", time.Since(start))
	}
	if _, ok := s.Base(); ok {
		t.Fatal("still has a base after stopping")
	}
	resp, err = http.Get(front.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("stopped Kolibri answered %d", resp.StatusCode)
	}
}

func TestServerRestartsAfterACrash(t *testing.T) {
	opts, _ := fakeOptions(t)
	s := NewServer(nil)
	defer s.Close()
	s.Start(opts)
	waitState(t, s, "running")
	s.mu.Lock()
	p := s.proc
	s.mu.Unlock()
	_ = p.cmd.Process.Kill()
	<-p.done
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		st := s.Status()
		if st.State == "running" && st.Restarts == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("not restarted: %+v", s.Status())
}

func TestServerReportsAFailedStart(t *testing.T) {
	opts, _ := fakeOptions(t)
	t.Setenv("FAKE_KOLIBRI_FAIL", "1")
	s := NewServer(nil)
	defer s.Close()
	s.Start(opts)
	st := waitState(t, s, "failed")
	if !strings.Contains(st.LastError, "Traceback: something broke") {
		t.Fatalf("error %q", st.LastError)
	}
}

// TestServerGivesUpOnABrokenKolibri: a Kolibri that runs but answers every
// request with an internal error is reported as failed, with the error it
// printed, instead of "starting" for the full ready timeout (15 minutes):
// what the owner saw when Kolibri 0.19.5 could not build its CSP.
func TestServerGivesUpOnABrokenKolibri(t *testing.T) {
	opts, _ := fakeOptions(t)
	t.Setenv("FAKE_KOLIBRI_BROKEN", "1")
	s := NewServer(nil)
	s.FailAfter = time.Second
	defer s.Close()
	start := time.Now()
	s.Start(opts)
	st := waitState(t, s, "failed")
	if time.Since(start) > 10*time.Second {
		t.Fatalf("took %v to give up", time.Since(start))
	}
	if !strings.Contains(st.LastError, "fails on every request") || !strings.Contains(st.LastError, "500") ||
		!strings.Contains(st.LastError, "TypeError: sequence item 3: expected str instance, int found") {
		t.Fatalf("error %q", st.LastError)
	}
}

// TestServerReportsActivity: while Kolibri starts, its latest message shows,
// so a long first start visibly makes progress.
func TestServerReportsActivity(t *testing.T) {
	opts, _ := fakeOptions(t)
	t.Setenv("FAKE_KOLIBRI_SLOW", "2s")
	s := NewServer(nil)
	defer s.Close()
	s.Start(opts)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st := s.Status()
		if st.State == "starting" && st.Activity != "" {
			if st.Activity != "Running update routines for new version..." {
				t.Fatalf("activity %q", st.Activity)
			}
			waitState(t, s, "running")
			if a := s.Status().Activity; a != "" {
				t.Fatalf("activity %q once running", a)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("no activity while starting: %+v", s.Status())
}

func TestActivityAndErrorLines(t *testing.T) {
	const out = "INFO: Initialized plugins.json\n" +
		"\x1b[37mINFO     2026-11-25 13:32:25,136 Running installation routines for kolibri.plugins.facility, installing 0.19.5\x1b[0m\n" +
		"\x1b[37mINFO     2026-11-25 13:32:25,155 Starting Kolibri 0.19.5\x1b[0m\n" +
		"\x1b[31mERROR    2026-11-25 13:16:23,944 Internal Server Error: /api/public/info/\x1b[0m\n" +
		"Traceback (most recent call last):\n" +
		"  File \"csp/utils.py\", line 92, in build_policy\n" +
		"    policy_parts[key] = \" \".join(value)\n" +
		"TypeError: sequence item 3: expected str instance, int found\n" +
		"\x1b[37mINFO     2026-11-25 13:16:23,945 127.0.0.1 - - \"GET /api/public/info/\" 500 0 \"\" \"Go-http-client/1.1\"\x1b[0m\n"
	for _, c := range []struct{ name, out, activity, err string }{
		{"request log skipped", out, "Internal Server Error: /api/public/info/", "TypeError: sequence item 3: expected str instance, int found"},
		{"progress", "\x1b[37mINFO     2026-11-25 13:32:25,136 Running update routines for new version...\x1b[0m\n", "Running update routines for new version...", "INFO     2026-11-25 13:32:25,136 Running update routines for new version..."},
		{"no log lines", "INFO: Initialized plugins.json\n", "", "INFO: Initialized plugins.json"},
		{"nothing", "", "", "no output"},
	} {
		if got := activityLine(c.out); got != c.activity {
			t.Errorf("%s: activity %q, want %q", c.name, got, c.activity)
		}
		if got := errorLine(c.out); got != c.err {
			t.Errorf("%s: error %q, want %q", c.name, got, c.err)
		}
	}
}

// TestBootstrapSettingsModule runs the real bootstrap with this system's
// Python and a stand-in "kolibri" package whose settings hold the zip port
// as a number, as Kolibri 0.19.5's do: the settings module Arcana provides
// must turn every CSP source into text, keeping the rest.
func TestBootstrapSettingsModule(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err == nil {
		// Windows may have only a stand-in that offers the Microsoft Store.
		err = exec.Command(python, "-I", "-c", "import importlib.abc").Run()
	}
	if err != nil {
		t.Skip("no working python3")
	}
	app := t.TempDir()
	files := map[string]string{
		"kolibri/__init__.py":                             "",
		"kolibri/deployment/__init__.py":                  "",
		"kolibri/deployment/default/__init__.py":          "",
		"kolibri/deployment/default/settings/__init__.py": "",
		"kolibri/deployment/default/settings/base.py": "CSP_DEFAULT_SRC = (\"'self'\", \"data:\")\n" +
			"CSP_FRAME_SRC = CSP_DEFAULT_SRC + (8471,)\nCSP_MEDIA_SRC = list(CSP_FRAME_SRC)\nSESSION_COOKIE_NAME = \"kolibri\"\n",
		"kolibri/__main__.py": "import importlib, json, os, sys\n" +
			"s = importlib.import_module(os.environ[\"DJANGO_SETTINGS_MODULE\"])\n" +
			"print(json.dumps({\"frame\": s.CSP_FRAME_SRC, \"media\": s.CSP_MEDIA_SRC, \"default\": s.CSP_DEFAULT_SRC,\n" +
			"  \"cookie\": s.SESSION_COOKIE_NAME, \"argv\": sys.argv[1:]}))\n" +
			// Kolibri never returns while its input is open; a stand-in that
			// did would stop Python at exit, its input still being read.
			"sys.stdout.flush()\nos._exit(0)\n",
	}
	for name, body := range files {
		p := filepath.Join(app, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := command(Options{Runtime: Runtime{Python: python, App: app}, Home: t.TempDir(), ContentPort: 8471}, "start", "--foreground")
	// Standard input stays open while it runs: its end tells Kolibri that
	// Arcana is gone.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	cmd.Stdin = r
	out, err := cmd.Output()
	r.Close()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	want := `{"frame": ["'self'", "data:", "8471"], "media": ["'self'", "data:", "8471"], "default": ["'self'", "data:"], "cookie": "kolibri", "argv": ["start", "--foreground"]}`
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestFixCSP(t *testing.T) {
	in := "default-src 'self' data: blob:; script-src 'self' blob:; frame-src 'self' data: blob: 8471; media-src 'self' data: blob: 8471"
	got := FixCSP(in, 8471, "https://10.0.0.2:8471")
	want := "default-src 'self' data: blob:; script-src 'self' blob:; frame-src 'self' data: blob: https://10.0.0.2:8471; media-src 'self' data: blob: https://10.0.0.2:8471"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	// Without the bare port (an origin Kolibri already knows), frame-src
	// and media-src still get the content origin, once.
	got = FixCSP("frame-src 'self'; media-src 'self' https://10.0.0.2:8471", 8471, "https://10.0.0.2:8471")
	if got != "frame-src 'self' https://10.0.0.2:8471; media-src 'self' https://10.0.0.2:8471" {
		t.Fatalf("got %s", got)
	}
}

func TestPasswordAndFacilityName(t *testing.T) {
	p := NewPassword()
	if len(p) != 19 || strings.Count(p, "-") != 3 || strings.ContainsAny(p, "0O1lI") {
		t.Fatalf("password %q", p)
	}
	if NewPassword() == p {
		t.Fatal("passwords repeat")
	}
	if FacilityName("  ") != "Arcana" || len([]rune(FacilityName(strings.Repeat("é", 200)))) != 90 {
		t.Fatal("facility name")
	}
}

func TestProxyGuards(t *testing.T) {
	post := func(h map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "http://10.0.0.2:8472/api/auth/session/", nil)
		for k, v := range h {
			r.Header.Set(k, v)
		}
		return r
	}
	for _, c := range []struct {
		h  map[string]string
		ok bool
	}{
		{map[string]string{"Sec-Fetch-Site": "same-origin"}, true},
		{map[string]string{"Sec-Fetch-Site": "same-site"}, false}, // an archive on the content port
		{map[string]string{"Sec-Fetch-Site": "cross-site"}, false},
		{map[string]string{"Origin": "http://10.0.0.2:8472"}, true},
		{map[string]string{"Origin": "http://10.0.0.2:8471"}, false},
		{map[string]string{}, true},
	} {
		if got := sameOriginWrite(post(c.h)); got != c.ok {
			t.Errorf("%v: got %v", c.h, got)
		}
	}
	if !sameOriginWrite(httptest.NewRequest(http.MethodGet, "http://10.0.0.2:8472/", nil)) {
		t.Error("a read was refused")
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Cookie", "arcana_session=secret; sessionid=k; csrftoken=c")
	dropCookies(r, []string{"arcana_session"})
	if got := r.Header.Get("Cookie"); got != "sessionid=k; csrftoken=c" {
		t.Fatalf("cookies %q", got)
	}
}

// New options replace the running Kolibri with one started with them,
// after the first has exited (the slot); Stop returns at once.
func TestServerRestartsWithNewOptions(t *testing.T) {
	opts, _ := fakeOptions(t)
	s := NewServer(nil)
	defer s.Close()
	s.Start(opts)
	waitState(t, s, "running")
	opts.ContentPort = 9999
	s.Start(opts)
	waitState(t, s, "running")
	env, _ := os.ReadFile(filepath.Join(opts.Home, "fake-env.txt"))
	if !strings.Contains(string(env), "KOLIBRI_ZIP_CONTENT_ORIGIN=9999") {
		t.Fatal("not restarted with the new content port")
	}
	start := time.Now()
	s.Stop()
	if time.Since(start) > time.Second {
		t.Fatalf("Stop waited %s", time.Since(start))
	}
	if st := s.Status(); st.State != "stopped" {
		t.Fatalf("state %s", st.State)
	}
}

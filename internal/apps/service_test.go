package apps

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMain doubles as a stand-in program: with ARCANA_APPS_FAKE set, the
// test binary serves HTTP on the port it names, as a native app would.
func TestMain(m *testing.M) {
	if port := os.Getenv("ARCANA_APPS_FAKE"); port != "" {
		if os.Getenv("ARCANA_APPS_FAKE_FAIL") != "" {
			fmt.Println("panic: the stand-in was told to fail")
			os.Exit(2)
		}
		cwd, _ := os.Getwd()
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, "fake %s %s host=%s fwd=%s cookie=%q cwd=%s", r.Method, r.URL.Path, r.Host, r.Header.Get("X-Forwarded-For"), r.Header.Get("Cookie"), cwd)
		})
		fmt.Println(http.ListenAndServe("127.0.0.1:"+port, nil))
		os.Exit(1)
	}
	os.Exit(m.Run())
}

func fakeSpec(id string, slot int, fail bool) *Spec {
	return &Spec{ID: id, Title: "Fake " + id, Kind: KindService, Slot: slot, Parts: map[string]Part{"bin": {Copy: true}},
		Service: &ServiceSpec{ReadyPath: "/", StartTimeout: 20 * time.Second, AdminUser: "admin",
			Command: func(env Env) (Command, error) {
				e := []string{fmt.Sprintf("ARCANA_APPS_FAKE=%d", env.Port), "ARCANA_PASSWORD=" + env.Password}
				if fail {
					e = append(e, "ARCANA_APPS_FAKE_FAIL=1")
				}
				return Command{Path: os.Args[0], Args: []string{"-test.run=^$"}, Env: e}, nil
			}}}
}

func readyService(t *testing.T, spec *Spec) *Manager {
	t.Helper()
	registry[spec.ID] = spec
	t.Cleanup(func() { delete(registry, spec.ID) })
	bin := filepath.Join(t.TempDir(), "bin")
	os.WriteFile(bin, []byte("x"), 0o644)
	data := t.TempDir()
	m := &Manager{Cache: t.TempDir(), Platform: "linux-amd64", Data: func(id string) string { return filepath.Join(data, id) },
		StateFile: filepath.Join(t.TempDir(), "apps.json")}
	t.Cleanup(m.Close)
	m.Sync([]Installed{{ResourceID: spec.ResourceID(), Version: "1", Files: []Packed{packed(t, bin, "bin_any_1")}}})
	waitFor(t, func() bool { st := m.Status(); return len(st) == 1 && st[0].State == "ready" })
	return m
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestServiceStartsOnDemandAndProxies(t *testing.T) {
	spec := fakeSpec("fakesvc", 93, false)
	m := readyService(t, spec)
	if st := m.Status()[0]; st.Service == nil || st.Service.State != "stopped" {
		t.Fatalf("before first use: %+v", st.Service)
	}
	srv := httptest.NewServer(m.ServiceHandler(spec))
	defer srv.Close()
	get := func() (int, string) {
		resp, err := http.Get(srv.URL + "/inside?q=1")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	// The first request starts the program and answers that it is starting.
	if code, body := get(); code != http.StatusServiceUnavailable || !strings.Contains(body, "starting") {
		t.Fatalf("first request: %d %q", code, body)
	}
	waitFor(t, func() bool { return m.Status()[0].Service.State == "running" })
	code, body := get()
	if code != 200 || !strings.HasPrefix(body, "fake GET /inside") || !strings.Contains(body, "fwd=127.0.0.1") {
		t.Fatalf("proxied: %d %q", code, body)
	}
	// The program runs in its data folder, made on the drive.
	if cwd := body[strings.Index(body, "cwd=")+4:]; filepath.Base(cwd) != spec.ID {
		t.Fatalf("working folder: %q", body)
	}
	// A password was made once, kept, and shown.
	pw := m.Password(spec.ID)
	if len(pw) != 17 || pw[8] != '-' {
		t.Fatalf("password %q", pw)
	}
	// A form posted from another origin is refused.
	req, _ := http.NewRequest("POST", srv.URL+"/", strings.NewReader("x"))
	req.Header.Set("Sec-Fetch-Site", "same-site")
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin post: %v %v", resp, err)
	}
	// The added CSP keeps the app's pages inside its origin.
	resp, _ := http.Get(srv.URL + "/")
	resp.Body.Close()
	if !strings.Contains(strings.Join(resp.Header.Values("Content-Security-Policy"), "|"), "connect-src 'self'") {
		t.Fatalf("CSP %q", resp.Header.Values("Content-Security-Policy"))
	}
	m.Stop(spec.ID)
	if st := m.Status()[0].Service.State; st != "stopped" {
		t.Fatalf("after stop: %s", st)
	}
	// Removing the app stops its program and forgets it.
	m.Sync(nil)
	if len(m.Status()) != 0 {
		t.Fatal("still listed")
	}
}

func TestServiceFailureIsReported(t *testing.T) {
	spec := fakeSpec("failsvc", 94, true)
	m := readyService(t, spec)
	if err := m.Start(spec.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return m.Status()[0].Service.State == "failed" })
	if e := m.Status()[0].Service.LastError; !strings.Contains(e, "panic: the stand-in was told to fail") {
		t.Fatalf("error %q", e)
	}
	// A request soon after does not restart it in a loop.
	rec := httptest.NewRecorder()
	m.ServiceHandler(spec).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "could not start") {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestNetworkSetting(t *testing.T) {
	spec := fakeSpec("netsvc", 95, false)
	m := readyService(t, spec)
	if m.NetworkAllowed(spec.ID) {
		t.Fatal("allowed by default")
	}
	if err := m.SetNetwork(spec.ID, true); err != nil {
		t.Fatal(err)
	}
	// Kept in the state file.
	m2 := &Manager{StateFile: m.StateFile}
	if !m2.NetworkAllowed(spec.ID) {
		t.Fatal("not kept")
	}
}

func TestReadOnlyLibraryRunsNoPrograms(t *testing.T) {
	spec := fakeSpec("rosvc", 96, false)
	registry[spec.ID] = spec
	defer delete(registry, spec.ID)
	m := &Manager{Cache: t.TempDir(), Platform: "linux-amd64", ReadOnly: true}
	m.Sync([]Installed{{ResourceID: spec.ResourceID(), Files: []Packed{{Name: "bin_any_1"}}}})
	if st := m.Status()[0]; st.State != "unavailable" || !strings.Contains(st.Problem, "read-only") {
		t.Fatalf("%+v", st)
	}
}

func TestJellyfinNetworkSettings(t *testing.T) {
	path := t.TempDir() + "/config/network.xml"
	if err := writeJellyfinNetwork(path, 41000); err != nil {
		t.Fatal(err)
	}
	// The owner's own settings survive; Arcana's are replaced.
	data, _ := os.ReadFile(path)
	edited := strings.Replace(string(data), "</NetworkConfiguration>", "  <BaseUrl>/media</BaseUrl>\n  <AutoDiscovery>true</AutoDiscovery>\n</NetworkConfiguration>", 1)
	edited = strings.Replace(edited, "<AutoDiscovery>false</AutoDiscovery>", "", 1)
	os.WriteFile(path, []byte(edited), 0o644)
	if err := writeJellyfinNetwork(path, 42000); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	s := string(data)
	for _, want := range []string{"<InternalHttpPort>42000</InternalHttpPort>", "<BaseUrl>/media</BaseUrl>", "<AutoDiscovery>false</AutoDiscovery>",
		"<LocalNetworkAddresses><string>127.0.0.1</string></LocalNetworkAddresses>"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in\n%s", want, s)
		}
	}
	if strings.Count(s, "<AutoDiscovery>") != 1 || strings.Contains(s, "41000") {
		t.Errorf("duplicated or stale settings:\n%s", s)
	}
}

func TestSecretFileIsKept(t *testing.T) {
	p := t.TempDir() + "/pepper"
	a, err := secretFile(p)
	if err != nil || len(a) < 32 {
		t.Fatalf("%q %v", a, err)
	}
	if b, _ := secretFile(p); b != a {
		t.Fatal("secret changed")
	}
}

func TestFreePortIsLoopback(t *testing.T) {
	p, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
}

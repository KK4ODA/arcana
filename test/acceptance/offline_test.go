//go:build acceptance && linux

// Package acceptance contains Arcana's offline acceptance test.
//
// The test prepares a library "drive", then re-runs itself inside a new
// Linux network namespace (unshare -rn) that has no route to the internet:
// only loopback and a second local address standing in for a LAN client.
// Any feature that silently depends on the internet fails here.
//
//	go test -tags acceptance -v ./test/acceptance
//
// Set ARCANA_ACCEPTANCE_ONLINE=1 to also prepare the drive with a real
// download from the Kiwix catalog before disconnecting.
package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const lanClientIP = "10.99.0.1"

var repoRoot = func() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(f), "..", "..")
}()

func TestMain(m *testing.M) {
	if os.Getenv("ARCANA_IN_NETNS") == "1" {
		if err := configureNamespace(); err != nil {
			fmt.Fprintln(os.Stderr, "configure network namespace:", err)
			os.Exit(1)
		}
		os.Exit(m.Run())
	}
	work, err := os.MkdirTemp("", "arcana-acceptance-")
	if err != nil {
		panic(err)
	}
	if err := prepare(work); err != nil {
		fmt.Fprintln(os.Stderr, "prepare:", err)
		os.Exit(1)
	}
	// Step 2: disconnect the internet by re-running inside an isolated
	// network namespace.
	args := append([]string{"-rn", os.Args[0]}, os.Args[1:]...)
	cmd := exec.Command("unshare", args...)
	cmd.Env = append(os.Environ(), "ARCANA_IN_NETNS=1", "ARCANA_ACCEPTANCE_WORK="+work)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	err = cmd.Run()
	code := 0
	if err != nil {
		code = 1
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		}
		fmt.Fprintln(os.Stderr, "acceptance run failed; work directory kept at", work)
	} else {
		os.RemoveAll(work)
	}
	os.Exit(code)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// prepare is step 1: build Arcana and prepare a drive with a sample library.
func prepare(work string) error {
	bin := filepath.Join(work, "arcana")
	build := exec.Command("go", "build", "-o", bin, "./cmd/arcana")
	build.Dir = repoRoot
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		return err
	}
	drive := filepath.Join(work, "driveA", "Arcana")
	if err := run(bin, "init", drive, "--label", "Acceptance"); err != nil {
		return err
	}
	plat := runtime.GOOS + "-" + runtime.GOARCH
	kiwixSrc := filepath.Join(repoRoot, "third_party", "kiwix", plat, "kiwix", "kiwix-serve")
	if _, err := os.Stat(kiwixSrc); err != nil {
		return fmt.Errorf("kiwix-serve missing; run scripts/fetch-kiwix.sh first")
	}
	kdir := filepath.Join(drive, "bin", plat, "kiwix")
	os.MkdirAll(kdir, 0o755)
	data, _ := os.ReadFile(kiwixSrc)
	if err := os.WriteFile(filepath.Join(kdir, "kiwix-serve"), data, 0o755); err != nil {
		return err
	}
	if err := run(bin, "import", "--library", drive, filepath.Join(repoRoot, "testdata", "sample", "arcana-sample_en_2026-11.zim")); err != nil {
		return err
	}
	if os.Getenv("ARCANA_ACCEPTANCE_ONLINE") == "1" {
		if err := run(bin, "catalog", "refresh", "--library", drive); err != nil {
			return err
		}
		// A real map region, extracted from the newest planet build.
		if err := run(bin, "get", "--library", drive, "kiwix.wikipedia_en_ray-charles_mini", "maps.country-lu-z10"); err != nil {
			return err
		}
		return nil
	}
	// Offline preparation: a synthetic map installed as the Luxembourg
	// overview, so the map pipeline (tiles, glyphs, place index) can be
	// exercised without any download.
	mapFile := filepath.Join(work, "country-lu-z10.pmtiles")
	if err := writeSampleMap(mapFile); err != nil {
		return err
	}
	return run(bin, "import", "--library", drive, "--id", "maps.country-lu-z10", "--allow-unverified", mapFile)
}

func mapPlaceQuery() (query, want string) {
	if os.Getenv("ARCANA_ACCEPTANCE_ONLINE") == "1" {
		return "esch", "Esch-sur-Alzette"
	}
	return "arcana test", sampleTown
}

// configureNamespace brings up loopback and adds a second address that
// plays the role of another device on the local network.
func configureNamespace() error {
	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	up := func(name string) error {
		ifr, err := unix.NewIfreq(name)
		if err != nil {
			return err
		}
		if err := unix.IoctlIfreq(fd, unix.SIOCGIFFLAGS, ifr); err != nil {
			return err
		}
		ifr.SetUint16(ifr.Uint16() | unix.IFF_UP | unix.IFF_RUNNING)
		return unix.IoctlIfreq(fd, unix.SIOCSIFFLAGS, ifr)
	}
	if err := up("lo"); err != nil {
		return err
	}
	alias, _ := unix.NewIfreq("lo:1")
	if err := alias.SetInet4Addr(net.ParseIP(lanClientIP).To4()); err != nil {
		return err
	}
	if err := unix.IoctlIfreq(fd, unix.SIOCSIFADDR, alias); err != nil {
		return err
	}
	return up("lo:1")
}

// ------------------------------------------------------------------ steps

type instance struct {
	t    *testing.T
	cmd  *exec.Cmd
	port int
	log  string
	tok  string
	// lanTok is the session token of a signed-in network client.
	lanTok string
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func launch(t *testing.T, bin, drive, logPath string) *instance {
	t.Helper()
	port := freePort(t)
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "serve", "--library", drive, "--no-browser", "--lan", "--port", fmt.Sprint(port))
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	in := &instance{t: t, cmd: cmd, port: port, log: logPath}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var s map[string]any
		if err := in.getJSON("/api/session", &s); err == nil {
			in.tok, _ = s["token"].(string)
			// Wait for the archive reader.
			var st struct {
				Offline struct {
					Ready bool `json:"ready"`
				} `json:"offline"`
			}
			if in.getJSON("/api/status", &st) == nil && st.Offline.Ready {
				return in
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	logData, _ := os.ReadFile(logPath)
	t.Fatalf("Arcana did not become ready:\n%s", logData)
	return nil
}

func (in *instance) url(p string) string { return fmt.Sprintf("http://127.0.0.1:%d%s", in.port, p) }

func (in *instance) getJSON(p string, v any) error {
	resp, err := http.Get(in.url(p))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s: %s", p, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func (in *instance) send(method, p, body string) (int, string) {
	req, _ := http.NewRequest(method, in.url(p), strings.NewReader(body))
	req.Header.Set("X-Arcana-Token", in.tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		in.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (in *instance) kill() {
	_ = in.cmd.Process.Signal(syscall.SIGKILL)
	_, _ = in.cmd.Process.Wait()
}

func (in *instance) stop() {
	_ = in.cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() { _, _ = in.cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		in.kill()
	}
}

// childPIDs lists processes named name whose parent is ppid (Linux /proc).
func childPIDs(ppid int, name string) []int {
	var out []int
	entries, _ := os.ReadDir("/proc")
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		// pid (comm) state ppid ...
		st := string(data)
		open, close := strings.IndexByte(st, '('), strings.LastIndexByte(st, ')')
		if open < 0 || close < open {
			continue
		}
		fields := strings.Fields(st[close+1:])
		if len(fields) < 2 || st[open+1:close] != name {
			continue
		}
		if pp, _ := strconv.Atoi(fields[1]); pp == ppid {
			out = append(out, pid)
		}
	}
	return out
}

// alive reports whether pid exists and is not a zombie.
func alive(pid int) bool {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	st := string(data)
	if i := strings.LastIndexByte(st, ')'); i >= 0 && i+2 < len(st) {
		return st[i+2] != 'Z'
	}
	return true
}

type libraryResp struct {
	Resources []struct {
		ResourceID string `json:"resourceId"`
		OpenPath   string `json:"openPath"`
	} `json:"resources"`
}

func browseAndSearch(t *testing.T, in *instance) {
	t.Helper()
	var lib libraryResp
	if err := in.getJSON("/api/library", &lib); err != nil {
		t.Fatal(err)
	}
	var sample string
	for _, r := range lib.Resources {
		if r.ResourceID == "arcana.sample" {
			sample = r.OpenPath
		}
	}
	if sample == "" {
		t.Fatalf("sample not browsable: %+v", lib)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d%sphonetic_alphabet", in.port+1, sample))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Foxtrot") {
		t.Fatalf("article: %d", resp.StatusCode)
	}
	var sr struct {
		Total int    `json:"total"`
		Error string `json:"error"`
	}
	if err := in.getJSON("/api/search?q=distress", &sr); err != nil || sr.Total == 0 {
		t.Fatalf("search: %v %+v", err, sr)
	}
}

func TestOfflineAcceptance(t *testing.T) {
	work := os.Getenv("ARCANA_ACCEPTANCE_WORK")
	bin := filepath.Join(work, "arcana")
	drive := filepath.Join(work, "driveA", "Arcana")
	logPath := filepath.Join(work, "arcana.log")

	t.Run("02-internet-is-disconnected", func(t *testing.T) {
		if c, err := net.DialTimeout("tcp", "1.1.1.1:443", 2*time.Second); err == nil {
			c.Close()
			t.Fatal("the test namespace can reach the internet")
		}
		if _, err := net.LookupHost("library.kiwix.org"); err == nil {
			t.Fatal("DNS works in the test namespace")
		}
	})

	var in *instance
	t.Run("03-launch-without-internet", func(t *testing.T) {
		in = launch(t, bin, drive, logPath)
	})
	if in == nil {
		t.FailNow()
	}

	t.Run("04-browse-and-search", func(t *testing.T) {
		browseAndSearch(t, in)
		var cat struct {
			Total int `json:"total"`
		}
		if err := in.getJSON("/api/catalog", &cat); err != nil || cat.Total == 0 {
			t.Fatalf("catalog not browsable offline: %v", err)
		}
		var ups map[string]any
		if err := in.getJSON("/api/updates", &ups); err != nil {
			t.Fatalf("updates view must work offline: %v", err)
		}
	})

	t.Run("05-offline-maps", func(t *testing.T) {
		var ms struct {
			Sets []struct {
				ResourceID string     `json:"resourceId"`
				Bounds     [4]float64 `json:"bounds"`
				MaxZoom    int        `json:"maxZoom"`
				Error      string     `json:"error"`
			} `json:"sets"`
			Indexing string `json:"indexing"`
		}
		query, want := mapPlaceQuery()
		var found bool
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) && !found {
			if err := in.getJSON("/api/maps", &ms); err != nil {
				t.Fatal(err)
			}
			var sr struct {
				Places []struct {
					Name string  `json:"name"`
					Lon  float64 `json:"lon"`
					Lat  float64 `json:"lat"`
				} `json:"places"`
			}
			if err := in.getJSON("/api/maps/search?q="+strings.ReplaceAll(query, " ", "+"), &sr); err != nil {
				t.Fatal(err)
			}
			for _, p := range sr.Places {
				found = found || p.Name == want
			}
			if !found {
				time.Sleep(300 * time.Millisecond)
			}
		}
		if len(ms.Sets) != 1 || ms.Sets[0].Error != "" || ms.Sets[0].MaxZoom != 10 {
			t.Fatalf("map sets: %+v", ms.Sets)
		}
		if !found {
			t.Fatalf("place search did not find %q offline", want)
		}
		get := func(p string) (int, int) {
			resp, err := http.Get(in.url(p))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			return resp.StatusCode, len(b)
		}
		// Native tile at the map's detail level, a tile derived from it
		// beyond that level, and everything the viewer needs.
		for _, p := range []string{"/maps/tiles/10/529/349.mvt", "/maps/tiles/13/4235/2792.mvt",
			"/maps/fonts/Noto%20Sans%20Regular/0-255.pbf", "/maps/layers-light.json", "/maps/sprites/light.json",
			"/vendor/maplibre-gl/maplibre-gl-csp.js", "/vendor/maplibre-gl/maplibre-gl-csp-worker.js"} {
			if code, n := get(p); code != 200 || n == 0 {
				t.Fatalf("%s: %d (%d bytes)", p, code, n)
			}
		}
		if code, _ := get("/maps/tiles/12/0/0.mvt"); code != 204 {
			t.Fatalf("tile outside every map: %d, want 204", code)
		}
		// Measuring a new area needs the internet; offline it must fail
		// promptly with an explanation, not hang.
		start := time.Now()
		code, body := in.send("POST", "/api/maps/plan", `{"name":"x","bbox":[6,49.5,6.2,49.7],"maxZoom":13}`)
		if code == 200 || time.Since(start) > 40*time.Second || !strings.Contains(body, "error") {
			t.Fatalf("offline area planning: %d after %s: %s", code, time.Since(start), body)
		}
	})

	t.Run("06-simulated-gps", func(t *testing.T) {
		type gpsStatus struct {
			State string `json:"state"`
			Fix   *struct {
				Lat  float64 `json:"lat"`
				Lon  float64 `json:"lon"`
				Mode string  `json:"mode"`
			} `json:"fix"`
			Recording bool `json:"recording"`
			Points    int  `json:"points"`
		}
		wait := func(want string, check func(gpsStatus) bool) gpsStatus {
			var st gpsStatus
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				st = gpsStatus{}
				if err := in.getJSON("/api/gps", &st); err != nil {
					t.Fatal(err)
				}
				if st.State == want && (check == nil || check(st)) {
					return st
				}
				time.Sleep(200 * time.Millisecond)
			}
			t.Fatalf("GPS state %+v, want %s", st, want)
			return st
		}
		// The simulator replays NMEA through the same parser as a
		// receiver; with no track given it circles the installed map.
		if code, body := in.send("PUT", "/api/settings", `{"gps":{"source":"simulator","simulatorSpeed":10,"recordTrack":true}}`); code != 200 {
			t.Fatalf("settings: %d %s", code, body)
		}
		st := wait("current", func(s gpsStatus) bool { return s.Fix != nil && s.Points >= 2 })
		if st.Fix.Mode != "simulated" || math.Abs(st.Fix.Lat-49.8) > 0.5 || math.Abs(st.Fix.Lon-6.1) > 0.5 || !st.Recording {
			t.Fatalf("simulated fix %+v", st)
		}
		// Live updates arrive as server-sent events.
		resp, err := http.Get(in.url("/api/gps/stream"))
		if err != nil {
			t.Fatal(err)
		}
		line, _ := bufioReadLine(resp.Body)
		resp.Body.Close()
		if !strings.HasPrefix(line, "data: {") || !strings.Contains(line, `"simulated"`) {
			t.Fatalf("stream: %q", line)
		}
		// Waypoints are saved to the drive and exported as GPX.
		if code, body := in.send("POST", "/api/gps/waypoints", `{"name":"Spring","lat":49.6,"lon":6.12}`); code != 200 {
			t.Fatalf("waypoint: %d %s", code, body)
		}
		r2, err := http.Get(in.url("/api/gps/export"))
		if err != nil {
			t.Fatal(err)
		}
		gpx, _ := io.ReadAll(r2.Body)
		r2.Body.Close()
		if !strings.Contains(string(gpx), "<name>Spring</name>") {
			t.Fatalf("GPX export: %s", gpx)
		}
		// A network receiver: NMEA over UDP on the loopback interface.
		port := freeUDPPort(t)
		if code, body := in.send("PUT", "/api/settings", fmt.Sprintf(`{"gps":{"source":"udp","udpPort":%d}}`, port)); code != 200 {
			t.Fatalf("settings: %d %s", code, body)
		}
		wait("waiting", nil)
		conn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		now := time.Now().UTC()
		rmc := nmeaChecksum(fmt.Sprintf("GPRMC,%s,A,4936.6600,N,00607.8000,E,0.5,10.0,%s,,,A", now.Format("150405.00"), now.Format("020106")))
		fmt.Fprintf(conn, "%s\r\n", rmc)
		st = wait("current", nil)
		if math.Abs(st.Fix.Lat-49.611) > 1e-3 || math.Abs(st.Fix.Lon-6.13) > 1e-3 {
			t.Fatalf("UDP fix %+v", st.Fix)
		}
		// The position is private to the host unless shared.
		dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(lanClientIP)}}
		client := &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}}
		r3, err := client.Get(fmt.Sprintf("http://%s:%d/api/gps", lanClientIP, in.port))
		if err != nil {
			t.Fatal(err)
		}
		r3.Body.Close()
		if r3.StatusCode != 403 {
			t.Fatalf("LAN client read the position: %s", r3.Status)
		}
		if code, body := in.send("PUT", "/api/settings", `{"gps":{"source":""}}`); code != 200 {
			t.Fatalf("settings: %d %s", code, body)
		}
		wait("off", nil)
	})

	t.Run("07-second-client-on-local-network", func(t *testing.T) {
		dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(lanClientIP)}}
		client := &http.Client{Transport: &http.Transport{DialContext: dialer.DialContext}}
		base := fmt.Sprintf("http://%s:%d", lanClientIP, in.port)
		resp, err := client.Get(base + "/api/library")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("LAN read: %s", resp.Status)
		}
		resp, err = client.Get(base + "/api/session")
		if err != nil {
			t.Fatal(err)
		}
		var s map[string]any
		json.NewDecoder(resp.Body).Decode(&s)
		resp.Body.Close()
		if _, ok := s["token"]; ok {
			t.Fatal("LAN client received the admin token")
		}
		req, _ := http.NewRequest("POST", base+"/api/catalog/refresh", nil)
		req.Header.Set("X-Arcana-Token", in.tok) // even a stolen token must not help
		resp, err = client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 403 {
			t.Fatalf("LAN client could modify the library: %s", resp.Status)
		}
		// The LAN client can read an article from the content server.
		var lib libraryResp
		r2, _ := client.Get(base + "/api/library")
		json.NewDecoder(r2.Body).Decode(&lib)
		r2.Body.Close()
		resp, err = client.Get(fmt.Sprintf("http://%s:%d%s", lanClientIP, in.port+1, lib.Resources[0].OpenPath))
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("LAN content: %v %v", err, resp)
		}
		resp.Body.Close()
	})

	t.Run("07b-remote-administration", func(t *testing.T) {
		jar, _ := cookiejar.New(nil)
		dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(lanClientIP)}}
		client := &http.Client{Jar: jar, Transport: &http.Transport{DialContext: dialer.DialContext}}
		base := fmt.Sprintf("http://%s:%d", lanClientIP, in.port)
		post := func(method, p, body, tok string) int {
			req, _ := http.NewRequest(method, base+p, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if tok != "" {
				req.Header.Set("X-Arcana-Token", tok)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if p == "/api/auth/login" && resp.StatusCode == 200 {
				var lr struct {
					Token string `json:"token"`
				}
				json.NewDecoder(resp.Body).Decode(&lr)
				in.lanTok = lr.Token
			}
			return resp.StatusCode
		}
		if code := post("POST", "/api/auth/login", `{"password":"correct horse battery"}`, ""); code != 403 {
			t.Fatalf("login before a password exists: %d", code)
		}
		if code, body := in.send("PUT", "/api/auth/password", `{"password":"correct horse battery"}`); code != 200 {
			t.Fatalf("set password: %d %s", code, body)
		}
		if code := post("POST", "/api/auth/login", `{"password":"wrong guess here"}`, ""); code != 401 {
			t.Fatalf("wrong password: %d", code)
		}
		if code := post("POST", "/api/auth/login", `{"password":"correct horse battery"}`, ""); code != 200 {
			t.Fatalf("login: %d", code)
		}
		if code := post("PUT", "/api/settings", `{"maxConcurrentDownloads":3}`, in.lanTok); code != 200 {
			t.Fatalf("signed-in change: %d", code)
		}
		if code := post("POST", "/api/transfer/export", `{}`, in.lanTok); code != 403 {
			t.Fatalf("host-only action from the network: %d", code)
		}
		post("POST", "/api/auth/logout", `{}`, in.lanTok)
		if code := post("PUT", "/api/settings", `{"maxConcurrentDownloads":2}`, in.lanTok); code != 403 {
			t.Fatalf("change after sign-out: %d", code)
		}
		if code, _ := in.send("DELETE", "/api/auth/password", ``); code != 200 {
			t.Fatal("could not turn remote administration off")
		}
	})

	t.Run("07c-interface-makes-no-external-requests", func(t *testing.T) {
		node, err := exec.LookPath("node")
		if err != nil {
			t.Skip("node not installed")
		}
		script := filepath.Join(repoRoot, "test", "acceptance", "ui-tripwire.js")
		out, err := exec.Command(node, script, in.url("/")).CombinedOutput()
		if strings.Contains(string(out), "Cannot find module 'playwright'") {
			t.Skip("playwright not installed")
		}
		if err != nil {
			t.Fatalf("UI check failed: %v\n%s", err, out)
		}
		t.Logf("%s", out)
	})

	t.Run("08-restart-host-abruptly", func(t *testing.T) {
		code, body := in.send("PUT", "/api/settings", `{"reserveBytes":7000000000}`)
		if code != 200 {
			t.Fatalf("settings: %d %s", code, body)
		}
		children := childPIDs(in.cmd.Process.Pid, "kiwix-serve")
		if len(children) == 0 {
			t.Fatal("no kiwix-serve child process found")
		}
		in.kill() // power loss: no shutdown handlers run
		// kiwix-serve must not outlive Arcana.
		deadline := time.Now().Add(10 * time.Second)
		for _, pid := range children {
			for alive(pid) {
				if time.Now().After(deadline) {
					t.Fatalf("kiwix-serve (pid %d) kept running after Arcana was killed", pid)
				}
				time.Sleep(200 * time.Millisecond)
			}
		}
	})

	moved := filepath.Join(work, "driveB", "Arcana")
	t.Run("09-reconnect-drive-at-new-mount-point", func(t *testing.T) {
		if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(drive, moved); err != nil {
			t.Fatal(err)
		}
		in = launch(t, bin, moved, logPath)
	})

	t.Run("10-content-and-settings-intact", func(t *testing.T) {
		browseAndSearch(t, in)
		var set struct {
			Settings struct {
				ReserveBytes int64 `json:"reserveBytes"`
			} `json:"settings"`
			LibraryRoot string `json:"libraryRoot"`
		}
		if err := in.getJSON("/api/settings", &set); err != nil {
			t.Fatal(err)
		}
		if set.Settings.ReserveBytes != 7000000000 {
			t.Fatalf("setting lost after power loss and move: %d", set.Settings.ReserveBytes)
		}
		if set.LibraryRoot != moved {
			t.Fatalf("library root %q", set.LibraryRoot)
		}
		in.stop()
		out, err := exec.Command(bin, "check", "--full", "--library", moved).CombinedOutput()
		if err != nil {
			t.Fatalf("integrity check failed:\n%s", out)
		}
		t.Logf("%s", out)
	})

	t.Run("11-recover-from-lost-metadata-database", func(t *testing.T) {
		for _, f := range []string{"arcana.db", "arcana.db-wal", "arcana.db-shm"} {
			os.Remove(filepath.Join(moved, "state", f))
		}
		in = launch(t, bin, moved, logPath)
		browseAndSearch(t, in)
	})

	t.Run("12-transfer-to-second-drive-offline", func(t *testing.T) {
		in.stop()
		exportDir := filepath.Join(work, "driveC", "ArcanaExport")
		if out, err := exec.Command(bin, "export", "--library", moved, "--to", exportDir, "--all").CombinedOutput(); err != nil {
			t.Fatalf("export: %v\n%s", err, out)
		}
		second := filepath.Join(work, "driveD", "Arcana")
		if out, err := exec.Command(bin, "init", second).CombinedOutput(); err != nil {
			t.Fatalf("init: %v\n%s", err, out)
		}
		if out, err := exec.Command(bin, "import", "--library", second, "--from", exportDir).CombinedOutput(); err != nil {
			t.Fatalf("import: %v\n%s", err, out)
		}
		out, err := exec.Command(bin, "check", "--full", "--library", second).CombinedOutput()
		if err != nil || !strings.Contains(string(out), "No problems") {
			t.Fatalf("second drive check: %v\n%s", err, out)
		}
		t.Logf("%s", out)
	})

	t.Run("13-no-internet-access-attempted", func(t *testing.T) {
		data, _ := os.ReadFile(logPath)
		for _, bad := range []string{"no such host", "dial tcp", "network is unreachable"} {
			if strings.Contains(string(data), bad) {
				t.Fatalf("Arcana attempted network access while offline (%q):\n%s", bad, data)
			}
		}
	})
	_ = context.Background
}

func bufioReadLine(r io.Reader) (string, error) {
	var b []byte
	buf := make([]byte, 1)
	for len(b) < 1<<16 {
		if _, err := r.Read(buf); err != nil {
			return string(b), err
		}
		if buf[0] == '\n' {
			break
		}
		b = append(b, buf[0])
	}
	return string(b), nil
}

func freeUDPPort(t *testing.T) int {
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

func nmeaChecksum(body string) string {
	var cs byte
	for i := 0; i < len(body); i++ {
		cs ^= body[i]
	}
	return fmt.Sprintf("$%s*%02X", body, cs)
}

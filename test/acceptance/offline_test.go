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
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
		if err := run(bin, "get", "--library", drive, "kiwix.wikipedia_en_ray-charles_mini"); err != nil {
			return err
		}
	}
	return nil
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
		t.Skip("NOT IMPLEMENTED: offline maps are planned for Phase 3")
	})
	t.Run("06-simulated-gps", func(t *testing.T) {
		t.Skip("NOT IMPLEMENTED: GPS input is planned for Phase 3")
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

	t.Run("07b-interface-makes-no-external-requests", func(t *testing.T) {
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
		in.kill() // power loss: no shutdown handlers run
		// kiwix-serve must not outlive Arcana.
		deadline := time.Now().Add(10 * time.Second)
		for {
			out, _ := exec.Command("pgrep", "-f", "kiwix-serve --address=127.0.0.1").Output()
			if len(strings.TrimSpace(string(out))) == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("kiwix-serve kept running after Arcana was killed")
			}
			time.Sleep(200 * time.Millisecond)
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
		defer in.stop()
		browseAndSearch(t, in)
	})

	t.Run("12-no-internet-access-attempted", func(t *testing.T) {
		data, _ := os.ReadFile(logPath)
		for _, bad := range []string{"no such host", "dial tcp", "network is unreachable"} {
			if strings.Contains(string(data), bad) {
				t.Fatalf("Arcana attempted network access while offline (%q):\n%s", bad, data)
			}
		}
	})
	_ = context.Background
}

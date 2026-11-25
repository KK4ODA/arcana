// Package kolibritest is a stand-in for Kolibri in tests. A test binary
// whose TestMain calls MaybeRun acts as Kolibri when Arcana starts it as
// "python … start --foreground --port=P --zip-port=Z": it serves the parts
// of Kolibri's API Arcana uses, provisions itself from
// KOLIBRI_AUTOMATIC_PROVISION_FILE, and exits when its standard input
// closes, as Arcana's bootstrap makes Kolibri do.
package kolibritest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Env marks a process started as the stand-in.
const Env = "ARCANA_TEST_FAKE_KOLIBRI"

// Python returns the interpreter path to give Arcana so it starts the
// stand-in: the test binary itself. Tests must also set Env=1.
func Python() string { return os.Args[0] }

// MaybeRun runs the stand-in and exits if this process is one.
func MaybeRun() {
	if os.Getenv(Env) != "1" {
		return
	}
	run(os.Args[1:])
	os.Exit(0)
}

type admin struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Facility string `json:"facility"`
}

type state struct {
	mu      sync.Mutex
	home    string
	admin   admin
	tasks   []map[string]any
	started time.Time
	// session is this process's session key: a restarted Kolibri does not
	// know the sessions of the one before, as when they expire.
	session string
}

func run(args []string) {
	var port, zipPort string
	for _, a := range args {
		if v, ok := strings.CutPrefix(a, "--port="); ok {
			port = v
		}
		if v, ok := strings.CutPrefix(a, "--zip-port="); ok {
			zipPort = v
		}
	}
	if port == "" || zipPort == "" || !contains(args, "start") {
		fmt.Fprintln(os.Stderr, "fake kolibri: expected start --port --zip-port")
		os.Exit(2)
	}
	if os.Getenv("KOLIBRI_LISTEN_ADDRESS") != "127.0.0.1" {
		fmt.Fprintln(os.Stderr, "fake kolibri: refusing to listen beyond loopback")
		os.Exit(2)
	}
	if os.Getenv("FAKE_KOLIBRI_FAIL") == "1" {
		fmt.Fprintln(os.Stderr, "Traceback: something broke")
		os.Exit(1)
	}
	// Kolibri's console output: coloured "LEVEL date time message" lines.
	logf := func(level, msg string) {
		fmt.Fprintf(os.Stdout, "\x1b[37m%-8s %s %s\x1b[0m\n", level, time.Now().Format("2006-01-02 15:04:05,000"), msg)
	}
	logf("INFO", "Running update routines for new version...")
	st := &state{home: os.Getenv("KOLIBRI_HOME"), started: time.Now()}
	st.session = strconv.FormatInt(st.started.UnixNano(), 36)
	adminFile := filepath.Join(st.home, "fake-admin.json")
	if data, err := os.ReadFile(adminFile); err == nil {
		_ = json.Unmarshal(data, &st.admin)
	} else if p := os.Getenv("KOLIBRI_AUTOMATIC_PROVISION_FILE"); p != "" {
		var prov struct {
			Facility  string `json:"facility_name"`
			Preset    string `json:"preset"`
			Superuser admin  `json:"superuser"`
			Device    struct {
				Guest bool `json:"allow_guest_access"`
			} `json:"device_settings"`
		}
		data, err := os.ReadFile(p)
		if err != nil || json.Unmarshal(data, &prov) != nil || prov.Preset != "formal" || !prov.Device.Guest {
			fmt.Fprintln(os.Stderr, "fake kolibri: bad provisioning file")
			os.Exit(2)
		}
		st.admin = prov.Superuser
		st.admin.Facility = prov.Facility
		data, _ = json.Marshal(st.admin)
		_ = os.WriteFile(adminFile, data, 0o600)
	}
	_ = os.WriteFile(filepath.Join(st.home, "fake-env.txt"), []byte(strings.Join(os.Environ(), "\n")), 0o600)

	zipOrigin := os.Getenv("KOLIBRI_ZIP_CONTENT_ORIGIN")
	prefix := "/" + strings.Trim(os.Getenv("KOLIBRI_ZIP_CONTENT_URL_PATH_PREFIX"), "/") + "/"
	main := http.NewServeMux()
	main.HandleFunc("/api/public/info/", func(w http.ResponseWriter, r *http.Request) {
		if os.Getenv("FAKE_KOLIBRI_BROKEN") == "1" {
			// As Kolibri 0.19.5 did with ZIP_CONTENT_ORIGIN set to a port
			// and its own settings: every response failed.
			logf("ERROR", "Internal Server Error: /api/public/info/")
			fmt.Fprintln(os.Stdout, "Traceback (most recent call last):")
			fmt.Fprintln(os.Stdout, `  File "csp/utils.py", line 92, in build_policy`)
			fmt.Fprintln(os.Stdout, "TypeError: sequence item 3: expected str instance, int found")
			logf("INFO", `127.0.0.1 - - "GET /api/public/info/" 500 0 "" "Go-http-client/1.1"`)
			http.Error(w, "", http.StatusInternalServerError)
			return
		}
		if d, _ := time.ParseDuration(os.Getenv("FAKE_KOLIBRI_SLOW")); time.Since(st.started) < d {
			http.Error(w, "starting", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprintf(w, `{"application":"kolibri","kolibri_version":"0.19.5-fake","device_name":%q}`, st.admin.Facility)
	})
	// Cookies are named as Kolibri names them (SESSION_COOKIE_NAME and
	// CSRF_COOKIE_NAME), not Django's defaults. As in Kolibri, the CSRF token
	// is required of requests from a valid session (Django REST framework's
	// SessionAuthentication), signing in again included; a request with no
	// session or an expired one needs none.
	csrfOK := func(r *http.Request) bool {
		tok, err := r.Cookie("kolibri_csrftoken")
		return err == nil && r.Header.Get("X-CSRFToken") == tok.Value
	}
	validSession := func(r *http.Request) bool {
		c, err := r.Cookie("kolibri")
		return err == nil && c.Value == "s-"+st.admin.Username+"-"+st.session
	}
	main.HandleFunc("/api/auth/session/", func(w http.ResponseWriter, r *http.Request) {
		if validSession(r) && !csrfOK(r) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `<p>CSRF verification failed. Request aborted.</p>`)
			return
		}
		var in admin
		_ = json.NewDecoder(r.Body).Decode(&in)
		if r.Method != http.MethodPost || in.Username != st.admin.Username || in.Password != st.admin.Password {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `[{"id":"INVALID_CREDENTIALS","metadata":{}}]`)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "kolibri", Value: "s-" + in.Username + "-" + st.session, Path: "/", HttpOnly: true})
		http.SetCookie(w, &http.Cookie{Name: "kolibri_csrftoken", Value: "csrf-token", Path: "/"})
		fmt.Fprint(w, `{"id":"current","username":"`+in.Username+`","kind":["superuser"]}`)
	})
	signedIn := func(w http.ResponseWriter, r *http.Request) bool {
		if !validSession(r) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"detail":"not signed in"}`)
			return false
		}
		if r.Method == http.MethodPost && !csrfOK(r) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `<p>CSRF verification failed. Request aborted.</p>`)
			return false
		}
		return true
	}
	main.HandleFunc("/api/tasks/tasks/", func(w http.ResponseWriter, r *http.Request) {
		if !signedIn(w, r) {
			return
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		rest := strings.TrimPrefix(r.URL.Path, "/api/tasks/tasks/")
		switch {
		case r.Method == http.MethodPost && rest == "":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in["type"] != "kolibri.core.content.tasks.remoteimport" {
				http.Error(w, `[{"id":"INVALID"}]`, http.StatusBadRequest)
				return
			}
			t := map[string]any{"id": fmt.Sprintf("task%d", len(st.tasks)+1), "status": "RUNNING", "type": in["type"],
				"percentage": 0.25, "cancellable": true, "clearable": false, "exception": nil,
				"extra_metadata": map[string]any{"channel_id": in["channel_id"], "channel_name": in["channel_name"],
					"node_ids": in["node_ids"], "file_size": 1000.0, "transferred_file_size": 250.0}}
			st.tasks = append(st.tasks, t)
			_ = json.NewEncoder(w).Encode(t)
		case r.Method == http.MethodGet && rest == "":
			_ = json.NewEncoder(w).Encode(st.tasks)
		case r.Method == http.MethodPost && strings.HasSuffix(rest, "/cancel/"):
			id := strings.TrimSuffix(rest, "/cancel/")
			for _, t := range st.tasks {
				if t["id"] == id {
					t["status"] = "CANCELED"
					t["clearable"] = true
				}
			}
			fmt.Fprint(w, `{}`)
		case r.Method == http.MethodPost && strings.HasSuffix(rest, "/clear/"):
			id := strings.TrimSuffix(rest, "/clear/")
			var keep []map[string]any
			for _, t := range st.tasks {
				if t["id"] != id {
					keep = append(keep, t)
				}
			}
			st.tasks = keep
			fmt.Fprint(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	})
	main.HandleFunc("/device/api/device_channel/", func(w http.ResponseWriter, r *http.Request) {
		if !signedIn(w, r) {
			return
		}
		fmt.Fprint(w, `[{"id":"c9d7f950ab6b5a1199e3d6c10d7f0103","name":"Khan Academy (English - US curriculum)","version":5,"on_device_resources":42,"on_device_file_size":123456}]`)
	})
	main.HandleFunc("/api/content/contentnode/", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("keywords")
		fmt.Fprintf(w, `{"results":[{"id":"0123456789abcdef0123456789abcdef","title":"Fractions: %s","kind":"video","channel_id":"c9d7f950ab6b5a1199e3d6c10d7f0103"}]}`, q)
	})
	main.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/learn" {
			http.Redirect(w, r, "http://"+r.Host+"/en/learn/", http.StatusFound)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self' data: blob:; frame-src 'self' data: blob: "+zipOrigin+"; media-src 'self' data: blob: "+zipOrigin)
		fmt.Fprintf(w, "<!doctype html><title>Kolibri</title><p>Kolibri for %s, host %s, forwarded %s</p>", st.admin.Facility, r.Host, r.Header.Get("X-Forwarded-Host"))
	})
	zip := http.NewServeMux()
	zip.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "zip", Value: "x"})
		fmt.Fprintf(w, "zip %s cookie=%q", strings.TrimPrefix(r.URL.Path, prefix), r.Header.Get("Cookie"))
	})
	for _, srv := range []struct {
		port string
		h    http.Handler
	}{{port, main}, {zipPort, zip}} {
		l, err := net.Listen("tcp", "127.0.0.1:"+srv.port)
		if err != nil {
			fmt.Fprintln(os.Stderr, "fake kolibri:", err)
			os.Exit(1)
		}
		go func(l net.Listener, h http.Handler) { _ = http.Serve(l, h) }(l, srv.h)
	}
	fmt.Fprintln(os.Stderr, "Kolibri running on port "+strconv.Quote(port))
	// Stop when Arcana closes standard input.
	_, _ = io.Copy(io.Discard, bufio.NewReader(os.Stdin))
	fmt.Fprintln(os.Stderr, "stdin closed; stopping")
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

package kolibri

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearchAndInfo(t *testing.T) {
	paginated := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/public/info/":
			fmt.Fprint(w, `{"application":"kolibri","kolibri_version":"0.17.4","device_name":"Classroom Pi"}`)
		case "/api/content/contentnode/":
			if r.URL.Query().Get("keywords") != "fractions" || r.URL.Query().Get("max_results") != "5" {
				http.Error(w, "bad query", 400)
				return
			}
			items := `[{"id":"abc123","title":"Adding fractions","kind":"video","description":"` + strings.Repeat("Fractions. ", 40) + `","channel_id":"khan"},
				{"id":"top1","title":"Fractions","kind":"topic","channel_id":"khan"},{"id":"","title":"broken"}]`
			if paginated {
				fmt.Fprintf(w, `{"more":null,"results":%s}`, items)
			} else {
				fmt.Fprint(w, items)
			}
		case "/locked/api/content/contentnode/":
			w.WriteHeader(403)
		}
	}))
	defer srv.Close()
	base, err := Normalize(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{BaseURL: base}
	info, err := c.Info(context.Background())
	if err != nil || info.Version != "0.17.4" || info.DeviceName != "Classroom Pi" {
		t.Fatalf("info: %+v %v", info, err)
	}
	for _, p := range []bool{true, false} {
		paginated = p
		items, err := c.Search(context.Background(), "fractions", 5)
		if err != nil || len(items) != 2 {
			t.Fatalf("search (paginated=%v): %+v %v", p, items, err)
		}
		if items[0].URL != base+"/learn/#/topics/c/abc123" || items[1].URL != base+"/learn/#/topics/t/top1" || len([]rune(items[0].Description)) > 241 {
			t.Fatalf("items: %+v", items)
		}
	}
	locked := &Client{BaseURL: srv.URL + "/locked"}
	if _, err := locked.Search(context.Background(), "x", 5); !errors.Is(err, ErrSignIn) {
		t.Fatalf("locked: %v", err)
	}
	for _, bad := range []string{"", "ftp://x", "http://u:p@host", "http://host/?a=1", "kolibri.local"} {
		if _, err := Normalize(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// TestAdminSignsInAfresh: Kolibri refuses a sign-in that carries a session
// it still knows but no CSRF token, which left Arcana locked out of every
// administrator request until it restarted. Arcana signs in again without
// its old cookies, and sends the token from kolibri_csrftoken.
func TestAdminSignsInAfresh(t *testing.T) {
	var signIns, refused, tasksAsked int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/session/":
			signIns++
			if _, err := r.Cookie("kolibri"); err == nil && r.Header.Get("X-CSRFToken") != "tok" {
				refused++
				http.Error(w, "CSRF verification failed. Request aborted.", http.StatusForbidden)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "kolibri", Value: "s", Path: "/", HttpOnly: true})
			fmt.Fprint(w, `{"id":"current"}`)
		case "/api/tasks/tasks/":
			tasksAsked++
			if tasksAsked == 1 {
				// The session lapsed: Arcana signs in again, still holding a
				// session cookie and, here, no CSRF cookie to echo.
				http.Error(w, `{"detail":"not signed in"}`, http.StatusForbidden)
				return
			}
			fmt.Fprint(w, `[]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := &Admin{BaseURL: srv.URL, Username: "admin", Password: "pw"}
	if _, err := a.Tasks(context.Background()); err != nil {
		t.Fatalf("tasks: %v (sign-ins %d, refused %d)", err, signIns, refused)
	}
	if signIns != 3 || refused != 1 {
		t.Fatalf("sign-ins %d, refused %d; want 3 and 1", signIns, refused)
	}
}

// TestAdminSendsKolibriCSRFToken: Kolibri names its CSRF cookie
// kolibri_csrftoken; a POST without it in X-CSRFToken is refused.
func TestAdminSendsKolibriCSRFToken(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/session/":
			http.SetCookie(w, &http.Cookie{Name: "kolibri", Value: "s", Path: "/", HttpOnly: true})
			http.SetCookie(w, &http.Cookie{Name: "kolibri_csrftoken", Value: "tok", Path: "/"})
			fmt.Fprint(w, `{"id":"current"}`)
		case "/api/tasks/tasks/1/cancel/":
			got = r.Header.Get("X-CSRFToken")
			fmt.Fprint(w, `{}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	a := &Admin{BaseURL: srv.URL, Username: "admin", Password: "pw"}
	if err := a.Cancel(context.Background(), "1"); err != nil {
		t.Fatal(err)
	}
	if got != "tok" {
		t.Fatalf("X-CSRFToken %q, want the kolibri_csrftoken cookie", got)
	}
}

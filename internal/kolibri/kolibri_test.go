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

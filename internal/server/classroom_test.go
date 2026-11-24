package server

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/kolibri/kolibritest"
	"github.com/KK4ODA/arcana/internal/library"
)

// TestClassroom sets the classroom up with the Kolibri stand-in and goes
// through what the Classroom page and Kolibri's origin do.
func TestClassroom(t *testing.T) {
	t.Setenv(kolibritest.Env, "1")
	root := filepath.Join(t.TempDir(), "Arcana")
	if _, err := library.Init(root, "Hill School"); err != nil {
		t.Fatal(err)
	}
	a, err := app.Open(root, app.Options{DisableKiwix: true, KolibriPython: kolibritest.Python()})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	s := New(a, Config{Port: 18470})
	s.appPort, s.content, s.kolibriPort = 18470, 18471, 18472
	a.SetPorts(s.content, s.kolibriPort)
	e := &env{t: t, app: a, srv: s, appH: s.appHandler(), content: s.contentHandler()}
	kh := s.kolibriHandler()
	// Devices on the network reach this computer as 192.168.1.20.
	s.policy.refreshHosts([]string{"192.168.1.20"})
	tok := map[string]string{"X-Arcana-Token": e.token(), "Content-Type": "application/json"}
	host := "127.0.0.1:18470"

	// Before setup, Kolibri's origin says there is no classroom.
	if rec := e.do(kh, "GET", "/", local, "127.0.0.1:18472", nil, nil); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "No classroom") {
		t.Fatalf("before setup: %d %s", rec.Code, rec.Body)
	}
	// Network devices cannot set it up.
	if rec := e.do(e.appH, "POST", "/api/classroom/setup", lanClient, "192.168.1.20:18470", nil, strings.NewReader(`{"language":"es"}`)); rec.Code != http.StatusForbidden {
		t.Fatalf("network setup: %d", rec.Code)
	}
	rec := e.do(e.appH, "POST", "/api/classroom/setup", local, host, tok, strings.NewReader(`{"language":"es"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body)
	}

	type status struct {
		Classroom app.ClassroomStatus `json:"classroom"`
		URL       string              `json:"url"`
	}
	get := func(remote, h string) status {
		rec := e.do(e.appH, "GET", "/api/classroom", remote, h, nil, nil)
		var st status
		if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
			t.Fatalf("status: %v %s", err, rec.Body)
		}
		return st
	}
	var st status
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if st = get(local, host); st.Classroom.State == "running" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	c := st.Classroom
	if c.State != "running" || c.Facility != "Hill School" || c.Language != "es" || c.Username != "admin" || len(c.Password) != 19 {
		t.Fatalf("status %+v", c)
	}
	if st.URL != "http://127.0.0.1:18472/" || len(c.OnDevice) != 1 || c.AdminError != "" {
		t.Fatalf("status %+v url %s", c, st.URL)
	}
	// The provisioning file, which holds the password, is gone once
	// Kolibri runs; the password stays in the state folder only.
	deadline = time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(a.KolibriHome(), ".arcana-provision.json")); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("provisioning file kept")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Network devices see the state, not the password.
	if lan := get(lanClient, "192.168.1.20:18470"); lan.Classroom.Password != "" || lan.Classroom.State != "running" || lan.URL != "http://192.168.1.20:18472/" {
		t.Fatalf("network status %+v %s", lan.Classroom, lan.URL)
	}

	// Kolibri's origin: proxied, CSP pointing at the content origin.
	rec = e.do(kh, "GET", "/", lanClient, "192.168.1.20:18472", map[string]string{"Cookie": "arcana_session=secret"}, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Kolibri for Hill School") ||
		!strings.Contains(rec.Header().Get("Content-Security-Policy"), "frame-src 'self' data: blob: http://192.168.1.20:18471") {
		t.Fatalf("kolibri origin: %d %v %s", rec.Code, rec.Header(), rec.Body)
	}
	if rec := e.do(kh, "GET", "/", lanClient, "evil.example:18472", nil, nil); rec.Code != http.StatusMisdirectedRequest {
		t.Fatalf("unknown host: %d", rec.Code)
	}
	if rec := e.do(kh, "POST", "/api/auth/session/", lanClient, "192.168.1.20:18472", map[string]string{"Sec-Fetch-Site": "same-site"}, strings.NewReader(`{}`)); rec.Code != http.StatusForbidden {
		t.Fatalf("a form from the content origin reached Kolibri: %d", rec.Code)
	}
	// Channel apps on the content origin.
	if rec := e.do(e.content, "GET", "/kolibri-zip/zipcontent/x.zip/index.html", lanClient, "192.168.1.20:18471", nil, nil); rec.Code != 200 || !strings.HasPrefix(rec.Body.String(), "zip zipcontent/x.zip/index.html") {
		t.Fatalf("zip content: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(e.content, "OPTIONS", "/kolibri-zip/zipcontent/x.zip/index.html", lanClient, "192.168.1.20:18471", nil, nil); rec.Code == http.StatusMethodNotAllowed {
		t.Fatal("zip content refused OPTIONS")
	}
	if rec := e.do(e.content, "OPTIONS", "/files/x/y", lanClient, "192.168.1.20:18471", nil, nil); rec.Code != http.StatusMethodNotAllowed {
		t.Fatal("the content origin accepted OPTIONS outside Kolibri's apps")
	}

	// The topic picker reads the channel's database (here a small one
	// already downloaded, so no request leaves the test).
	dbPath := filepath.Join(a.Lib.CatalogCache(), "kolibri", kolibritest.ChannelID+".sqlite3")
	os.MkdirAll(filepath.Dir(dbPath), 0o755)
	if err := kolibritest.WriteChannelDB(dbPath, kolibritest.ChannelID); err != nil {
		t.Fatal(err)
	}
	if rec := e.do(e.appH, "GET", "/api/classroom/channels/"+kolibritest.ChannelID+"/tree", lanClient, "192.168.1.20:18470", nil, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("network tree: %d", rec.Code)
	}
	rec = e.do(e.appH, "GET", "/api/classroom/channels/"+kolibritest.ChannelID+"/tree", local, host, nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"title":"Matemáticas"`) {
		t.Fatalf("tree: %d %s", rec.Code, rec.Body)
	}
	rec = e.do(e.appH, "POST", "/api/classroom/channels/"+kolibritest.ChannelID+"/size", local, host, tok,
		strings.NewReader(`{"nodeIds":["`+kolibritest.Grade1+`","`+kolibritest.Grade2+`"]}`))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"size":1010`) {
		t.Fatalf("size: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(e.appH, "GET", "/api/classroom/channels/0123/tree", local, host, nil, nil); rec.Code == 200 {
		t.Fatal("a channel that is not offered was read")
	}

	// Import, as Kolibri's administrator.
	rec = e.do(e.appH, "POST", "/api/classroom/import", local, host, tok,
		strings.NewReader(`{"channel":"`+kolibritest.ChannelID+`","nodeIds":["`+kolibritest.Grade1+`"]}`))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"RUNNING"`) {
		t.Fatalf("import: %d %s", rec.Code, rec.Body)
	}
	var task struct{ ID string }
	json.Unmarshal(rec.Body.Bytes(), &task)
	if rec := e.do(e.appH, "POST", "/api/classroom/import", local, host, tok,
		strings.NewReader(`{"channel":"`+kolibritest.ChannelID+`","nodeIds":["f0000000000000000000000000000000"]}`)); rec.Code == 200 {
		t.Fatal("a topic outside the channel was imported")
	}
	if st := get(local, host); len(st.Classroom.Imports) != 1 || st.Classroom.Imports[0].ChannelID != kolibritest.ChannelID {
		t.Fatalf("imports %+v", st.Classroom.Imports)
	}
	if rec := e.do(e.appH, "POST", "/api/classroom/imports/"+task.ID+"/cancel", local, host, tok, strings.NewReader(`{}`)); rec.Code != 200 {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}

	// Library search finds the classroom's courses, linked on its origin.
	rec = e.do(e.appH, "GET", "/api/kolibri/search?q=fractions", lanClient, "192.168.1.20:18470", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"url":"http://192.168.1.20:18472/learn/#/topics/c/0123456789abcdef0123456789abcdef"`) {
		t.Fatalf("search: %d %s", rec.Code, rec.Body)
	}

	// A wrong password is refused, the right one kept.
	if rec := e.do(e.appH, "POST", "/api/classroom/password", local, host, tok, strings.NewReader(`{"password":"nope"}`)); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong password: %d %s", rec.Code, rec.Body)
	}
	if rec := e.do(e.appH, "POST", "/api/classroom/password", local, host, tok, strings.NewReader(`{"password":"`+c.Password+`"}`)); rec.Code != 200 {
		t.Fatalf("password: %d %s", rec.Code, rec.Body)
	}

	// Turning it off stops Kolibri and keeps its data.
	if rec := e.do(e.appH, "POST", "/api/classroom/stop", local, host, tok, strings.NewReader(`{}`)); rec.Code != 200 {
		t.Fatalf("stop: %d %s", rec.Code, rec.Body)
	}
	if st := get(local, host); st.Classroom.State != "off" || st.Classroom.Wanted {
		t.Fatalf("after stop %+v", st.Classroom)
	}
	if _, err := os.Stat(filepath.Join(a.KolibriHome(), "fake-admin.json")); err != nil {
		t.Fatal("Kolibri's data was removed")
	}
	if rec := e.do(kh, "GET", "/", local, "127.0.0.1:18472", nil, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("after stop the origin answered %d", rec.Code)
	}
}

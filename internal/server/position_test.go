package server

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func localToken(t *testing.T, e *env) string {
	t.Helper()
	var sess map[string]any
	json.Unmarshal(e.do(e.appH, "GET", "/api/session", local, "127.0.0.1:18470", nil, nil).Body.Bytes(), &sess)
	token, _ := sess["token"].(string)
	if token == "" {
		t.Fatal("no token")
	}
	return token
}

func TestNetworkPositionSettings(t *testing.T) {
	e := newEnv(t)
	hdr := map[string]string{"X-Arcana-Token": localToken(t, e), "Content-Type": "application/json"}
	cases := []struct {
		name string
		gps  string
		want int
	}{
		{"starlink", `{"source":"starlink"}`, 200},
		{"starlink with address", `{"source":"starlink","starlinkAddress":"192.168.100.1:9200"}`, 200},
		{"starlink address without port", `{"source":"starlink","starlinkAddress":"192.168.100.1"}`, 400},
		{"browser", `{"source":"browser","browserId":"a1b2-c3"}`, 200},
		{"bad browser id", `{"source":"browser","browserId":"<script>"}`, 400},
		{"receiver checked by starlink", `{"source":"serial","checkSource":"starlink"}`, 200},
		{"starlink checked by receiver", `{"source":"starlink","checkSource":"serial"}`, 200},
		{"same source twice", `{"source":"starlink","checkSource":"starlink"}`, 400},
		{"tcp and gpsd share an address", `{"source":"tcp","checkSource":"gpsd"}`, 400},
		{"simulator cannot be a check", `{"source":"serial","checkSource":"simulator"}`, 400},
		{"ip geolocation is not offered", `{"source":"ip"}`, 400},
		{"off", `{"source":""}`, 200},
	}
	for _, c := range cases {
		rec := e.do(e.appH, "PUT", "/api/settings", local, "127.0.0.1:18470", hdr, strings.NewReader(`{"gps":`+c.gps+`}`))
		if rec.Code != c.want {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body.String())
		}
	}
}

func TestBrowserPositionAndSavedRoutes(t *testing.T) {
	e := newEnv(t)
	hdr := map[string]string{"X-Arcana-Token": localToken(t, e), "Content-Type": "application/json"}
	put := func(method, url, body string) (int, string) {
		rec := e.do(e.appH, method, url, local, "127.0.0.1:18470", hdr, strings.NewReader(body))
		return rec.Code, rec.Body.String()
	}
	if code, body := put("POST", "/api/gps/browser", `{"lat":1,"lon":2,"accuracy":5,"clientId":"tab-1"}`); code != 409 {
		t.Fatalf("no browser source: %d %s", code, body)
	}
	if code, body := put("PUT", "/api/settings", `{"gps":{"source":"browser","browserId":"tab-1"}}`); code != 200 {
		t.Fatalf("settings: %d %s", code, body)
	}
	if _, body := put("GET", "/api/gps/browser?id=tab-1", ""); !strings.Contains(body, `"wanted":true`) {
		t.Fatalf("chosen browser not wanted: %s", body)
	}
	if _, body := put("GET", "/api/gps/browser?id=tab-2", ""); !strings.Contains(body, `"wanted":false`) {
		t.Fatalf("other browser wanted: %s", body)
	}
	if code, _ := put("POST", "/api/gps/browser", `{"lat":1,"lon":2,"accuracy":5,"clientId":"tab-2"}`); code != 409 {
		t.Fatalf("other browser accepted: %d", code)
	}
	if code, body := put("POST", "/api/gps/browser", `{"lat":46.2044,"lon":6.1432,"accuracy":35,"altitude":null,"speed":null,"heading":null,"clientId":"tab-1"}`); code != 200 {
		t.Fatalf("position: %d %s", code, body)
	}
	var st struct {
		State string `json:"state"`
		Fix   struct {
			Lat, Accuracy float64
		} `json:"fix"`
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && st.State != "current"; time.Sleep(20 * time.Millisecond) {
		_, body := put("GET", "/api/gps", "")
		json.Unmarshal([]byte(body), &st)
	}
	if st.State != "current" || st.Fix.Lat != 46.2044 || st.Fix.Accuracy != 35 {
		t.Fatalf("status %+v", st)
	}

	route := `{"name":"To the lake","mode":"foot","from":{"lat":46.2044,"lon":6.1432,"label":"My position"},"to":{"lat":46.21,"lon":6.15,"label":"Lake"},
		"distance":900,"duration":650,"warnings":[],"snapped":[[6.1432,46.2044]],
		"geometry":[[6.1432,46.2044],[6.146,46.207],[6.15,46.21]],
		"steps":[{"type":"depart","text":"Head northeast","distance":900,"duration":650,"location":[6.1432,46.2044],"index":0},
		         {"type":"arrive","text":"Arrive at Lake","distance":0,"duration":0,"location":[6.15,46.21],"index":2}]}`
	code, body := put("POST", "/api/gps/routes", route)
	if code != 200 {
		t.Fatalf("save route: %d %s", code, body)
	}
	var saved struct{ ID string }
	json.Unmarshal([]byte(body), &saved)
	if _, body := put("GET", "/api/gps/routes", ""); !strings.Contains(body, "To the lake") {
		t.Fatalf("list: %s", body)
	}
	if _, body := put("GET", "/api/gps/routes/"+saved.ID, ""); !strings.Contains(body, "Arrive at Lake") {
		t.Fatalf("get: %s", body)
	}
	rec := e.do(e.appH, "GET", "/api/gps/routes/"+saved.ID+"/gpx", local, "127.0.0.1:18470", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<rte>") || rec.Header().Get("Content-Type") != "application/gpx+xml" {
		t.Fatalf("gpx: %d %s", rec.Code, rec.Body.String())
	}
	if code, _ := put("POST", "/api/gps/routes", `{"mode":"foot","geometry":[]}`); code != 400 {
		t.Fatalf("invalid route accepted: %d", code)
	}
	if code, _ := put("DELETE", "/api/gps/routes/"+saved.ID, ""); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := put("GET", "/api/gps/routes/"+saved.ID, ""); code != 404 {
		t.Fatalf("after delete: %d", code)
	}
}

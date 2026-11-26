package server

import (
	"encoding/json"
	"testing"
)

// The remedies are built in: they answer on a library without the FDA label
// reference, and network clients may read them like any reference content.
func TestRemediesWithoutLabelReference(t *testing.T) {
	e := newEnv(t)
	for _, remote := range []string{local, "192.168.1.30:50000"} {
		rec := e.do(e.appH, "GET", "/api/drugs/remedies?s=dehydration,diarrhea", remote, "127.0.0.1:18470", nil, nil)
		if rec.Code != 200 {
			t.Fatalf("%s: remedies: %d %s", remote, rec.Code, rec.Body.String())
		}
		var body struct {
			Remedies []struct{ Slug, Kind string } `json:"remedies"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, r := range body.Remedies {
			found = found || r.Slug == "oral-rehydration"
		}
		if !found {
			t.Fatalf("%s: oral rehydration missing from %+v", remote, body.Remedies)
		}
	}

	if rec := e.do(e.appH, "GET", "/api/drugs/remedies?s=hiccups", local, "127.0.0.1:18470", nil, nil); rec.Code != 400 {
		t.Errorf("unknown situation: %d", rec.Code)
	}
	if rec := e.do(e.appH, "GET", "/api/drugs/remedies/honey-for-cough", local, "127.0.0.1:18470", nil, nil); rec.Code != 200 {
		t.Errorf("one remedy: %d %s", rec.Code, rec.Body.String())
	}
	if rec := e.do(e.appH, "GET", "/api/drugs/remedies/nope", local, "127.0.0.1:18470", nil, nil); rec.Code != 404 {
		t.Errorf("unknown remedy: %d", rec.Code)
	}

	rec := e.do(e.appH, "GET", "/api/drugs", local, "127.0.0.1:18470", nil, nil)
	var info struct {
		Installed bool `json:"installed"`
		Remedies  struct {
			Count   int    `json:"count"`
			License string `json:"license"`
		} `json:"remedies"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.Installed || info.Remedies.Count != 38 || info.Remedies.License != "Apache-2.0" {
		t.Errorf("info: %+v", info)
	}
}

package integration

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestTokens(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Create("RadioPost", []string{"search:read", "root"}); err == nil {
		t.Fatal("unknown scope accepted")
	}
	if _, _, err := s.Create("", []string{ScopeSearch}); err == nil {
		t.Fatal("empty name accepted")
	}
	c, tok, err := s.Create("RadioPost", []string{ScopeSearch, ScopeDocs, ScopeSearch})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "arc1_") || len(c.Scopes) != 2 || !strings.HasPrefix(tok, c.Prefix) {
		t.Fatalf("client %+v token %s", c, tok)
	}
	data, _ := os.ReadFile(dir + "/" + FileName)
	if strings.Contains(string(data), tok) {
		t.Fatal("token stored in clear text")
	}
	got, ok := s.Authenticate(tok)
	if !ok || got.ID != c.ID || !got.Has(ScopeDocs) || got.Has(ScopeLocation) {
		t.Fatalf("authenticate: %+v %v", got, ok)
	}
	for _, bad := range []string{"", "arc1_x", tok + "x", strings.ToUpper(tok)} {
		if _, ok := s.Authenticate(bad); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
	// Reloaded from disk (another start of Arcana).
	s2, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Authenticate(tok); !ok {
		t.Fatal("token lost after reload")
	}
	if l := s2.List(); len(l) != 1 || l[0].Hash != "" {
		t.Fatalf("list %+v", l)
	}
	if err := s2.Revoke(c.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Authenticate(tok); ok {
		t.Fatal("revoked token accepted")
	}
	ro, _ := Open(dir, true)
	if _, _, err := ro.Create("x", []string{ScopeSearch}); err != ErrReadOnly {
		t.Fatalf("read-only create: %v", err)
	}
}

func TestPairing(t *testing.T) {
	s, _ := Open(t.TempDir(), false)
	clock := time.Now()
	s.now = func() time.Time { return clock }
	req, secret, err := s.RequestPairing("Aether HF", []string{ScopeLocation, ScopeMaps}, "192.168.1.9")
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Code) != 6 || len(s.Pending()) != 1 {
		t.Fatalf("request %+v", req)
	}
	if st, _ := s.Poll(req.ID, "wrong"); st != "unknown" {
		t.Fatal("wrong secret accepted")
	}
	if st, tok := s.Poll(req.ID, secret); st != "pending" || tok != "" {
		t.Fatal(st)
	}
	if err := s.Decide(req.ID, true); err != nil {
		t.Fatal(err)
	}
	st, tok := s.Poll(req.ID, secret)
	if st != "approved" || tok == "" {
		t.Fatal(st)
	}
	if c, ok := s.Authenticate(tok); !ok || c.Name != "Aether HF" || !c.Has(ScopeLocation) {
		t.Fatal("approved token does not work")
	}
	if st, _ := s.Poll(req.ID, secret); st != "unknown" {
		t.Fatal("token handed out twice")
	}
	// Denied and expired requests.
	r2, sec2, _ := s.RequestPairing("Spam", []string{ScopeSearch}, "x")
	s.Decide(r2.ID, false)
	if st, _ := s.Poll(r2.ID, sec2); st != "denied" {
		t.Fatal(st)
	}
	r3, sec3, _ := s.RequestPairing("Late", []string{ScopeSearch}, "x")
	clock = clock.Add(11 * time.Minute)
	if st, _ := s.Poll(r3.ID, sec3); st != "unknown" || len(s.Pending()) != 0 {
		t.Fatal("expired request survived")
	}
	for i := 0; i < maxPending; i++ {
		if _, _, err := s.RequestPairing("Flood", []string{ScopeSearch}, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := s.RequestPairing("Flood", []string{ScopeSearch}, "x"); err == nil {
		t.Fatal("pending requests not limited")
	}
}

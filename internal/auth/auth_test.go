package auth

import (
	"os"
	"testing"
	"time"
)

func TestPasswordStore(t *testing.T) {
	s := NewStore(t.TempDir())
	if s.Enabled() {
		t.Fatal("enabled without a password")
	}
	if _, err := s.Verify("anything"); err != ErrNoPassword {
		t.Fatalf("verify without password: %v", err)
	}
	if err := s.Set("short"); err == nil {
		t.Fatal("short password accepted")
	}
	if err := s.Set("correct horse battery"); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(s.Path)
	if st.Mode().Perm()&0o077 != 0 && os.Getenv("GOOS") != "windows" {
		t.Logf("password file mode %v (exFAT ignores modes)", st.Mode())
	}
	data, _ := os.ReadFile(s.Path)
	if string(data) == "" || containsStr(string(data), "correct horse") {
		t.Fatal("password stored in clear text")
	}
	for pw, want := range map[string]bool{"correct horse battery": true, "correct horse batterx": false, "": false} {
		ok, err := s.Verify(pw)
		if err != nil || ok != want {
			t.Fatalf("verify %q = %v, %v", pw, ok, err)
		}
	}
	// A second store (another process, or after restart) reads the file.
	if ok, _ := NewStore(s.Path[:len(s.Path)-len(FileName)-1]).Verify("correct horse battery"); !ok {
		t.Fatal("password not persisted")
	}
	if err := s.Clear(); err != nil || s.Enabled() {
		t.Fatal("clear failed")
	}
	os.WriteFile(s.Path, []byte("{not json"), 0o600)
	if _, err := s.Verify("x"); err == nil || err == ErrNoPassword {
		t.Fatalf("damaged file: %v", err)
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestSessions(t *testing.T) {
	ss := NewSessions()
	clock := time.Now()
	ss.now = func() time.Time { return clock }
	s := ss.New("192.168.1.5")
	if len(s.ID) != 64 || len(s.CSRF) != 64 || s.ID == s.CSRF {
		t.Fatalf("session %+v", s)
	}
	if ss.Get(s.ID) == nil || ss.Get("nope") != nil {
		t.Fatal("lookup")
	}
	clock = clock.Add(SessionIdle - time.Minute)
	if ss.Get(s.ID) == nil {
		t.Fatal("expired while active")
	}
	clock = clock.Add(SessionIdle + time.Minute)
	if ss.Get(s.ID) != nil {
		t.Fatal("idle session survived")
	}
	s2 := ss.New("x")
	for i := 0; i < 11; i++ {
		clock = clock.Add(time.Hour)
		ss.Get(s2.ID)
	}
	clock = clock.Add(2 * time.Hour)
	if ss.Get(s2.ID) != nil {
		t.Fatal("session outlived the absolute limit")
	}
	for i := 0; i < maxSessions+5; i++ {
		ss.New("y")
	}
	if ss.Count() != maxSessions {
		t.Fatalf("count %d", ss.Count())
	}
	ss.Clear()
	if ss.Count() != 0 {
		t.Fatal("clear")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(15*time.Minute, 3, 5)
	clock := time.Now()
	l.now = func() time.Time { return clock }
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow("a"); !ok {
			t.Fatal("blocked too early")
		}
		l.Fail("a")
	}
	if ok, wait := l.Allow("a"); ok || wait <= 0 {
		t.Fatal("not blocked after three failures")
	}
	l.Fail("b")
	l.Fail("c")
	if ok, _ := l.Allow("d"); ok {
		t.Fatal("global limit not applied")
	}
	clock = clock.Add(16 * time.Minute)
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("window did not expire")
	}
	l.Fail("a")
	l.Succeed("a")
	if ok, _ := l.Allow("a"); !ok {
		t.Fatal("success did not reset")
	}
}

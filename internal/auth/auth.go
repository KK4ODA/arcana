// Package auth implements optional password-based administration for
// clients on the local network. Without a password, network clients are
// read-only; the computer running Arcana is always an administrator.
//
// The password is stored as an Argon2id hash in the library's user folder,
// so it travels with the drive. Sessions live in memory only: restarting
// Arcana signs everyone out.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/KK4ODA/arcana/internal/fsutil"
)

// Argon2id parameters (RFC 9106 second recommendation, with memory sized for
// a Raspberry Pi). Stored with each hash so they can change later.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16
	// MinPasswordLength is the shortest accepted password.
	MinPasswordLength = 10
)

// ErrNoPassword means remote administration is not enabled.
var ErrNoPassword = errors.New("remote administration is not enabled")

type record struct {
	Format    string    `json:"format"`
	Algorithm string    `json:"algorithm"`
	Time      uint32    `json:"time"`
	Memory    uint32    `json:"memory"`
	Threads   uint8     `json:"threads"`
	Salt      string    `json:"salt"`
	Hash      string    `json:"hash"`
	Set       time.Time `json:"set"`
}

// Store reads and writes the password file.
type Store struct {
	Path string

	mu  sync.Mutex
	rec *record
	mod time.Time
}

// FileName is the password file inside the user folder.
const FileName = "admin-password.json"

// NewStore uses dir/admin-password.json.
func NewStore(dir string) *Store { return &Store{Path: filepath.Join(dir, FileName)} }

func (s *Store) load() (*record, error) {
	st, err := os.Stat(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		s.rec = nil
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if s.rec != nil && st.ModTime().Equal(s.mod) {
		return s.rec, nil
	}
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return nil, err
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil || r.Algorithm != "argon2id" || r.Hash == "" {
		return nil, fmt.Errorf("%s is damaged; remove it and set the password again", FileName)
	}
	if r.Memory > 1<<21 || r.Time > 20 || r.Threads == 0 {
		return nil, fmt.Errorf("%s has implausible parameters", FileName)
	}
	s.rec, s.mod = &r, st.ModTime()
	return s.rec, nil
}

// Enabled reports whether a password is set.
func (s *Store) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.load()
	return err == nil && r != nil
}

// SetAt reports when the password was set.
func (s *Store) SetAt() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, err := s.load(); err == nil && r != nil {
		return r.Set
	}
	return time.Time{}
}

// Set stores a new password.
func (s *Store) Set(password string) error {
	if utf8.RuneCountInString(password) < MinPasswordLength {
		return fmt.Errorf("use at least %d characters", MinPasswordLength)
	}
	if len(password) > 1024 {
		return errors.New("password too long")
	}
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	r := record{Format: "arcana-admin-password", Algorithm: "argon2id", Time: argonTime, Memory: argonMemory,
		Threads: argonThreads, Salt: base64.StdEncoding.EncodeToString(salt), Hash: base64.StdEncoding.EncodeToString(hash),
		Set: time.Now().UTC().Truncate(time.Second)}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return err
	}
	if err := fsutil.WriteFileAtomic(s.Path, append(data, '\n'), 0o600); err != nil {
		return err
	}
	s.rec = nil
	return nil
}

// Clear disables remote administration.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rec = nil
	err := os.Remove(s.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Verify checks a password in constant time.
func (s *Store) Verify(password string) (bool, error) {
	s.mu.Lock()
	r, err := s.load()
	s.mu.Unlock()
	if err != nil {
		return false, err
	}
	if r == nil {
		return false, ErrNoPassword
	}
	salt, err1 := base64.StdEncoding.DecodeString(r.Salt)
	want, err2 := base64.StdEncoding.DecodeString(r.Hash)
	if err1 != nil || err2 != nil || len(want) == 0 || len(want) > 128 {
		return false, fmt.Errorf("%s is damaged", FileName)
	}
	if len(password) > 1024 {
		return false, nil
	}
	got := argon2.IDKey([]byte(password), salt, r.Time, r.Memory, r.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// ---------------------------------------------------------------- sessions

// Session is a signed-in network client.
type Session struct {
	ID       string
	CSRF     string
	Addr     string
	Created  time.Time
	LastSeen time.Time
}

// Session lifetimes.
const (
	SessionIdle     = 2 * time.Hour
	SessionAbsolute = 12 * time.Hour
	maxSessions     = 64
)

// Sessions holds active sessions in memory.
type Sessions struct {
	mu  sync.Mutex
	m   map[string]*Session
	now func() time.Time
}

// NewSessions returns an empty set.
func NewSessions() *Sessions { return &Sessions{m: map[string]*Session{}, now: time.Now} }

func token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

// New creates a session for a client address.
func (ss *Sessions) New(addr string) *Session {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.expireLocked()
	if len(ss.m) >= maxSessions {
		// Drop the oldest.
		var oldest *Session
		for _, s := range ss.m {
			if oldest == nil || s.LastSeen.Before(oldest.LastSeen) {
				oldest = s
			}
		}
		delete(ss.m, oldest.ID)
	}
	now := ss.now()
	s := &Session{ID: token(), CSRF: token(), Addr: addr, Created: now, LastSeen: now}
	ss.m[s.ID] = s
	return s
}

// Get returns a live session and refreshes its idle timer.
func (ss *Sessions) Get(id string) *Session {
	if len(id) != 64 {
		return nil
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s := ss.m[id]
	if s == nil {
		return nil
	}
	now := ss.now()
	if now.Sub(s.LastSeen) > SessionIdle || now.Sub(s.Created) > SessionAbsolute {
		delete(ss.m, id)
		return nil
	}
	s.LastSeen = now
	c := *s
	return &c
}

// Delete ends one session.
func (ss *Sessions) Delete(id string) {
	ss.mu.Lock()
	delete(ss.m, id)
	ss.mu.Unlock()
}

// Clear ends every session.
func (ss *Sessions) Clear() {
	ss.mu.Lock()
	ss.m = map[string]*Session{}
	ss.mu.Unlock()
}

// Count returns the number of live sessions.
func (ss *Sessions) Count() int {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.expireLocked()
	return len(ss.m)
}

func (ss *Sessions) expireLocked() {
	now := ss.now()
	for id, s := range ss.m {
		if now.Sub(s.LastSeen) > SessionIdle || now.Sub(s.Created) > SessionAbsolute {
			delete(ss.m, id)
		}
	}
}

// ---------------------------------------------------------------- limiter

// Limiter slows password guessing: each address may fail a few times per
// window, and all addresses together a few dozen times.
type Limiter struct {
	mu       sync.Mutex
	window   time.Duration
	perAddr  int
	global   int
	failures map[string][]time.Time
	all      []time.Time
	now      func() time.Time
}

// NewLimiter allows perAddr failures per address and global failures in
// total within window.
func NewLimiter(window time.Duration, perAddr, global int) *Limiter {
	return &Limiter{window: window, perAddr: perAddr, global: global, failures: map[string][]time.Time{}, now: time.Now}
}

func prune(ts []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(ts) && ts[i].Before(cutoff) {
		i++
	}
	return ts[i:]
}

// Allow reports whether addr may try now, and if not, how long to wait.
func (l *Limiter) Allow(addr string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cutoff := now.Add(-l.window)
	l.all = prune(l.all, cutoff)
	f := prune(l.failures[addr], cutoff)
	l.failures[addr] = f
	if len(f) >= l.perAddr {
		return false, f[0].Add(l.window).Sub(now)
	}
	if len(l.all) >= l.global {
		return false, l.all[0].Add(l.window).Sub(now)
	}
	return true, 0
}

// Fail records a failed attempt.
func (l *Limiter) Fail(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.failures[addr] = append(l.failures[addr], now)
	l.all = append(l.all, now)
	if len(l.failures) > 4096 {
		cutoff := now.Add(-l.window)
		for k, v := range l.failures {
			if len(prune(v, cutoff)) == 0 {
				delete(l.failures, k)
			}
		}
	}
}

// Succeed clears an address's failures.
func (l *Limiter) Succeed(addr string) {
	l.mu.Lock()
	delete(l.failures, addr)
	l.mu.Unlock()
}

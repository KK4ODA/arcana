// Package integration manages access for other programs (for example
// RadioPost or Aether HF) to Arcana's versioned integration API.
//
// Every client holds a bearer token limited to scopes. Tokens are random
// 256-bit values; only their SHA-256 is stored, in user/integrations.json,
// so the file can travel with the library without revealing them. A program
// obtains a token either from the operator directly, or by pairing: it asks
// for access with a name and scopes, the operator approves the request in
// Arcana (comparing a short code), and the program collects its token once.
package integration

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/KK4ODA/arcana/internal/fsutil"
)

// Scopes a client can hold. There is no scope that changes the library.
const (
	ScopeSearch   = "search:read"
	ScopeDocs     = "documents:read"
	ScopeShare    = "documents:share"
	ScopeMaps     = "maps:read"
	ScopeLocation = "location:read"
	// ScopeLibrary lets another Arcana copy installed resources (peer sync).
	ScopeLibrary = "library:read"
	// ScopeAsk lets a program ask the assistant questions (it uses this
	// computer's processor and memory).
	ScopeAsk = "assistant:ask"
)

// Scope describes one permission for the operator.
type Scope struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

// AllScopes lists the scopes in display order.
var AllScopes = []Scope{
	{ScopeSearch, "Search installed archives and documents"},
	{ScopeDocs, "Read articles and documents as text, with citations"},
	{ScopeShare, "Create short links to an article for people on this network"},
	{ScopeMaps, "Look up coordinates, grid locators and nearby place names"},
	{ScopeLocation, "Read the current GPS position"},
	{ScopeLibrary, "List and copy installed resources (another Arcana syncing from this one)"},
	{ScopeAsk, "Ask questions answered from the library, with citations (uses this computer's processor)"},
}

func validScope(s string) bool {
	for _, sc := range AllScopes {
		if sc.ID == s {
			return true
		}
	}
	return false
}

// ErrReadOnly is returned when the library cannot be written.
var ErrReadOnly = errors.New("the library is read-only")

// Client is a program allowed to use the API.
type Client struct {
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Scopes   []string   `json:"scopes"`
	Hash     string     `json:"hash"`   // SHA-256 of the token, hex
	Prefix   string     `json:"prefix"` // first characters, to recognise it
	Created  time.Time  `json:"created"`
	LastUsed *time.Time `json:"lastUsed,omitempty"`
}

// Has reports whether the client holds a scope.
func (c *Client) Has(scope string) bool {
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// PairRequest is a pending request for access.
type PairRequest struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Scopes  []string  `json:"scopes"`
	Code    string    `json:"code"` // shown by both sides
	Addr    string    `json:"addr"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`

	secretHash string
	status     string // pending, approved, denied
	token      string // delivered once after approval
}

const (
	pairLifetime = 10 * time.Minute
	maxPending   = 8
	maxClients   = 100
	tokenPrefix  = "arc1_"
)

// Store keeps clients and pairing requests.
type Store struct {
	path     string
	readOnly bool
	now      func() time.Time

	mu        sync.Mutex
	clients   []Client
	pending   map[string]*PairRequest
	lastSaved time.Time
}

type fileFormat struct {
	Format  string   `json:"format"`
	Clients []Client `json:"clients"`
}

// FileName is the client list inside the user folder.
const FileName = "integrations.json"

// Open loads dir/integrations.json.
func Open(dir string, readOnly bool) (*Store, error) {
	s := &Store{path: filepath.Join(dir, FileName), readOnly: readOnly, now: time.Now, pending: map[string]*PairRequest{}}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("%s is damaged: %w", FileName, err)
	}
	for _, c := range f.Clients {
		if len(c.Hash) == 64 && c.ID != "" {
			s.clients = append(s.clients, c)
		}
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	if s.readOnly {
		return ErrReadOnly
	}
	data, err := json.MarshalIndent(fileFormat{Format: "arcana-integrations", Clients: s.clients}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	s.lastSaved = s.now()
	return fsutil.WriteFileAtomic(s.path, append(data, '\n'), 0o600)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func cleanName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 80 {
		return "", errors.New("give the program a name of 1 to 80 characters")
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", errors.New("the name contains control characters")
		}
	}
	return name, nil
}

func cleanScopes(scopes []string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, sc := range scopes {
		if !validScope(sc) {
			return nil, fmt.Errorf("unknown scope %q", sc)
		}
		if !seen[sc] {
			seen[sc] = true
			out = append(out, sc)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("choose at least one permission")
	}
	sort.Strings(out)
	return out, nil
}

// Create adds a client and returns its token (shown only once).
func (s *Store) Create(name string, scopes []string) (Client, string, error) {
	name, err := cleanName(name)
	if err != nil {
		return Client{}, "", err
	}
	scopes, err = cleanScopes(scopes)
	if err != nil {
		return Client{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createLocked(name, scopes)
}

func (s *Store) createLocked(name string, scopes []string) (Client, string, error) {
	if s.readOnly {
		return Client{}, "", ErrReadOnly
	}
	if len(s.clients) >= maxClients {
		return Client{}, "", errors.New("too many integrations; revoke unused ones first")
	}
	tok := newToken()
	c := Client{ID: randomHex(8), Name: name, Scopes: scopes, Hash: hashToken(tok), Prefix: tok[:len(tokenPrefix)+6],
		Created: s.now().UTC().Truncate(time.Second)}
	s.clients = append(s.clients, c)
	if err := s.saveLocked(); err != nil {
		s.clients = s.clients[:len(s.clients)-1]
		return Client{}, "", err
	}
	return c, tok, nil
}

// List returns the clients (without hashes).
func (s *Store) List() []Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Client, 0, len(s.clients))
	for _, c := range s.clients {
		c.Hash = ""
		out = append(out, c)
	}
	return out
}

// Revoke removes a client.
func (s *Store) Revoke(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.clients {
		if c.ID == id {
			s.clients = append(s.clients[:i:i], s.clients[i+1:]...)
			return s.saveLocked()
		}
	}
	return os.ErrNotExist
}

// Authenticate returns the client holding token.
func (s *Store) Authenticate(token string) (Client, bool) {
	if !strings.HasPrefix(token, tokenPrefix) || len(token) > 100 {
		return Client{}, false
	}
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.clients {
		c := &s.clients[i]
		if subtle.ConstantTimeCompare([]byte(c.Hash), []byte(h)) == 1 {
			now := s.now()
			t := now.UTC().Truncate(time.Second)
			c.LastUsed = &t
			// Record use without rewriting the file on every request.
			if !s.readOnly && now.Sub(s.lastSaved) > time.Minute {
				_ = s.saveLocked()
			}
			return *c, true
		}
	}
	return Client{}, false
}

// ---------------------------------------------------------------- pairing

func (s *Store) expireLocked() {
	now := s.now()
	for id, p := range s.pending {
		if now.After(p.Expires) {
			delete(s.pending, id)
		}
	}
}

func shortCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	return fmt.Sprintf("%06d", n.Int64())
}

// RequestPairing records a request from a program and returns it with the
// secret the program uses to collect its token.
func (s *Store) RequestPairing(name string, scopes []string, addr string) (PairRequest, string, error) {
	name, err := cleanName(name)
	if err != nil {
		return PairRequest{}, "", err
	}
	scopes, err = cleanScopes(scopes)
	if err != nil {
		return PairRequest{}, "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly {
		return PairRequest{}, "", ErrReadOnly
	}
	s.expireLocked()
	if len(s.pending) >= maxPending {
		return PairRequest{}, "", errors.New("too many pending requests; ask the operator to approve or deny them")
	}
	secret := randomHex(24)
	now := s.now()
	p := &PairRequest{ID: randomHex(8), Name: name, Scopes: scopes, Code: shortCode(), Addr: addr,
		Created: now.UTC(), Expires: now.Add(pairLifetime).UTC(), secretHash: hashToken(secret), status: "pending"}
	s.pending[p.ID] = p
	return *p, secret, nil
}

// Pending lists requests awaiting a decision.
func (s *Store) Pending() []PairRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	out := []PairRequest{}
	for _, p := range s.pending {
		if p.status == "pending" {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// Decide approves or denies a request. Approval creates the client.
func (s *Store) Decide(id string, approve bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	p := s.pending[id]
	if p == nil || p.status != "pending" {
		return os.ErrNotExist
	}
	if !approve {
		p.status = "denied"
		return nil
	}
	_, tok, err := s.createLocked(p.Name, p.Scopes)
	if err != nil {
		return err
	}
	p.status, p.token = "approved", tok
	return nil
}

// Poll lets the requesting program check its request. The token is handed
// out once; afterwards the request is forgotten.
func (s *Store) Poll(id, secret string) (status, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireLocked()
	p := s.pending[id]
	if p == nil || subtle.ConstantTimeCompare([]byte(p.secretHash), []byte(hashToken(secret))) != 1 {
		return "unknown", ""
	}
	switch p.status {
	case "approved":
		delete(s.pending, id)
		return "approved", p.token
	case "denied":
		delete(s.pending, id)
		return "denied", ""
	}
	return "pending", ""
}

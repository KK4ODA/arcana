package download

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Container registries (Docker Hub, GitHub's ghcr.io) serve image layers
// by digest, a layer's SHA-256, but only to a client holding a token, even
// for public images. The token is anonymous: the registry answers 401 with
// a "Bearer" challenge naming where to get one (the OCI distribution
// specification's token authentication). registryAuth answers that
// challenge for blob downloads, so the catalog can list a layer of an
// official image as an ordinary mirror URL (ADR-0030). It sends no
// credentials of its own.
type registryAuth struct {
	base http.RoundTripper

	mu     sync.Mutex
	tokens map[string]registryToken // by realm, service and scope
}

type registryToken struct {
	value   string
	expires time.Time
}

// blobPath matches the registry API's blob URLs: /v2/<name>/blobs/sha256:<hex>.
var blobPath = regexp.MustCompile(`^/v2/[a-z0-9]+(?:[._/-][a-z0-9]+)*/blobs/sha256:[0-9a-f]{64}$`)

func (t *registryAuth) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("Authorization") != "" || !blobPath.MatchString(req.URL.Path) || req.URL.Scheme != "https" {
		return t.base.RoundTrip(req)
	}
	key := req.URL.Host + req.URL.Path
	if tok, ok := t.cached(key); ok {
		return t.base.RoundTrip(withToken(req, tok))
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		return resp, err
	}
	ch, ok := parseBearer(resp.Header.Get("WWW-Authenticate"))
	if !ok {
		return resp, nil
	}
	resp.Body.Close()
	tok, err := t.fetchToken(req.Context(), ch)
	if err != nil {
		return nil, fmt.Errorf("registry token for %s: %w", req.URL.Host, err)
	}
	t.mu.Lock()
	if t.tokens == nil {
		t.tokens = map[string]registryToken{}
	}
	t.tokens[key] = tok
	t.mu.Unlock()
	return t.base.RoundTrip(withToken(req, tok.value))
}

func (t *registryAuth) cached(key string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tok, ok := t.tokens[key]
	if !ok || time.Now().After(tok.expires) {
		return "", false
	}
	return tok.value, true
}

// withToken returns a copy of req carrying the token. Go's client does
// not forward the header when the registry redirects to its storage host.
func withToken(req *http.Request, tok string) *http.Request {
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+tok)
	return r
}

type bearerChallenge struct{ realm, service, scope string }

var challengeParam = regexp.MustCompile(`([a-z]+)="([^"]*)"`)

// parseBearer reads `Bearer realm="…",service="…",scope="…"`.
func parseBearer(h string) (bearerChallenge, bool) {
	if !strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return bearerChallenge{}, false
	}
	var c bearerChallenge
	for _, m := range challengeParam.FindAllStringSubmatch(h[len("bearer "):], -1) {
		switch m[1] {
		case "realm":
			c.realm = m[2]
		case "service":
			c.service = m[2]
		case "scope":
			c.scope = m[2]
		}
	}
	u, err := url.Parse(c.realm)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return bearerChallenge{}, false
	}
	// Only read access is ever requested.
	if c.scope != "" && !strings.HasSuffix(c.scope, ":pull") {
		return bearerChallenge{}, false
	}
	return c, true
}

func (t *registryAuth) fetchToken(ctx context.Context, c bearerChallenge) (registryToken, error) {
	u, _ := url.Parse(c.realm)
	q := u.Query()
	if c.service != "" {
		q.Set("service", c.service)
	}
	if c.scope != "" {
		q.Set("scope", c.scope)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return registryToken{}, err
	}
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return registryToken{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return registryToken{}, fmt.Errorf("token service answered %s", resp.Status)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return registryToken{}, err
	}
	tok := body.Token
	if tok == "" {
		tok = body.AccessToken
	}
	if tok == "" {
		return registryToken{}, errors.New("token service sent no token")
	}
	life := time.Duration(body.ExpiresIn) * time.Second
	if life < time.Minute {
		life = 5 * time.Minute // the specification's default lifetime
	}
	// Renew a little early: a segmented download may outlast it.
	return registryToken{value: tok, expires: time.Now().Add(life - 30*time.Second)}, nil
}

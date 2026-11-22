package catalog

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func TestSignedFeed(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	feed := []byte(`{"format":"arcana-catalog","formatVersion":1,"title":"Community","resources":[]}`)
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, feed)) + "\n")
	url := "https://example.org/feed.json"
	key := base64.StdEncoding.EncodeToString(pub)

	p := &FeedProvider{ProviderID: "community", URL: url, PublicKey: key}
	if _, err := p.Fetch(context.Background(), mapFetcher{url: feed, url + ".sig": sig}); err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	tampered := append([]byte(nil), feed...)
	tampered[len(tampered)-3] = ' '
	for name, f := range map[string]mapFetcher{
		"missing signature": {url: feed},
		"tampered feed":     {url: tampered, url + ".sig": sig},
		"garbage signature": {url: feed, url + ".sig": []byte("not base64!")},
	} {
		if _, err := p.Fetch(context.Background(), f); !errors.Is(err, ErrFeedSignature) {
			t.Errorf("%s: %v", name, err)
		}
	}
	p.PublicKey = base64.StdEncoding.EncodeToString(other)
	if _, err := p.Fetch(context.Background(), mapFetcher{url: feed, url + ".sig": sig}); !errors.Is(err, ErrFeedSignature) {
		t.Errorf("other key: %v", err)
	}
	p.PublicKey = "short"
	if _, err := p.Fetch(context.Background(), mapFetcher{url: feed, url + ".sig": sig}); err == nil {
		t.Error("invalid key accepted")
	}
	// Unsigned feeds still work when no key is configured.
	p.PublicKey = ""
	if _, err := p.Fetch(context.Background(), mapFetcher{url: feed}); err != nil {
		t.Fatal(err)
	}
}

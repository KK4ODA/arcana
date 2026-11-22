package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/KK4ODA/arcana/internal/buildinfo"
)

// Provider imports resource descriptions from one source. Implementations
// must be side-effect free: they return normalized resources and never touch
// the library directly.
type Provider interface {
	// ID is the namespace for resource IDs from this provider.
	ID() string
	// Name is shown to users.
	Name() string
	// Fetch retrieves the provider's current resource list.
	Fetch(ctx context.Context, f Fetcher) ([]Resource, error)
}

// Resolver is implemented by providers whose catalog entries need extra,
// per-resource information at download time (for example a metalink that
// lists hashes and mirrors).
type Resolver interface {
	Resolve(ctx context.Context, f Fetcher, r Resource) (Resource, error)
}

// Fetcher performs bounded HTTP GETs. Catalog documents are untrusted, so
// every read has a size cap.
type Fetcher interface {
	Get(ctx context.Context, url string, maxBytes int64) ([]byte, error)
}

// HTTPFetcher is the default Fetcher.
type HTTPFetcher struct {
	Client *http.Client
}

// NewHTTPFetcher returns a fetcher with conservative timeouts.
func NewHTTPFetcher(client *http.Client) *HTTPFetcher {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &HTTPFetcher{Client: client}
}

// UserAgent identifies Arcana to catalog and download servers. It contains
// no information about the user or host.
func UserAgent() string {
	return "Arcana/" + buildinfo.Version + " (+https://github.com/KK4ODA/arcana)"
}

// ErrTooLarge is returned when a catalog document exceeds its size cap.
var ErrTooLarge = errors.New("response exceeds size limit")

// Get fetches url, failing if the body exceeds maxBytes.
func (h *HTTPFetcher) Get(ctx context.Context, url string, maxBytes int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", UserAgent())
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("GET %s: %w (%d bytes)", url, ErrTooLarge, maxBytes)
	}
	return data, nil
}

// ValidateAll validates resources from a provider, forcing them into the
// provider's namespace. Invalid entries are dropped and reported, so one bad
// entry cannot block a whole catalog.
func ValidateAll(providerID string, in []Resource) ([]Resource, []error) {
	var out []Resource
	var errs []error
	seen := map[string]bool{}
	for _, r := range in {
		r.Provider = providerID
		if err := r.Validate(); err != nil {
			errs = append(errs, err)
			continue
		}
		if seen[r.ID] {
			errs = append(errs, fmt.Errorf("%s: duplicate id", r.ID))
			continue
		}
		seen[r.ID] = true
		out = append(out, r)
	}
	return out, errs
}

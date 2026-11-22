package catalog

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
)

// Feed is Arcana's own catalog document format. It is used for the bundled
// curated catalog, community catalogs and user-specified sources. The JSON
// schema is in docs/spec/catalog-feed.schema.json.
type Feed struct {
	Format        string     `json:"format"`
	FormatVersion int        `json:"formatVersion"`
	Title         string     `json:"title"`
	Generated     string     `json:"generated"`
	Resources     []Resource `json:"resources"`
	Presets       []Preset   `json:"presets,omitempty"`
}

// ParseFeed decodes and checks the envelope of a feed document.
func ParseFeed(data []byte) (*Feed, error) {
	var f Feed
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("catalog feed: %w", err)
	}
	if f.Format != "arcana-catalog" {
		return nil, fmt.Errorf("catalog feed: unexpected format %q", f.Format)
	}
	if f.FormatVersion != 1 {
		return nil, fmt.Errorf("catalog feed: unsupported format version %d", f.FormatVersion)
	}
	return &f, nil
}

// FeedProvider serves resources from an Arcana feed, fetched from a URL or
// supplied in memory. Resource IDs must use the provider's own namespace, so
// a third-party feed cannot shadow resources from another provider.
type FeedProvider struct {
	ProviderID   string
	ProviderName string
	URL          string
	Data         []byte
}

// ID implements Provider.
func (p *FeedProvider) ID() string { return p.ProviderID }

// Name implements Provider.
func (p *FeedProvider) Name() string { return p.ProviderName }

// Fetch implements Provider.
func (p *FeedProvider) Fetch(ctx context.Context, f Fetcher) ([]Resource, error) {
	data := p.Data
	if data == nil {
		if !isHTTPURL(p.URL) {
			return nil, fmt.Errorf("feed %s: invalid URL", p.ProviderID)
		}
		var err error
		data, err = f.Get(ctx, p.URL, 32<<20)
		if err != nil {
			return nil, err
		}
	}
	feed, err := ParseFeed(data)
	if err != nil {
		return nil, err
	}
	return feed.Resources, nil
}

// Resolve implements Resolver for feed entries that use metalinks.
func (p *FeedProvider) Resolve(ctx context.Context, f Fetcher, r Resource) (Resource, error) {
	return ResolveMetalinks(ctx, f, r)
}

//go:embed bundled/catalog.json
var bundledCatalog []byte

//go:embed bundled/presets.json
var bundledPresets []byte

// Bundled returns the curated catalog compiled into Arcana. It works with no
// network access at all.
func Bundled() *FeedProvider {
	return &FeedProvider{ProviderID: "arcana", ProviderName: "Arcana curated catalog", Data: bundledCatalog}
}

// BundledPresets returns the curated collection presets.
func BundledPresets() ([]Preset, error) {
	var doc struct {
		Presets []Preset `json:"presets"`
	}
	if err := json.Unmarshal(bundledPresets, &doc); err != nil {
		return nil, err
	}
	return doc.Presets, nil
}

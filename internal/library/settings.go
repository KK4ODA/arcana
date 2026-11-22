package library

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/KK4ODA/arcana/internal/fsutil"
)

// Settings are stored on the drive (user/settings.json), never in the host
// OS, so a library behaves identically on every computer.
type Settings struct {
	// ReserveBytes is free space Arcana will never fill with downloads.
	// Zero means "use the default for this volume".
	ReserveBytes int64 `json:"reserveBytes"`
	// MaxConcurrentDownloads limits parallel transfers (default 2).
	MaxConcurrentDownloads int `json:"maxConcurrentDownloads"`
	// BandwidthLimit in bytes per second; zero is unlimited.
	BandwidthLimit int64 `json:"bandwidthLimit"`
	// AllowPrivateNetworkDownloads permits catalog URLs that resolve to
	// LAN, loopback or link-local addresses. Off by default so a remote
	// catalog cannot make Arcana send requests to devices on the local
	// network.
	AllowPrivateNetworkDownloads bool            `json:"allowPrivateNetworkDownloads"`
	Server                       ServerSettings  `json:"server"`
	Catalog                      CatalogSettings `json:"catalog"`
}

// ServerSettings control the embedded HTTP server.
type ServerSettings struct {
	// ShareOnLAN binds to all interfaces instead of loopback only.
	ShareOnLAN bool `json:"shareOnLan"`
	// Port for the application UI and API (default 8470). Reference content
	// is served on Port+1 to keep it in a separate browser origin.
	Port int `json:"port"`
}

// CatalogSettings select which catalog providers are active.
type CatalogSettings struct {
	KiwixEnabled bool         `json:"kiwixEnabled"`
	KiwixURL     string       `json:"kiwixUrl"`
	Feeds        []FeedSource `json:"feeds"`
}

// FeedSource is a user-added Arcana JSON catalog feed.
type FeedSource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

// DefaultSettings returns settings for a new library.
func DefaultSettings() Settings {
	return Settings{
		MaxConcurrentDownloads: 2,
		Server:                 ServerSettings{Port: 8470},
		Catalog: CatalogSettings{
			KiwixEnabled: true,
			KiwixURL:     "https://library.kiwix.org",
		},
	}
}

// DefaultReserve picks a reserve for a volume of the given size: 5%, at
// least 1 GB and at most 50 GB.
func DefaultReserve(total int64) int64 {
	const GB = 1_000_000_000
	r := total / 20
	if r < GB {
		r = GB
	}
	if r > 50*GB {
		r = 50 * GB
	}
	return r
}

// SettingsStore loads and saves settings. Unknown keys written by newer
// versions of Arcana are preserved when an older version saves the file.
type SettingsStore struct {
	mu       sync.Mutex
	path     string
	readOnly bool
	current  Settings
	raw      map[string]json.RawMessage
}

// LoadSettings reads the settings file, creating defaults if absent.
func LoadSettings(path string, readOnly bool) (*SettingsStore, error) {
	s := &SettingsStore{path: path, readOnly: readOnly, current: DefaultSettings(), raw: map[string]json.RawMessage{}}
	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return s, nil
	case err != nil:
		return nil, err
	}
	if err := json.Unmarshal(data, &s.raw); err != nil {
		// A damaged settings file must not prevent access to the library.
		// Keep a copy for diagnosis and continue with defaults.
		if !readOnly {
			_ = os.Rename(path, path+".damaged")
		}
		s.raw = map[string]json.RawMessage{}
		return s, nil
	}
	if err := json.Unmarshal(data, &s.current); err != nil {
		return nil, fmt.Errorf("settings: %w", err)
	}
	if s.current.MaxConcurrentDownloads <= 0 {
		s.current.MaxConcurrentDownloads = 2
	}
	if s.current.Server.Port <= 0 || s.current.Server.Port > 65534 {
		s.current.Server.Port = 8470
	}
	return s, nil
}

// Get returns a copy of the current settings.
func (s *SettingsStore) Get() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.current
	c.Catalog.Feeds = append([]FeedSource(nil), s.current.Catalog.Feeds...)
	return c
}

// ErrReadOnly is returned when modifying a read-only library.
var ErrReadOnly = errors.New("the library is read-only")

// Update applies fn to a copy of the settings and saves the result
// atomically.
func (s *SettingsStore) Update(fn func(*Settings) error) (Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readOnly {
		return s.current, ErrReadOnly
	}
	next := s.current
	next.Catalog.Feeds = append([]FeedSource(nil), s.current.Catalog.Feeds...)
	if err := fn(&next); err != nil {
		return s.current, err
	}
	known, err := json.Marshal(next)
	if err != nil {
		return s.current, err
	}
	var knownMap map[string]json.RawMessage
	if err := json.Unmarshal(known, &knownMap); err != nil {
		return s.current, err
	}
	merged := map[string]json.RawMessage{}
	for k, v := range s.raw {
		merged[k] = v
	}
	for k, v := range knownMap {
		merged[k] = v
	}
	data, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return s.current, err
	}
	if err := fsutil.WriteFileAtomic(s.path, append(data, '\n'), 0o644); err != nil {
		return s.current, err
	}
	s.current = next
	s.raw = merged
	return next, nil
}

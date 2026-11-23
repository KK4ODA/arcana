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
	Updates                      UpdateSettings  `json:"updates"`
	Maps                         MapSettings     `json:"maps"`
	GPS                          GPSSettings     `json:"gps"`
	// Peers are other Arcana libraries on the network to copy from.
	Peers []PeerSource `json:"peers,omitempty"`
}

// PeerSource is another Arcana whose integration API this library may
// read. Token is that library's access token (scope library:read); it only
// grants reading, and is stored in clear text because Arcana must send it.
type PeerSource struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	URL   string `json:"url"`
	Token string `json:"token"`
}

// MapSettings control the map region provider.
type MapSettings struct {
	// Enabled offers OpenStreetMap regions extracted from Protomaps planet
	// builds. Region sizes are bundled, so browsing works offline.
	Enabled bool `json:"enabled"`
	// BuildsURL lists planet builds; BaseURL serves them. Empty means the
	// Protomaps defaults.
	BuildsURL string `json:"buildsUrl,omitempty"`
	BaseURL   string `json:"baseUrl,omitempty"`
	// RoadsURL is the index of OSM extracts road networks are built from.
	// Empty means Geofabrik's.
	RoadsURL string `json:"roadsUrl,omitempty"`
}

// GPSSettings select a position source. Arcana only reads positions; it
// never sends them anywhere.
type GPSSettings struct {
	// Source is "", "serial", "udp", "tcp", "gpsd", "simulator" or "manual".
	Source     string `json:"source"`
	SerialPort string `json:"serialPort,omitempty"`
	BaudRate   int    `json:"baudRate,omitempty"`
	// UDPPort listens for NMEA datagrams (default 10110).
	UDPPort int `json:"udpPort,omitempty"`
	// Address is host:port for "tcp" (an NMEA stream) or "gpsd".
	Address string `json:"address,omitempty"`
	// SimulatorTrack is a GPX file in the user folder replayed as NMEA.
	SimulatorTrack string `json:"simulatorTrack,omitempty"`
	// SimulatorSpeed multiplies the replay rate (default 1).
	SimulatorSpeed float64 `json:"simulatorSpeed,omitempty"`
	// Manual is a fixed position for "manual".
	ManualLat float64 `json:"manualLat,omitempty"`
	ManualLon float64 `json:"manualLon,omitempty"`
	// ShareWithClients shows the position to LAN clients. Off by default.
	ShareWithClients bool `json:"shareWithClients"`
	// RecordTrack keeps a breadcrumb track while a fix is available.
	RecordTrack bool `json:"recordTrack"`
}

// ServerSettings control the embedded HTTP server.
type ServerSettings struct {
	// ShareOnLAN binds to all interfaces instead of loopback only.
	ShareOnLAN bool `json:"shareOnLan"`
	// Port for the application UI and API (default 8470). Reference content
	// is served on Port+1 to keep it in a separate browser origin.
	Port int `json:"port"`
	// MDNS advertises the library as <Name>.local while sharing.
	MDNS bool `json:"mdns"`
	// Name is the mDNS host label (default "arcana").
	Name string `json:"name,omitempty"`
	// TLS is "" (HTTP only), "self-signed" or "custom" (user/tls/cert.pem
	// and key.pem). HTTPS is offered on the same ports as HTTP.
	TLS string `json:"tls,omitempty"`
	// HotspotSSID and HotspotPassword are used when Arcana starts a Wi-Fi
	// hotspot (Linux with NetworkManager). The password is stored in clear
	// text because it is shown to people joining.
	HotspotSSID     string `json:"hotspotSsid,omitempty"`
	HotspotPassword string `json:"hotspotPassword,omitempty"`
}

// CatalogSettings select which catalog providers are active.
type CatalogSettings struct {
	KiwixEnabled bool   `json:"kiwixEnabled"`
	KiwixURL     string `json:"kiwixUrl"`
	// GutenbergEnabled imports Project Gutenberg's practical bookshelves.
	GutenbergEnabled bool         `json:"gutenbergEnabled"`
	GutenbergMirror  string       `json:"gutenbergMirror"`
	Feeds            []FeedSource `json:"feeds"`
	// AutoRefreshHours, when positive, refreshes catalogs in the background
	// while Arcana is running. Off by default: Arcana makes no network
	// requests unless asked.
	AutoRefreshHours int `json:"autoRefreshHours"`
}

// UpdateSettings control application updates.
type UpdateSettings struct {
	// URL is where release.json and its signature are published.
	URL string `json:"url"`
	// AutoCheck looks for a new version once a day while Arcana runs and
	// the computer is online (only the update address is contacted).
	AutoCheck bool `json:"autoCheck"`
}

// FeedSource is a user-added Arcana JSON catalog feed.
type FeedSource struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
	// PublicKey (base64 Ed25519) makes Arcana accept the feed only with a
	// valid signature from this key.
	PublicKey string `json:"publicKey,omitempty"`
}

// DefaultSettings returns settings for a new library.
func DefaultSettings() Settings {
	return Settings{
		MaxConcurrentDownloads: 2,
		Server:                 ServerSettings{Port: 8470, MDNS: true, Name: "arcana"},
		Catalog: CatalogSettings{
			KiwixEnabled:    true,
			KiwixURL:        "https://library.kiwix.org",
			GutenbergMirror: "https://gutenberg.pglaf.org",
		},
		Updates: UpdateSettings{URL: "https://github.com/KK4ODA/arcana/releases/latest/download", AutoCheck: true},
		Maps:    MapSettings{Enabled: true},
		GPS:     GPSSettings{BaudRate: 4800, UDPPort: 10110, SimulatorSpeed: 1},
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
	c.Peers = append([]PeerSource(nil), s.current.Peers...)
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

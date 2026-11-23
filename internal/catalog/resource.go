// Package catalog defines Arcana's provider-independent resource model and
// the providers that populate it.
//
// Everything a provider returns is treated as untrusted input: identifiers,
// file names and URLs are validated before they reach the database, the
// download manager or the file system.
package catalog

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/KK4ODA/arcana/internal/fsutil"
)

// Resource is a downloadable unit of knowledge at a specific version.
//
// ID identifies the resource across versions ("kiwix.wikipedia_en_all_maxi");
// Version distinguishes releases ("2026-10"). Family groups alternatives
// that cover the same material at different detail levels (maxi, nopic,
// mini), so the UI can compare them.
type Resource struct {
	ID          string   `json:"id"`
	Provider    string   `json:"provider"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Category    string   `json:"category"`
	Subcategory string   `json:"subcategory,omitempty"`
	Tags        []string `json:"tags,omitempty"`

	Family  string `json:"family,omitempty"`
	Variant string `json:"variant,omitempty"`
	// VariantLabel is a human description of the variant ("No images").
	VariantLabel string `json:"variantLabel,omitempty"`

	Publisher string `json:"publisher,omitempty"`
	Creator   string `json:"creator,omitempty"`
	// SourceURL is the publisher's page for the material.
	SourceURL string `json:"sourceUrl,omitempty"`

	Version string `json:"version"`
	// Published is the content date (YYYY-MM-DD or YYYY-MM).
	Published string `json:"published,omitempty"`
	// LastVerified is when the catalog entry was last confirmed against the
	// source.
	LastVerified *time.Time `json:"lastVerified,omitempty"`

	// Languages are BCP 47 tags ("en", "es", "pt-BR"); "mul" for
	// multilingual.
	Languages []string `json:"languages"`
	Coverage  Coverage `json:"coverage"`

	Format string `json:"format"`
	Files  []File `json:"files"`
	// DownloadSize and InstalledSize are totals in bytes.
	DownloadSize  int64 `json:"downloadSize"`
	InstalledSize int64 `json:"installedSize"`
	// SizeEstimated is true when sizes are approximate (exact values are
	// only known once the provider publishes file metadata).
	SizeEstimated bool `json:"sizeEstimated,omitempty"`

	License License `json:"license"`

	Capabilities Capabilities `json:"capabilities"`
	Media        Media        `json:"media"`

	Dependencies   []string `json:"dependencies,omitempty"`
	UpdateStrategy string   `json:"updateStrategy"`

	Warnings []Warning `json:"warnings,omitempty"`

	// Stats are optional descriptive counters (articles, media).
	Stats map[string]int64 `json:"stats,omitempty"`
	// Popularity is a provider-specific rank; higher is more popular.
	Popularity int `json:"popularity,omitempty"`
	// ProviderRef carries provider-private data needed to resolve the
	// resource at download time (e.g. a metalink URL or a ZIM UUID).
	ProviderRef map[string]string `json:"providerRef,omitempty"`
}

// File is one file belonging to a resource.
type File struct {
	// Name is the file name on disk. Validated as a single safe component.
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Hashes maps algorithm ("sha256", "sha512", "sha1", "md5") to a lower
	// case hex digest.
	Hashes map[string]string `json:"hashes,omitempty"`
	// Mirrors are candidate download URLs in preference order.
	Mirrors []Mirror `json:"mirrors,omitempty"`
	// MetalinkURL, when set, is resolved at download time to obtain
	// authoritative size, hashes and mirrors.
	MetalinkURL string `json:"metalinkUrl,omitempty"`
}

// Mirror is a download location.
type Mirror struct {
	URL      string `json:"url"`
	Priority int    `json:"priority,omitempty"`
	Location string `json:"location,omitempty"`
}

// Coverage describes geographic scope.
type Coverage struct {
	// Scope is "global", "regional" or "none" (not geographic).
	Scope string `json:"scope"`
	// Regions are ISO 3166-1/2 codes ("US", "US-VA").
	Regions []string `json:"regions,omitempty"`
	// BBox is [west, south, east, north] in WGS84 degrees.
	BBox []float64 `json:"bbox,omitempty"`
}

// License records the terms content is distributed under.
type License struct {
	// SPDX identifier where one exists ("CC-BY-SA-4.0"), otherwise a short
	// name.
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
	// Redistribution is "permitted", "noncommercial", "restricted" or
	// "unknown". Arcana never claims "permitted" unless Verified is true.
	Redistribution string `json:"redistribution"`
	Attribution    string `json:"attribution,omitempty"`
	// Verified means a person checked the license against the source.
	Verified bool   `json:"verified"`
	Notes    string `json:"notes,omitempty"`
}

// Capabilities describe what Arcana can do with the resource offline.
type Capabilities struct {
	FullTextSearch bool `json:"fullTextSearch"`
	TitleSearch    bool `json:"titleSearch"`
}

// Media describes the richness of the content.
type Media struct {
	Pictures bool `json:"pictures"`
	Videos   bool `json:"videos"`
	// Detail is "full", "reduced" or "summary".
	Detail string `json:"detail,omitempty"`
}

// Warning is shown wherever the resource is listed or read.
type Warning struct {
	// Kind is "medical", "regulatory", "license", "outdated" or "content".
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Update strategies.
const (
	UpdateReplace = "snapshot-replace"
	UpdateNone    = "none"
)

// Redistribution states.
const (
	RedistPermitted     = "permitted"
	RedistNonCommercial = "noncommercial"
	RedistRestricted    = "restricted"
	RedistUnknown       = "unknown"
)

var (
	idRe       = regexp.MustCompile(`^[a-z0-9]+(\.[a-z0-9][a-z0-9._-]*)?$`)
	providerRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	versionRe  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	hexRe      = regexp.MustCompile(`^[0-9a-f]+$`)
	langRe     = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)
)

// MaxResourceSize bounds what a catalog may claim (64 TB); larger values are
// treated as corrupt metadata.
const MaxResourceSize = 64 << 40

var knownFormats = map[string]bool{
	"zim": true, "pdf": true, "epub": true, "html": true, "markdown": true, "text": true,
	"image": true, "audio": true, "video": true, "pmtiles": true, "mbtiles": true, "gpx": true,
	"archive": true, "arcroute": true,
}

var hashLengths = map[string]int{"sha256": 64, "sha512": 128, "sha1": 40, "md5": 32}

// Validate checks a resource from an untrusted source and normalizes
// harmless variations. It returns an error describing the first problem.
func (r *Resource) Validate() error {
	r.ID = strings.TrimSpace(r.ID)
	if len(r.ID) > 128 || !idRe.MatchString(r.ID) {
		return fmt.Errorf("invalid resource id %q", r.ID)
	}
	if strings.HasSuffix(r.ID, ".") {
		return fmt.Errorf("invalid resource id %q", r.ID)
	}
	if err := fsutil.ValidateFileName(r.ID); err != nil {
		return fmt.Errorf("resource id %q is not a portable directory name: %w", r.ID, err)
	}
	if !providerRe.MatchString(r.Provider) {
		return fmt.Errorf("%s: invalid provider %q", r.ID, r.Provider)
	}
	if !strings.HasPrefix(r.ID, r.Provider+".") && r.ID != r.Provider {
		return fmt.Errorf("%s: id must be prefixed with provider %q", r.ID, r.Provider)
	}
	if !versionRe.MatchString(r.Version) || strings.HasSuffix(r.Version, ".") {
		return fmt.Errorf("%s: invalid version %q", r.ID, r.Version)
	}
	r.Title = cleanText(r.Title, 200)
	if r.Title == "" {
		return fmt.Errorf("%s: missing title", r.ID)
	}
	r.Description = cleanText(r.Description, 4000)
	r.Publisher = cleanText(r.Publisher, 200)
	r.Creator = cleanText(r.Creator, 200)
	r.VariantLabel = cleanText(r.VariantLabel, 80)
	if !IsCategory(r.Category) {
		r.Category = CategoryReference
	}
	if !knownFormats[r.Format] {
		return fmt.Errorf("%s: unsupported format %q", r.ID, r.Format)
	}
	if r.SourceURL != "" && !isHTTPURL(r.SourceURL) {
		r.SourceURL = ""
	}
	for i, l := range r.Languages {
		r.Languages[i] = strings.TrimSpace(l)
		if !langRe.MatchString(r.Languages[i]) {
			return fmt.Errorf("%s: invalid language tag %q", r.ID, l)
		}
	}
	if len(r.Languages) == 0 {
		r.Languages = []string{"und"}
	}
	if len(r.Files) == 0 {
		return fmt.Errorf("%s: no files", r.ID)
	}
	seen := map[string]bool{}
	var total int64
	for i := range r.Files {
		f := &r.Files[i]
		if err := fsutil.ValidateFileName(f.Name); err != nil {
			return fmt.Errorf("%s: file %q: %w", r.ID, f.Name, err)
		}
		lower := strings.ToLower(f.Name)
		if seen[lower] {
			return fmt.Errorf("%s: duplicate file name %q", r.ID, f.Name)
		}
		seen[lower] = true
		if f.Size < 0 || f.Size > MaxResourceSize {
			return fmt.Errorf("%s: implausible size %d", r.ID, f.Size)
		}
		total += f.Size
		for algo, sum := range f.Hashes {
			want, ok := hashLengths[algo]
			sum = strings.ToLower(sum)
			if !ok || len(sum) != want || !hexRe.MatchString(sum) {
				return fmt.Errorf("%s: invalid %s hash", r.ID, algo)
			}
			f.Hashes[algo] = sum
		}
		if len(f.Mirrors) == 0 && f.MetalinkURL == "" {
			return fmt.Errorf("%s: file %q has no download location", r.ID, f.Name)
		}
		for _, m := range f.Mirrors {
			if !isHTTPURL(m.URL) {
				return fmt.Errorf("%s: mirror URL must be http(s): %q", r.ID, m.URL)
			}
		}
		if f.MetalinkURL != "" && !isHTTPURL(f.MetalinkURL) {
			return fmt.Errorf("%s: metalink URL must be http(s)", r.ID)
		}
	}
	if r.DownloadSize <= 0 {
		r.DownloadSize = total
	}
	if r.InstalledSize <= 0 {
		r.InstalledSize = r.DownloadSize
	}
	if r.DownloadSize > MaxResourceSize || r.InstalledSize > MaxResourceSize {
		return fmt.Errorf("%s: implausible size", r.ID)
	}
	switch r.License.Redistribution {
	case RedistPermitted, RedistNonCommercial, RedistRestricted, RedistUnknown:
	default:
		r.License.Redistribution = RedistUnknown
	}
	// A provider cannot assert redistribution rights without a verified
	// license record.
	if !r.License.Verified && r.License.Redistribution == RedistPermitted {
		r.License.Redistribution = RedistUnknown
	}
	if r.License.ID == "" {
		r.License.ID = "unknown"
	}
	if r.License.URL != "" && !isHTTPURL(r.License.URL) {
		r.License.URL = ""
	}
	if r.UpdateStrategy == "" {
		r.UpdateStrategy = UpdateReplace
	}
	if r.Coverage.Scope == "" {
		r.Coverage.Scope = "none"
	}
	if len(r.Coverage.BBox) != 0 && len(r.Coverage.BBox) != 4 {
		return fmt.Errorf("%s: bbox must have 4 numbers", r.ID)
	}
	for i := range r.Warnings {
		r.Warnings[i].Text = cleanText(r.Warnings[i].Text, 1000)
	}
	for _, d := range r.Dependencies {
		if !idRe.MatchString(d) {
			return fmt.Errorf("%s: invalid dependency %q", r.ID, d)
		}
	}
	sort.Strings(r.Tags)
	return nil
}

// Unverifiable reports whether any file lacks a cryptographic hash and has
// no metalink from which one could be obtained.
func (r *Resource) Unverifiable() bool {
	for _, f := range r.Files {
		if f.Hashes["sha256"] == "" && f.Hashes["sha512"] == "" && f.MetalinkURL == "" {
			return true
		}
	}
	return false
}

func isHTTPURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	return (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
}

// cleanText strips control characters and bounds length. Catalog text is
// rendered as text (never HTML) by the UI; this is defense in depth.
func cleanText(s string, max int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut] + "…"
	}
	return s
}

// ErrNotFound is returned for unknown resource IDs.
var ErrNotFound = errors.New("resource not found")

// CompareVersions orders two version strings. Date-like versions
// ("2026-10", "2026-10-05", Kiwix rebuilds such as "2026-11a") and dotted
// numbers ("1.2.10") compare numerically component by component. A trailing
// alphabetic component ("1.0-rc1") marks a pre-release and sorts before the
// release. It returns -1, 0 or +1.
func CompareVersions(a, b string) int {
	if a == b {
		return 0
	}
	pa := splitVersion(a)
	pb := splitVersion(b)
	for i := 0; i < len(pa) || i < len(pb); i++ {
		if i >= len(pa) {
			return extraSign(pb[i])
		}
		if i >= len(pb) {
			return -extraSign(pa[i])
		}
		if c := compareComponent(pa[i], pb[i]); c != 0 {
			return c
		}
	}
	return 0
}

// extraSign is the ordering contributed by a component present only in the
// longer version: +1 if it extends the version (2026-10-05 > 2026-10), -1
// if it is a pre-release label (1.0-rc1 < 1.0).
func extraSign(c string) int {
	if c != "" && (c[0] < '0' || c[0] > '9') {
		return 1 // the shorter version is the release
	}
	return -1
}

func splitVersion(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool { return r == '.' || r == '-' || r == '_' })
}

// compareComponent compares a numeric prefix, then any letter suffix
// ("11" < "11a" < "11b"). Numbers sort after pure labels.
func compareComponent(a, b string) int {
	an, as, aok := numPrefix(a)
	bn, bs, bok := numPrefix(b)
	switch {
	case aok && bok:
		switch {
		case an < bn:
			return -1
		case an > bn:
			return 1
		}
		return strings.Compare(as, bs)
	case aok:
		return 1
	case bok:
		return -1
	}
	return strings.Compare(a, b)
}

func numPrefix(s string) (int64, string, bool) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, s, false
	}
	n, ok := atoi(s[:i])
	return n, s[i:], ok
}

func atoi(s string) (int64, bool) {
	if s == "" || len(s) > 18 {
		return 0, false
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}

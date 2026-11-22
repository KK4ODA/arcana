package catalog

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// KiwixProvider imports the Kiwix OPDS v2 catalog
// (https://library.kiwix.org/catalog/v2/entries). The OPDS feed carries
// descriptive metadata and a link to a Metalink file; exact sizes, SHA-256
// hashes and mirrors are obtained from that Metalink at download time.
type KiwixProvider struct {
	// BaseURL is the catalog host, e.g. "https://library.kiwix.org".
	BaseURL string
	// PageSize controls OPDS pagination.
	PageSize int
	// Now is injectable for tests.
	Now func() time.Time
}

// ID implements Provider.
func (k *KiwixProvider) ID() string { return "kiwix" }

// Name implements Provider.
func (k *KiwixProvider) Name() string { return "Kiwix library" }

type opdsFeed struct {
	TotalResults int         `xml:"totalResults"`
	Entries      []opdsEntry `xml:"entry"`
}

type opdsEntry struct {
	ID           string `xml:"id"`
	Title        string `xml:"title"`
	Updated      string `xml:"updated"`
	Summary      string `xml:"summary"`
	Language     string `xml:"language"`
	Name         string `xml:"name"`
	Flavour      string `xml:"flavour"`
	Category     string `xml:"category"`
	Tags         string `xml:"tags"`
	ArticleCount int64  `xml:"articleCount"`
	MediaCount   int64  `xml:"mediaCount"`
	Issued       string `xml:"issued"`
	Author       struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Publisher struct {
		Name string `xml:"name"`
	} `xml:"publisher"`
	Links []struct {
		Rel    string `xml:"rel,attr"`
		Type   string `xml:"type,attr"`
		Href   string `xml:"href,attr"`
		Length int64  `xml:"length,attr"`
	} `xml:"link"`
}

// maxCatalogBytes caps one OPDS page. The complete Kiwix catalog (about 3,700
// entries) is roughly 4.5 MB.
const maxCatalogBytes = 64 << 20

// Fetch implements Provider.
func (k *KiwixProvider) Fetch(ctx context.Context, f Fetcher) ([]Resource, error) {
	base := strings.TrimRight(k.BaseURL, "/")
	if base == "" {
		base = "https://library.kiwix.org"
	}
	if !isHTTPURL(base) {
		return nil, fmt.Errorf("kiwix: invalid catalog URL %q", base)
	}
	size := k.PageSize
	if size <= 0 {
		size = 1000
	}
	var all []Resource
	for start := 0; start < 100000; start += size {
		u := fmt.Sprintf("%s/catalog/v2/entries?start=%d&count=%d", base, start, size)
		data, err := f.Get(ctx, u, maxCatalogBytes)
		if err != nil {
			return nil, fmt.Errorf("kiwix catalog: %w", err)
		}
		page, total, err := k.ParseOPDS(data, base)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) == 0 || start+size >= total {
			break
		}
	}
	return all, nil
}

// ParseOPDS converts an OPDS acquisition feed into resources. base resolves
// relative links. Entries that cannot be mapped are skipped.
func (k *KiwixProvider) ParseOPDS(data []byte, base string) ([]Resource, int, error) {
	var feed opdsFeed
	dec := xml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&feed); err != nil {
		return nil, 0, fmt.Errorf("kiwix catalog: %w", err)
	}
	now := time.Now
	if k.Now != nil {
		now = k.Now
	}
	verified := now().UTC().Truncate(time.Second)
	baseURL, err := url.Parse(base + "/")
	if err != nil {
		return nil, 0, err
	}
	var out []Resource
	for _, e := range feed.Entries {
		r, ok := k.mapEntry(e, baseURL)
		if !ok {
			continue
		}
		r.LastVerified = &verified
		out = append(out, r)
	}
	return out, feed.TotalResults, nil
}

var (
	kiwixNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,100}$`)
	// Kiwix file names end in _YYYY-MM or _YYYY-MM-DD, sometimes with a
	// letter suffix for same-month rebuilds ("2026-11a").
	kiwixVersionRe = regexp.MustCompile(`_(\d{4}-\d{2}(?:-\d{2})?[a-z]?)\.zim$`)
)

func (k *KiwixProvider) mapEntry(e opdsEntry, base *url.URL) (Resource, bool) {
	name := strings.TrimSpace(e.Name)
	flavour := strings.TrimSpace(e.Flavour)
	if !kiwixNameRe.MatchString(name) || (flavour != "" && !kiwixNameRe.MatchString(flavour)) {
		return Resource{}, false
	}
	var metalink string
	var length int64
	for _, l := range e.Links {
		if l.Rel == "http://opds-spec.org/acquisition/open-access" && strings.HasSuffix(l.Href, ".meta4") {
			ref, err := url.Parse(l.Href)
			if err != nil {
				continue
			}
			metalink = base.ResolveReference(ref).String()
			length = l.Length
		}
	}
	if metalink == "" {
		return Resource{}, false
	}
	fileName := path.Base(strings.TrimSuffix(mustPath(metalink), ".meta4"))
	version := ""
	if m := kiwixVersionRe.FindStringSubmatch(fileName); m != nil {
		version = m[1]
	} else if len(e.Updated) >= 10 {
		version = e.Updated[:10]
	}
	family := strings.ToLower(name)
	id := "kiwix." + family
	if flavour != "" {
		id += "_" + strings.ToLower(flavour)
	}
	id = strings.ReplaceAll(id, "+", "plus")

	tags := parseKiwixTags(e.Tags)
	langs := mapLanguages(e.Language)
	category, sub := kiwixCategory(name, e.Category)

	r := Resource{
		ID:           id,
		Provider:     "kiwix",
		Title:        e.Title,
		Description:  e.Summary,
		Category:     category,
		Subcategory:  sub,
		Family:       "kiwix." + strings.ReplaceAll(family, "+", "plus"),
		Variant:      flavourOr(flavour),
		VariantLabel: variantLabel(flavour),
		Publisher:    firstNonEmpty(e.Author.Name, e.Publisher.Name),
		Creator:      e.Author.Name,
		Version:      version,
		Published:    dateOnly(firstNonEmpty(e.Issued, e.Updated)),
		Languages:    langs,
		Coverage:     Coverage{Scope: "none"},
		Format:       "zim",
		Files: []File{{
			Name:        fileName,
			MetalinkURL: metalink,
			// Direct URL: used only if the metalink lists no mirrors.
			Mirrors: []Mirror{{URL: strings.TrimSuffix(metalink, ".meta4"), Priority: 1000}},
		}},
		DownloadSize:  length,
		InstalledSize: length,
		Capabilities: Capabilities{
			FullTextSearch: tags["_ftindex"] == "yes",
			TitleSearch:    true,
		},
		Media: Media{
			Pictures: tags["_pictures"] != "no",
			Videos:   tags["_videos"] == "yes",
			Detail:   detailLevel(flavour, tags["_details"]),
		},
		UpdateStrategy: UpdateReplace,
		Stats:          map[string]int64{"articles": e.ArticleCount, "media": e.MediaCount},
		ProviderRef: map[string]string{
			"uuid":    strings.TrimPrefix(e.ID, "urn:uuid:"),
			"name":    name,
			"flavour": flavour,
		},
		License: KiwixLicense(name, e.Category),
	}
	for t := range tags {
		if !strings.HasPrefix(t, "_") && len(r.Tags) < 20 {
			r.Tags = append(r.Tags, t)
		}
	}
	r.Warnings = append(r.Warnings, standardWarnings(r)...)
	return r, true
}

func mustPath(u string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	return p.Path
}

func parseKiwixTags(s string) map[string]string {
	out := map[string]string{}
	for _, t := range strings.Split(s, ";") {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if k, v, ok := strings.Cut(t, ":"); ok && strings.HasPrefix(k, "_") {
			out[k] = v
		} else {
			out[strings.ToLower(t)] = ""
		}
	}
	return out
}

func flavourOr(f string) string {
	if f == "" {
		return "standard"
	}
	return strings.ToLower(f)
}

func variantLabel(f string) string {
	switch strings.ToLower(f) {
	case "maxi":
		return "Full articles with images"
	case "nopic":
		return "Full articles, no images"
	case "mini":
		return "Introductions only, no images"
	case "":
		return "Standard edition"
	}
	return f
}

func detailLevel(flavour, details string) string {
	if strings.EqualFold(flavour, "mini") || details == "no" {
		return "summary"
	}
	if strings.EqualFold(flavour, "nopic") {
		return "reduced"
	}
	return "full"
}

func dateOnly(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// kiwixCategory maps Kiwix categories and well-known names onto Arcana's
// categories. Name rules come first because Kiwix leaves many entries
// uncategorized.
func kiwixCategory(name, category string) (string, string) {
	n := strings.ToLower(name)
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(n, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("medicine", "mdwiki", "wikem", "cdc.gov", "military-medicine", "libretexts.org_en_med"):
		return CategoryMedicine, ""
	case has("first-aid", "firstaid"):
		return CategoryFirstAid, ""
	case has("ham.stackexchange", "amateur-radio", "radio"):
		return CategoryRadio, ""
	case has("water"):
		return CategoryWater, ""
	case has("energypedia", "solar", "electronics.stackexchange", "electrical"):
		return CategoryEnergy, ""
	case has("mechanics.stackexchange", "engineering.stackexchange"):
		return CategoryMechanical, ""
	case has("gardening", "cooking", "food", "agriculture", "farming", "homebrew"):
		return CategoryAgriculture, ""
	case has("post-disaster", "prepper", "outdoors.stackexchange", "survival", "knots"):
		return CategoryPreparedness, ""
	case has("ifixit", "diy.stackexchange", "appropedia", "wikihow", "lowtech", "sustainability"):
		return CategoryPractical, ""
	case has("devdocs", "freecodecamp", "stackoverflow", "superuser", "serverfault", "askubuntu",
		"unix.stackexchange", "raspberrypi", "arduino", "rosetta"):
		return CategoryComputing, ""
	case has("maps_", "openstreetmap"):
		return CategoryMaps, ""
	case has("wikivoyage", "worldfactbook"):
		return CategoryReference, "travel and geography"
	case has("libretexts", "physics", "chemistry", "biology", "math", "astronomy"):
		return CategoryScience, ""
	}
	switch strings.ToLower(category) {
	case "wikipedia", "wiktionary", "wikiquote", "vikidia":
		return CategoryReference, ""
	case "wikibooks", "wikiversity", "phet", "ted", "mooc", "education":
		return CategoryEducation, ""
	case "gutenberg", "wikisource", "books":
		return CategoryHistory, "literature"
	case "ifixit":
		return CategoryPractical, ""
	case "stack_exchange":
		return CategoryReference, "questions and answers"
	}
	return CategoryReference, ""
}

// KiwixLicense returns the license Arcana records for a Kiwix ZIM. Only
// families whose license has been checked against the publisher's terms are
// marked verified; everything else is "unknown" until a maintainer reviews
// it. See docs/design/08-licensing.md.
func KiwixLicense(name, category string) License {
	n := strings.ToLower(name)
	c := strings.ToLower(category)
	wikimedia := map[string]bool{"wikipedia": true, "wiktionary": true, "wikivoyage": true, "wikibooks": true,
		"wikiversity": true, "wikiquote": true, "wikisource": true, "wikinews": true}
	switch {
	case wikimedia[c]:
		return License{ID: "CC-BY-SA-4.0", Name: "Creative Commons Attribution-ShareAlike 4.0",
			URL: "https://creativecommons.org/licenses/by-sa/4.0/", Redistribution: RedistPermitted, Verified: true,
			Attribution: "Text from Wikimedia contributors. Images carry their own licenses.",
			Notes:       "Wikimedia text is CC BY-SA 4.0 (some older revisions also GFDL). Media files have individual free licenses."}
	case c == "stack_exchange":
		return License{ID: "CC-BY-SA-4.0", Name: "Creative Commons Attribution-ShareAlike (2.5/3.0/4.0 by contribution date)",
			URL: "https://stackoverflow.com/help/licensing", Redistribution: RedistPermitted, Verified: true,
			Attribution: "Stack Exchange contributors"}
	case strings.HasPrefix(n, "ifixit"):
		return License{ID: "CC-BY-NC-SA-3.0", Name: "Creative Commons Attribution-NonCommercial-ShareAlike 3.0",
			URL: "https://www.ifixit.com/Info/Licensing", Redistribution: RedistNonCommercial, Verified: true,
			Notes: "Non-commercial use only: may not be sold or bundled with a commercial product."}
	case strings.HasPrefix(n, "khanacademy"):
		return License{ID: "LicenseRef-CC-BY-NC-SA", Name: "Creative Commons Attribution-NonCommercial-ShareAlike",
			URL: "https://support.khanacademy.org/hc/en-us/articles/202262954", Redistribution: RedistNonCommercial, Verified: true,
			Notes: "Non-commercial use only. Khan Academy does not state the license version."}
	case c == "ted":
		return License{ID: "CC-BY-NC-ND-4.0", Name: "Creative Commons Attribution-NonCommercial-NoDerivatives 4.0, with TED usage policy",
			URL:            "https://www.ted.com/about/programs-initiatives/ted-talks/ted-talks-usage-policy",
			Redistribution: RedistRestricted, Verified: true,
			Notes: "TED's usage policy restricts distribution outside TED.com and YouTube. Personal offline viewing only; do not redistribute."}
	case c == "phet":
		return License{ID: "CC-BY-4.0", Name: "Creative Commons Attribution 4.0 (simulations published before 2026-03-29)",
			URL: "https://phet.colorado.edu/en/licensing/html", Redistribution: RedistUnknown, Verified: false,
			Attribution: "PhET Interactive Simulations, University of Colorado Boulder",
			Notes:       "Terms for simulations published after 2026-03-29 have not been verified."}
	case c == "gutenberg":
		return License{ID: "LicenseRef-PD-US", Name: "Public domain in the United States",
			URL: "https://www.gutenberg.org/policy/license.html", Redistribution: RedistPermitted, Verified: true,
			Notes: "Copyright status varies by country. The Project Gutenberg trademark license applies to redistributed copies that keep the Project Gutenberg name."}
	case strings.HasPrefix(n, "theworldfactbook"), strings.HasPrefix(n, "wwwnc.cdc.gov"):
		return License{ID: "LicenseRef-PD-USGov", Name: "US federal government work (public domain in the US)",
			Redistribution: RedistPermitted, Verified: true,
			Notes: "Works of the US federal government are not subject to US copyright (17 U.S.C. 105). Third-party images may be excepted."}
	}
	return License{ID: "unknown", Redistribution: RedistUnknown,
		Notes: "License not yet reviewed by Arcana maintainers. Check the publisher's terms before redistributing."}
}

// standardWarnings attaches warnings that depend on category and age.
func standardWarnings(r Resource) []Warning {
	var w []Warning
	if r.Category == CategoryMedicine || r.Category == CategoryFirstAid {
		w = append(w, Warning{Kind: "medical", Text: fmt.Sprintf(
			"Reference material dated %s. Clinical guidance changes; this is not current medical advice. "+
				"In an emergency, contact emergency services or a qualified clinician whenever possible.",
			firstNonEmpty(r.Published, r.Version))})
	}
	if r.Category == CategoryRadio {
		w = append(w, Warning{Kind: "regulatory",
			Text: "Operating rules and band plans differ by country and change over time. Confirm against your licensing authority's current regulations."})
	}
	if y, ok := yearOf(firstNonEmpty(r.Published, r.Version)); ok && time.Now().Year()-y >= 3 {
		w = append(w, Warning{Kind: "outdated", Text: fmt.Sprintf("This edition was published in %d and may be out of date.", y)})
	}
	return w
}

// iso639_3to1 maps the ISO 639-3 codes Kiwix uses to the shorter ISO 639-1
// form preferred in BCP 47. Codes not listed are kept as three letters,
// which is also valid BCP 47.
var iso639_3to1 = map[string]string{
	"eng": "en", "fra": "fr", "deu": "de", "spa": "es", "por": "pt", "ita": "it", "nld": "nl",
	"rus": "ru", "ukr": "uk", "pol": "pl", "ces": "cs", "slk": "sk", "hun": "hu", "ron": "ro",
	"bul": "bg", "srp": "sr", "hrv": "hr", "slv": "sl", "ell": "el", "tur": "tr", "ara": "ar",
	"heb": "he", "fas": "fa", "urd": "ur", "hin": "hi", "ben": "bn", "tam": "ta", "tel": "te",
	"mar": "mr", "guj": "gu", "pan": "pa", "kan": "kn", "mal": "ml", "zho": "zh", "jpn": "ja",
	"kor": "ko", "vie": "vi", "tha": "th", "ind": "id", "msa": "ms", "fil": "fil", "tgl": "tl",
	"swa": "sw", "amh": "am", "hau": "ha", "yor": "yo", "ibo": "ig", "zul": "zu", "xho": "xh",
	"afr": "af", "swe": "sv", "nor": "no", "nob": "nb", "dan": "da", "fin": "fi", "isl": "is",
	"est": "et", "lav": "lv", "lit": "lt", "cat": "ca", "eus": "eu", "glg": "gl", "epo": "eo",
	"lat": "la", "cym": "cy", "gle": "ga", "kat": "ka", "hye": "hy", "aze": "az", "kaz": "kk",
	"uzb": "uz", "mon": "mn", "nep": "ne", "sin": "si", "khm": "km", "lao": "lo", "mya": "my",
	"som": "so", "kin": "rw", "mlg": "mg", "hat": "ht", "bel": "be", "mkd": "mk", "sqi": "sq",
	"bos": "bs", "pus": "ps", "kur": "ku", "tir": "ti", "orm": "om", "mul": "mul",
}

func mapLanguages(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range strings.Split(s, ",") {
		l = strings.ToLower(strings.TrimSpace(l))
		if l == "" {
			continue
		}
		if short, ok := iso639_3to1[l]; ok {
			l = short
		}
		if !langRe.MatchString(l) || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}
	return out
}

// yearOf extracts a leading four-digit year from a date or version string.
func yearOf(s string) (int, bool) {
	if len(s) < 4 {
		return 0, false
	}
	y, err := strconv.Atoi(s[:4])
	if err != nil || y < 1900 || y > 3000 {
		return 0, false
	}
	return y, true
}

// Resolve implements Resolver: it fetches the Metalink and fills in exact
// size, hashes and mirrors.
func (k *KiwixProvider) Resolve(ctx context.Context, f Fetcher, r Resource) (Resource, error) {
	return ResolveMetalinks(ctx, f, r)
}

// ResolveMetalinks fetches the Metalink of every file that has one and
// replaces its size, hashes and mirror list with the authoritative values.
func ResolveMetalinks(ctx context.Context, f Fetcher, r Resource) (Resource, error) {
	files := make([]File, len(r.Files))
	copy(files, r.Files)
	for i, file := range files {
		if file.MetalinkURL == "" {
			continue
		}
		data, err := f.Get(ctx, file.MetalinkURL, 4<<20)
		if err != nil {
			return r, fmt.Errorf("metalink for %s: %w", file.Name, err)
		}
		mfs, err := ParseMetalink(data)
		if err != nil {
			return r, err
		}
		var mf *MetalinkFile
		for j := range mfs {
			if mfs[j].Name == file.Name {
				mf = &mfs[j]
			}
		}
		if mf == nil {
			if len(mfs) != 1 {
				return r, fmt.Errorf("metalink for %s does not describe that file", file.Name)
			}
			mf = &mfs[0]
		}
		if mf.Size <= 0 {
			return r, fmt.Errorf("metalink for %s has no size", file.Name)
		}
		file.Size = mf.Size
		file.Hashes = mf.Hashes
		if len(mf.Mirrors) > 0 {
			file.Mirrors = mf.Mirrors
		}
		files[i] = file
	}
	r.Files = files
	var total int64
	for _, f := range r.Files {
		total += f.Size
	}
	if total > 0 {
		r.DownloadSize = total
		r.InstalledSize = total
		r.SizeEstimated = false
	}
	if err := r.Validate(); err != nil {
		return r, err
	}
	return r, nil
}

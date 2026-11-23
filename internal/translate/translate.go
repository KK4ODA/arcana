// Package translate offers offline machine translation of archive pages.
//
// Translation runs in the reader's browser: Mozilla's Bergamot engine
// (the WebAssembly build Firefox uses for its own translations) in a
// worker, with language models from Firefox's model registry. Arcana
// downloads the engine and models as catalog resources, verified against
// the SHA-256 Mozilla publishes, keeps them compressed on the drive and
// serves them decompressed (checked against Mozilla's decompressed hash).
// Nothing is translated on the server and nothing leaves the network.
package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/KK4ODA/arcana/internal/catalog"
)

// Catalog formats and identifiers.
const (
	Format           = "translation-models"
	EngineFormat     = "bergamot"
	EngineResourceID = "arcana.translation-engine"
	ProviderID       = "translations"
)

// Mozilla's model registry (Firefox Remote Settings) and attachment CDN.
const (
	DefaultRecordsURL     = "https://firefox.settings.services.mozilla.com/v1/buckets/main/collections/translations-models-v2/records"
	DefaultAttachmentBase = "https://firefox-settings-attachments.cdn.mozilla.net/"
)

// Record is one file in the model registry.
type Record struct {
	Name             string `json:"name"`
	Version          string `json:"version"`
	FileType         string `json:"fileType"`
	SourceLanguage   string `json:"sourceLanguage"`
	TargetLanguage   string `json:"targetLanguage"`
	Architecture     string `json:"architecture"`
	FilterExpression string `json:"filter_expression"`
	DecompressedHash string `json:"decompressedHash"`
	DecompressedSize int64  `json:"decompressedSize"`
	Attachment       struct {
		Hash     string `json:"hash"`
		Size     int64  `json:"size"`
		Filename string `json:"filename"`
		Location string `json:"location"`
	} `json:"attachment"`
}

// ModelFile describes one installed model file (kept in ProviderRef).
type ModelFile struct {
	Name             string `json:"name"` // file name on the drive (compressed)
	FileType         string `json:"fileType"`
	From             string `json:"from"`
	To               string `json:"to"`
	DecompressedHash string `json:"decompressedHash"`
	DecompressedSize int64  `json:"decompressedSize"`
	// Gemm is the engine's integer precision for a model file, chosen
	// from the published file name as Firefox does.
	Gemm string `json:"gemm,omitempty"`
}

// releaseFilters are the record filters desktop Firefox release uses:
// none, or "not on Android". Pre-releases are gated to Nightly.
func releaseRecord(r Record) bool {
	f := strings.TrimSpace(r.FilterExpression)
	if f != "" && f != "env.appinfo.OS != 'Android'" && f != "env.appinfo.OS != 'Android' || env.channel != 'release'" {
		return false
	}
	for _, p := range strings.Split(r.Version, ".") {
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return r.Version != ""
}

func versionLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

var langRe = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,8})*$`)
var hashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

// direction chooses the newest release with a complete set of files.
func direction(records []Record, from, to string) []Record {
	byVersion := map[string]map[string]Record{}
	for _, r := range records {
		if r.SourceLanguage != from || r.TargetLanguage != to || !releaseRecord(r) {
			continue
		}
		if byVersion[r.Version] == nil {
			byVersion[r.Version] = map[string]Record{}
		}
		byVersion[r.Version][r.FileType] = r
	}
	var versions []string
	for v := range byVersion {
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return versionLess(versions[j], versions[i]) })
	for _, v := range versions {
		f := byVersion[v]
		if f["model"].Name == "" || f["lex"].Name == "" {
			continue
		}
		if f["vocab"].Name != "" {
			return []Record{f["model"], f["lex"], f["vocab"]}
		}
		if f["srcvocab"].Name != "" && f["trgvocab"].Name != "" {
			return []Record{f["model"], f["lex"], f["srcvocab"], f["trgvocab"]}
		}
	}
	return nil
}

// Provider offers one language pack per language: models to and from
// English (Bergamot translates between other pairs through English).
type Provider struct {
	RecordsURL     string
	AttachmentBase string
}

// ID implements catalog.Provider.
func (p *Provider) ID() string { return ProviderID }

// Name implements catalog.Provider.
func (p *Provider) Name() string { return "Translation models (Mozilla, as used by Firefox)" }

// Fetch implements catalog.Provider.
func (p *Provider) Fetch(ctx context.Context, f catalog.Fetcher) ([]catalog.Resource, error) {
	u := p.RecordsURL
	if u == "" {
		u = DefaultRecordsURL
	}
	data, err := f.Get(ctx, u, 32<<20)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Data []Record `json:"data"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("translation model registry: %w", err)
	}
	return p.Resources(doc.Data)
}

// Resources builds the language packs from registry records.
func (p *Provider) Resources(records []Record) ([]catalog.Resource, error) {
	base := p.AttachmentBase
	if base == "" {
		base = DefaultAttachmentBase
	}
	bu, err := url.Parse(base)
	if err != nil || bu.Scheme != "https" {
		return nil, fmt.Errorf("translation attachments must come from an HTTPS address")
	}
	langs := map[string]bool{}
	for _, r := range records {
		for _, l := range []string{r.SourceLanguage, r.TargetLanguage} {
			if l != "en" && langRe.MatchString(l) {
				langs[l] = true
			}
		}
	}
	var out []catalog.Resource
	for lang := range langs {
		var files []catalog.File
		var manifest []ModelFile
		var versions []string
		var size int64
		dirs := ""
		for _, d := range [][2]string{{"en", lang}, {lang, "en"}} {
			recs := direction(records, d[0], d[1])
			if recs == nil {
				continue
			}
			if dirs != "" {
				dirs += ","
			}
			dirs += d[0] + "-" + d[1]
			versions = append(versions, recs[0].Version)
			for _, r := range recs {
				loc, err := bu.Parse(r.Attachment.Location)
				if err != nil || loc.Host != bu.Host || !hashRe.MatchString(r.Attachment.Hash) || !hashRe.MatchString(r.DecompressedHash) {
					files = nil
					break
				}
				name := fmt.Sprintf("%s.%s%s.zst", r.FileType, strings.ReplaceAll(d[0], "-", ""), strings.ReplaceAll(d[1], "-", ""))
				files = append(files, catalog.File{Name: name, Size: r.Attachment.Size,
					Hashes: map[string]string{"sha256": r.Attachment.Hash}, Mirrors: []catalog.Mirror{{URL: loc.String()}}})
				mf := ModelFile{Name: name, FileType: r.FileType, From: d[0], To: d[1],
					DecompressedHash: r.DecompressedHash, DecompressedSize: r.DecompressedSize}
				if r.FileType == "model" {
					mf.Gemm = "int8shiftAlphaAll"
					if strings.HasSuffix(r.Name, "intgemm8.bin") {
						mf.Gemm = "int8shiftAll"
					}
				}
				manifest = append(manifest, mf)
				size += r.Attachment.Size
			}
		}
		if len(files) == 0 || len(manifest) != len(files) {
			continue
		}
		mj, _ := json.Marshal(manifest)
		name := LanguageName(lang)
		title := "Translation: " + name + " ↔ English"
		switch dirs {
		case "en-" + lang:
			title = "Translation: English → " + name
		case lang + "-en":
			title = "Translation: " + name + " → English"
		}
		var installed int64
		for _, m := range manifest {
			installed += m.DecompressedSize
		}
		out = append(out, catalog.Resource{
			ID: "translations." + lang, Provider: ProviderID, Title: title,
			Description: "Lets the reader translate archive pages between " + name + " and English on this computer, with no internet connection. " +
				"Pages in other languages are translated through English when both language packs are installed. Machine translation is literal: " +
				"check exact medical or safety wording in the original.",
			Category: catalog.CategoryReference, Subcategory: "translation", Tags: []string{"translation", "language"},
			Family: "translations." + lang, Variant: "standard",
			Publisher: "Mozilla (Firefox Translations)", SourceURL: "https://github.com/mozilla/translations",
			Version: strings.Join(versions, "-"), Languages: []string{lang, "en"}, Coverage: catalog.Coverage{Scope: "none"},
			Format: Format, Files: files, DownloadSize: size, InstalledSize: size,
			License: catalog.License{ID: "MPL-2.0", Name: "Mozilla Public License 2.0", URL: "https://www.mozilla.org/MPL/2.0/",
				Redistribution: catalog.RedistPermitted, Verified: true},
			Media:          catalog.Media{Detail: "full"},
			Dependencies:   []string{EngineResourceID},
			UpdateStrategy: catalog.UpdateReplace,
			Warnings:       []catalog.Warning{{Kind: "content", Text: "Machine translation can be wrong or literal. For medicine, safety and law, check the original wording."}},
			ProviderRef:    map[string]string{"files": string(mj), "directions": dirs},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out, nil
}

// Manifest reads a language pack's file list.
func Manifest(r catalog.Resource) ([]ModelFile, error) {
	var m []ModelFile
	if err := json.Unmarshal([]byte(r.ProviderRef["files"]), &m); err != nil {
		return nil, fmt.Errorf("language pack %s has no file list", r.ID)
	}
	return m, nil
}

// LanguageName names a language in English.
func LanguageName(code string) string {
	if n, ok := languageNames[code]; ok {
		return n
	}
	return code
}

var languageNames = map[string]string{
	"af": "Afrikaans", "ar": "Arabic", "az": "Azerbaijani", "be": "Belarusian", "bg": "Bulgarian", "bn": "Bengali", "bs": "Bosnian",
	"ca": "Catalan", "cs": "Czech", "cy": "Welsh", "da": "Danish", "de": "German", "el": "Greek", "es": "Spanish", "et": "Estonian",
	"eu": "Basque", "fa": "Persian", "fi": "Finnish", "fr": "French", "ga": "Irish", "gl": "Galician", "gu": "Gujarati", "he": "Hebrew",
	"hi": "Hindi", "hr": "Croatian", "hu": "Hungarian", "hy": "Armenian", "id": "Indonesian", "is": "Icelandic", "it": "Italian",
	"ja": "Japanese", "ka": "Georgian", "kk": "Kazakh", "kn": "Kannada", "ko": "Korean", "lt": "Lithuanian", "lv": "Latvian",
	"mk": "Macedonian", "ml": "Malayalam", "mr": "Marathi", "ms": "Malay", "mt": "Maltese", "nb": "Norwegian Bokmål", "nl": "Dutch",
	"nn": "Norwegian Nynorsk", "pl": "Polish", "pt": "Portuguese", "ro": "Romanian", "ru": "Russian", "sk": "Slovak", "sl": "Slovenian",
	"sq": "Albanian", "sr": "Serbian", "sv": "Swedish", "sw": "Swahili", "ta": "Tamil", "te": "Telugu", "th": "Thai", "tl": "Tagalog",
	"tr": "Turkish", "uk": "Ukrainian", "ur": "Urdu", "uz": "Uzbek", "vi": "Vietnamese", "zh-hans": "Chinese (Simplified)",
	"zh-hant": "Chinese (Traditional)", "zh": "Chinese",
}

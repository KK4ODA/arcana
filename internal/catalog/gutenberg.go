package catalog

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// GutenbergProvider imports Project Gutenberg's catalog (pg_catalog.csv)
// from an official mirror. Project Gutenberg blocks automated access to its
// main site and asks programs to use mirrors.
//
// Only books on practical bookshelves are imported by default (health,
// farming, engineering, cooking, crafts, sciences, scouting), about 6,500
// titles; the full catalog has nearly 80,000.
//
// Project Gutenberg publishes no checksums, so these downloads are installed
// as "unverified" (Arcana records the SHA-256 it computes).
type GutenbergProvider struct {
	// Mirror is the mirror base URL, e.g. https://gutenberg.pglaf.org
	Mirror string
	// All imports every text, not only practical bookshelves.
	All bool
	Now func() time.Time
}

// ID implements Provider.
func (g *GutenbergProvider) ID() string { return "gutenberg" }

// Name implements Provider.
func (g *GutenbergProvider) Name() string { return "Project Gutenberg" }

func (g *GutenbergProvider) mirror() string {
	if g.Mirror == "" {
		return "https://gutenberg.pglaf.org"
	}
	return strings.TrimRight(g.Mirror, "/")
}

// Fetch implements Provider.
func (g *GutenbergProvider) Fetch(ctx context.Context, f Fetcher) ([]Resource, error) {
	if !isHTTPURL(g.mirror()) {
		return nil, errors.New("gutenberg: invalid mirror URL")
	}
	data, err := f.Get(ctx, g.mirror()+"/cache/epub/feeds/pg_catalog.csv.gz", 64<<20)
	if err != nil {
		return nil, fmt.Errorf("gutenberg catalog: %w", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gutenberg catalog: %w", err)
	}
	return g.ParseCSV(io.LimitReader(zr, 256<<20))
}

// gutenbergShelf maps bookshelf names to Arcana categories.
var gutenbergShelves = []struct {
	match    string
	category string
}{
	{"health & medicine", CategoryMedicine},
	{"medicine", CategoryMedicine},
	{"the nursery", CategoryMedicine},
	{"agricultural/farming", CategoryAgriculture},
	{"cooking & drinking", CategoryAgriculture},
	{"cookbooks", CategoryAgriculture},
	{"cook book", CategoryAgriculture},
	{"bee journal", CategoryAgriculture},
	{"prairie farmer", CategoryAgriculture},
	{"nature/gardening", CategoryAgriculture},
	{"engineering & technology", CategoryScience},
	{"engineering", CategoryScience},
	{"technology", CategoryScience},
	{"crafts", CategoryPractical},
	{"scouts", CategoryPreparedness},
	{"animals-wild-trapping", CategoryPreparedness},
	{"science - physics", CategoryScience},
	{"science - chemistry", CategoryScience},
	{"science - biology", CategoryScience},
	{"mathematics", CategoryScience},
	{"physics", CategoryScience},
	{"chemistry", CategoryScience},
	{"botany", CategoryScience},
}

var pgDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// ParseCSV converts pg_catalog.csv into resources.
func (g *GutenbergProvider) ParseCSV(r io.Reader) ([]Resource, error) {
	cr := csv.NewReader(r)
	cr.LazyQuotes = true
	cr.FieldsPerRecord = -1
	header, err := cr.Read()
	if err != nil {
		return nil, fmt.Errorf("gutenberg catalog: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.TrimPrefix(h, "\uFEFF")] = i
	}
	for _, need := range []string{"Text#", "Type", "Issued", "Title", "Language", "Authors", "Subjects", "Bookshelves"} {
		if _, ok := col[need]; !ok {
			return nil, fmt.Errorf("gutenberg catalog: missing column %q", need)
		}
	}
	get := func(rec []string, name string) string {
		i := col[name]
		if i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}
	now := time.Now
	if g.Now != nil {
		now = g.Now
	}
	verified := now().UTC().Truncate(time.Second)
	var out []Resource
	for {
		rec, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			continue
		}
		if get(rec, "Type") != "Text" {
			continue
		}
		num := get(rec, "Text#")
		if num == "" || len(num) > 7 || strings.Trim(num, "0123456789") != "" {
			continue
		}
		shelves := strings.ToLower(get(rec, "Bookshelves"))
		category := ""
		for _, s := range gutenbergShelves {
			if strings.Contains(shelves, s.match) {
				category = s.category
				break
			}
		}
		if category == "" {
			if !g.All {
				continue
			}
			category = CategoryHistory
		}
		issued := get(rec, "Issued")
		if !pgDateRe.MatchString(issued) {
			issued = "1971-01-01"
		}
		var langs []string
		for _, l := range strings.Split(get(rec, "Language"), ";") {
			l = strings.ToLower(strings.TrimSpace(l))
			if langRe.MatchString(l) {
				langs = append(langs, l)
			}
		}
		authors := get(rec, "Authors")
		subjects := get(rec, "Subjects")
		desc := "Project Gutenberg eBook #" + num + "."
		if authors != "" {
			desc = "By " + authors + ". " + desc
		}
		if subjects != "" {
			desc += " Subjects: " + subjects + "."
		}
		r := Resource{
			ID:           "gutenberg.pg" + num,
			Provider:     "gutenberg",
			Title:        get(rec, "Title"),
			Description:  desc,
			Category:     category,
			Subcategory:  "historical books",
			Family:       "gutenberg.pg" + num,
			Variant:      "epub",
			VariantLabel: "EPUB with images",
			Publisher:    "Project Gutenberg",
			Creator:      authors,
			SourceURL:    "https://www.gutenberg.org/ebooks/" + num,
			Version:      issued,
			Published:    issued,
			LastVerified: &verified,
			Languages:    langs,
			Coverage:     Coverage{Scope: "none"},
			Format:       "epub",
			Files: []File{{
				Name:    "pg" + num + "-images-3.epub",
				Mirrors: []Mirror{{URL: fmt.Sprintf("%s/cache/epub/%s/pg%s-images-3.epub", g.mirror(), num, num)}},
			}},
			DownloadSize:   2_000_000,
			SizeEstimated:  true,
			Capabilities:   Capabilities{FullTextSearch: true, TitleSearch: true},
			Media:          Media{Pictures: true, Detail: "full"},
			UpdateStrategy: UpdateReplace,
			License: License{
				ID: "LicenseRef-PD-US", Name: "Public domain in the United States",
				URL: "https://www.gutenberg.org/policy/license.html", Redistribution: RedistPermitted, Verified: true,
				Notes: "Copyright status varies by country. Copies that keep the Project Gutenberg name are subject to its trademark license; check local law outside the US.",
			},
			Warnings: []Warning{{Kind: "outdated", Text: "Historical book: its information reflects the time it was written."}},
		}
		if category == CategoryMedicine {
			r.Warnings = append(r.Warnings, Warning{Kind: "medical",
				Text: "Historical medical text. Many treatments it describes are obsolete or dangerous. Do not use it as medical advice."})
		}
		if subjects != "" {
			for _, s := range strings.Split(subjects, ";") {
				if s = strings.TrimSpace(s); s != "" && len(r.Tags) < 8 {
					r.Tags = append(r.Tags, s)
				}
			}
		}
		out = append(out, r)
	}
	return out, nil
}

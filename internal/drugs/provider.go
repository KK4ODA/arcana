package drugs

import (
	"context"
	"fmt"

	"github.com/KK4ODA/arcana/internal/catalog"
)

// Provider offers the FDA drug label reference, versioned by openFDA's
// export date so updates appear in Updates like any other resource.
type Provider struct {
	// IndexURL is openFDA's download index (download.json).
	IndexURL string
}

// ID implements catalog.Provider.
func (p *Provider) ID() string { return "openfda" }

// Name implements catalog.Provider.
func (p *Provider) Name() string { return "openFDA (U.S. Food and Drug Administration)" }

// Fetch implements catalog.Provider.
func (p *Provider) Fetch(ctx context.Context, f catalog.Fetcher) ([]catalog.Resource, error) {
	u := p.IndexURL
	if u == "" {
		u = DefaultIndexURL
	}
	data, err := f.Get(ctx, u, 16<<20)
	if err != nil {
		return nil, err
	}
	ix, err := ParseIndex(data)
	if err != nil {
		return nil, err
	}
	return []catalog.Resource{ResourceFor(ix, u)}, nil
}

// ResourceFor describes the reference built from an index.
func ResourceFor(ix Index, indexURL string) catalog.Resource {
	total := ix.TotalBytes()
	return catalog.Resource{
		ID: ResourceID, Provider: "openfda",
		Title: "FDA drug labels: medicines reference",
		Description: fmt.Sprintf("The manufacturers' labels of about %d medicines sold in the United States, from the FDA's openFDA "+
			"downloads: the Drug Facts panel of non-prescription medicines and the main sections of prescription labels "+
			"(uses, dosage, warnings, interactions, overdose). Search by name or ingredient, look up medicines by "+
			"situation (fever, burns, diarrhea…), and compare two labels' warnings. Arcana builds the reference on your "+
			"computer from about %d MB of downloads.", ix.Total, total/1_000_000),
		Category: catalog.CategoryMedicine, Subcategory: "drug-reference", Tags: []string{"drugs", "medicines", "fda", "otc"},
		Family: ResourceID, Variant: "standard",
		Publisher: "U.S. Food and Drug Administration (openFDA)", SourceURL: "https://open.fda.gov/data/downloads/",
		Version: ix.Export, Published: ix.Export,
		Languages: []string{"en"}, Coverage: catalog.Coverage{Scope: "regional", Regions: []string{"US"}},
		Format:       Format,
		Files:        []catalog.File{{Name: "drug-labels.db", Mirrors: []catalog.Mirror{{URL: indexURL}}}},
		DownloadSize: total, InstalledSize: int64(float64(total) * dbPerZipByte), SizeEstimated: true,
		License: catalog.License{ID: "CC0-1.0", Name: "Creative Commons Zero 1.0 (public domain dedication)",
			URL: "https://open.fda.gov/license/", Redistribution: catalog.RedistPermitted, Verified: true,
			Notes: "openFDA content and data are public domain under CC0 unless noted; label text is the manufacturers'."},
		Capabilities: catalog.Capabilities{FullTextSearch: true, TitleSearch: true},
		Media:        catalog.Media{Detail: "full"},
		Warnings: []catalog.Warning{
			{Kind: "medical", Text: "Manufacturers' label text as submitted to the FDA, not medical advice. Labels can be out of date and are not reviewed by openFDA; the FDA says not to rely on openFDA for decisions about medical care. Follow the label of the product you actually have, and ask a pharmacist, doctor or nurse when you can."},
			{Kind: "regulatory", Text: "United States products and rules. Names, strengths and availability differ in other countries."},
		},
		UpdateStrategy: catalog.UpdateReplace,
		ProviderRef:    map[string]string{"method": MethodBuild, "index": indexURL},
	}
}

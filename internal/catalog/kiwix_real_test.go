package catalog

import (
	"os"
	"strings"
	"testing"
)

// TestPresetsAgainstRealKiwixCatalog checks that every Kiwix family and
// preferred variant named in the bundled presets exists in a downloaded
// Kiwix catalog, so a misspelt or withdrawn archive is caught before users
// see "Not found in the current catalog". Download the catalog with
//
//	curl -o kiwix.xml 'https://library.kiwix.org/catalog/v2/entries?count=-1'
//
// and point ARCANA_KIWIX_CATALOG at it.
func TestPresetsAgainstRealKiwixCatalog(t *testing.T) {
	p := os.Getenv("ARCANA_KIWIX_CATALOG")
	if p == "" {
		t.Skip("set ARCANA_KIWIX_CATALOG to a downloaded Kiwix OPDS catalog to run")
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	res, _, err := (&KiwixProvider{}).ParseOPDS(data, "https://library.kiwix.org")
	if err != nil {
		t.Fatal(err)
	}
	valid, _ := ValidateAll("kiwix", res)
	families := map[string][]Resource{}
	for _, r := range valid {
		families[r.Family] = append(families[r.Family], r)
	}
	t.Logf("%d Kiwix resources in %d families", len(valid), len(families))
	presets, err := BundledPresets()
	if err != nil {
		t.Fatal(err)
	}
	for _, pr := range presets {
		for _, it := range pr.Items {
			if !strings.HasPrefix(it.Family, "kiwix.") {
				continue
			}
			fam, _ := resolveFamily(it.Family, "en", families)
			got := families[fam]
			if len(got) == 0 {
				t.Errorf("preset %s: %s is not in the Kiwix catalog", pr.ID, fam)
				continue
			}
			// The recommender uses the listed variants that exist, so one
			// is enough; a missing preference is only worth a note.
			if len(it.Variants) > 0 && len(orderedCandidates(got, it.Variants)) == 0 {
				t.Errorf("preset %s: %s has none of the variants %v (has %v)", pr.ID, fam, it.Variants, variants(got))
			}
			for _, v := range it.Variants {
				if len(orderedCandidates(got, []string{v})) == 0 {
					t.Logf("preset %s: %s has no %q variant; %v remain", pr.ID, fam, v, variants(got))
				}
			}
		}
	}
}

func variants(rs []Resource) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.Variant)
	}
	return out
}

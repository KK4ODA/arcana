package catalog

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

type mapFetcher map[string][]byte

func (m mapFetcher) Get(_ context.Context, url string, max int64) ([]byte, error) {
	if b, ok := m[url]; ok {
		if int64(len(b)) > max {
			return nil, ErrTooLarge
		}
		return b, nil
	}
	return nil, fmt.Errorf("GET %s: 404", url)
}

func fixedNow() time.Time { return time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC) }

func loadOPDS(t *testing.T) []Resource {
	t.Helper()
	data, err := os.ReadFile("testdata/opds.xml")
	if err != nil {
		t.Fatal(err)
	}
	k := &KiwixProvider{Now: fixedNow}
	res, total, err := k.ParseOPDS(data, "https://library.kiwix.org")
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 {
		t.Fatalf("total = %d", total)
	}
	valid, errs := ValidateAll("kiwix", res)
	for _, e := range errs {
		t.Logf("rejected: %v", e)
	}
	return valid
}

func byID(rs []Resource) map[string]Resource {
	m := map[string]Resource{}
	for _, r := range rs {
		m[r.ID] = r
	}
	return m
}

func TestKiwixOPDSMapping(t *testing.T) {
	m := byID(loadOPDS(t))
	if len(m) != 4 {
		t.Fatalf("expected 4 valid resources (hostile entry rejected), got %d: %v", len(m), m)
	}
	rc, ok := m["kiwix.wikipedia_en_ray-charles_mini"]
	if !ok {
		t.Fatal("ray charles missing")
	}
	if rc.Version != "2026-10" || rc.Family != "kiwix.wikipedia_en_ray-charles" || rc.Variant != "mini" {
		t.Errorf("identity: %+v", rc)
	}
	if rc.Files[0].Name != "wikipedia_en_ray-charles_mini_2026-10.zim" {
		t.Errorf("file name %q", rc.Files[0].Name)
	}
	if rc.Languages[0] != "en" || !rc.Capabilities.FullTextSearch || rc.Media.Pictures || rc.Media.Detail != "summary" {
		t.Errorf("metadata: %+v", rc)
	}
	if rc.License.ID != "CC-BY-SA-4.0" || rc.License.Redistribution != RedistPermitted {
		t.Errorf("license: %+v", rc.License)
	}
	if rc.ProviderRef["uuid"] != "1069cd95-bab8-1494-b6ec-f402f990833b" {
		t.Errorf("uuid: %v", rc.ProviderRef)
	}

	med := m["kiwix.wikipedia_en_medicine_nopic"]
	if med.Category != CategoryMedicine {
		t.Errorf("medicine category = %s", med.Category)
	}
	var hasMedicalWarning bool
	for _, w := range med.Warnings {
		if w.Kind == "medical" && strings.Contains(w.Text, "2026-10-01") {
			hasMedicalWarning = true
		}
	}
	if !hasMedicalWarning {
		t.Errorf("medical resources must carry a dated warning: %+v", med.Warnings)
	}
	// Relative link resolved against the catalog base URL.
	if got := med.Files[0].MetalinkURL; got != "https://library.kiwix.org/zim/wikipedia/wikipedia_en_medicine_nopic_2026-10.zim.meta4" {
		t.Errorf("relative metalink resolved to %q", got)
	}

	bash := m["kiwix.devdocs_en_bash"]
	if bash.Category != CategoryComputing || bash.Variant != "standard" {
		t.Errorf("devdocs: category %s variant %s", bash.Category, bash.Variant)
	}
	if bash.License.Redistribution != RedistUnknown {
		t.Errorf("unreviewed license must be unknown, got %+v", bash.License)
	}
}

func TestResolveMetalink(t *testing.T) {
	m := byID(loadOPDS(t))
	rc := m["kiwix.wikipedia_en_ray-charles_mini"]
	meta, _ := os.ReadFile("testdata/ray-charles.meta4")
	f := mapFetcher{rc.Files[0].MetalinkURL: meta}
	got, err := (&KiwixProvider{}).Resolve(context.Background(), f, rc)
	if err != nil {
		t.Fatal(err)
	}
	file := got.Files[0]
	if file.Size != 26229009 || got.DownloadSize != 26229009 {
		t.Errorf("size %d / %d", file.Size, got.DownloadSize)
	}
	if file.Hashes["sha256"] != "ad37a9355a356b12cc4f2e339fcd15940dd93099de0c2851b6a25cc5b9c33c31" {
		t.Errorf("sha256 %q", file.Hashes["sha256"])
	}
	if len(file.Mirrors) != 2 || !strings.Contains(file.Mirrors[0].URL, "driftle.ss") {
		t.Errorf("mirrors should be http(s) only and sorted by priority: %+v", file.Mirrors)
	}
}

func TestValidateRejectsHostileInput(t *testing.T) {
	base := func() Resource {
		return Resource{ID: "feed.x", Provider: "feed", Title: "X", Version: "1", Format: "pdf",
			Files: []File{{Name: "x.pdf", Mirrors: []Mirror{{URL: "https://example.org/x.pdf"}}}}}
	}
	cases := map[string]func(*Resource){
		"traversal file name": func(r *Resource) { r.Files[0].Name = "../x.pdf" },
		"absolute file name":  func(r *Resource) { r.Files[0].Name = "/etc/passwd" },
		"windows device name": func(r *Resource) { r.Files[0].Name = "aux.pdf" },
		"file url":            func(r *Resource) { r.Files[0].Mirrors[0].URL = "file:///etc/passwd" },
		"javascript url":      func(r *Resource) { r.Files[0].Mirrors[0].URL = "javascript:alert(1)" },
		"credentials in url":  func(r *Resource) { r.Files[0].Mirrors[0].URL = "https://u:p@example.org/x" },
		"foreign namespace":   func(r *Resource) { r.ID = "kiwix.x" },
		"uppercase id":        func(r *Resource) { r.ID = "feed.X" },
		"bad version":         func(r *Resource) { r.Version = "../1" },
		"bad hash":            func(r *Resource) { r.Files[0].Hashes = map[string]string{"sha256": "zz"} },
		"unknown hash algo":   func(r *Resource) { r.Files[0].Hashes = map[string]string{"crc32": "00000000"} },
		"negative size":       func(r *Resource) { r.Files[0].Size = -1 },
		"huge size":           func(r *Resource) { r.Files[0].Size = MaxResourceSize + 1 },
		"no location":         func(r *Resource) { r.Files[0].Mirrors = nil },
		"unknown format":      func(r *Resource) { r.Format = "exe" },
		"duplicate files":     func(r *Resource) { r.Files = append(r.Files, File{Name: "X.PDF", Mirrors: r.Files[0].Mirrors}) },
		"bad language":        func(r *Resource) { r.Languages = []string{"english!"} },
		"bad dependency":      func(r *Resource) { r.Dependencies = []string{"../x"} },
	}
	for name, mutate := range cases {
		r := base()
		mutate(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	r := base()
	r.Title = "Line\nbreak\x00 and\ttabs"
	r.License = License{Redistribution: RedistPermitted} // not verified
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if r.Title != "Line break and tabs" {
		t.Errorf("title not cleaned: %q", r.Title)
	}
	if r.License.Redistribution != RedistUnknown {
		t.Error("unverified license must not claim redistribution rights")
	}
}

func TestBundledCatalogIsValid(t *testing.T) {
	res, err := Bundled().Fetch(context.Background(), mapFetcher{})
	if err != nil {
		t.Fatal(err)
	}
	valid, errs := ValidateAll("arcana", res)
	if len(errs) > 0 {
		t.Fatalf("bundled catalog has invalid entries: %v", errs)
	}
	for _, r := range valid {
		if r.Unverifiable() {
			t.Errorf("%s: bundled entries must carry a sha256", r.ID)
		}
		if !r.License.Verified {
			t.Errorf("%s: bundled entries must have a verified license", r.ID)
		}
	}
	presets, err := BundledPresets()
	if err != nil || len(presets) < 9 {
		t.Fatalf("presets: %v (%d)", err, len(presets))
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"2026-10", "2026-09", 1},
		{"2026-10", "2026-10", 0},
		{"2025-12", "2026-01", -1},
		{"2026-11a", "2026-11", 1},
		{"1.10.0", "1.9.3", 1},
		{"2026-10-05", "2026-10", 1},
		{"2025", "2026", -1},
		{"1.0.0-rc1", "1.0.0", -1},
		{"1.0.0", "1.0.0-rc1", 1},
	}
	for _, c := range cases {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("CompareVersions(%q,%q) = %d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestRecommendScalesWithCapacity(t *testing.T) {
	const GB = 1_000_000_000
	mk := func(id, family, variant string, size int64) Resource {
		return Resource{ID: id, Family: family, Variant: variant, VariantLabel: variant, Title: family, DownloadSize: size}
	}
	families := map[string][]Resource{
		"kiwix.wikipedia_en_all": {
			mk("kiwix.wikipedia_en_all_maxi", "kiwix.wikipedia_en_all", "maxi", 127*GB),
			mk("kiwix.wikipedia_en_all_nopic", "kiwix.wikipedia_en_all", "nopic", 53*GB),
			mk("kiwix.wikipedia_en_all_mini", "kiwix.wikipedia_en_all", "mini", 14*GB),
		},
		"kiwix.wikipedia_en_medicine": {
			mk("kiwix.wikipedia_en_medicine_maxi", "kiwix.wikipedia_en_medicine", "maxi", 2*GB),
			mk("kiwix.wikipedia_en_medicine_mini", "kiwix.wikipedia_en_medicine", "mini", GB/5),
		},
		"kiwix.wiktionary_en_all": {mk("kiwix.wiktionary_en_all_nopic", "kiwix.wiktionary_en_all", "nopic", 15*GB)},
	}
	preset := Preset{ID: "p", Items: []PresetItem{
		{Family: "kiwix.wikipedia_{lang}_all", Variants: []string{"maxi", "nopic", "mini"}, Priority: 1},
		{Family: "kiwix.wikipedia_{lang}_medicine", Variants: []string{"maxi", "mini"}, Priority: 1},
		{Family: "kiwix.wiktionary_{lang}_all", Priority: 3},
		{Family: "kiwix.missing_{lang}", Priority: 2},
	}}
	pick := func(budget int64, lang string) map[string]RecItem {
		rec := Recommend(RecommendInput{Preset: preset, Language: lang, Budget: budget, Families: families, Present: map[string]bool{}})
		if rec.TotalBytes > budget {
			t.Fatalf("budget %d exceeded: %d", budget, rec.TotalBytes)
		}
		m := map[string]RecItem{}
		for _, it := range rec.Items {
			m[it.Family] = it
		}
		return m
	}

	small := pick(20*GB, "en")
	if small["kiwix.wikipedia_en_all"].Variant != "mini" || small["kiwix.wikipedia_en_medicine"].Variant != "maxi" {
		t.Errorf("20 GB: %+v", small)
	}
	if small["kiwix.wiktionary_en_all"].Status != "skipped" || !strings.Contains(small["kiwix.wiktionary_en_all"].Decision, "Skipped") {
		t.Errorf("20 GB should skip the optional dictionary: %+v", small["kiwix.wiktionary_en_all"])
	}
	if !strings.Contains(small["kiwix.wikipedia_en_all"].Decision, "do not fit") {
		t.Errorf("decision should explain the downgrade: %q", small["kiwix.wikipedia_en_all"].Decision)
	}
	if small["kiwix.missing_en"].Status != "unavailable" {
		t.Error("missing family should be reported as unavailable")
	}

	mid := pick(115*GB, "en")
	if mid["kiwix.wikipedia_en_all"].Variant != "nopic" || mid["kiwix.wiktionary_en_all"].Status != "selected" {
		t.Errorf("115 GB: %+v", mid)
	}

	large := pick(2000*GB, "en")
	if large["kiwix.wikipedia_en_all"].Variant != "maxi" {
		t.Errorf("2 TB should choose maxi: %+v", large["kiwix.wikipedia_en_all"])
	}

	// Language fallback is explained.
	es := pick(2000*GB, "es")
	if es["kiwix.wikipedia_en_all"].Status != "selected" || !strings.Contains(es["kiwix.wikipedia_en_all"].Decision, "English edition") {
		t.Errorf("fallback: %+v", es)
	}

	// Already-installed variants consume no budget.
	rec := Recommend(RecommendInput{Preset: preset, Language: "en", Budget: 20 * GB, Families: families,
		Present: map[string]bool{"kiwix.wikipedia_en_all_nopic": true}})
	for _, it := range rec.Items {
		if it.Family == "kiwix.wikipedia_en_all" && it.Status != "present" {
			t.Errorf("installed family should be present: %+v", it)
		}
	}
}

func TestGutenbergCSV(t *testing.T) {
	csvData := "Text#,Type,Issued,Title,Language,Authors,Subjects,LoCC,Bookshelves\n" +
		"10001,Text,2003-11-01,\"The Bee-Keeper's\nManual\",en,\"Smith, J.\",Bee culture,SF,Category: Science - Earth/Agricultural/Farming\n" +
		"10002,Text,2004-01-01,Household Medicine,en,,Medicine -- Popular works,R,Category: Health & Medicine\n" +
		"10003,Text,2004-01-01,A Novel,en,,Fiction,PR,Category: Novels\n" +
		"10004,Sound,2004-01-01,Audio,en,,,,Category: Health & Medicine\n" +
		"../x,Text,2004-01-01,Bad,en,,,,Category: Health & Medicine\n"
	g := &GutenbergProvider{Now: fixedNow}
	res, err := g.ParseCSV(strings.NewReader(csvData))
	if err != nil {
		t.Fatal(err)
	}
	valid, errs := ValidateAll("gutenberg", res)
	if len(errs) > 0 || len(valid) != 2 {
		t.Fatalf("valid %d errs %v", len(valid), errs)
	}
	m := byID(valid)
	bee := m["gutenberg.pg10001"]
	if bee.Category != CategoryAgriculture || bee.Title != "The Bee-Keeper's Manual" || !bee.SizeEstimated || !bee.Unverifiable() {
		t.Errorf("bee: %+v", bee)
	}
	med := m["gutenberg.pg10002"]
	found := false
	for _, w := range med.Warnings {
		if w.Kind == "medical" && strings.Contains(w.Text, "obsolete") {
			found = true
		}
	}
	if !found {
		t.Errorf("historical medical books need a strong warning: %+v", med.Warnings)
	}
	all, _ := (&GutenbergProvider{All: true}).ParseCSV(strings.NewReader(csvData))
	if len(all) != 3 {
		t.Errorf("All should include non-practical texts: %d", len(all))
	}
}

package maps

import (
	"strings"
	"testing"

	"github.com/KK4ODA/arcana/internal/catalog"
)

func TestRegionCatalog(t *testing.T) {
	rc, err := Regions()
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.Regions) < 400 || len(rc.Build) != 8 {
		t.Fatalf("%d regions, build %q", len(rc.Regions), rc.Build)
	}
	ids := map[string]bool{}
	for _, r := range rc.Regions {
		if ids[r.ID] {
			t.Errorf("duplicate region %s", r.ID)
		}
		ids[r.ID] = true
		if !r.BBox.Valid() && r.BBox[0] <= r.BBox[2] {
			t.Errorf("%s: invalid bbox %v", r.ID, r.BBox)
		}
		if len(r.Levels) == 0 {
			t.Errorf("%s: no levels", r.ID)
		}
	}
	for _, r := range rc.Regions {
		if r.Parent != "" && !ids[r.Parent] {
			t.Errorf("%s: unknown parent %s", r.ID, r.Parent)
		}
	}
	p := &Provider{}
	res, err := p.Offline()
	if err != nil {
		t.Fatal(err)
	}
	valid, errs := catalog.ValidateAll("maps", res)
	if len(errs) > 0 {
		t.Fatalf("%d invalid resources, first: %v", len(errs), errs[0])
	}
	var lu catalog.Resource
	for _, r := range valid {
		if r.ID == "maps.country-lu-z15" {
			lu = r
		}
	}
	if lu.ID == "" || lu.ProviderRef["method"] != MethodExtract || lu.DownloadSize < 50e6 || !lu.SizeEstimated ||
		!strings.HasSuffix(lu.Files[0].Mirrors[0].URL, rc.Build+".pmtiles") || lu.License.ID != "ODbL-1.0" {
		t.Fatalf("Luxembourg z15 = %+v", lu)
	}
	if _, err := ParseBBox(lu.ProviderRef["bbox"]); err != nil {
		t.Fatal(err)
	}
	// Custom areas get a stable, valid ID.
	ca, err := p.CustomArea(rc.Build, "Esch / Alzette!", BBox{5.9, 49.45, 6.05, 49.55}, 15, 1234)
	if err != nil {
		t.Fatal(err)
	}
	if err := ca.Validate(); err != nil || !strings.HasPrefix(ca.ID, "maps.custom-esch-alzette-") {
		t.Fatalf("custom area %s: %v", ca.ID, err)
	}
}

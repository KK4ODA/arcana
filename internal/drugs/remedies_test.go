package drugs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedRemedies(t *testing.T) {
	set, err := Remedies()
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, r := range set.Remedies {
		kinds[r.Kind]++
	}
	if kinds[RemedyHome] != 20 || kinds[RemedyHerbal] != 18 {
		t.Fatalf("kinds = %v, want 20 home and 18 herbal", kinds)
	}
	if set.License != "Apache-2.0" || !strings.Contains(set.CompiledBy, "Project NOMAD") || set.Version == "" {
		t.Fatalf("provenance missing: %+v", set)
	}
}

func TestRemediesFor(t *testing.T) {
	got := RemediesFor([]string{"dehydration"})
	if len(got) != 1 || got[0].Slug != "oral-rehydration" {
		t.Fatalf("dehydration: %+v", got)
	}
	// Home care comes before herbs.
	cold := RemediesFor([]string{"cold"})
	sawHerbal := false
	for _, r := range cold {
		if r.Kind == RemedyHerbal {
			sawHerbal = true
		} else if sawHerbal {
			t.Fatalf("home remedy %s after an herb", r.Slug)
		}
	}
	if !sawHerbal || len(cold) < 5 {
		t.Fatalf("cold: %d remedies", len(cold))
	}
	if all := RemediesFor(nil); len(all) != 38 {
		t.Fatalf("all: %d", len(all))
	}
	if _, ok := RemedyBySlug("honey-for-cough"); !ok {
		t.Fatal("honey-for-cough not found")
	}
	if _, ok := RemedyBySlug("nope"); ok {
		t.Fatal("unknown slug found")
	}
}

// Every situation should be reachable from the remedies or the labels; the
// five added for NOMAD parity each have at least one remedy or a label term.
func TestRemediesCoverNewSituations(t *testing.T) {
	for _, s := range []string{"cold", "dry-skin", "ears", "dehydration", "pinworm"} {
		if len(RemediesFor([]string{s})) == 0 {
			t.Errorf("no remedy for %s", s)
		}
	}
}

func TestParseRemediesRejects(t *testing.T) {
	good := `{"slug":"x","kind":"home","name":"X","situations":["cough"],"uses":"u","evidence":"e","cautions":"c","sourceUrl":"https://medlineplus.gov/x.html"}`
	cases := map[string]string{
		"foreign host":      strings.Replace(good, "medlineplus.gov", "example.com", 1),
		"plain http":        strings.Replace(good, "https://", "http://", 1),
		"unknown situation": strings.Replace(good, `"cough"`, `"hiccups"`, 1),
		"unknown kind":      strings.Replace(good, `"home"`, `"folk"`, 1),
		"no cautions":       strings.Replace(good, `"cautions":"c"`, `"cautions":""`, 1),
		"repeated slug":     good + "," + good,
	}
	if _, err := parseRemedies([]byte(`{"remedies":[` + good + `]}`)); err != nil {
		t.Fatalf("good entry rejected: %v", err)
	}
	for name, entry := range cases {
		if _, err := parseRemedies([]byte(`{"remedies":[` + entry + `]}`)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The situations added in 0.14.0 must find the products whose Drug Facts
// use this wording (taken from current openFDA labels), and must leave out
// homeopathic products.
func TestNewSituationsMatchLabelWording(t *testing.T) {
	otc := func(id, brand, generic, purpose, uses string) map[string]any {
		return rec(map[string]any{"set_id": id,
			"openfda":               map[string]any{"brand_name": []string{brand}, "generic_name": []string{generic}, "product_type": []string{"HUMAN OTC DRUG"}, "substance_name": []string{generic}},
			"purpose":               []string{purpose},
			"indications_and_usage": []string{uses}})
	}
	records := []map[string]any{
		otc("E1", "Debrox", "CARBAMIDE PEROXIDE", "Purpose Earwax removal aid", "Uses for occasional use as an aid to soften, loosen, and remove excessive earwax"),
		otc("E2", "Swim-Ear", "ISOPROPYL ALCOHOL", "Purpose Ear drying aid", "Uses dries water in the ears and relieves water-clogged ears after swimming, showering, bathing or washing the hair"),
		otc("P1", "Pin-X", "PYRANTEL PAMOATE", "Purpose Pinworm treatment", "Use for the treatment of pinworms"),
		otc("S1", "Vaseline", "WHITE PETROLATUM", "Purpose Skin protectant", "Uses temporarily protects and helps relieve chapped or cracked skin and lips"),
		otc("H1", "Salt Tablets", "SODIUM CHLORIDE", "Purpose Electrolyte Replenisher", "Uses for the preparation of normal isotonic solution of sodium chloride as an electrolyte replenisher for the prevention of heat cramps due to excessive perspiration"),
		otc("C1", "Tylenol", "ACETAMINOPHEN", "Purposes Pain reliever/fever reducer", "Uses temporarily relieves minor aches and pains due to the common cold, headache, toothache"),
		rec(map[string]any{"set_id": "X1",
			"spl_product_data_elements": []string{"Earache Relief Belladonna Chamomilla"},
			"purpose":                   []string{"Earache"},
			"indications_and_usage":     []string{"Uses temporarily relieves earache and ear pain"},
			"active_ingredient":         []string{"Belladonna 30C HPUS"}}),
	}
	dir := t.TempDir()
	z := filepath.Join(dir, "p.zip")
	writeZip(t, z, records)
	out := filepath.Join(dir, "d.db")
	if _, err := Build(context.Background(), []string{z}, out, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	r, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	want := map[string][]string{
		"ears":        {"carbamide peroxide", "isopropyl alcohol"},
		"pinworm":     {"pyrantel pamoate"},
		"dry-skin":    {"white petrolatum"},
		"dehydration": {"sodium chloride"},
		"cold":        {"acetaminophen"},
	}
	for slug, keys := range want {
		res, err := r.BySituation(context.Background(), []string{slug}, SituationOptions{})
		if err != nil {
			t.Fatalf("%s: %v", slug, err)
		}
		var got []string
		for _, g := range res[0].Groups {
			got = append(got, g.Key)
		}
		for _, k := range keys {
			if !strings.Contains(strings.Join(got, ","), k) {
				t.Errorf("%s: got %v, want %s", slug, got, k)
			}
		}
		if len(got) != len(keys) {
			t.Errorf("%s: got %v, want only %v (homeopathic must be left out)", slug, got, keys)
		}
	}
}

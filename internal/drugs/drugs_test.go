package drugs

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KK4ODA/arcana/internal/catalog"
)

var fixedTime = time.Unix(1_700_000_000, 0)

func rec(fields map[string]any) map[string]any {
	if _, ok := fields["set_id"]; !ok {
		fields["set_id"] = fmt.Sprintf("set-%p", &fields)
	}
	fields["effective_time"] = "20250115"
	fields["version"] = "3"
	return fields
}

func sampleRecords() []map[string]any {
	return []map[string]any{
		rec(map[string]any{"set_id": "A1",
			"openfda":               map[string]any{"brand_name": []string{"Pepto-Bismol"}, "generic_name": []string{"BISMUTH SUBSALICYLATE"}, "manufacturer_name": []string{"Procter"}, "product_type": []string{"HUMAN OTC DRUG"}, "substance_name": []string{"BISMUTH SUBSALICYLATE"}, "route": []string{"ORAL"}},
			"purpose":               []string{"Purpose Upset stomach reliever and antidiarrheal"},
			"indications_and_usage": []string{"Uses relieves travelers' diarrhea, diarrhea, upset stomach due to overindulgence"},
			"warnings":              []string{"Warnings Reye's syndrome: Children and teenagers who have or are recovering from chicken pox should not use this product."},
			"active_ingredient":     []string{"Active ingredient (in each 30 mL) Bismuth subsalicylate 525 mg"},
		}),
		// A store brand without openFDA's harmonized fields.
		rec(map[string]any{"set_id": "A2",
			"spl_product_data_elements":     []string{"TopCare Stomach Relief Bismuth subsalicylate CARBOXYMETHYLCELLULOSE SODIUM D&C RED NO. 22"},
			"purpose":                       []string{"Purposes Upset stomach reliever and antidiarrheal"},
			"indications_and_usage":         []string{"Uses relieves diarrhea and upset stomach"},
			"active_ingredient":             []string{"Active ingredient (in each 15 mL) Bismuth subsalicylate USP 262 mg"},
			"keep_out_of_reach_of_children": []string{"Keep out of reach of children. In case of overdose, get medical help."},
		}),
		rec(map[string]any{"set_id": "B1",
			"openfda":               map[string]any{"brand_name": []string{"IMODIUM A-D"}, "generic_name": []string{"LOPERAMIDE HYDROCHLORIDE"}, "product_type": []string{"HUMAN OTC DRUG"}, "substance_name": []string{"LOPERAMIDE HYDROCHLORIDE"}},
			"purpose":               []string{"Anti-diarrheal"},
			"indications_and_usage": []string{"Use controls symptoms of diarrhea, including Travelers' Diarrhea"},
			"warnings":              []string{"Heart alert: Taking more than directed can cause serious heart problems."},
		}),
		rec(map[string]any{"set_id": "B2",
			"spl_product_data_elements": []string{"Anti-Diarrheal loperamide HCl LOPERAMIDE HYDROCHLORIDE"},
			"purpose":                   []string{"Anti-diarrheal"},
			"indications_and_usage":     []string{"Use controls symptoms of diarrhea"},
			"active_ingredient":         []string{"Active ingredient (in each caplet) Loperamide HCl 2 mg"},
		}),
		rec(map[string]any{"set_id": "C1",
			"openfda":               map[string]any{"brand_name": []string{"Silvadene"}, "generic_name": []string{"SILVER SULFADIAZINE"}, "product_type": []string{"HUMAN PRESCRIPTION DRUG"}, "substance_name": []string{"SILVER SULFADIAZINE"}},
			"indications_and_usage": []string{"INDICATIONS AND USAGE Silver sulfadiazine is an adjunct for the prevention and treatment of wound sepsis in patients with second- and third-degree burns."},
			"contraindications":     []string{"CONTRAINDICATIONS Contraindicated in pregnant women at or near term."},
			"clinical_pharmacology": []string{strings.Repeat("Not kept. ", 500)},
		}),
		rec(map[string]any{"set_id": "D1",
			"spl_product_data_elements": []string{"Diarrhea Relief Arsenicum album, Podophyllum ARSENIC TRIOXIDE"},
			"purpose":                   []string{"Diarrhea"},
			"indications_and_usage":     []string{"Uses temporarily relieves diarrhea"},
			"active_ingredient":         []string{"Arsenicum album 30C HPUS"},
		}),
		// Nothing usable: skipped.
		rec(map[string]any{"set_id": "E1"}),
	}
}

func writeZip(t *testing.T, path string, records []map[string]any) {
	t.Helper()
	doc, _ := json.Marshal(map[string]any{"meta": map[string]any{"disclaimer": "x"}, "results": records})
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("drug-label-0001-of-0001.json")
	w.Write(doc)
	zw.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestParseRecord(t *testing.T) {
	recs := sampleRecords()
	parse := func(i int) Label {
		b, _ := json.Marshal(recs[i])
		l, ok := ParseRecord(b)
		if !ok && i != 6 {
			t.Fatalf("record %d skipped", i)
		}
		return l
	}
	l := parse(0)
	if l.Name != "Pepto-Bismol" || l.Kind != KindOTC || l.GroupKey() != "bismuth subsalicylate" || l.Sections["purpose"] != "Upset stomach reliever and antidiarrheal" {
		t.Fatalf("openFDA record: %+v", l)
	}
	l = parse(1)
	if l.Name != "TopCare Stomach Relief Bismuth subsalicylate" || l.Kind != KindOTC || l.GroupKey() != "bismuth subsalicylate" {
		t.Fatalf("store brand: name=%q kind=%s key=%q", l.Name, l.Kind, l.GroupKey())
	}
	if parse(3).GroupKey() != "loperamide hydrochloride" || parse(2).GroupKey() != "loperamide hydrochloride" {
		t.Fatalf("HCl not normalized: %q %q", parse(3).GroupKey(), parse(2).GroupKey())
	}
	l = parse(4)
	if l.Kind != KindRx || l.Sections["clinical_pharmacology"] != "" || !strings.HasPrefix(l.Sections["contraindications"], "Contraindicated") {
		t.Fatalf("rx: %+v", l)
	}
	if parse(5).Kind != KindHomeopathic {
		t.Fatalf("homeopathic not detected")
	}
	if b, _ := json.Marshal(recs[6]); true {
		if _, ok := ParseRecord(b); ok {
			t.Fatal("empty record accepted")
		}
	}
}

func TestIngredientsFromSection(t *testing.T) {
	for in, want := range map[string]string{
		"Active ingredient (in each tablet) Bismuth subsalicylate 262 mg":                    "bismuth subsalicylate",
		"Active ingredients (in each caplet) Acetaminophen 500 mg Diphenhydramine HCl 25 mg": "acetaminophen,diphenhydramine hydrochloride",
		"Drug Facts Active ingredient Ethyl alcohol 70% v/v":                                 "ethyl alcohol",
		"Active ingredient(s) Calcium carbonate USP 750 mg":                                  "calcium carbonate",
	} {
		if got := strings.Join(ingredientsFromSection(in), ","); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestNameFromElements(t *testing.T) {
	for in, want := range map[string]string{
		"TopCare Regular Strength Bismuth subsalicylate CARBOXYMETHYLCELLULOSE SODIUM": "TopCare Regular Strength Bismuth subsalicylate",
		"MAGNESIA PHOS magnesium phosphate, dibasic trihydrate ACACIA LACTOSE":         "MAGNESIA PHOS magnesium phosphate, dibasic trihydrate",
		"Tofacitinib Tofacitinib TOFACITINIB CITRATE POVIDONE":                         "Tofacitinib",
	} {
		if got := nameFromElements(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestBuildAndQuery(t *testing.T) {
	dir := t.TempDir()
	z := filepath.Join(dir, "p1.zip")
	writeZip(t, z, sampleRecords())
	out := filepath.Join(dir, "drug-labels.db")
	st, err := Build(context.Background(), []string{z}, out, map[string]string{"export": "2026-11-20"})
	if err != nil {
		t.Fatal(err)
	}
	if st.Labels != 6 || st.Skipped != 1 {
		t.Fatalf("stats %+v", st)
	}
	r, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx := context.Background()
	gs, total, err := r.Search(ctx, "bismuth", SearchOptions{})
	if err != nil || total != 1 || gs[0].Products != 2 || gs[0].Title != "Bismuth Subsalicylate" {
		t.Fatalf("search bismuth: %+v total=%d err=%v", gs, total, err)
	}
	gs, _, _ = r.Search(ctx, "pepto", SearchOptions{})
	if len(gs) != 1 || gs[0].Key != "bismuth subsalicylate" {
		t.Fatalf("brand prefix search: %+v", gs)
	}
	gs, _, _ = r.Search(ctx, "silver", SearchOptions{Kind: KindOTC})
	if len(gs) != 0 {
		t.Fatalf("OTC filter: %+v", gs)
	}
	ps, n, err := r.GroupProducts(ctx, "loperamide hydrochloride", SearchOptions{})
	if err != nil || n != 2 || len(ps) != 2 {
		t.Fatalf("group products: %+v %d %v", ps, n, err)
	}
	p, secs, err := r.Label(ctx, ps[0].ID)
	if err != nil || len(secs) == 0 || p.Name == "" {
		t.Fatalf("label: %+v %+v %v", p, secs, err)
	}
	if secs[0].Key != "purpose" && secs[0].Key != "active_ingredient" {
		t.Fatalf("sections out of order: %+v", secs)
	}
	if _, _, err := r.Label(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing label: %v", err)
	}
	res, err := r.BySituation(ctx, []string{"diarrhea", "upset-stomach"}, SituationOptions{})
	if err != nil || len(res) != 3 {
		t.Fatalf("situations: %+v %v", res, err)
	}
	if len(res[0].Groups) != 1 || res[0].Groups[0].Key != "bismuth subsalicylate" {
		t.Fatalf("both situations: %+v", res[0])
	}
	keys := []string{}
	for _, g := range res[1].Groups {
		keys = append(keys, g.Key)
	}
	if strings.Join(keys, ",") != "bismuth subsalicylate,loperamide hydrochloride" && strings.Join(keys, ",") != "loperamide hydrochloride,bismuth subsalicylate" {
		t.Fatalf("diarrhea alone (homeopathic must be excluded): %v", keys)
	}
	res, _ = r.BySituation(ctx, []string{"burn"}, SituationOptions{Kind: "all"})
	if len(res[0].Groups) != 1 || res[0].Groups[0].Example.Kind != KindRx {
		t.Fatalf("burns incl. prescription: %+v", res)
	}
	st2, _ := r.Stats(ctx)
	if st2.Labels != 6 || st2.ByKind[KindHomeopathic] != 1 || st2.Meta["export"] != "2026-11-20" {
		t.Fatalf("stats: %+v", st2)
	}
}

func TestParseIndexRejectsForeignHosts(t *testing.T) {
	good := `{"results":{"drug":{"label":{"export_date":"2026-11-20","total_records":2,"partitions":[{"file":"https://download.open.fda.gov/drug/label/x.zip","size_mb":"1.5","records":2}]}}}}`
	if ix, err := ParseIndex([]byte(good)); err != nil || ix.TotalBytes() != 1_500_000 {
		t.Fatalf("good index: %+v %v", ix, err)
	}
	for _, bad := range []string{
		strings.Replace(good, "download.open.fda.gov", "evil.example", 1),
		strings.Replace(good, "https://", "http://", 1),
		strings.Replace(good, "2026-11-20", "soon", 1),
	} {
		if _, err := ParseIndex([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestInstallFromServer(t *testing.T) {
	dir := t.TempDir()
	z := filepath.Join(dir, "src.zip")
	writeZip(t, z, sampleRecords())
	zdata, _ := os.ReadFile(z)
	var hits atomic.Int32
	corrupt := atomic.Bool{}
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download.json":
			fmt.Fprintf(w, `{"results":{"drug":{"label":{"export_date":"2026-11-20","total_records":7,"partitions":[{"file":"%s/p1.zip","size_mb":"0.01","records":7}]}}}}`, srv.URL)
		case "/p1.zip":
			hits.Add(1)
			data := zdata
			if corrupt.Load() {
				data = append([]byte(nil), zdata...)
				data[len(data)/2] ^= 0xFF
			}
			http.ServeContent(w, r, "p1.zip", fixedTime, bytes.NewReader(data))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	// The test server is not on fda.gov; ParseIndex is checked above.
	ix := Index{Export: "2026-11-20", Total: 7, Partitions: []Partition{{File: srv.URL + "/p1.zip", SizeMB: "0.01"}}}
	res := ResourceFor(ix, srv.URL+"/download.json")
	if err := res.Validate(); err != nil {
		t.Fatalf("resource invalid: %v", err)
	}
	allowTestHost = srv.Listener.Addr().String()
	defer func() { allowTestHost = "" }()
	out := filepath.Join(dir, "lib", "drug-labels.db")
	os.MkdirAll(filepath.Dir(out), 0o755)
	req := InstallRequest{Resource: res, Out: out, Client: srv.Client()}

	corrupt.Store(true)
	_, _, err := Install(context.Background(), req)
	var pe *PartError
	if !errors.As(err, &pe) {
		t.Fatalf("corrupt part: %v", err)
	}
	corrupt.Store(false)
	size, sum, err := Install(context.Background(), req)
	if err != nil || size == 0 || len(sum) != 64 {
		t.Fatalf("install: %d %s %v", size, sum, err)
	}
	if hits.Load() != 2 {
		t.Fatalf("expected the damaged part to be fetched again (hits=%d)", hits.Load())
	}
	if m, _ := filepath.Glob(out + ".part*"); len(m) != 0 {
		t.Fatalf("parts left behind: %v", m)
	}
	r, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	r.Close()
	// A newer export restarts the job.
	res.Version = "2026-10-01"
	if _, _, err := Install(context.Background(), InstallRequest{Resource: res, Out: out + "2", Client: srv.Client()}); !errors.Is(err, ErrSourceChanged) {
		t.Fatalf("version change: %v", err)
	}
	_ = catalog.UpdateReplace
}

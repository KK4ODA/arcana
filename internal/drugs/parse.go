package drugs

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Kinds of product.
const (
	KindOTC         = "otc"
	KindRx          = "rx"
	KindHomeopathic = "homeopathic"
	KindOther       = "other"
)

// Section is one part of a label, in display order.
type Section struct {
	Key   string
	Title string
}

// Sections lists the parts of a label Arcana keeps: what a reader at home
// needs (uses, directions, warnings, interactions, overdose). Clinical
// pharmacology, studies, tables and package text are left out, which keeps
// the reference to a fraction of the source's size.
var Sections = []Section{
	{"boxed_warning", "Boxed warning"},
	{"active_ingredient", "Active ingredients"},
	{"purpose", "Purpose"},
	{"indications_and_usage", "Uses"},
	{"dosage_and_administration", "Directions and dosage"},
	{"dosage_forms_and_strengths", "Forms and strengths"},
	{"contraindications", "Do not use (contraindications)"},
	{"do_not_use", "Do not use"},
	{"ask_doctor", "Ask a doctor before use if you have"},
	{"ask_doctor_or_pharmacist", "Ask a doctor or pharmacist before use if you are"},
	{"when_using", "When using this product"},
	{"stop_use", "Stop use and ask a doctor if"},
	{"warnings", "Warnings"},
	{"warnings_and_cautions", "Warnings and precautions"},
	{"precautions", "Precautions"},
	{"drug_interactions", "Drug interactions"},
	{"pregnancy_or_breast_feeding", "Pregnancy or breast-feeding"},
	{"pregnancy", "Pregnancy"},
	{"use_in_specific_populations", "Use in specific populations"},
	{"pediatric_use", "Children"},
	{"geriatric_use", "Older adults"},
	{"keep_out_of_reach_of_children", "Keep out of reach of children"},
	{"overdosage", "Overdose"},
	{"adverse_reactions", "Side effects (adverse reactions)"},
	{"other_safety_information", "Other information"},
	{"inactive_ingredient", "Inactive ingredients"},
}

var sectionKeys = func() map[string]bool {
	m := map[string]bool{}
	for _, s := range Sections {
		m[s.Key] = true
	}
	return m
}()

// maxSection bounds one stored section (a few Rx labels have enormous
// adverse-reaction lists).
const maxSection = 24_000

// Label is a record reduced to what the reference stores.
type Label struct {
	SetID        string
	Version      string
	Effective    string // YYYY-MM-DD
	Name         string
	Generic      string
	Manufacturer string
	Kind         string
	Route        string
	// Ingredients are the active substances, lower case, sorted.
	Ingredients []string
	// Sections maps section keys to text.
	Sections map[string]string
}

// GroupKey groups products with the same active ingredients; a product
// whose ingredients are unknown forms its own group by name.
func (l Label) GroupKey() string {
	if len(l.Ingredients) > 0 {
		return strings.Join(l.Ingredients, " + ")
	}
	if g := strings.ToLower(strings.TrimSpace(l.Generic)); g != "" {
		return g
	}
	return "name:" + strings.ToLower(l.Name)
}

// Uses is the text searched for situations (purpose and uses).
func (l Label) Uses() string {
	return strings.TrimSpace(l.Sections["purpose"] + "\n" + l.Sections["indications_and_usage"])
}

type rawRecord struct {
	SetID     string           `json:"set_id"`
	ID        string           `json:"id"`
	Version   string           `json:"version"`
	Effective string           `json:"effective_time"`
	OpenFDA   map[string][]any `json:"openfda"`
}

// ParseRecord converts one openFDA label record. It returns false for
// records that carry nothing a reader could use.
func ParseRecord(data []byte) (Label, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Label{}, false
	}
	var r rawRecord
	_ = json.Unmarshal(data, &r)
	text := func(k string) string {
		raw, ok := fields[k]
		if !ok {
			return ""
		}
		var parts []string
		if err := json.Unmarshal(raw, &parts); err != nil {
			var one string
			if json.Unmarshal(raw, &one) == nil {
				parts = []string{one}
			}
		}
		return clean(strings.Join(parts, "\n"))
	}
	of := func(k string) []string {
		var out []string
		for _, v := range r.OpenFDA[k] {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	}
	l := Label{SetID: strings.ToLower(strings.TrimSpace(r.SetID)), Version: r.Version, Effective: date(r.Effective),
		Sections: map[string]string{}}
	if l.SetID == "" {
		return Label{}, false
	}
	for k := range sectionKeys {
		if t := text(k); t != "" {
			if len(t) > maxSection {
				t = cut(t, maxSection) + " …"
			}
			l.Sections[k] = stripHeading(k, t)
		}
	}
	elements := text("spl_product_data_elements")
	all := strings.ToLower(elements + " " + text("package_label_principal_display_panel") + " " + l.Sections["indications_and_usage"] +
		" " + l.Sections["purpose"] + " " + l.Sections["active_ingredient"] + " " + text("description"))

	if names := of("brand_name"); len(names) > 0 {
		l.Name = titleCase(names[0])
	}
	if g := of("generic_name"); len(g) > 0 {
		l.Generic = titleCase(g[0])
	}
	if m := of("manufacturer_name"); len(m) > 0 {
		l.Manufacturer = m[0]
	}
	if rt := of("route"); len(rt) > 0 {
		l.Route = strings.ToLower(strings.Join(rt, ", "))
	}
	for _, s := range of("substance_name") {
		l.Ingredients = append(l.Ingredients, normIngredient(s))
	}
	if l.Name == "" {
		l.Name = nameFromElements(elements)
	}
	if len(l.Ingredients) == 0 {
		l.Ingredients = ingredientsFromSection(l.Sections["active_ingredient"])
	}
	if l.Generic == "" && len(l.Ingredients) > 0 {
		l.Generic = titleCase(strings.Join(l.Ingredients, ", "))
	}
	l.Ingredients = uniqSorted(l.Ingredients)

	pt := strings.ToUpper(strings.Join(of("product_type"), " "))
	switch {
	case strings.Contains(all, "homeopathic") || strings.Contains(all, "hpus") || potencyRe.MatchString(l.Sections["active_ingredient"]):
		l.Kind = KindHomeopathic
	case strings.Contains(pt, "OTC"):
		l.Kind = KindOTC
	case strings.Contains(pt, "PRESCRIPTION"):
		l.Kind = KindRx
	case l.Sections["purpose"] != "" || l.Sections["keep_out_of_reach_of_children"] != "" || l.Sections["do_not_use"] != "":
		l.Kind = KindOTC
	case l.Sections["contraindications"] != "" || l.Sections["boxed_warning"] != "" || l.Sections["adverse_reactions"] != "":
		l.Kind = KindRx
	default:
		l.Kind = KindOther
	}
	if l.Name == "" || (len(l.Sections) == 0) {
		return Label{}, false
	}
	return l, true
}

// potencyRe matches homeopathic dilutions ("30C", "6X", "12CK", "LM1").
var potencyRe = regexp.MustCompile(`(?i)\b\d{1,4}\s?(?:x|c|ck|cH|lm)\b`)

var spaceRe = regexp.MustCompile(`[ \t\r\f\v]+`)

func clean(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == ' ' {
			return ' '
		}
		if unicode.IsControl(r) && r != '\n' {
			return -1
		}
		return r
	}, s)
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(spaceRe.ReplaceAllString(l, " ")); l != "" {
			lines = append(lines, l)
		}
	}
	return strings.Join(lines, "\n")
}

func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n]
}

// stripHeading removes the section's own heading that labels often repeat
// ("Purpose Pain reliever" → "Pain reliever", "4 CONTRAINDICATIONS …").
func stripHeading(key, t string) string {
	words := map[string][]string{
		"purpose":                       {"purposes", "purpose"},
		"indications_and_usage":         {"indications and usage", "indications & usage", "uses", "use"},
		"dosage_and_administration":     {"dosage and administration", "directions"},
		"warnings":                      {"warnings", "warning"},
		"do_not_use":                    {"do not use"},
		"ask_doctor":                    {"ask a doctor before use if you have", "ask a doctor before use if"},
		"ask_doctor_or_pharmacist":      {"ask a doctor or pharmacist before use if you are", "ask a doctor or pharmacist before use if"},
		"pregnancy_or_breast_feeding":   {"if pregnant or breast-feeding,", "if pregnant or breast-feeding"},
		"stop_use":                      {"stop use and ask a doctor if", "stop using and ask a doctor if"},
		"when_using":                    {"when using this product"},
		"keep_out_of_reach_of_children": {"keep out of reach of children."},
		"active_ingredient":             {"active ingredients", "active ingredient"},
		"inactive_ingredient":           {"inactive ingredients", "inactive ingredient"},
		"contraindications":             {"contraindications"},
		"overdosage":                    {"overdosage"},
		"adverse_reactions":             {"adverse reactions"},
		"drug_interactions":             {"drug interactions"},
		"boxed_warning":                 {"boxed warning"},
	}[key]
	low := strings.ToLower(t)
	// A leading section number ("5 WARNINGS AND PRECAUTIONS").
	trim := strings.TrimLeft(low, "0123456789. ")
	offset := len(low) - len(trim)
	for _, w := range words {
		if strings.HasPrefix(trim, w) {
			rest := strings.TrimLeft(t[offset+len(w):], " :.-\n")
			if rest != "" {
				return rest
			}
		}
	}
	return t
}

// nameFromElements takes the product name from the start of the SPL
// "product data elements" text: "TopCare Regular Strength Bismuth
// subsalicylate CARBOXYMETHYLCELLULOSE …" → the words before the run of
// upper-case ingredient names.
func nameFromElements(s string) string {
	words := strings.Fields(s)
	var out []string
	sawLower := false
	for i, w := range words {
		up := isUpperWord(w)
		if i > 0 && up && sawLower {
			break
		}
		if !up {
			sawLower = true
		}
		out = append(out, w)
		if len(out) == 12 {
			break
		}
	}
	// "Tofacitinib Tofacitinib" → "Tofacitinib".
	var dedup []string
	seen := map[string]bool{}
	for _, w := range out {
		k := strings.ToLower(w)
		if seen[k] {
			continue
		}
		seen[k] = true
		dedup = append(dedup, w)
	}
	return strings.TrimRight(strings.Join(dedup, " "), " ,;")
}

func isUpperWord(w string) bool {
	letters := 0
	for _, r := range w {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsLower(r) {
				return false
			}
		}
	}
	return letters >= 2
}

var ingredientRe = regexp.MustCompile(`(?i)([a-z][a-z0-9 ,\-'/]*?)\s*(?:\([^)]*\)\s*)?\d+(?:[.,]\d+)?\s*(?:(?:mg|mcg|µg|g|ml|iu|units?)\b|%)`)

// ingredientsFromSection reads an OTC "Active ingredient" panel:
// "(in each tablet) Bismuth subsalicylate 262 mg" → ["bismuth subsalicylate"].
func ingredientsFromSection(s string) []string {
	s = regexp.MustCompile(`\([^)]*\)`).ReplaceAllString(s, " ")
	var out []string
	for _, m := range ingredientRe.FindAllStringSubmatch(s, 8) {
		name := normIngredient(headingRe.ReplaceAllString(strings.ToLower(m[1]), ""))
		if len(name) >= 3 && len(name) <= 60 {
			out = append(out, name)
		}
	}
	return out
}

// headingRe matches panel headings and filler before an ingredient name.
var headingRe = regexp.MustCompile(`^(?:\s*(?:drug facts|active ingredients?|ingredients?|s\b|each (?:tablet|caplet|capsule|softgel|ml|gram|dose)(?: contains)?|in each \w+|per \w+|purposes?|contains)[\s:,.-]*)+`)

var (
	hclRe    = regexp.MustCompile(`\bhc[li]\b`)
	hbrRe    = regexp.MustCompile(`\bhbr\b`)
	gradeRe  = regexp.MustCompile(`\s*\b(?:usp|nf|usp/nf|bp|ep)\b\.?`)
	spacesRe = regexp.MustCompile(`\s+`)
)

// normIngredient makes ingredient names comparable: "Loperamide HCl" and
// "LOPERAMIDE HYDROCHLORIDE", "Calcium carbonate USP" and "calcium
// carbonate" become the same.
func normIngredient(s string) string {
	s = strings.ToLower(s)
	s = hclRe.ReplaceAllString(s, "hydrochloride")
	s = hbrRe.ReplaceAllString(s, "hydrobromide")
	s = gradeRe.ReplaceAllString(s, "")
	s = strings.Trim(spacesRe.ReplaceAllString(s, " "), " ,-/.")
	// "menthol menthol" (name and active moiety) → "menthol".
	if w := strings.Fields(s); len(w)%2 == 0 && len(w) > 0 && strings.Join(w[:len(w)/2], " ") == strings.Join(w[len(w)/2:], " ") {
		s = strings.Join(w[:len(w)/2], " ")
	}
	return s
}

func uniqSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// titleCase turns "ACETAMINOPHEN AND CODEINE" into "Acetaminophen and
// Codeine"; mixed-case names are kept.
func titleCase(s string) string {
	s = strings.TrimSpace(s)
	if s != strings.ToUpper(s) {
		return s
	}
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		if i > 0 && (w == "and" || w == "or" || w == "of" || w == "with" || w == "in") {
			continue
		}
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}

func date(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 8 {
		return s[:4] + "-" + s[4:6] + "-" + s[6:8]
	}
	return ""
}

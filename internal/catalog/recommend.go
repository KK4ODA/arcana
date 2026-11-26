package catalog

import (
	"fmt"
	"sort"
	"strings"

	"github.com/KK4ODA/arcana/internal/storage"
)

// Preset is a curated, editable starting point for a collection. Presets
// recommend; they never install anything on their own.
type Preset struct {
	ID          string       `json:"id"`
	Title       string       `json:"title"`
	Description string       `json:"description"`
	Items       []PresetItem `json:"items"`
}

// PresetItem names a resource family and the variants the curator prefers,
// most detailed first. "{lang}" in Family is replaced with the user's
// language.
type PresetItem struct {
	Family   string   `json:"family"`
	Variants []string `json:"variants,omitempty"`
	// Fallbacks are smaller families offered after every edition of Family,
	// in order, for when none of those fits: Wikipedia's most-read articles
	// after the whole encyclopedia. "{lang}" is replaced with the language
	// Family resolved to; a fallback missing in that language is left out.
	Fallbacks []PresetFallback `json:"fallbacks,omitempty"`
	// Priority 1 is essential, 2 recommended, 3 optional.
	Priority int    `json:"priority"`
	Reason   string `json:"reason"`
}

// PresetFallback is a smaller family a preset item may settle for, with the
// curator's preferred variants, most detailed first.
type PresetFallback struct {
	Family   string   `json:"family"`
	Variants []string `json:"variants,omitempty"`
}

// FamilyIDs lists the family IDs a preset item may draw from in a language:
// Family in that language and in English, then each fallback in both.
func (pi PresetItem) FamilyIDs(lang string) []string {
	ids := []string{strings.ReplaceAll(pi.Family, "{lang}", lang), strings.ReplaceAll(pi.Family, "{lang}", "en")}
	for _, fb := range pi.Fallbacks {
		ids = append(ids, strings.ReplaceAll(fb.Family, "{lang}", lang), strings.ReplaceAll(fb.Family, "{lang}", "en"))
	}
	return ids
}

// RecommendInput is everything the recommender needs. It is a pure function
// of its input so it can be explained and tested.
type RecommendInput struct {
	Preset   Preset
	Language string
	// Budget is the space available for new downloads after the reserve
	// and existing queue (storage.Plan.Headroom with no selection).
	Budget int64
	// Families maps family ID to all available variants.
	Families map[string][]Resource
	// Present holds resource IDs that are installed or already queued.
	Present map[string]bool
}

// Recommendation is the recommender's output: what it suggests, and why.
type Recommendation struct {
	PresetID   string    `json:"presetId"`
	Language   string    `json:"language"`
	Budget     int64     `json:"budget"`
	TotalBytes int64     `json:"totalBytes"`
	Remaining  int64     `json:"remaining"`
	Items      []RecItem `json:"items"`
}

// RecItem is the decision for one preset item.
type RecItem struct {
	Family     string `json:"family"`
	Title      string `json:"title"`
	Priority   int    `json:"priority"`
	Reason     string `json:"reason"`
	ResourceID string `json:"resourceId,omitempty"`
	Variant    string `json:"variant,omitempty"`
	Size       int64  `json:"size"`
	// Status is "selected", "present" (already installed or queued),
	// "skipped" (does not fit) or "unavailable" (not in the catalog).
	Status       string     `json:"status"`
	Decision     string     `json:"decision"`
	Alternatives []RecAlt   `json:"alternatives,omitempty"`
	candidates   []Resource // preference order
}

// RecAlt is another variant the user could pick instead.
type RecAlt struct {
	ResourceID string `json:"resourceId"`
	Variant    string `json:"variant"`
	Label      string `json:"label"`
	Size       int64  `json:"size"`
}

// Recommend chooses variants tier by tier: every essential item is first
// included at its smallest acceptable size, then upgraded toward the
// curator's preferred edition while space allows, before recommended and then
// optional items are considered. Each decision carries a plain-language
// explanation.
func Recommend(in RecommendInput) Recommendation {
	rec := Recommendation{PresetID: in.Preset.ID, Language: in.Language, Budget: in.Budget}
	remaining := in.Budget
	items := make([]*RecItem, 0, len(in.Preset.Items))

	for _, pi := range in.Preset.Items {
		it := &RecItem{Priority: pi.Priority, Reason: pi.Reason}
		if it.Priority < 1 || it.Priority > 3 {
			it.Priority = 3
		}
		family, note := resolveFamily(pi.Family, in.Language, in.Families)
		it.Family = family
		cands := orderedCandidates(in.Families[family], pi.Variants)
		families := []string{family}
		lang := strings.ToLower(in.Language)
		if lang == "" || note != "" {
			lang = "en"
		}
		for _, fb := range pi.Fallbacks {
			f := strings.ReplaceAll(fb.Family, "{lang}", lang)
			if f == family || len(in.Families[f]) == 0 {
				continue
			}
			families = append(families, f)
			cands = append(cands, orderedCandidates(in.Families[f], fb.Variants)...)
		}
		it.candidates = cands
		if len(cands) == 0 {
			it.Status = "unavailable"
			it.Title = family
			it.Decision = "Not found in the current catalog. Refresh the catalog while online, or the publisher may have withdrawn it."
			items = append(items, it)
			continue
		}
		it.Title = cands[0].Title
		for _, c := range cands {
			label := c.VariantLabel
			if c.Family != family {
				label = c.Title + ": " + label
			}
			it.Alternatives = append(it.Alternatives, RecAlt{ResourceID: c.ID, Variant: c.Variant, Label: label, Size: c.DownloadSize})
		}
		for _, f := range families {
			for _, c := range in.Families[f] {
				if in.Present[c.ID] {
					it.Status = "present"
					it.ResourceID, it.Variant = c.ID, c.Variant
					it.Decision = "Already in the library or download queue."
				}
			}
		}
		if note != "" {
			it.Decision = strings.TrimSpace(note + " " + it.Decision)
		}
		items = append(items, it)
	}

	for tier := 1; tier <= 3; tier++ {
		// Include at the smallest acceptable size.
		for _, it := range items {
			if it.Priority != tier || it.Status != "" {
				continue
			}
			smallest := smallestOf(it.candidates)
			if smallest.DownloadSize <= remaining {
				it.Status = "selected"
				it.ResourceID, it.Variant, it.Size = smallest.ID, smallest.Variant, smallest.DownloadSize
				remaining -= smallest.DownloadSize
			} else {
				it.Status = "skipped"
				it.Decision = appendSentence(it.Decision, fmt.Sprintf(
					"Skipped: the smallest edition (%s) needs more than the %s still available.",
					storage.FormatBytes(smallest.DownloadSize), storage.FormatBytes(max64(remaining, 0))))
			}
		}
		// Upgrade toward the preferred edition.
		for _, it := range items {
			if it.Priority != tier || it.Status != "selected" {
				continue
			}
			var chosen *Resource
			var tooBig []string
			for i := range it.candidates {
				c := &it.candidates[i]
				extra := c.DownloadSize - it.Size
				if extra <= remaining {
					chosen = c
					break
				}
				tooBig = append(tooBig, fmt.Sprintf("%s (%s)", editionName(it.Family, *c), storage.FormatBytes(c.DownloadSize)))
			}
			if chosen != nil && chosen.ID != it.ResourceID {
				remaining -= chosen.DownloadSize - it.Size
				it.ResourceID, it.Variant, it.Size = chosen.ID, chosen.Variant, chosen.DownloadSize
			}
			chosenRes := findByID(it.candidates, it.ResourceID)
			if chosenRes.Family != it.Family {
				// Settled for a fallback: name what will actually be downloaded.
				it.Title = chosenRes.Title
			}
			msg := fmt.Sprintf("Chose %s (%s).", editionName(it.Family, chosenRes), storage.FormatBytes(it.Size))
			if len(tooBig) > 0 {
				msg += " Larger editions do not fit the remaining space: " + strings.Join(tooBig, ", ") + "."
			} else if len(it.candidates) > 1 {
				msg += " This is the curator's preferred edition."
			}
			it.Decision = appendSentence(it.Decision, msg)
		}
	}

	for _, it := range items {
		if it.Status == "selected" {
			rec.TotalBytes += it.Size
		}
		rec.Items = append(rec.Items, *it)
	}
	rec.Remaining = remaining
	return rec
}

// resolveFamily substitutes the language and falls back to English when the
// requested language edition does not exist.
func resolveFamily(pattern, lang string, families map[string][]Resource) (string, string) {
	if !strings.Contains(pattern, "{lang}") {
		return pattern, ""
	}
	if lang == "" {
		lang = "en"
	}
	primary := strings.ReplaceAll(pattern, "{lang}", strings.ToLower(lang))
	if len(families[primary]) > 0 || lang == "en" {
		return primary, ""
	}
	fallback := strings.ReplaceAll(pattern, "{lang}", "en")
	if len(families[fallback]) > 0 {
		return fallback, fmt.Sprintf("No %s edition is available; the English edition is suggested instead.", lang)
	}
	return primary, ""
}

// orderedCandidates returns the variants in the curator's preference order.
// With no stated preference, larger (more detailed) editions come first.
func orderedCandidates(all []Resource, prefs []string) []Resource {
	if len(prefs) == 0 {
		out := append([]Resource(nil), all...)
		sort.SliceStable(out, func(i, j int) bool { return out[i].DownloadSize > out[j].DownloadSize })
		return out
	}
	var out []Resource
	for _, p := range prefs {
		for _, r := range all {
			if r.Variant == p {
				out = append(out, r)
			}
		}
	}
	return out
}

func smallestOf(rs []Resource) Resource {
	best := rs[0]
	for _, r := range rs[1:] {
		if r.DownloadSize < best.DownloadSize {
			best = r
		}
	}
	return best
}

func findByID(rs []Resource, id string) Resource {
	for _, r := range rs {
		if r.ID == id {
			return r
		}
	}
	return Resource{}
}

// editionName is variantName, prefixed with the resource's title when it
// comes from a fallback family rather than the item's own.
func editionName(family string, r Resource) string {
	if r.Family != family {
		return r.Title + ", " + variantName(r)
	}
	return variantName(r)
}

func variantName(r Resource) string {
	if r.VariantLabel != "" {
		return strings.ToLower(r.VariantLabel[:1]) + r.VariantLabel[1:]
	}
	if r.Variant != "" {
		return r.Variant
	}
	return "the standard edition"
}

func appendSentence(a, b string) string {
	if a == "" {
		return b
	}
	return a + " " + b
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

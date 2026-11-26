package drugs

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

// Remedies are home-care measures and herbs for the same everyday problems
// as Situations. Unlike the label reference they ship inside Arcana (about
// 50 KB), so they are there before the FDA download and on read-only
// drives. See ADR-0028.
//
// remedies.json is Project NOMAD's home_remedies.json and
// natural_remedies.json (Apache-2.0) with NOMAD's condition names replaced
// by Arcana's situation slugs; the text is unchanged. Each entry summarizes
// one US government page (CDC, MedlinePlus, FDA or NCCIH) named in
// sourceUrl.
//
//go:embed remedies.json
var remediesJSON []byte

// Remedy kinds.
const (
	RemedyHome   = "home"   // home care from CDC, MedlinePlus and FDA pages
	RemedyHerbal = "herbal" // herbs and supplements from NCCIH
)

// Remedy is one home-care measure or herb.
type Remedy struct {
	Slug        string   `json:"slug"`
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	CommonNames []string `json:"commonNames"`
	Situations  []string `json:"situations"`
	Uses        string   `json:"uses"`
	How         string   `json:"how,omitempty"`
	Evidence    string   `json:"evidence"`
	Cautions    string   `json:"cautions"`
	SourceURL   string   `json:"sourceUrl"`
}

// RemedySource describes where one kind of remedy comes from.
type RemedySource struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	License string `json:"license"`
}

// RemedySet is the whole embedded list with its provenance.
type RemedySet struct {
	Version    string         `json:"version"`
	CompiledBy string         `json:"compiledBy"`
	License    string         `json:"license"`
	Converted  string         `json:"converted"`
	Sources    []RemedySource `json:"sources"`
	Remedies   []Remedy       `json:"remedies"`
}

// remedySourceHosts are the only sites a remedy may cite: the US
// government agencies whose pages the list summarizes.
var remedySourceHosts = map[string]bool{
	"www.cdc.gov": true, "medlineplus.gov": true, "www.fda.gov": true, "www.nccih.nih.gov": true,
}

var (
	remediesOnce sync.Once
	remedies     RemedySet
	remediesErr  error
)

// Remedies returns the embedded list. It is parsed and checked once.
func Remedies() (RemedySet, error) {
	remediesOnce.Do(func() {
		remedies, remediesErr = parseRemedies(remediesJSON)
	})
	return remedies, remediesErr
}

func parseRemedies(data []byte) (RemedySet, error) {
	var set RemedySet
	if err := json.Unmarshal(data, &set); err != nil {
		return RemedySet{}, fmt.Errorf("remedies: %w", err)
	}
	seen := map[string]bool{}
	for _, r := range set.Remedies {
		switch {
		case r.Slug == "" || seen[r.Slug]:
			return RemedySet{}, fmt.Errorf("remedies: missing or repeated slug %q", r.Slug)
		case r.Kind != RemedyHome && r.Kind != RemedyHerbal:
			return RemedySet{}, fmt.Errorf("remedies: %s has unknown kind %q", r.Slug, r.Kind)
		case r.Name == "" || r.Uses == "" || r.Evidence == "" || r.Cautions == "":
			return RemedySet{}, fmt.Errorf("remedies: %s lacks a name, uses, evidence or cautions", r.Slug)
		case len(r.Situations) == 0:
			return RemedySet{}, fmt.Errorf("remedies: %s has no situation", r.Slug)
		}
		for _, s := range r.Situations {
			if _, ok := SituationBySlug(s); !ok {
				return RemedySet{}, fmt.Errorf("remedies: %s names unknown situation %q", r.Slug, s)
			}
		}
		u, err := url.Parse(r.SourceURL)
		if err != nil || u.Scheme != "https" || !remedySourceHosts[u.Host] {
			return RemedySet{}, fmt.Errorf("remedies: %s cites %q, which is not a US government health page", r.Slug, r.SourceURL)
		}
		seen[r.Slug] = true
	}
	return set, nil
}

// RemediesFor lists the remedies for any of the situations, home care
// first, in the list's order. No situations means all remedies.
func RemediesFor(slugs []string) []Remedy {
	set, err := Remedies()
	if err != nil {
		return nil
	}
	want := map[string]bool{}
	for _, s := range slugs {
		want[strings.TrimSpace(s)] = true
	}
	var home, herbal []Remedy
	for _, r := range set.Remedies {
		match := len(want) == 0
		for _, s := range r.Situations {
			match = match || want[s]
		}
		if !match {
			continue
		}
		if r.Kind == RemedyHome {
			home = append(home, r)
		} else {
			herbal = append(herbal, r)
		}
	}
	return append(home, herbal...)
}

// RemedyBySlug finds one remedy.
func RemedyBySlug(slug string) (Remedy, bool) {
	set, err := Remedies()
	if err != nil {
		return Remedy{}, false
	}
	for _, r := range set.Remedies {
		if r.Slug == slug {
			return r, true
		}
	}
	return Remedy{}, false
}

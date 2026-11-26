package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/KK4ODA/arcana/internal/catalog"
)

// licenseKind is a license recognised from its text.
type licenseKind struct {
	ID, Name, URL string
	// Redist is what the terms allow when the license is confirmed.
	Redist string
	// HF lists the Hugging Face license values that name the same terms.
	HF []string
	// match reports whether a license text is this license.
	match func(t string) bool
}

func has(subs ...string) func(string) bool {
	return func(t string) bool {
		for _, s := range subs {
			if !strings.Contains(t, s) {
				return false
			}
		}
		return true
	}
}

// licenseKinds are tried in order on the normalised (lower case, single
// spaced) text of each license layer. Acceptable use policies that come as a
// second layer are recognised so they do not hide the license itself.
var licenseKinds = []licenseKind{
	{ID: "Apache-2.0", Name: "Apache License 2.0", URL: "https://www.apache.org/licenses/LICENSE-2.0", Redist: catalog.RedistPermitted,
		HF: []string{"apache-2.0"}, match: func(t string) bool {
			return strings.Contains(t, "apache license version 2.0") || strings.Contains(t, "apache license 2.0") || t == "apache-2.0" ||
				strings.Contains(t, "released under the apache license 2.0")
		}},
	{ID: "MIT", Name: "MIT License", URL: "https://opensource.org/license/mit", Redist: catalog.RedistPermitted,
		HF: []string{"mit"}, match: has("permission is hereby granted, free of charge")},
	{ID: "CC-BY-NC-4.0", Name: "Creative Commons Attribution-NonCommercial 4.0", URL: "https://creativecommons.org/licenses/by-nc/4.0/",
		Redist: catalog.RedistNonCommercial, HF: []string{"cc-by-nc-4.0"}, match: has("attribution-noncommercial 4.0")},
	{ID: "CC-BY-SA-4.0", Name: "Creative Commons Attribution-ShareAlike 4.0", URL: "https://creativecommons.org/licenses/by-sa/4.0/",
		Redist: catalog.RedistPermitted, HF: []string{"cc-by-sa-4.0"}, match: has("attribution-sharealike 4.0")},
	{ID: "LicenseRef-Llama-2", Name: "Llama 2 Community License", URL: "https://ai.meta.com/llama/license/", Redist: catalog.RedistRestricted,
		HF: []string{"llama2"}, match: has("llama 2 community license")},
	{ID: "LicenseRef-Llama-3", Name: "Meta Llama 3 Community License", URL: "https://www.llama.com/llama3/license/", Redist: catalog.RedistRestricted,
		HF: []string{"llama3"}, match: has("meta llama 3 community license")},
	{ID: "LicenseRef-Llama-3.1", Name: "Llama 3.1 Community License", URL: "https://www.llama.com/llama3_1/license/", Redist: catalog.RedistRestricted,
		HF: []string{"llama3.1"}, match: has("llama 3.1 community license")},
	{ID: "LicenseRef-Llama-3.2", Name: "Llama 3.2 Community License", URL: "https://www.llama.com/llama3_2/license/", Redist: catalog.RedistRestricted,
		HF: []string{"llama3.2"}, match: has("llama 3.2 community license")},
	{ID: "LicenseRef-Llama-3.3", Name: "Llama 3.3 Community License", URL: "https://www.llama.com/llama3_3/license/", Redist: catalog.RedistRestricted,
		HF: []string{"llama3.3"}, match: has("llama 3.3 community license")},
	{ID: "LicenseRef-Llama-4", Name: "Llama 4 Community License", URL: "https://www.llama.com/llama4/license/", Redist: catalog.RedistRestricted,
		HF: []string{"llama4"}, match: has("llama 4 community license")},
	{ID: "LicenseRef-Gemma", Name: "Gemma Terms of Use", URL: "https://ai.google.dev/gemma/terms", Redist: catalog.RedistRestricted,
		HF: []string{"gemma"}, match: has("gemma terms of use")},
	{ID: "LicenseRef-HAI-DEF", Name: "Health AI Developer Foundations Terms of Use", URL: "https://developers.google.com/health-ai-developer-foundations/terms",
		Redist: catalog.RedistRestricted, HF: []string{"health-ai-developer-foundations"}, match: has("health ai developer foundations terms of use")},
	{ID: "LicenseRef-Qwen-Research", Name: "Qwen Research License", URL: "https://huggingface.co/Qwen/Qwen2.5-3B-Instruct/blob/main/LICENSE",
		Redist: catalog.RedistNonCommercial, HF: []string{"qwen-research"}, match: has("qwen research license")},
	{ID: "LicenseRef-Tongyi-Qianwen-Research", Name: "Tongyi Qianwen Research License", URL: "https://huggingface.co/Qwen/Qwen1.5-0.5B-Chat/blob/main/LICENSE",
		Redist: catalog.RedistNonCommercial, HF: []string{"tongyi-qianwen-research"}, match: has("tongyi qianwen research license")},
	{ID: "LicenseRef-Tongyi-Qianwen", Name: "Tongyi Qianwen License", URL: "https://huggingface.co/Qwen/Qwen-7B/blob/main/LICENSE",
		Redist: catalog.RedistRestricted, HF: []string{"tongyi-qianwen", "qwen"}, match: has("tongyi qianwen license agreement")},
	{ID: "LicenseRef-DeepSeek", Name: "DeepSeek License Agreement", URL: "https://github.com/deepseek-ai/DeepSeek-LLM/blob/main/LICENSE-MODEL",
		Redist: catalog.RedistRestricted, HF: []string{"deepseek", "deepseek-license"}, match: has("deepseek license agreement")},
	{ID: "LicenseRef-MSR-LA", Name: "Microsoft Research License", URL: "https://huggingface.co/microsoft/Orca-2-7b/blob/main/LICENSE",
		Redist: catalog.RedistNonCommercial, HF: []string{"microsoft-research-license"}, match: has("microsoft research license terms")},
	{ID: "LicenseRef-MRL", Name: "Mistral AI Research License", URL: "https://mistral.ai/licenses/MRL-0.1.md", Redist: catalog.RedistNonCommercial,
		HF: []string{"mrl"}, match: has("mistral ai research license")},
	{ID: "LicenseRef-MNPL", Name: "Mistral AI Non-Production License", URL: "https://mistral.ai/licenses/MNPL-0.1.md", Redist: catalog.RedistNonCommercial,
		HF: []string{"mnpl"}, match: has("mistral ai non-production license")},
	{ID: "LicenseRef-NVIDIA-Open-Model", Name: "NVIDIA Open Model License", URL: "https://www.nvidia.com/en-us/agreements/enterprise-software/nvidia-open-model-license/",
		Redist: catalog.RedistRestricted, HF: []string{"nvidia-open-model-license"}, match: has("nvidia open model license agreement")},
	{ID: "LicenseRef-NVIDIA-AI-Foundation", Name: "NVIDIA AI Foundation Models Community License", URL: "https://developer.download.nvidia.com/licenses/nvidia-ai-foundation-models-community-license-agreement.pdf",
		Redist: catalog.RedistRestricted, HF: []string{"nvidia-community-model-license"}, match: has("nvidia ai foundation models community license")},
	{ID: "LicenseRef-NVIDIA-Evaluation", Name: "NVIDIA Software and Model Evaluation License", URL: "https://www.nvidia.com/en-us/agreements/",
		Redist: catalog.RedistNonCommercial, match: has("nvidia software and model evaluation license")},
	{ID: "LicenseRef-OpenMDW-1.1", Name: "OpenMDW License 1.1", URL: "https://openmdw.ai/license/", Redist: catalog.RedistPermitted,
		HF: []string{"openmdw"}, match: has("openmdw license agreement")},
	{ID: "LicenseRef-LFM-1.0", Name: "LFM Open License 1.0", URL: "https://www.liquid.ai/lfm-license", Redist: catalog.RedistRestricted,
		HF: []string{"lfm1.0"}, match: has("lfm open license v1.0")},
	{ID: "LicenseRef-EXAONE-NC", Name: "EXAONE AI Model License 1.1 (non-commercial)", URL: "https://huggingface.co/LGAI-EXAONE/EXAONE-3.5-7.8B-Instruct/blob/main/LICENSE",
		Redist: catalog.RedistNonCommercial, HF: []string{"exaone"}, match: has("exaone ai model license agreement")},
	{ID: "LicenseRef-Falcon-180B", Name: "Falcon 180B TII License", URL: "https://huggingface.co/spaces/tiiuae/falcon-180b-license/blob/main/LICENSE.txt",
		Redist: catalog.RedistRestricted, match: has("falcon 180b tii license")},
	{ID: "LicenseRef-Falcon-2", Name: "Falcon 2 11B TII License", URL: "https://falconllm-staging.tii.ae/falcon-2-terms-and-conditions.html",
		Redist: catalog.RedistRestricted, HF: []string{"unknown"}, match: has("falcon 2 11b tii license")},
	{ID: "LicenseRef-Falcon-3", Name: "TII Falcon License (Falcon 3)", URL: "https://falconllm.tii.ae/falcon-terms-and-conditions.html",
		Redist: catalog.RedistRestricted, HF: []string{"falcon-llm-license"}, match: has("tii falcon license")},
	{ID: "LicenseRef-BigCode-OpenRAIL-M", Name: "BigCode OpenRAIL-M v1", URL: "https://www.bigcode-project.org/docs/pages/bigcode-openrail/",
		Redist: catalog.RedistRestricted, HF: []string{"bigcode-openrail-m"}, match: has("bigcode open rail-m")},
	{ID: "LicenseRef-Stability-NC", Name: "Stability AI Non-Commercial Research Community License", URL: "https://huggingface.co/stabilityai/stablelm-2-1_6b-chat/blob/main/LICENSE",
		Redist: catalog.RedistNonCommercial, HF: []string{"stabilityai-non-commercial-research-community-license"}, match: func(t string) bool {
			return strings.Contains(t, "stability ai non-commercial research community license") || strings.Contains(t, "stable beluga non-commercial community license")
		}},
	{ID: "LicenseRef-Databricks-Open-Model", Name: "Databricks Open Model License", URL: "https://www.databricks.com/legal/open-model-license",
		Redist: catalog.RedistRestricted, HF: []string{"databricks-open-model-license"}, match: has("databricks open model license")},
	{ID: "LicenseRef-GLM-4", Name: "GLM-4-9B License", URL: "https://huggingface.co/THUDM/glm-4-9b-chat/blob/main/LICENSE",
		Redist: catalog.RedistRestricted, HF: []string{"glm-4"}, match: has("the glm-4-9b license")},
	{ID: "LicenseRef-Nexusflow", Name: "Nexusflow.ai License", URL: "https://huggingface.co/Nexusflow/Athene-V2-Chat/blob/main/Nexusflow_Research_License_.pdf",
		Redist: catalog.RedistRestricted, HF: []string{"nexusflowai-license"}, match: has("nexusflow.ai license terms")},
	{ID: "LicenseRef-Qwen-Community-1.0", Name: "Qwen Community License 1.0", Redist: catalog.RedistRestricted, match: has("qwen community license 1.0")},
	{ID: "LicenseRef-MiniCPM", Name: "MiniCPM Model License", URL: "https://github.com/OpenBMB/MiniCPM/blob/main/MiniCPM%20Model%20License.md",
		Redist: catalog.RedistRestricted, match: has("openbmb")},
}

// policy reports whether a license layer is only an acceptable use policy
// that accompanies a license in another layer.
func policy(t string) bool {
	return strings.Contains(t, "acceptable use policy") && !strings.Contains(t, "license agreement") && !strings.Contains(t, "terms of use")
}

func normalise(t string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.ReplaceAll(t, "*", ""))), " ")
}

// classifyLicense names the license the registry's layers carry. It returns
// nil when there is no license text, and an "unknown" kind when the text is
// not recognised.
func classifyLicense(c *cache, cd *candidate) (*licenseKind, string) {
	var unknown string
	for _, d := range cd.Licenses {
		b, err := os.ReadFile(c.path("blobs", strings.TrimPrefix(d, "sha256:")))
		if err != nil {
			continue
		}
		t := normalise(string(b))
		if t == "" || policy(t) {
			continue
		}
		for i := range licenseKinds {
			if licenseKinds[i].match(t) {
				return &licenseKinds[i], ""
			}
		}
		if unknown == "" {
			unknown = t
		}
	}
	if unknown != "" {
		if len(unknown) > 80 {
			unknown = unknown[:80] + "…"
		}
		return &licenseKind{ID: "unknown", Redist: catalog.RedistUnknown}, unknown
	}
	return nil, ""
}

// hfCard is the license a publisher states on Hugging Face.
type hfCard struct {
	License string `json:"license"`
	Name    string `json:"name"`
	Link    string `json:"link"`
	Status  int    `json:"status"`
}

func (c *cache) hfLicense(ctx context.Context, repo string) (hfCard, error) {
	p := c.path("hf", strings.ReplaceAll(repo, "/", "__")+".json")
	var card hfCard
	if c.readJSON(p, &card) {
		return card, nil
	}
	b, status, err := c.get(ctx, "https://huggingface.co/api/models/"+repo, "application/json", 16<<20)
	if err != nil {
		return card, err
	}
	card.Status = status
	if status == 200 {
		var doc struct {
			CardData struct {
				License any    `json:"license"`
				Name    string `json:"license_name"`
				Link    string `json:"license_link"`
			} `json:"cardData"`
			Tags []string `json:"tags"`
		}
		if err := json.Unmarshal(b, &doc); err != nil {
			return card, err
		}
		switch v := doc.CardData.License.(type) {
		case string:
			card.License = v
		case []any:
			if len(v) > 0 {
				card.License, _ = v[0].(string)
			}
		}
		if card.License == "" {
			for _, t := range doc.Tags {
				if l, ok := strings.CutPrefix(t, "license:"); ok {
					card.License = l
				}
			}
		}
		card.Name, card.Link = doc.CardData.Name, doc.CardData.Link
	}
	return card, c.writeJSON(p, card)
}

func repoFor(st statement, tag string) string {
	if r, ok := st.Repos[tag]; ok {
		return r
	}
	if st.Repo == "" {
		return ""
	}
	return strings.NewReplacer("{S}", strings.ToUpper(tag), "{s}", tag).Replace(st.Repo)
}

// sameTerms reports whether the publisher's Hugging Face license value names
// the terms recognised in the registry's text.
func sameTerms(k *licenseKind, card hfCard) bool {
	for _, v := range k.HF {
		if card.License == v || (card.License == "other" && card.Name == v) {
			return true
		}
	}
	return false
}

// kindForHF finds the license a Hugging Face value names, for models whose
// registry entry carries no license text.
func kindForHF(card hfCard) *licenseKind {
	for i := range licenseKinds {
		if sameTerms(&licenseKinds[i], card) {
			return &licenseKinds[i]
		}
	}
	return nil
}

// license builds the catalog license record. Verified is set only when the
// publisher's statement was read and agrees with the registry's text (or
// the registry has none); everything else is recorded as unconfirmed, with
// redistribution "unknown".
func license(ctx context.Context, c *cache, cd *candidate, st statement, tag string, res *result) catalog.License {
	kind, unknownText := classifyLicense(c, cd)
	repo := repoFor(st, tag)
	var card hfCard
	var cardErr error
	if repo != "" {
		card, cardErr = c.hfLicense(ctx, repo)
		switch {
		case cardErr != nil:
			res.hfProblems = append(res.hfProblems, fmt.Sprintf("%s:%s: %s: %v", cd.Model, tag, repo, cardErr))
		case card.Status != 200:
			res.hfProblems = append(res.hfProblems, fmt.Sprintf("%s:%s: %s: HTTP %d", cd.Model, tag, repo, card.Status))
		}
	}
	statedAt := "https://huggingface.co/" + repo
	if repo != "" && cardErr == nil && card.Status == 200 {
		switch {
		case kind != nil && kind.ID != "unknown" && sameTerms(kind, card):
			res.verified++
			return catalog.License{ID: kind.ID, Name: kind.Name, URL: kind.URL, Redistribution: kind.Redist, Verified: true,
				Notes: fmt.Sprintf("%s states this license on %s; the Ollama registry's copy of the license agrees.", publisherName(st), statedAt)}
		case kind == nil:
			if k := kindForHF(card); k != nil {
				res.verified++
				return catalog.License{ID: k.ID, Name: k.Name, URL: k.URL, Redistribution: k.Redist, Verified: true,
					Notes: fmt.Sprintf("%s states this license on %s. The Ollama registry entry carries no license text.", publisherName(st), statedAt)}
			}
		}
	}
	l := catalog.License{ID: "unknown", Redistribution: catalog.RedistUnknown}
	var notes []string
	switch {
	case kind != nil && kind.ID != "unknown":
		l.ID, l.Name, l.URL = kind.ID, kind.Name, kind.URL
		notes = append(notes, fmt.Sprintf("The license text in the Ollama registry is the %s.", kind.Name))
	case kind != nil:
		notes = append(notes, fmt.Sprintf("The Ollama registry carries a license text Arcana does not recognise (%q).", unknownText))
	default:
		notes = append(notes, "The Ollama registry entry carries no license text.")
	}
	switch {
	case repo == "":
		notes = append(notes, "Not yet confirmed against the publisher's own statement.")
	case cardErr != nil || card.Status != 200:
		notes = append(notes, "The publisher's statement could not be read, so the license is unconfirmed.")
	default:
		stated := card.License
		if card.License == "other" && card.Name != "" {
			stated = card.Name
		}
		notes = append(notes, fmt.Sprintf("%s states %q on %s; Arcana has not confirmed that they match.", publisherName(st), stated, statedAt))
	}
	l.Notes = strings.Join(notes, " ")
	return l
}

func publisherName(st statement) string {
	if st.Publisher != "" {
		return "The publisher (" + st.Publisher + ")"
	}
	return "The publisher"
}

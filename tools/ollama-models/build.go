package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/catalog"
)

// statement says where a publisher states a model's license.
type statement struct {
	Publisher string            `json:"publisher"`
	Repo      string            `json:"repo"`
	Repos     map[string]string `json:"repos"`
}

func readStatements(path string) (map[string]statement, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	out := map[string]statement{}
	for k, v := range raw {
		if strings.HasPrefix(k, "_") {
			continue
		}
		var s statement
		if err := json.Unmarshal(v, &s); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", path, k, err)
		}
		out[k] = s
	}
	return out, nil
}

// Exclusion reasons, in the order a model's files are tested.
const (
	reasonCloud     = "cloud-only"
	reasonNotGGUF   = "not-gguf"
	reasonEmbedding = "embedding"
	reasonEngine    = "engine"
	reasonUntested  = "untested"
	reasonNotQA     = "not-qa"
)

// notAnswering lists models made for one narrow task whose output is not an
// answer to a question: safety classifiers reply "safe" or "unsafe", the OCR
// model reads images, the text-to-SQL models write queries.
var notAnswering = map[string]string{
	"granite3-guardian":   "safety classifier",
	"granite4.1-guardian": "safety classifier",
	"llama-guard3":        "safety classifier",
	"shieldgemma":         "safety classifier",
	"glm-ocr":             "OCR model for images (looped on a question in testing)",
	"duckdb-nsql":         "text-to-SQL model",
	"sqlcoder":            "text-to-SQL model",
}

// codeModels are trained for programming. They are offered with a warning;
// those whose file has no chat template are left out, because without one
// they continue the prompt instead of answering (starcoder:1b in testing).
var codeModels = map[string]bool{
	"codebooga": true, "codegeex4": true, "codegemma": true, "codellama": true, "codeqwen": true,
	"codestral": true, "codeup": true, "deepcoder": true, "deepseek-coder": true, "deepseek-coder-v2": true,
	"devstral": true, "devstral-2": true, "dolphincoder": true, "granite-code": true, "magicoder": true,
	"north-mini-code-1.0": true, "opencoder": true, "phind-codellama": true, "qwen2.5-coder": true,
	"qwen3-coder": true, "stable-code": true, "starcoder": true, "starcoder2": true, "wizardcoder": true,
	"yi-coder": true,
}

var reasonText = map[string]string{
	reasonCloud:     "Cloud only: every tag runs on Ollama's servers, there is no file to download.",
	reasonNotGGUF:   "No GGUF file: the plain size tags are stored in Ollama's own tensor format (MLX, MXFP8), which llamafile cannot read.",
	reasonEmbedding: "Embedding model: turns text into vectors and cannot write answers; Arcana's retrieval does not use embeddings (ADR-0021).",
	reasonEngine:    "llamafile 0.10.6 cannot load the file.",
	reasonUntested:  "Not load-tested with llamafile.",
	reasonNotQA: "Cannot answer questions: made for one narrow task (safety classification, OCR, text-to-SQL), " +
		"or a code model whose file has no chat template, so it continues the prompt instead of answering.",
}

type exclusion struct {
	Model  string
	Tags   []string
	Reason string
	Detail string
}

type result struct {
	listed    int
	files     int
	resources []catalog.Resource
	offered   map[string]int // model -> files offered
	textOnly  map[string]bool
	excluded  []exclusion // models with nothing offered
	fileExcl  []exclusion // files left out of models that are offered
	verified  int
	// contextOnly counts offered files whose context did not fit the test
	// machine (see contextOnly).
	contextOnly int
	hfProblems  []string
	generated   time.Time
}

func build(ctx context.Context, c *cache, models []nomadModel, survey map[string]*modelSurvey, cands []*candidate, stmts map[string]statement) *result {
	res := &result{listed: len(models), offered: map[string]int{}, textOnly: map[string]bool{}, generated: time.Now().UTC()}
	byModel := map[string][]*candidate{}
	for _, cd := range cands {
		byModel[cd.Model] = append(byModel[cd.Model], cd)
	}
	desc := map[string]string{}
	for _, m := range models {
		desc[m.Name] = m.Description
	}
	previous := previousVersions()
	for _, m := range models {
		s := survey[m.Name]
		if s == nil || s.LocalTags == 0 {
			res.excluded = append(res.excluded, exclusion{Model: m.Name, Reason: reasonCloud})
			continue
		}
		var offered []catalog.Resource
		var dropped []exclusion
		for _, cd := range byModel[m.Name] {
			res.files++
			reason, detail := judge(cd)
			if reason != "" {
				dropped = append(dropped, exclusion{Model: cd.Model, Tags: cd.Tags, Reason: reason, Detail: detail})
				continue
			}
			r := resource(ctx, c, cd, desc[cd.Model], stmts[cd.Model], previous, res)
			if !cd.Load.OK {
				res.contextOnly++
			}
			offered = append(offered, r)
			if hasMedia(cd, "projector") {
				res.textOnly[cd.Model] = true
			}
		}
		if len(offered) == 0 {
			// The most telling reason: an embedding model, then a GGUF
			// file the engine refused, then no GGUF file at all.
			reason, detail := reasonNotGGUF, ""
			rank := map[string]int{reasonNotQA: 4, reasonEmbedding: 3, reasonEngine: 2, reasonUntested: 1}
			for _, d := range dropped {
				if rank[d.Reason] > rank[reason] {
					reason, detail = d.Reason, d.Detail
				}
			}
			res.excluded = append(res.excluded, exclusion{Model: m.Name, Reason: reason, Detail: detail})
			continue
		}
		res.fileExcl = append(res.fileExcl, dropped...)
		res.offered[m.Name] = len(offered)
		res.resources = append(res.resources, offered...)
	}
	sort.Slice(res.resources, func(i, j int) bool { return res.resources[i].ID < res.resources[j].ID })
	return res
}

func hasMedia(cd *candidate, m string) bool {
	for _, x := range cd.Media {
		if x == m {
			return true
		}
	}
	return false
}

// judge decides whether a file is offered; it returns a reason when not.
func judge(cd *candidate) (string, string) {
	switch {
	case cd.Digest == "":
		return reasonNotGGUF, ""
	case cd.Header == nil:
		return reasonNotGGUF, cd.Error
	case cd.Header.Pooling || strings.Contains(cd.Model, "embed"):
		return reasonEmbedding, "architecture " + cd.Header.Arch
	case notAnswering[cd.Model] != "":
		return reasonNotQA, notAnswering[cd.Model]
	case codeModels[cd.Model] && !cd.Header.ChatTemplate:
		return reasonNotQA, "code model without a chat template"
	case cd.Load == nil:
		return reasonUntested, ""
	case !cd.Load.OK && !contextOnly(cd.Load):
		return reasonEngine, engineReason(cd)
	}
	return "", ""
}

// contextOnly reports a load that read the whole model but could not
// allocate the 4,096-token context in the test machine's memory (15 GB):
// the file is one llamafile runs, on a computer with enough memory.
func contextOnly(l *loadResult) bool {
	return strings.Contains(l.Error, "failed to create_context") && !strings.Contains(l.Error, "error loading model")
}

// engineReason shortens llama.cpp's message to its cause.
func engineReason(cd *candidate) string {
	e := cd.Load.Error
	switch {
	case strings.Contains(e, "unknown model architecture"):
		return "unknown architecture " + cd.Header.Arch
	case strings.Contains(e, "wrong number of tensors"):
		return fmt.Sprintf("architecture %s with tensors llama.cpp does not expect (%s)", cd.Header.Arch, extraPrefixes(cd.Header))
	case strings.Contains(e, "missing tensor"):
		i := strings.Index(e, "missing tensor")
		msg := e[i:]
		if j := strings.IndexAny(msg, "|\n"); j > 0 {
			msg = msg[:j]
		}
		return fmt.Sprintf("architecture %s: %s", cd.Header.Arch, strings.TrimSpace(msg))
	case strings.Contains(e, "--no-jinja"):
		return "architecture " + cd.Header.Arch + ": the model loads, but llama.cpp's server cannot parse the chat template in the file"
	case strings.Contains(e, "timed out"):
		return "architecture " + cd.Header.Arch + ": loading timed out"
	}
	if i := strings.Index(e, "error loading model:"); i >= 0 {
		msg := strings.TrimSpace(e[i+len("error loading model:"):])
		if j := strings.Index(msg, "|"); j > 0 {
			msg = strings.TrimSpace(msg[:j])
		}
		return "architecture " + cd.Header.Arch + ": " + msg
	}
	return "architecture " + cd.Header.Arch + ": " + e
}

func extraPrefixes(g *ggufInfo) string {
	var out []string
	for p := range g.Prefixes {
		switch p {
		case "v", "mm", "a", "s", "dec", "decision", "mtp", "cls":
			out = append(out, p+".*")
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return "see the load log"
	}
	return strings.Join(out, ", ")
}

// variantTag is the size tag a file is offered under ("7b"), or "latest".
func variantTag(cd *candidate) string {
	for _, t := range cd.Tags {
		if t != "latest" && sizeTag.MatchString(t) {
			return t
		}
	}
	return cd.Tags[0]
}

func resource(ctx context.Context, c *cache, cd *candidate, description string, st statement, previous map[string]string, res *result) catalog.Resource {
	g := cd.Header
	tag := variantTag(cd)
	id := "ollama." + cd.Model
	if tag != "latest" {
		id += "-" + strings.ToLower(tag)
	}
	quant := fileTypeName(g.FileType)
	mem := memoryNeeded(cd)
	sizeWord := strings.ToUpper(tag)
	if g.SizeLabel != "" {
		sizeWord = g.SizeLabel
	}
	if tag == "latest" && g.SizeLabel == "" {
		sizeWord = ""
	}
	var d strings.Builder
	d.WriteString(strings.TrimSpace(description))
	if !strings.HasSuffix(d.String(), ".") && d.Len() > 0 {
		d.WriteString(".")
	}
	params := ""
	if sizeWord != "" {
		params = sizeWord + " parameters, "
	}
	fmt.Fprintf(&d, " This file: %s%s, %s. Needs about %s of memory with Arcana's default 4,096-token context; longer contexts need more.",
		params, quant, gb(cd.Size), gbRound(mem))
	if hasMedia(cd, "projector") {
		d.WriteString(" Ollama also offers image input for this model; Arcana asks text questions only and does not download the image part.")
	}
	if !g.ChatTemplate {
		d.WriteString(" The file has no chat template, so llamafile falls back to a generic one and answers may be poorer.")
	}
	label := fmt.Sprintf("%s: %s file, about %s memory", tag, gb(cd.Size), gbRound(mem))
	version := previous[cd.Digest]
	if version == "" {
		version = res.generated.Format("2006-01-02")
	}
	lic := license(ctx, c, cd, st, tag, res)
	verifiedAt := res.generated.Truncate(24 * time.Hour)
	tags := []string{"ai", "assistant", "gguf", "ollama", g.Arch}
	r := catalog.Resource{
		ID:           id,
		Provider:     "ollama",
		Title:        cd.Model + ":" + tag,
		Description:  d.String(),
		Category:     "assistant",
		Subcategory:  "model",
		Tags:         tags,
		Family:       "ollama." + cd.Model,
		Variant:      tag,
		VariantLabel: label,
		Publisher:    st.Publisher,
		SourceURL:    "https://ollama.com/library/" + cd.Model,
		Version:      version,
		LastVerified: &verifiedAt,
		Languages:    []string{"mul"},
		Coverage:     catalog.Coverage{Scope: "none"},
		Format:       "gguf",
		Files: []catalog.File{{
			Name:    fmt.Sprintf("%s-%s-%s.gguf", cd.Model, tag, quant),
			Size:    cd.Size,
			Hashes:  map[string]string{"sha256": strings.TrimPrefix(cd.Digest, "sha256:")},
			Mirrors: []catalog.Mirror{{URL: blobURL(cd.Model, cd.Digest)}},
		}},
		DownloadSize:   cd.Size,
		InstalledSize:  cd.Size,
		License:        lic,
		Dependencies:   []string{"arcana.llamafile"},
		UpdateStrategy: catalog.UpdateReplace,
		Warnings:       warnings(cd.Model),
		Stats:          map[string]int64{"memoryBytes": mem, "parameters": int64(g.Params), "contextLength": int64(g.Context)},
		ProviderRef:    map[string]string{"ollama": cd.Model + ":" + tag, "architecture": g.Arch, "quantization": quant},
	}
	if hasMedia(cd, "projector") {
		r.ProviderRef["textOnly"] = "true"
	}
	if !g.ChatTemplate {
		r.ProviderRef["chatTemplate"] = "none"
	}
	return r
}

func warnings(model string) []catalog.Warning {
	w := []catalog.Warning{{Kind: "content", Text: "Answers come from a language model reading passages of your library. " +
		"It can misread or leave things out: check the cited passages before acting, especially for medical, legal or safety questions."}}
	if codeModels[model] {
		w = append(w, catalog.Warning{Kind: "content", Text: "Made for programming: it answers general questions poorly."})
	}
	return w
}

// memoryNeeded estimates the memory a model needs in Arcana: the weights,
// the key/value cache for 4,096 tokens in 16-bit floats, and llama.cpp's
// compute buffers.
func memoryNeeded(cd *candidate) int64 {
	g := cd.Header
	headDim := g.KeyLength
	if headDim == 0 && g.Heads > 0 {
		headDim = g.Embedding / g.Heads
	}
	kvHeads := g.HeadsKV
	if kvHeads == 0 {
		kvHeads = g.Heads
	}
	kv := int64(2 * g.Layers * 4096 * kvHeads * headDim * 2)
	if kv < 0 || kv > 64<<30 {
		kv = 0
	}
	return cd.Size + kv + cd.Size/20 + 300<<20
}

func gb(n int64) string {
	v := float64(n) / 1e9
	switch {
	case v < 1:
		return fmt.Sprintf("%d MB", int(math.Round(v*1000)))
	case v < 10:
		return fmt.Sprintf("%.1f GB", v)
	}
	return fmt.Sprintf("%.0f GB", v)
}

// gbRound rounds a memory estimate up: to half gigabytes below 4 GB, whole
// gigabytes above. Gigabytes are decimal, as the interface shows sizes.
func gbRound(n int64) string {
	v := float64(n) / 1e9
	if v < 4 {
		v = math.Ceil(v*2) / 2
		if v == math.Trunc(v) {
			return fmt.Sprintf("%.0f GB", v)
		}
		return fmt.Sprintf("%.1f GB", v)
	}
	return fmt.Sprintf("%.0f GB", math.Ceil(v))
}

// previousVersions keeps an entry's version while its file is unchanged, so
// regenerating the catalog does not announce updates that are not there.
func previousVersions() map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(*outFlag)
	if err != nil {
		return out
	}
	var f catalog.Feed
	if json.Unmarshal(b, &f) != nil {
		return out
	}
	for _, r := range f.Resources {
		if len(r.Files) == 1 {
			out["sha256:"+r.Files[0].Hashes["sha256"]] = r.Version
		}
	}
	return out
}

func writeCatalog(path string, res *result) error {
	f := catalog.Feed{Format: "arcana-catalog", FormatVersion: 1,
		Title:     "Models on Project NOMAD's list that llamafile runs, from the Ollama registry",
		Generated: res.generated.Format(time.RFC3339), Resources: res.resources}
	valid, errs := catalog.ValidateAll("ollama", append([]catalog.Resource(nil), res.resources...))
	if len(errs) > 0 {
		return fmt.Errorf("generated entries do not validate: %v", errs[0])
	}
	_ = valid
	b, err := json.MarshalIndent(f, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func (r *result) summary() string {
	return fmt.Sprintf("%d models listed, %d offered as %d files; %d excluded; %d licenses verified",
		r.listed, len(r.offered), len(r.resources), len(r.excluded), r.verified)
}

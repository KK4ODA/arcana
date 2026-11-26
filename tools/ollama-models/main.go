// Command ollama-models builds internal/catalog/bundled/ollama-models.json:
// the models on Project NOMAD's list that Arcana's engine (llamafile) can
// run, each pinned to the SHA-256 digest of its GGUF file in the Ollama
// registry (ADR-0029).
//
// For every model on the list it reads the registry manifests of the
// "latest" tag and of each plain size tag ("7b", "e4b", "30b-a3b"), keeps one
// entry per distinct model file, reads the GGUF header with a streamed GET
// that stops where tensor data begins, and reads the license layer. With
// -llamafile it also starts llamafile's server on a sparse copy of each file
// (the real header, zeros after it) to confirm the engine loads it.
//
// Every answer is cached under -cache, so a run can be repeated offline and
// the report regenerated:
//
//	go run ./tools/ollama-models -cache /tmp/ollama-cache \
//	    -llamafile /path/to/llamafile-0.10.6 \
//	    -out internal/catalog/bundled/ollama-models.json \
//	    -report docs/ollama-models.md
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// nomadModel is one entry of NOMAD's model list.
type nomadModel struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Tags        []nomadTag `json:"tags"`
}

type nomadTag struct {
	Name    string `json:"name"`
	Size    string `json:"size"`
	Context string `json:"context"`
	Input   string `json:"input"`
	Cloud   int    `json:"cloud"`
}

var outFlag = flag.String("out", "", "catalog output file (its versions are kept for unchanged files)")

// candidate is one distinct model file of a listed model.
type candidate struct {
	Model    string      `json:"model"`
	Tags     []string    `json:"tags"`
	Digest   string      `json:"digest"`
	Size     int64       `json:"size"`
	Media    []string    `json:"media"`
	Licenses []string    `json:"licenses,omitempty"`
	Header   *ggufInfo   `json:"header,omitempty"`
	Error    string      `json:"error,omitempty"`
	Load     *loadResult `json:"load,omitempty"`
}

// sizeTag matches tags that name a size and nothing else: "7b", "1.5b",
// "135m", "e4b", "8x7b", "30b-a3b", "16x17b". Ollama points each at its
// default quantization of that size.
var sizeTag = regexp.MustCompile(`^(e?\d+(\.\d+)?[bm]|\d+x\d+(\.\d+)?b|\d+(\.\d+)?b-a\d+(\.\d+)?b)$`)

func main() {
	listURL := flag.String("list", "https://api.projectnomad.us/api/v1/ollama/models", "NOMAD model list (URL or file)")
	cacheDir := flag.String("cache", "ollama-cache", "cache directory")
	engine := flag.String("llamafile", "", "llamafile executable for load tests (optional)")
	engineVersion := flag.String("engine-version", "llamafile 0.10.6", "engine name recorded with load results")
	out := outFlag
	report := flag.String("report", "", "Markdown report output file")
	licenses := flag.String("licenses", "tools/ollama-models/licenses.json", "publisher license statements")
	workers := flag.Int("workers", 8, "parallel registry requests")
	flag.Parse()
	ctx := context.Background()
	c := &cache{dir: *cacheDir, client: &http.Client{Timeout: 0}}

	models, err := loadList(ctx, c, *listURL)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("%d models listed", len(models))

	var mu sync.Mutex
	var cands []*candidate
	survey := map[string]*modelSurvey{}
	jobs := make(chan nomadModel)
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for m := range jobs {
				s, cs := surveyModel(ctx, c, m)
				mu.Lock()
				survey[m.Name] = s
				cands = append(cands, cs...)
				mu.Unlock()
			}
		}()
	}
	for _, m := range models {
		jobs <- m
	}
	close(jobs)
	wg.Wait()
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].Model != cands[j].Model {
			return cands[i].Model < cands[j].Model
		}
		return cands[i].Size < cands[j].Size
	})

	if *engine != "" {
		runLoadTests(ctx, c, *engine, *engineVersion, cands)
	}
	if err := c.writeJSON(filepath.Join(c.dir, "survey.json"), cands); err != nil {
		log.Fatal(err)
	}
	stmts, err := readStatements(*licenses)
	if err != nil {
		log.Fatal(err)
	}
	res := build(ctx, c, models, survey, cands, stmts)
	if *out != "" {
		if err := writeCatalog(*out, res); err != nil {
			log.Fatal(err)
		}
	}
	if *report != "" {
		if err := os.WriteFile(*report, []byte(res.report(*engineVersion)), 0o644); err != nil {
			log.Fatal(err)
		}
	}
	log.Print(res.summary())
}

func loadList(ctx context.Context, c *cache, src string) ([]nomadModel, error) {
	var b []byte
	var err error
	if strings.HasPrefix(src, "https://") {
		p := c.path("nomad-models.json")
		if b, err = os.ReadFile(p); err != nil {
			var status int
			b, status, err = c.get(ctx, src, "application/json", 64<<20)
			if err != nil {
				return nil, err
			}
			if status != 200 {
				return nil, fmt.Errorf("%s: HTTP %d", src, status)
			}
			if err := os.WriteFile(p, b, 0o644); err != nil {
				return nil, err
			}
		}
	} else if b, err = os.ReadFile(src); err != nil {
		return nil, err
	}
	var doc struct {
		Models []nomadModel `json:"models"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("model list: %w", err)
	}
	return doc.Models, nil
}

// modelSurvey records how one listed model was handled before any file was
// examined.
type modelSurvey struct {
	LocalTags int    `json:"localTags"`
	Picked    int    `json:"picked"`
	Problem   string `json:"problem,omitempty"`
}

func surveyModel(ctx context.Context, c *cache, m nomadModel) (*modelSurvey, []*candidate) {
	s := &modelSurvey{}
	var local []string
	for _, t := range m.Tags {
		if t.Cloud != 0 {
			continue
		}
		_, tag, ok := strings.Cut(t.Name, ":")
		if !ok {
			tag = "latest"
		}
		local = append(local, tag)
	}
	s.LocalTags = len(local)
	if len(local) == 0 {
		return s, nil
	}
	var picked []string
	for _, t := range local {
		if t == "latest" || sizeTag.MatchString(t) {
			picked = append(picked, t)
		}
	}
	if len(picked) == 0 {
		// No plain tag: take the first listed, which NOMAD shows first.
		picked = local[:1]
	}
	sort.Strings(picked)
	s.Picked = len(picked)
	byDigest := map[string]*candidate{}
	var out []*candidate
	gguf := false
	for i := 0; i < len(picked); i++ {
		tag := picked[i]
		man, err := c.fetchManifest(ctx, m.Name, tag)
		if err != nil {
			s.Problem = err.Error()
			continue
		}
		if man == nil {
			continue
		}
		var model *layer
		var media, licenses []string
		for i, l := range man.Layers {
			media = append(media, strings.TrimPrefix(l.MediaType, "application/vnd.ollama.image."))
			switch l.MediaType {
			case "application/vnd.ollama.image.model":
				if model == nil {
					model = &man.Layers[i]
				}
			case "application/vnd.ollama.image.license":
				licenses = append(licenses, l.Digest)
			}
		}
		key := "nomodel:" + tag
		if model != nil {
			key = model.Digest
		}
		if cd, ok := byDigest[key]; ok {
			cd.Tags = append(cd.Tags, tag)
			continue
		}
		cd := &candidate{Model: m.Name, Tags: []string{tag}, Media: dedupe(media), Licenses: licenses}
		if model != nil {
			cd.Digest, cd.Size = model.Digest, model.Size
			info, err := c.fetchHeader(ctx, m.Name, model.Digest)
			if err != nil {
				cd.Error = err.Error()
			} else {
				cd.Header = info
			}
		}
		for _, l := range licenses {
			if _, err := c.fetchBlob(ctx, m.Name, l); err != nil && cd.Error == "" {
				cd.Error = "license: " + err.Error()
			}
		}
		byDigest[key] = cd
		out = append(out, cd)
		gguf = gguf || model != nil
		if i == len(picked)-1 && !gguf {
			// The plain tags hold no GGUF file (Ollama's tensor format):
			// try the usual default quantization, then 8 bits.
			if q := quantTag(local); q != "" {
				picked = append(picked, q)
				s.Picked++
			}
		}
	}
	return s, out
}

// quantTag returns the Q4_K_M tag of a model, else its Q8_0 tag.
func quantTag(tags []string) string {
	for _, suffix := range []string{"q4_k_m", "q8_0"} {
		for _, t := range tags {
			if strings.HasSuffix(strings.ToLower(t), suffix) {
				return t
			}
		}
	}
	return ""
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// runLoadTests loads every GGUF candidate in llamafile, one at a time (each
// load maps the whole file and allocates its context).
func runLoadTests(ctx context.Context, c *cache, engine, version string, cands []*candidate) {
	work := filepath.Join(c.dir, "work")
	n := 0
	for _, cd := range cands {
		if cd.Header == nil || cd.Header.Pooling {
			continue
		}
		hex := strings.TrimPrefix(cd.Digest, "sha256:")
		p := c.path("loads", hex+".json")
		var r loadResult
		if c.readJSON(p, &r) && r.Engine == version {
			cd.Load = &r
			continue
		}
		r = loadTest(ctx, engine, version, c.path("heads", hex+".bin"), cd.Size, work)
		cd.Load = &r
		n++
		log.Printf("load %s:%s ok=%v %.1fs %s", cd.Model, cd.Tags[0], r.OK, r.Seconds, r.Error)
		if err := c.writeJSON(p, r); err != nil {
			log.Fatal(err)
		}
	}
	log.Printf("%d load tests run", n)
}

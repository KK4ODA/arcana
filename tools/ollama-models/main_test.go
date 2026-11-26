package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"strings"
	"testing"
)

// ggufBytes builds a minimal GGUF v3 header with string and integer
// metadata and the given tensors (2-D, F32), followed by data padding.
func ggufBytes(t *testing.T, kv map[string]any, tensors []string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	str := func(s string) { w(uint64(len(s))); b.WriteString(s) }
	b.WriteString("GGUF")
	w(uint32(3))
	w(uint64(len(tensors)))
	w(uint64(len(kv)))
	for k, v := range kv {
		str(k)
		switch v := v.(type) {
		case string:
			w(uint32(8))
			str(v)
		case uint32:
			w(uint32(4))
			w(v)
		case []string: // array of strings
			w(uint32(9))
			w(uint32(8))
			w(uint64(len(v)))
			for _, s := range v {
				str(s)
			}
		}
	}
	for i, name := range tensors {
		str(name)
		w(uint32(2))
		w(uint64(4))
		w(uint64(8))
		w(uint32(0))
		w(uint64(i * 128))
	}
	for b.Len()%32 != 0 {
		b.WriteByte(0)
	}
	b.Write(make([]byte, 64)) // the start of tensor data
	return b.Bytes()
}

func TestParseGGUF(t *testing.T) {
	raw := ggufBytes(t, map[string]any{
		"general.architecture":          "qwen2",
		"general.file_type":             uint32(15),
		"qwen2.context_length":          uint32(32768),
		"qwen2.block_count":             uint32(24),
		"qwen2.attention.head_count":    uint32(14),
		"qwen2.attention.head_count_kv": uint32(2),
		"qwen2.embedding_length":        uint32(896),
		"tokenizer.chat_template":       "{{ messages }}",
		"tokenizer.ggml.tokens":         []string{"a", "b", "c"},
	}, []string{"token_embd.weight", "blk.0.attn_q.weight", "v.blk.0.attn_q.weight", "mm.0.weight"})
	g, err := parseGGUF(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if g.Arch != "qwen2" || g.Context != 32768 || g.Layers != 24 || g.HeadsKV != 2 || !g.ChatTemplate || g.Pooling {
		t.Fatalf("metadata %+v", g)
	}
	if fileTypeName(g.FileType) != "Q4_K_M" || g.Params != 4*32 || g.Tensors != 4 {
		t.Fatalf("file type %s, params %d, tensors %d", fileTypeName(g.FileType), g.Params, g.Tensors)
	}
	if g.Prefixes["v"] != 1 || g.Prefixes["mm"] != 1 || g.Prefixes["blk"] != 1 {
		t.Fatalf("prefixes %v", g.Prefixes)
	}
	if g.DataOffset%32 != 0 || g.DataOffset != int64(len(raw)-64) {
		t.Fatalf("data offset %d, header %d bytes", g.DataOffset, len(raw)-64)
	}
	if _, err := parseGGUF(strings.NewReader("PK\x03\x04 not a model")); err == nil {
		t.Fatal("a zip file parsed as GGUF")
	}
	// A truncated header is an error, never a short result.
	if _, err := parseGGUF(bytes.NewReader(raw[:40])); err == nil {
		t.Fatal("truncated header parsed")
	}
}

func TestSizeTags(t *testing.T) {
	for tag, want := range map[string]bool{
		"7b": true, "0.5b": true, "135m": true, "e4b": true, "8x7b": true, "30b-a3b": true, "16x17b": true, "120b-a12b": true,
		"latest": false, "7b-instruct": false, "7b-q4_K_M": false, "e2b-mxfp8": false, "27b-mlx": false, "1048k": false, "v1.5": false,
	} {
		if sizeTag.MatchString(tag) != want {
			t.Errorf("%s: size tag %v, want %v", tag, !want, want)
		}
	}
}

func TestClassifyLicense(t *testing.T) {
	dir := t.TempDir()
	c := &cache{dir: dir}
	put := func(digest, text string) string {
		if err := os.WriteFile(c.path("blobs", digest), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return "sha256:" + digest
	}
	apache := put("a1", "Apache License\n Version 2.0, January 2004\n http://www.apache.org/licenses/")
	policy := put("b2", "# Llama 3.1 Acceptable Use Policy\nMeta is committed to promoting safe and fair use")
	llama := put("c3", "LLAMA 3.1 COMMUNITY LICENSE AGREEMENT\nLlama 3.1 Version Release Date: July 23, 2024")
	odd := put("d4", "Some bespoke terms nobody has seen")
	cases := []struct {
		layers []string
		want   string
	}{
		{[]string{apache}, "Apache-2.0"},
		{[]string{policy, llama}, "LicenseRef-Llama-3.1"}, // the policy does not hide the license
		{[]string{odd}, "unknown"},
	}
	for _, tc := range cases {
		k, _ := classifyLicense(c, &candidate{Licenses: tc.layers})
		if k == nil || k.ID != tc.want {
			t.Errorf("%v: got %+v, want %s", tc.layers, k, tc.want)
		}
	}
	if k, _ := classifyLicense(c, &candidate{}); k != nil {
		t.Errorf("no license layer gave %+v", k)
	}
	// The publisher's statement must name the same terms.
	if !sameTerms(&licenseKinds[0], hfCard{License: "apache-2.0"}) || sameTerms(&licenseKinds[0], hfCard{License: "mit"}) {
		t.Error("Apache-2.0 matching")
	}
	qwen := kindForHF(hfCard{License: "other", Name: "qwen-research"})
	if qwen == nil || qwen.ID != "LicenseRef-Qwen-Research" {
		t.Errorf("qwen-research: %+v", qwen)
	}
	if repoFor(statement{Repo: "Qwen/Qwen2.5-{S}-Instruct"}, "0.5b") != "Qwen/Qwen2.5-0.5B-Instruct" ||
		repoFor(statement{Repo: "x/{s}", Repos: map[string]string{"70b": "y/z"}}, "70b") != "y/z" {
		t.Error("repository patterns")
	}
}

func TestJudge(t *testing.T) {
	g := &ggufInfo{Arch: "llama"}
	cases := []struct {
		cd     candidate
		reason string
	}{
		{candidate{Model: "m"}, reasonNotGGUF},
		{candidate{Model: "nomic-embed-text", Digest: "sha256:x", Header: &ggufInfo{Arch: "nomic-bert", Pooling: true}}, reasonEmbedding},
		{candidate{Model: "m", Digest: "sha256:x", Header: g}, reasonUntested},
		{candidate{Model: "m", Digest: "sha256:x", Header: g, Load: &loadResult{Error: "llama_model_load: error loading model: unknown model architecture: 'clef'"}}, reasonEngine},
		// The weights loaded; only the context did not fit the test machine.
		{candidate{Model: "m", Digest: "sha256:x", Header: g, Load: &loadResult{Error: "E srv load_model: failed to create_context with model"}}, ""},
		{candidate{Model: "m", Digest: "sha256:x", Header: g, Load: &loadResult{OK: true}}, ""},
		// Models that do not answer questions are left out even when they load.
		{candidate{Model: "llama-guard3", Digest: "sha256:x", Header: g, Load: &loadResult{OK: true}}, reasonNotQA},
		{candidate{Model: "starcoder", Digest: "sha256:x", Header: g, Load: &loadResult{OK: true}}, reasonNotQA},
		{candidate{Model: "starcoder2", Digest: "sha256:x", Header: &ggufInfo{Arch: "starcoder2", ChatTemplate: true}, Load: &loadResult{OK: true}}, ""},
	}
	for i, tc := range cases {
		if got, _ := judge(&tc.cd); got != tc.reason {
			t.Errorf("case %d: %q, want %q", i, got, tc.reason)
		}
	}
	if len(warnings("qwen2.5-coder")) != 2 || len(warnings("qwen2.5")) != 1 {
		t.Error("code models must carry the programming warning, others not")
	}
	if r := memoryNeeded(&candidate{Size: 1 << 30, Header: &ggufInfo{Layers: 24, Heads: 14, HeadsKV: 2, Embedding: 896}}); r <= 1<<30 {
		t.Errorf("memory %d is not above the file size", r)
	}
}

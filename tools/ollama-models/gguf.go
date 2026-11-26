package main

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
)

// ggufInfo is what the generator needs from a GGUF header: the metadata it
// shows or uses to estimate memory, and the tensor names that reveal parts
// llama.cpp does not expect (vision or audio towers in the same file).
type ggufInfo struct {
	Version      uint32            `json:"version"`
	Arch         string            `json:"arch"`
	Name         string            `json:"name,omitempty"`
	SizeLabel    string            `json:"sizeLabel,omitempty"`
	FileType     uint32            `json:"fileType"`
	Context      uint64            `json:"context,omitempty"`
	Layers       uint64            `json:"layers,omitempty"`
	Embedding    uint64            `json:"embedding,omitempty"`
	Heads        uint64            `json:"heads,omitempty"`
	HeadsKV      uint64            `json:"headsKV,omitempty"`
	KeyLength    uint64            `json:"keyLength,omitempty"`
	ValueLength  uint64            `json:"valueLength,omitempty"`
	Pooling      bool              `json:"pooling,omitempty"`
	ChatTemplate bool              `json:"chatTemplate"`
	Languages    []string          `json:"languages,omitempty"`
	License      string            `json:"license,omitempty"`
	Params       uint64            `json:"params"`
	Tensors      int               `json:"tensors"`
	Prefixes     map[string]int    `json:"prefixes"`
	DataOffset   int64             `json:"dataOffset"`
	Strings      map[string]string `json:"strings,omitempty"`
}

type countingReader struct {
	r *bufio.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// maxHeaderString bounds one metadata string; tokenizer vocabularies are
// arrays of short strings, chat templates are the longest single values.
const maxHeaderString = 4 << 20

// parseGGUF reads a GGUF header (metadata and tensor infos) from r. It stops
// at the start of tensor data, so r can be a ranged HTTP stream of a file of
// any size.
func parseGGUF(r io.Reader) (*ggufInfo, error) {
	cr := &countingReader{r: bufio.NewReaderSize(r, 1<<20)}
	rd := func(v any) error { return binary.Read(cr, binary.LittleEndian, v) }
	var magic [4]byte
	if _, err := io.ReadFull(cr, magic[:]); err != nil {
		return nil, err
	}
	if string(magic[:]) != "GGUF" {
		return nil, errors.New("not a GGUF file")
	}
	g := &ggufInfo{Prefixes: map[string]int{}, Strings: map[string]string{}}
	if err := rd(&g.Version); err != nil {
		return nil, err
	}
	if g.Version < 2 || g.Version > 3 {
		return nil, fmt.Errorf("GGUF version %d", g.Version)
	}
	var nTensors, nKV uint64
	if err := rd(&nTensors); err != nil {
		return nil, err
	}
	if err := rd(&nKV); err != nil {
		return nil, err
	}
	if nTensors > 1<<20 || nKV > 1<<16 {
		return nil, errors.New("implausible GGUF header counts")
	}
	readString := func() (string, error) {
		var n uint64
		if err := rd(&n); err != nil {
			return "", err
		}
		if n > maxHeaderString {
			return "", fmt.Errorf("GGUF string of %d bytes", n)
		}
		b := make([]byte, n)
		_, err := io.ReadFull(cr, b)
		return string(b), err
	}
	var readValue func(t uint32) (any, error)
	readValue = func(t uint32) (any, error) {
		switch t {
		case 0, 1, 7:
			var v uint8
			err := rd(&v)
			return uint64(v), err
		case 2, 3:
			var v uint16
			err := rd(&v)
			return uint64(v), err
		case 4, 5:
			var v uint32
			err := rd(&v)
			return uint64(v), err
		case 6:
			var v uint32
			err := rd(&v)
			return float64(math.Float32frombits(v)), err
		case 8:
			return readString()
		case 10, 11:
			var v uint64
			err := rd(&v)
			return v, err
		case 12:
			var v uint64
			err := rd(&v)
			return math.Float64frombits(v), err
		case 9:
			var et uint32
			var n uint64
			if err := rd(&et); err != nil {
				return nil, err
			}
			if err := rd(&n); err != nil {
				return nil, err
			}
			if n > 1<<24 {
				return nil, fmt.Errorf("GGUF array of %d items", n)
			}
			var out []any
			for i := uint64(0); i < n; i++ {
				v, err := readValue(et)
				if err != nil {
					return nil, err
				}
				// Only short arrays are kept; vocabularies are skipped.
				if n <= 64 {
					out = append(out, v)
				}
			}
			return out, nil
		}
		return nil, fmt.Errorf("unknown GGUF value type %d", t)
	}
	kv := map[string]any{}
	for i := uint64(0); i < nKV; i++ {
		k, err := readString()
		if err != nil {
			return nil, err
		}
		var t uint32
		if err := rd(&t); err != nil {
			return nil, err
		}
		v, err := readValue(t)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		kv[k] = v
	}
	str := func(k string) string { s, _ := kv[k].(string); return s }
	num := func(k string) uint64 {
		switch v := kv[k].(type) {
		case uint64:
			return v
		case []any: // per-layer values (some hybrid models): take the largest
			var m uint64
			for _, x := range v {
				if u, ok := x.(uint64); ok && u > m {
					m = u
				}
			}
			return m
		}
		return 0
	}
	g.Arch = str("general.architecture")
	g.Name = str("general.name")
	g.SizeLabel = str("general.size_label")
	g.License = str("general.license")
	g.FileType = uint32(num("general.file_type"))
	a := g.Arch
	g.Context = num(a + ".context_length")
	g.Layers = num(a + ".block_count")
	g.Embedding = num(a + ".embedding_length")
	g.Heads = num(a + ".attention.head_count")
	g.HeadsKV = num(a + ".attention.head_count_kv")
	g.KeyLength = num(a + ".attention.key_length")
	g.ValueLength = num(a + ".attention.value_length")
	_, g.Pooling = kv[a+".pooling_type"]
	_, g.ChatTemplate = kv["tokenizer.chat_template"]
	if langs, ok := kv["general.languages"].([]any); ok {
		for _, l := range langs {
			if s, ok := l.(string); ok {
				g.Languages = append(g.Languages, s)
			}
		}
	}
	for _, k := range []string{"general.basename", "general.finetune", "general.organization", "general.license.name", "general.license.link", "general.base_model.0.repo_url", "general.repo_url"} {
		if s := str(k); s != "" {
			g.Strings[k] = s
		}
	}
	alignment := uint64(32)
	if v := num("general.alignment"); v != 0 {
		alignment = v
	}
	for i := uint64(0); i < nTensors; i++ {
		name, err := readString()
		if err != nil {
			return nil, err
		}
		var nd uint32
		if err := rd(&nd); err != nil {
			return nil, err
		}
		if nd > 8 {
			return nil, fmt.Errorf("tensor %s has %d dimensions", name, nd)
		}
		count := uint64(1)
		for d := uint32(0); d < nd; d++ {
			var dim uint64
			if err := rd(&dim); err != nil {
				return nil, err
			}
			count *= dim
		}
		var typ uint32
		var off uint64
		if err := rd(&typ); err != nil {
			return nil, err
		}
		if err := rd(&off); err != nil {
			return nil, err
		}
		g.Prefixes[tensorPrefix(name)]++
		g.Params += count
	}
	g.Tensors = int(nTensors)
	pos := uint64(cr.n)
	g.DataOffset = int64((pos + alignment - 1) / alignment * alignment)
	return g, nil
}

// tensorPrefix groups tensor names: "blk.3.attn_q.weight" is "blk",
// "v.blk.0.attn_q.weight" is "v", "mm.0.weight" is "mm".
func tensorPrefix(name string) string {
	p, _, _ := strings.Cut(name, ".")
	return p
}

// fileTypeName names llama.cpp's general.file_type values.
func fileTypeName(t uint32) string {
	names := map[uint32]string{0: "F32", 1: "F16", 2: "Q4_0", 3: "Q4_1", 7: "Q8_0", 8: "Q5_0", 9: "Q5_1",
		10: "Q2_K", 11: "Q3_K_S", 12: "Q3_K_M", 13: "Q3_K_L", 14: "Q4_K_S", 15: "Q4_K_M", 16: "Q5_K_S",
		17: "Q5_K_M", 18: "Q6_K", 19: "IQ2_XXS", 20: "IQ2_XS", 21: "Q2_K_S", 22: "IQ3_XS", 23: "IQ3_XXS",
		24: "IQ1_S", 25: "IQ4_NL", 26: "IQ3_S", 27: "IQ3_M", 28: "IQ2_S", 29: "IQ2_M", 30: "IQ4_XS",
		31: "IQ1_M", 32: "BF16", 36: "TQ1_0", 37: "TQ2_0", 38: "MXFP4"}
	if n, ok := names[t]; ok {
		return n
	}
	return fmt.Sprintf("type %d", t)
}

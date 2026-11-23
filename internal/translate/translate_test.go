package translate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func rec(name, fileType, from, to, version, filter string) Record {
	r := Record{Name: name, FileType: fileType, SourceLanguage: from, TargetLanguage: to, Version: version, FilterExpression: filter,
		DecompressedHash: strings.Repeat("d", 64), DecompressedSize: 100}
	r.Attachment.Hash = strings.Repeat("a", 64)
	r.Attachment.Size = 40
	r.Attachment.Filename = name + ".zst"
	r.Attachment.Location = "main-workspace/translations-models-v2/" + from + to + fileType + version + ".zst"
	return r
}

func TestResources(t *testing.T) {
	records := []Record{
		// en→es: 3.1 complete, 3.0 complete, 4.0a1 pre-release (ignored).
		rec("model.enes.intgemm.alphas.bin", "model", "en", "es", "3.0", ""),
		rec("lex.50.50.enes.s2t.bin", "lex", "en", "es", "3.0", ""),
		rec("vocab.enes.spm", "vocab", "en", "es", "3.0", ""),
		rec("model.enes.intgemm8.bin", "model", "en", "es", "3.1", ""),
		rec("lex.50.50.enes.s2t.bin", "lex", "en", "es", "3.1", ""),
		rec("vocab.enes.spm", "vocab", "en", "es", "3.1", ""),
		rec("model.enes.intgemm.alphas.bin", "model", "en", "es", "4.0a1", "env.channel == 'nightly'"),
		// es→en: only Android (ignored) and an incomplete 3.0.
		rec("model.esen.intgemm.alphas.bin", "model", "es", "en", "3.0", "env.appinfo.OS == 'Android'"),
		rec("lex.50.50.esen.s2t.bin", "lex", "es", "en", "3.0", "env.appinfo.OS == 'Android'"),
		rec("vocab.esen.spm", "vocab", "es", "en", "3.0", "env.appinfo.OS == 'Android'"),
		rec("model.esen.intgemm.alphas.bin", "model", "es", "en", "3.0", ""),
		// ja: separate source and target vocabularies, both directions.
		rec("model.enja.intgemm.alphas.bin", "model", "en", "ja", "3.0", ""),
		rec("lex.50.50.enja.s2t.bin", "lex", "en", "ja", "3.0", ""),
		rec("srcvocab.enja.spm", "srcvocab", "en", "ja", "3.0", ""),
		rec("trgvocab.enja.spm", "trgvocab", "en", "ja", "3.0", ""),
		rec("model.jaen.intgemm.alphas.bin", "model", "ja", "en", "3.0", "env.appinfo.OS != 'Android'"),
		rec("lex.50.50.jaen.s2t.bin", "lex", "ja", "en", "3.0", "env.appinfo.OS != 'Android'"),
		rec("srcvocab.jaen.spm", "srcvocab", "ja", "en", "3.0", "env.appinfo.OS != 'Android'"),
		rec("trgvocab.jaen.spm", "trgvocab", "ja", "en", "3.0", "env.appinfo.OS != 'Android'"),
	}
	res, err := (&Provider{}).Resources(records)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("resources: %+v", res)
	}
	byID := map[string]int{}
	for i, r := range res {
		byID[r.ID] = i
		if err := r.Validate(); err != nil {
			t.Fatalf("%s invalid: %v", r.ID, err)
		}
	}
	es := res[byID["translations.es"]]
	if es.Title != "Translation: English → Spanish" || es.Version != "3.1" || len(es.Files) != 3 || es.Dependencies[0] != EngineResourceID {
		t.Fatalf("es: %+v", es)
	}
	m, err := Manifest(es)
	if err != nil || m[0].Gemm != "int8shiftAll" || m[0].Name != "model.enes.zst" || m[2].Name != "vocab.enes.zst" {
		t.Fatalf("es manifest: %+v %v", m, err)
	}
	ja := res[byID["translations.ja"]]
	if ja.Title != "Translation: Japanese ↔ English" || len(ja.Files) != 8 || ja.ProviderRef["directions"] != "en-ja,ja-en" {
		t.Fatalf("ja: %+v", ja)
	}
	if !strings.HasPrefix(ja.Files[0].Mirrors[0].URL, DefaultAttachmentBase) {
		t.Fatalf("mirror: %s", ja.Files[0].Mirrors[0].URL)
	}
	// An attachment outside the CDN is refused.
	bad := append([]Record(nil), records[11:15]...)
	bad[0].Attachment.Location = "https://evil.example/x.zst"
	res, _ = (&Provider{}).Resources(bad)
	if len(res) != 0 {
		t.Fatalf("foreign attachment accepted: %+v", res)
	}
}

func TestVerifierAndDecompress(t *testing.T) {
	dir := t.TempDir()
	content := bytes.Repeat([]byte("bergamot "), 5000)
	enc, _ := zstd.NewWriter(nil)
	p := filepath.Join(dir, "model.enes.zst")
	os.WriteFile(p, enc.EncodeAll(content, nil), 0o644)
	sum := sha256.Sum256(content)
	var v Verifier
	if err := v.Check(p, hex.EncodeToString(sum[:]), int64(len(content))); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if n, err := Decompress(p, &out); err != nil || n != int64(len(content)) || !bytes.Equal(out.Bytes(), content) {
		t.Fatalf("decompress: %d %v", n, err)
	}
	if err := v.Check(p, strings.Repeat("0", 64), 0); !errors.Is(err, ErrDamaged) {
		t.Fatalf("wrong hash accepted: %v", err)
	}
	os.WriteFile(p, []byte("not zstd"), 0o644)
	if err := (&Verifier{}).Check(p, hex.EncodeToString(sum[:]), 0); !errors.Is(err, ErrDamaged) {
		t.Fatalf("garbage accepted: %v", err)
	}
}

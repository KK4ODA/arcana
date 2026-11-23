package translate

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/klauspost/compress/zstd"
)

// ErrDamaged means a file does not decompress to the content Mozilla
// published.
var ErrDamaged = errors.New("the file does not match Mozilla's published content")

// Verifier remembers which compressed files were checked against their
// decompressed hash, so each is checked once per run.
type Verifier struct {
	mu   sync.Mutex
	seen map[string]string // key → "" ok, or error text
}

func fileKey(p string) (string, error) {
	st, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s|%d|%d", p, st.Size(), st.ModTime().UnixNano()), nil
}

// Check decompresses p and compares the result with want (SHA-256) and
// size (if positive).
func (v *Verifier) Check(p, want string, size int64) error {
	key, err := fileKey(p)
	if err != nil {
		return err
	}
	// The expected content is part of the key: a result only answers
	// the question that was asked.
	key += "|" + want + "|" + fmt.Sprint(size)
	v.mu.Lock()
	if v.seen == nil {
		v.seen = map[string]string{}
	}
	if msg, ok := v.seen[key]; ok {
		v.mu.Unlock()
		if msg != "" {
			return fmt.Errorf("%w: %s", ErrDamaged, msg)
		}
		return nil
	}
	v.mu.Unlock()
	h := sha256.New()
	n, err := Decompress(p, h)
	msg := ""
	switch {
	case err != nil:
		msg = err.Error()
	case hex.EncodeToString(h.Sum(nil)) != want:
		msg = "decompressed SHA-256 differs"
	case size > 0 && n != size:
		msg = fmt.Sprintf("decompressed size %d, expected %d", n, size)
	}
	v.mu.Lock()
	v.seen[key] = msg
	v.mu.Unlock()
	if msg != "" {
		return fmt.Errorf("%w: %s", ErrDamaged, msg)
	}
	return nil
}

// Decompress writes the zstd-decompressed content of p to w.
func Decompress(p string, w io.Writer) (int64, error) {
	f, err := os.Open(p)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	d, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(512<<20))
	if err != nil {
		return 0, err
	}
	defer d.Close()
	return io.Copy(w, d)
}

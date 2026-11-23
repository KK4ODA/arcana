package routing

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/catalog"
)

// InstallRequest describes one road network installation.
type InstallRequest struct {
	Resource  catalog.Resource
	Out       string
	Client    *http.Client
	UserAgent string
	// Planned is told the extract's size once known; it returns an error
	// when the drive cannot hold it.
	Planned func(size int64) error
	// Progress reports bytes of the extract downloaded so far.
	Progress func(done int64)
	Log      *slog.Logger
}

// ErrChecksum means the downloaded extract did not match its published
// checksum.
var ErrChecksum = errors.New("the downloaded road data did not match the checksum its publisher lists; it has been discarded and will be downloaded again")

// building lets one network be built at a time: building is memory-heavy.
var building = make(chan struct{}, 1)

type sourceState struct {
	Source string `json:"source"`
	MD5    string `json:"md5"`
	// Validator is the ETag or Last-Modified of the partial download.
	Validator string `json:"validator"`
}

var md5Re = regexp.MustCompile(`^[0-9a-fA-F]{32}\b`)

// Install downloads the resource's OSM extract (resuming a partial one),
// checks it against the publisher's MD5, builds the road network into
// req.Out and removes the extract. It returns the network file's size and
// SHA-256.
func Install(ctx context.Context, req InstallRequest) (int64, string, error) {
	ref := req.Resource.ProviderRef
	if ref["method"] != MethodBuild {
		return 0, "", errors.New("not a road network resource")
	}
	src, sumURL := ref["source"], ref["md5"]
	for _, u := range []string{src, sumURL} {
		if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
			return 0, "", errors.New("road data source must be an HTTP(S) URL")
		}
	}
	log := req.Log
	if log == nil {
		log = slog.Default()
	}
	pbf := req.Out + ".osm.pbf"
	statePath := req.Out + ".source.json"

	want, err := fetchMD5(ctx, req, sumURL)
	if err != nil {
		return 0, "", err
	}
	var st sourceState
	if b, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Source != src || !strings.EqualFold(st.MD5, want) {
		// A new extract was published since the partial download began.
		os.Remove(pbf)
		st = sourceState{Source: src, MD5: want}
	}
	if err := download(ctx, req, src, pbf, &st, statePath); err != nil {
		return 0, "", err
	}
	got, err := fileMD5(pbf)
	if err != nil {
		return 0, "", err
	}
	if !strings.EqualFold(got, want) {
		os.Remove(pbf)
		os.Remove(statePath)
		return 0, "", ErrChecksum
	}

	select {
	case building <- struct{}{}:
		defer func() { <-building }()
	case <-ctx.Done():
		return 0, "", ctx.Err()
	}
	start := time.Now()
	g, stats, err := Build(ctx, func() (io.ReadCloser, int64, error) {
		f, err := os.Open(pbf)
		if err != nil {
			return nil, 0, err
		}
		fi, _ := f.Stat()
		return f, fi.Size(), nil
	}, BuildOptions{Source: fmt.Sprintf("%s (MD5 %s, downloaded %s)", src, want, time.Now().UTC().Format("2006-01-02"))})
	if err != nil {
		return 0, "", fmt.Errorf("building the road network: %w", err)
	}
	log.Info("built road network", "resource", req.Resource.ID, "stats", stats.String(), "took", time.Since(start).Round(time.Second))

	f, err := os.Create(req.Out)
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	n, err := g.WriteTo(io.MultiWriter(f, h))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(req.Out)
		return 0, "", err
	}
	// The network is written; the extract is no longer needed.
	os.Remove(pbf)
	os.Remove(statePath)
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func get(ctx context.Context, req InstallRequest, u string, header http.Header) (*http.Response, error) {
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		hr.Header[k] = v
	}
	if req.UserAgent != "" {
		hr.Header.Set("User-Agent", req.UserAgent)
	}
	client := req.Client
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(hr)
}

func fetchMD5(ctx context.Context, req InstallRequest, u string) (string, error) {
	resp, err := get(ctx, req, u, nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("road data checksum: %s", resp.Status)
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	m := md5Re.Find([]byte(strings.TrimSpace(string(b))))
	if m == nil {
		return "", errors.New("road data checksum file is not in the expected format")
	}
	return strings.ToLower(string(m)), nil
}

// download fetches src into path, resuming from what is already there when
// the server still has the same file.
func download(ctx context.Context, req InstallRequest, src, path string, st *sourceState, statePath string) error {
	saveState := func() {
		b, _ := json.Marshal(st)
		_ = os.WriteFile(statePath, b, 0o644)
	}
	{
		var have int64
		if fi, err := os.Stat(path); err == nil {
			have = fi.Size()
		}
		h := http.Header{}
		if have > 0 && st.Validator != "" {
			h.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
			h.Set("If-Range", st.Validator)
		} else {
			have = 0
		}
		resp, err := get(ctx, req, src, h)
		if err != nil {
			return err
		}
		var total int64
		flags := os.O_CREATE | os.O_WRONLY
		switch {
		case resp.StatusCode == http.StatusPartialContent && have > 0:
			total = have + resp.ContentLength
			flags |= os.O_APPEND
		case resp.StatusCode == http.StatusOK:
			have = 0
			total = resp.ContentLength
			flags |= os.O_TRUNC
		case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && have > 0:
			// Already complete.
			resp.Body.Close()
			if req.Progress != nil {
				req.Progress(have)
			}
			return nil
		default:
			resp.Body.Close()
			return fmt.Errorf("road data download: %s", resp.Status)
		}
		if v := resp.Header.Get("ETag"); v != "" && !strings.HasPrefix(v, "W/") {
			st.Validator = v
		} else {
			st.Validator = resp.Header.Get("Last-Modified")
		}
		saveState()
		if total > 0 && req.Planned != nil {
			if err := req.Planned(total); err != nil {
				resp.Body.Close()
				return err
			}
		}
		f, err := os.OpenFile(path, flags, 0o644)
		if err != nil {
			resp.Body.Close()
			return err
		}
		done := have
		buf := make([]byte, 256<<10)
		var copyErr error
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := f.Write(buf[:n]); werr != nil {
					copyErr = werr
					break
				}
				done += int64(n)
				if req.Progress != nil {
					req.Progress(done)
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				copyErr = rerr
				break
			}
		}
		resp.Body.Close()
		if err := f.Close(); copyErr == nil {
			copyErr = err
		}
		if copyErr != nil {
			return copyErr
		}
		if total > 0 && done != total {
			return fmt.Errorf("road data download ended early (%d of %d bytes)", done, total)
		}
		return nil
	}
}

func fileMD5(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

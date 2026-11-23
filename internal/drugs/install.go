package drugs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/catalog"
)

// MethodBuild marks the resource built from openFDA's label downloads.
const MethodBuild = "openfda-labels"

// Format is the catalog format of the built reference.
const Format = "druglabels"

// ResourceID is the drug label reference's catalog ID.
const ResourceID = "openfda.drug-labels"

// DefaultIndexURL lists openFDA's downloadable datasets.
const DefaultIndexURL = "https://api.fda.gov/download.json"

// dbPerZipByte is the measured ratio of the built database to the
// downloaded zips (0.59 on the first 20 000 labels of the 2026-11-20
// export); rounded up for space checks.
const dbPerZipByte = 0.65

// Partition is one downloadable part of the label dataset.
type Partition struct {
	File    string `json:"file"`
	SizeMB  string `json:"size_mb"`
	Records int    `json:"records"`
}

// Bytes is the partition's approximate size.
func (p Partition) Bytes() int64 {
	f, _ := strconv.ParseFloat(p.SizeMB, 64)
	return int64(f * 1_000_000)
}

// Index is the drug label section of openFDA's download index.
type Index struct {
	Export     string      `json:"export_date"`
	Total      int         `json:"total_records"`
	Partitions []Partition `json:"partitions"`
}

// ParseIndex reads download.json and validates the label partitions:
// HTTPS addresses on an fda.gov host only.
func ParseIndex(data []byte) (Index, error) {
	var doc struct {
		Results struct {
			Drug struct {
				Label Index `json:"label"`
			} `json:"drug"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return Index{}, fmt.Errorf("openFDA index: %w", err)
	}
	ix := doc.Results.Drug.Label
	if len(ix.Partitions) == 0 || len(ix.Partitions) > 200 {
		return Index{}, errors.New("openFDA index lists no drug label files")
	}
	if len(ix.Export) != 10 || ix.Export[4] != '-' || ix.Export[7] != '-' {
		return Index{}, fmt.Errorf("openFDA index has an unexpected export date %q", ix.Export)
	}
	for _, p := range ix.Partitions {
		u, err := url.Parse(p.File)
		if err != nil || u.Scheme != "https" || !(u.Hostname() == "fda.gov" || strings.HasSuffix(u.Hostname(), ".fda.gov") ||
			(allowTestHost != "" && u.Host == allowTestHost)) {
			return Index{}, fmt.Errorf("openFDA index lists an unexpected file address %q", p.File)
		}
	}
	return ix, nil
}

// allowTestHost lets tests serve partitions from a local server.
var allowTestHost string

// TotalBytes is the size of all partitions.
func (ix Index) TotalBytes() int64 {
	var n int64
	for _, p := range ix.Partitions {
		n += p.Bytes()
	}
	return n
}

// ErrSourceChanged means openFDA published a new export since the catalog
// was refreshed; the job must restart with the new version.
var ErrSourceChanged = errors.New("openFDA published a newer export")

// InstallRequest describes one build.
type InstallRequest struct {
	Resource  catalog.Resource
	Out       string
	Client    *http.Client
	UserAgent string
	Planned   func(int64) error
	Progress  func(int64)
	Log       *slog.Logger
}

type partState struct {
	Validator string `json:"validator,omitempty"`
	Size      int64  `json:"size"`
	Done      bool   `json:"done"`
}

type installState struct {
	Export string               `json:"export"`
	Parts  map[string]partState `json:"parts"`
}

var building = make(chan struct{}, 1)

// Install downloads every partition (resuming across interruptions),
// builds the reference at req.Out and returns its size and SHA-256.
func Install(ctx context.Context, req InstallRequest) (int64, string, error) {
	ref := req.Resource.ProviderRef
	if ref["method"] != MethodBuild {
		return 0, "", errors.New("not an openFDA label resource")
	}
	log := req.Log
	if log == nil {
		log = slog.Default()
	}
	indexURL := ref["index"]
	if !strings.HasPrefix(indexURL, "https://") {
		return 0, "", errors.New("openFDA index must be an HTTPS address")
	}
	data, err := getBytes(ctx, req, indexURL, 16<<20)
	if err != nil {
		return 0, "", err
	}
	ix, err := ParseIndex(data)
	if err != nil {
		return 0, "", err
	}
	if ix.Export != req.Resource.Version {
		return 0, "", fmt.Errorf("%w (%s, catalog has %s)", ErrSourceChanged, ix.Export, req.Resource.Version)
	}
	statePath := req.Out + ".parts.json"
	var st installState
	if b, err := os.ReadFile(statePath); err == nil {
		_ = json.Unmarshal(b, &st)
	}
	if st.Export != ix.Export || st.Parts == nil {
		st = installState{Export: ix.Export, Parts: map[string]partState{}}
	}
	save := func() {
		b, _ := json.Marshal(st)
		_ = os.WriteFile(statePath, b, 0o644)
	}
	total := ix.TotalBytes()
	if req.Planned != nil {
		// Room for the downloads and the database built from them, which
		// exist together until the build finishes; then report the
		// download alone, which is what the progress counts.
		if err := req.Planned(total + int64(float64(total)*dbPerZipByte)); err != nil {
			return 0, "", err
		}
		if err := req.Planned(total); err != nil {
			return 0, "", err
		}
	}
	var zips []string
	var done int64
	for i, p := range ix.Partitions {
		path := fmt.Sprintf("%s.part%03d.zip", req.Out, i+1)
		zips = append(zips, path)
		ps := st.Parts[p.File]
		if ps.Done {
			if fi, err := os.Stat(path); err == nil && fi.Size() == ps.Size {
				done += ps.Size
				report(req, done)
				continue
			}
			ps = partState{}
		}
		n, err := fetchPart(ctx, req, p.File, path, &ps, done, func() { st.Parts[p.File] = ps; save() })
		if err != nil {
			return 0, "", fmt.Errorf("openFDA part %d of %d: %w", i+1, len(ix.Partitions), err)
		}
		ps.Done, ps.Size = true, n
		st.Parts[p.File] = ps
		save()
		done += n
	}

	select {
	case building <- struct{}{}:
		defer func() { <-building }()
	case <-ctx.Done():
		return 0, "", ctx.Err()
	}
	start := time.Now()
	stats, err := Build(ctx, zips, req.Out, map[string]string{
		"export": ix.Export, "source": indexURL, "records": strconv.Itoa(ix.Total),
		"license": "CC0-1.0", "publisher": "U.S. Food and Drug Administration (openFDA)",
	})
	var pe *PartError
	if errors.As(err, &pe) {
		// A damaged part is downloaded again on the next attempt.
		for file, ps := range st.Parts {
			if fmt.Sprintf("%s.part%03d.zip", req.Out, indexOf(ix, file)+1) == pe.Path {
				ps.Done = false
				st.Parts[file] = ps
			}
		}
		os.Remove(pe.Path)
		save()
		return 0, "", err
	}
	if err != nil {
		return 0, "", fmt.Errorf("building the drug label reference: %w", err)
	}
	log.Info("built drug label reference", "export", ix.Export, "labels", stats.Labels, "distinct", stats.Docs,
		"skipped", stats.Skipped, "took", time.Since(start).Round(time.Second))
	f, err := os.Open(req.Out)
	if err != nil {
		return 0, "", err
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	f.Close()
	if err != nil {
		return 0, "", err
	}
	for _, z := range zips {
		os.Remove(z)
	}
	os.Remove(statePath)
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

func indexOf(ix Index, file string) int {
	for i, p := range ix.Partitions {
		if p.File == file {
			return i
		}
	}
	return -1
}

func report(req InstallRequest, n int64) {
	if req.Progress != nil {
		req.Progress(n)
	}
}

func request(ctx context.Context, req InstallRequest, u string, h http.Header) (*http.Response, error) {
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range h {
		hr.Header[k] = v
	}
	if req.UserAgent != "" {
		hr.Header.Set("User-Agent", req.UserAgent)
	}
	c := req.Client
	if c == nil {
		c = http.DefaultClient
	}
	return c.Do(hr)
}

func getBytes(ctx context.Context, req InstallRequest, u string, max int64) ([]byte, error) {
	resp, err := request(ctx, req, u, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openFDA index: %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if int64(len(b)) > max {
		return nil, errors.New("openFDA index is unexpectedly large")
	}
	return b, err
}

// fetchPart downloads one partition, resuming a partial file when the
// server still has the same version (ETag or Last-Modified).
func fetchPart(ctx context.Context, req InstallRequest, src, path string, ps *partState, base int64, persist func()) (int64, error) {
	var have int64
	if fi, err := os.Stat(path); err == nil {
		have = fi.Size()
	}
	h := http.Header{}
	if have > 0 && ps.Validator != "" {
		h.Set("Range", "bytes="+strconv.FormatInt(have, 10)+"-")
		h.Set("If-Range", ps.Validator)
	} else {
		have = 0
	}
	resp, err := request(ctx, req, src, h)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch {
	case resp.StatusCode == http.StatusPartialContent && have > 0:
		flags |= os.O_APPEND
	case resp.StatusCode == http.StatusOK:
		have = 0
		flags |= os.O_TRUNC
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && have > 0:
		return have, nil
	default:
		return 0, fmt.Errorf("download: %s", resp.Status)
	}
	if v := resp.Header.Get("ETag"); v != "" && !strings.HasPrefix(v, "W/") {
		ps.Validator = v
	} else {
		ps.Validator = resp.Header.Get("Last-Modified")
	}
	persist()
	want := int64(-1)
	if resp.ContentLength >= 0 {
		want = have + resp.ContentLength
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return 0, err
	}
	done := have
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				return 0, werr
			}
			done += int64(n)
			report(req, base+done)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return 0, rerr
		}
	}
	if err := f.Close(); err != nil {
		return 0, err
	}
	if want >= 0 && done != want {
		return 0, fmt.Errorf("download ended early (%d of %d bytes)", done, want)
	}
	return done, nil
}

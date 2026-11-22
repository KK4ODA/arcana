package maps

import (
	"compress/gzip"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/KK4ODA/arcana/internal/catalog"
)

// ErrSourceGone means the planet build a resumed extraction was reading is no
// longer published; the download must be resolved again.
var ErrSourceGone = errors.New("the map build being extracted is no longer available; restarting from the newest build")

// InstallRequest describes one pmtiles-extract installation.
type InstallRequest struct {
	Resource  catalog.Resource
	Out       string
	Client    *http.Client
	UserAgent string
	// Planned is told the exact output size once known; returning an error
	// (for example, not enough space) stops before tile data is fetched.
	Planned func(size int64) error
	// Progress reports bytes of the output written so far.
	Progress func(done int64)
}

type savedPlan struct {
	Source string
	Plan   Plan
}

// Install builds the resource's archive at req.Out by extracting its area
// from the remote planet archive. Planning is cached next to the output so a
// resumed installation does not fetch directories again; tile data resumes
// from Extract's own checkpoint. It returns the output size and SHA-256.
func Install(ctx context.Context, req InstallRequest) (int64, string, error) {
	ref := req.Resource.ProviderRef
	if ref["method"] != MethodExtract {
		return 0, "", errors.New("not a map extraction resource")
	}
	b, err := ParseBBox(ref["bbox"])
	if err != nil {
		return 0, "", fmt.Errorf("map area: %w", err)
	}
	minz, err1 := strconv.ParseUint(ref["minzoom"], 10, 8)
	maxz, err2 := strconv.ParseUint(ref["maxzoom"], 10, 8)
	if err1 != nil || err2 != nil || minz > maxz || maxz > 15 {
		return 0, "", errors.New("map zoom range is invalid")
	}
	source := ref["source"]
	if !strings.HasPrefix(source, "https://") && !strings.HasPrefix(source, "http://") {
		return 0, "", errors.New("map source must be an HTTP(S) URL")
	}
	rr := &HTTPRangeReader{URL: source, Client: req.Client, UserAgent: req.UserAgent}
	planPath := req.Out + ".plan"
	statePath := req.Out + ".state.json"
	reset := func() {
		os.Remove(planPath)
		os.Remove(statePath)
		os.Remove(req.Out)
	}

	var p *Plan
	if sp, err := loadPlan(planPath); err == nil && sp.Source == source && sp.Plan.BBox == b &&
		sp.Plan.MaxZoom <= uint8(maxz) && sp.Plan.ETag != "" {
		p = &sp.Plan
		rr.SetETag(p.ETag)
	} else {
		p, err = MakePlan(ctx, rr, b, uint8(minz), uint8(maxz), nil)
		if err != nil {
			return 0, "", classifySourceErr(err)
		}
		if p.ETag == "" {
			// Without a version marker a resumed extraction could mix
			// two builds; plan again on every attempt instead.
			os.Remove(planPath)
		} else if err := savePlan(planPath, savedPlan{Source: source, Plan: *p}); err != nil {
			return 0, "", err
		}
	}
	size := int64(p.OutputSize())
	if req.Planned != nil {
		if err := req.Planned(size); err != nil {
			return 0, "", err
		}
	}
	prefix := size - int64(p.TileBytes)
	sha, err := Extract(ctx, rr, p, req.Out, func(done, total int64) {
		if req.Progress != nil {
			req.Progress(prefix + done)
		}
	})
	if err != nil {
		err = classifySourceErr(err)
		if errors.Is(err, ErrSourceChanged) || errors.Is(err, ErrSourceGone) {
			reset()
		}
		return 0, "", err
	}
	os.Remove(planPath)
	// Check the result reads back as a valid archive before it is published.
	a, err := Open(req.Out)
	if err != nil {
		reset()
		return 0, "", fmt.Errorf("extracted map is not readable: %w", err)
	}
	ok := a.Header.TileEntries > 0 || p.Tiles == 0
	a.Close()
	if !ok {
		reset()
		return 0, "", errors.New("extracted map has no tiles")
	}
	return size, sha, nil
}

// classifySourceErr maps "the build was deleted" to ErrSourceGone.
func classifySourceErr(err error) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	if strings.Contains(s, "404 Not Found") || strings.Contains(s, "410 Gone") {
		return fmt.Errorf("%w (%v)", ErrSourceGone, err)
	}
	return err
}

func savePlan(path string, sp savedPlan) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(f)
	if err := gob.NewEncoder(zw).Encode(sp); err != nil {
		f.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadPlan(path string) (savedPlan, error) {
	var sp savedPlan
	f, err := os.Open(path)
	if err != nil {
		return sp, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return sp, err
	}
	err = gob.NewDecoder(zr).Decode(&sp)
	return sp, err
}

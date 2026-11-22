package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/download"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/maps"
)

// mapState holds the map service, place index and indexing status.
type mapState struct {
	service *maps.Service
	places  *maps.Gazetteer
	wake    chan struct{}

	mu       sync.Mutex
	indexing string // map set currently being indexed
	progress float64
	lastErr  string
}

// MapProvider returns the configured map region provider, or nil when maps
// are disabled.
func (a *App) MapProvider() *maps.Provider {
	s := a.Lib.Settings.Get().Maps
	if !s.Enabled {
		return nil
	}
	return &maps.Provider{BuildsURL: s.BuildsURL, BaseURL: s.BaseURL}
}

// Maps returns the merged tile service.
func (a *App) Maps() *maps.Service { return a.maps.service }

// openMaps prepares the map service and place index.
func (a *App) openMaps() error {
	a.maps = &mapState{service: maps.NewService(), wake: make(chan struct{}, 1)}
	path := filepath.Join(a.Lib.Root, "index", "places.db")
	if a.Lib.ReadOnly {
		// The place index is a rebuildable cache; keep it off a
		// read-only drive.
		sum := sha256.Sum256([]byte(a.Lib.Root))
		path = filepath.Join(os.TempDir(), "arcana-places-"+hex.EncodeToString(sum[:6])+".db")
	} else if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	g, err := maps.OpenGazetteer(path)
	if err != nil {
		// A damaged cache is simply rebuilt.
		os.Remove(path)
		os.Remove(path + "-wal")
		os.Remove(path + "-shm")
		if g, err = maps.OpenGazetteer(path); err != nil {
			return err
		}
	}
	a.maps.places = g
	return nil
}

func (a *App) closeMaps() {
	if a.maps == nil {
		return
	}
	a.maps.service.Close()
	if a.maps.places != nil {
		a.maps.places.Close()
	}
}

// mapExtractor adapts maps.Install to the download manager.
func (a *App) mapExtractor(ctx context.Context, req download.ExtractRequest) (int64, string, error) {
	size, sum, err := maps.Install(ctx, maps.InstallRequest{
		Resource: req.Resource, Out: req.Out, Client: req.Client, UserAgent: catalog.UserAgent(),
		Planned: req.Planned, Progress: req.Progress,
	})
	if errors.Is(err, maps.ErrSourceChanged) || errors.Is(err, maps.ErrSourceGone) {
		err = fmt.Errorf("%w: %v", download.ErrRestart, err)
	}
	return size, sum, err
}

// reloadMaps opens every installed map archive.
func (a *App) reloadMaps() {
	if a.maps == nil {
		return
	}
	installed, err := a.Store.ListInstalled(context.Background())
	if err != nil {
		a.Log.Error("list installed", "err", err)
		return
	}
	var specs []maps.SetSpec
	for _, in := range installed {
		if in.Resource.Format != "pmtiles" {
			continue
		}
		for _, f := range in.Files {
			p, err := a.Content.FilePath(in, f)
			if err != nil {
				continue
			}
			specs = append(specs, maps.SetSpec{ResourceID: in.ResourceID, Title: in.Resource.Title, Version: in.Version, Path: p})
		}
	}
	a.maps.service.Load(specs)
	select {
	case a.maps.wake <- struct{}{}:
	default:
	}
}

// placesLoop keeps the place-name index in step with installed maps.
func (a *App) placesLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.maps.wake:
		}
		a.indexPlaces(ctx)
	}
}

func (a *App) indexPlaces(ctx context.Context) {
	g := a.maps.places
	indexed := g.Indexed(ctx)
	present := map[string]bool{}
	for _, set := range a.maps.service.Sets() {
		present[set.ResourceID] = true
		if set.Error != "" || indexed[set.ResourceID] == set.Version {
			continue
		}
		ar := a.maps.service.Archive(set.ResourceID)
		if ar == nil {
			continue
		}
		a.maps.mu.Lock()
		a.maps.indexing, a.maps.progress = set.Title, 0
		a.maps.mu.Unlock()
		start := time.Now()
		n, err := g.IndexArchive(ctx, set.ResourceID, set.Version, ar, func(done, total int) {
			a.maps.mu.Lock()
			if total > 0 {
				a.maps.progress = float64(done) / float64(total)
			}
			a.maps.mu.Unlock()
		})
		a.maps.mu.Lock()
		a.maps.indexing, a.maps.progress = "", 0
		if err != nil && ctx.Err() == nil {
			a.maps.lastErr = set.Title + ": " + err.Error()
		}
		a.maps.mu.Unlock()
		if err != nil {
			a.Log.Warn("index places", "map", set.ResourceID, "err", err)
			continue
		}
		a.Log.Info("indexed places", "map", set.ResourceID, "places", n, "took", time.Since(start).Round(time.Millisecond))
	}
	for id := range indexed {
		if !present[id] {
			_ = g.Remove(ctx, id)
		}
	}
}

// MapStatus describes installed maps for the UI.
type MapStatus struct {
	Sets     []maps.SetInfo `json:"sets"`
	Indexing string         `json:"indexing,omitempty"`
	Progress float64        `json:"progress,omitempty"`
	Error    string         `json:"error,omitempty"`
	MaxZoom  uint8          `json:"maxZoom"`
	Enabled  bool           `json:"enabled"`
}

// MapStatus reports loaded map sets and indexing progress.
func (a *App) MapStatus() MapStatus {
	st := MapStatus{Sets: a.maps.service.Sets(), MaxZoom: a.maps.service.MaxZoom(), Enabled: a.Lib.Settings.Get().Maps.Enabled}
	a.maps.mu.Lock()
	st.Indexing, st.Progress, st.Error = a.maps.indexing, a.maps.progress, a.maps.lastErr
	a.maps.mu.Unlock()
	return st
}

// SearchPlaces searches the offline place index.
func (a *App) SearchPlaces(ctx context.Context, q string, limit int) ([]maps.Place, error) {
	return a.maps.places.Search(ctx, q, limit)
}

// loadMapCatalog makes map regions browsable on a library that has never
// refreshed the map provider, using the sizes bundled with Arcana.
func (a *App) loadMapCatalog(ctx context.Context, existing map[string]string) error {
	p := a.MapProvider()
	if p == nil || existing[p.ID()] != "" {
		return nil
	}
	res, err := p.Offline()
	if err != nil {
		return err
	}
	rc, _ := maps.Regions()
	measured, err := time.Parse("2006-01-02", rc.Measured)
	if err != nil {
		measured = time.Now()
	}
	valid, errs := catalog.ValidateAll(p.ID(), res)
	if len(errs) > 0 {
		a.Log.Warn("bundled map regions rejected", "count", len(errs), "first", errs[0])
	}
	return a.Store.ReplaceProviderResources(ctx, p.ID(), p.Name(), valid, measured)
}

// AreaPlan is the result of planning a custom map area.
type AreaPlan struct {
	Name     string           `json:"name"`
	BBox     maps.BBox        `json:"bbox"`
	MaxZoom  uint8            `json:"maxZoom"`
	Tiles    int              `json:"tiles"`
	Bytes    int64            `json:"bytes"`
	Build    string           `json:"build"`
	Resource catalog.Resource `json:"resource"`
}

// maxCustomTargets bounds how many tile positions a custom-area plan may
// examine, so planning stays quick; larger areas should use a region.
const maxCustomTargets = 6_000_000

// PlanArea measures a custom map area against the newest planet build.
// Only directories are downloaded; the size returned is exact.
func (a *App) PlanArea(ctx context.Context, name string, b maps.BBox, maxzoom uint8) (AreaPlan, error) {
	p := a.MapProvider()
	if p == nil {
		return AreaPlan{}, errors.New("maps are disabled in Settings")
	}
	if !b.Valid() || maxzoom < 6 || maxzoom > 15 {
		return AreaPlan{}, errors.New("choose an area and a detail level between 6 and 15")
	}
	if maps.TileCount(b, 0, maxzoom) > maxCustomTargets {
		return AreaPlan{}, maps.ErrTooManyTiles
	}
	if name == "" {
		name = "Custom area"
	}
	if len(name) > 80 {
		name = name[:80]
	}
	build, err := p.LatestBuild(ctx, a.Fetcher)
	if err != nil {
		return AreaPlan{}, fmt.Errorf("find the newest map build: %w", err)
	}
	src := p.SourceURL(build)
	client := download.NewClient(func() bool { return a.Lib.Settings.Get().AllowPrivateNetworkDownloads })
	rr := &maps.HTTPRangeReader{URL: src, Client: client, UserAgent: catalog.UserAgent()}
	plan, err := maps.MakePlan(ctx, rr, b, 0, maxzoom, nil)
	if err != nil {
		return AreaPlan{}, err
	}
	size := int64(plan.OutputSize())
	res, err := p.CustomArea(build, name, b, maxzoom, uint64(size))
	if err != nil {
		return AreaPlan{}, err
	}
	res.Stats["tiles"] = int64(plan.Tiles)
	return AreaPlan{Name: name, BBox: b, MaxZoom: maxzoom, Tiles: plan.Tiles, Bytes: size, Build: build, Resource: res}, nil
}

// QueueArea plans and queues a custom map area.
func (a *App) QueueArea(ctx context.Context, name string, b maps.BBox, maxzoom uint8) (AreaPlan, error) {
	if a.Lib.ReadOnly {
		return AreaPlan{}, library.ErrReadOnly
	}
	plan, err := a.PlanArea(ctx, name, b, maxzoom)
	if err != nil {
		return plan, err
	}
	_, err = a.Downloads.Enqueue(ctx, plan.Resource)
	return plan, err
}

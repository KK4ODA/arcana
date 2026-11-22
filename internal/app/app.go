// Package app wires Arcana's modules together for one opened library.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/KK4ODA/arcana/internal/appupdate"
	"github.com/KK4ODA/arcana/internal/auth"
	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/content"
	"github.com/KK4ODA/arcana/internal/docs"
	"github.com/KK4ODA/arcana/internal/download"
	"github.com/KK4ODA/arcana/internal/fsutil"
	"github.com/KK4ODA/arcana/internal/gps"
	"github.com/KK4ODA/arcana/internal/integration"
	"github.com/KK4ODA/arcana/internal/kiwix"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/maps"
	"github.com/KK4ODA/arcana/internal/storage"
	"github.com/KK4ODA/arcana/internal/store"
	"github.com/KK4ODA/arcana/internal/tasks"
	"github.com/KK4ODA/arcana/internal/userdata"
)

// Options configure an App.
type Options struct {
	// KiwixBinary overrides kiwix-serve discovery.
	KiwixBinary string
	// ForceReadOnly opens the library without modifying it.
	ForceReadOnly bool
	// DisableKiwix skips starting kiwix-serve (used by CLI commands).
	DisableKiwix bool
	Log          *slog.Logger
}

// App is an opened library with its services.
type App struct {
	Lib       *library.Library
	Store     *store.Store
	Content   *content.Manager
	Downloads *download.Manager
	Kiwix     *kiwix.Server
	Log       *slog.Logger
	Fetcher   catalog.Fetcher
	User      *userdata.Store
	Docs      *docs.Index
	Tasks     *tasks.Manager
	Updater   *appupdate.Updater
	reindex   chan struct{}
	maps      *mapState
	GPS       *gps.Manager
	// Admin holds the optional remote-administration password.
	Admin *auth.Store
	// Integrations holds tokens of programs using the integration API.
	Integrations *integration.Store
	events       eventHub
	selfupd      updateState
	// RequestRestart, set by the program's main function, stops Arcana and
	// starts the newly installed version as described by the SelfUpdate
	// taken before installing.
	RequestRestart func(SelfUpdate)
	// executable replaces os.Executable in tests.
	executable func() (string, error)
	runCtx     context.Context

	opts         Options
	refreshMu    sync.Mutex
	kiwixMu      sync.Mutex
	StartupNotes []string
	cancel       context.CancelFunc
}

// Open opens the library at root and prepares its services.
func Open(root string, opts Options) (*App, error) {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	lib, err := library.Open(root, library.OpenOptions{ForceReadOnly: opts.ForceReadOnly})
	if err != nil {
		return nil, err
	}
	a := &App{Lib: lib, Log: log, opts: opts}

	if lib.ReadOnly {
		a.Store, err = store.OpenMemory()
		if err == nil {
			a.note("Read-only: " + lib.ReadOnlyReason + ". Browsing and search work; downloads and settings changes are disabled.")
		}
	} else {
		var recovered bool
		a.Store, recovered, err = store.OpenOrRecover(lib.DB())
		if recovered && err == nil {
			a.note("The metadata index was damaged (for example by unplugging the drive during a write) and has been rebuilt from the drive. No content was lost; the damaged copy was kept in the state folder.")
		}
	}
	if err != nil {
		lib.Close()
		return nil, err
	}
	a.Content = &content.Manager{Layout: lib.Layout, Store: a.Store, Log: log}
	a.Content.OnChange = func() {
		a.reloadKiwix()
		a.reloadMaps()
		a.RequestReindex()
		a.publish(Event{Type: "library"})
	}
	a.reindex = make(chan struct{}, 1)
	a.Tasks = tasks.New(context.Background())
	a.Updater = &appupdate.Updater{Lib: lib, Keys: appupdate.TrustedKeys()}
	if a.User, err = userdata.Open(lib.User(), lib.ReadOnly); err != nil {
		a.Close()
		return nil, err
	}
	if err := a.openDocsIndex(); err != nil {
		a.Close()
		return nil, err
	}
	if err := a.openMaps(); err != nil {
		a.Close()
		return nil, err
	}
	a.openGPS()
	a.Admin = auth.NewStore(lib.User())
	if a.Integrations, err = integration.Open(lib.User(), lib.ReadOnly); err != nil {
		a.Close()
		return nil, err
	}

	ctx := context.Background()
	// Sidecars are the source of truth for installed content; re-index on
	// every start so the index can never drift from the drive.
	problems, err := a.Content.Rebuild(ctx)
	if err != nil {
		a.Close()
		return nil, err
	}
	for _, p := range problems {
		log.Warn("library problem", "resource", p.ResourceID, "kind", p.Kind, "detail", p.Detail)
	}
	if err := a.loadCatalogCaches(ctx); err != nil {
		log.Warn("catalog cache", "err", err)
	}

	client := download.NewClientFor(func() bool { return lib.Settings.Get().AllowPrivateNetworkDownloads }, a.peerAddrAllowed)
	a.Fetcher = catalog.NewHTTPFetcher(client)
	a.Downloads = download.New(download.Options{
		Store:       a.Store,
		Layout:      lib.Layout,
		Content:     a.Content,
		Settings:    lib.Settings.Get,
		ResolverFor: func(p string) catalog.Resolver { return a.resolvers()[p] },
		Fetcher:     a.Fetcher,
		Client:      client,
		Extractors:  map[string]download.Extractor{maps.MethodExtract: a.mapExtractor},
		RequestHook: a.peerRequestHook,
		Log:         log,
		ReadOnly:    lib.ReadOnly,
	})

	if !opts.DisableKiwix {
		bin, err := kiwix.FindBinary(opts.KiwixBinary, lib.Root)
		if err != nil {
			log.Warn("ZIM reader unavailable", "err", err)
			bin = ""
		}
		a.Kiwix = kiwix.New(bin, lib.Root, log)
	}
	return a, nil
}

func (a *App) note(s string) { a.StartupNotes = append(a.StartupNotes, s) }

// Start begins background services.
func (a *App) Start(ctx context.Context) error {
	ctx, a.cancel = context.WithCancel(ctx)
	a.runCtx = ctx
	if err := a.Downloads.Start(ctx); err != nil {
		return err
	}
	a.Tasks = tasks.New(ctx)
	a.reloadKiwix()
	a.reloadMaps()
	go a.placesLoop(ctx)
	a.ApplyGPSSettings()
	go a.reindexLoop(ctx)
	go a.autoRefreshLoop(ctx)
	go a.appUpdateLoop(ctx, 2*time.Minute)
	a.RequestReindex()
	return nil
}

// Close stops services and releases the library. It is safe to call on a
// partially opened App.
func (a *App) Close() {
	if a.cancel != nil {
		a.cancel()
	}
	if a.Downloads != nil {
		a.Downloads.Stop()
	}
	if a.GPS != nil {
		a.GPS.Stop()
	}
	if a.Kiwix != nil {
		a.Kiwix.Stop()
	}
	if a.Docs != nil {
		a.Docs.Close()
	}
	a.closeMaps()
	if a.Store != nil {
		a.Store.Close()
	}
	if a.Lib != nil {
		a.Lib.Close()
	}
}

// reloadKiwix makes kiwix-serve serve the installed ZIM archives.
func (a *App) reloadKiwix() {
	if a.Kiwix == nil {
		return
	}
	a.kiwixMu.Lock()
	defer a.kiwixMu.Unlock()
	installed, err := a.Store.ListInstalled(context.Background())
	if err != nil {
		a.Log.Error("list installed", "err", err)
		return
	}
	var archives []kiwix.Archive
	for _, in := range installed {
		if in.Resource.Format != "zim" {
			continue
		}
		for _, f := range in.Files {
			p, err := a.Content.FilePath(in, f)
			if err != nil || !strings.HasSuffix(strings.ToLower(p), ".zim") {
				continue
			}
			if _, err := os.Stat(p); err != nil {
				continue
			}
			lang := ""
			if len(in.Resource.Languages) > 0 {
				lang = in.Resource.Languages[0]
			}
			archives = append(archives, kiwix.Archive{ResourceID: in.ResourceID, Path: p,
				UUID: in.Resource.ProviderRef["uuid"], Language: lang, Title: in.Resource.Title})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := a.Kiwix.Apply(ctx, archives); err != nil {
		a.Log.Error("start ZIM reader", "err", err)
	}
}

// ---------------------------------------------------------------- catalog

// Providers returns the catalog providers enabled in settings. The bundled
// catalog is always present.
func (a *App) Providers() []catalog.Provider {
	s := a.Lib.Settings.Get()
	ps := []catalog.Provider{catalog.Bundled()}
	if s.Catalog.KiwixEnabled {
		ps = append(ps, &catalog.KiwixProvider{BaseURL: s.Catalog.KiwixURL})
	}
	if s.Catalog.GutenbergEnabled {
		ps = append(ps, &catalog.GutenbergProvider{Mirror: s.Catalog.GutenbergMirror})
	}
	if mp := a.MapProvider(); mp != nil {
		ps = append(ps, mp)
	}
	for _, f := range s.Catalog.Feeds {
		if f.Enabled {
			ps = append(ps, &catalog.FeedProvider{ProviderID: f.ID, ProviderName: f.Name, URL: f.URL, PublicKey: f.PublicKey})
		}
	}
	return ps
}

func (a *App) resolvers() map[string]catalog.Resolver {
	out := map[string]catalog.Resolver{}
	for _, p := range a.Providers() {
		if r, ok := p.(catalog.Resolver); ok {
			out[p.ID()] = r
		}
	}
	return out
}

type catalogCache struct {
	Format    string             `json:"format"`
	Provider  string             `json:"provider"`
	Name      string             `json:"name"`
	FetchedAt time.Time          `json:"fetchedAt"`
	Resources []catalog.Resource `json:"resources"`
}

// loadCatalogCaches imports cached catalogs into the index. The bundled
// catalog is always imported from the binary, so a fresh library can be used
// without ever going online.
func (a *App) loadCatalogCaches(ctx context.Context) error {
	b := catalog.Bundled()
	res, err := b.Fetch(ctx, nil)
	if err != nil {
		return err
	}
	valid, _ := catalog.ValidateAll(b.ID(), res)
	if err := a.Store.ReplaceProviderResources(ctx, b.ID(), b.Name(), valid, time.Now()); err != nil {
		return err
	}
	existing := map[string]string{}
	if ps, err := a.Store.Providers(ctx); err == nil {
		for _, p := range ps {
			existing[p.ID] = p.FetchedAt
		}
	}
	defer func() {
		if err := a.loadMapCatalog(ctx, existing); err != nil {
			a.Log.Warn("map regions", "err", err)
		}
	}()
	entries, err := os.ReadDir(a.Lib.CatalogCache())
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(a.Lib.CatalogCache(), e.Name()))
		if err != nil {
			continue
		}
		var c catalogCache
		if err := json.Unmarshal(data, &c); err != nil || c.Format != "arcana-catalog-cache" || c.Provider == "arcana" {
			continue
		}
		if existing[c.Provider] == c.FetchedAt.UTC().Format(time.RFC3339) {
			continue // index already current
		}
		// The cache lives on the drive and is re-validated like any
		// other untrusted input.
		valid, _ := catalog.ValidateAll(c.Provider, c.Resources)
		if err := a.Store.ReplaceProviderResources(ctx, c.Provider, c.Name, valid, c.FetchedAt); err != nil {
			return err
		}
	}
	return nil
}

// RefreshResult reports one provider refresh.
type RefreshResult struct {
	Provider  string `json:"provider"`
	Name      string `json:"name"`
	Resources int    `json:"resources"`
	Rejected  int    `json:"rejected"`
	Error     string `json:"error,omitempty"`
}

// RefreshCatalog fetches every enabled provider. A provider that fails keeps
// its previous cached entries.
func (a *App) RefreshCatalog(ctx context.Context) ([]RefreshResult, error) {
	if a.Lib.ReadOnly {
		return nil, library.ErrReadOnly
	}
	a.refreshMu.Lock()
	defer a.refreshMu.Unlock()
	var out []RefreshResult
	for _, p := range a.Providers() {
		r := RefreshResult{Provider: p.ID(), Name: p.Name()}
		res, err := p.Fetch(ctx, a.Fetcher)
		if err != nil {
			r.Error = err.Error()
			_ = a.Store.RecordProviderError(ctx, p.ID(), p.Name(), err.Error())
			out = append(out, r)
			continue
		}
		valid, errs := catalog.ValidateAll(p.ID(), res)
		r.Resources, r.Rejected = len(valid), len(errs)
		for i, e := range errs {
			if i < 5 {
				a.Log.Info("catalog entry rejected", "provider", p.ID(), "err", e)
			}
		}
		now := time.Now().UTC().Truncate(time.Second)
		if p.ID() != "arcana" {
			cache := catalogCache{Format: "arcana-catalog-cache", Provider: p.ID(), Name: p.Name(), FetchedAt: now, Resources: valid}
			data, err := json.Marshal(cache)
			if err == nil {
				err = fsutil.WriteFileAtomic(filepath.Join(a.Lib.CatalogCache(), p.ID()+".json"), data, 0o644)
			}
			if err != nil {
				a.Log.Warn("save catalog cache", "err", err)
			}
		}
		if err := a.Store.ReplaceProviderResources(ctx, p.ID(), p.Name(), valid, now); err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}

// ---------------------------------------------------------------- storage

// StorageReport combines the volume, file-system notes and capacity plan.
type StorageReport struct {
	Volume storage.Volume `json:"volume"`
	FS     storage.FSInfo `json:"fs"`
	Plan   storage.Plan   `json:"plan"`
	// ReserveIsDefault is true when the user has not set a reserve.
	ReserveIsDefault bool `json:"reserveIsDefault"`
}

// Storage computes the capacity plan, optionally including a selection of
// catalog resources not yet queued.
func (a *App) Storage(ctx context.Context, selected []string) (StorageReport, error) {
	vol, err := storage.Stat(a.Lib.Root)
	if err != nil {
		return StorageReport{}, err
	}
	installed, err := a.Store.ListInstalled(ctx)
	if err != nil {
		return StorageReport{}, err
	}
	var inst int64
	for _, in := range installed {
		inst += in.TotalSize()
	}
	pending, err := a.Downloads.Pending(ctx)
	if err != nil {
		return StorageReport{}, err
	}
	var sel, uncertain int64
	for _, id := range selected {
		r, err := a.Store.GetResource(ctx, id)
		if err != nil {
			continue
		}
		sel += r.DownloadSize
		if r.SizeEstimated {
			uncertain += r.DownloadSize
		}
	}
	settings := a.Lib.Settings.Get()
	reserve := settings.ReserveBytes
	if reserve <= 0 {
		reserve = library.DefaultReserve(int64(vol.TotalBytes))
	}
	plan := storage.Compute(storage.PlanInput{
		Volume: vol, Installed: inst, Staged: a.Content.StagedBytes(), QueuedRemaining: pending,
		Selected: sel, SelectedUncertain: uncertain, Reserve: reserve,
	})
	return StorageReport{Volume: vol, FS: storage.FileSystemInfo(vol.FSType), Plan: plan,
		ReserveIsDefault: settings.ReserveBytes <= 0}, nil
}

// ---------------------------------------------------------------- updates

// Update describes a newer catalog version of an installed resource.
type Update struct {
	ResourceID       string `json:"resourceId"`
	Title            string `json:"title"`
	InstalledVersion string `json:"installedVersion"`
	InstalledDate    string `json:"installedDate"`
	LatestVersion    string `json:"latestVersion"`
	LatestDate       string `json:"latestDate"`
	DownloadSize     int64  `json:"downloadSize"`
	// Priority is "high" for medical and regulatory material, otherwise
	// "normal".
	Priority string `json:"priority"`
	Queued   bool   `json:"queued"`
}

// Updates compares installed versions with the catalog index. It works
// offline using the last refreshed catalog.
func (a *App) Updates(ctx context.Context) ([]Update, error) {
	installed, err := a.Store.ListInstalled(ctx)
	if err != nil {
		return nil, err
	}
	jobs, _ := a.Store.ListJobs(ctx)
	queued := map[string]string{}
	for _, j := range jobs {
		if j.Active() {
			queued[j.ResourceID] = j.Version
		}
	}
	var out []Update
	for _, in := range installed {
		latest, err := a.Store.GetResource(ctx, in.ResourceID)
		if err != nil || catalog.CompareVersions(latest.Version, in.Version) <= 0 {
			continue
		}
		pr := "normal"
		if latest.Category == catalog.CategoryMedicine || latest.Category == catalog.CategoryFirstAid ||
			latest.Subcategory == "regulations" {
			pr = "high"
		}
		out = append(out, Update{
			ResourceID: in.ResourceID, Title: latest.Title,
			InstalledVersion: in.Version, InstalledDate: in.Resource.Published,
			LatestVersion: latest.Version, LatestDate: latest.Published,
			DownloadSize: latest.DownloadSize, Priority: pr, Queued: queued[in.ResourceID] == latest.Version,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority == "high"
		}
		return out[i].Title < out[j].Title
	})
	return out, nil
}

// EnqueueByID queues catalog resources for download.
func (a *App) EnqueueByID(ctx context.Context, ids []string) map[string]string {
	out := map[string]string{}
	for _, id := range ids {
		r, err := a.Store.GetResource(ctx, id)
		if err != nil {
			out[id] = "not in catalog"
			continue
		}
		if _, err := a.Downloads.Enqueue(ctx, r); err != nil {
			out[id] = err.Error()
			continue
		}
		out[id] = "queued"
	}
	return out
}

// Recommend runs the preset recommender against current storage.
func (a *App) Recommend(ctx context.Context, presetID, lang string) (catalog.Recommendation, error) {
	presets, err := a.Presets()
	if err != nil {
		return catalog.Recommendation{}, err
	}
	var preset *catalog.Preset
	for i := range presets {
		if presets[i].ID == presetID {
			preset = &presets[i]
		}
	}
	if preset == nil {
		return catalog.Recommendation{}, fmt.Errorf("unknown preset %q", presetID)
	}
	rep, err := a.Storage(ctx, nil)
	if err != nil {
		return catalog.Recommendation{}, err
	}
	var families []string
	for _, it := range preset.Items {
		families = append(families, strings.ReplaceAll(it.Family, "{lang}", lang), strings.ReplaceAll(it.Family, "{lang}", "en"))
	}
	fam, err := a.Store.FamilyVariants(ctx, families)
	if err != nil {
		return catalog.Recommendation{}, err
	}
	present := map[string]bool{}
	installed, _ := a.Store.ListInstalled(ctx)
	for _, in := range installed {
		present[in.ResourceID] = true
	}
	jobs, _ := a.Store.ListJobs(ctx)
	for _, j := range jobs {
		if j.Active() {
			present[j.ResourceID] = true
		}
	}
	return catalog.Recommend(catalog.RecommendInput{
		Preset: *preset, Language: lang, Budget: rep.Plan.Headroom, Families: fam, Present: present,
	}), nil
}

// ErrNoLibrary is returned when no library has been opened.
var ErrNoLibrary = errors.New("no library is open")

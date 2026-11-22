package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/appupdate"
	"github.com/KK4ODA/arcana/internal/buildinfo"
	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/fsutil"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/storage"
	"github.com/KK4ODA/arcana/internal/tasks"
	"github.com/KK4ODA/arcana/internal/transfer"
)

// autoRefreshLoop refreshes catalogs periodically when the user has opted
// in. Failures (for example no internet) are recorded quietly and retried
// at the next interval.
func (a *App) autoRefreshLoop(ctx context.Context) {
	if a.Lib.ReadOnly {
		return
	}
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		hours := a.Lib.Settings.Get().Catalog.AutoRefreshHours
		if hours <= 0 {
			continue
		}
		providers, _ := a.Store.Providers(ctx)
		last := time.Time{}
		for _, p := range providers {
			if p.ID == "arcana" {
				continue
			}
			if ts, err := time.Parse(time.RFC3339, p.LastAttempt); err == nil && ts.After(last) {
				last = ts
			}
		}
		if time.Since(last) < time.Duration(hours)*time.Hour {
			continue
		}
		rctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		_, err := a.RefreshCatalog(rctx)
		cancel()
		if err != nil {
			a.Log.Info("scheduled catalog refresh failed", "err", err)
		}
	}
}

// ---------------------------------------------------------------- transfer

// StartExport copies resources to dest in the background.
func (a *App) StartExport(ids []string, dest string, withUserDocs bool) (tasks.Task, error) {
	if len(ids) == 0 {
		return tasks.Task{}, errors.New("choose at least one resource")
	}
	if !filepath.IsAbs(dest) {
		return tasks.Task{}, errors.New("use a full destination path, for example E:\\ArcanaExport")
	}
	return a.Tasks.Start("transfer", fmt.Sprintf("Export %d resources to %s", len(ids), dest), true,
		func(ctx context.Context, p tasks.Progress) (any, error) {
			return transfer.Export(ctx, a.Lib, a.Content, a.Store, ids, dest, withUserDocs, transfer.Progress(p))
		})
}

// ScanSource reads an export folder or library and plans an import.
func (a *App) ScanSource(ctx context.Context, src string) (transfer.Manifest, []transfer.Plan, error) {
	m, err := transfer.ReadSource(src)
	if err != nil {
		return m, nil, err
	}
	return m, transfer.PlanImport(ctx, a.Store, m), nil
}

// StartImport copies resources from src in the background.
func (a *App) StartImport(src string, ids []string, withUserDocs bool) (tasks.Task, error) {
	if a.Lib.ReadOnly {
		return tasks.Task{}, library.ErrReadOnly
	}
	vol, err := storage.Stat(a.Lib.Root)
	if err != nil {
		return tasks.Task{}, err
	}
	reserve := a.Downloads.Reserve(vol)
	return a.Tasks.Start("transfer", "Import from "+src, true, func(ctx context.Context, p tasks.Progress) (any, error) {
		return transfer.Import(ctx, a.Lib, a.Content, a.Store, src, ids, withUserDocs, reserve, transfer.Progress(p))
	})
}

// WriteLibraryManifest writes manifest/library.json describing every
// installed resource, so another installation can validate this drive.
func (a *App) WriteLibraryManifest(ctx context.Context) (string, error) {
	if a.Lib.ReadOnly {
		return "", library.ErrReadOnly
	}
	m, err := transfer.LibraryManifest(ctx, a.Lib, a.Store)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(a.Lib.Root, "manifest")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "library.json")
	return p, fsutil.WriteFileAtomic(p, append(data, '\n'), 0o644)
}

// StartFullCheck runs a full integrity check in the background.
func (a *App) StartFullCheck() (tasks.Task, error) {
	return a.Tasks.Start("check", "Full integrity check", true, func(ctx context.Context, p tasks.Progress) (any, error) {
		p(0, 0, "Re-hashing every installed file")
		return a.Content.Check(ctx, true)
	})
}

// ---------------------------------------------------------------- updates

// UpdateInfo describes the application update state for the UI.
type UpdateInfo struct {
	Version      string              `json:"version"`
	Platform     string              `json:"platform"`
	KeysTrusted  int                 `json:"keysTrusted"`
	Status       appupdate.Status    `json:"status"`
	Available    *appupdate.Manifest `json:"available,omitempty"`
	CheckedURL   string              `json:"checkedUrl,omitempty"`
	CheckMessage string              `json:"checkMessage,omitempty"`
}

// AppUpdateInfo returns the current update state.
func (a *App) AppUpdateInfo() UpdateInfo {
	return UpdateInfo{Version: buildinfo.Version, Platform: buildinfo.Platform(), KeysTrusted: len(a.Updater.Keys),
		Status: a.Updater.LoadStatus()}
}

// CheckAppUpdate fetches and verifies the published release manifest. It is
// only called when the user asks.
func (a *App) CheckAppUpdate(ctx context.Context) (UpdateInfo, error) {
	info := a.AppUpdateInfo()
	base := strings.TrimRight(a.Lib.Settings.Get().Updates.URL, "/")
	info.CheckedURL = base
	if len(a.Updater.Keys) == 0 {
		return info, appupdate.ErrNoKeys
	}
	mdata, err := a.Fetcher.Get(ctx, base+"/"+appupdate.ManifestFile, 4<<20)
	if err != nil {
		return info, fmt.Errorf("could not reach the update server: %w", err)
	}
	sig, err := a.Fetcher.Get(ctx, base+"/"+appupdate.SignatureFile, 4096)
	if err != nil {
		return info, fmt.Errorf("the release has no signature: %w", err)
	}
	m, err := appupdate.Verify(mdata, sig, a.Updater.Keys)
	if err != nil {
		return info, err
	}
	if !m.Newer() {
		info.CheckMessage = "You have the latest version."
		return info, nil
	}
	info.Available = &m
	info.CheckMessage = "Version " + m.Version + " is available."
	return info, nil
}

// StartAppUpdateDownload downloads and stages the verified release archive.
func (a *App) StartAppUpdateDownload() (tasks.Task, error) {
	return a.Tasks.Start("appupdate", "Download application update", true, func(ctx context.Context, p tasks.Progress) (any, error) {
		info, err := a.CheckAppUpdate(ctx)
		if err != nil {
			return nil, err
		}
		if info.Available == nil || info.Available.Archive == nil {
			return nil, errors.New("no newer release with a downloadable archive")
		}
		m := *info.Available
		if !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(m.Archive.Path) {
			return nil, errors.New("invalid archive name in release manifest")
		}
		dir := filepath.Join(a.Lib.Staging(), "appupdate-download")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		dst := filepath.Join(dir, m.Archive.Path)
		if err := downloadFile(ctx, a.Lib.Settings.Get().Updates.URL+"/"+m.Archive.Path, dst, m.Archive.Size, p); err != nil {
			return nil, err
		}
		defer os.Remove(dst)
		if err := a.Updater.StageFromArchive(ctx, m, dst); err != nil {
			return nil, err
		}
		return "Version " + m.Version + " is ready to install.", nil
	})
}

func downloadFile(ctx context.Context, url, dst string, size int64, p tasks.Progress) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", catalog.UserAgent())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	var done int64
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				return err
			}
			done += int64(n)
			p(done, size, "")
			if done > size+1 {
				return errors.New("archive larger than the signed manifest states")
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	return f.Sync()
}

// StageAppUpdateFromDir verifies and stages an update folder.
func (a *App) StageAppUpdateFromDir(ctx context.Context, dir string) (appupdate.Manifest, error) {
	return a.Updater.StageFromDir(ctx, dir)
}

// ---------------------------------------------------------------- presets

var presetIDRe = regexp.MustCompile(`[^a-z0-9-]+`)

// Presets returns bundled presets followed by the user's saved ones.
func (a *App) Presets() ([]catalog.Preset, error) {
	presets, err := catalog.BundledPresets()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(a.Lib.User(), "presets")
	entries, _ := os.ReadDir(dir)
	var custom []catalog.Preset
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var p catalog.Preset
		if json.Unmarshal(data, &p) == nil && strings.HasPrefix(p.ID, "user-") && len(p.Items) > 0 {
			custom = append(custom, p)
		}
	}
	sort.Slice(custom, func(i, j int) bool { return custom[i].Title < custom[j].Title })
	return append(presets, custom...), nil
}

// SavePreset saves a selection of catalog resources as a custom preset in
// user/presets/. Each resource becomes an essential item preferring the
// chosen edition.
func (a *App) SavePreset(ctx context.Context, title, description string, ids []string) (catalog.Preset, error) {
	if a.Lib.ReadOnly {
		return catalog.Preset{}, library.ErrReadOnly
	}
	title = strings.TrimSpace(title)
	if title == "" || len(title) > 80 {
		return catalog.Preset{}, errors.New("give the collection a name of up to 80 characters")
	}
	slug := strings.Trim(presetIDRe.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if slug == "" {
		slug = "collection"
	}
	if len(slug) > 40 {
		slug = slug[:40]
	}
	p := catalog.Preset{ID: "user-" + slug, Title: title, Description: strings.TrimSpace(description)}
	for _, id := range ids {
		r, err := a.Store.GetResource(ctx, id)
		if err != nil {
			continue
		}
		family := r.Family
		if family == "" {
			family = r.ID
		}
		p.Items = append(p.Items, catalog.PresetItem{Family: family, Variants: []string{r.Variant}, Priority: 1, Reason: "Saved in your collection."})
	}
	if len(p.Items) == 0 {
		return p, errors.New("none of the selected resources are in the catalog")
	}
	data, _ := json.MarshalIndent(p, "", "  ")
	dir := filepath.Join(a.Lib.User(), "presets")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return p, err
	}
	return p, fsutil.WriteFileAtomic(filepath.Join(dir, p.ID+".json"), append(data, '\n'), 0o644)
}

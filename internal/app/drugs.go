package app

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/download"
	"github.com/KK4ODA/arcana/internal/drugs"
)

// drugState holds the open drug label reference.
type drugState struct {
	mu     sync.RWMutex
	reader *drugs.Reader
	id     string
	path   string
}

// DrugProvider returns the openFDA provider, or nil when it is disabled.
func (a *App) DrugProvider() *drugs.Provider {
	if !a.Lib.Settings.Get().Catalog.OpenFDAEnabled {
		return nil
	}
	return &drugs.Provider{}
}

// drugExtractor adapts drugs.Install to the download manager.
func (a *App) drugExtractor(ctx context.Context, req download.ExtractRequest) (int64, string, error) {
	size, sum, err := drugs.Install(ctx, drugs.InstallRequest{
		Resource: req.Resource, Out: req.Out, Client: req.Client, UserAgent: catalog.UserAgent(),
		Planned: req.Planned, Progress: req.Progress, Log: a.Log,
	})
	if errors.Is(err, drugs.ErrSourceChanged) {
		err = fmt.Errorf("%w: %v", download.ErrRestart, err)
	}
	return size, sum, err
}

// Drugs returns the installed drug label reference, or nil.
func (a *App) Drugs() *drugs.Reader {
	a.drugs.mu.RLock()
	defer a.drugs.mu.RUnlock()
	return a.drugs.reader
}

// DrugsResourceID is the installed reference's resource ID ("" if none).
func (a *App) DrugsResourceID() string {
	a.drugs.mu.RLock()
	defer a.drugs.mu.RUnlock()
	return a.drugs.id
}

// reloadDrugs opens the installed reference (or closes a removed one).
func (a *App) reloadDrugs() {
	installed, err := a.Store.ListInstalled(context.Background())
	if err != nil {
		return
	}
	var id, path string
	for _, in := range installed {
		if in.Resource.Format != drugs.Format || len(in.Files) != 1 {
			continue
		}
		if p, err := a.Content.FilePath(in, in.Files[0]); err == nil {
			id, path = in.ResourceID, p
			break
		}
	}
	a.drugs.mu.Lock()
	defer a.drugs.mu.Unlock()
	if path == a.drugs.path && (path == "" || a.drugs.reader != nil) {
		return
	}
	if a.drugs.reader != nil {
		a.drugs.reader.Close()
		a.drugs.reader = nil
	}
	a.drugs.id, a.drugs.path = id, path
	if path == "" {
		return
	}
	r, err := drugs.Open(path)
	if err != nil {
		a.Log.Warn("drug label reference", "err", err)
		return
	}
	a.drugs.reader = r
}

func (a *App) closeDrugs() {
	a.drugs.mu.Lock()
	defer a.drugs.mu.Unlock()
	if a.drugs.reader != nil {
		a.drugs.reader.Close()
		a.drugs.reader = nil
	}
}

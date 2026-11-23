package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/download"
	"github.com/KK4ODA/arcana/internal/routing"
)

// RoadProvider returns the road network provider, or nil when maps are
// disabled.
func (a *App) RoadProvider() *routing.Provider {
	s := a.Lib.Settings.Get().Maps
	if !s.Enabled {
		return nil
	}
	return &routing.Provider{IndexURL: s.RoadsURL}
}

// Roads returns the routing service.
func (a *App) Roads() *routing.Service { return a.roads }

// roadExtractor adapts routing.Install to the download manager.
func (a *App) roadExtractor(ctx context.Context, req download.ExtractRequest) (int64, string, error) {
	size, sum, err := routing.Install(ctx, routing.InstallRequest{
		Resource: req.Resource, Out: req.Out, Client: req.Client, UserAgent: catalog.UserAgent(),
		Planned: req.Planned, Progress: req.Progress, Log: a.Log,
	})
	if errors.Is(err, routing.ErrChecksum) {
		// The damaged extract was discarded: start again.
		err = fmt.Errorf("%w: %v", download.ErrRestart, err)
	}
	return size, sum, err
}

// reloadRoads tells the routing service which road networks are installed.
func (a *App) reloadRoads() {
	if a.roads == nil {
		return
	}
	installed, err := a.Store.ListInstalled(context.Background())
	if err != nil {
		a.Log.Error("list installed", "err", err)
		return
	}
	var specs []routing.NetworkSpec
	for _, in := range installed {
		if in.Resource.Format != routing.Format || len(in.Files) != 1 {
			continue
		}
		p, err := a.Content.FilePath(in, in.Files[0])
		if err != nil {
			continue
		}
		specs = append(specs, routing.NetworkSpec{
			ResourceID: in.ResourceID, Title: in.Resource.Title, Version: in.Version, Path: p,
			BBox: in.Resource.Coverage.BBox,
		})
	}
	a.roads.Load(specs)
}

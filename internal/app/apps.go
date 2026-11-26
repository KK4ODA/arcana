package app

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/KK4ODA/arcana/internal/apps"
	"github.com/KK4ODA/arcana/internal/buildinfo"
)

// Apps are the web applications installed from the catalog (ADR-0030).

func (a *App) openApps() {
	a.Apps = &apps.Manager{
		Log:      a.Log,
		Cache:    filepath.Join(a.runtimeCache(), "apps"),
		Platform: buildinfo.Platform(),
		ReadOnly: a.Lib.ReadOnly,
		Data:     a.AppData,
	}
}

// AppData is the folder on the drive where an app keeps its data, under
// user/ so it travels with the drive and content updates never touch it.
func (a *App) AppData(id string) string {
	return filepath.Join(a.Lib.User(), "apps", id)
}

// reloadApps prepares the installed apps on this computer.
func (a *App) reloadApps() {
	if a.Apps == nil || a.Store == nil {
		return
	}
	list, err := a.Store.ListInstalled(context.Background())
	if err != nil {
		a.Log.Warn("apps", "err", err)
		return
	}
	var installed []apps.Installed
	for _, in := range list {
		if !strings.HasPrefix(in.ResourceID, apps.ResourcePrefix) {
			continue
		}
		ai := apps.Installed{ResourceID: in.ResourceID, Version: in.Version}
		for _, f := range in.Files {
			p, err := a.Content.FilePath(in, f)
			if err != nil {
				continue
			}
			ai.Files = append(ai.Files, apps.Packed{Name: filepath.Base(f.Path), Path: p, SHA256: f.SHA256})
		}
		installed = append(installed, ai)
	}
	a.Apps.Sync(installed)
}

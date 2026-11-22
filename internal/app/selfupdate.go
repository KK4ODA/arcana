package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/KK4ODA/arcana/internal/appupdate"
	"github.com/KK4ODA/arcana/internal/buildinfo"
	"github.com/KK4ODA/arcana/internal/catalog"
	"github.com/KK4ODA/arcana/internal/fsutil"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/tasks"
)

// SelfUpdate says whether this running copy of Arcana can replace itself
// with a new version and restart.
type SelfUpdate struct {
	Possible bool   `json:"possible"`
	Reason   string `json:"reason,omitempty"`
	// Exe is the program to start after installing; Program is the
	// library's copy. They differ when the launcher runs a temporary copy
	// (drives that do not allow running programs).
	Exe     string `json:"-"`
	Program string `json:"-"`
	Copy    bool   `json:"-"`
}

func sameFile(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

// SelfUpdate reports how this process can be updated in place.
func (a *App) SelfUpdate() SelfUpdate {
	if a.Lib.ReadOnly {
		return SelfUpdate{Reason: "The library is read-only, so the program files on it cannot be replaced."}
	}
	if len(a.Updater.Keys) == 0 {
		return SelfUpdate{Reason: "This build has no release key, so updates cannot be verified."}
	}
	prog := filepath.Join(a.Lib.Root, filepath.FromSlash(appupdate.CurrentPlatformPath()))
	executable := os.Executable
	if a.executable != nil {
		executable = a.executable
	}
	exe, err := executable()
	if err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
	}
	switch {
	case err == nil && sameFile(exe, prog):
		return SelfUpdate{Possible: true, Exe: prog, Program: prog}
	case err == nil && os.Getenv("ARCANA_PROGRAM_SOURCE") != "" && sameFile(os.Getenv("ARCANA_PROGRAM_SOURCE"), prog):
		return SelfUpdate{Possible: true, Exe: exe, Program: prog, Copy: true}
	case os.Getenv("INVOCATION_ID") != "" || strings.HasPrefix(exe, "/opt/arcana/"):
		return SelfUpdate{Reason: "Arcana runs as a system service here. Update it by running appliance/install.sh from the new release."}
	}
	return SelfUpdate{Reason: "This copy of Arcana does not run from the library folder, so it cannot replace itself. Download the new version instead."}
}

// ---------------------------------------------------------------- checks

// UpdateCheck is the result of the last check, kept in
// state/update-check.json so the notice survives restarts offline.
type UpdateCheck struct {
	CheckedAt time.Time           `json:"checkedAt"`
	Available *appupdate.Manifest `json:"available,omitempty"`
	Error     string              `json:"error,omitempty"`
}

type updateState struct {
	mu         sync.Mutex
	installing bool
}

// installedPending returns a newer version that is installed on the drive
// but not running yet, because Arcana has not been restarted.
func (a *App) installedPending() string {
	st := a.Updater.LoadStatus()
	if st.AppliedVersion == "" || st.RolledBack ||
		catalog.CompareVersions(st.AppliedVersion, strings.TrimPrefix(buildinfo.Version, "v")) <= 0 {
		return ""
	}
	return st.AppliedVersion
}

func (a *App) updateCheckPath() string {
	return filepath.Join(a.Lib.Layout.State(), "update-check.json")
}

// LastUpdateCheck returns the stored check result. A stored offer that is
// no longer newer (Arcana was updated) is dropped.
func (a *App) LastUpdateCheck() UpdateCheck {
	var c UpdateCheck
	data, err := os.ReadFile(a.updateCheckPath())
	if err != nil {
		return c
	}
	if json.Unmarshal(data, &c) != nil {
		return UpdateCheck{}
	}
	if c.Available != nil && !c.Available.Newer() {
		c.Available = nil
	}
	return c
}

func (a *App) recordCheck(info UpdateInfo, err error) {
	if a.Lib.ReadOnly {
		return
	}
	c := UpdateCheck{CheckedAt: time.Now().UTC().Truncate(time.Second), Available: info.Available}
	if err != nil {
		c.Error = err.Error()
		// Keep a known offer when a later check fails (for example offline).
		if prev := a.LastUpdateCheck(); prev.Available != nil {
			c.Available = prev.Available
		}
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	_ = fsutil.WriteFileAtomic(a.updateCheckPath(), append(data, '\n'), 0o644)
}

// updateCheckInterval is how often the automatic check runs.
const updateCheckInterval = 24 * time.Hour

// appUpdateLoop checks for a new version once a day while the setting is on.
// The first check waits a little after start, so starting Arcana stays fast
// and an offline start makes no request it cannot complete.
func (a *App) appUpdateLoop(ctx context.Context, firstDelay time.Duration) {
	if len(a.Updater.Keys) == 0 {
		return
	}
	timer := time.NewTimer(firstDelay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if a.Lib.Settings.Get().Updates.AutoCheck && time.Since(a.LastUpdateCheck().CheckedAt) >= updateCheckInterval {
			cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			info, err := a.CheckAppUpdate(cctx)
			cancel()
			if err != nil {
				a.Log.Info("automatic update check failed; will try again later", "err", err)
			} else if info.Available != nil {
				a.Log.Info("a new version of Arcana is available", "version", info.Available.Version)
			}
		}
		timer.Reset(time.Hour)
	}
}

// AppUpdateNotice is what the interface shows about program updates.
type AppUpdateNotice struct {
	Current   string `json:"current"`
	Available string `json:"available,omitempty"`
	// Installed is a newer version already installed on the drive that
	// starts the next time Arcana starts.
	Installed  string     `json:"installed,omitempty"`
	Released   string     `json:"released,omitempty"`
	Notes      string     `json:"notes,omitempty"`
	CheckedAt  *time.Time `json:"checkedAt,omitempty"`
	AutoCheck  bool       `json:"autoCheck"`
	Installing bool       `json:"installing,omitempty"`
	SelfUpdate SelfUpdate `json:"selfUpdate"`
}

// UpdateNotice summarises the update state for the interface.
func (a *App) UpdateNotice() AppUpdateNotice {
	c := a.LastUpdateCheck()
	n := AppUpdateNotice{Current: buildinfo.Version, AutoCheck: a.Lib.Settings.Get().Updates.AutoCheck, SelfUpdate: a.SelfUpdate()}
	if !c.CheckedAt.IsZero() {
		t := c.CheckedAt
		n.CheckedAt = &t
	}
	if c.Available != nil {
		n.Available, n.Released, n.Notes = c.Available.Version, c.Available.Released, c.Available.Notes
	}
	if n.Installed = a.installedPending(); n.Installed != "" && catalog.CompareVersions(n.Available, n.Installed) <= 0 {
		n.Available, n.Released, n.Notes = "", "", ""
	}
	a.selfupd.mu.Lock()
	n.Installing = a.selfupd.installing
	a.selfupd.mu.Unlock()
	return n
}

// ---------------------------------------------------------------- install

// downloadAndStage fetches the newest release archive and stages it.
func (a *App) downloadAndStage(ctx context.Context, p tasks.Progress) (appupdate.Manifest, error) {
	info, err := a.CheckAppUpdate(ctx)
	if err != nil {
		return appupdate.Manifest{}, err
	}
	if info.Available == nil || info.Available.Archive == nil {
		return appupdate.Manifest{}, errors.New("no newer release with a downloadable archive")
	}
	m := *info.Available
	if !archiveNameRe.MatchString(m.Archive.Path) {
		return m, errors.New("invalid archive name in release manifest")
	}
	dir := filepath.Join(a.Lib.Staging(), "appupdate-download")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return m, err
	}
	dst := filepath.Join(dir, m.Archive.Path)
	if err := downloadFile(ctx, strings.TrimRight(a.Lib.Settings.Get().Updates.URL, "/")+"/"+m.Archive.Path, dst, m.Archive.Size, p); err != nil {
		return m, err
	}
	defer os.Remove(dst)
	p(0, 0, "Verifying "+m.Version)
	if err := a.Updater.StageFromArchive(ctx, m, dst); err != nil {
		return m, err
	}
	return m, nil
}

// StartAppUpdateInstall downloads, verifies and installs the newest version
// in one step, then restarts Arcana when this copy can restart itself.
func (a *App) StartAppUpdateInstall() (tasks.Task, error) {
	if a.Lib.ReadOnly {
		return tasks.Task{}, library.ErrReadOnly
	}
	// Installing again before a restart would replace the kept previous
	// program (the one running now) with a version that never ran.
	if v := a.installedPending(); v != "" {
		return tasks.Task{}, fmt.Errorf("version %s is already installed; close Arcana and start it again to use it", v)
	}
	a.selfupd.mu.Lock()
	if a.selfupd.installing {
		a.selfupd.mu.Unlock()
		return tasks.Task{}, errors.New("an update is already being installed")
	}
	a.selfupd.installing = true
	a.selfupd.mu.Unlock()
	t, err := a.Tasks.Start("appupdate", "Update Arcana", true, func(ctx context.Context, p tasks.Progress) (any, error) {
		defer func() {
			a.selfupd.mu.Lock()
			a.selfupd.installing = false
			a.selfupd.mu.Unlock()
		}()
		version := ""
		if st := a.Updater.LoadStatus(); st.Staged != nil && st.Staged.Newer() {
			version = st.Staged.Version // already downloaded and verified
		} else {
			p(0, 0, "Downloading")
			m, err := a.downloadAndStage(ctx, p)
			if err != nil {
				return nil, err
			}
			version = m.Version
		}
		// Decide how to restart before installing: installing renames the
		// running program to .previous, after which the system reports
		// that file as this process's program.
		su := a.SelfUpdate()
		p(0, 0, "Installing "+version)
		if _, err := a.Updater.Apply(); err != nil {
			return nil, err
		}
		a.Log.Info("installed a new version of Arcana", "version", version)
		if su.Possible && a.RequestRestart != nil {
			// Give the interface a moment to see the result first.
			go func() {
				time.Sleep(1500 * time.Millisecond)
				a.RequestRestart(su)
			}()
			return map[string]any{"version": version, "restarting": true,
				"message": fmt.Sprintf("Version %s is installed. Arcana is restarting.", version)}, nil
		}
		return map[string]any{"version": version, "restarting": false,
			"message": fmt.Sprintf("Version %s is installed. Close Arcana and start it again to use it.", version)}, nil
	})
	if err != nil {
		a.selfupd.mu.Lock()
		a.selfupd.installing = false
		a.selfupd.mu.Unlock()
	}
	return t, err
}

package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/KK4ODA/arcana/internal/buildinfo"
	"github.com/KK4ODA/arcana/internal/download"
	"github.com/KK4ODA/arcana/internal/fsutil"
	"github.com/KK4ODA/arcana/internal/kolibri"
)

// The classroom is Kolibri run by Arcana (ADR-0024): Khan Academy with
// classes, lessons, quizzes and progress for each student.

// ClassroomResource is the catalog resource holding Kolibri and Python.
const ClassroomResource = "arcana.kolibri"

// classroomState is kept in state/kolibri.json.
type classroomState struct {
	// Wanted: the owner set the classroom up and has not turned it off.
	Wanted   bool   `json:"wanted"`
	Facility string `json:"facility"`
	Username string `json:"username"`
	Password string `json:"password"`
	Language string `json:"language"`
	// Provisioned: Kolibri has run once with the provisioning file.
	Provisioned bool `json:"provisioned"`
}

type classroom struct {
	mu        sync.Mutex
	server    *kolibri.Server
	state     classroomState
	content   int // Arcana's content port, once known
	port      int // Kolibri's origin port
	runtime   kolibri.Runtime
	packed    kolibri.Packed
	preparing bool
	problem   string // why the runtime cannot be prepared
	admin     *kolibri.Admin
	adminFor  string
}

func (a *App) classroomFile() string { return filepath.Join(a.Lib.State(), "kolibri.json") }

// KolibriHome is Kolibri's folder on the drive (KOLIBRI_HOME).
func (a *App) KolibriHome() string { return filepath.Join(a.Lib.Root, "kolibri") }

func (a *App) openClassroom() {
	a.class = &classroom{server: kolibri.NewServer(a.Log)}
	if data, err := os.ReadFile(a.classroomFile()); err == nil {
		_ = json.Unmarshal(data, &a.class.state)
	}
}

func (a *App) closeClassroom() {
	if a.class != nil {
		a.class.server.Close()
	}
}

// saveClassroom writes the state; the caller holds c.mu.
func (a *App) saveClassroom() error {
	data, err := json.MarshalIndent(a.class.state, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(a.classroomFile(), data, 0o600)
}

// SetPorts tells the classroom Arcana's content port (where Kolibri's
// channel apps are served) and the port of Kolibri's own origin.
func (a *App) SetPorts(content, kolibriPort int) {
	if a.class == nil {
		return
	}
	a.class.mu.Lock()
	changed := a.class.content != content || a.class.port != kolibriPort
	a.class.content, a.class.port = content, kolibriPort
	a.class.mu.Unlock()
	if changed {
		a.reloadClassroom()
	}
}

// classroomFiles finds the installed runtime files for this computer.
func (a *App) classroomFiles(ctx context.Context) (kolibri.Packed, bool) {
	if a.opts.KolibriPython != "" {
		return kolibri.Packed{Wheel: "test", Python: a.opts.KolibriPython}, true
	}
	in, err := a.Store.GetInstalled(ctx, ClassroomResource)
	if err != nil {
		return kolibri.Packed{}, false
	}
	var p kolibri.Packed
	for _, f := range in.Files {
		fp, err := a.Content.FilePath(in, f)
		if err != nil {
			continue
		}
		name := filepath.Base(f.Path)
		switch {
		case strings.HasSuffix(name, ".whl"):
			p.Wheel, p.WheelSHA256 = fp, f.SHA256
		case kolibri.IsPythonFor(name, buildinfo.Platform()):
			p.Python, p.PythonSHA256 = fp, f.SHA256
		}
	}
	return p, true
}

// reloadClassroom starts, keeps or stops Kolibri to match the state.
func (a *App) reloadClassroom() {
	c := a.class
	if c == nil || a.Lib == nil {
		return
	}
	c.mu.Lock()
	want := c.state.Wanted && !a.Lib.ReadOnly && c.content > 0
	c.mu.Unlock()
	if !want {
		c.server.Stop()
		return
	}
	packed, ok := a.classroomFiles(context.Background())
	c.mu.Lock()
	defer c.mu.Unlock()
	if !ok {
		c.problem = ""
		c.server.Stop()
		return
	}
	if packed.Python == "" {
		c.problem = "Kolibri cannot run on this computer (" + buildinfo.Platform() + "): its Python runtime is not part of the download."
		c.server.Stop()
		return
	}
	if c.preparing {
		return
	}
	if a.opts.KolibriPython != "" {
		c.runtime, c.packed = kolibri.Runtime{Python: a.opts.KolibriPython, App: filepath.Join(a.Lib.State(), "kolibri-app")}, packed
	}
	if c.runtime.Python != "" && c.packed == packed {
		a.startKolibriLocked()
		return
	}
	c.preparing, c.problem = true, ""
	go func() {
		rt, err := kolibri.Unpack(packed, a.runtimeCache())
		c.mu.Lock()
		c.preparing = false
		if err != nil {
			c.problem = "Kolibri could not be prepared on this computer: " + err.Error()
			c.mu.Unlock()
			a.Log.Warn("kolibri runtime", "err", err)
			return
		}
		c.runtime, c.packed = rt, packed
		c.mu.Unlock()
		kolibri.CleanOld(filepath.Dir(rt.Dir), rt.Dir)
		a.reloadClassroom()
	}()
}

// runtimeCache is where Kolibri is unpacked on this computer: the user's
// cache folder, or the library's state folder without one.
func (a *App) runtimeCache() string {
	if dir, err := os.UserCacheDir(); err == nil {
		if err := os.MkdirAll(filepath.Join(dir, "arcana"), 0o755); err == nil {
			return filepath.Join(dir, "arcana")
		}
	}
	return filepath.Join(a.Lib.State(), "runtime")
}

// startKolibriLocked starts Kolibri; the caller holds c.mu.
func (a *App) startKolibriLocked() {
	c := a.class
	home := a.KolibriHome()
	opts := kolibri.Options{Runtime: c.runtime, Home: home, ContentPort: c.content}
	if !c.state.Provisioned {
		p, err := kolibri.WriteProvisionFile(home, kolibri.Setup{Facility: c.state.Facility,
			Username: c.state.Username, Password: c.state.Password, Language: c.state.Language})
		if err != nil {
			c.problem = "Kolibri could not be set up: " + err.Error()
			return
		}
		opts.ProvisionFile = p
	}
	c.server.Start(opts)
	if !c.state.Provisioned {
		go a.awaitProvisioned()
	}
}

// awaitProvisioned records that Kolibri read its provisioning file once it
// runs, and deletes the file, which holds the password.
func (a *App) awaitProvisioned() {
	c := a.class
	for i := 0; i < 60*60; i++ {
		st := c.server.Status()
		if st.State == "running" {
			c.mu.Lock()
			c.state.Provisioned = true
			err := a.saveClassroom()
			c.mu.Unlock()
			if err == nil {
				kolibri.RemoveProvisionFile(a.KolibriHome())
			}
			return
		}
		if st.State == "stopped" || st.State == "failed" {
			return
		}
		time.Sleep(time.Second)
	}
}

// ClassroomStatus describes the classroom for the Classroom page.
type ClassroomStatus struct {
	// State: "off" (not set up), "download" (the runtime is downloading or
	// must be), "preparing" (unpacking on this computer), "starting",
	// "running", "failed", "stopped" or "read-only".
	State    string `json:"state"`
	Message  string `json:"message,omitempty"`
	Wanted   bool   `json:"wanted"`
	Version  string `json:"version,omitempty"`
	Port     int    `json:"port"`
	Facility string `json:"facility,omitempty"`
	Language string `json:"language,omitempty"`
	// Download is the runtime's download, while there is one.
	Download *download.View `json:"download,omitempty"`
	// RuntimeSize is the runtime download's size.
	RuntimeSize int64 `json:"runtimeSize"`
	// Username and Password are Kolibri's administrator (administrators
	// of Arcana only).
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	// Channels are the channels offered, and on the device.
	Channels []kolibri.Channel  `json:"channels"`
	OnDevice []kolibri.OnDevice `json:"onDevice,omitempty"`
	Imports  []kolibri.Task     `json:"imports,omitempty"`
	// AdminError says why Arcana could not sign in to Kolibri.
	AdminError string `json:"adminError,omitempty"`
}

// ClassroomStatus reports the classroom; secrets and Kolibri's lists only
// for administrators.
func (a *App) ClassroomStatus(ctx context.Context, admin bool) ClassroomStatus {
	c := a.class
	st := ClassroomStatus{Channels: kolibri.Channels}
	if res, err := a.Store.GetResource(ctx, ClassroomResource); err == nil {
		st.RuntimeSize = res.DownloadSize
	}
	if c == nil {
		st.State = "off"
		return st
	}
	c.mu.Lock()
	s := c.state
	st.Wanted, st.Facility, st.Language, st.Port = s.Wanted, s.Facility, s.Language, c.port
	preparing, problem := c.preparing, c.problem
	c.mu.Unlock()
	if admin && s.Wanted {
		st.Username, st.Password = s.Username, s.Password
	}
	ks := c.server.Status()
	st.Version = ks.Version
	_, installed := a.classroomFiles(ctx)
	switch {
	case a.Lib.ReadOnly:
		st.State, st.Message = "read-only", "The library is open read-only, so the classroom cannot run."
	case !s.Wanted:
		st.State = "off"
	case !installed:
		st.State = "download"
		if views, err := a.Downloads.List(ctx); err == nil {
			for i := range views {
				if views[i].ResourceID == ClassroomResource && views[i].State != "completed" && views[i].State != "cancelled" {
					st.Download = &views[i]
					break
				}
			}
		}
	case problem != "":
		st.State, st.Message = "failed", problem
	case preparing:
		st.State = "preparing"
	default:
		st.State, st.Message = ks.State, ks.LastError
	}
	if admin && st.State == "running" {
		if adm := a.kolibriAdmin(); adm != nil {
			var err error
			if st.OnDevice, err = adm.Channels(ctx); err != nil {
				st.AdminError = err.Error()
			}
			if tasks, err := adm.Tasks(ctx); err == nil {
				for _, t := range tasks {
					if t.Type == kolibri.ImportTaskType {
						st.Imports = append(st.Imports, t)
					}
				}
			} else if st.AdminError == "" {
				st.AdminError = err.Error()
			}
		}
	}
	return st
}

// kolibriAdmin returns a client signed in as Kolibri's administrator.
func (a *App) kolibriAdmin() *kolibri.Admin {
	c := a.class
	base, ok := c.server.Base()
	if !ok {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := base + "\x00" + c.state.Username + "\x00" + c.state.Password
	if c.admin == nil || c.adminFor != key {
		c.admin = &kolibri.Admin{BaseURL: base, Username: c.state.Username, Password: c.state.Password}
		c.adminFor = key
	}
	return c.admin
}

// KolibriBase returns the running Kolibri's loopback address (for search).
func (a *App) KolibriBase() (string, bool) {
	if a.class == nil {
		return "", false
	}
	return a.class.server.Base()
}

// ClassroomWanted reports whether the owner set the classroom up.
func (a *App) ClassroomWanted() bool {
	if a.class == nil {
		return false
	}
	a.class.mu.Lock()
	defer a.class.mu.Unlock()
	return a.class.state.Wanted
}

// KolibriServer is the Kolibri supervisor (for the proxy).
func (a *App) KolibriServer() *kolibri.Server { return a.class.server }

// SetUpClassroom turns the classroom on: it creates the school's details
// the first time and downloads the runtime if needed.
func (a *App) SetUpClassroom(ctx context.Context, language string) error {
	if a.Lib.ReadOnly {
		return errors.New("the library is open read-only")
	}
	if language != "es" {
		language = "en"
	}
	c := a.class
	c.mu.Lock()
	if c.state.Password == "" {
		c.state = classroomState{Facility: kolibri.FacilityName(a.Lib.Identity.Label),
			Username: kolibri.AdminUsername, Password: kolibri.NewPassword(), Language: language}
	}
	c.state.Wanted = true
	err := a.saveClassroom()
	c.mu.Unlock()
	if err != nil {
		return err
	}
	if _, ok := a.classroomFiles(ctx); !ok {
		if why := a.EnqueueByID(ctx, []string{ClassroomResource})[ClassroomResource]; why != "queued" && why != "" {
			if strings.Contains(why, "not in catalog") {
				return errors.New("the catalog has no Kolibri download yet; refresh the catalog in Discover and try again")
			}
			a.Log.Info("classroom runtime", "enqueue", why)
		}
	}
	a.reloadClassroom()
	return nil
}

// StopClassroom turns the classroom off. Kolibri's data stays on the drive.
func (a *App) StopClassroom() error {
	c := a.class
	c.mu.Lock()
	c.state.Wanted = false
	err := a.saveClassroom()
	c.mu.Unlock()
	a.reloadClassroom()
	return err
}

// SetClassroomPassword records the administrator's password after it was
// changed in Kolibri, checking it first.
func (a *App) SetClassroomPassword(ctx context.Context, password string) error {
	if strings.TrimSpace(password) == "" || len(password) > 200 {
		return errors.New("enter the password")
	}
	base, ok := a.class.server.Base()
	if !ok {
		return errors.New("the classroom is not running")
	}
	c := a.class
	c.mu.Lock()
	user := c.state.Username
	c.mu.Unlock()
	if err := (&kolibri.Admin{BaseURL: base, Username: user, Password: password}).Check(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state.Password = password
	return a.saveClassroom()
}

// channelDB returns a channel's database, fetching it from Kolibri Studio
// when missing or a month old (an old copy is used when Studio cannot be
// reached).
func (a *App) channelDB(ctx context.Context, id string) (*kolibri.ChannelDB, string, error) {
	if _, ok := kolibri.ChannelByID(id); !ok {
		return nil, "", errors.New("this channel is not offered")
	}
	p := filepath.Join(a.Lib.CatalogCache(), "kolibri", id+".sqlite3")
	if kolibri.StaleChannelDB(p) && !a.Lib.ReadOnly {
		client := download.NewClient(func() bool { return a.Lib.Settings.Get().AllowPrivateNetworkDownloads })
		fctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		err := kolibri.FetchChannelDB(fctx, client, kolibri.StudioURL, id, p)
		cancel()
		if err != nil {
			if _, statErr := os.Stat(p); statErr != nil {
				return nil, "", fmt.Errorf("the channel's list of topics could not be downloaded: %w", err)
			}
			a.Log.Warn("channel database", "channel", id, "err", err)
		}
	}
	db, err := kolibri.OpenChannelDB(p)
	return db, p, err
}

var treeMu sync.Mutex

// ClassroomTree returns a channel's grades and subjects with their sizes.
func (a *App) ClassroomTree(ctx context.Context, id string) (kolibri.Node, error) {
	treeMu.Lock()
	defer treeMu.Unlock()
	db, p, err := a.channelDB(ctx, id)
	if err != nil {
		return kolibri.Node{}, err
	}
	defer db.Close()
	// Measuring takes seconds: keep the result beside the database.
	cache := strings.TrimSuffix(p, ".sqlite3") + ".tree.json"
	if dst, err := os.Stat(p); err == nil {
		if cst, err := os.Stat(cache); err == nil && cst.ModTime().After(dst.ModTime()) {
			var n kolibri.Node
			if data, err := os.ReadFile(cache); err == nil && json.Unmarshal(data, &n) == nil && n.ID != "" {
				return n, nil
			}
		}
	}
	n, err := db.Tree(ctx, 2)
	if err != nil {
		return kolibri.Node{}, err
	}
	if data, err := json.Marshal(n); err == nil && !a.Lib.ReadOnly {
		_ = fsutil.WriteFileAtomic(cache, data, 0o644)
	}
	return n, nil
}

// ClassroomSize measures a selection of a channel's topics.
func (a *App) ClassroomSize(ctx context.Context, id string, nodeIDs []string) (int64, int, error) {
	db, _, err := a.channelDB(ctx, id)
	if err != nil {
		return 0, 0, err
	}
	defer db.Close()
	return db.SizeOf(ctx, nodeIDs)
}

// ClassroomImport asks Kolibri to import topics of a channel.
func (a *App) ClassroomImport(ctx context.Context, id string, nodeIDs []string) (kolibri.Task, error) {
	ch, ok := kolibri.ChannelByID(id)
	if !ok {
		return kolibri.Task{}, errors.New("this channel is not offered")
	}
	if len(nodeIDs) == 0 {
		return kolibri.Task{}, errors.New("choose at least one subject or grade")
	}
	db, _, err := a.channelDB(ctx, id)
	if err != nil {
		return kolibri.Task{}, err
	}
	err = db.Has(ctx, nodeIDs)
	db.Close()
	if err != nil {
		return kolibri.Task{}, err
	}
	adm := a.kolibriAdmin()
	if adm == nil {
		return kolibri.Task{}, errors.New("the classroom is not running")
	}
	sort.Strings(nodeIDs)
	return adm.Import(ctx, id, ch.Title, nodeIDs)
}

// ClassroomTask cancels or clears an import.
func (a *App) ClassroomTask(ctx context.Context, id, action string) error {
	adm := a.kolibriAdmin()
	if adm == nil {
		return errors.New("the classroom is not running")
	}
	switch action {
	case "cancel":
		return adm.Cancel(ctx, id)
	case "clear":
		return adm.Clear(ctx, id)
	}
	return errors.New("unknown action")
}

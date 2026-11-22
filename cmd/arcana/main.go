// Command arcana is the portable Arcana application: a local web server for
// an offline knowledge library stored on a removable drive.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/buildinfo"
	"github.com/KK4ODA/arcana/internal/hotspot"
	"github.com/KK4ODA/arcana/internal/library"
	"github.com/KK4ODA/arcana/internal/sdnotify"
	"github.com/KK4ODA/arcana/internal/server"
	"github.com/KK4ODA/arcana/internal/storage"
	"github.com/KK4ODA/arcana/internal/store"
)

const usage = `Arcana — portable offline knowledge library

Usage:
  arcana [serve] [flags]        Start Arcana and open it in the browser
  arcana init DIR [--label L]   Create a new library in DIR
  arcana status                 Show library and storage status
  arcana check [--full]         Verify installed resources (--full re-hashes every file)
  arcana catalog refresh        Download the latest resource catalogs
  arcana catalog search TEXT    Search the catalog
  arcana get ID...              Download and install resources, then exit
  arcana import FILE... [--id ID] [--allow-unverified]
                                Install archives from another drive (offline);
                                verified against the catalog checksum
  arcana import --from DIR [ID...] [--user-documents]
                                Copy resources from an export or another library
  arcana export --to DIR [ID...|--all] [--user-documents]
                                Copy resources to a folder or drive (verified)
  arcana manifest               Write manifest/library.json describing this library
  arcana update stage DIR | apply | rollback | status
                                Install a signed application update (offline)
  arcana hotspot start --ssid NAME --password PW [--device wlan0] | stop | status
                                Wi-Fi hotspot for phones (Linux with NetworkManager)
  arcana volumes                List drives and their file systems
  arcana version                Print version information

Common flags:
  --library DIR   Library folder (default: found next to the program)
  --port N        Application port (default 8470; content uses N+1)
  --lan           Share the library with other devices on the network (read-only)
  --no-browser    Do not open a browser window
  --read-only     Open the library without modifying it
  --kiwix PATH    Path to kiwix-serve
  --captive-port N  Appliance: redirect every request on port N to the library
                  (used with an access point whose port 80 is forwarded to N)
`

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "serve":
		err = cmdServe(args)
	case "init":
		err = cmdInit(args)
	case "status":
		err = cmdStatus(args)
	case "check":
		err = cmdCheck(args)
	case "catalog":
		err = cmdCatalog(args)
	case "get":
		err = cmdGet(args)
	case "import":
		err = cmdImport(args)
	case "export":
		err = cmdExport(args)
	case "manifest":
		err = cmdManifest(args)
	case "update":
		err = cmdUpdate(args)
	case "volumes":
		err = cmdVolumes()
	case "hotspot":
		err = cmdHotspot(args)
	case "version", "--version", "-v":
		fmt.Printf("arcana %s (%s, %s, built %s)\n", buildinfo.Version, buildinfo.Platform(), buildinfo.Commit, buildinfo.Date)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		err = fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "arcana:", err)
		pauseIfDoubleClicked()
		os.Exit(1)
	}
}

type commonFlags struct {
	library  string
	port     int
	lan      bool
	noOpen   bool
	readOnly bool
	kiwix    string
	verbose  bool
	// captivePort serves the appliance's captive portal.
	captivePort int
}

func parseFlags(name string, args []string, extra func(*flag.FlagSet)) (*commonFlags, *flag.FlagSet, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	c := &commonFlags{}
	fs.StringVar(&c.library, "library", "", "")
	fs.IntVar(&c.port, "port", 0, "")
	fs.BoolVar(&c.lan, "lan", false, "")
	fs.BoolVar(&c.noOpen, "no-browser", false, "")
	fs.IntVar(&c.captivePort, "captive-port", 0, "")
	fs.BoolVar(&c.readOnly, "read-only", false, "")
	fs.StringVar(&c.kiwix, "kiwix", "", "")
	fs.BoolVar(&c.verbose, "verbose", false, "")
	if extra != nil {
		extra(fs)
	}
	if err := fs.Parse(args); err != nil {
		return nil, nil, fmt.Errorf("%v\n\n%s", err, usage)
	}
	return c, fs, nil
}

func requireLibrary(c *commonFlags) (string, error) {
	root := library.Discover(c.library)
	if root == "" {
		if c.library != "" {
			return "", fmt.Errorf("%s is not an Arcana library (create one with: arcana init %s)", c.library, c.library)
		}
		return "", errors.New("no Arcana library found next to this program; pass --library DIR or run: arcana init DIR")
	}
	return root, nil
}

// newLogger writes to stderr and, when a library is open and writable, to
// logs/arcana.log on the drive (rotated at 5 MB to limit drive wear).
func newLogger(c *commonFlags, root string) (*slog.Logger, func()) {
	level := slog.LevelInfo
	if c.verbose {
		level = slog.LevelDebug
	}
	var w io.Writer = os.Stderr
	closer := func() {}
	if root != "" && !c.readOnly {
		logPath := filepath.Join(root, "logs", "arcana.log")
		if st, err := os.Stat(logPath); err == nil && st.Size() > 5<<20 {
			_ = os.Rename(logPath, logPath+".1")
		}
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			w = io.MultiWriter(os.Stderr, f)
			closer = func() { f.Close() }
		}
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})), closer
}

func cmdServe(args []string) error {
	c, _, err := parseFlags("serve", args, nil)
	if err != nil {
		return err
	}
	root := library.Discover(c.library)
	if c.library != "" && root == "" && !library.IsLibrary(c.library) {
		// An explicit folder that is not a library yet: offer to create
		// it in setup mode.
		root = ""
	}
	log, closeLog := newLogger(c, root)
	defer closeLog()
	slog.SetDefault(log)

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// An installed update asks for a restart by ending the serve context.
	ctx, endServe := context.WithCancel(sigCtx)
	defer endServe()
	var restart atomic.Pointer[app.SelfUpdate]

	openApp := func(r string) (*app.App, error) {
		a, err := app.Open(r, app.Options{KiwixBinary: c.kiwix, ForceReadOnly: c.readOnly, Log: log})
		if err != nil {
			return nil, err
		}
		a.RequestRestart = func(su app.SelfUpdate) {
			restart.Store(&su)
			endServe()
		}
		if err := a.Start(ctx); err != nil {
			a.Close()
			return nil, err
		}
		return a, nil
	}

	var a *app.App
	port, lan := c.port, c.lan
	if root != "" {
		a, err = openApp(root)
		if err != nil {
			return err
		}
		set := a.Lib.Settings.Get()
		if port == 0 {
			port = set.Server.Port
		}
		lan = lan || set.Server.ShareOnLAN
	}
	if port == 0 {
		port = 8470
	}
	srv := server.New(a, server.Config{
		Port: port, ShareOnLAN: lan, Log: log, CaptivePort: c.captivePort,
		OpenLibrary:        openApp,
		DefaultLibraryPath: suggestLibraryPath(c.library),
	})
	la, lc, err := srv.Listen()
	if err != nil {
		if a != nil {
			a.Close()
		}
		return err
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/", srv.AppPort())
	fmt.Println()
	fmt.Println("  Arcana " + buildinfo.Version)
	if a != nil {
		fmt.Printf("  Library:  %s%s\n", a.Lib.Root, map[bool]string{true: " (read-only)", false: ""}[a.Lib.ReadOnly])
	} else {
		fmt.Println("  No library found: the browser will open the setup page.")
	}
	fmt.Printf("  Open:     %s\n", url)
	if lan {
		fmt.Println("  Sharing on the local network (read-only for other devices).")
		if a != nil {
			if set := a.Lib.Settings.Get().Server; set.MDNS {
				name := set.Name
				if name == "" {
					name = "arcana"
				}
				fmt.Printf("  Network:  http://%s.local:%d/ (if the name is taken, the Server view shows the one in use)\n", name, srv.AppPort())
			}
		}
	}
	fmt.Println("  Press Ctrl+C or close this window to stop Arcana.")
	fmt.Println()
	if !c.noOpen {
		go func() {
			time.Sleep(300 * time.Millisecond)
			if err := openBrowser(url); err != nil {
				log.Info("could not open a browser; open the address manually", "url", url)
			}
		}()
	}
	// Under systemd (the appliance), report readiness and keep the watchdog
	// fed while the web server answers.
	if ok, _ := sdnotify.Notify(fmt.Sprintf("READY=1\nSTATUS=Serving on port %d", srv.AppPort())); ok {
		health := fmt.Sprintf("http://127.0.0.1:%d/api/session", srv.AppPort())
		go sdnotify.Watchdog(ctx, func(hctx context.Context) error {
			req, _ := http.NewRequestWithContext(hctx, http.MethodGet, health, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("status %d", resp.StatusCode)
			}
			return nil
		})
	}
	err = srv.Serve(ctx, la, lc)
	_, _ = sdnotify.Notify("STOPPING=1")
	log.Info("shutting down")
	if cur := srv.App(); cur != nil {
		cur.Close()
	}
	// The restart plan was made before the update was installed: asking
	// again now would see the renamed old program, not the new one.
	if su := restart.Load(); su != nil && su.Possible && sigCtx.Err() == nil {
		return restartUpdated(*su, srv.AppPort(), log)
	}
	return err
}

// restartUpdated starts the newly installed program on the same port, so
// the open browser page can reload into it.
func restartUpdated(su app.SelfUpdate, port int, log *slog.Logger) error {
	if su.Copy {
		// The launcher runs a temporary copy (the drive does not allow
		// running programs): refresh it from the updated library copy.
		data, err := os.ReadFile(su.Program)
		if err != nil {
			return fmt.Errorf("restart after update: %w", err)
		}
		tmp := su.Exe + ".new"
		if err := os.WriteFile(tmp, data, 0o755); err != nil {
			return fmt.Errorf("restart after update: %w", err)
		}
		if err := os.Rename(tmp, su.Exe); err != nil {
			return fmt.Errorf("restart after update: %w", err)
		}
	}
	args := append(append([]string(nil), os.Args[1:]...), "--no-browser", "--port", strconv.Itoa(port))
	log.Info("restarting into the new version", "program", su.Exe)
	return relaunch(su.Exe, args)
}

// suggestLibraryPath proposes where to create a library: the folder the
// program was started from (normally the Arcana folder on the drive).
func suggestLibraryPath(explicit string) string {
	if explicit != "" {
		if abs, err := filepath.Abs(explicit); err == nil {
			return abs
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	// bin/<platform>/arcana -> library root two levels up.
	if filepath.Base(filepath.Dir(dir)) == "bin" {
		return filepath.Dir(filepath.Dir(dir))
	}
	return dir
}

func cmdInit(args []string) error {
	var label string
	c, fs, err := parseFlags("init", reorder(args), func(fs *flag.FlagSet) { fs.StringVar(&label, "label", "", "") })
	if err != nil {
		return err
	}
	_ = c
	if fs.NArg() != 1 {
		return errors.New("usage: arcana init DIR [--label NAME]")
	}
	id, err := library.Init(fs.Arg(0), label)
	if err != nil {
		return err
	}
	fmt.Printf("Created library %q (%s) in %s\n", id.Label, id.LibraryID, fs.Arg(0))
	return nil
}

var boolFlags = map[string]bool{"full": true, "lan": true, "read-only": true, "no-browser": true,
	"allow-unverified": true, "online": true, "verbose": true, "all": true, "user-documents": true}

// reorder moves flags before positional arguments so "init DIR --label X"
// works as users expect.
func reorder(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flags = append(flags, args[i])
			if !strings.Contains(args[i], "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") &&
				!boolFlags[strings.TrimLeft(args[i], "-")] {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, args[i])
	}
	return append(flags, pos...)
}

func openCLI(c *commonFlags) (*app.App, func(), error) {
	root, err := requireLibrary(c)
	if err != nil {
		return nil, nil, err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	a, err := app.Open(root, app.Options{DisableKiwix: true, ForceReadOnly: c.readOnly, Log: log})
	if err != nil {
		return nil, nil, err
	}
	return a, a.Close, nil
}

func cmdStatus(args []string) error {
	c, _, err := parseFlags("status", args, nil)
	if err != nil {
		return err
	}
	a, done, err := openCLI(c)
	if err != nil {
		return err
	}
	defer done()
	ctx := context.Background()
	rep, err := a.Storage(ctx, nil)
	if err != nil {
		return err
	}
	installed, _ := a.Store.ListInstalled(ctx)
	p := rep.Plan
	fmt.Printf("Library      %s (%s)\n", a.Lib.Identity.Label, a.Lib.Root)
	if a.Lib.ReadOnly {
		fmt.Printf("Mode         read-only: %s\n", a.Lib.ReadOnlyReason)
	}
	fmt.Printf("Drive        %s, %s (%s)", rep.Volume.MountPoint, rep.FS.Name, storage.FormatBytes(p.Total))
	if p.Nominal > 0 {
		fmt.Printf(", likely sold as %s", storage.FormatBytes(p.Nominal))
	}
	fmt.Println()
	fmt.Printf("Installed    %d resources, %s\n", len(installed), storage.FormatBytes(p.Installed))
	fmt.Printf("Queued       %s still to download\n", storage.FormatBytes(p.Queued))
	fmt.Printf("Free         %s now, %s after queued downloads\n", storage.FormatBytes(p.Available), storage.FormatBytes(p.AfterQueue))
	fmt.Printf("Reserve      %s kept free\n", storage.FormatBytes(p.Reserve))
	for _, m := range append(p.Messages, rep.FS.Warnings...) {
		fmt.Println("Note         " + m)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	if len(installed) > 0 {
		fmt.Fprintln(tw, "\nRESOURCE\tVERSION\tSIZE\tVERIFIED")
		for _, in := range installed {
			v := "yes"
			if in.Unverified {
				v = "no checksum"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", in.ResourceID, in.Version, storage.FormatBytes(in.TotalSize()), v)
		}
	}
	return tw.Flush()
}

func cmdCheck(args []string) error {
	var full bool
	c, _, err := parseFlags("check", args, func(fs *flag.FlagSet) { fs.BoolVar(&full, "full", false, "") })
	if err != nil {
		return err
	}
	a, done, err := openCLI(c)
	if err != nil {
		return err
	}
	defer done()
	rep, err := a.Content.Check(context.Background(), full)
	if err != nil {
		return err
	}
	kind := "quick"
	if full {
		kind = "full"
	}
	fmt.Printf("%s check: %d resources, %d files, %s\n", kind, rep.Resources, rep.Files, storage.FormatBytes(rep.Bytes))
	for _, p := range rep.Problems {
		fmt.Printf("  %-16s %-24s %s %s\n", p.Kind, p.ResourceID, p.File, p.Detail)
	}
	if !rep.OK() {
		return fmt.Errorf("%d problem(s) found", len(rep.Problems))
	}
	fmt.Println("No problems found.")
	return nil
}

func cmdCatalog(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: arcana catalog refresh | search TEXT")
	}
	sub := args[0]
	c, fs, err := parseFlags("catalog", reorder(args[1:]), nil)
	if err != nil {
		return err
	}
	a, done, err := openCLI(c)
	if err != nil {
		return err
	}
	defer done()
	ctx := context.Background()
	switch sub {
	case "refresh":
		results, err := a.RefreshCatalog(ctx)
		for _, r := range results {
			if r.Error != "" {
				fmt.Printf("%-10s failed: %s (cached entries kept)\n", r.Provider, r.Error)
			} else {
				fmt.Printf("%-10s %d resources (%d rejected)\n", r.Provider, r.Resources, r.Rejected)
			}
		}
		return err
	case "search":
		rs, total, err := a.Store.SearchResources(ctx, store.Query{Text: strings.Join(fs.Args(), " "), Limit: 50})
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tVERSION\tSIZE\tLICENSE\tTITLE")
		for _, r := range rs {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.ID, r.Version, storage.FormatBytes(r.DownloadSize), r.License.ID, r.Title)
		}
		tw.Flush()
		fmt.Printf("%d of %d matches\n", len(rs), total)
		return nil
	}
	return fmt.Errorf("unknown catalog command %q", sub)
}

func cmdGet(args []string) error {
	c, fs, err := parseFlags("get", reorder(args), nil)
	if err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("usage: arcana get RESOURCE-ID...")
	}
	a, done, err := openCLI(c)
	if err != nil {
		return err
	}
	defer done()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	results := a.EnqueueByID(ctx, fs.Args())
	ids := make([]string, 0, len(results))
	for id, r := range results {
		fmt.Printf("%s: %s\n", id, r)
		if r == "queued" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if err := a.Downloads.Start(ctx); err != nil {
		return err
	}
	sort.Strings(ids)
	for {
		views, err := a.Downloads.List(ctx)
		if err != nil {
			return err
		}
		pending := 0
		var line []string
		for _, v := range views {
			if !contains(ids, v.ResourceID) || v.State == store.JobCancelled {
				continue
			}
			switch v.State {
			case store.JobCompleted:
			case store.JobFailed:
				fmt.Printf("\n%s failed: %s\n", v.ResourceID, v.Error)
				ids = remove(ids, v.ResourceID)
			default:
				pending++
				pct := 0.0
				if v.BytesTotal > 0 {
					pct = 100 * float64(v.BytesDone) / float64(v.BytesTotal)
				}
				line = append(line, fmt.Sprintf("%s %s %.1f%% %s/s", v.ResourceID, v.State, pct, storage.FormatBytes(v.Speed)))
			}
		}
		if pending == 0 {
			fmt.Println("\nDone.")
			return nil
		}
		fmt.Printf("\r%-100s", strings.Join(line, " | "))
		select {
		case <-ctx.Done():
			fmt.Println("\nInterrupted; downloads will resume next time Arcana runs.")
			return nil
		case <-time.After(time.Second):
		}
	}
}

func cmdImport(args []string) error {
	var id, from string
	var allowUnverified, online, userDocs bool
	c, fs, err := parseFlags("import", reorder(args), func(fs *flag.FlagSet) {
		fs.StringVar(&id, "id", "", "")
		fs.StringVar(&from, "from", "", "")
		fs.BoolVar(&allowUnverified, "allow-unverified", false, "")
		fs.BoolVar(&online, "online", false, "")
		fs.BoolVar(&userDocs, "user-documents", false, "")
	})
	if err != nil {
		return err
	}
	if from != "" {
		return importCollection(c, from, fs.Args(), userDocs)
	}
	if fs.NArg() == 0 {
		return errors.New("usage: arcana import FILE... [--id RESOURCE-ID] [--allow-unverified] [--online]")
	}
	a, done, err := openCLI(c)
	if err != nil {
		return err
	}
	defer done()
	failed := 0
	for _, f := range fs.Args() {
		res, err := a.ImportFile(context.Background(), f, app.ImportOptions{ResourceID: id, AllowUnverified: allowUnverified, Resolve: online})
		if err != nil {
			fmt.Printf("%s: %v\n", filepath.Base(f), err)
			failed++
			continue
		}
		fmt.Printf("%s: installed as %s %s\n", filepath.Base(f), res.ID, res.Version)
	}
	if failed > 0 {
		return fmt.Errorf("%d file(s) not imported", failed)
	}
	return nil
}

func importCollection(c *commonFlags, from string, ids []string, userDocs bool) error {
	a, done, err := openCLI(c)
	if err != nil {
		return err
	}
	defer done()
	ctx := context.Background()
	src, err := filepath.Abs(from)
	if err != nil {
		return err
	}
	m, plans, err := a.ScanSource(ctx, src)
	if err != nil {
		return err
	}
	fmt.Printf("Source: %s (%d resources)\n", m.SourceLabel, len(plans))
	for _, p := range plans {
		fmt.Printf("  %-10s %-40s %s\n", p.Action, p.ResourceID, p.Version)
	}
	t, err := a.StartImport(src, ids, userDocs)
	if err != nil {
		return err
	}
	return waitTask(a, t.ID)
}

func cmdExport(args []string) error {
	var to string
	var all, userDocs bool
	c, fs, err := parseFlags("export", reorder(args), func(fs *flag.FlagSet) {
		fs.StringVar(&to, "to", "", "")
		fs.BoolVar(&all, "all", false, "")
		fs.BoolVar(&userDocs, "user-documents", false, "")
	})
	if err != nil {
		return err
	}
	if to == "" {
		return errors.New("usage: arcana export --to DIR [ID...|--all] [--user-documents]")
	}
	a, done, err := openCLI(c)
	if err != nil {
		return err
	}
	defer done()
	ids := fs.Args()
	if all {
		installed, _ := a.Store.ListInstalled(context.Background())
		ids = nil
		for _, in := range installed {
			ids = append(ids, in.ResourceID)
		}
	}
	dest, err := filepath.Abs(to)
	if err != nil {
		return err
	}
	t, err := a.StartExport(ids, dest, userDocs)
	if err != nil {
		return err
	}
	return waitTask(a, t.ID)
}

// waitTask prints progress of a background task until it finishes.
func waitTask(a *app.App, id string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	for {
		t, ok := a.Tasks.Get(id)
		if !ok {
			return errors.New("task disappeared")
		}
		if t.Total > 0 {
			fmt.Printf("\r%-70s %5.1f%%", t.Message, 100*float64(t.Done)/float64(t.Total))
		}
		if t.State != "running" {
			fmt.Println()
			if t.Error != "" {
				return errors.New(t.Error)
			}
			if t.Result != nil {
				out, _ := json.MarshalIndent(t.Result, "", "  ")
				fmt.Println(string(out))
			}
			return nil
		}
		select {
		case <-ctx.Done():
			a.Tasks.Cancel(id)
			fmt.Println("\nCancelled.")
			return nil
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func cmdManifest(args []string) error {
	c, _, err := parseFlags("manifest", args, nil)
	if err != nil {
		return err
	}
	a, done, err := openCLI(c)
	if err != nil {
		return err
	}
	defer done()
	p, err := a.WriteLibraryManifest(context.Background())
	if err != nil {
		return err
	}
	fmt.Println("Wrote", p)
	return nil
}

func cmdUpdate(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: arcana update stage DIR | apply | rollback | status")
	}
	sub := args[0]
	c, fs, err := parseFlags("update", reorder(args[1:]), nil)
	if err != nil {
		return err
	}
	a, done, err := openCLI(c)
	if err != nil {
		return err
	}
	defer done()
	switch sub {
	case "status":
		out, _ := json.MarshalIndent(a.AppUpdateInfo(), "", "  ")
		fmt.Println(string(out))
		return nil
	case "stage":
		if fs.NArg() != 1 {
			return errors.New("usage: arcana update stage DIR")
		}
		m, err := a.StageAppUpdateFromDir(context.Background(), fs.Arg(0))
		if err != nil {
			return err
		}
		fmt.Printf("Verified and staged version %s. Run: arcana update apply\n", m.Version)
		return nil
	case "apply":
		st, err := a.Updater.Apply()
		if err != nil {
			return err
		}
		fmt.Printf("Installed version %s (%d files). Start Arcana again to use it.\n", st.AppliedVersion, len(st.AppliedFiles))
		return nil
	case "rollback":
		if _, err := a.Updater.Rollback(); err != nil {
			return err
		}
		fmt.Println("Restored the previous version.")
		return nil
	}
	return fmt.Errorf("unknown update command %q", sub)
}

func cmdVolumes() error {
	vols, err := storage.ListVolumes()
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "MOUNT\tFS\tSIZE\tFREE\tREMOVABLE\tLIBRARY")
	for _, v := range vols {
		rem := "unknown"
		if v.Removable != nil {
			rem = map[bool]string{true: "yes", false: "no"}[*v.Removable]
		}
		lib := ""
		if root, ok := library.Find(v.MountPoint, 0); ok {
			lib = root
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", v.MountPoint, v.FSType, storage.FormatBytes(int64(v.TotalBytes)),
			storage.FormatBytes(int64(v.AvailBytes)), rem, lib)
	}
	return tw.Flush()
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func remove(list []string, s string) []string {
	var out []string
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

func cmdHotspot(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: arcana hotspot start --ssid NAME --password PW [--device DEV] | stop | status")
	}
	m := hotspot.New()
	ctx := context.Background()
	switch args[0] {
	case "status":
		st := m.Status(ctx)
		if !st.Supported {
			fmt.Println("Hotspot not available:", st.Reason)
			return nil
		}
		state := "off"
		if st.Active {
			state = "on (" + st.Device + ")"
		}
		fmt.Printf("Hotspot: %s\nNetwork name: %s\nWi-Fi devices: %s\n", state, st.SSID, strings.Join(st.Devices, ", "))
		return nil
	case "stop":
		return m.Stop(ctx)
	case "start":
		fs := flag.NewFlagSet("hotspot", flag.ContinueOnError)
		ssid := fs.String("ssid", "Arcana", "")
		pw := fs.String("password", "", "")
		dev := fs.String("device", "", "")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if err := m.Start(ctx, *dev, *ssid, *pw); err != nil {
			return err
		}
		fmt.Printf("Hotspot %q is on. Start Arcana with --lan so phones that join it can open the library.\n", *ssid)
		return nil
	}
	return fmt.Errorf("unknown hotspot action %q", args[0])
}

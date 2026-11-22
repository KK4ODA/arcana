// Command arcana is the portable Arcana application: a local web server for
// an offline knowledge library stored on a removable drive.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/KK4ODA/arcana/internal/app"
	"github.com/KK4ODA/arcana/internal/buildinfo"
	"github.com/KK4ODA/arcana/internal/library"
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
  arcana volumes                List drives and their file systems
  arcana version                Print version information

Common flags:
  --library DIR   Library folder (default: found next to the program)
  --port N        Application port (default 8470; content uses N+1)
  --lan           Share the library with other devices on the network (read-only)
  --no-browser    Do not open a browser window
  --read-only     Open the library without modifying it
  --kiwix PATH    Path to kiwix-serve
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
	case "volumes":
		err = cmdVolumes()
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
}

func parseFlags(name string, args []string, extra func(*flag.FlagSet)) (*commonFlags, *flag.FlagSet, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	c := &commonFlags{}
	fs.StringVar(&c.library, "library", "", "")
	fs.IntVar(&c.port, "port", 0, "")
	fs.BoolVar(&c.lan, "lan", false, "")
	fs.BoolVar(&c.noOpen, "no-browser", false, "")
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	openApp := func(r string) (*app.App, error) {
		a, err := app.Open(r, app.Options{KiwixBinary: c.kiwix, ForceReadOnly: c.readOnly, Log: log})
		if err != nil {
			return nil, err
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
		Port: port, ShareOnLAN: lan, Log: log,
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
	err = srv.Serve(ctx, la, lc)
	log.Info("shutting down")
	if cur := srv.App(); cur != nil {
		cur.Close()
	}
	return err
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

// reorder moves flags before positional arguments so "init DIR --label X"
// works as users expect.
func reorder(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		if strings.HasPrefix(args[i], "-") {
			flags = append(flags, args[i])
			if !strings.Contains(args[i], "=") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") &&
				args[i] != "--full" && args[i] != "--lan" && args[i] != "--read-only" && args[i] != "--no-browser" {
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

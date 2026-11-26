package apps

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/KK4ODA/arcana/internal/fsutil"
	"golang.org/x/crypto/bcrypt"
)

// The native apps: programs Arcana starts on this computer, on loopback,
// and reaches through its proxy. Each keeps its data in Env.Data on the
// drive (ADR-0030).

func init() {
	register(
		&Spec{
			ID: "filebrowser", Title: "File Browser", Kind: KindService, Slot: 7,
			Summary: "Upload, download, organize and share files in a folder on the drive.",
			Parts:   map[string]Part{"filebrowser": {}},
			Service: &ServiceSpec{
				Command:   fileBrowser,
				ReadyPath: "/health",
				AdminUser: "admin",
				FirstUse:  "Sign in as admin with the password below. The files are in the Arcana/user/files folder on the drive.",
			},
		},
		&Spec{
			ID: "homebox", Title: "Homebox", Kind: KindService, Slot: 8,
			Summary: "An inventory of what you own and store: locations, quantities, photos, warranties.",
			Parts:   map[string]Part{"homebox": {}},
			Service: &ServiceSpec{
				Command:   homebox,
				ReadyPath: "/api/v1/status",
				FirstUse:  "Create the first account on Homebox's own sign-in page.",
			},
		},
		&Spec{
			ID: "vaultwarden", Title: "Vaultwarden", Kind: KindService, Slot: 9,
			Summary:    "A password manager that Bitwarden's apps and browser extensions can use.",
			Parts:      map[string]Part{"vaultwarden": {}, "web-vault": {}},
			NeedsHTTPS: "The web vault and Bitwarden's apps accept only HTTPS addresses, even on the computer running Arcana. Turn on HTTPS on Arcana's Server page, then open Vaultwarden from here.",
			Service: &ServiceSpec{
				Command:     vaultwarden,
				ReadyPath:   "/alive",
				FirstUse:    "Create an account on the web vault's own page; untick \"Check known data breaches\", which needs the internet and otherwise stops the sign-up. Bitwarden's apps can then use this address as a self-hosted server.",
				CrossOrigin: true,
			},
		},
		&Spec{
			ID: "jellyfin", Title: "Jellyfin", Kind: KindService, Slot: 10,
			Summary: "A media server for your own films, series, music and photos.",
			Parts:   map[string]Part{"jellyfin": {}, "ffmpeg": {NotNeededOn: []string{"windows-amd64"}}},
			Service: &ServiceSpec{
				Command:      jellyfin,
				ReadyPath:    "/health",
				StartTimeout: 5 * time.Minute,
				FirstUse:     "Jellyfin's setup asks for an administrator account and your media folders: Arcana/user/files on the drive is where File Browser puts uploads.",
				CrossOrigin:  true,
			},
		},
		&Spec{
			ID: "calibre-web", Title: "Calibre-Web", Kind: KindService, Slot: 11,
			Summary: "An e-book library: upload, organize and read books in the browser.",
			Parts:   map[string]Part{"python": {}, "pypi": {Copy: true}, "library": {Copy: true}},
			Service: &ServiceSpec{
				Command:      calibreWeb,
				ReadyPath:    "/login",
				StartTimeout: 5 * time.Minute,
				AdminUser:    "admin",
				FirstUse:     "Sign in as admin with the password below. The books are in Arcana/user/apps/calibre-web/library on the drive.",
			},
		},
		&Spec{
			ID: "stirling-pdf", Title: "Stirling PDF", Kind: KindService, Slot: 12,
			Summary: "Merge, split, compress, convert, sign and redact PDF files.",
			Parts:   map[string]Part{"stirling-pdf": {Copy: true}, "jre": {}},
			Service: &ServiceSpec{
				Command:      stirlingPDF,
				ReadyPath:    "/api/v1/info/status",
				StartTimeout: 5 * time.Minute,
				FirstUse:     "No account is needed. Files you open are processed on the computer running Arcana and are not kept.",
			},
		},
		&Spec{
			ID: "flatnotes", Title: "Flatnotes", Kind: KindService, Slot: 14,
			Summary: "Notes in Markdown, kept as plain files on the drive, with full-text search.",
			Parts:   map[string]Part{"python": {}, "pypi": {Copy: true}, "flatnotes": {}},
			Service: &ServiceSpec{
				Command:   flatnotes,
				ReadyPath: "/health",
				AdminUser: "admin",
				FirstUse:  "Sign in as admin with the password below. Each note is a Markdown file in Arcana/user/apps/flatnotes on the drive.",
			},
		},
	)
}

// fileBrowser serves Env.Files. At its first start it creates the
// administrator with Arcana's password (it takes a bcrypt hash).
func fileBrowser(env Env) (Command, error) {
	db := filepath.Join(env.Data, "filebrowser.db")
	args := []string{"--address", "127.0.0.1", "--port", strconv.Itoa(env.Port), "--database", db,
		"--root", env.Files, "--cacheDir", env.Cache, "--log", "stdout"}
	if _, err := os.Stat(db); errors.Is(err, os.ErrNotExist) {
		hash, err := bcrypt.GenerateFromPassword([]byte(env.Password), bcrypt.DefaultCost)
		if err != nil {
			return Command{}, err
		}
		args = append(args, "--username", "admin", "--password", string(hash))
	}
	return Command{Path: filepath.Join(env.Runtime, "filebrowser", env.Exe("filebrowser")), Args: args}, nil
}

// homebox keeps its database and photos in .data under its working
// folder. It refuses to start without a secret of at least 32 bytes for
// its API keys; Arcana makes one at the first start and keeps it with the
// data, since changing it invalidates every key issued.
func homebox(env Env) (Command, error) {
	pepper, err := secretFile(filepath.Join(env.Data, "arcana-api-key-pepper"))
	if err != nil {
		return Command{}, err
	}
	return Command{Path: filepath.Join(env.Runtime, "homebox", env.Exe("homebox")), Env: []string{"HBOX_AUTH_API_KEY_PEPPER=" + pepper}, Args: []string{
		"--mode", "production", "--web-host", "127.0.0.1", "--web-port", strconv.Itoa(env.Port),
		"--options-github-release-check=false", "--options-allow-analytics=false", "--options-trust-proxy=true",
		"--web-max-upload-size", "100",
	}}, nil
}

// vaultwarden is the static Linux program from the official image, with
// the web vault beside it.
func vaultwarden(env Env) (Command, error) {
	return Command{Path: filepath.Join(env.Runtime, "vaultwarden", "vaultwarden"), Env: []string{
		"ROCKET_ADDRESS=127.0.0.1", "ROCKET_PORT=" + strconv.Itoa(env.Port),
		"DATA_FOLDER=" + env.Data, "WEB_VAULT_FOLDER=" + filepath.Join(env.Runtime, "web-vault", "web-vault"),
		"WEB_VAULT_ENABLED=true",
		// Site icons would be fetched from the internet for every login.
		"DISABLE_ICON_DOWNLOAD=true", "ICON_SERVICE=internal",
		// Arcana's proxy is the only client and passes the browser's
		// address on.
		"IP_HEADER=X-Forwarded-For",
	}}, nil
}

// jellyfin keeps configuration and its database on the drive and its
// transcoding cache on this computer. Its port is in network.xml, which
// Arcana writes before each start.
func jellyfin(env Env) (Command, error) {
	config := filepath.Join(env.Data, "config")
	if err := writeJellyfinNetwork(filepath.Join(config, "network.xml"), env.Port); err != nil {
		return Command{}, fmt.Errorf("prepare Jellyfin's network settings: %w", err)
	}
	server := filepath.Join(env.Runtime, "jellyfin", "jellyfin")
	ffmpeg := filepath.Join(env.Runtime, "ffmpeg", "ffmpeg")
	if strings.HasPrefix(env.Platform, "windows-") {
		ffmpeg = filepath.Join(server, "ffmpeg.exe") // the Windows build carries its own
	}
	return Command{Path: filepath.Join(server, env.Exe("jellyfin")), Args: []string{
		"--datadir", filepath.Join(env.Data, "data"), "--configdir", config,
		"--cachedir", env.Cache, "--logdir", filepath.Join(env.Data, "log"),
		"--webdir", filepath.Join(server, "jellyfin-web"), "--ffmpeg", ffmpeg, "--nonetchange",
	}}, nil
}

// jellyfinNetwork are the settings Arcana owns in Jellyfin's network.xml:
// loopback only, on Arcana's port, no discovery or port mapping on the
// network, and Arcana's proxy trusted for the browser's address.
func jellyfinNetwork(port int) [][2]string {
	p := strconv.Itoa(port)
	return [][2]string{
		{"InternalHttpPort", p}, {"PublicHttpPort", p}, {"EnableHttps", "false"},
		{"LocalNetworkAddresses", "<string>127.0.0.1</string>"},
		{"KnownProxies", "<string>127.0.0.1</string>"},
		{"AutoDiscovery", "false"}, {"EnableUPnP", "false"}, {"EnableIPv6", "false"},
		{"EnableRemoteAccess", "true"},
		// Tell clients the address they used, not Arcana's loopback one.
		{"EnablePublishedServerUriByRequest", "true"},
	}
}

// writeJellyfinNetwork sets Arcana's settings in network.xml and keeps the
// others the owner chose in Jellyfin.
func writeJellyfinNetwork(path string, port int) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		data = []byte("<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<NetworkConfiguration xmlns:xsi=\"http://www.w3.org/2001/XMLSchema-instance\" xmlns:xsd=\"http://www.w3.org/2001/XMLSchema\">\n</NetworkConfiguration>\n")
	} else if err != nil {
		return err
	}
	s := string(data)
	end := strings.LastIndex(s, "</NetworkConfiguration>")
	if end < 0 {
		return errors.New("network.xml has no NetworkConfiguration element")
	}
	for _, kv := range jellyfinNetwork(port) {
		name, value := kv[0], kv[1]
		elem := regexp.MustCompile(`(?s)<` + name + `\s*/>|<` + name + `>.*?</` + name + `>`)
		repl := "<" + name + ">" + value + "</" + name + ">"
		if elem.MatchString(s) {
			s = elem.ReplaceAllLiteralString(s, repl)
		} else {
			end = strings.LastIndex(s, "</NetworkConfiguration>")
			s = s[:end] + "  " + repl + "\n" + s[end:]
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(s), 0o644)
}

// calibreWeb installs Calibre-Web's Python packages on this computer at
// the first start (offline, from the wheels on the drive), seeds an empty
// Calibre library, and runs Calibre-Web through a bootstrap that points
// it at Arcana's port and that library.
func calibreWeb(env Env) (Command, error) {
	py, site, err := pythonApp(env, "Calibre-Web", "calibreweb")
	if err != nil {
		return Command{}, err
	}
	library := filepath.Join(env.Data, "library")
	if err := os.MkdirAll(library, 0o755); err != nil {
		return Command{}, err
	}
	if _, err := os.Stat(filepath.Join(library, "metadata.db")); errors.Is(err, os.ErrNotExist) {
		if err := copyFile(filepath.Join(env.Runtime, "library", "metadata.db"), filepath.Join(library, "metadata.db")); err != nil {
			return Command{}, fmt.Errorf("create the Calibre library: %w", err)
		}
	}
	c := Command{Path: py, Args: []string{"-I", "-X", "utf8", "-c", calibreBootstrap, "-i", "127.0.0.1"},
		DropEnv: pythonVars,
		Env: []string{"ARCANA_SITE=" + site, "ARCANA_PORT=" + strconv.Itoa(env.Port), "ARCANA_LIBRARY=" + library,
			"CALIBRE_DBPATH=" + env.Data, "CACHE_DIRECTORY=" + env.Cache},
	}
	// Calibre-Web creates "admin" with the published password admin123.
	// Before the first start Arcana replaces it with its own, using
	// Calibre-Web's -s option, which sets a password and exits.
	marker := filepath.Join(env.Data, "arcana-password-set")
	if _, err := os.Stat(marker); errors.Is(err, os.ErrNotExist) {
		env.Progress("Setting Calibre-Web's administrator password")
		cmd := exec.Command(c.Path, "-I", "-X", "utf8", "-c", calibreBootstrap, "-s", "admin:"+env.Password)
		cmd.Dir = env.Data
		cmd.Env = environment(c.DropEnv, c.Env)
		configureProcess(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			return Command{}, fmt.Errorf("set Calibre-Web's administrator password: %v: %s", err, errorLines(string(out)))
		}
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			return Command{}, err
		}
	}
	return c, nil
}

// calibreBootstrap runs Calibre-Web with its settings database's port and
// library set to Arcana's on every start: Calibre-Web reads both from
// that database, and has no option for the library.
const calibreBootstrap = `import os, sys
site = os.environ["ARCANA_SITE"]
sys.path[:0] = [site, os.path.join(site, "calibreweb")]
import cps.config_sql as cs
_load = cs.load_configuration
def load_configuration(session, secret_key):
    _load(session, secret_key)
    s = session.query(cs._Settings).first()
    s.config_port = int(os.environ["ARCANA_PORT"])
    s.config_calibre_dir = os.environ["ARCANA_LIBRARY"]
    s.config_use_google_drive = False
    session.commit()
cs.load_configuration = load_configuration
sys.argv = ["calibre-web"] + sys.argv[1:]
from cps.main import main
main()
`

// pythonVars are Python's and pip's own settings (a PYTHONPATH, a pip
// configuration), left out of the environment so the runtime on the drive
// behaves the same on every computer.
var pythonVars = []string{"PYTHON", "PIP_"}

// pythonApp returns the Python runtime of an app and the folder its
// packages are installed in, installing them at the first start on this
// computer.
func pythonApp(env Env, title string, packages ...string) (py, site string, err error) {
	py = filepath.Join(env.Runtime, "python", "python", "bin", "python3")
	if strings.HasPrefix(env.Platform, "windows-") {
		py = filepath.Join(env.Runtime, "python", "python", "python.exe")
	}
	site = filepath.Join(env.Runtime, "site")
	if _, err := os.Stat(filepath.Join(site, completeMarker)); err != nil {
		env.Progress("Installing " + title + "'s Python packages on this computer")
		if err := pipInstall(py, filepath.Join(env.Runtime, "pypi"), site, packages...); err != nil {
			return "", "", fmt.Errorf("install %s's packages: %w", title, err)
		}
	}
	return py, site, nil
}

// pipInstall installs packages and their dependencies with the runtime's
// own pip from wheels on the drive, without the network: pip chooses the
// files for this computer. Packages published only as source (two of
// Calibre-Web's) are built with the runtime's setuptools.
func pipInstall(py, wheels, site string, packages ...string) error {
	tmp := site + ".installing"
	_ = os.RemoveAll(tmp)
	cmd := exec.Command(py, "-I", "-m", "pip", "install", "--no-index", "--find-links", wheels,
		"--no-build-isolation", "--no-cache-dir", "--disable-pip-version-check", "--no-warn-script-location",
		"--target", tmp)
	cmd.Args = append(cmd.Args, packages...)
	cmd.Env = environment(pythonVars, nil)
	configureProcess(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(tmp)
		return fmt.Errorf("%v: %s", err, errorLines(string(out)))
	}
	if err := os.WriteFile(filepath.Join(tmp, completeMarker), nil, 0o644); err != nil {
		return err
	}
	_ = os.RemoveAll(site)
	return os.Rename(tmp, site)
}

// stirlingPDF runs Stirling-PDF.jar with Temurin's Java. It keeps its
// settings and logs in its working folder; login, analytics and update
// checks are off.
func stirlingPDF(env Env) (Command, error) {
	java, err := findJava(filepath.Join(env.Runtime, "jre"), env)
	if err != nil {
		return Command{}, err
	}
	jars, _ := filepath.Glob(filepath.Join(env.Runtime, "stirling-pdf", "*.jar"))
	if len(jars) != 1 {
		return Command{}, errors.New("Stirling PDF's program (a .jar file) is missing")
	}
	return Command{Path: java, Args: []string{"-jar", jars[0]}, DropEnv: []string{"JAVA_TOOL_OPTIONS", "JDK_JAVA_OPTIONS", "_JAVA_OPTIONS"}, Env: []string{
		"SERVER_ADDRESS=127.0.0.1", "SERVER_PORT=" + strconv.Itoa(env.Port),
		"SECURITY_ENABLELOGIN=false", "DISABLE_ADDITIONAL_FEATURES=true",
		"SYSTEM_ENABLEANALYTICS=false", "SYSTEM_SHOWUPDATE=false", "SYSTEM_SHOWUPDATEONLYADMIN=true",
		"SERVER_FORWARDHEADERSSTRATEGY=NATIVE",
	}}, nil
}

// secretFile returns the secret kept in path, creating it (48 random
// bytes, base64) the first time.
func secretFile(path string) (string, error) {
	if data, err := os.ReadFile(path); err == nil && len(strings.TrimSpace(string(data))) >= 32 {
		return strings.TrimSpace(string(data)), nil
	}
	b := make([]byte, 48)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	s := base64.StdEncoding.EncodeToString(b)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := fsutil.WriteFileAtomic(path, []byte(s+"\n"), 0o600); err != nil {
		return "", err
	}
	return s, nil
}

// findJava finds the java program in an unpacked Temurin archive, whose
// top folder is named after the release (macOS adds Contents/Home).
func findJava(dir string, env Env) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		for _, p := range []string{filepath.Join(dir, e.Name(), "bin", env.Exe("java")), filepath.Join(dir, e.Name(), "Contents", "Home", "bin", "java")} {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	return "", errors.New("the Java runtime has no java program for this computer")
}

// flatnotes runs Flatnotes' server (FastAPI, under uvicorn) from the
// official image's layers, with its Python packages installed from the
// drive like Calibre-Web's. Its notes are Markdown files in its data
// folder. It signs in one user: Arcana's administrator password, with a
// secret for its sessions kept with the data. Flatnotes writes its base
// address into client/dist/index.html at every start, so it runs in its
// unpacked folder on this computer.
func flatnotes(env Env) (Command, error) {
	py, site, err := pythonApp(env, "Flatnotes",
		"whoosh", "fastapi", "uvicorn[standard]", "aiofiles", "python-jose[cryptography]", "pyotp", "qrcode", "python-multipart")
	if err != nil {
		return Command{}, err
	}
	secret, err := secretFile(filepath.Join(env.Data, ".arcana-secret-key"))
	if err != nil {
		return Command{}, err
	}
	app := filepath.Join(env.Runtime, "flatnotes", "app")
	return Command{Path: py, Dir: app, DropEnv: append([]string{"FLATNOTES_"}, pythonVars...),
		Args: []string{"-I", "-X", "utf8", "-c", flatnotesBootstrap, "main:app", "--app-dir", filepath.Join(app, "server"),
			"--host", "127.0.0.1", "--port", strconv.Itoa(env.Port), "--proxy-headers", "--forwarded-allow-ips", "127.0.0.1"},
		Env: []string{"ARCANA_SITE=" + site, "FLATNOTES_PATH=" + env.Data, "FLATNOTES_AUTH_TYPE=password",
			"FLATNOTES_USERNAME=admin", "FLATNOTES_PASSWORD=" + env.Password, "FLATNOTES_SECRET_KEY=" + secret},
	}, nil
}

// flatnotesBootstrap runs uvicorn from the installed packages; -I keeps
// them off the path otherwise.
const flatnotesBootstrap = `import os, sys, runpy
sys.path.insert(0, os.environ["ARCANA_SITE"])
sys.argv = ["uvicorn"] + sys.argv[1:]
runpy.run_module("uvicorn", run_name="__main__", alter_sys=True)
`

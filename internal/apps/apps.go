// Package apps runs the web applications Arcana offers in its catalog
// (ADR-0030): static web apps it serves itself, and native programs it
// supervises. Each app is one catalog resource, "arcana.app.<id>", whose
// files are unpacked on each computer and served on a browser origin of
// the app's own.
package apps

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ResourcePrefix starts the catalog ID of every app.
const ResourcePrefix = "arcana.app."

// Kinds of app.
const (
	// KindWeb is a prebuilt web application: files Arcana serves as they
	// are, with no program of its own.
	KindWeb = "web"
	// KindService is a program Arcana starts on this computer, listening
	// on loopback, and reaches through a proxy.
	KindService = "service"
)

// FirstSlot is the offset from Arcana's application port of the first
// app's port. P+1 is the content origin and P+2 the classroom.
const FirstSlot = 3

// Spec says how Arcana runs one app. The catalog resource holds the files;
// the spec, compiled into Arcana, says what to do with them, so a catalog
// cannot make Arcana run anything it was not built to run.
type Spec struct {
	ID    string
	Title string
	// Summary is one line for the Apps page.
	Summary string
	Kind    string
	// Slot is the app's port, as an offset from the application port.
	// It never changes, so the browser keeps the app's saved data (which
	// belongs to its origin, port included).
	Slot int
	// Parts says how each part of the resource's files unpacks.
	Parts map[string]Part
	// Root (web apps) is the folder, inside the unpacked runtime, holding
	// the site; Index is the page served for "/".
	Root  string
	Index string
	// SPA (web apps) serves the index page for paths that name no file,
	// for apps that route in the browser.
	SPA bool
	// Extra (web apps) serves files from outside the site at fixed paths:
	// URL path to a path in the unpacked folder.
	Extra map[string]string
	// Rewrites (web apps) change the text of a site's files as they are
	// served; the files on the drive stay as published and verified.
	Rewrites []Rewrite
	// SecureContext explains, when set, why the app works only in a
	// secure context: on the computer running Arcana, or over HTTPS.
	SecureContext string
}

// Part says how the files of one part unpack: archives are extracted
// into a folder named after the part, other files are copied into it.
type Part struct {
	// Copy keeps archives as they are (Python packages for pip).
	Copy bool
	// Gunzip decompresses every "name.gz" in the archive to "name", for
	// sites published only as precompressed files.
	Gunzip bool
}

// Rewrite replaces Old with New in the site file Path ("/index.html") as
// it is served. A file without Old is served as it is.
type Rewrite struct {
	Path, Old, New string
}

// ResourceID returns the catalog ID of the app.
func (s *Spec) ResourceID() string { return ResourcePrefix + s.ID }

// Port returns the app's port for an application port.
func (s *Spec) Port(appPort int) int { return appPort + s.Slot }

var registry = map[string]*Spec{}

// register adds specs at start-up; IDs and slots must be unique.
func register(specs ...*Spec) {
	for _, s := range specs {
		if _, dup := registry[s.ID]; dup {
			panic("apps: duplicate app " + s.ID)
		}
		for _, o := range registry {
			if o.Slot == s.Slot {
				panic(fmt.Sprintf("apps: %s and %s share slot %d", s.ID, o.ID, s.Slot))
			}
		}
		if s.Slot < FirstSlot {
			panic("apps: slot below FirstSlot for " + s.ID)
		}
		registry[s.ID] = s
	}
}

// Lookup returns the spec of an app ID ("cyberchef") or resource ID
// ("arcana.app.cyberchef").
func Lookup(id string) (*Spec, bool) {
	s, ok := registry[strings.TrimPrefix(id, ResourcePrefix)]
	return s, ok
}

// All returns every app Arcana can run, in port order.
func All() []*Spec {
	out := make([]*Spec, 0, len(registry))
	for _, s := range registry {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

// MaxSlot is the highest slot in use, so callers know how many ports
// beyond the application port the apps may take.
func MaxSlot() int {
	m := 0
	for _, s := range registry {
		if s.Slot > m {
			m = s.Slot
		}
	}
	return m
}

// FileName is the parsed name of a resource file: part_platform_rest.
type FileName struct {
	// Part names what the file holds ("jellyfin", "ffmpeg", "pypi").
	Part string
	// Platform is "any" or GOOS-GOARCH ("linux-arm64").
	Platform string
	// Rest is the version and extension, or the name the file must keep
	// (a Python package).
	Rest string
}

// ParseFileName splits a resource file name.
func ParseFileName(name string) (FileName, error) {
	parts := strings.SplitN(name, "_", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return FileName{}, fmt.Errorf("app file %q is not named part_platform_rest", name)
	}
	return FileName{Part: parts[0], Platform: parts[1], Rest: parts[2]}, nil
}

// ErrNotForPlatform means an app has no build for this computer.
var ErrNotForPlatform = errors.New("no build for this computer")

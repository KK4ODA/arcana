// Package buildinfo exposes version information injected at build time with
// -ldflags "-X github.com/KK4ODA/arcana/internal/buildinfo.Version=...".
package buildinfo

import "runtime"

var (
	// Version is the release version, or "dev" for local builds.
	Version = "0.1.0-dev"
	// Commit is the source revision.
	Commit = "unknown"
	// Date is the build date (UTC, RFC 3339).
	Date = "unknown"
)

// Platform returns the directory name used for this build's binaries inside
// a library's bin/ folder, e.g. "windows-amd64".
func Platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

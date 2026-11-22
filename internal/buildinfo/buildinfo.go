// Package buildinfo exposes version information injected at build time with
// -ldflags "-X github.com/KK4ODA/arcana/internal/buildinfo.Version=...".
package buildinfo

import "runtime"

var (
	// Version is the release version, or "dev" for local builds.
	Version = "0.4.0-dev"
	// Commit is the source revision.
	Commit = "unknown"
	// Date is the build date (UTC, RFC 3339).
	Date = "unknown"
	// UpdateKeys lists base64 Ed25519 public keys trusted to sign release
	// manifests, comma-separated. Release builds set it with -ldflags;
	// development builds have none and cannot verify updates.
	UpdateKeys = ""
)

// Platform returns the directory name used for this build's binaries inside
// a library's bin/ folder, e.g. "windows-amd64".
func Platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

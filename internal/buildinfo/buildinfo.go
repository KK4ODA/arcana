// Package buildinfo exposes version information injected at build time with
// -ldflags "-X github.com/KK4ODA/arcana/internal/buildinfo.Version=...".
package buildinfo

import "runtime"

var (
	// Version is the release version, or "dev" for local builds.
	Version = "0.6.0-dev"
	// Commit is the source revision.
	Commit = "unknown"
	// Date is the build date (UTC, RFC 3339).
	Date = "unknown"
	// UpdateKeys lists base64 Ed25519 public keys trusted to sign release
	// manifests, comma-separated. The default is the Arcana project's
	// release key (its private half is held by the release workflow as the
	// ARCANA_SIGNING_KEY secret); -ldflags can replace it, for example to
	// add a second key while rotating.
	UpdateKeys = "CdoqUrdB5pKjARf+h8sMt/by5csdn6ZrkzbzSV0N3XY="
)

// Platform returns the directory name used for this build's binaries inside
// a library's bin/ folder, e.g. "windows-amd64".
func Platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// Package web embeds Arcana's browser interface. All assets are served from
// the binary; the interface makes no requests to any other host.
package web

import "embed"

// Static holds the files under static/.
//
//go:embed static
var Static embed.FS

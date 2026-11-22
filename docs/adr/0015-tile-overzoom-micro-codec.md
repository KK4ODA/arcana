# ADR-0015: Derive missing tiles from ancestors with a small MVT codec

- Status: Accepted
- Date: 2026-11-22

## Context
Users combine a world overview (zoom 6 or 7) with detailed regions (zoom 13
or 15). MapLibre takes one maximum zoom per source, so outside the detailed
region the map went blank past the overview's zoom. Options:

1. One MapLibre source per map set, each with its own layers. Labels and
   roads would be drawn twice where sets overlap, and the style would grow
   with every installed map.
2. Server-side overzoom: when no set has a tile, scale and clip the nearest
   ancestor tile into the requested tile.

For option 2, `github.com/paulmach/orb` (MIT) offers an MVT codec and
clipping, but it pulls in `github.com/gogo/protobuf` (deprecated and
flagged by vulnerability scanners) and the MongoDB BSON driver.

## Decision
Implement option 2 with a focused codec in `internal/maps/overzoom.go`
(about 350 lines): it rewrites only geometry (Liang–Barsky for lines,
Sutherland–Hodgman for polygon rings, keeping ring orientation and dropping
holes whose outer ring was clipped away) and copies each layer's key and
value tables, feature IDs and tags byte-for-byte. Derived tiles are cached
(512 tiles) and limited to 10 levels below the ancestor.

## Consequences
- Overlapping maps look seamless at every zoom with a single source.
- No new dependencies. The codec handles untrusted tiles defensively and is
  covered by unit tests, including real Protomaps tiles.
- Derived tiles carry only the detail of their ancestor; the map is honest
  about this because labels and features simply stay sparse there.

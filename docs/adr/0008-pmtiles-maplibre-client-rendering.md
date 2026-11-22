# ADR-0008: PMTiles and MapLibre with client-side rendering (Phase 3)

- Status: Accepted (implementation planned)
- Date: 2026-11-22

## Context
Offline maps must be compact, regionally downloadable, verifiable, and
render on phones served by a Raspberry Pi.

## Decision
- Store vector basemaps as PMTiles v3 using the Protomaps v4 schema.
- Vendor the style, glyphs (Noto, OFL) and sprites (MIT).
- Render in the browser with vendored MapLibre GL JS v6, with a v5 build
  for devices without WebGL2.
- Serve tiles with a small in-house PMTiles reader rather than go-pmtiles,
  whose package brings cloud SDKs (about 28 MB). Range-request serving
  remains possible.
- Publish regional extracts with SHA-256 through the normal pipeline.
- Show OSM attribution at all times.

## Consequences
The host does no rendering, which scales to many clients. Updating a region
means downloading a new extract (no in-place updates); MBTiles stays
available for user-editable overlays.

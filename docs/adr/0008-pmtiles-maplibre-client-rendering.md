# ADR-0008: PMTiles and MapLibre with client-side rendering

- Status: Accepted; implemented in 0.3.0 (revised from the Phase 1 plan)
- Date: 2026-11-22

## Context
Offline maps must be compact, regionally downloadable, verifiable, and
render on phones served by a Raspberry Pi.

## Decision
- Store vector basemaps as PMTiles v3 using the Protomaps basemap schema.
- Vendor the style layers (Protomaps basemaps, BSD-3 code / CC0 design),
  glyphs (Noto, OFL) and sprites into the binary.
- Render in the browser with vendored MapLibre GL JS 5 (CSP build, so the
  strict content security policy stays unchanged apart from `blob:` images).
- Serve tiles with a small in-house PMTiles reader rather than go-pmtiles,
  whose package brings cloud SDKs (about 28 MB).
- Serve every installed map as one merged tile pyramid; the most detailed
  set covering a tile answers.
- Regions are extracted on demand from the newest Protomaps planet build
  with HTTP range requests, through the normal download manager (an
  "extractor" hook), rather than downloaded as pre-built files. Region
  sizes are measured once by `tools/map-regions` and bundled.
- Show OSM attribution at all times.

## Consequences
- The host does no rendering, which scales to many clients.
- No maintainer infrastructure is needed to publish regions, and every
  download comes from the newest build. In exchange there is no publisher
  checksum: extractions are pinned to one build by ETag, structure-checked,
  and labelled unverified, with their SHA-256 recorded for later checks.
- Protomaps keeps planet builds only for a limited time; an extraction
  interrupted across a build deletion restarts from the newest build.
- Updating a region means extracting it again; MBTiles remains possible for
  user-editable overlays.
- MapLibre 5 needs WebGL2; devices without it cannot show maps yet.

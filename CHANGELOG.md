# Changelog

All notable changes are recorded here. Versions follow semantic versioning;
the library format version is tracked separately (docs/design/09-storage-and-data.md).

## Unreleased — 0.3.0 (Phase 3: offline maps and GPS)

### Added
- **Maps** view with offline vector maps rendered in the browser by
  MapLibre GL JS 5 (vendored; no CDN), Protomaps light and dark styles,
  bundled Noto glyphs and sprites, and permanent OpenStreetMap attribution.
- A `maps` catalog provider with 541 regions: the world (zoom 6 and 7), six
  continents, 241 countries and 293 states and provinces at overview,
  standard and detailed levels, with sizes measured against a real planet
  build and bundled, so regions can be browsed offline.
- Region downloads extract only the needed tiles from the newest Protomaps
  planet build with HTTP range requests. They run in the download manager,
  resume after interruptions, restart cleanly if the build changes or is
  deleted, and record a SHA-256 for later integrity checks.
- "Download this area": measure the visible map area against the newest
  build (exact size before downloading) and queue it.
- Overlapping maps are merged into one map; where only a low-detail map
  exists, tiles are derived from it so zooming in never shows a blank map.
- Offline place search from the installed maps' place names.
- Position from USB/serial GPS receivers, gpsd, NMEA over UDP or TCP, a
  simulator that replays GPX tracks as NMEA, or a fixed position, with
  current/stale/lost states, live updates, coordinates in decimal degrees,
  DMS and Maidenhead locator, follow mode, and a dashboard readout.
- Waypoints (click the map or save the current position), track recording
  to GPX, GPX import and export.
- Settings → Maps and position.
- Acceptance steps 5 (offline maps) and 6 (simulated GPS and a UDP receiver)
  are implemented and pass without internet.

### Changed
- Resources marked as multilingual (`mul`), such as maps, now appear under
  every language filter in Discover.
- The content security policy allows `blob:` images and same-origin workers
  for the map renderer.

### Security
- Position, waypoints and tracks are shown to network clients only when the
  host turns on "Share position" (off by default). Planning a map area,
  which contacts the map source, is a host-only action.

## 0.2.0 (Phase 2: content management and updates)

### Added
- User data in `user/` as plain, human-readable files: bookmarks, favorites,
  reading history and notes (Markdown with a small header). Bookmarks point at
  a page inside a resource rather than an edition, so they survive updates.
  Deleted notes and documents move to recoverable folders.
- Reader integration: archive pages report their location to the reader, which
  offers Bookmark and a Notes panel and records history.
- Your documents: upload or copy files into `user/documents`; PDF, EPUB,
  HTML, Markdown and text are extracted and indexed (SQLite FTS5,
  `index/documents.db`, rebuildable). Installed PDF and EPUB resources are
  indexed too. One search box returns archive articles and documents; documents
  have an in-app text preview.
- Collection transfer between drives with no network: export, import from an
  export or directly from another library, identical content skipped,
  every copy verified against the recorded SHA-256, user documents merged
  without overwriting. `arcana export`, `arcana import --from`, Settings →
  Transfer. Portable library manifest (`arcana manifest`).
- Signed application updates: Ed25519-signed `release.json`, staged and verified
  before a rename-based swap, previous version kept for rollback, offline
  installation from a folder copied from another drive, never automatic.
  `tools/release-sign` creates keys, manifests and signatures.
- Project Gutenberg provider (opt-in): about 6,500 practical public-domain books
  from the official mirror, with strong warnings on historical medical texts.
- 47 CFR Part 95 (GMRS, FRS, CB, MURS) in the curated catalog.
- Saved custom collections, "Select all shown" in Discover, optional scheduled
  catalog refresh (off by default).
- Background tasks with progress for transfers, full integrity checks and
  update downloads.
- Acceptance test step: offline transfer to a second drive and full check.

## 0.1.0 (Phase 1 vertical slice)

### Added
- Portable launch on Windows (Arcana.exe), Linux and macOS (arcana.sh) from a
  library folder; no installation or administrator rights.
- Library layout with drive-resident settings, OS-level single-writer lock,
  read-only mode, and relocation-safe relative paths.
- Volume detection (capacity, file system, removable media) and capacity
  planning with a user-defined reserve.
- Provider-based catalog: bundled curated catalog, Kiwix OPDS catalog,
  Arcana JSON feeds; untrusted-input validation; offline catalog cache.
- Resumable, verified download manager with crash-safe checkpoints, mirror
  fallback, backoff, bandwidth limit and atomic publication.
- Offline import of archives from another drive, verified against catalog
  checksums.
- ZIM reading and full-text search through kiwix-serve (separate process,
  separate browser origin).
- Web interface: Library, Discover, Downloads, Updates, Server, Settings,
  first-run setup; light and dark themes.
- Integrity checker (quick and full) and database rebuild from sidecars.
- Offline acceptance test in an isolated network namespace.

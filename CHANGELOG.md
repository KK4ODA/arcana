# Changelog

All notable changes are recorded here. Versions follow semantic versioning;
the library format version is tracked separately (docs/design/09-storage-and-data.md).

## 0.6.0 — 2026-11-22

The first public release. It includes everything from the development
milestones 0.1.0 to 0.5.0 (see CHANGELOG.md), which were not released
separately.

### Highlights
- **Portable library** on any drive (exFAT recommended): one program per
  platform, nothing to install, works without internet once filled.
- **Choose and download** from the Kiwix catalog (3,600+ archives),
  Project Gutenberg's practical shelves, a curated catalog and signed
  community catalogs; capacity planning before anything is downloaded;
  resumable downloads verified against the publisher's checksums.
- **Read and search** offline: ZIM archives (through kiwix-serve), PDFs,
  EPUBs and your own documents; bookmarks, notes and history.
- **Offline maps** of 541 regions plus custom areas, with place search;
  **GPS** from USB/serial receivers, gpsd, NMEA over UDP/TCP or a
  simulator; waypoints, tracks and GPX.
- **Share on the local network**, read-only for visitors, found as
  `arcana.local` or by QR code; optional HTTPS and password-protected
  remote administration.
- **Raspberry Pi / Linux appliance** installer with its own Wi-Fi network
  and captive portal.
- **Integration API** for programs such as RadioPost and Aether HF, and
  copying between Arcana libraries on a network.

### Added in this milestone (Phase 6: integration and peer sync)
- **Integration API v1** (`/api/integration/v1/`) for programs such as
  RadioPost and Aether HF: search with citations, article text and bounded
  excerpts with an attribution footer, current position, coordinates with
  Maidenhead locator and nearby places, server-sent events, and short share
  links. Read-only; every call needs a scoped token. Programs pair by asking
  for access and the operator approves the request in *Settings →
  Integrations* after comparing a code. Tokens are stored hashed and can be
  revoked. Advertised on the network as `_arcana._tcp`.
- **Copy from another Arcana on the network** (*Settings → Other Arcana
  libraries*): browse its library, see what is new or newer, and copy
  through the download queue, verified against that library's checksums
  and the publisher's when known. Works without internet.
- **Signed catalogs:** a catalog feed can carry a publisher key; Arcana then
  accepts it only with a valid Ed25519 signature (`release-sign sign`
  produces it).
- docs/integration-api.md; docs/design/15-local-ai.md (design only).
- Acceptance step 11b: a second library copies from the first over the
  LAN address inside the offline namespace.

### Security
- Token, pairing and peer management stay host-only even for signed-in
  network administrators. The integration API never accepts the UI's
  session tokens, and the UI API never accepts integration tokens.

### Known limitations
- Tested automatically on Linux; the Windows and macOS programs pass the
  test suite on GitHub's runners but have not been tried on real machines
  by the developers. Nothing has been run on Raspberry Pi hardware or with
  a physical GPS receiver yet.
- The programs are not code-signed: Windows SmartScreen and macOS
  Gatekeeper warn on first start (README-FIRST.txt explains how to proceed).
- In-app updates are not active in this release: no release signing key is
  configured yet, so new versions are installed by downloading them.
- Map regions are extracted from Protomaps planet builds on demand; they
  have no publisher checksum and are labelled unverified.
- On Windows and macOS, Arcana cannot create a Wi-Fi hotspot itself.

## 0.5.0 — development milestone (Phase 5: appliance)

### Added
- **Appliance installer** for Raspberry Pi OS (Bookworm) and other systemd
  Linux: `appliance/install.sh` in the release creates an unprivileged
  `arcana` service user, installs a confined `arcana.service` that starts
  at boot, and with `--ap` sets up a Wi-Fi access point (NetworkManager)
  with a captive portal that leads phones to the library. `--dry-run`
  previews every step; `--uninstall` removes the service but keeps the
  library.
- systemd integration: readiness notification and a watchdog that is fed
  only while the web server answers.
- `--captive-port`: a port that redirects every request (OS connectivity
  checks, typed addresses) to the library; it never serves the API.
- docs/appliance.md: installation, library drives, how phones connect,
  read-only system, updating, security notes.
- CI checks the installer (shellcheck, dry run), the units
  (`systemd-analyze verify`) and the firewall rules (nftables syntax).

## 0.4.0 — development milestone (Phase 4: network serving)

### Added
- **Remote administration** (off by default): the host sets a password in
  the Server view; people on the network sign in to manage downloads,
  catalog and settings. Drives, transfers, program updates, the password
  and the host's private notes stay host-only. Argon2id password hash,
  HttpOnly cookies with a per-session token, throttled sign-in.
- **Name on the network:** a built-in mDNS responder advertises
  `http://arcana.local:8470/` (configurable; renamed automatically if taken)
  and a `_http._tcp` service while sharing.
- **QR codes** for every network address in the Server view and a printable
  **connection card** (`#/connect`).
- **Optional HTTPS** on the same ports as HTTP, with a self-signed
  certificate (fingerprint shown) or your own in `user/tls/`. Phones get a
  secure context, so they can share their own location with the map.
- **Wi-Fi hotspot** control on Linux with NetworkManager (Server view and
  `arcana hotspot start|stop|status`), with a QR code to join it.
- Acceptance step 7b covers remote administration from a LAN address.

### Changed
- Opening a map from the Library goes to the Maps view, centred on it.
- Leaving the Maps view while updates are arriving no longer raises errors.

## 0.3.0 — development milestone (Phase 3: offline maps and GPS)

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

## 0.2.0 — development milestone (Phase 2: content management and updates)

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

## 0.1.0 — development milestone (Phase 1 vertical slice)

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

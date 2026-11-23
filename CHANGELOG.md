# Changelog

All notable changes are recorded here. Versions follow semantic versioning;
the library format version is tracked separately (docs/design/09-storage-and-data.md).

## Unreleased

### Added
- **Ask your library.** A new *Ask* view (and an *Ask* button beside the
  library search) answers questions in plain words from your installed
  archives and documents. Arcana searches the library, picks the passages
  that cover the question, and a language model writes a short answer that
  cites them; each passage is listed with its resource, edition, date,
  license and warnings, and links to the page it came from. When nothing in
  the library covers the question closely enough, Arcana says so and no
  model runs. Answers appear as they are written; an answer that cites
  nothing is flagged, and medical sources bring the medical notice with
  them. Off until you turn it on in *Settings → Assistant*, where you choose
  where the model runs:
  - **On this computer:** download a model from *Discover → Question
    answering* (Qwen2.5 0.5B, 0.5 GB, for small computers; Granite 3.3 2B,
    1.5 GB, recommended; Qwen3 4B, 2.5 GB, for 8 GB of memory or more). The
    llamafile engine (370 MB) is queued with the first model. It runs only
    while answering, only on this computer, and stops when idle.
  - **On a model server:** Ollama, LM Studio or another OpenAI-compatible
    server, on this computer or another (*Test connection* lists its
    models).
  Devices on the network can ask only if you allow it; they never see your
  own documents. Optional instructions ("Use metric units") and a
  strictness setting tune the answers. See ADR-0021.
- **Medicines.** An FDA drug label reference (*Discover → Medicine and
  public health → FDA drug labels*, after refreshing the catalog): the
  labels of about 265,000 US medicines from openFDA, public domain. A new
  *Medicines* view searches by brand, generic name or ingredient, grouping
  products that share active ingredients; *By situation* lists
  non-prescription medicines whose labels cover one or several of 34
  everyday problems (medicines covering all of them first); a label page
  shows uses, directions, warnings, interactions, overdose and side
  effects; *Compare warnings* puts two labels' warnings side by side (it
  does not check interactions, and says so). The view asks you to
  acknowledge that it is label text, not medical advice, once per browser.
  Arcana downloads the 14 parts openFDA publishes (1.8 GB, resumable),
  checks each zip entry's CRC, keeps the parts a reader needs, stores
  identical labels once and builds an indexed reference of about 1 GB on
  your computer. New FDA exports appear in Updates. See ADR-0022.
- **Translate archive pages offline.** Download a language pack
  (*Discover → Encyclopedias and reference → Translation: …*, 55 languages,
  about 50 MB each; the 1.2 MB engine comes with the first one), then
  choose *Translate to …* in the reader. Pages are translated on the device
  reading them by Mozilla's Bergamot engine, the one Firefox uses, so it
  works on phones and on every computer Arcana runs on, without internet.
  Links and emphasis are kept, letters and codes are left alone, the choice
  follows you from page to page, and *Original language* restores the
  text. Between two languages other than English, Arcana goes through
  English when both packs are installed. Files are checked against
  Mozilla's published hashes before and after decompression. See ADR-0023.
- **Meshtastic on the map.** Connect a Meshtastic radio (*Settings → Maps
  and position → Meshtastic radio*: a radio on Wi-Fi or Ethernet, or on
  USB) and the map shows every node it hears that reports a position, with
  its name, when it was last heard, hops, signal and altitude; nodes
  without a position and the text messages on the radio's channels are
  listed beside the map. Arcana only listens: it asks the radio for its
  node list and keeps the connection alive, and never transmits on the
  mesh. Network clients see the mesh only if you allow it.
- **Kolibri courses in search.** If you run a Kolibri server (Khan Academy
  and other courses, with progress for each learner), enter its address in
  *Settings → Kolibri (courses)*: its courses appear in library search
  after the library's own results, and the Library page links to it.
  Kolibri stays a separate program; see docs/kolibri.md.
- **U.S. Army field manuals.** Three public-domain manuals cleared for
  public release join the catalog as searchable PDFs: *FM 4-25.11 First
  Aid*, *ATP 3-50.21 Survival* (2018) and *FM 3-25.26 Map Reading and Land
  Navigation*. They are offered in the Family Emergency Preparedness and
  Remote Expedition collections, with notes on what has changed since they
  were written. (The 2013 TC 3-25.26 was left out: its distribution is
  restricted to government agencies.)
- **Links on the Library page.** Add shortcuts to other services on your
  network (a Kolibri server, a radio's web page, a file share) in
  *Settings → Links on the Library page*; everyone sees them on the Library
  page.
- **OpenAPI description of the integration API** at
  `/api/integration/v1/openapi.json` (OpenAPI 3.1), for client generators
  and API tools. A test keeps it in step with the routes.
- **Getting started.** A new library opens a short guided setup (and the
  Library view offers it until you finish or skip it): choose topics (the
  nine collections) and tools (maps of your area, directions, the medicines
  reference, question answering, translation), your language and region,
  and how much of the drive to use (Essential, about 8 GB; Standard, about
  40 GB; Comprehensive, everything above the reserve). Arcana's recommender
  fits editions to that budget across all chosen topics, adds what models
  need (the engine), shows the plan on the storage bar with each item's
  reason, and queues it in one click.
- **Automatic content updates** (Updates → Automatic updates; off by
  default). Choose the hours they may run, how often the catalogs are
  checked, whether to take every update or only medical and regulatory
  references, and a bandwidth cap. Inside the window Arcana refreshes the
  catalogs, queues the updates that fit above your reserved free space
  (most important and smallest first), and pauses those downloads when the
  window closes, continuing in the next one. Without internet it tries
  again later. *Check and queue now* runs a check at once. As with every
  update, the installed edition is replaced only once the new one is
  verified.
- **`arcana ask`** answers a question in the terminal; `arcana ask --eval
  FILE [--with-model]` scores retrieval (and answers) against a set of
  questions with known sources.
- **Integration API:** `GET /api/integration/v1/ask` with the new scope
  `assistant:ask` lets paired programs ask questions and receive the answer
  with its passages and citations.
- **Dependencies.** Queueing a resource also queues the resources it needs
  (a model queues the engine that runs it).

### Fixed
- **Your own documents no longer appear in searches from other devices.**
  Library search returned the names and text snippets of the documents you
  added (*Your documents*) to every device on the network, although the
  files themselves could only be opened on the computer running Arcana.
  Now devices on the network, signed-in administrators included, search
  only installed resources, and searching *Your documents* from them is
  refused; the computer running Arcana still finds everything. The
  integration API and the assistant already left your documents out for
  other devices; they now also stop them from taking up result slots.

### Changed
- Text extracted from web pages keeps the cells of a table row on one line
  ("QTH | Location of the station"), so document search snippets and the
  assistant read tables correctly.

## 0.7.0 — 2026-11-23

Directions and turn-by-turn navigation without internet, search inside the
archive or document you are reading, a reworked library search, measuring
on the map, and a choice of miles or kilometres. Archives that cannot be
opened now say why. Arcana 0.6.0 to 0.6.2 offer this version by themselves
within a day of being online: choose *Update now* in the banner at the top
of the page.

### Added
- **Units.** Choose kilometres and metres or miles and feet, in the map's
  Tools panel or under Settings → Appearance. The map scale bar, GPS speed
  and altitude, track lengths and measurements follow the choice. It is
  stored per browser, so each person on the network can pick their own;
  the default follows the browser's language (miles for the US, the UK,
  Liberia and Myanmar).
- **Measuring on the map.** *Measure distance* adds up straight-line
  (great-circle) distances between points you click and shows the bearing
  of the last leg; *Measure area* shows the area and perimeter of the shape
  you outline. Double-click or press *Done* to finish, *Undo point* (or
  Backspace) to take back a point, Escape to stop. Works offline.
- **Search inside what you are reading.** The reader has a search box for
  the open archive (press `/` to reach it). Titles complete as you type, so
  you can jump straight to an article; Enter lists every page that mentions
  the words beside the page you are reading, with *Show more results*.
  Archives without a full-text index are searched by title.
- **Find in documents.** PDFs, EPUBs and your own documents have a *Find in
  this document* bar that marks every match and steps through them with
  Enter, Shift+Enter or the arrows. Opening a document from search results
  marks the words you searched for.

- **Directions and turn-by-turn navigation, offline.** Install a *road
  network* for your country or state (Discover → Maps and navigation), then
  use *Directions* in the map view: choose a start (a place, your GPS
  position, or a point on the map) and a destination, and travel by car,
  bicycle or on foot. Arcana shows the route, its time and distance, and
  each turn. *Start navigation* follows your GPS position with the next
  turn and the distance to it, finds a new route if you leave this one, and
  can speak the instructions with the browser's voices. Routes can be saved
  as GPX. Road networks are built on your computer from OpenStreetMap
  extracts published by Geofabrik, checked against their checksum
  (ADR-0020). A route must stay inside one installed road network, and very
  large countries (such as Germany or France as a whole) are not offered
  yet because building them needs too much memory.

### Changed
- **Library search, reworked.** Results now start with articles whose
  titles match (an exact title first), then show each source separately
  with its number of matches, so a small archive such as a repair guide is
  no longer buried under an encyclopedia. *Show all in …* (or the new
  *Search in* menu) searches one source, a page at a time. Titles complete
  as you type, and the menu also offers your own documents.

### Fixed
- **Archives that cannot be opened now say why.** "Cannot open" in the
  Library is now a link, and the reader names the cause: the archive
  reader is still opening the archives, it could not start, or it skipped
  this archive as damaged (with kiwix-serve's own message and a hint to
  run a full check). Before, every case was blamed on "the archive reader
  is not available on this computer".
- The archive reader now gets more time to start when many or large
  archives are installed (30 s plus 20 s per archive, at most 15 minutes)
  instead of a fixed 30 s. Large archives such as iFixit take about a
  thousand scattered reads each to open, which on a slow external hard
  disk could run past 30 s and leave archives unopenable. A start that
  runs out of time is no longer retried from the beginning.
- In search results, a matching map opened the document viewer instead of
  the map.

## 0.6.2 — 2026-11-22

Downloads are much faster, and full-text search works again for common
words in larger archives. Arcana 0.6.0 and 0.6.1 offer this version by
themselves within a day of being online: choose *Update now* in the banner
at the top of the page.

### Changed
- **Much faster downloads.** Large files now download in 4 MiB pieces over
  up to six connections and several mirrors at once. Arcana measures each
  mirror and gives most of the work to the fastest one, and re-requests a
  piece from a faster mirror rather than waiting for a slow one. Pieces
  are still written and checked in order, so pausing, resuming after a
  crash and verification work as before. On a 306 MB Kiwix file this went
  from 2.4–5.8 MB/s to 64–84 MB/s (ADR-0019).
- **Faster map regions.** Map tiles are fetched with up to eight requests
  in flight instead of one at a time; a test region went from 30 s to 4 s.
- The Downloads view names the main mirror in use and how many others
  help.

### Fixed
- Full-text search failed when an archive reported 1,000 or more matches.
  kiwix-serve writes the count with a thousands separator ("1,800"), which
  Arcana could not read, so no results were shown at all. The count is now
  read whatever the separator, and an unreadable count no longer hides the
  results.

## 0.6.1 — 2026-11-22

Reading ZIM archives now works on Windows computers that do not have the
Microsoft Visual C++ Redistributable installed. Arcana 0.6.0 offers this
version by itself within a day of being online: choose *Update now* in the
banner at the top of the page.

### Added
- **The Windows build includes the Microsoft Visual C++ runtime**
  (msvcp140.dll, vcruntime140.dll, vcruntime140_1.dll, version
  14.52.37029.0) beside kiwix-serve, so ZIM archives open on computers
  without the Visual C++ Redistributable. The files are Microsoft's,
  unmodified, from the official installer and pinned by SHA-256
  (`VCRUNTIME-NOTICE.txt`). Windows drives updated with *Update now*
  receive them too.
- CI and the release workflow run the packaged Windows build on Windows
  and check that kiwix-serve loads these copies.

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
- **One-click updates:** Arcana looks for a new version once a day while
  online and installs it, verified against the project's signature, with
  one click.

### Added for this release
- **Application updates in one click.** Once a day, starting two minutes
  after Arcana starts, Arcana checks the project's signed `release.json`
  (on by default; *Settings → Application updates* has the switch and
  *Check now*). A banner shows the new version and what is new; *Update
  now* downloads it, verifies the signature and hashes, installs it and
  restarts Arcana on the same port, and the page reloads into the new
  version. The previous version is kept for *Roll back*. The last check is
  kept on the drive, so the notice also appears offline. Copies that cannot
  replace themselves (the appliance service, copies outside the library)
  say how to update instead. This check is the only network request Arcana
  makes without being asked.
- **Signed releases.** The programs trust the project's release key; the
  release workflow signs each release's update manifest and checks the
  signature (`release-sign verify`). An unsigned release stays a draft.
- **Complete kiwix-serve sources.** Every release publishes the source of
  kiwix-serve and of every library built into it, in the exact versions
  shipped, with Kiwix's build recipes and patches and the GNU C library of
  the static Linux builds (`arcana-<version>-kiwix-sources.tar`, about
  170 MB, with a checksum list; see `licenses/SOURCES.txt`).
- **Release files** for each platform, an all-platform portable zip, and
  checksums; build provenance attestations for public releases.
- On Windows, when kiwix-serve cannot start because the Microsoft Visual
  C++ runtime is missing, the dashboard says so and what to install.

### Added in Phase 6 (integration and peer sync)
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
- Built with Go 1.26.11, which includes the standard-library security
  fixes released since Go 1.26.0 (found by govulncheck in CI).
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
- On Windows, the ZIM reader (kiwix-serve, built by Kiwix) needs the
  Microsoft Visual C++ runtime, which Arcana does not include. Most
  computers have it; Arcana explains when it is missing.
- One-click updates start with this release: this version will offer the
  next one. The automatic restart has been tested on Linux; on Windows and
  macOS it is covered by the test suite only.
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

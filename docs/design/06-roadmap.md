# F. Phased implementation plan

Each phase ends with a release that is useful on its own and passes the
offline acceptance test for everything it claims. Experimental work stays
behind a build tag or a disabled-by-default setting until it has tests;
features are never merged on the strength of a demo.

Status key: **Done** (implemented and tested) · **Partial** · **Planned** ·
**Unresolved** (needs a decision or external input).

## Phase 0 — Research and design · Done

Deliverables are in `docs/design/` and `docs/adr/`:
- the landscape review;
- functional requirements;
- the architecture alternatives and recommendation;
- the catalog specification;
- UX and wireframes;
- maps and GPS;
- security, licensing and storage/data model;
- risks, performance and testing;
- this roadmap;
- 14 ADRs.

## Phase 1 — Minimum viable portable library · Done (this release)

| Milestone | Status | Evidence |
|---|---|---|
| Windows portable launch (Arcana.exe, no install, no admin) | **Partial**: cross-compiled and packaged; Windows-specific code (volume API, locking, console, hidden kiwix window) compiles; **not yet run on real Windows**. CI runs the test suite on `windows-latest`. | `scripts/package.sh`, `.github/workflows/ci.yml` |
| Linux and macOS portable launch | Linux done (tested). macOS cross-compiled, not run | `packaging/arcana.sh` |
| Drive detection and capacity | Done | `internal/storage` |
| Local metadata DB | Done (rebuildable index) | `internal/store` |
| Resource catalog (bundled + Kiwix + feeds) | Done | `internal/catalog` |
| Kiwix/ZIM integration: browsing and search | Done | `internal/kiwix` |
| Selection and download queue, resumable, verified | Done | `internal/download` |
| Offline use | Done | `test/acceptance` |
| Offline import from another drive | Done (single files) | `arcana import` |
| Read-only LAN sharing | Done (`--lan`) | `internal/server` |

**Acceptance test** (`go test -tags acceptance ./test/acceptance`) passes:
1. Prepare a drive.
2. Cut the internet in a network namespace.
3. Launch.
4. Browse and search.
5. ~~Maps~~ and 6. ~~GPS~~ are skipped, explicitly marked not implemented.
7. Serve a LAN client (read-only enforced) with a browser tripwire for
   external requests.
8. kill -9.
9. Move the drive to a new mount point.
10. Settings and content intact, plus a full hash check.
11. Lose the database and rebuild it.
12. Confirm no network access was attempted.

## Phase 2 — Content management and updates · Done

**Dependencies:** Phase 1.

| Milestone | Status and evidence |
|---|---|
| Gutenberg provider (catalog CSV from the official mirror, EPUB) | **Done.** Opt-in; 6,572 practical titles from the live catalog validated (`TestGutenbergRealCatalog`); downloads marked unverified (no published checksums); EPUB indexed and previewed in-app |
| Government documents | **Partial.** Curated, hash-pinned 47 CFR Parts 95 and 97. FEMA and Ready.gov block automated downloads; more entries need curation by hand |
| PDF/EPUB/HTML/Markdown/text adapters + SQLite FTS5 index for non-ZIM content | **Done.** `internal/docs`; malformed and hostile documents tested; unified search returns archive and document hits |
| User documents, bookmarks, notes, history, favorites | **Done.** Plain files in `user/` (JSON and Markdown), recoverable deletes, `.bak` copies; private data (history, notes, documents) host-only |
| Collection export/import between drives with manifest | **Done.** `internal/transfer`; acceptance step 12 |
| Library manifest | **Done** (`manifest/library.json`); signing deferred to Phase 6 signed feeds |
| Application updates: Ed25519-signed manifest, staged swap, rollback, offline package | **Done.** `internal/appupdate`, `tools/release-sign`; tampering, foreign keys, missing signatures and path escapes rejected in tests. Needs a maintainer signing key before it is active in releases |
| Saved custom presets | **Done** (`user/presets/`); geography step moves to Phase 3 with map regions |
| Scheduled catalog refresh (opt-in) | **Done**; off by default |

## Phase 3 — Offline maps and GPS · Done (0.3.0), except 3b terrain/overlays and 3c routing

**Dependencies:** the Phase 2 adapters (for GPX in the library) and the
Phase 1 content origin. Details: [05-maps-gps.md](05-maps-gps.md#implementation-status-phase-3).

| Milestone | Status |
|---|---|
| `maps` provider with regional PMTiles (overview/standard/detailed) and sizes | **Done.** 541 regions with sizes measured against planet build 20261121; extracted on demand from the newest build through the download manager (resumable, ETag-pinned, structure-checked, SHA-256 recorded; labelled unverified because no publisher hash exists). Real extractions: Luxembourg z13 (34.5 MB) and the world z6 overview (45.1 MB) in 12 s |
| Map view: MapLibre (vendored), Protomaps style/glyphs/sprites offline, attribution | **Done.** Acceptance step 5 passes in the network namespace; browser check finds no external requests, CSP violations or script errors |
| Seamless overlapping maps | **Done.** Merged tile pyramid with server-side overzoom (ADR-0015) |
| GPS: serial/COM, UDP, TCP, gpsd, GPX playback, manual; fix state machine; SSE | **Done** (TCP server mode and baud autodetection not implemented). Acceptance step 6: simulator and NMEA over UDP, privacy from LAN clients |
| Waypoints, tracks, GPX import/export | **Done.** Round-trip GPX tests |
| Distance and bearing tool, DDM/MGRS coordinates | Planned |
| 3b: gazetteer, custom bbox | **Done.** Place search works offline |
| 3b: terrain, contours, emergency overlays | Planned |
| 3c: routing evaluation (Valhalla sidecar; BRouter alternative) | Planned: decision ADR with measured sizes and Pi 5 memory |
| Geography step in the selection wizard | Planned |

## Phase 4 — Network serving · Partial

**Dependencies:** Phase 1 server.

| Milestone | Status |
|---|---|
| Local HTTP server, responsive UI, multiple clients, read-only clients | Done |
| Authentication for remote administration (password, Argon2id hash in `user/`, session cookie, rate-limited) | Planned |
| mDNS (`arcana.local`, DNS-SD `_http._tcp`), registered via OS responder where present, else brutella/dnssd | Planned |
| QR code connection page | Planned |
| Hotspot helpers: NetworkManager (Linux), documented Windows Mobile Hotspot use | Planned; **Unresolved**: no supported no-upstream hotspot API on Windows, none on macOS |
| Optional local TLS (user cert or self-signed) for Geolocation/service workers on LAN | Planned |

## Phase 5 — Cross-platform and appliance · Planned

**Dependencies:** Phase 4.
- Linux ARM64 portable builds: **done**, packaged.
- macOS signing and notarization: **Unresolved**; needs an Apple Developer
  account.
- Windows code signing: **Unresolved**; needs a certificate.
- Raspberry Pi 4/5 appliance image (pi-gen):
  - read-only root (overlayfs) with the library on USB SSD/NVMe;
  - a systemd service with `Restart=always`;
  - hostapd + dnsmasq AP with a local DNS name;
  - optional captive portal that answers OS probes with a local landing
    page (not an internet emulation);
  - watchdog;
  - power-loss tests (cut power during download, update and indexing ×
    100 cycles).
- The library is prepared on a desktop and moved to the appliance with no
  conversion. Already true of the format; to be verified on hardware.

## Phase 6 — Advanced · Planned (not on the critical path)

- Unified cross-library search ranking across languages and formats.
- Advanced offline routing.
- Optional local AI question answering:
  - a separate optional sidecar (llama.cpp-class) installed as a catalog
    resource;
  - retrieval only from installed sources, with citations and visible
    uncertainty;
  - never required and never on by default.
- Peer-to-peer library sync on the LAN, built on the Phase 2 manifest and
  verified transfers.
- RadioPost / Aether HF interoperability through the local integration API
  (14-integrations.md).
- Community catalog plugins (signed feeds), multi-drive collections.

## Priorities and sequencing rationale

1. **Integrity and portability before breadth.** A library that cannot be
   trusted after a power cut is worse than a small one.
2. **Documents and user data (Phase 2) before maps (Phase 3).** Bookmarks
   and notes establish the `user/` contract that waypoints and tracks
   reuse.
3. **Maps before hotspot automation.** Maps deliver value in every mode;
   hotspot automation only in appliance mode, and has the most unresolved
   platform constraints.

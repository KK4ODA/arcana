# Overview

## Mission

Give people the ability to curate, preserve, transport, search, update and
share essential knowledge independently of internet infrastructure.

## Design principles

1. **Offline is the default state.** The internet is used only to fetch
   catalogs and content when the user asks. Every runtime asset ships
   inside Arcana.
2. **The drive is the computer's guest.** Arcana installs nothing on the
   host, needs no administrator rights, and keeps every piece of state on
   the drive.
3. **Never trust, always verify.** Catalogs and downloaded content are
   untrusted. Files are verified before use. Metadata is committed
   atomically. The index can always be rebuilt.
4. **Explain, don't decide.** Recommendations show their reasoning, sizes
   show their uncertainty, and Arcana never deletes or substitutes content
   on its own.
5. **Reuse what works.** Kiwix for ZIM, SQLite, PMTiles and MapLibre for
   maps, standard NMEA libraries for GPS. Arcana builds the parts nobody
   else has: portability, integrity, storage planning and a unified
   interface.
6. **Honest status.** Planned features are described as planned, not shown
   as buttons that do nothing.

## What exists today (0.2.0, Phases 1–2)

| Area | State |
|---|---|
| Portable runtime (layout, discovery, lock, settings on drive, read-only mode, relocation) | Implemented, tested |
| Drive detection and capacity planning | Implemented; tested on Linux; Windows and macOS code compiled, not run |
| Catalog: bundled curated, Kiwix OPDS (3,682 entries), user feeds | Implemented, tested against the live catalog |
| Presets (9) and explained recommender | Implemented, tested |
| Download manager (resume, checkpoints, mirrors, backoff, bandwidth, verification, atomic publish, updates) | Implemented, tested (including power-loss simulation) |
| Offline import from another drive | Implemented (single-file resources) |
| ZIM reading and full-text search via kiwix-serve | Implemented, tested with the real kiwix-serve 3.8.2 |
| Web UI (Library, Discover, Downloads, Updates, Server, Settings, Setup, Reader) | Implemented; checked in headless Chromium, including phone width and dark theme |
| LAN sharing, read-only for network clients | Implemented, acceptance-tested |
| Integrity check, database rebuild | Implemented, tested |
| User data: bookmarks, favorites, history, notes, user documents | Implemented, tested (plain files in `user/`) |
| Document adapters and full-text index (PDF, EPUB, HTML, Markdown, text) | Implemented, tested |
| Collection transfer between drives, library manifest | Implemented, acceptance-tested |
| Signed application updates with rollback and offline packages | Implemented, tested; inactive until a maintainer key is configured |
| Project Gutenberg provider (opt-in) | Implemented, tested against the live catalog |
| Portable release bundle for 5 platforms | Implemented (`scripts/package.sh`) |
| Offline acceptance test | Implemented; steps 5 (maps) and 6 (GPS) skipped as not implemented |
| CI (lint, tests on Linux/Windows/macOS, acceptance, packaging) | Written; not yet run on GitHub |

## Prototypes

None. Everything listed as implemented is real behavior with tests.

## Planned

See [06-roadmap.md](06-roadmap.md).

- **Phase 2:** documents and user data, more providers, collection
  transfer, signed application updates.
- **Phase 3:** maps and GPS.
- **Phase 4:** discovery, remote admin, hotspot helpers.
- **Phase 5:** appliance image.
- **Phase 6:** advanced features.

## Unresolved dependencies and decisions

| Item | Why it matters | Owner |
|---|---|---|
| Confirm GPL-3.0-or-later (ADR-0012) | Licensing of all contributions | Repository owner |
| kiwix-tools source bundle for releases | GPL compliance for redistributed binaries | Maintainers |
| Windows and Apple code signing | First-run warnings, antivirus false positives | Repository owner (certificates cost money) |
| Windows kiwix-tools 3.8.2 build | Windows ships kiwix-serve 3.8.1 | Upstream Kiwix |
| Hotspot on Windows without upstream / on macOS | No supported OS API | Platform limitation; appliance covers the use case |
| Kiwix catalog checksums (v3 feed) | Would remove reliance on metalink fetch for hashes | Upstream Kiwix (merged, not deployed) |
| Real-hardware testing on Windows, macOS and Raspberry Pi | Phase 1 claims for those platforms | Maintainers / testers |

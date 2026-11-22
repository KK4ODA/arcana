# Arcana

**A portable, offline-first knowledge library.** Arcana lets you build a
collection of reference material on an external drive: encyclopedias,
medical references, repair guides, amateur radio regulations, and (soon)
maps. You can then carry the drive between computers and read, search and
share it with no internet connection.

> The ultimate design criterion is not how many features Arcana advertises,
> but whether you can depend on the information stored in it when
> everything else is offline.

Status: **0.2.0 — Phases 1 and 2.** It works end to end on Linux
and is packaged for Windows and macOS, but **has not yet been run on real
Windows or macOS hardware** (see [Status](#status)).

## What it does today

- **Runs from the drive.** No installation and no administrator rights.
  `Arcana.exe` on Windows; `sh arcana.sh` on Linux and macOS (Intel and
  Apple Silicon; x86-64 and ARM64 Linux). Settings, downloads and indexes
  stay on the drive, which can change drive letter or mount point.
- **Knows the drive.** It shows capacity, file system and free space,
  explains why a "1 TB" drive shows 931 GiB, warns about FAT32's 4 GiB
  limit and NTFS being read-only on macOS, and keeps a reserve you choose.
- **Real catalog.** Over 3,600 offline archives from the Kiwix library
  (Wikipedia, WikiMed, Stack Exchange, iFixit, Project Gutenberg and more),
  plus a curated catalog. Licenses, redistribution status, publication
  dates and medical or regulatory warnings are shown for every resource.
- **Storage-aware selection.** Pick resources or start from nine
  collections ("Family Emergency Preparedness", "Amateur Radio Operator",
  …). Arcana recommends editions that fit your drive and explains each
  choice.
- **Downloads you can trust.**
  - Downloads resume after network failures, crashes and power loss, and
    retry across mirrors.
  - Every file is verified against the publisher's SHA-256 before it is
    used.
  - An update never removes the working version until the new one is
    verified.
- **Offline reading and search.** Archives open through Kiwix's reader,
  with full-text search across installed content. Provenance and warnings
  are shown above every page.
- **Share on the local network** (`--lan`). Phones and other computers can
  browse and search; only the host computer can make changes.
- **Integrity tools.** Quick and full checks find missing or corrupted
  files. If the metadata index is lost, it is rebuilt from the drive.
- **Updates in one click.** Once a day, while online, Arcana looks for a
  new version. A banner offers *Update now*: the new version is
  verified against the project's signature, installed, and Arcana
  restarts into it. The previous version is kept for rollback. The check
  can be switched off in *Settings → Application updates*; it is the only
  network request Arcana makes without being asked.
- **Offline import.** `arcana import` installs archives from another drive
  and verifies them against the catalog with no network.

## Quick start (portable)

1. Download a release archive and unzip the `Arcana` folder onto an
   external drive (exFAT recommended).
2. Start it:
   - **Windows:** double-click `Arcana.exe`.
   - **Linux / macOS:** `sh /path/to/Arcana/arcana.sh`
3. Your browser opens Arcana. Confirm where to create the library (the
   folder you unzipped is suggested). A small sample library is
   pre-installed, so you can check offline browsing and search immediately.
4. While online, open **Discover → Refresh catalog**, choose resources or a
   collection, and press **Download selected**.

More in [docs/portable-use.md](docs/portable-use.md), including first-run
security prompts (the binaries are not yet code-signed) and network sharing.

## Command line

```
arcana [serve] [--library DIR] [--port N] [--lan] [--no-browser] [--read-only]
arcana init DIR [--label NAME]
arcana status | volumes | version
arcana check [--full]
arcana catalog refresh | catalog search TEXT
arcana get RESOURCE-ID...                       # download, verify, install
arcana import FILE... [--id ID] [--allow-unverified]
```

## Building from source

Requires Go 1.26+.

```
scripts/fetch-kiwix.sh          # hash-pinned kiwix-serve for your platform
go test ./...                   # unit and integration tests
make acceptance                 # offline acceptance test (Linux, network namespace)
go run ./cmd/arcana --library /tmp/lib   # opens setup if /tmp/lib is not a library yet
scripts/package.sh 0.1.0        # portable bundle for all platforms → dist/
```

See [docs/development.md](docs/development.md).

## Status

| | |
|---|---|
| **Implemented and tested** | Portable library layout; drive detection (Linux tested; Windows and macOS code compiled); capacity planning; catalog (bundled, Kiwix OPDS, feeds); recommender; verified resumable downloads; offline import; ZIM reading and search via kiwix-serve; web UI; read-only LAN sharing; integrity check; offline acceptance test |
| **Built, not yet run on hardware** | Windows `Arcana.exe`, macOS binaries, Linux ARM64 |
| **Implemented (Phase 2)** | Bookmarks, favorites, history and notes; your own documents with full-text search (PDF, EPUB, HTML, Markdown, text); drive-to-drive collection transfer; signed application updates with rollback; Project Gutenberg provider |
| **Implemented (Phase 3)** | Offline vector maps: 541 OpenStreetMap regions (world, continents, countries, states) extracted on demand from Protomaps planet builds, custom areas, seamless merging of overlapping maps, offline place search; GPS from USB/serial receivers, gpsd, NMEA over UDP/TCP, a GPX simulator or a fixed position; waypoints, track recording, GPX import/export |
| **Implemented (Phase 4)** | `arcana.local` on the network (built-in mDNS), QR codes and a printable connection card, optional HTTPS on the same port, password-protected remote administration (off by default), Wi-Fi hotspot control on Linux |
| **Implemented (Phase 5), not yet run on hardware** | Raspberry Pi / Linux appliance: installer, confined systemd service with watchdog, Wi-Fi access point with captive portal ([docs/appliance.md](docs/appliance.md)) |
| **Implemented (Phase 6)** | Integration API for other programs (RadioPost, Aether HF) with scoped, approved tokens and citations ([docs/integration-api.md](docs/integration-api.md)); copying between Arcana libraries on a LAN; signed community catalogs |
| **Design only** | Optional local question answering over installed sources ([docs/design/15-local-ai.md](docs/design/15-local-ai.md)) |
| **Planned** | Ready-made Raspberry Pi SD image; hardware and power-loss tests. Map routing, terrain and measuring tools; scheduled peer sync |
| **Not yet** | Code-signed programs (releases are not code-signed yet; Windows and macOS warn on first start) |

The full design package is in [docs/](docs/README.md).

## License

Arcana is licensed under the GNU General Public License v3.0 or later. It
runs [kiwix-serve](https://github.com/kiwix/kiwix-tools) (GPL-3.0-or-later)
as a separate program. Third-party components are listed in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). Reference content is
licensed by its publishers; Arcana shows each resource's license.

Arcana is not a source of current medical advice. Medical references are
dated reference material; in an emergency, contact emergency services or a
qualified clinician whenever possible.

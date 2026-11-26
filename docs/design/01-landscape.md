# A. Competitive and technical landscape

Reviewed 2026-11-22. Facts were checked against each project's repository,
license files, release pages and documentation; Kiwix behavior was also
verified by running kiwix-serve 3.8.2 locally. Items that could not be
verified are marked *unverified*. Project NOMAD was re-reviewed on
2026-11-23 against its source (commit `f633b36`, 17 Nov 2026, after
v1.35.1).

## Summary

The offline-knowledge problem is mostly solved at the **content-format**
layer and mostly unsolved at the **library-management** layer.

- **Kiwix** owns the format: ZIM archives, a 3,682-entry catalog (OPDS),
  metalinks with SHA-256 and mirrors, a mature server (kiwix-serve) and
  readers for every platform. Arcana should reuse all of it and reimplement
  none of it.
- **Every complete system** — Internet-in-a-Box, PrepperPi, Project
  N.O.M.A.D., Prepper Disk — is a **Linux appliance** that installs into the
  operating system. None runs portably from a drive on Windows, none plans
  content against the capacity of the drive it is on, and none treats
  integrity, interrupted downloads and drive relocation as first-class
  concerns.
- **Maps** are converging on PMTiles + MapLibre (IIAB, PrepperPi and
  N.O.M.A.D. all use them). Routing and geocoding offline remain heavy (Java
  or PostgreSQL stacks) with no portable solution.
- **Project NOMAD** is the most active and most visible of the appliances.
  Its lead is breadth: local AI over the library, an FDA drug reference,
  offline article translation, Kolibri courses and a Docker app catalog.
  Its gaps are the ones above: x86-64 Debian only, size-only download
  checks, no authentication, and maps without place search, GPS or
  routing.

Arcana's opportunity is the layer in between: a portable, storage-aware,
integrity-checked library manager and server that uses Kiwix for reading and
PMTiles/MapLibre for maps, and runs the same drive on a Windows laptop and a
Raspberry Pi.

## Comparison matrix

| | Kiwix (tools, libs, readers) | Internet-in-a-Box | PrepperPi | Kolibri | Prepper Disk | Project N.O.M.A.D. |
|---|---|---|---|---|---|---|
| **What it is** | ZIM format, reader apps, kiwix-serve, OPDS catalog | Ansible-built Linux "learning hotspot" bundling ~20 apps | Pi appliance: AP, captive portal, Kiwix, maps, admin UI | Offline learning platform with curricula and progress sync | Commercial Pi appliance with preloaded 512 GB card | Docker-orchestrated server ("Command Center", TypeScript/AdonisJS, MySQL, Redis): Kiwix, Kolibri, maps, local LLM + RAG, app catalog |
| **License** | libzim GPL-2.0-or-later; libkiwix, kiwix-tools, desktop, JS GPL-3.0-or-later | GPL-2.0 (GPL-2.0-or-later encouraged) | MIT-0 | MIT | Proprietary (runs Kiwix) | Apache-2.0 |
| **Latest release** | kiwix-tools 3.8.2 (2026-03-02); libzim 9.8.2 (2026-08-12); desktop 2.5.1 (2026-01-04) | release-8.2 (2024-12-31) + daily images (2026-11-20) | v1.1.0 (2026-09-16) | v0.19.5 (2026-07-13) | n/a | v1.35.1 (2026-10-05) |
| **Activity** | Very active; commits Nov 2026 | Active (418 commits since Aug 2026) | One main author | Active, funded org | Commercial | Active |
| **Fully offline** | Yes | Yes (after install) | Yes | Yes | Yes | Yes (after install) |
| **Windows** | Readers yes; kiwix-serve 3.8.1 (no 3.8.2 build) | No | No | Yes | No | WSL2 only (community-supported) |
| **macOS** | Yes | No | No | Yes | No | No |
| **Linux / Pi** | Yes, incl. ARM | Yes (Pi Zero 2 W–5, x86) | Pi 4/5 only | Yes | Pi 4 | Debian/Ubuntu x86-64; ARM only via community forks |
| **Runs from removable media** | Desktop "portable" mode; kiwix-serve tarballs are self-contained | No (installs into OS) | No (SD image) | Partially (`KOLIBRI_HOME`) | Card is the system | No (data path is a host-level Docker mount) |
| **Programmatic API** | OPDS v2, `/search`, `/raw` (public); `/content`, `/suggest` (private) | Per-app only | FastAPI admin | REST, "subject to change" | None public | Full REST API with generated OpenAPI 3.1; **no authentication** |
| **Content catalog** | 3,682 ZIMs, metalinks with SHA-256 | Pulls Kiwix, Kolibri, maps | Kiwix + regional maps | Kolibri Studio channels | Fixed curated set | Full Kiwix catalog + curated categories in Essential/Standard/Comprehensive tiers, US-region map packs, openFDA |
| **Storage planning** | No | No | Partial (bundle sizes) | No | n/a (fixed card) | Partial (projected usage bar; 2 GiB headroom check) |
| **Integrity verification** | meta4 hashes published; readers do not verify | Not systematic | Signed SD images | Channel checksums | Unknown | Size only: strips `.meta4`, compares byte count; no hash check |
| **Maps** | `maps_*` ZIMs only | PMTiles + MapLibre, Nominatim, terrain | PMTiles + tileserver-gl-light | No | NA/EU/Oceania street maps | Protomaps viewer, markers with notes; coordinate jump only (no place search, no routing) |
| **GPS** | No | No | No | No | No | No |
| **Local AI over the library** | No | *unverified* | *unverified* | No | *unverified* | Yes: Ollama or OpenAI-compatible, Qdrant vectors of ZIMs and uploads, citations |
| **Offline translation** | No | *unverified* | *unverified* | No | *unverified* | Yes: Bergamot app, x86-64 only |
| **Drug reference** | WikiMed and medicine ZIMs | Via Kiwix ZIMs | Via Kiwix ZIMs | No | Via Kiwix ZIMs | openFDA labels: by drug, by situation, compare warnings |
| **Authentication / LAN safety** | None (read-only server) | *unverified* | *unverified* | User accounts | *unverified* | None; anyone on the LAN can install or delete |
| **Suitability as a dependency** | High: stable formats, public API, long-lived project | Patterns only | Patterns only (permissive) | Optional sidecar for courses | Benchmark only | Patterns only |

## Per-project notes

### Kiwix
- **Reusable:** kiwix-serve as a sidecar process (Arcana's choice); the OPDS
  catalog and `.meta4` metalinks (exact size, MD5, SHA-1, SHA-256, 4 MiB
  piece hashes, about ten geo-ranked mirrors); libzim (including split
  `.zimaa` archives) if in-process reading is ever needed.
- **Constraints verified locally:**
  - The `/content/<name>` URL name comes from the **file name** (lower-cased,
    `.zim` removed, spaces to `_`, `+` to `plus`). It does not come from ZIM
    metadata.
  - Multi-archive full-text search requires all selected archives to share
    **one language**; otherwise kiwix-serve returns a "confusion of tongues"
    error. Arcana groups searches by language.
  - With a minimal `library.xml` (id and path only), kiwix-serve trusts the
    XML and shows empty metadata. Passing ZIM paths as arguments makes it
    read metadata from the files. Arcana passes paths.
  - kiwix-serve sends `Access-Control-Allow-Origin: *` and a CSP
    `sandbox allow-scripts allow-same-origin`. ZIM content can run scripts.
  - Search snippets mark matches with `<b>` child elements, not escaped text.
  - `--attachToProcess=<pid>` makes kiwix-serve exit when Arcana dies.
- **Catalog:** OPDS `length` is rounded up to a whole KiB and is not exact;
  the metalink `<size>` is authoritative. Only the latest file per
  name+flavour is listed. A `/catalog/v3` feed with digests was merged on
  2026-11-20 but is not deployed yet.
- **Licensing:** GPL. Running kiwix-serve as a separate program is
  aggregation, not linking. IIAB, PrepperPi and N.O.M.A.D. do the same.

### Internet-in-a-Box
- **Strengths:** broad app coverage; a working maps stack (PMTiles,
  MapLibre, Nominatim sqlite, terrarium terrain); a captive portal role.
- **Constraints:** installs as root onto a dedicated OS image with Ansible.
  There is no portable mode and no single API. Its Sentinel-2 imagery is
  CC BY-NC-SA, which makes it unsuitable for unrestricted redistribution.

### PrepperPi
- **Patterns worth adopting:**
  - kiwix-serve on loopback behind a reverse proxy.
  - Library rebuilt when content changes.
  - Country PMTiles cut out of the planet with HTTP range requests.
  - Its own meta4 resolution, because aria2 does not follow metalinks.
  - Signed, reproducible SD images.
- **Constraints:** Pi 4/5 only, Python admin layer, bus factor of one.

### Kolibri
- **Strengths:** a structured courseware platform with peer-to-peer sync
  (Morango).
- **Constraints:** Python 3.6–3.14 runtime and its own database; the API is
  not stable.
- **Fit:** an *optional* Phase 6 sidecar for education use, not a core
  dependency.

### Prepper Disk (commercial)
- Raspberry Pi 4B with a 512 GB SD card ($297, or $169 for the card alone).
  Up to 20 clients. The FAQ states it "runs on Kiwix under the hood".
- Content includes Wikipedia, WikiHow, iFixit, Gutenberg, TED, Stack
  Exchange, Khan Academy Lite, Ready.gov, medical wikis, street maps and
  RepeaterBook.
- Several of those sources are **non-commercial** (iFixit and Khan Academy
  are CC BY-NC-SA; TED is CC BY-NC-ND with a restrictive usage policy).
  Arcana's catalog therefore records redistribution status per resource.

### Project NOMAD
Reviewed against source on 2026-11-23 (Apache-2.0; v1.35.1, 5 Oct 2026,
plus unreleased fixes). Crosstalk Solutions' YouTube audience gives it the
largest community of the projects here.
- **Shape:** a web "Command Center" that installs and supervises Docker
  containers on a Debian/Ubuntu x86-64 host. Base install about 5 GB;
  about 25 GB with the AI assistant. Root install; external drives are
  left to the host's Docker mounts.
- **Strengths worth matching:**
  - *AI assistant:* chat over Ollama (or any OpenAI-compatible server,
    possibly on another host) with retrieval from uploaded documents and
    every ZIM, embedded into Qdrant with `nomic-embed-text`. v1.35 added
    citations (source and date), a relevance floor that lets retrieval
    decline, per-chat retrieval, a lightweight model for titles and query
    rewriting, vision attachments, a `nomad.md` instructions file and a
    frozen-corpus evaluation harness. Embedding a large ZIM "can take
    several hours"; the UI asks for confirmation when the vectors would
    use more than 10% of free disk.
    Models come from a list NOMAD's own service publishes (272 on
    2026-11-26, pulled from the Ollama registry); Arcana offers the 197
    of them that llamafile runs (ADR-0028).
  - *Drug Reference:* openFDA drug labels (about 1.7 GB download, 8–10 GB
    indexed in MySQL); search grouped by active ingredient, "by situation"
    lookup from a curated condition list, side-by-side label warnings, a
    disclaimer the reader must acknowledge.
  - *Translated Library:* Bergamot (Firefox Translations) in a container
    that rewrites Kiwix pages; 50 languages, 45–140 MB each, CPU only.
    x86-64 only (intgemm), no AVX means no translation.
  - *Automatic updates:* opt-in, per kind (core, apps, content), inside a
    time window, after a cool-off, never across a major version; content
    has its own overnight window and a bandwidth cap.
  - *Easy Setup:* capabilities → map regions → curated category tiers
    (Essential / Standard / Comprehensive) → review.
  - *Supply Depot:* one-click Docker apps (Kolibri, CyberChef, FlatNotes,
    Calibre-Web, Stirling PDF, Meshtastic Web, MeshCore Web, Vaultwarden,
    Jellyfin, …) and user-added link tiles for unmanaged services.
- **Constraints:**
  - Downloads are checked for size only (`download_integrity.ts`): the
    metalink is discarded, so a corrupted file of the right size is
    accepted.
  - No authentication at all; the README recommends network-level
    controls. The REST API that can install, delete and run containers is
    open to the LAN.
  - Maps: viewer, markers and coordinate entry. No place search, GPS,
    tracks or routing. Map packs are US census divisions plus a country
    picker.
  - Meshtastic Web is served over plain HTTP, so browsers refuse its USB
    and Bluetooth connections; only radios on Wi-Fi work.
  - No transfer between installations other than uploading one ZIM
    through the browser, unchecked against the catalog.
  - The hardware benchmark submits results to a public leaderboard
    (opt-in, HMAC-signed).
- **Adopted into Arcana (0.8.0):** local question answering
  built on Arcana's existing search rather than a vector store
  (15-local-ai.md), an openFDA drug reference, scheduled content updates,
  offline translation, a first-run wizard, Meshtastic on the map, link
  tiles, an OpenAPI document for the integration API, and optional
  Kolibri. Not adopted: the Docker app catalog, the benchmark
  leaderboard and bundled general-purpose apps.

### Others
- **Kiwix Hotspot / offspot** (GPL-3.0; base image 1.5.0, 2026-11-09):
  Kiwix's own appliance images. A reference for Phase 5.
- **Calibre-Web** (GPL-3.0): a possible EPUB library sidecar later.
- **RACHEL** (CC BY-NC-SA shell, not an OSI license): not reusable.

## Supporting technologies

| Area | Choice evaluated | License | Status | Decision |
|---|---|---|---|---|
| ZIM reading | kiwix-serve sidecar | GPL-3.0+ | Stable | **Reuse** (ADR-0003) |
| Catalog source | Kiwix OPDS v2 + meta4 | data CC/varied | Stable | **Reuse** via provider |
| Metadata DB | SQLite via modernc.org/sqlite (pure Go, FTS5 included) | BSD-3 | v1.62.0, SQLite 3.54.1 | **Reuse** (ADR-0004) |
| Full-text search (ZIM) | Xapian indexes inside ZIMs via kiwix-serve `/search` | GPL | Stable | **Reuse** |
| Full-text search (own docs) | SQLite FTS5 | public domain | Stable | **Reuse** (Phase 2) |
| Map tiles | PMTiles v3 (spec CC0; go-pmtiles BSD-3) | CC0 / BSD-3 | Stable | **Reuse** format; evaluate a small reader instead of go-pmtiles, whose cloud SDKs add ~28 MB (ADR-0008) |
| Map rendering | MapLibre GL JS v6 (WebGL2, ESM) | BSD-3 | v6.19.0 | **Reuse**, vendored |
| Basemap schema/assets | Protomaps basemaps v4, basemaps-assets (OFL fonts, MIT sprites) | BSD-3 / CC0 / OFL | Active | **Reuse** |
| Routing | Valhalla (MIT; prebuilt Win/Linux/macOS executables in pyvalhalla wheels), BRouter (MIT, Java), OSRM (BSD-2, no Win/ARM builds), GraphHopper (Apache-2, Java) | — | — | **Defer**; Valhalla sidecar preferred (05-maps-gps.md) |
| Geocoding | Nominatim, Photon, Pelias all need heavy servers | — | — | **Build** a small FTS5 gazetteer (ODbL derivative) |
| GPS parsing | adrianmo/go-nmea (MIT, 67 sentences) | MIT | v1.11.0 | **Reuse** (Phase 3) |
| Serial ports | go.bug.st/serial (BSD-3; cgo only for macOS USB enumeration) | BSD-3 | v1.8.0 | **Reuse** (Phase 3) |
| gpsd | gpsd JSON protocol on TCP 2947 (no Windows support) | BSD-2 | 3.27.5 | **Reuse** as an input (Linux) |
| mDNS | brutella/dnssd (probes conflicts), pion/mdns/v2 | MIT | Active | **Reuse** (Phase 4) |
| Downloads | aria2 (GPL-2) | — | — | **Build**: it needs metalink handling anyway, cannot share Arcana's checkpoints/verification, and is another binary per OS |

## Reuse / extend / build

**Reuse unchanged**
- ZIM format, kiwix-serve, and Kiwix's OPDS catalog and metalinks.
- SQLite.
- PMTiles format, MapLibre GL JS, and the Protomaps basemap and assets.
- go-nmea, go.bug.st/serial and gpsd as GPS inputs.

**Extend or wrap**
- kiwix-serve: run it as a supervised sidecar with origin isolation,
  language-grouped search federation and dashboard status.
- Kiwix catalog: normalize it into Arcana's provider-independent model and
  add license, warning and category curation.

**Build (Arcana's own value)**
- The portable runtime: library layout, drive detection and relocation.
- Capacity planning and transparent recommendations.
- A verified, crash-safe download and update pipeline.
- Offline import and transfer between drives.
- Integrity checking.
- A unified UI and API for desktop, LAN and appliance modes.
- The GPS subsystem and map UX (Phase 3), and the local integration API.

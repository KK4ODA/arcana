# A. Competitive and technical landscape

Reviewed 2026-11-22. Facts were checked against each project's repository,
license files, release pages and documentation; Kiwix behavior was also
verified by running kiwix-serve 3.8.2 locally. Items that could not be
verified are marked *unverified*.

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

Arcana's opportunity is the layer in between: a portable, storage-aware,
integrity-checked library manager and server that uses Kiwix for reading and
PMTiles/MapLibre for maps, and runs the same drive on a Windows laptop and a
Raspberry Pi.

## Comparison matrix

| | Kiwix (tools, libs, readers) | Internet-in-a-Box | PrepperPi | Kolibri | Prepper Disk | Project N.O.M.A.D. |
|---|---|---|---|---|---|---|
| **What it is** | ZIM format, reader apps, kiwix-serve, OPDS catalog | Ansible-built Linux "learning hotspot" bundling ~20 apps | Pi appliance: AP, captive portal, Kiwix, maps, admin UI | Offline learning platform with curricula and progress sync | Commercial Pi appliance with preloaded 512 GB card | Docker bundle: Kiwix, Kolibri, maps, local LLM |
| **License** | libzim GPL-2.0-or-later; libkiwix, kiwix-tools, desktop, JS GPL-3.0-or-later | GPL-2.0 (GPL-2.0-or-later encouraged) | MIT-0 | MIT | Proprietary (runs Kiwix) | Apache-2.0 |
| **Latest release** | kiwix-tools 3.8.2 (2026-03-02); libzim 9.8.2 (2026-08-12); desktop 2.5.1 (2026-01-04) | release-8.2 (2024-12-31) + daily images (2026-11-20) | v1.1.0 (2026-09-16) | v0.19.5 (2026-07-13) | n/a | v1.35.1 (2026-10-05) |
| **Activity** | Very active; commits Nov 2026 | Active (418 commits since Aug 2026) | One main author | Active, funded org | Commercial | Active |
| **Fully offline** | Yes | Yes (after install) | Yes | Yes | Yes | Yes (after install) |
| **Windows** | Readers yes; kiwix-serve 3.8.1 (no 3.8.2 build) | No | No | Yes | No | No |
| **macOS** | Yes | No | No | Yes | No | No |
| **Linux / Pi** | Yes, incl. ARM | Yes (Pi Zero 2 W–5, x86) | Pi 4/5 only | Yes | Pi 4 | Debian/Ubuntu |
| **Runs from removable media** | Desktop "portable" mode; kiwix-serve tarballs are self-contained | No (installs into OS) | No (SD image) | Partially (`KOLIBRI_HOME`) | Card is the system | No |
| **Programmatic API** | OPDS v2, `/search`, `/raw` (public); `/content`, `/suggest` (private) | Per-app only | FastAPI admin | REST, "subject to change" | None public | Per-app |
| **Content catalog** | 3,682 ZIMs, metalinks with SHA-256 | Pulls Kiwix, Kolibri, maps | Kiwix + regional maps | Kolibri Studio channels | Fixed curated set | Kiwix + presets |
| **Storage planning** | No | No | Partial (bundle sizes) | No | n/a (fixed card) | No |
| **Integrity verification** | meta4 hashes published; readers do not verify | Not systematic | Signed SD images | Channel checksums | Unknown | Unknown |
| **Maps** | `maps_*` ZIMs only | PMTiles + MapLibre, Nominatim, terrain | PMTiles + tileserver-gl-light | No | NA/EU/Oceania street maps | ProtoMaps |
| **GPS** | No | No | No | No | No | No |
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

# G. Risk register

Likelihood (L) and impact (I) are rated 1–3; score = L × I. Owner "M" means
the maintainers.

## Technical

| ID | Risk | L | I | Score | Mitigation | Status |
|---|---|---|---|---|---|---|
| T1 | Kiwix changes private endpoints Arcana uses (`/content` naming, `/suggest`) | 2 | 2 | 4 | Use only public `/search` and OPDS where possible; book names derived from file names with UUID fallback; kiwix-tools version pinned per release; integration test against the pinned binary | Mitigated |
| T2 | Kiwix stops publishing Windows builds (3.8.2 already missing) | 2 | 3 | 6 | Pin 3.8.1 for Windows; track kiwix-build; fallback options: build kiwix-tools via kiwix-build in CI, or link libzim through a small C shim in a separate helper binary | Open |
| T3 | Command-line length limit with hundreds of ZIMs (Windows 32 767 chars) | 2 | 2 | 4 | Relative paths (~90 chars → ~350 archives); planned switch to a generated `library.xml` with full metadata via `kiwix-manage` when the limit approaches | Open |
| T4 | Multi-language search fragmentation (kiwix requires one language per query) | 3 | 1 | 3 | Grouped per language; unified ranking deferred to Phase 6 | Accepted |
| T5 | SQLite corruption on USB bridges that ignore flushes | 2 | 1 | 2 | DB is a cache: quick_check, move aside, rebuild from sidecars (tested) | Mitigated |
| T6 | exFAT has no journal: sidecar or content damaged by unplugging | 2 | 2 | 4 | Atomic writes, verify-before-publish, full integrity check, eject reminders; sidecar loss leaves an "orphan" that re-import or re-download repairs | Mitigated |
| T7 | Routing engines too heavy for portable use | 3 | 2 | 6 | Valhalla sidecar evaluation with measured gates; BRouter alternative; straight-line tools clearly labeled | Open (Phase 3) |
| T8 | MapLibre 5 requires WebGL2; old phones and some blocklisted GPUs cannot show maps | 2 | 2 | 4 | Detect and explain; raster fallback for small areas still to be built | Open |
| T9 | Map extracts have no publisher checksum | 2 | 2 | 4 | Every range response must carry the ETag seen first; output must open as a valid archive; SHA-256 recorded for later integrity checks; labelled "unverified" (implemented) | Accepted |
| T11 | Protomaps changes its build hosting, retention or schema | 2 | 3 | 6 | Builds URL and base URL are settings; a deleted build restarts from the newest; region sizes regenerated with `tools/map-regions`; styles pinned with the binary (basemaps 5.7.2). A schema change needs a new Arcana release | Open |
| T12 | Overzoomed tiles look sparse far beyond the source zoom | 2 | 1 | 2 | Limited to 10 levels; users add detailed regions where needed | Accepted |
| T13 | No GPS receiver tested on real hardware yet | 3 | 2 | 6 | Single NMEA path tested with real receiver sentences, simulator and network sources; hardware test plan needed (u-blox USB, phone NMEA apps, gpsd on a Pi) | Open |
| T10 | Pure-Go SQLite slower than cgo SQLite | 1 | 1 | 1 | Measured: catalog queries 2–12 ms on 3,684 entries | Accepted |

## Licensing

| ID | Risk | L | I | Score | Mitigation | Status |
|---|---|---|---|---|---|---|
| L1 | GPL source obligations for redistributed kiwix-serve not met in a release | 2 | 3 | 6 | Ship the source tarball and SOURCES.txt; releases stay drafts until the checklist in 08-licensing.md is done | Open: maintainer action |
| L2 | Catalog misstates a license; users redistribute NC/ND content commercially | 2 | 2 | 4 | Per-family reviewed mapping; unreviewed = "unknown"; export warnings (Phase 2); content-source issue template requires a license link | Mitigated |
| L3 | TED usage policy conflicts with offline redistribution | 3 | 1 | 3 | Marked "restricted" (personal use); not in any preset | Mitigated |
| L4 | ODbL share-alike for Arcana-built derived databases | 2 | 2 | 4 | Publish build scripts and attribution; keep derived DBs separate from user data | Planned |
| L5 | Arcana's GPL choice deters some integrators | 1 | 1 | 1 | Integration is via local HTTP API, which imposes no license on clients (ADR-0012) | Accepted |

## Operational

| ID | Risk | L | I | Score | Mitigation | Status |
|---|---|---|---|---|---|---|
| O1 | Unsigned binaries blocked or scary (SmartScreen, Gatekeeper quarantine) | 3 | 2 | 6 | README-FIRST instructions; Windows code-signing certificate and Apple notarization (cost: certificates, Apple Developer account) | Open: maintainer decision |
| O2 | Medical content mistaken for current advice | 2 | 3 | 6 | Dated medical warning in catalog, library and above every reader page; high-priority updates; no AI answers in MVP | Mitigated |
| O3 | Users let catalogs go stale (no internet for months) | 3 | 1 | 3 | "Catalog checked N days ago" on the dashboard; updates work from the cached catalog | Mitigated |
| O4 | Kiwix mirrors or catalog unavailable | 2 | 2 | 4 | Metalink lists ~10 mirrors; cached catalog; offline import from another drive | Mitigated |
| O5 | Large initial downloads (100+ GB) fail on poor connections | 3 | 2 | 6 | Resume from checkpoints, indefinite retry for network errors, bandwidth limit, mirror rotation (tested) | Mitigated |
| O6 | Bus factor: single maintainer | 3 | 2 | 6 | ADRs, design docs, tests, CONTRIBUTING; keep dependencies few and boring | Ongoing |
| O7 | Pi appliance SD-card corruption on power loss | 3 | 2 | 6 | Read-only root (overlayfs), library on USB SSD/NVMe, power-cut test cycles (Phase 5) | Planned |

## Security

| ID | Risk | L | I | Score | Mitigation | Status |
|---|---|---|---|---|---|---|
| S1 | Script in a ZIM attacks Arcana's API | 2 | 3 | 6 | Separate content origin, token, CSP, no CORS (tested) | Mitigated |
| S2 | DNS rebinding / CSRF from websites the user visits | 2 | 3 | 6 | Host allow-list, token header, Sec-Fetch-Site (tested) | Mitigated |
| S3 | Hostile catalog writes outside the library or runs code | 1 | 3 | 3 | Strict validation, SafeJoin, no execution of content (tested) | Mitigated |
| S4 | Catalog triggers requests to LAN devices | 2 | 2 | 4 | Private destinations blocked by default, checked at dial time (tested) | Mitigated |
| S5 | LAN client modifies the library | 1 | 3 | 3 | Loopback-only mutations; token not sent to remote clients (acceptance-tested) | Mitigated |
| S6 | Compromised update channel (Phase 2) | 2 | 3 | 6 | Ed25519-signed manifests, offline keys, staged swap with rollback | Planned |
| S7 | Someone on the LAN injects false positions through the UDP listener | 2 | 2 | 4 | UDP is off unless chosen; checksums required; the help text says it listens on every interface; sender allow-list not yet implemented | Open |
| S8 | Position disclosed to other people on the network | 2 | 2 | 4 | Position, waypoints and tracks are host-only unless "Share position" is on (acceptance-tested); never transmitted | Mitigated |
| S7 | kiwix-serve vulnerability exposed on LAN | 2 | 2 | 4 | kiwix-serve binds loopback only; reached only through Arcana's read-only proxy; pinned and updated with releases | Mitigated |

## Performance

| ID | Risk | L | I | Score | Mitigation | Status |
|---|---|---|---|---|---|---|
| P1 | Pi 4 serving 20 clients plus map tiles | 2 | 2 | 4 | Client-side rendering; range reads; kiwix-serve threads; benchmark in Phase 5 | Open |
| P2 | Many large ZIMs raise kiwix-serve memory | 2 | 2 | 4 | Measure with full English Wikipedia (127 GB); archives opened lazily by libzim | Open |
| P3 | Slow USB 2.0 drives make full integrity checks take hours | 3 | 1 | 3 | Quick check by default; full check user-initiated, cancellable; progress reporting planned | Accepted |

## Cross-platform

| ID | Risk | L | I | Score | Mitigation | Status |
|---|---|---|---|---|---|---|
| X1 | Windows-specific code untested on real hardware in this phase | 3 | 2 | 6 | CI on windows-latest; manual test plan in docs/portable-use.md; tester recruitment | **Open** |
| X2 | macOS: exFAT `._` AppleDouble files, quarantine, NTFS read-only | 2 | 2 | 4 | Validators ignore dot-files; read-only detection; quarantine instructions | Mitigated |
| X3 | Linux automount without exec | 2 | 1 | 2 | `arcana.sh` copies the binary to a temp folder if it cannot run; same for kiwix-serve | Mitigated |
| X4 | Antivirus quarantines unsigned Go binaries | 2 | 2 | 4 | Signing (O1); submit false-positive reports | Open |
| X5 | Hotspot creation unsupported on many Windows adapters, none on macOS | 3 | 1 | 3 | Distinguish "join existing LAN" (always works) from "create hotspot" (appliance or supported hosts only) | Accepted |

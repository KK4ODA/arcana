# Functional requirements

Status: ✅ implemented and tested · 🟡 partial · ⏳ planned (phase).

## Portable operation (Mode A)
| ID | Requirement | Status |
|---|---|---|
| FR-A1 | Launch from the drive without installation or admin rights | ✅ Linux; 🟡 Windows/macOS built, not yet run on hardware |
| FR-A2 | All internal paths relative; survive drive-letter/mount changes | ✅ (acceptance step 9) |
| FR-A3 | No critical configuration in the host OS | ✅ settings in `user/` |
| FR-A4 | Graceful handling of sudden disconnection and power loss | ✅ checkpoints, atomic commits, DB rebuild |
| FR-A5 | Read-only browsing when the medium or library is read-only, locked, or newer format | ✅ |
| FR-A6 | No internet needed to launch or use downloaded content | ✅ (acceptance) |
| FR-A7 | Platform-specific launchers with shared portable library | ✅ `Arcana.exe`, `arcana.sh`, `bin/<os>-<arch>/` |

## Local server (Mode B)
| ID | Requirement | Status |
|---|---|---|
| FR-B1 | Embedded HTTP server, responsive browser UI, multiple clients | ✅ |
| FR-B2 | Read-only for ordinary clients; changes only by authorized users | ✅ local-only admin; ⏳ password admin (4) |
| FR-B3 | No cloud services, CDNs or online auth; local-only by default | ✅ |
| FR-B4 | mDNS name, local IP, QR code | 🟡 IP addresses shown; ⏳ mDNS, QR (4) |
| FR-B5 | Hotspot creation where the OS supports it | ⏳ (4/5) |

## Appliance (Mode C)
| ID | Requirement | Status |
|---|---|---|
| FR-C1 | Same library on Pi/ARM64/x86-64 without conversion | 🟡 format portable, ARM64 builds packaged; ⏳ appliance image (5) |
| FR-C2 | Auto-start, AP, optional captive portal, power-loss resilience | ⏳ (5) |

## Storage
| ID | Requirement | Status |
|---|---|---|
| FR-S1 | Detect capacity, free space, installed, queued, reserve, FS type and limits | ✅ |
| FR-S2 | Visualize storage including projected state after selection | ✅ |
| FR-S3 | Explain advertised vs. reported capacity | ✅ |
| FR-S4 | User-defined reserve; downloads never breach it | ✅ (enqueue and per-checkpoint checks) |
| FR-S5 | Estimates with uncertainty | ✅ (≈, "tight" status) |
| FR-S6 | Storage-based, explained recommendations; no silent deletion | ✅ |

## Catalog
| ID | Requirement | Status |
|---|---|---|
| FR-K1 | Provider architecture; no hard-coded URL list | ✅ bundled, Kiwix OPDS, Project Gutenberg, feeds; ⏳ maps (3) |
| FR-K2 | Standard metadata incl. license, redistribution, hash, provenance, warnings | ✅ |
| FR-K3 | 16 extensible categories | ✅ |
| FR-K4 | Medical provenance, dates and warnings | ✅ |
| FR-K5 | Treat catalogs as untrusted | ✅ |
| FR-K6 | Format adapters | ✅ ZIM (kiwix-serve); PDF, EPUB, HTML, Markdown, text (extraction, index, preview) |

## Selection UX
| ID | Requirement | Status |
|---|---|---|
| FR-U1 | Browse by category, search, filter by language, sort by size/date/relevance | ✅ (popularity: provider data not available from Kiwix) |
| FR-U2 | Region filter | ⏳ (3, with map regions) |
| FR-U3 | Compare alternative editions; see total selection vs. space | ✅ |
| FR-U4 | Curated presets; wizard with goals, language, capacity | ✅ 9 presets, saved custom collections; ⏳ geography step (3) |
| FR-U5 | Select entire categories | ✅ "Select all shown" (filter by category first) |

## Downloads
| ID | Requirement | Status |
|---|---|---|
| FR-D1 | Concurrency limit, pause/resume, Range resume, backoff, mirrors, speed/ETA, bandwidth limit | ✅ |
| FR-D2 | Integrity verification, atomic publication, never replace valid content with corrupt | ✅ |
| FR-D3 | Persistent queue; recovery after crash, shutdown and network loss | ✅ |
| FR-D4 | Disk-space reservation | ✅ |
| FR-D5 | Import from another drive offline | ✅ single files, collections, whole libraries |

## Updates
| ID | Requirement | Status |
|---|---|---|
| FR-P1 | Knowledge updates dashboard with versions, dates, sizes, priority, last check | ✅ |
| FR-P2 | Update all/selected/individual; never remove before verified replacement | ✅ |
| FR-P3 | Offline: show last status, no errors | ✅ |
| FR-P4 | Application updates: signed, rollback, offline packages, never mandatory | ✅ (needs maintainer key in releases) |
| FR-P5 | Delta updates where formats allow | ⏳ (no ZIM deltas published upstream) |

## Reading and search
| ID | Requirement | Status |
|---|---|---|
| FR-R1 | Browse all installed resources offline | ✅ ZIM via reader; PDF/EPUB opened by browser |
| FR-R2 | Full-text search within and across resources, with snippets and source | ✅ ZIMs (grouped by language) and documents |
| FR-R3 | Bookmarks, recent, favorites, notes, user documents | ✅ |
| FR-R4 | Metadata and publication dates visible while reading | ✅ |
| FR-R5 | Optional local AI with citations | ⏳ (6, optional) |

## Maps, GPS, radio, integration
| ID | Requirement | Status |
|---|---|---|
| FR-M1–M6 | Offline maps, regions, measurements, GPX, terrain, routing | ⏳ (3) — design in 05-maps-gps.md |
| FR-G1–G5 | Serial/COM, UDP, TCP, gpsd, simulator; fix display; stale handling; tracks | ⏳ (3) — design in 05-maps-gps.md |
| FR-H1 | Amateur radio category with jurisdiction and dates | ✅ category, regulatory warnings, 47 CFR 97; ⏳ more sources |
| FR-I1 | Generic local API for RadioPost/Aether HF | ⏳ (6) — design in 14-integrations.md |

## Integrity and transfer
| ID | Requirement | Status |
|---|---|---|
| FR-T1 | Integrity checker (missing, corrupted, orphaned) | ✅ quick + full |
| FR-T2 | Separate binaries, content, staging, indexes, metadata, settings, user data, logs | ✅ |
| FR-T3 | Portable manifest; export/merge between drives without duplication | ✅ |

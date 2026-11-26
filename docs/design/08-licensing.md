# Licensing analysis

*This is an engineering analysis, not legal advice. Items marked "maintainer
action" should be reviewed before the first public binary release.*

## Arcana's own license

**GPL-3.0-or-later** ([ADR-0012](../adr/0012-license-gpl-3.0-or-later.md)), for three reasons:

- It matches the Kiwix stack that Arcana depends on and ships beside.
- It keeps the option of linking libzim (GPL-2.0-or-later) or libkiwix
  (GPL-3.0-or-later) in-process later without relicensing.
- Appliance and commercial derivatives stay open source.

All direct Go dependencies are BSD-3-Clause or MIT, which are compatible.

## Distributing kiwix-serve (GPL-3.0-or-later)

- **No linking.** Arcana starts kiwix-serve as a separate program and talks
  to it over HTTP. This is "aggregation" under GPL-3.0 §5, and the same
  arrangement IIAB, PrepperPi and N.O.M.A.D. use. Arcana's license would
  not be constrained even if it were not GPL.
- **Source obligation (maintainer action).** Release archives ship
  unmodified official Kiwix binaries. Kiwix builds them statically against
  libkiwix, libzim, Xapian, ICU, libmicrohttpd and others, so GPL-3.0 §6
  requires the release to:
  - include the corresponding source (kiwix-tools 3.8.2 source tarball,
    519 kB, plus the kiwix-build recipe versions for dependencies); **or**
  - include a written offer, valid for three years; **or**
  - point to the source on a network server under §6(d) while ensuring it
    stays available.
- **Implemented (0.6.0): all sources published.** The maintainer decided to
  publish the complete source of everything inside the shipped binaries,
  not only the GPL components. `scripts/fetch-kiwix-sources.sh` downloads,
  each checked against a pinned SHA-256 or commit:
  - kiwix-tools, libkiwix and libzim for both builds (3.8.2 for Linux and
    macOS, 3.8.1 for Windows);
  - the kiwix-build recipes and patches at the commits that released them
    (f4325ef and 6f3a27d);
  - every library those recipes link in, in the recipes' versions, with
    their Meson build files: Xapian (and openzim/xapian-meson for Windows),
    libcurl, libmicrohttpd, zlib, xz/liblzma (including the copy libzim
    builds on Windows), zstd, ICU (including the 74.1 source of the Windows
    DLLs), pugixml, Mustache, docopt.cpp, Boost.Regex and libuuid;
  - the GNU C library linked into the static Linux builds: Ubuntu's glibc
    2.35-0ubuntu3.11 (read from Kiwix's build image) for x86_64, and glibc
    2.31 with the crosstool-NG patches and toolchain configuration for
    arm64.

  GCC's runtime libraries are not included (GCC Runtime Library Exception);
  macOS and Windows builds use the operating system's libraries.
  `scripts/package.sh` puts the matching kiwix-tools source and
  `licenses/SOURCES.txt` in every archive and publishes the whole set,
  with a SHA256SUMS list, as `arcana-<version>-kiwix-sources.tar` next to
  the binaries on the release page (§6(d): same place, clear directions).
  The release workflow checks the set against that list. `SOURCES.txt`
  explains which file belongs to which build, and how each version was
  established. When kiwix-tools is updated, the pins are re-read from the
  kiwix-build commit that released it.
- **Visual C++ runtime (Windows, maintainer decision 2026-11-22).**
  Kiwix's Windows kiwix-serve needs msvcp140.dll, vcruntime140.dll and
  vcruntime140_1.dll and does not ship them, so it failed on computers
  without the Visual C++ Redistributable. The Windows build now carries
  them beside kiwix-serve.exe (`scripts/fetch-vcruntime.sh`): unmodified
  files from Microsoft's official `VC_redist.x64.exe`, the installer and
  each DLL pinned by SHA-256, with `VCRUNTIME-NOTICE.txt`. Microsoft
  documents this "local deployment" of the redistributable files; they
  stay under Microsoft's terms. For kiwix-serve (GPL-3.0-or-later) they
  are System Libraries of the compiler, which GPL-3.0 allows to accompany
  the program without being part of its Corresponding Source. Windows
  loads them from the program's folder first, so they must be at least as
  new as the toolset kiwix-serve was built with (14.44 for 3.8.1); the
  release workflow runs the Windows build and checks that these copies are
  the ones loaded.
- **Notices.** `bin/<platform>/kiwix/NOTICE.txt` and
  `THIRD_PARTY_NOTICES.md` identify the program, its license and its
  source.

## Map, GPS and future components

| Component | License | Obligation |
|---|---|---|
| MapLibre GL JS | BSD-3 | Keep notice in vendored file |
| PMTiles spec / go-pmtiles | CC0 / BSD-3 | Notice |
| Protomaps basemap style | BSD-3 code, CC0 design | Notice |
| basemaps-assets fonts (Noto) | SIL OFL 1.1 | Ship OFL text; do not sell fonts alone |
| basemaps-assets sprites | MIT (tangrams) | Notice |
| OSM-derived tiles | ODbL 1.0, "Produced Work" | Visible "© OpenStreetMap contributors" + link on the map |
| Arcana-built gazetteer / routing graphs | ODbL (Derivative Database) | Publish build scripts (ODbL §4.6 "method" option); attribute |
| Copernicus DEM | Copernicus license | Required notice + liability disclaimer passed to users |
| Mapterhorn terrain | Per-source (CC-BY 4.0, PD, Licence Ouverte, DL-DE) | Per-source attribution file |
| go-nmea | MIT | Notice |
| go.bug.st/serial | BSD-3 | Notice |
| Valhalla (if adopted) | MIT | Notice |

## Content redistribution

Arcana is a *download manager and reader*. In the default flow each user
downloads content directly from publishers' mirrors, so Arcana does not
redistribute catalog content.

Redistribution happens when someone **copies a prepared drive** to others,
**exports a collection** (Phase 2) or **sells preloaded drives or
appliances**. The catalog therefore records, per resource:
- `license.id`, `license.url` and `license.verified`;
- `license.redistribution`: `permitted`, `noncommercial`, `restricted` or
  `unknown`.

Phase 2 export will warn before exporting `noncommercial`, `restricted` or
`unknown` items, and will include each resource's license and attribution
in the export manifest.

### Status of notable sources (verified 2026-11-22)

| Source | License | Redistribution | In catalog |
|---|---|---|---|
| Wikipedia, Wiktionary, Wikivoyage, Wikibooks, Wikiversity, Wikiquote, Wikisource (Kiwix ZIMs) | CC BY-SA 4.0 text; media individually licensed | Permitted with attribution and share-alike | Yes (verified) |
| WikiMed / mdwiki (WikiProjectMed) | CC BY-SA | Permitted | Yes (Wikipedia medicine verified; mdwiki family marked unknown pending review) |
| Stack Exchange sites | CC BY-SA 2.5/3.0/4.0 by post date | Permitted | Yes (verified) |
| iFixit | CC BY-NC-SA 3.0 | Non-commercial only | Yes (verified, NC) |
| Khan Academy | CC BY-NC-SA (version unstated) | Non-commercial only | Yes (NC; archive dated 2023-03) |
| TED talks | CC BY-NC-ND 4.0 + TED usage policy restricting use outside TED.com/YouTube | **Restricted** | Yes (restricted, personal use) |
| PhET simulations | CC BY 4.0 for sims published before 2026-03-29; newer unverified | Unknown until reviewed | Yes (unverified) |
| Project Gutenberg | Public domain in the US; PG trademark license (20 % royalty if charging with the PG name; strip PG references otherwise) | Permitted with conditions; check local law outside the US | Yes (verified, notes) |
| CIA World Factbook, CDC travel health (Kiwix) | US federal works (17 U.S.C. §105); logos and third-party images excepted | Permitted | Yes (verified) |
| Ready.gov (Kiwix, English and Spanish) | FEMA's web policy: "Most material on FEMA.gov is free of copyright and may be copied and distributed without permission"; licensed photos and graphics are credited and excepted | Permitted | Yes (verified 2026-11-26) |
| Home care and herbs (Project NOMAD's remedies lists) | Apache-2.0 compilation of summaries of CDC, MedlinePlus, FDA and NCCIH pages (US federal works) | Permitted with the Apache-2.0 notice | Yes (built in; ADR-0028) |
| Canadian Prepper, Urban Prepper, Learning Self-Reliance videos (Kiwix) | Creators' videos; terms not yet reviewed | Unknown | Yes (unverified) |
| MedlinePlus (Kiwix) | NLM pages are US federal work; the A.D.A.M. Medical Encyclopedia, ASHP drug monographs, Natural Medicines herb monographs and many images are copyrighted and licensed to NLM | **Restricted** | Yes (verified 2026-11-26, restricted) |
| 47 CFR Part 97 (govinfo annual CFR) | US federal work | Permitted | Yes (bundled, hash-pinned) |
| zimgit-* collections (water, medicine, post-disaster…) | Bundled documents' licenses unverified | Unknown | Yes (unverified) |
| freeCodeCamp curriculum | Code BSD-3; curriculum no stated license | Unknown | Unverified |
| WHO publications | CC BY-NC-SA 3.0 IGO (since 2016); older titles not relicensed | Non-commercial | Not yet |
| Hesperian "Where There Is No Doctor" | Permission required for **any digital format** | Restricted | **Excluded** |
| MSF clinical guidelines | No open license found | Restricted | **Excluded** |
| ARRL band plan | All rights reserved | Restricted | **Excluded** (write band-plan summaries from 47 CFR 97 instead) |
| IARU band plans | No reuse terms found | Unknown | Not yet |
| OpenStax | 80 titles CC BY-NC-SA 4.0; 38 CC BY 4.0 (mostly retired editions) | Mixed | Not yet (per-title review) |
| Practical Action | All rights reserved | Restricted | Excluded |
| "Survivor Library" | Rights statement unverifiable; items after 1930 may be in copyright | High risk | **Excluded** |
| OpenStreetMap | ODbL 1.0 | Permitted with attribution / share-alike for databases | Phase 3 |
| Feature films from the Internet Archive (13 titles) | Public domain in the US (copyright not renewed, or no valid notice), per title; status elsewhere differs | Permitted in the US | Yes (bundled, hash-pinned; ADR-0027) |
| Silent films on the Internet Archive | Film public domain in the US; scores added to the copies have unknown rights | Unknown | **Excluded** |
| Blender Studio open films (Kiwix) | Creative Commons by film; not yet reviewed | Unknown | Yes (unverified) |

## Release checklist (maintainer action)

1. Run `go list -deps` and refresh `THIRD_PARTY_NOTICES.md`. Copy every
   license text into `licenses/` in the bundle.
2. Include the kiwix sources and `SOURCES.txt` (automated by
   `scripts/package.sh`; when kiwix-tools changes, re-read every pin from
   the kiwix-build commit that released it, as described in `SOURCES.txt`).
3. Confirm the bundled catalog's license records against the publishers'
   current pages, and update `lastVerified`.
4. Make sure no restricted content ships inside the bundle. Today only the
   CC0 sample ships.

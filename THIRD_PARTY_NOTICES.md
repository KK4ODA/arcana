# Third-party software and content

Arcana is licensed under the GNU General Public License, version 3 or later
(see `LICENSE`). It includes or distributes the components below. This file
is regenerated for each release (see `docs/development.md`, "Dependency
review"); the list here matches `go.mod` at the time of writing.

## Go modules compiled into the `arcana` program

| Module | Version | License |
|---|---|---|
| modernc.org/sqlite | v1.62.0 | BSD-3-Clause (embeds SQLite, public domain) |
| modernc.org/libc | v1.80.1 | BSD-3-Clause |
| modernc.org/mathutil | v1.7.1 | BSD-3-Clause |
| modernc.org/memory | v1.12.1 | BSD-3-Clause |
| golang.org/x/sys | v0.50.0 | BSD-3-Clause |
| golang.org/x/time | v0.16.0 | BSD-3-Clause |
| github.com/google/uuid | v1.6.0 | BSD-3-Clause |
| github.com/remyoudompheng/bigfft | v0.0.0-20230129092748 | BSD-3-Clause |
| github.com/dustin/go-humanize | v1.1.0 | MIT |
| Go standard library | 1.26 | BSD-3-Clause |

The full license texts are in each module's source, available from
`proxy.golang.org` and reproduced in release archives under `licenses/`.

## Programs distributed alongside Arcana (not linked)

| Program | Version | License | Source |
|---|---|---|---|
| kiwix-serve (kiwix-tools) | 3.8.2 (Linux, macOS), 3.8.1 (Windows) | GPL-3.0-or-later | https://github.com/kiwix/kiwix-tools, https://download.kiwix.org/release/kiwix-tools/ |

kiwix-serve is statically linked by the Kiwix project against libkiwix
(GPL-3.0-or-later), libzim (GPL-2.0-or-later), Xapian (GPL-2.0-or-later),
ICU (Unicode License), libmicrohttpd (LGPL-2.1-or-later), zstd and xz/liblzma,
pugixml (MIT), libcurl (curl license) and others. Arcana redistributes the
official, unmodified binaries published by Kiwix. Release archives must
include, or point to with a written offer, the corresponding source code;
see `docs/design/08-licensing.md` for the release checklist.

## Content

| Item | License |
|---|---|
| Arcana sample library (`testdata/sample/`) | CC0-1.0 — written for this project |
| 47 CFR Part 97 (bundled catalog entry; downloaded, not shipped) | US federal government work, public domain in the US |

Reference content downloaded through the catalog is licensed by its
publishers. Each resource's license, as recorded in the catalog, is shown in
Arcana and stored with the installed resource.

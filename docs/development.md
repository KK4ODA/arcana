# Development guide

## Setup

- Go 1.26 or newer (`go.mod` pins the language version; the toolchain is
  fetched automatically).
- `scripts/fetch-kiwix.sh` downloads the pinned kiwix-tools release for your
  platform into `third_party/kiwix/<os>-<arch>/kiwix/`, after checking its
  SHA-256.
- Optional:
  - Node.js 22 with Playwright, for the UI tripwire in the acceptance test.
  - python-libzim, only to regenerate the sample archive
    (`tools/sample-zim/make_sample.py`).

## Everyday commands

| Task | Command |
|---|---|
| Run against a scratch library | `go run ./cmd/arcana --library /tmp/arcana-dev` (opens setup the first time) |
| Unit + integration tests | `make test` (or `go test ./...`) |
| Race detector | `make race` |
| Offline acceptance test (Linux) | `make acceptance`; add `ARCANA_ACCEPTANCE_ONLINE=1` to include a real Kiwix download |
| Lint | `make vet`, `gofmt -l .` |
| Download benchmark | `go test -run X -bench DownloadThroughput ./internal/download` |
| Portable bundle | `scripts/package.sh 0.1.0` → `dist/Arcana/`, `dist/arcana-0.1.0-portable.zip` |

On Ubuntu 24.04 the acceptance test needs unprivileged user namespaces:
`sudo sysctl -w kernel.apparmor_restrict_unprivileged_userns=0`.

## Repository layout

```
cmd/arcana/            CLI entry point and launcher behavior
internal/app/          wires modules for an open library; refresh, storage report, updates, import
internal/catalog/      resource model, validation, providers, metalinks, presets, recommender
internal/content/      publishing, sidecars, integrity checks, garbage collection
internal/download/     persistent verified download queue, safe HTTP client
internal/kiwix/        kiwix-serve supervision, proxy, search federation
internal/library/      library layout, discovery, locking, settings
internal/server/       HTTP listeners, access policy, JSON API
internal/storage/      volume detection (per OS) and capacity planning
internal/store/        SQLite index
internal/fsutil/       atomic writes, safe paths, OS locks
internal/maps/         PMTiles reader/writer, region extraction, overzoom, gazetteer, maps provider
internal/gps/          NMEA tracker, position sources, simulator, tracks, waypoints, GPX
internal/auth/         remote-administration password (Argon2id), sessions, sign-in throttling
internal/mdns/         mDNS responder and DNS-SD advertisement
internal/tlsutil/      self-signed and custom certificates, HTTP/HTTPS on one port
internal/hotspot/      Wi-Fi hotspot through NetworkManager (Linux)
internal/web/static/   embedded UI (ES modules, CSS)
packaging/             launcher and first-run readme for release bundles
packaging/appliance/   appliance installer, systemd units, captive-portal rules (docs/appliance.md)
internal/sdnotify/     systemd readiness and watchdog notifications
internal/integration/  integration API tokens, scopes and pairing
scripts/               fetch-kiwix.sh, package.sh
test/acceptance/       offline acceptance test and UI tripwire
testdata/sample/       CC0 sample ZIM
tools/sample-zim/      generator for the sample ZIM
tools/map-regions/     generator for internal/maps/regions.json (measured region sizes)
tools/fetch-map-assets.sh  refreshes vendored MapLibre, map styles, glyphs and sprites
docs/                  design package, ADRs, specs, guides
```

## HTTP API (internal; may change)

The UI's API lives under `/api/` on the application origin. GET requests
are open to local and LAN clients (except settings and integrity checks,
which need an administrator, and drives, file paths and private notes,
which are host-only). Other methods require the `X-Arcana-Token` header
from `GET /api/session`, and either a loopback client or a network client
signed in as administrator (for whom host-only routes stay closed). The
versioned integration API for other programs is designed in
docs/design/14-radio-and-integrations.md.

| Endpoint | Purpose |
|---|---|
| `GET /api/session` | Version, setup mode, admin flag, content port, token (local only) |
| `GET /api/status` | Dashboard: installed, storage plan, offline readiness, updates, sharing, GPS, attention |
| `GET /api/library`, `DELETE /api/library/{id}` | Installed resources; remove |
| `GET /api/search?q=` | Full-text search across installed archives |
| `GET /api/catalog?q=&category=&lang=&sort=&offset=&limit=` | Catalog query |
| `GET /api/catalog/facets`, `GET /api/catalog/item/{id}` | Categories, languages, providers; one resource with editions |
| `POST /api/catalog/refresh` | Refresh enabled providers |
| `GET /api/presets`, `POST /api/recommend` | Collections and explained recommendations |
| `POST /api/storage/plan` | Capacity plan for a selection |
| `GET/POST /api/downloads`, `POST /api/downloads/{id}/{pause,resume,cancel}`, `DELETE /api/downloads/{id}` | Queue |
| `GET /api/updates` | Installed vs. latest known editions |
| `GET /api/check?full=1` | Integrity check |
| `GET/PUT /api/settings` | Settings |
| `GET /api/server` | Addresses and ports |
| `GET /api/setup/volumes`, `POST /api/setup/init` | First-run setup |
| `GET/POST /api/user/bookmarks`, `DELETE /api/user/bookmarks/{id}` | Bookmarks (readable by network clients) |
| `GET/PUT /api/user/favorites` | Favorite resources |
| `GET/POST/DELETE /api/user/recent` | Reading history (host only) |
| `GET/POST /api/user/notes`, `DELETE /api/user/notes/{id}` | Notes (host only) |
| `GET/POST /api/user/documents`, `DELETE /api/user/documents/{name}` | User documents (host only; upload body is the file, name in `X-Filename`) |
| `GET /api/documents/text?key=` | Extracted text for in-app preview |
| `GET /api/tasks`, `POST /api/tasks/{id}/cancel` | Background tasks |
| `POST /api/transfer/export`, `GET /api/transfer/scan?source=`, `POST /api/transfer/import`, `POST /api/manifest` | Drive-to-drive transfer |
| `POST /api/check/full`, `POST /api/documents/reindex` | Maintenance |
| `GET /api/app/update`, `POST /api/app/update/{check,download,stage,apply,rollback}` | Application updates |
| `POST /api/presets` | Save a selection as a collection |
| `GET /maps/tiles/{z}/{x}/{y}.mvt` | Merged vector tiles from every installed map (derived from an ancestor where missing; 204 when nothing covers the tile) |
| `GET /maps/fonts/{fontstack}/{range}.pbf` | Map label glyphs (empty set for ranges not bundled) |
| `GET /api/maps`, `GET /api/maps/search?q=` | Installed map sets and indexing progress; offline place search |
| `POST /api/maps/plan`, `POST /api/maps/areas` | Measure / queue a custom area (`{name, bbox:[w,s,e,n], maxZoom}`; contacts the map source) |
| `GET /api/auth`, `POST /api/auth/login`, `POST /api/auth/logout` | Remote administration status; sign in (JSON `{password}`, sets the session cookie, returns the session token); sign out |
| `PUT/DELETE /api/auth/password` | Set or remove the administration password (host only) |
| `GET /api/server/qr.svg?url=`, `GET /api/server/wifi-qr.svg` | QR codes for this server's own addresses; for joining the hotspot (administrators) |
| `PUT /api/server/tls` | HTTPS mode: `""`, `self-signed` or `custom` (host only) |
| `GET/POST /api/server/hotspot` | Hotspot status; `{action: start/stop, ssid, password, device}` (host only) |
| `GET/POST /api/integrations`, `DELETE /api/integrations/{id}`, `POST /api/integrations/pair/{id}/{approve,deny}` | Integration tokens and pairing requests (host only) |
| `GET/POST /api/peers`, `DELETE /api/peers/{id}`, `GET /api/peers/{id}/library`, `POST /api/peers/{id}/pull` | Other Arcana libraries to copy from (host only) |
| `/api/integration/v1/*` | Versioned API for other programs: see [integration-api.md](integration-api.md) |
| `GET /api/gps`, `GET /api/gps/stream` | Position status; server-sent events (host only unless sharing is on) |
| `GET /api/gps/ports` | Serial ports (host only) |
| `GET/POST /api/gps/waypoints`, `DELETE /api/gps/waypoints/{id}` | Waypoints |
| `GET /api/gps/tracks`, `GET/DELETE /api/gps/tracks/{name}` | Recorded and imported tracks (single track as GeoJSON) |
| `GET /api/gps/export?tracks=a.gpx,b.gpx`, `POST /api/gps/import?name=` | GPX export (waypoints plus chosen tracks); import (body is the GPX file) |

The content origin (port + 1) serves `/kiwix/*` (proxied kiwix-serve, with
`/_arcana/frame.js` added to HTML pages so the reader learns the current page),
`/files/{id}/{name}` (installed documents) and `/userdocs/{name}` (the
user's documents, host only).

## Making a release

1. Move the changes under a finished heading in `CHANGELOG.md`:
   `## X.Y.Z — YYYY-MM-DD` (the release notes are taken from it; a section
   still marked *Unreleased* is refused).
2. Tag and push: `git tag -a vX.Y.Z -m "Arcana X.Y.Z" && git push origin vX.Y.Z`.
   A tag with a suffix (`v0.7.0-rc.1`) makes a pre-release.
3. `.github/workflows/release.yml` then runs every CI check (Linux,
   Windows, macOS, offline acceptance test, appliance checks), builds the
   files with `scripts/package.sh`, verifies them, adds build provenance
   (public repositories), and creates the GitHub release with notes from
   `scripts/release-notes.sh`.

Release files: one archive per platform, the all-platform portable zip (also
the in-app update archive), `arcana-X.Y.Z-kiwix-sources.tar` (the complete
source of kiwix-serve and every library built into it, with Kiwix's build
recipes, about 170 MB; see `packaging/SOURCES.txt`), `release.json` and `SHA256SUMS`. Build them locally with
`scripts/package.sh X.Y.Z` (needs `zip`, and `cabextract` for the Visual
C++ runtime of the Windows build; downloads kiwix-serve binaries and
sources, all checked against pinned SHA-256 values).

## Release signing

```
go run ./tools/release-sign keygen -out arcana-release.key   # once; keep offline; prints the public key
ARCANA_UPDATE_PUBKEY=<public key> ARCANA_SIGNING_KEY=$(cat arcana-release.key) scripts/package.sh 0.2.0
```

`package.sh` writes `dist/Arcana/release.json` (offline updates from a copied
folder) and `dist/release.json` (online updates of the zip), and signs both
when `ARCANA_SIGNING_KEY` is set. The release workflow reads the key from the
`ARCANA_SIGNING_KEY` secret and the public key from the `ARCANA_UPDATE_PUBKEY`
variable. If the variable is set but the secret is not (the key is kept
offline), the workflow creates a draft: sign `release.json` offline
(`go run ./tools/release-sign sign -in release.json`), upload
`release.json.sig` to the draft, then publish it. Without either, in-app
updates stay off and the release says so.

## Dependency review

Before each release:

1. `go list -deps -f '{{if .Module}}{{.Module.Path}} {{.Module.Version}}{{end}}' ./cmd/arcana | sort -u`
2. Check each module's license and update `THIRD_PARTY_NOTICES.md`.
3. Run `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`.
4. If kiwix-tools changes: update the versions and SHA-256 pins in
   `scripts/fetch-kiwix.sh` after verifying the archives against Kiwix's
   published checksums. Then run the kiwix integration tests and the
   acceptance test.

## Map regions and assets

`internal/maps/regions.json` lists every offered region with sizes measured
against one planet build. To regenerate it after a basemap schema change:

```sh
# Natural Earth 1:50m admin-0 and admin-1 boundaries as GeoJSON (public domain)
go build -o /tmp/map-regions ./tools/map-regions
/tmp/map-regions -admin0 ne_50m_admin_0_countries.geojson -admin1 ne_50m_admin_1_states_provinces.geojson \
  -planet https://build.protomaps.com/YYYYMMDD.pmtiles -cache /tmp/planet-dirs -out internal/maps/regions.json
```

The first run downloads the planet's leaf directories (about 350 MB) into
the cache; planning all regions then takes about 15 minutes. Map styles,
glyphs, sprites and MapLibre are refreshed with `tools/fetch-map-assets.sh`
(needs node and npm).

To try GPS without hardware, choose Settings → Maps and position →
Simulator, or send NMEA over UDP:

```sh
printf '$GPRMC,120000.00,A,4936.6600,N,00607.8000,E,0.0,0.0,221126,,,A*58\r\n' | nc -u -w1 127.0.0.1 10110
```

## Regenerating the sample archive

```
python3 -m venv /tmp/zimenv && /tmp/zimenv/bin/pip install libzim
/tmp/zimenv/bin/python tools/sample-zim/make_sample.py testdata/sample/arcana-sample_en_2026-11.zim
```

libzim writes a new UUID each time, so the file's SHA-256 changes. Update
the hash and size in `internal/catalog/bundled/catalog.json`.

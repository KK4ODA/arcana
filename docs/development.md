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
internal/web/static/   embedded UI (ES modules, CSS)
packaging/             launcher and first-run readme for release bundles
scripts/               fetch-kiwix.sh, package.sh
test/acceptance/       offline acceptance test and UI tripwire
testdata/sample/       CC0 sample ZIM
tools/sample-zim/      generator for the sample ZIM
docs/                  design package, ADRs, specs, guides
```

## HTTP API (internal; may change)

The UI's API lives under `/api/` on the application origin. GET requests
are open to local and LAN clients (except settings, drive list and
integrity checks, which are local-only). Other methods require the
`X-Arcana-Token` header from `GET /api/session` and a loopback client. The
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

The content origin (port + 1) serves `/kiwix/*` (proxied kiwix-serve) and
`/files/{id}/{name}` (installed documents).

## Dependency review

Before each release:

1. `go list -deps -f '{{if .Module}}{{.Module.Path}} {{.Module.Version}}{{end}}' ./cmd/arcana | sort -u`
2. Check each module's license and update `THIRD_PARTY_NOTICES.md`.
3. Run `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`.
4. If kiwix-tools changes: update the versions and SHA-256 pins in
   `scripts/fetch-kiwix.sh` after verifying the archives against Kiwix's
   published checksums. Then run the kiwix integration tests and the
   acceptance test.

## Regenerating the sample archive

```
python3 -m venv /tmp/zimenv && /tmp/zimenv/bin/pip install libzim
/tmp/zimenv/bin/python tools/sample-zim/make_sample.py testdata/sample/arcana-sample_en_2026-11.zim
```

libzim writes a new UUID each time, so the file's SHA-256 changes. Update
the hash and size in `internal/catalog/bundled/catalog.json`.

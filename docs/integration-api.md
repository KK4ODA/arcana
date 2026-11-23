# Arcana integration API (v1)

Programs such as RadioPost (offline messaging) or Aether HF can use an
Arcana library: search with citations, read articles as plain text, look up
coordinates, read the GPS position, follow changes, and copy resources
between Arcana libraries. The API is separate from Arcana's own interface
API, versioned, and **read-only**: nothing in v1 changes the library.

- Base: `http://<host>:8470/api/integration/v1/` (HTTPS on the same port
  when the host enables it).
- On the local network the service is advertised by DNS-SD as
  `_arcana._tcp` with TXT `api=/api/integration/v1/` and `apiversion=1`.
- JSON with stable field names; within v1 fields are only added.
- Every call except discovery and pairing needs
  `Authorization: Bearer <token>`. Loopback clients need a token too.

## Getting a token

**Pairing (recommended).** The program asks; the operator approves.

```sh
curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"name":"RadioPost","scopes":["search:read","documents:read"]}' \
  http://arcana.local:8470/api/integration/v1/pair
# {"id":"…","code":"258342","secret":"…","expires":"…","message":"…"}
```

Show `code` to the user. The operator sees the request under
*Settings → Integrations* with the same code and approves it. Then:

```sh
curl -s "http://arcana.local:8470/api/integration/v1/pair/<id>?secret=<secret>"
# {"status":"approved","token":"arc1_…"}   (pending | approved | denied | unknown)
```

The token is handed out once; requests expire after 10 minutes, and at
most 8 can be pending.

**Manual.** The operator creates a token in *Settings → Integrations* and
pastes it into the program.

Tokens are stored only as SHA-256 hashes in `user/integrations.json` and
can be revoked at any time.

## Scopes

| Scope | Allows |
|---|---|
| `search:read` | `GET /search` |
| `documents:read` | `GET /documents/{resource}/text`, `/excerpt`; library events |
| `documents:share` | `POST /share/excerpt` |
| `maps:read` | `GET /maps/reference` |
| `location:read` | `GET /location`; location events |
| `library:read` | `GET /library`, `GET /files/{id}/{name}` (another Arcana copying); library events |

## Endpoints

| Method and path | Result |
|---|---|
| `GET /` | `{api, version, server, authenticated, scopes, granted?, library?}` — no token needed |
| `POST /pair`, `GET /pair/{id}?secret=` | Pairing, above |
| `GET /search?q=&limit=` | `{query, results:[{resourceId, title, snippet, path, page?, citation}]}`; snippets are plain text; `page` (from 1) is the best matching page when the result is a PDF document; the host's private documents are never included |
| `GET /documents/{resource}/text?path=&max=` | `{article:{title, path, text, truncated}, citation}`; `path` as returned by search (archives) or a file name (documents); `max` bytes, default 20 000, at most 2 MiB |
| `GET /documents/{resource}/excerpt?path=&max=` | `{title, path, excerpt, truncated, footer, citation}`; `max` default 1 200, at most 4 000; `footer` is a one-line attribution including warnings |
| `POST /share/excerpt` `{resource, path}` | `{code, path:"/s/<code>", urls, expires}`: a 24-hour link people on the network can open in Arcana's reader |
| `GET /location` | `200 {state, stale, age, source, fix, maidenhead}` when there is a current, stale or fixed position; `404 {state}` when there is none |
| `GET /maps/reference?lat=&lon=` | `{decimal, dms, ddm, maidenhead, nearby:[{name, kind, lat, lon, distance, bearing}]}`; nearby places come from installed maps |
| `GET /events` | Server-sent events: `hello`, `location` (at most once a second), `library` (installed content changed); keep-alive comments every 30 s |
| `GET /library` | `{library:{id,label}, resources:[{resource, version, files:[{name,size,sha256}], verification, unverified}]}` |
| `GET /files/{id}/{name}` | The installed file, with `Range` support, `ETag` and `X-Arcana-SHA256` |

Errors are JSON `{"error": "..."}` with 400 (bad input), 401 (no or
invalid token), 403 (scope missing), 404 (not found or no position).

## Citations

Every content response carries a `citation`:

```json
{"resourceId":"kiwix.wikipedia_en_medicine_maxi","title":"Wikipedia Medicine","publisher":"Kiwix",
 "edition":"2026-10","published":"2026-10-03",
 "license":{"id":"CC-BY-SA-4.0","name":"…","url":"…","attribution":"…"},
 "sourceUrl":"…","warnings":[{"kind":"medical","text":"…"}]}
```

A medical excerpt therefore cannot be passed on without its dated warning.
Programs sending text over low-bandwidth links should append `footer`.

## Example: answering a question with a cited excerpt

```sh
T=arc1_...
curl -s -H "Authorization: Bearer $T" "http://arcana.local:8470/api/integration/v1/search?q=water+purification&limit=3"
curl -s -H "Authorization: Bearer $T" \
  "http://arcana.local:8470/api/integration/v1/documents/kiwix.wikipedia_en_medicine_maxi/excerpt?max=800&path=/kiwix/content/wikipedia_en_medicine_maxi_2026-10/A/Water_purification"
```

## Copying between Arcana libraries

Library B copies from library A without internet:

1. On A: *Settings → Integrations*, create a token with `library:read`.
2. On B: *Settings → Other Arcana libraries*, add A's address and token,
   *Browse*, choose resources, *Copy selected*.

B downloads through its normal queue (resumable, space-checked) and
verifies every file against the SHA-256 that A recorded, and against the
publisher's checksum when B's catalog has the same edition. Map regions are
copied as finished files. B's download client may reach A's local address
only because A was added explicitly; catalog downloads still cannot reach
local addresses.

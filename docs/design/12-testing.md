# Testing strategy

## Layers

| Layer | Where | Runs | What it proves |
|---|---|---|---|
| Unit | `internal/*/…_test.go` | every push, 3 OSes | Pure logic: validation, version ordering, capacity math, name safety, recommender, snippet sanitizing |
| Integration (in-process) | `internal/download`, `internal/server`, `internal/kiwix`, `internal/app` | every push | Real HTTP servers (`httptest`), real SQLite, real kiwix-serve (pinned binary), real file system |
| Offline acceptance | `test/acceptance` (build tag `acceptance`, Linux) | every push (CI job) | The whole product with no internet, as a user would run it |
| UI tripwire | `test/acceptance/ui-tripwire.js` (Playwright) | inside acceptance | No external requests, no script errors in any view |
| Benchmarks | `go test -bench` | on demand | Throughput and latency figures in 11-performance.md |
| Manual platform checks | docs/portable-use.md checklist | before releases | Real Windows, macOS, Pi hardware, real USB drives |

Race detection (`-race`) runs on Linux CI. `govulncheck` and `gofmt`/`go vet`
run in the lint job.

## Coverage of the required areas

| Area | Tests |
|---|---|
| Storage calculations | `storage`: brief's 1 TB example, over/tight plans, nominal capacity, GB/GiB formatting, FAT32 per-file limit, `CheckWrite` |
| Content selection & dependency resolution | `catalog`: recommender across 20 GB / 115 GB / 2 TB budgets, language fallback, already-installed handling, unavailable families. (Dependency resolution: schema field validated; resolution logic arrives with the first provider that declares dependencies, Phase 2) |
| File integrity | `content`: quick check finds missing and orphaned resources; full check finds same-size corruption; hostile sidecar paths rejected |
| Download interruption & resumption | `download`: connection drop mid-body (resumes with Range), constructed power-loss state (truncates garbage past checkpoint, restores hash state, resumes from exact offset), server ignoring Range (restarts cleanly), pause/resume, shutdown/restart |
| Version comparison | `catalog.TestCompareVersions`: dates, Kiwix `2026-11a` rebuilds, dotted versions, pre-releases |
| Safe resource updates | `download.TestUpdateReplacesOnlyAfterVerification`: corrupted v2 leaves v1 intact; good v2 replaces v1 and removes old directory |
| Platform path handling | `fsutil`: traversal, absolute, backslash, drive-letter, Windows device names, trailing dot/space, length; `library`: relocation to a new path |
| GPS parsing, UDP reception | `internal/gps`: real receiver sentences, bad checksums and garbage lines, no-fix sentences, generated sentences round-tripped for all hemispheres, UDP/TCP/gpsd sources against local servers, connection errors, simulator, current/stale/lost timing. Not yet: recorded logs with dropouts and clock jumps, UDP flood tests |
| Offline map loading | `internal/maps`: PMTiles header/directory round trips, Hilbert IDs, region extraction with interruption and resume, source-change detection, plan caching, overzoom (synthetic and real tiles), region catalog validation; acceptance step 5 |
| Library searching | `store` (FTS5, diacritics, injection-proof queries), `kiwix` (real kiwix-serve, highlight extraction), `server` end-to-end, acceptance |
| HTTP server authorization | `server.TestSessionAndAccessPolicy`: Host rebinding, LAN read vs. write, token, cross-site, local-only endpoints, content origin read-only, map and GPS endpoints; `TestRemoteAdministration`: password lifecycle, cookie flags, session tokens, host-only routes, throttling, sign-out and password change; acceptance steps 7 and 7b over a real second address |
| Network features | `auth` (Argon2id store, sessions, limiter), `mdns` (responses, legacy unicast, conflicts, a live query), `tlsutil` (certificate renewal, HTTP and HTTPS on one listener, idle clients), `hotspot` (against a simulated nmcli), `server.TestNetworkFeatures` and `TestCaptivePortal`; browser test of sign-in and HTTPS secure context (manual run) |
| Appliance | CI job `appliance`: shellcheck, installer dry run, `systemd-analyze verify`, nftables syntax; `sdnotify` tests; readiness, watchdog and captive port verified against a simulated service manager. Not yet: Raspberry Pi hardware, power-loss cycles |
| Unexpected drive removal | Simulated by kill -9 + relocation (acceptance 8–9) and by damaged/missing DB (store, acceptance 11). Physical unplug tests are manual (checklist) |
| Database recovery | `store.TestRecoverFromDamagedDatabase`, `content.TestRebuildFromSidecars`, acceptance step 11 |
| Migration between versions | `library.TestNewerFormatOpensReadOnly`, `TestSettingsPreserveUnknownKeys`; schema migrations are sequential with `user_version`; each new migration ships with a test that opens a fixture DB from the previous release |
| Hostile catalogs | `catalog.TestValidateRejectsHostileInput` (18 cases), hostile OPDS entry, private-network block |

## The offline acceptance test

`go test -tags acceptance -v ./test/acceptance` (Linux, needs `unshare` and
the kiwix-serve fetched by `scripts/fetch-kiwix.sh`):

1. **Prepare a drive.** Build Arcana, create a library, place kiwix-serve
   in `bin/`, and import the sample archive (verified against the bundled
   catalog). With `ARCANA_ACCEPTANCE_ONLINE=1`, also refresh the live Kiwix
   catalog and download a real archive.
2. **Disconnect.** Re-run inside `unshare -rn`: a new network namespace
   with only loopback and an alias address `10.99.0.1` standing in for a
   LAN device. Assert that `1.1.1.1:443` and DNS both fail.
3. **Launch** `arcana serve --lan` and wait for "offline ready".
4. **Browse and search.**
   - Open an article through the content origin.
   - Run a full-text search.
   - Browse the catalog.
   - Open Updates.
5. **Offline maps.** A synthetic map (generated with Arcana's own PMTiles writer and imported offline; a real Luxembourg extract when `ARCANA_ACCEPTANCE_ONLINE=1`) is indexed and found by place search; native and derived tiles, glyphs, style, sprites and the MapLibre files are served; tiles outside every map return 204; planning a new area fails promptly with an explanation.
6. **Simulated GPS.** The simulator produces a current, simulated fix on the installed map while a track records; the SSE stream delivers it; a waypoint is saved and exported as GPX; then NMEA sent over UDP becomes the position, and a LAN client is refused the position.
7. **Second client.** From 10.99.0.1:
   - read the library;
   - read an article;
   - confirm the session has no token;
   - confirm a write is refused even with a stolen token;
   - (7b) remote administration: sign-in is refused before a password
     exists, a wrong password is refused, the right one allows a settings
     change, a host-only action is still refused, and sign-out ends the
     session;
   - (7c) run the Playwright tripwire over nine views, including Maps: any
     non-local request or script error fails the test.
8. **Restart the host.** Change a setting, then `kill -9` (no shutdown
   handlers). Assert that kiwix-serve exits by itself.
9. **Reconnect the drive.** Move the library to a different path (a new
   mount point or drive letter) and relaunch.
10. **Verify.**
    - The setting survived.
    - The library root updated.
    - Browsing and search work.
    - `arcana check --full` re-hashes every file successfully.
11. **Lost database.** Delete `state/arcana.db*` and relaunch; the index is
    rebuilt from sidecars and the library works.
12. **No access attempted.** Fail if the log shows any attempt to reach the
    network.

## Fixtures

- **Sample archive.** `testdata/sample/arcana-sample_en_2026-11.zim`
  (63 kB) is original CC0 text generated by `tools/sample-zim/make_sample.py`
  with python-libzim. It has a full-text index.
- **Catalog fixtures.** `internal/catalog/testdata/opds.xml` contains real
  OPDS entries plus a hostile one; `ray-charles.meta4` is a real metalink.
- **Download servers.** `httptest` servers with Range support, connection
  drops, Range ignored, 404/503 mirrors and corrupted bytes.

## Rules

- Every bug fix comes with a regression test that fails before the fix.
  Examples already in the history:
  - snippets losing highlighted terms (XML child elements);
  - a route-pattern conflict that panicked at startup;
  - a bandwidth limiter rejecting reads larger than its burst.
- No test may depend on the public internet, except the opt-in online
  preparation step.
- Tests that need kiwix-serve skip with a clear message when it is absent.
  CI always fetches it, so they always run there.

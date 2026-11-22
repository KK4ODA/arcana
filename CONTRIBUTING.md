# Contributing to Arcana

Thank you for helping. People may rely on Arcana when nothing else works,
so the bar for correctness is higher than the bar for features.

## Before you start

- For anything larger than a small fix, open an issue first and describe
  the problem in terms of what someone needs while offline.
- Read [docs/design/02-architecture.md](docs/design/02-architecture.md) and
  the ADRs in [docs/adr/](docs/adr/). A change that contradicts an ADR
  needs a new ADR that supersedes it.
- To propose content for the catalog, use the "Propose a content source"
  issue form. Resources are listed only after their license and
  redistribution terms are confirmed from the publisher's own statement.

## Ground rules

1. **Offline first.** No runtime dependency on the internet: no CDN
   assets, web fonts or remote calls outside catalog refresh and downloads
   the user starts. `TestInterfaceMakesNoExternalRequests` and the
   acceptance test enforce this.
2. **Never lose or corrupt user data.** Content operations touch only
   `content/` and `staging/`. Metadata writes are atomic. Anything
   downloaded is verified before it is published.
3. **Treat catalogs and content as hostile.** Validate at the boundary and
   never build file paths from unvalidated strings (use `fsutil.SafeJoin`).
4. **Fix root causes.** When debugging, find the cause, fix it, and add a
   regression test that fails without the fix. Do not stack special cases.
5. **Keep dependencies few.** A new Go module needs a reason in the pull
   request, a compatible license (BSD, MIT, Apache-2.0, ISC, MPL-2.0) and an
   entry in THIRD_PARTY_NOTICES.md. The shipped web UI has no npm
   dependencies (ADR-0014).
6. **Experimental work stays behind a flag or build tag** until it has
   tests. Unfinished features are not shown in the UI as working controls.

## Coding standards

- Go: `gofmt`, `go vet` clean; small packages with one responsibility; doc
  comments on exported identifiers that explain *why*; errors wrapped with
  context and written for the person who will read them in the UI.
- Platform code goes in `_windows.go` / `_linux.go` / `_darwin.go` files
  with a fallback. Every change must cross-compile for all release targets
  (`make vet` and CI).
- JavaScript: ES modules, no framework. Build DOM with `h()`; never assign
  `innerHTML` from data, except server-sanitized search snippets. Styles
  only through classes or CSSOM, because the CSP forbids inline styles.
- UI copy: plain, specific, and actionable. Show dates and sources for
  reference content.
- Tests: table-driven where possible. Use `httptest` servers for network
  behavior, temporary directories for libraries, and no sleeps where a
  condition can be polled.

## Workflow

1. Fork, branch, commit in focused steps with clear messages.
2. Run `make vet test`. Run `make acceptance` if you touched the server,
   downloads, storage or the UI.
3. Open a pull request using the template checklist.

## License of contributions

By contributing you agree that your contributions are licensed under the
project's license, GPL-3.0-or-later (see LICENSE and ADR-0012).

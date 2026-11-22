# ADR-0014: Web UI in plain ES modules, without a framework or build step

- Status: Accepted
- Date: 2026-11-22

## Context
- The UI must be embedded in the binary, work offline, and run under a
  strict CSP.
- It must be maintainable for decades, by contributors who may not follow
  front-end tooling trends.
- A large npm dependency tree is a supply-chain risk for software used in
  emergencies.

## Decision
- Write the UI as plain ES modules and CSS in `internal/web/static/`.
- A tiny `h()` helper builds elements through `textContent`.
- No framework, no bundler, no npm dependencies in the shipped UI.
- Third-party browser libraries (MapLibre, Phase 3) are vendored as
  versioned files with their licenses.

## Consequences
Some code is more verbose than with a framework. There is no build step to
break, the shipped files are exactly what is reviewed, and a test fails if
any asset references an external URL.

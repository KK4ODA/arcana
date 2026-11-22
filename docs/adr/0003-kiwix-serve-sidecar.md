# ADR-0003: Use kiwix-serve as a supervised sidecar for ZIM archives

- Status: Accepted
- Date: 2026-11-22

## Context
ZIM is the de facto format for offline reference content. libzim (C++,
GPL-2.0-or-later) and libkiwix (GPL-3.0-or-later) implement reading,
search (Xapian) and serving; kiwix-serve packages them in official static
binaries for Linux (x86-64, ARM), macOS and Windows. The brief says not to
reimplement ZIM reading without a compelling reason.

## Decision
- Run the official kiwix-serve as a child process bound to 127.0.0.1 on a
  random port. Pass ZIM paths relative to the library root, with
  `--urlRootLocation=/kiwix --blockexternal --skipInvalid
  --attachToProcess=<arcana pid>`.
- Reverse-proxy it from Arcana's content origin, and strip its permissive
  CORS header.
- When the installed set changes, start a new process, verify it, swap the
  proxy target, then stop the old process (blue/green). This releases file
  handles before old versions are deleted on Windows.
- Map archives to resources by the file-name-derived book name, falling
  back to the ZIM UUID.
- Federate search per language through the public `/search` API.
- Pin kiwix-tools versions and SHA-256 hashes in `scripts/fetch-kiwix.sh`.

## Consequences
- Arcana reuses a mature reader with zero format code and avoids
  cgo/static C++ builds in its own binary. The GPL obligation is limited to
  shipping or offering kiwix-tools source (see docs/design/08-licensing.md).
- Arcana depends on Kiwix publishing binaries. Windows builds stopped at
  3.8.1 for now (risk T2).
- Multi-archive search requires one language per query.
- Windows command lines limit the number of archives to about 350 with
  relative paths (risk T3; plan: generated `library.xml`).

## Alternatives
- Link libzim through cgo: breaks pure-Go cross-compilation, larger
  binaries, GPL linking.
- Pure-Go ZIM reader: reimplements the format and Xapian search, which the
  brief rules out.
- kiwix-js in the browser: needs service workers, which do not run on
  plain-HTTP LAN origins.

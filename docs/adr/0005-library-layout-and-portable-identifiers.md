# ADR-0005: Library layout, relative paths and portable identifiers

- Status: Accepted
- Date: 2026-11-22

## Context
The same drive moves between Windows (drive letters, case-insensitive,
reserved names), macOS and Linux (mount points).

## Decision
- A library is any folder containing `arcana-library.json` (format,
  `formatVersion`, a random `libraryId`, label).
- Fixed areas: `bin/`, `content/`, `staging/`, `state/`, `catalog/`,
  `index/`, `user/`, `logs/`. Only Arcana writes `content/` and `staging/`;
  content operations never touch `user/`.
- Stored paths are relative and use forward slashes.
- Resource IDs are lower-case `<provider>.<name>` using `[a-z0-9._-]`.
  Versions and file names are validated as single portable components (no
  Windows device names, no trailing dots or spaces).
- A newer `formatVersion` opens read-only.
- The program finds its library from `--library`, then `ARCANA_LIBRARY`,
  then the executable's folder and its parents, then the working
  directory.

## Consequences
The acceptance test moves a library to a new path with no changes. Release
bundles ship without a marker so each library gets its own identity at
first run.

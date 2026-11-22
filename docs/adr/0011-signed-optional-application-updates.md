# ADR-0011: Signed application updates that are never mandatory (Phase 2)

- Status: Accepted (implemented in 0.2.0; active once a release key is configured)
- Date: 2026-11-22

## Context
Libraries are prepared online and used offline, sometimes for years. An
update must never break a working library or be required to keep using it.

## Decision
- **Manifest.** Releases publish `release.json`, listing the version,
  per-platform files with SHA-256, the minimum library format and release
  notes. It is signed with an offline Ed25519 key (minisign-compatible);
  the public key is compiled into Arcana.
- **Checks.** Manual by default; optional scheduled checks only while
  Arcana runs.
- **Install.**
  1. Download into `staging/`.
  2. Verify the signature and hashes.
  3. Move the current `bin/<platform>/` to `bin/<platform>.previous/`.
  4. Rename the new one into place.
  5. On first successful start, keep the previous version for rollback.
- **Offline.** The same package copied from another drive installs without
  a network.

## Consequences
Release engineering needs key management and a signing step. The release
workflow (0.6.0) signs in CI when the key is provided as a secret; when the
programs trust a key but CI does not hold it, it creates a draft for a
maintainer to sign offline; with no key configured, releases are published
with in-app updates off. Application and content updates are independent.

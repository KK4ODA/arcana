# ADR-0011: Signed application updates that are never mandatory (Phase 2)

- Status: Accepted (implemented in 0.2.0; amended for 0.6.0: daily check on
  by default, one-click install with automatic restart)
- Date: 2026-11-22

## Context
Libraries are prepared online and used offline, sometimes for years. An
update must never break a working library or be required to keep using it.

## Decision
- **Manifest.** Releases publish `release.json`, listing the version,
  per-platform files with SHA-256, the minimum library format and release
  notes. It is signed with an offline Ed25519 key (minisign-compatible);
  the public key is compiled into Arcana.
- **Checks (amended 0.6.0).** Once a day while Arcana runs, the first two
  minutes after it starts; on by default, with a switch in *Settings →
  Application updates* and a *Check now* button. A check downloads only
  `release.json` and its signature from the update address and sends
  nothing but the User-Agent `Arcana/<version>`. The result is kept on the
  drive, so a known new version is still shown offline. A failed check
  (for example offline) is not retried until the next day. The maintainer
  chose on-by-default so that libraries left alone for months still learn
  about fixes the next time they are online.
- **Install (amended 0.6.0).** One action, *Update now*:
  1. Download the release archive into `staging/`.
  2. Verify the signature, the hashes and the library format range.
  3. Rename each program file to `<file>.previous`, then rename the new one
     into place (a running program can be renamed but not overwritten on
     Windows).
  4. Restart on the same port when this copy runs from the library folder
     (exec on Unix, a new process on Windows; the Linux launcher's
     temporary copy on no-exec drives is refreshed first). The restart is
     planned before step 3, because the system then reports the renamed
     file as the running program. The page reloads into the new version.
  5. The previous version stays for *Roll back* until the next update.

  Service installs (the appliance) and copies outside the library cannot
  replace themselves; the notice explains how to update them instead.
- **Offline.** The same package copied from another drive installs without
  a network.

## Consequences
Release engineering needs key management and a signing step. The
programs trust the project's release key (`internal/buildinfo`); the
release workflow (0.6.0) signs in CI with its private half, held as the
`ARCANA_SIGNING_KEY` secret, and checks the signatures against the
compiled-in key. Without the secret it leaves the release as a draft for a
maintainer to sign offline, because users' updaters would reject an
unsigned release. The daily check is the one network request Arcana makes
without being asked (docs/design/07-security.md). Application and content
updates are independent.

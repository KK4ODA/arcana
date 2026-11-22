# ADR-0009: Verified, checkpointed downloads with atomic publication

- Status: Accepted
- Date: 2026-11-22

## Context
Archives reach hundreds of gigabytes over unreliable links, onto drives
that may be unplugged. Re-reading a partial 100 GB file to resume hashing is
too slow on USB.

## Decision
- **Checkpoint.** Download to `staging/<job>/<file>.part`. Every 16 MiB or
  10 s, fsync the file and commit the byte offset together with the
  serialized SHA-256 (and publisher-algorithm) hash state to SQLite.
- **Resume.** Truncate to the last checkpoint, restore the hash state and
  send a Range request. `If-Range` is used only when no publisher hash
  exists; then the final hash proves correctness across mirrors.
- **Retry.** Network errors retry indefinitely with capped jittered
  backoff; server errors retry a bounded number of times; 4xx responses
  reject a mirror. A hash mismatch restarts once from another mirror, then
  fails.
- **Publish.** Rename into `content/<id>/<version>/`, commit the sidecar,
  then remove the old version.
- **Implementation.** Built in Go rather than with aria2, which cannot
  share Arcana's checkpoints, verification or space accounting.

## Consequences
At most 16 MiB of progress is lost to a crash. Corrupted or substituted
data never reaches `content/`. Tests cover connection drops, constructed
power-loss states, servers without Range, mirror failures and corrupted
updates.

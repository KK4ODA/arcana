# ADR-0019: Download large files in pieces from several mirrors at once

- Status: Accepted (0.6.2)
- Date: 2026-11-22

## Context
Users reported downloads far slower than their connection. Measured on a
Kiwix file (306 MB, the mirror list Kiwix returns for one location):

- One connection to each mirror ranged from 1 to 81 MB/s. The mirror Kiwix
  ranks first is not always the fastest, and Arcana used it until it
  failed.
- Several mirrors limit each connection (one went from 4.8 MB/s on one
  connection to 18.6 MB/s on four); others limit the whole mirror.
- Splitting the file evenly across mirrors makes the slowest one decide.

Map extraction had the same kind of limit: a region at high zoom levels is
thousands of small range requests, each made only after the previous one
arrived.

## Decision
- **Pieces.** A file of at least four pieces (4 MiB each) that has a
  publisher hash is fetched as pieces over up to 6 connections, at most 3
  per mirror, using HTTP/1.1 so each request gets its own connection.
- **Mirror choice.** Each of the publisher's first six mirrors gets one
  piece to measure it; then each new piece goes to the fastest measured
  mirror with a free connection. Speed is counted from the moment the
  connection is ready, so each request's wait counts but connection setup
  (TCP, TLS, proxy) does not.
  Mirrors slower than a quarter of the best are left alone.
- **No straggler waits.** When nothing new can start, a piece still in
  flight is requested again from a faster mirror if that would finish it
  in under half the time; the first copy to arrive is used.
- **Same file, same checkpoints.** Pieces are kept in memory (at most 16,
  64 MiB) and written and hashed strictly in order. The staging file,
  checkpoint and resume rules of ADR-0009 are unchanged.
- **Faults.** A mirror answering 404/403/410 or with a different file size
  is dropped for the job; server and network errors back off per mirror;
  a mirror without range support is dropped, and with none left the file
  is fetched over one connection. Because pieces may come from several
  mirrors, a final hash mismatch cannot name the faulty one: the retry
  goes mirror by mirror over one connection, and one extra attempt is
  allowed for this case.
- **Not segmented:** small files, files without a publisher hash (pieces
  from different mirrors could not be proven to belong together), and
  copies from another Arcana on the local network.
- **Maps.** Tile ranges are fetched with up to 8 requests in flight (at
  most 96 MiB ahead) and written in order; the extraction checkpoint is
  unchanged.

## Consequences
On the measured file, 2.4–5.8 MB/s became 64–80 MB/s; a map region went
from 30 s to 4 s. Memory use grows by up to 64 MiB per running download.
Mirrors see up to three connections from one download. Tests cover
several mirrors, preference for the fast one, faulty and size-mismatched
mirrors, servers without ranges, crash recovery, a corrupt mirror and the
HTTP/1.1 requirement.

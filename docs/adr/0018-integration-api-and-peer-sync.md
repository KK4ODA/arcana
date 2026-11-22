# ADR-0018: Versioned integration API with scoped tokens; peer sync over it

- Status: Accepted
- Date: 2026-11-22

## Context
RadioPost and Aether HF should use Arcana's knowledge and position without
tight coupling, and two Arcana libraries on a network should be able to
share content without the internet. Arcana's UI API is internal and
changes freely; its security model (host token, loopback-only changes,
optional remote admin) is designed for browsers, not programs.

## Decision
- A separate API under `/api/integration/v1/`, read-only, JSON with stable
  field names (additions only within v1), every content response carrying a
  citation with license and warnings.
- Bearer tokens (256-bit, `arc1_` prefix) with scopes, stored as SHA-256
  hashes in `user/integrations.json`. Loopback clients need tokens too.
- Pairing: the program requests access with a name and scopes; the
  operator approves in the UI after comparing a six-digit code; the program
  collects the token once with a secret. No unauthenticated call changes
  anything except recording a bounded, expiring request.
- Peer sync uses the same API (`library:read`): the receiving library lists
  the peer's installed resources and queues downloads whose mirrors are the
  peer's file endpoint and whose checksums are the peer's recorded SHA-256
  (cross-checked against the local catalog). The download manager adds the
  peer's token through a request hook, skips catalog resolution for these
  jobs, and may dial the peer's private address only because the user added
  that peer.
- Discovery by DNS-SD `_arcana._tcp`.

## Consequences
- RadioPost and Aether HF integrate over HTTP with no code inside Arcana.
- Peer copies are only as authentic as the peer's records unless the local
  catalog knows the publisher's checksum; the UI and sidecars say so.
- Static map rendering and native plugins remain out of scope.

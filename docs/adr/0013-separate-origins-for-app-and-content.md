# ADR-0013: Separate browser origins for the application and for content

- Status: Accepted
- Date: 2026-11-22

## Context
ZIM archives contain HTML and JavaScript written by third parties, and
kiwix-serve's CSP allows scripts with `allow-same-origin`. Served on the
same origin as Arcana's UI, a hostile archive could read the session token
and call the management API.

## Decision
- Serve the UI and API on port P (application origin).
- Serve all reference content on port P+1 (content origin): the kiwix-serve
  proxy and sidecar-listed documents. That origin is read-only and
  host-checked.
- The application CSP allows framing only the content origin.
- Documents are served with `CSP: sandbox`, and active types are forced to
  download.

## Consequences
Clients need two ports reachable (both unprivileged). The reader embeds
content in an iframe from the content origin. The model extends to future
user documents and map tiles.

# ADR-0007: Provider-based catalog; all catalog data is untrusted

- Status: Accepted
- Date: 2026-11-22

## Context
Resources come from Kiwix, community lists, government sites and users.
Catalogs may be stale, wrong or hostile.

## Decision
- Providers convert sources into a normalized `Resource`.
- `Validate()` enforces portable IDs locked to the provider's namespace,
  single safe file names, `http(s)` URLs without credentials, hash formats,
  size bounds and cleaned text.
- Redistribution may only be "permitted" when the license is marked
  verified.
- Invalid entries are dropped individually.
- Catalog caches on the drive are re-validated on load.
- A provider may resolve entries just before download (metalinks); the
  resolved result is validated again.

## Consequences
New providers are added without touching the download or storage code. The
live Kiwix catalog (3,682 entries) passes validation with zero rejections.

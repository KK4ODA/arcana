# Amateur radio module and local integrations

## Amateur radio and emergency communications content

**In the catalog today:**
- **47 CFR Part 97** (US amateur radio rules), annual govinfo edition,
  hash-pinned. It carries a jurisdiction warning and the edition date.
- **ham.stackexchange.com** (CC BY-SA, 77 MB) and
  **electronics.stackexchange.com** (4.3 GB), mapped to the radio and
  energy/electrical categories.
- The **Amateur Radio Operator** preset combines these with general
  reference and small-computer Q&A (Raspberry Pi, Arduino) for digital
  modes.
- Every resource in the radio category automatically gets the regulatory
  warning (rules differ by country and change; confirm with your licensing
  authority).

**Investigated sources and decisions:**

| Topic | Source | Status |
|---|---|---|
| US regulations | eCFR API (current, unofficial compilation) and govinfo annual CFR (official) | govinfo edition bundled; eCFR as a "current text" provider in Phase 2 with its date shown |
| Band plans | ARRL (all rights reserved), IARU R1/R2/R3 (terms unclear) | **Not redistributed.** Arcana will generate a factual frequency-privilege table from 47 CFR 97.301–97.305 (public domain), labeled with jurisdiction and edition, and link IARU plans as "download from publisher" entries once terms are confirmed |
| Operating practice, antennas, propagation, digital modes, APRS, packet, Winlink, Morse | Wikibooks (CC BY-SA; some material dated), Wikipedia articles, Stack Exchange; Morse and Q-codes in the sample library | Through Wikimedia/SE ZIMs; curated topic collections in Phase 2 |
| Emergency communications procedures | FEMA/ICS forms and NIMS publications (US federal works) | Phase 2 `govdocs` provider (ICS-205/213/214 forms, NIMS doctrine), each hash-pinned with date |
| Equipment documentation | Manufacturer manuals (copyright varies) | User-imported documents only (Phase 2), not catalog |
| Other jurisdictions | Ofcom, ISED, ACMA, etc. | Per-country entries via community feeds, each with jurisdiction and "last known update" |

No transmission functionality is in scope.

## Local integration API (design, Phase 6)

**Goal.** Let independent programs such as RadioPost (offline messaging)
and Aether HF use Arcana's knowledge and mapping services without tight
coupling. Either program must work without Arcana, and Arcana without
them.

### Principles
- **Versioned, read-mostly HTTP API** under `/api/integration/v1/`.
  - Arcana's UI API stays internal and may change.
  - JSON with stable field names; additive changes only within v1.
- **Explicit pairing.**
  1. A client generates a key pair or receives a token.
  2. The Arcana operator approves it in Settings → Integrations.
  3. The token is limited to scopes and stored hashed in `user/`.
  - Loopback clients still need a token, because other local programs and
    browser pages are not trusted by default.
- **Scopes:** `search:read`, `documents:read`, `maps:read`,
  `location:read`, `documents:share` (create a shareable excerpt).
  There is no write access to the library.
- **Discovery.** On the LAN, DNS-SD `_arcana._tcp` with a TXT record for
  the API version (Phase 4 mDNS work). A fixed loopback port is
  configurable.

### Endpoints (v1 draft)

| Endpoint | Purpose | Example consumer |
|---|---|---|
| `GET /search?q=&lang=&limit=` | Unified search results with resource ID, title, snippet, citation (publisher, edition, date, license) | RadioPost answering a "how do I purify water" request with a cited excerpt |
| `GET /documents/{resource}/{path}?format=text\|html` | Fetch an article as plain text (stripped, size-limited) for transmission over low-bandwidth links | Sending a procedure over HF via Winlink-style messages |
| `GET /documents/{resource}/{path}/excerpt?max=1200` | Bounded excerpt with attribution footer, for links where every byte counts | Aether HF message composer |
| `GET /location` | Current fix (lat, lon, accuracy, age, source), or 404 when stale | Position reports (APRS-style) composed by RadioPost |
| `GET /maps/reference?lat=&lon=` | Grid locator, nearest place (gazetteer), coordinate formats | Including a Maidenhead grid square in a message |
| `GET /maps/static?bbox=&z=` (Phase 3+) | Small rendered map image or GeoJSON for a region | Attaching a map snippet to a message |
| `POST /share/excerpt` | Create a short-lived local share code for a document excerpt that a LAN user can open | Pointing a message recipient on the local network to the full article |
| `GET /events` (SSE) | GPS fix changes, library changes | Live position in a comms dashboard |

### Data contracts
- Every content response carries `citation`: resource ID, title, publisher,
  edition, published date, license ID and URL, plus `warnings`. A medical
  excerpt therefore cannot be sent without its dated warning.
- Text extraction removes scripts and styles and keeps headings and lists.
  Length limits are enforced server-side.

### Plugin interface (beyond HTTP)
- Arcana will not load native plugins into its process; that would break
  the security model.
- Extension points are **catalog feeds** (data only), **sidecar services**
  (separate processes registered in `user/integrations.json`, reached by
  HTTP on loopback, supervised like kiwix-serve), and the **integration
  API** above.

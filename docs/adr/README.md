# Architecture Decision Records

| ADR | Title | Status |
|---|---|---|
| [0001](0001-record-architecture-decisions.md) | Record architecture decisions | Accepted |
| [0002](0002-single-go-binary-local-web-app.md) | A single Go binary serving a local web application | Accepted |
| [0003](0003-kiwix-serve-sidecar.md) | Use kiwix-serve as a supervised sidecar for ZIM archives | Accepted |
| [0004](0004-sqlite-index-sidecars-source-of-truth.md) | SQLite as a rebuildable index; sidecars are the source of truth | Accepted |
| [0005](0005-library-layout-and-portable-identifiers.md) | Library layout, relative paths and portable identifiers | Accepted |
| [0006](0006-exfat-and-durability-without-journal.md) | Recommend exFAT; durability without a journal | Accepted |
| [0007](0007-provider-catalog-untrusted-input.md) | Provider-based catalog; catalog data is untrusted | Accepted |
| [0008](0008-pmtiles-maplibre-client-rendering.md) | PMTiles and MapLibre with client-side rendering | Accepted, implemented |
| [0009](0009-verified-checkpointed-downloads.md) | Verified, checkpointed downloads with atomic publication | Accepted |
| [0010](0010-privileged-networking-outside-portable-app.md) | Keep privileged networking out of the portable app | Accepted |
| [0011](0011-signed-optional-application-updates.md) | Signed application updates, never mandatory | Accepted, implemented |
| [0012](0012-license-gpl-3.0-or-later.md) | License: GPL-3.0-or-later | Accepted (confirmed) |
| [0013](0013-separate-origins-for-app-and-content.md) | Separate browser origins for app and content | Accepted |
| [0014](0014-web-ui-without-framework-or-build-step.md) | Web UI without framework or build step | Accepted |
| [0015](0015-tile-overzoom-micro-codec.md) | Derive missing tiles from ancestors with a small MVT codec | Accepted |
| [0016](0016-gps-single-nmea-path.md) | One NMEA parsing path for every position source | Accepted |
| [0017](0017-network-serving-features.md) | Remote administration, HTTPS on the same port, built-in mDNS | Accepted |
| [0018](0018-integration-api-and-peer-sync.md) | Versioned integration API with scoped tokens; peer sync over it | Accepted |
| [0019](0019-parallel-multi-mirror-downloads.md) | Download large files in pieces from several mirrors at once | Accepted |
| [0020](0020-built-in-offline-routing.md) | Built-in offline routing from OpenStreetMap extracts | Accepted |
| [0021](0021-question-answering-over-the-library.md) | Question answering from Arcana's own search, with a llamafile sidecar | Accepted |
| [0022](0022-fda-drug-label-reference.md) | FDA drug label reference built on the user's computer | Accepted |
| [0023](0023-offline-translation-in-the-browser.md) | Offline translation in the reader's browser (Bergamot WebAssembly) | Accepted |
| [0024](0024-kolibri-built-in.md) | Kolibri built in, for Khan Academy classrooms | Accepted |
| [0025](0025-more-classroom-channels.md) | More channels in the classroom | Accepted |
| [0026](0026-network-position-sources.md) | Positions from the network: a Starlink dish and the browser | Accepted |
| [0027](0027-entertainment-board-games-and-films.md) | Entertainment: built-in board games and public-domain films | Accepted |

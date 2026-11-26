# Security design and threat model

## Assets

1. **Library integrity:** reference content must be what its publisher
   published, or flagged as unverified.
2. **The host computer:** Arcana runs from a drive on computers it does not
   own.
3. **User data:** notes, documents and settings under `user/`.
4. **The local network** that Arcana serves.

## Adversaries and attack paths

| Adversary | Path | Mitigations (implemented unless noted) |
|---|---|---|
| Malicious or compromised catalog/feed | Entries with traversal names, absolute paths, Windows device names, `file:`/`javascript:` URLs, credentials in URLs, other providers' IDs, huge sizes, HTML in text, false "redistribution permitted" | Strict validation (`Resource.Validate`): single portable file-name components, `http(s)` only, namespace locked to the configured provider, size caps, text cleaned and rendered as text, `permitted` downgraded unless verified. XML parsed with Go's decoder (no external entities). Response size caps on every fetch. Invalid entries dropped individually. |
| Catalog pointing at LAN devices (SSRF-style) | Download URLs resolving to 10/8, 192.168/16, loopback, link-local, CGNAT | Dialer checks the *connected* IP (defeats DNS rebinding); private ranges blocked unless the user enables them. Redirects limited to `http(s)`, max 10. |
| Tampered download / bad mirror | Altered bytes, truncated file, wrong edition | Publisher SHA-256 (from metalink or catalog) verified before publish; size checked against catalog; mismatched mirror rejected; restart from another mirror once, then fail. Unverifiable sources flagged. |
| Malicious archive content (ZIM with scripts) | Script in an article calls Arcana's API to download or delete | **Origin isolation**: content served from port+1, a different browser origin. The API requires a token only readable same-origin. kiwix-serve's own CSP sandbox applies. `/files/*` sends `CSP: sandbox` and forces download for active types. |
| Hostile web page while the user browses the internet | CSRF to `127.0.0.1:8470`; DNS rebinding to read the API | Host header allow-list (loopback names, local IPs, `arcana.local`); token header required for unsafe methods; `Sec-Fetch-Site` cross-site rejected; no CORS headers; `Access-Control-Allow-Origin: *` from kiwix-serve stripped. |
| Device on the LAN | Modify library, read host files, read settings or drive list | Mutations only from loopback. Network clients never receive the token. Settings, drive list and integrity check are local-only. No endpoint takes a file-system path from a client except setup on loopback. Files are served only if listed in a sidecar. |
| Archive extraction attacks | Zip-slip, symlinks | Arcana does not extract archives in Phase 1. Future extraction (Phase 2 adapters, updates) must use `fsutil.SafeJoin` per entry and reject links. |
| Supply chain: Arcana itself | Malicious release binary | Phase 2: Ed25519-signed release manifest verified by the running Arcana before staging an update; reproducible builds (`-trimpath`, pinned toolchain); signing keys offline. Today: SHA-256 sums published with releases. |
| Supply chain: kiwix-serve | Substituted binary | `fetch-kiwix.sh` pins SHA-256 per platform archive; refuses mismatches. |
| Supply chain: dependencies | Compromised module | 9 small modules, all BSD/MIT; `go.sum` pinning; `govulncheck` in CI; Dependabot; review required for new dependencies. |
| Local attacker with the drive | Edits sidecars or settings | Out of scope for confidentiality (no encryption in Phase 1). Sidecars are re-validated (paths must stay inside the resource directory). A full integrity check re-hashes content. |

## Principles enforced in code

- **No code execution from content.** Arcana never executes archive or
  document content. Besides `bin/<platform>/kiwix/kiwix-serve` (or one
  given with `--kiwix`), the only programs it starts are those of
  resources it was built to run (llamafile, Kolibri's Python, the apps of
  ADR-0030), from files verified against the SHA-256 in its bundled
  catalog, at paths compiled into Arcana. A catalog cannot place files in
  `bin/`: content is confined to
  `content/<validated-id>/<validated-version>/<validated-name>`.
- **No arbitrary file serving.** The content origin serves kiwix-serve
  proxied pages and sidecar-listed files only.
- **Local-only by default.** It binds 127.0.0.1 unless `--lan` or the
  setting is used. There is no UPnP, no port mapping, and no public
  exposure.
- **No telemetry.** Arcana makes no network request except catalog refresh,
  downloads the user starts, measuring a custom map area when the host asks
  for it, and the daily check for a new version of Arcana. That check is
  on by default and can be switched off in *Settings → Application
  updates*; it fetches only the signed `release.json` from the update
  address (GitHub releases by default), starting two minutes after Arcana
  starts and at most once a day, and a failed check waits for the next
  day. The acceptance test confirms that nothing else is attempted at
  startup or while browsing. The User-Agent names only "Arcana/<version>"
  and the project URL.
- **Least privilege.** Arcana never needs administrator rights. Appliance
  networking (AP, DNS, DHCP, captive portal) lives in a separate systemd
  unit with its own privileges (Phase 5).

- **Untrusted map tiles.** Map archives and their tiles are parsed with
  bounded readers (directory sizes, varints, tile lengths, MVT commands);
  malformed input fails cleanly (tested). Glyph requests are restricted to
  bundled font names and numeric ranges.
- **Position privacy.** GPS positions, waypoints and tracks are served only
  to the host unless sharing is enabled. GPS sources only read; Arcana
  never sends position data.

## Apps (ADR-0030)

- **Each app on its own origin.** Apps from the catalog are served on
  port P+3 onwards, one fixed port each, so an app's scripts cannot read
  Arcana's session token, archive pages or another app's saved data.
  Arcana's session cookie is removed from requests to app ports; the Host
  check applies to them as to the other ports.
- **Web apps reach nothing outside their origin.** Their CSP allows the
  scripts they ship (inline and evaluated, as their authors wrote them)
  but limits every load and connection to the app's own origin, and the
  origin is read-only.
- **The catalog chooses files, not behaviour.** What an app runs and how
  is compiled into Arcana (`internal/apps`); a catalog entry for an app
  Arcana does not know is ignored. Files are verified against their
  SHA-256 before they are unpacked, and unpacking refuses paths and links
  that leave the app's folder.
- **Programs.** Native apps listen on loopback only and are reached
  through Arcana's proxy, which adds a CSP limiting their pages to their
  own origin and refuses changes sent from other origins (except to the
  token-authenticated APIs of Vaultwarden and Jellyfin). Devices on the
  network may use them only after signing in to Arcana as administrators,
  or when the owner allows it per app. Arcana replaces the published
  default administrator passwords of File Browser and Calibre-Web with
  random ones.
- **Registry tokens.** For container image layers the downloader fetches
  an anonymous, pull-only token over HTTPS from the realm the registry
  names, and only for blob URLs addressed by digest.

## Remote administration (implemented in 0.4.0)

- **Off until the host sets a password.** Only the host can set, change or
  remove it. It is stored as an Argon2id hash (time 3, 64 MiB, 2 lanes,
  16-byte salt; parameters stored with the hash) in
  `user/admin-password.json`, so it travels with the drive.
- **Sessions.** Signing in creates a random 256-bit session ID in an
  `HttpOnly; SameSite=Strict` cookie (`Secure` over HTTPS) and a separate
  per-session token that the page sends in `X-Arcana-Token`. Sessions live
  in memory (2 h idle, 12 h absolute, 64 at most); a restart, a password
  change or turning the feature off signs everyone out.
- **Guessing.** Failed attempts are limited to 5 per address and 30 in
  total per 15 minutes, on top of Argon2id's cost and a fixed delay.
- **CSRF and the content origin.** Browsers send cookies to every port of
  a host, so the content origin (port + 1) receives the session cookie: it
  strips cookies before serving anything, mutations require the session
  token (unreadable from another origin), and `Sec-Fetch-Site` must be
  `same-origin`. The login endpoint requires a JSON body, which forces a
  CORS preflight for any cross-origin attempt.
- **Host-only actions.** A signed-in network administrator manages
  downloads, catalog, removals and settings, but cannot list drives, run
  transfers, apply program updates, change the password, or read the host
  user's notes, history and documents.
- **Transport.** Over plain HTTP the password crosses the network in clear
  text; the sign-in dialog says so. Optional HTTPS (below) avoids this.

## HTTPS, names and hotspot (implemented in 0.4.0)

- **HTTPS on the same ports.** When enabled, each connection's first byte
  decides: a TLS handshake is served with TLS, anything else as HTTP. The
  host keeps using `http://127.0.0.1`, which browsers already treat as a
  secure context, while phones can use `https://` (needed for browser
  geolocation and service workers). Certificates are either self-signed
  (ECDSA P-256, kept in `state/tls/`, renewed when an address is missing or
  30 days before expiry; the Server view shows the SHA-256 fingerprint to
  compare) or supplied by the user in `user/tls/`. TLS 1.2 minimum.
- **mDNS.** A built-in responder answers only for its own name
  (`arcana.local` by default) and its `_http._tcp` service, only while
  sharing on the network, and probes for a conflicting name first
  (renaming to `arcana-2.local` and so on).
- **Hotspot.** On Linux with NetworkManager, the host can start and stop a
  Wi-Fi access point. Arcana runs `nmcli` with a fixed argument list (no
  shell), validates the network name and password, and needs no
  administrator rights of its own; NetworkManager's policy decides. The
  hotspot password is stored in `user/settings.json` in clear text because
  it is shown to people joining (as a QR code to the host only).

## Residual risks of third-party content

- **Accuracy.** Archives can be wrong, outdated or biased. Arcana shows the
  publisher, edition dates and medical and regulatory warnings, but cannot
  vouch for content.
- **Scripts inside ZIMs.** They run within kiwix-serve's sandbox CSP on the
  content origin. They cannot reach the Arcana API but can, for example,
  show misleading UI inside the reader frame.
- **External links.** `--blockexternal` stops navigation to external links
  from archives, but content cannot be fully policed.
- **License misstatements.** Kiwix category mapping can misattribute a
  license. Unreviewed families are marked "License unverified".
- **Source authenticity.** Metalink hashes are only as trustworthy as TLS
  to the catalog host. Kiwix's catalog v3 digests (merged upstream) and
  signed feeds would strengthen this.

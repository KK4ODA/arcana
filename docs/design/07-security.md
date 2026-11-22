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

- **No code execution from content.** Arcana never executes downloaded
  files. The only executables it starts are `bin/<platform>/kiwix/kiwix-serve`
  from its own library, or one given explicitly with `--kiwix`. A catalog
  cannot place files in `bin/`: content is confined to
  `content/<validated-id>/<validated-version>/<validated-name>`.
- **No arbitrary file serving.** The content origin serves kiwix-serve
  proxied pages and sidecar-listed files only.
- **Local-only by default.** It binds 127.0.0.1 unless `--lan` or the
  setting is used. There is no UPnP, no port mapping, and no public
  exposure.
- **No telemetry.** Arcana makes no network request except catalog refresh
  and downloads the user starts. The acceptance test confirms that nothing
  is attempted at startup or while browsing. The User-Agent names only
  "Arcana/<version>" and the project URL.
- **Least privilege.** Arcana never needs administrator rights. Appliance
  networking (AP, DNS, DHCP, captive portal) lives in a separate systemd
  unit with its own privileges (Phase 5).

## Planned (Phase 4) remote administration

- A password stored as an Argon2id hash in `user/settings.json`.
- Login sets an `HttpOnly; SameSite=Strict` cookie scoped to the app
  origin.
- Admin endpoints accept loopback **or** an admin session plus the token.
- Rate-limited login.
- Optional local TLS.
- Remote administration stays disabled until a password is set.

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

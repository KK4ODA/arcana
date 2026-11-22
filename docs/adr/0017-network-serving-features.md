# ADR-0017: Remote administration, HTTPS on the same port, built-in mDNS

- Status: Accepted
- Date: 2026-11-22

## Context
Phase 4 makes the library easier to reach and manage from other devices on
a local network, without weakening the defaults: local-only binding,
read-only network clients, no administrator rights, no new background
services.

## Decision
1. **Remote administration is opt-in.** The host sets a password (Argon2id,
   stored on the drive). Network clients sign in to an in-memory session
   with an HttpOnly, SameSite=Strict cookie plus a per-session token header.
   Host-only actions (drives, transfers, program updates, the password,
   private notes) stay unavailable remotely.
2. **HTTPS shares the HTTP port.** A listener peeks at each connection's
   first byte and serves TLS handshakes with TLS. The host keeps
   `http://127.0.0.1` (already a secure context); phones can use `https://`.
   Certificates are self-signed (stored in the library, fingerprint shown)
   or user-supplied.
3. **A built-in mDNS responder** (about 500 lines on `golang.org/x/net`'s
   DNS message and IPv4 packages, both already dependencies) advertises
   `arcana.local` and `_http._tcp` while sharing. It shares port 5353 with
   the system responder using address reuse and probes for name conflicts.
   Registering through each OS's responder would need cgo (Bonjour), D-Bus
   (Avahi) or Windows APIs, and the portable app must work without them;
   `brutella/dnssd` would add a dependency for the same result.
4. **Hotspot control only through NetworkManager** on Linux, with fixed
   `nmcli` arguments and the user's own permissions.

## Consequences
- Ports stay as they are; enabling HTTPS breaks no existing bookmark.
- Self-signed certificates cause a browser warning on each new device;
  the fingerprint lets users check they are talking to their own server.
- Some Android versions do not resolve `.local` names; QR codes use the
  numeric address first.
- Windows and macOS users create hotspots with their system tools.

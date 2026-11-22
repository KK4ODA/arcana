# ADR-0002: A single Go binary serving a local web application

- Status: Accepted
- Date: 2026-11-22

## Context
Arcana must run from a removable drive on Windows, Linux and macOS without
installation, serve phones and tablets on a LAN, and run headless on a
Raspberry Pi. LAN and appliance clients can only use a browser, so a web UI
is required in every mode.

## Decision
Build Arcana as one statically linked Go program per platform
(`CGO_ENABLED=0`). It embeds the web UI, runs an HTTP server, and opens the
system browser in portable mode. The same binary is the LAN server and the
appliance service.

## Consequences
- One UI serves the desktop, LAN and appliance modes. Binaries are about
  12.5 MB with no runtime dependencies. All six targets cross-compile from
  one Linux runner. Startup is 281 ms and memory use 43 MB (measured).
- The UI is a browser tab rather than a native window. Closing the tab does
  not stop Arcana; the console window (or a future tray icon) does.
- Go's garbage collector and the pure-Go SQLite are slower than C
  equivalents; both measured well within targets.

## Alternatives
- Tauri: needs WebView2 or WebKitGTK on the host and a separate web-only
  path for LAN clients.
- Python with a desktop shell: large bundles, slow start from USB,
  antivirus false positives.
- Qt: heavy runtime, and a separate web UI is still needed for clients.

See docs/design/02-architecture.md for the full comparison.

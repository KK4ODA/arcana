# ADR-0030: Apps from the catalog, each on an origin of its own

- Status: Accepted (web apps in 0.14.0; native programs follow)
- Date: 2026-11-26

## Context
Project NOMAD (v1.35.2, `admin/database/seeders/service_seeder.ts`) installs
eleven applications as Docker containers besides its library: CyberChef, IT
Tools, Excalidraw, Meshtastic Web, MeshCore Web, File Browser, Homebox,
Vaultwarden, Jellyfin, Calibre-Web and Stirling PDF. The owner asked for
all of them in Arcana (2026-11-26).

Arcana is one portable Go program without Docker (ADR-0002) that runs from
a drive on Windows, macOS and Linux, on x86-64 and ARM64. It already runs
three programs beside itself: kiwix-serve (ADR-0003), llamafile (ADR-0021)
and Kolibri with a standalone Python (ADR-0024). The applications are of
two kinds. Five are prebuilt web sites with no server of their own (NOMAD
serves each with nginx). Six are programs with a server and a database.

Each of them is a web application with its own scripts, and most keep data
in the browser (Excalidraw's drawings, Meshtastic's saved radios) or in
accounts (Vaultwarden's vaults). A browser keeps that data per origin:
scheme, host and port.

## Decision
- **One catalog resource per app, `arcana.app.<id>`, format `app`.** Its
  files are official upstream artifacts pinned by SHA-256 and fetched by
  the download manager (ADR-0009). Where the publisher states a checksum
  (Homebox's `checksums.txt`, Adoptium's API, Jellyfin's repository, PyPI,
  an image layer's digest), the pin is checked against it.
  `tools/app-catalog/pins.py` downloads every file and writes the entries.
- **Files for every platform the app supports.** An app keeps its files for
  each computer it runs on, packed on the drive, as Kolibri does, so the
  drive works wherever it is taken. A file's name says what it holds and
  for which platform: `part_platform_rest`, with platform `any` or
  `GOOS-GOARCH` (`jellyfin_linux-arm64_10.11.11.tar.gz`). On each computer
  Arcana unpacks the parts it needs into the computer's cache folder
  (`<user cache>/arcana/apps/<id>-<hash>/`), marked complete only after
  every file is written; a new version unpacks beside the old one.
- **What runs is compiled into Arcana, not read from the catalog.** A spec
  in `internal/apps` says how each app unpacks, where its site or program
  is and how to start it. A catalog can offer only the apps this build
  knows, and cannot make Arcana run anything else.
- **Each app gets an origin of its own: port P+3 onwards.** The application
  origin is P, the content origin P+1 and the classroom P+2 (ADR-0013,
  ADR-0024). Each app has a fixed slot: CyberChef 8473, IT Tools 8474,
  Excalidraw 8475 and Meshtastic Web 8476 with the default port 8470. A
  fixed port keeps an app's saved data, which belongs to the origin, across
  restarts. A separate origin per app keeps an app's scripts away from
  Arcana's session, from archive pages and from every other app's data.
  Arcana opens an app's port only while the app is installed, with the
  Host check and same-port HTTPS of its other ports (ADR-0017), and drops
  its own session cookie from requests to apps.
- **Web apps are served by Arcana**, read-only, from the unpacked files. Their
  Content-Security-Policy lets their scripts run as written (some inline,
  some compiled at run time) but limits every load and connection to the
  app's own origin: an app cannot reach the internet, so it works the same
  offline and sends nothing anywhere. Apps that route in the browser get
  their index page for paths that name no file.
- **Native programs are supervised as sidecars**: started on demand, on
  loopback, reached through a proxy on the app's port, stopped with
  Arcana. Each keeps its data under `user/apps/<id>/` on the drive, which
  travels with it and which content operations never touch. A read-only
  library does not run them. The second change of this ADR adds them.
- **No build for a platform: official artifacts only.** Taking a program
  out of the publisher's official container image is acceptable when its
  license allows it and the layer's digest is pinned. Otherwise the app is
  marked unavailable on that platform, and its catalog entry says why.
- **Container image layers download like any file.** Registries serve
  layers by digest, a layer's SHA-256, to anonymous clients holding a
  token. The downloader answers the registry's `Bearer` challenge for blob
  URLs (`/v2/<name>/blobs/sha256:<hex>`) by fetching an anonymous, pull-only
  token from the realm the registry names, over HTTPS. It sends no
  credentials of its own.
- **Secure contexts.** Web Serial and Web Bluetooth (Meshtastic Web) and the
  Web Crypto API (Vaultwarden's web vault) work only in a secure context.
  `http://localhost` and `http://127.0.0.1` are secure on the host; devices
  on the network need Arcana's HTTPS. The Apps page says so when an app
  needs it and the page is not secure.
- **An Apps page** lists the installed apps with their state and an *Open*
  button that opens the app's origin in a new tab, and offers the
  administrator the apps not yet installed, with the platforms each runs
  on.

### The web apps
| App | Upstream artifact | License |
|---|---|---|
| CyberChef 10.24.0 | `CyberChef_v10.24.0.zip` from GCHQ's GitHub release | Apache-2.0 |
| IT Tools 2024.10.22 | `it-tools-2024.10.22-7ca5933.zip` from the GitHub release | GPL-3.0 |
| Excalidraw `sha-4bfc5bb` | the site layer of `excalidraw/excalidraw:sha-4bfc5bb` on Docker Hub, the image NOMAD pins (built November 2021; Excalidraw publishes no other build of the app) | MIT |
| Meshtastic Web 2.7.1 | `build.tar` from the GitHub release; every file is gzip-compressed, and Arcana decompresses them as it unpacks | GPL-3.0 |

**MeshCore Web is left out.** The MeshCore companion app is closed source:
its README says it is "free for anyone to use", and neither Liam Cottle's
builds nor aXistem's image grant a license to redistribute it. Arcana's
rule is to list only what may be redistributed (CONTRIBUTING.md).

## Consequences
- Four more ports for the web apps, ten when all apps are installed. The
  firewall's rule for Arcana covers them. A port already in use by another
  program leaves that app unopenable, and the Apps page says which port.
- If Arcana falls back to other ports because 8470 is busy, every app
  moves with it, and the browser treats it as a new origin: drawings and
  settings saved in the browser under the old port are not shown.
- Excalidraw's live collaboration and its link sharing need Excalidraw's
  servers; the CSP blocks them, and drawings are saved as files instead.
  On `localhost` Excalidraw's service worker fails to load (Workbox loads
  debug builds there), which only disables its offline cache; on
  `127.0.0.1` and network addresses it registers.
- Meshtastic Web asks for `en-US` translations and a font that its build
  does not contain (404s that nginx would give too); it falls back to its
  English text and a system font.
- Tested on Linux x86-64 in Chromium through Arcana (2026-11-26): every web
  app opened from the Apps page; CyberChef converted text to Base64,
  IT Tools generated a UUID on a page reached by its own route, Excalidraw
  saved a drawn rectangle in the browser, and Meshtastic Web loaded in a
  secure context with Web Serial available. No request left the computer.
  The sites are the same files on every platform; Windows, macOS and ARM64
  were not tried. No radio was connected to Meshtastic Web, and headless
  Chromium on Linux has no Web Bluetooth.

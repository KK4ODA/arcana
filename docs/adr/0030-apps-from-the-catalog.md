# ADR-0030: Apps from the catalog, each on an origin of its own

- Status: Accepted
- Date: 2026-11-26

## Context
Project NOMAD (v1.35.2, `admin/database/seeders/service_seeder.ts`) installs
twelve applications as Docker containers besides its library: CyberChef,
IT Tools, Excalidraw, Meshtastic Web, MeshCore Web, File Browser, Homebox,
Vaultwarden, Jellyfin, Calibre-Web, Stirling PDF and Flatnotes. The owner
asked for all of them in Arcana (2026-11-26), MeshCore Web included once
it turned out to be closed source.

Arcana is one portable Go program without Docker (ADR-0002) that runs from
a drive on Windows, macOS and Linux, on x86-64 and ARM64. It already runs
three programs beside itself: kiwix-serve (ADR-0003), llamafile (ADR-0021)
and Kolibri with a standalone Python (ADR-0024). The applications are of
two kinds. Five are prebuilt web sites with no server of their own (NOMAD
serves each with nginx). Seven are programs with a server and their own
data.

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
  ADR-0024). Each app has a fixed slot; with the default port 8470:
  CyberChef 8473, IT Tools 8474, Excalidraw 8475, Meshtastic Web 8476,
  File Browser 8477, Homebox 8478, Vaultwarden 8479, Jellyfin 8480,
  Calibre-Web 8481, Stirling PDF 8482, MeshCore Web 8483 and Flatnotes
  8484. A
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
  their index page for paths that name no file. Where an app's own
  start-up code fetches from the internet what its build already carries,
  Arcana changes that code as it serves it (exact text replacements
  compiled into the app's spec); the files on the drive stay as published
  and verified.
- **Native programs are supervised as sidecars.** The first request to an
  app's port starts its program on a loopback port Arcana chooses, shows
  a page that reloads until it answers, and then proxies every request,
  web sockets and streams included. A program that has had no request for
  two hours is stopped; the next request starts it again. Programs stop
  with Arcana; on Linux they also die with it (a parent-death signal), and
  on Windows a job object ends them when Arcana's process ends. macOS has
  no such mechanism. A program that fails to start is not retried for 30
  seconds, and the Apps page shows the line of its output that says why.
- **Data on the drive, programs on the computer.** Each program keeps its
  data under `user/apps/<id>/`, which travels with the drive and which
  content operations never touch; `user/files/` is shared (File Browser
  manages it and Jellyfin can read media from it). Caches (Jellyfin's
  transcodes) stay on the computer. A read-only library does not run
  programs. What Arcana set up (passwords it chose, which apps the network
  may use) is in `state/apps.json`, as Kolibri's is in `state/kolibri.json`.
- **Who may use a program** (ADR-0017). These apps keep accounts, files and
  passwords, so Arcana's read-only access for network devices does not
  extend to them. The computer running Arcana may use every app; a device
  on the network may use one after its owner signs in to Arcana as an
  administrator, or when the owner allows devices on the network for that
  app on the Apps page. Web apps are open to everyone who can reach
  Arcana: they keep nothing on the computer.
- **Programs' pages are held to their origin too.** Arcana adds its own
  Content-Security-Policy to every response of a program, beside the
  program's own; a browser enforces both. Without it, Jellyfin's web client
  fetches Google's Chromecast script and Stirling PDF's pages load PostHog
  analytics, both from the internet. Changes sent from another origin are
  refused, except to Vaultwarden and Jellyfin, whose APIs authenticate
  each request with a token in a header and whose own clients (Bitwarden's
  browser extensions, Jellyfin's apps) call them from elsewhere.
- **Passwords Arcana sets.** File Browser, Calibre-Web and Flatnotes
  (whose own default is no sign-in at all) start with a
  known administrator (File Browser prints a random password to a log no
  one reads; Calibre-Web's is the published `admin123`; Flatnotes asks
  for a password in its configuration). Arcana gives
  each a random password at the first start (17 characters with a hyphen,
  about 95 bits) and shows it to its administrators on the Apps page.
  Homebox, Vaultwarden and Jellyfin ask for the first account themselves;
  Stirling PDF runs without accounts.
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
- **Secure contexts.** Web Serial and Web Bluetooth (Meshtastic Web) work
  only in a secure context. `http://localhost` and `http://127.0.0.1` are
  secure on the host; devices on the network need Arcana's HTTPS. The Apps
  page says so when an app needs it and the page is not secure.
  Vaultwarden's web vault goes further and accepts only HTTPS addresses
  (below).
- **An Apps page** lists the installed apps with their state and an *Open*
  button that opens the app's origin in a new tab, and offers the
  administrator the apps not yet installed, with the platforms each runs
  on.

### The web apps
| App | Upstream artifact | License |
|---|---|---|
| CyberChef 10.24.0 | `CyberChef_v10.24.0.zip` from GCHQ's GitHub release | Apache-2.0 |
| IT Tools 2024.10.22 | `it-tools-2024.10.22-7ca5933.zip` from the GitHub release | GPL-3.0 |
| Excalidraw `sha-4bfc5bb` | the site layer of `excalidraw/excalidraw:sha-4bfc5bb` on Docker Hub, the image NOMAD pins (built November 2021; Excalidraw publishes no other build of the app); Docker Hub limits anonymous downloads by address, so `mirror.gcr.io`, Google's Docker Hub mirror serving the same digest, is a second source | MIT |
| Meshtastic Web 2.7.1 | `build.tar` from the GitHub release; every file is gzip-compressed, and Arcana decompresses them as it unpacks | GPL-3.0 |
| MeshCore Web 1.45.0 | the author's own web build, `MeshCore-v1.45.0+71-dc3a209-web.zip` from files.liamcottle.net, with Roboto and Noto Sans Symbols from Google Fonts | closed source, free to use; fonts SIL OFL 1.1 |

### The native apps
| App | Upstream artifacts | Platforms | License |
|---|---|---|---|
| File Browser 2.63.23 | GitHub release archives | all five | Apache-2.0 |
| Homebox 0.26.2 | GitHub release archives, checked against its `checksums.txt` | all five | AGPL-3.0 |
| Vaultwarden 1.36.0 | the server and web-vault layers of the official `vaultwarden/server:1.36.0-alpine` image (ghcr.io); its server is a static Linux program | Linux x86-64 and ARM64 | AGPL-3.0; web vault GPL-3.0 |
| Jellyfin 10.11.11 | portable builds from repo.jellyfin.org, with jellyfin-ffmpeg 7.1.4-3 (the Windows build carries its own) | all five | GPL-2.0; ffmpeg GPL-3.0 |
| Calibre-Web 0.6.26 | the wheel from PyPI, 94 files of its dependencies from PyPI for the five platforms, CPython 3.11.17 from python-build-standalone (as ADR-0024), and Project NOMAD's empty Calibre library | all five | GPL-3.0-or-later |
| Stirling PDF 2.13.1 | `Stirling-PDF.jar` from the GitHub release, with Eclipse Temurin 25 JREs from Adoptium | all five | MIT; Temurin GPL-2.0 with the Classpath Exception |
| Flatnotes 5.5.4 | the server and web client layers of the official `dullage/flatnotes:v5.5.4` image (the same for amd64 and arm64; Flatnotes publishes no other build), its Python dependencies at its `Pipfile.lock` versions from PyPI (64 files), and the CPython runtime Calibre-Web uses | all five | MIT |

Per-app setup:
- **File Browser** serves `user/files/` with its database in its data
  folder, and gets Arcana's password as a bcrypt hash at its first start.
- **Homebox** runs in its data folder. It refuses to start without a
  secret of at least 32 bytes for its API keys; Arcana makes one at the
  first start and keeps it with the data, since changing it invalidates
  every key issued. Its release check is off.
- **Vaultwarden** gets `DATA_FOLDER`, the web vault folder and loopback
  through its environment, with icon downloads off. Its web vault accepts
  only HTTPS addresses, even on `localhost` ("Insecure URL not allowed"),
  so it needs Arcana's HTTPS mode; the Apps page says so, and opens its
  `https://` address once HTTPS is on. Vaultwarden publishes no build for
  Windows or macOS, and no Windows or macOS program can be taken from its
  images, so it is unavailable there.
- **Jellyfin** keeps configuration, database and logs on the drive and its
  cache on the computer. Its port lives in `network.xml`; before each start
  Arcana sets its own entries there (port, loopback, discovery and UPnP
  off, Arcana trusted as a proxy, published address taken from each
  request) and keeps the rest.
- **Calibre-Web** is installed on each computer at its first start by the
  standalone Python's own pip, offline, from the wheels on the drive
  (`--no-index --find-links`): pip picks the files for the computer. Two
  dependencies are published only as source (Flask-Principal and iso-639);
  pip builds them with the setuptools that ships with the Python runtime.
  A bootstrap sets Calibre-Web's port and library in its settings database
  on every start, because Calibre-Web reads both from there and has no
  option for the library. Arcana seeds the library with Project NOMAD's
  empty Calibre database (Calibre-Web cannot create one) and sets the
  administrator password with Calibre-Web's own `-s` option.
- **Flatnotes** is installed like Calibre-Web, by pip from the drive at
  its first start, and runs under uvicorn in its unpacked folder (it
  writes its base address into its own `index.html` at every start). Its
  notes are Markdown files in its data folder. Arcana turns on its
  password sign-in with one user, admin, with Arcana's password and a
  session secret kept with the data; NOMAD runs it without sign-in.
  Docker Hub limits anonymous downloads by address, so its layers also
  list Google's Docker Hub mirror, `mirror.gcr.io`, which serves the same
  bytes under the same digests.
- **Stirling PDF** runs with login, analytics and update checks off. Its
  repository holds proprietary parts (`app/proprietary`, under the
  Stirling PDF User License); the release's `Stirling-PDF.jar` is built
  with `DISABLE_ADDITIONAL_FEATURES` and contains none of them, which was
  checked by listing the jar (no `proprietary` module), and the build that
  has them (`Stirling-PDF-with-login.jar`) is not used. It needs Java 25.

**AGPL-3.0 (Homebox, Vaultwarden).** Arcana downloads and runs the
publishers' unmodified programs. Section 13 obliges whoever *modifies* the
program to offer its source to network users; Arcana modifies nothing,
and the catalog entries link the publishers' repositories. Arcana is not a
derivative work: it starts them as separate programs and speaks HTTP to
them.

**MeshCore Web is downloaded from its author, not shipped.** The MeshCore
companion app is closed source; its author's statement is that it is
"free for anyone to use", and no license grants a right to redistribute
it. Arcana's rule is to list content only after its redistribution terms
are confirmed (CONTRIBUTING.md); the owner asked for MeshCore Web anyway
(2026-11-26). It is offered the way the catalog already offers Kiwix
archives whose terms forbid passing them on: the entry is marked
`restricted`, with a license warning, and each library downloads the
build from the author's site, files.liamcottle.net, the source NOMAD's
image is made from. Arcana never ships it, and the warning asks the owner
to remove it before copying the library for someone else; `arcana
export` and copying between libraries do not yet enforce that.

The build loads Flutter's rendering engine from www.gstatic.com and its
default font, Roboto, from fonts.gstatic.com, although it carries the
engine itself: offline it stops at its splash screen, and under the CSP
it shows no text. Arcana serves its `index.html` and
`flutter_bootstrap.js` with `useLocalCanvasKit` set and
`window.flutterConfiguration.fontFallbackBaseUrl` pointing at the app's
own origin, where it serves Roboto and Noto Sans Symbols (the two fonts
the app asked for in testing), downloaded and pinned with the app. Text in
scripts those fonts do not cover (Cyrillic, CJK, emoji) has no font
offline. The map tab draws OpenStreetMap tiles from the internet and is
blank offline.

## Consequences
- Up to ten more ports, one per installed app. The
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
- The drive holds every platform's files: 729 MB for Jellyfin, 567 MB for
  Stirling PDF and 240 MB for Calibre-Web, against 160 to 270 MB for one
  platform's. Calibre-Web brings its own Python (140 MB), the same files as
  Kolibri's but a second copy, since resources do not share files.
- The first start on each computer unpacks the app (Jellyfin: about 10
  seconds on a local disk) and, for Calibre-Web, installs its packages
  (about 4 seconds). On Linux x86-64 a first start took from about a
  second (File Browser, Vaultwarden, Jellyfin) to 3 seconds (Homebox) and
  10 to 14 seconds (Calibre-Web with its package installation, Stirling
  PDF).
- Offline, some features cannot work and say so in the app: Vaultwarden's
  "Check known data breaches" (on by default when creating an account;
  left on, sign-up does not finish, so the Apps page says to untick it),
  metadata and artwork lookups in Jellyfin and Calibre-Web, and Stirling
  PDF's conversions that need LibreOffice, Tesseract or other programs its
  container image adds (merging, splitting, rotating, compressing,
  signing and redacting work with Java alone).
- On macOS a program left running by a crashed Arcana runs on until it is
  stopped or the computer restarts; the next Arcana starts another on a
  new port, and both use the same data.
- Tested on Linux x86-64 in Chromium through Arcana (2026-11-26): every
  native app started on demand from its port and was used: File Browser
  signed in with Arcana's password and took an upload that appeared in
  `user/files`; Homebox registered a user and signed in; Vaultwarden
  created an account and signed in over Arcana's HTTPS; Jellyfin finished
  its setup, signed in and found the bundled ffmpeg 7.1.4; Calibre-Web
  refused `admin123`, accepted Arcana's password and opened the seeded
  library; Stirling PDF merged two PDFs into a two-page file; Flatnotes
  signed in with Arcana's password, saved a note as a Markdown file on the
  drive and found it by search. No request
  left the computer. Windows, macOS and Linux ARM64 builds were not run;
  their files are pinned and verified like the others.
- Tested on Linux x86-64 in Chromium through Arcana (2026-11-26): every web
  app opened from the Apps page; MeshCore Web drew its contacts page with
  text in a secure context with Web Serial present; CyberChef converted text to Base64,
  IT Tools generated a UUID on a page reached by its own route, Excalidraw
  saved a drawn rectangle in the browser, and Meshtastic Web loaded in a
  secure context with Web Serial available. No request left the computer.
  The sites are the same files on every platform; Windows, macOS and ARM64
  were not tried. No radio was connected to Meshtastic Web, and headless
  Chromium on Linux has no Web Bluetooth.

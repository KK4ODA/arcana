# Changelog

All notable changes are recorded here. Versions follow semantic versioning;
the library format version is tracked separately (docs/design/09-storage-and-data.md).

## Unreleased

### Added
- **Apps** (ADR-0030). A new *Apps* page installs web applications from
  the catalog and opens each in a tab of its own: CyberChef 10.24.0 (GCHQ's
  data toolkit), IT Tools 2024.10.22, Excalidraw (the whiteboard, in the
  build Project NOMAD ships) and Meshtastic Web 2.7.1, which configures a
  Meshtastic radio and sends messages through it over USB or Bluetooth.
  Arcana serves each app itself on a port of its own (8473 and up by
  default), so an app cannot read Arcana's session or another app's saved
  drawings and settings, and no app can contact anything outside its own
  address. Meshtastic Web needs Chrome or Edge, on the computer running
  Arcana or over its HTTPS address.
- **Six programs as apps** (ADR-0030): File Browser 2.63.23, Homebox
  0.26.2 (an inventory of what you own and store), Vaultwarden 1.36.0 (a
  Bitwarden-compatible password manager; Linux only, and only over
  Arcana's HTTPS), Jellyfin 10.11.11 (a media server), Calibre-Web 0.6.26
  (an e-book library) and Stirling PDF 2.13.1. Arcana keeps each program's
  files for Windows, macOS and Linux on the drive, starts it when it is
  first opened, on this computer only, and stops it after two idle hours
  and when Arcana stops. Their data stays on the drive under `user/apps/`;
  File Browser manages `user/files/`. Arcana sets random administrator
  passwords for File Browser and Calibre-Web and shows them on the Apps
  page. Devices on the network may use the programs after signing in to
  Arcana as administrators, or when the owner allows it for an app.
- **Container image layers as downloads.** The downloader fetches the
  anonymous token container registries ask for, so a layer of an official
  image, pinned by its digest, downloads like any other file. Excalidraw's
  site and Vaultwarden come this way.
- **MeshCore Web 1.45.0**, the MeshCore radio app for the browser. It is
  closed source and free to use, without a right to pass it on, so Arcana
  downloads it from its author's site for each library and marks it
  restricted; remove it before copying the library for someone else.
  Arcana serves it with its own rendering engine and two fonts from the
  drive, which it otherwise loads from Google's servers.

## 0.13.0 — 2026-11-26

Arcana now has an Entertainment page. Seven board games are built in and
need no download: chess, checkers, backgammon, reversi, four in a row,
mancala and Go, for two players on one device or against the computer.
Thirteen classic feature films that are in the public domain in the United
States can be downloaded from the Internet Archive and watched in Arcana,
on the computer running it or on any phone or tablet on the network. The
games and the player have been tried in Chromium on Linux, at desktop and
phone width, not yet in Safari or on real phones. Arcana 0.6.0 to 0.12.0
offer this version by themselves within a day of being online: choose
*Update now* in the banner at the top of the page, or *Settings →
Application updates → Check now* to look straight away.

### Added
- **Board games** (ADR-0027). *Entertainment* lists chess, checkers
  (English draughts), backgammon (without the doubling cube), reversi, four
  in a row, mancala (Kalah) and Go (9×9, 13×13 or 19×19, scored by area
  with 7.5 komi). Each can be played by two people taking turns on one
  device, and all but Go against the computer at three levels. The squares
  a chosen piece can reach are marked, *How to play* explains the rules,
  *Undo* takes back a move, and a game in progress survives closing the
  page. Games run in the browser showing them, so network visitors play
  them too, and nothing is stored on the drive.
- **Films** (ADR-0027). Thirteen feature films from 1939 to 1968, among
  them His Girl Friday, The Little Princess, Gulliver's Travels, Jungle
  Book, Meet John Doe, The Stranger and Night of the Living Dead (7.4 GB
  together). Each film's licence record says why it is in the US public
  domain and that the status differs elsewhere; horror, violence, Nazi
  camp footage and stereotyped portrayals carry content notes. Downloads
  are checked against the SHA-256 recorded when each film was reviewed.
  The administrator can download them from the Entertainment page or from
  *Discover → Films, games and entertainment*.
- **Film player.** Films play inside Arcana with their source, licence and
  download check shown below, and each device is offered to continue where
  it stopped.
- **Films and Games** collection and a *Films and games* topic in Getting
  started: the films by suitability for families, and the Board & Card
  Games and Chess question-and-answer archives.

### Changed
- Kiwix archives about board games, chess, puzzles, role-playing games,
  films, television, comics and sports, and Blender Studio's open films,
  are now in the new category *Films, games and entertainment* instead of
  *Encyclopedias and reference*.
- WebM files are served as video rather than as downloads.

## 0.12.0 — 2026-11-26

The map shows where you are again, as an arrow pointing the way you are
heading, and records a track at the press of a button. Since 0.8.0 the
Maps view often left out your position and the directions route; that is
fixed. Recording has been tried with the simulator, not yet with a GPS
receiver on the move. Arcana 0.6.0 to 0.11.0 offer this version by
themselves within a day of being online: choose *Update now* in the
banner at the top of the page, or *Settings → Application updates → Check
now* to look straight away.

### Added
- **Position symbol.** Your position is an arrow pointing along your
  course while you move, and a dot when you stand still or the source
  gives no course. It is blue when current, amber when stale and grey
  when lost.
- **Record a track from the map.** A Record button on the map and
  *Record track* under Position start and stop recording; Settings →
  Track recording is the same switch, so recording resumes after a
  restart. The track being recorded is drawn on the map as it grows,
  with its length so far, and stays on the map when you stop.
- **Show and follow my position** button on the map, under the zoom
  buttons. It zooms in to street level and follows the position; pressed
  again, it stops following.

### Changed
- Turning track recording on or off no longer restarts the position
  source, so the fix is kept and a receiver's port is not reopened.
- Recording stopped and started again within the same second starts a
  new file instead of overwriting the track just saved.

### Fixed
- **The map did not show your position or the directions route.** Since
  0.8.0 the Maps view could finish loading the map before its own
  variables existed, and the error stopped it before it added the
  position, the route and its start and end markers. In testing this
  happened on every load of the Maps view; changing the theme brought
  them back. The UI check in the acceptance test now reports such errors.

## 0.11.0 — 2026-11-25

Arcana can now find its position without a GPS receiver: from a Starlink
dish on the local network, or from the location services of the phone or
computer showing it. So directions from where you are can be worked out,
saved and followed even with no GPS, or with one that is jammed or wrong.
A second source can be shown beside the first, with a warning when the two
disagree. Both network sources stay off until chosen in Settings. The
Starlink source has been tested against a stand-in for the dish, not a
real one, and some Starlink plans no longer share the dish's location at
all. Arcana 0.6.0 to 0.10.2 offer this version by themselves within a day
of being online: choose *Update now* in the banner at the top of the page,
or *Settings → Application updates → Check now* to look straight away.

### Added
- **Positions without a GPS receiver** (ADR-0026). *Settings → Maps and
  position* offers two new position sources, both off until chosen:
  - **Starlink dish**: Arcana asks the dish on the local network for its
    location every 2 seconds. No internet is needed, but the owner must
    first turn on "Allow access on local network" for Starlink location
    in the Starlink app, and some service plans no longer share the
    location at all (Arcana says so). The map shows whether the dish's
    position comes from its GPS receiver or from Starlink satellites.
  - **This browser's location services**: the phone, tablet or computer
    showing Arcana sends the position its system knows (its own GPS, or
    Wi-Fi and mobile networks). Browsers allow this only over HTTPS or on
    the computer running Arcana.
  Both go through the same path as a receiver, so the map, tracks,
  waypoints and turn-by-turn guidance work as before. The position panel
  shows where a position came from and how accurate it is ("within about
  25 m"); receivers that send GST sentences now show it too.
- **Compare with a second source.** A second position source can be shown
  beside the first (a ring on the map). When the two disagree by more than
  their accuracy allows, for example because the GPS is jammed or spoofed,
  the map says so and offers to use the other one instead.
- **Saved routes.** *Save route* in Directions keeps a route in the user
  folder; *Saved routes* opens it later to look at or follow, even without
  the road network, and exports it as GPX.
- **Directions from here** work with any position source: the button under
  Position, the ◎ button, and *Directions to here* on the map, which now
  starts from your position when no start is chosen.

### Changed
- Guidance does not count a coarse position as leaving the route while it
  is within its own accuracy (up to 300 m).
- An empty speed field in NMEA is shown as unknown instead of 0.

## 0.10.2 — 2026-11-25

The classroom works with Kolibri itself: it starts instead of staying on
*Starting*, courses download into it, and students can watch and practise.
Tested until now against a stand-in for Kolibri, it is now checked with
Kolibri 0.19.5 on Linux (not yet on Windows or macOS). While Kolibri
starts, the Classroom page shows how long it has been starting and what it
is doing. A classroom set up with 0.9.0 to 0.10.1 starts once Arcana is
updated, with the same administrator password. Arcana 0.6.0 to 0.10.1
offer this version by themselves within a day of being online: choose
*Update now* in the banner at the top of the page, or *Settings →
Application updates → Check now* to look straight away.

### Fixed
- **The classroom starts.** With Kolibri itself, rather than the stand-in
  it had been tested with, the classroom never left *Starting*: Kolibri
  answered every request with an error, because the zip port Arcana gives
  it ended up in its Content-Security-Policy as a number its CSP library
  could not handle. Arcana now runs Kolibri with settings that keep it as
  text. A classroom set up with 0.9.0 to 0.10.1 starts once Arcana is
  updated, with the same administrator password.
- **Downloading courses into the classroom.** Two more faults showed with
  Kolibri itself: the list of courses on the drive was asked for at an
  address Kolibri does not have, and every download was refused because
  Arcana sent Kolibri's security token under the wrong cookie name (after
  which it could not sign in again until restarted). Both are fixed; a
  course chosen on the Classroom page now downloads, and students can watch
  its videos and do its exercises. Checked with Kolibri 0.19.5 on Linux.

### Added
- **Progress while the classroom starts.** The Classroom page shows how
  long Kolibri has been starting and its latest message, so a long first
  start (a slow drive, a Raspberry Pi) visibly makes progress. If Kolibri
  runs but fails on every request, the page says so with Kolibri's error
  after 30 seconds instead of waiting 15 minutes.

## 0.10.1 — 2026-11-24

Nothing in the program changes: this release makes a test of map
downloads reliable on Windows, so that it no longer holds up releases at
random. Arcana 0.6.0 to 0.10.0 offer this version as an update like any
other; from 0.10.0 it changes only the version number.

### Fixed
- **A map download test on Windows** (development). The test that map
  regions are fetched several ranges at a time timed the whole extraction,
  including the disk syncs at each checkpoint, which can take over 100 ms
  each on Windows CI disks. It now times the fetching alone, and still
  fails if fetching stops being parallel.

## 0.10.0 — 2026-11-24

The classroom now offers 60 Kolibri channels instead of two, for high
school, AP, college and adults: Khan Academy for more curricula and
languages and its test preparation, and courses from CK-12, OpenStax, PhET,
MIT Blossoms and other publishers, each with who it is for, what it holds,
its size and its licence. A whole channel can be chosen at once. The
Settings page also fits a phone's screen. The new channels are tested, like
the first two, against a stand-in for Kolibri rather than Kolibri itself.
Arcana 0.6.0 to 0.9.0 offer this version by themselves within a day of
being online: choose *Update now* in the banner at the top of the page, or
*Settings → Application updates → Check now* to look straight away.

### Added
- **Sixty channels in the classroom.** The *Classroom* page now offers,
  besides Khan Academy in English (US curriculum) and Spanish: Khan
  Academy's English channels for the Philippine and Indian (CBSE)
  curricula, its archive of test preparation (SAT before 2024, MCAT,
  NCLEX-RN, LSAT, GMAT, Praxis Core), Khan Academy in 25 other languages
  (folded at the end), and 30 channels from other publishers for high
  school, college and adults in English and Spanish, among them CK-12,
  OpenStax, MIT Blossoms, PhET, EngageNY, BCcampus, Osmosis and Career
  Girls. Each says who it is for, what it holds, its size and its licence;
  many are for non-commercial use only. For AP courses missing from the
  English Khan Academy channel, its Spanish channel has AP Physics 1 and 2
  and AP Chemistry, and OpenStax has The AP Physics Collection. See
  docs/kolibri.md and ADR-0025.
- **Everything in a channel.** The topic picker can take a whole channel at
  once, including resources kept outside its topics.

### Fixed
- **Settings on a phone.** The page no longer scrolls sideways on a narrow
  screen: a list with long choices (*Where the model runs*) made the whole
  page 520 pixels wide. The offline acceptance test now loads every page at
  phone width and fails if one is wider than the screen.

## 0.9.0 — 2026-11-24

Arcana can now run a classroom: Kolibri with Khan Academy in English or
Spanish, chosen by grade and subject, with classes, lessons, quizzes and
progress for each student. Set it up on the new *Classroom* page. It is
new and so far tested against a stand-in for Kolibri rather than Kolibri
itself, so tell us what goes wrong. *Ask* can also search only the sources
you choose. Arcana now uses three neighbouring ports (8470 to 8472) instead
of two. Arcana 0.6.0 to 0.8.1 offer this version by themselves within a
day of being online: choose *Update now* in the banner at the top of the
page, or *Settings → Application updates → Check now* to look straight
away.

### Added
- **Ask within chosen sources.** *Search in*, under the question in the
  *Ask* view, limits a question to the archives and documents you tick
  (and on the computer running Arcana, your own documents), instead of the
  whole library. The answer says what it searched, and when the chosen
  sources do not cover the question, *Ask the whole library* asks again.
  The *Ask* button beside library search keeps its *Search in* choice, so
  a question asked while searching one archive is answered from it. For
  programs: `in=ID,ID` on the integration API's `GET /ask`; on the command
  line, `arcana ask --in ID,ID`.
- **The classroom: Khan Academy with Kolibri, built in.** A new
  *Classroom* page sets up Learning Equality's Kolibri on the library, so
  students watch, read and practise Khan Academy, and teachers make
  classes, lessons and quizzes and follow each student's progress. Arcana
  downloads Kolibri with Python for Windows, macOS and Linux (245 MB,
  checksum-verified), unpacks it on each computer, creates the school and
  an administrator (students sign in by username alone, teachers with a
  password), and runs it on its own port, 8472 by default. Choose Khan
  Academy grades and subjects, in English (US curriculum) or Spanish, with
  the size of each, and Kolibri downloads them. Its courses appear in
  library search and on the Library page. See docs/kolibri.md and
  ADR-0024. Tested against a stand-in for Kolibri, not yet with Kolibri
  itself on each system.

### Changed
- Arcana now listens on three neighbouring ports (8470, 8471 and 8472 by
  default; the next free three if those are taken): the third is the
  classroom's.

## 0.8.1 — 2026-11-24

*Get a road network* and *Browse map regions* open their lists again: on
0.8.0 they opened Discover filtered to a category that does not exist, so
the road networks needed for directions never appeared. The Server view
also explains why another device on the network might not connect.
Arcana 0.6.0 to 0.8.0 offer this version by themselves within a day of
being online: choose *Update now* in the banner at the top of the page, or
*Settings → Application updates → Check now* to look straight away.

### Changed
- **Help when another device cannot connect.** The Server view (*Another
  device cannot connect?*) and the sharing guide (docs/portable-use.md)
  now cover the causes that look like a network fault: Windows treating
  the Wi-Fi as a public network, where Arcana is usually allowed only on
  private ones; a Mac's browser without macOS's Local Network permission
  (Chrome and Firefox time out, Safari works); Android on mobile data; a
  VPN; and networks that keep devices apart.
- *Settings → Integrations* no longer says that RadioPost and Aether HF
  use the integration API already; they will in coming versions.
- Discover lists its categories alphabetically.

### Fixed
- **"Get a road network" and "Browse map regions" opened an empty list.**
  The links from the Directions panel and the Maps view asked Discover
  for a category that does not exist, so nothing matched, however many
  road networks and map regions the catalog held. They now open *Maps and
  navigation*, and a test checks every link into Discover against the
  catalog's categories.

## 0.8.0 — 2026-11-23

Ask questions of your library and get answers that cite their sources, a
medicines reference built from the FDA's drug labels, offline translation of
archive pages, a guided Getting started, automatic content updates on a
schedule you choose, a Meshtastic mesh on the map, and optional Kolibri
courses in search. All of it is off or optional until you choose it. This
release also stops library search from showing your own documents to other
devices on the network. Arcana 0.6.0 to 0.7.0 offer this version by
themselves within a day of being online: choose *Update now* in the banner at
the top of the page. Rolling back to 0.7.0 is safe: what 0.8.0 installed stays
on the drive untouched, and works again after updating.

### Added
- **Ask your library.** A new *Ask* view (and an *Ask* button beside the
  library search) answers questions in plain words from your installed
  archives and documents. Arcana searches the library, picks the passages
  that cover the question, and a language model writes a short answer that
  cites them; each passage is listed with its resource, edition, date,
  license and warnings, and links to the page it came from. When nothing in
  the library covers the question closely enough, Arcana says so and no
  model runs. Answers appear as they are written; an answer that cites
  nothing is flagged, and medical sources bring the medical notice with
  them. Off until you turn it on in *Settings → Assistant*, where you choose
  where the model runs:
  - **On this computer:** download a model from *Discover → Question
    answering* (Qwen2.5 0.5B, 0.5 GB, for small computers; Granite 3.3 2B,
    1.5 GB, recommended; Qwen3 4B, 2.5 GB, for 8 GB of memory or more). The
    llamafile engine (370 MB) is queued with the first model. It runs only
    while answering, only on this computer, and stops when idle.
  - **On a model server:** Ollama, LM Studio or another OpenAI-compatible
    server, on this computer or another (*Test connection* lists its
    models).
  Devices on the network can ask only if you allow it; they never see your
  own documents. Optional instructions ("Use metric units") and a
  strictness setting tune the answers. See ADR-0021.
- **Medicines.** An FDA drug label reference (*Discover → Medicine and
  public health → FDA drug labels*, after refreshing the catalog): the
  labels of about 265,000 US medicines from openFDA, public domain. A new
  *Medicines* view searches by brand, generic name or ingredient, grouping
  products that share active ingredients; *By situation* lists
  non-prescription medicines whose labels cover one or several of 34
  everyday problems (medicines covering all of them first); a label page
  shows uses, directions, warnings, interactions, overdose and side
  effects; *Compare warnings* puts two labels' warnings side by side (it
  does not check interactions, and says so). The view asks you to
  acknowledge that it is label text, not medical advice, once per browser.
  Arcana downloads the 14 parts openFDA publishes (1.8 GB, resumable),
  checks each zip entry's CRC, keeps the parts a reader needs, stores
  identical labels once and builds an indexed reference of about 1 GB on
  your computer. New FDA exports appear in Updates. See ADR-0022.
- **Translate archive pages offline.** Download a language pack
  (*Discover → Encyclopedias and reference → Translation: …*, 55 languages,
  about 50 MB each; the 1.2 MB engine comes with the first one), then
  choose *Translate to …* in the reader. Pages are translated on the device
  reading them by Mozilla's Bergamot engine, the one Firefox uses, so it
  works on phones and on every computer Arcana runs on, without internet.
  Links and emphasis are kept, letters and codes are left alone, the choice
  follows you from page to page, and *Original language* restores the
  text. Between two languages other than English, Arcana goes through
  English when both packs are installed. Files are checked against
  Mozilla's published hashes before and after decompression. See ADR-0023.
- **Meshtastic on the map.** Connect a Meshtastic radio (*Settings → Maps
  and position → Meshtastic radio*: a radio on Wi-Fi or Ethernet, or on
  USB) and the map shows every node it hears that reports a position, with
  its name, when it was last heard, hops, signal and altitude; nodes
  without a position and the text messages on the radio's channels are
  listed beside the map. Arcana only listens: it asks the radio for its
  node list and keeps the connection alive, and never transmits on the
  mesh. Network clients see the mesh only if you allow it.
- **Kolibri courses in search.** If you run a Kolibri server (Khan Academy
  and other courses, with progress for each learner), enter its address in
  *Settings → Kolibri (courses)*: its courses appear in library search
  after the library's own results, and the Library page links to it.
  Kolibri stays a separate program; see docs/kolibri.md.
- **U.S. Army field manuals.** Three public-domain manuals cleared for
  public release join the catalog as searchable PDFs: *FM 4-25.11 First
  Aid*, *ATP 3-50.21 Survival* (2018) and *FM 3-25.26 Map Reading and Land
  Navigation*. They are offered in the Family Emergency Preparedness and
  Remote Expedition collections, with notes on what has changed since they
  were written. (The 2013 TC 3-25.26 was left out: its distribution is
  restricted to government agencies.)
- **Links on the Library page.** Add shortcuts to other services on your
  network (a Kolibri server, a radio's web page, a file share) in
  *Settings → Links on the Library page*; everyone sees them on the Library
  page.
- **OpenAPI description of the integration API** at
  `/api/integration/v1/openapi.json` (OpenAPI 3.1), for client generators
  and API tools. A test keeps it in step with the routes.
- **Getting started.** A new library opens a short guided setup (and the
  Library view offers it until you finish or skip it): choose topics (the
  nine collections) and tools (maps of your area, directions, the medicines
  reference, question answering, translation), your language and region,
  and how much of the drive to use (Essential, about 8 GB; Standard, about
  40 GB; Comprehensive, everything above the reserve). Arcana's recommender
  fits editions to that budget across all chosen topics, adds what models
  need (the engine), shows the plan on the storage bar with each item's
  reason, and queues it in one click.
- **Automatic content updates** (Updates → Automatic updates; off by
  default). Choose the hours they may run, how often the catalogs are
  checked, whether to take every update or only medical and regulatory
  references, and a bandwidth cap. Inside the window Arcana refreshes the
  catalogs, queues the updates that fit above your reserved free space
  (most important and smallest first), and pauses those downloads when the
  window closes, continuing in the next one. Without internet it tries
  again later. *Check and queue now* runs a check at once. As with every
  update, the installed edition is replaced only once the new one is
  verified.
- **`arcana ask`** answers a question in the terminal; `arcana ask --eval
  FILE [--with-model]` scores retrieval (and answers) against a set of
  questions with known sources.
- **Integration API:** `GET /api/integration/v1/ask` with the new scope
  `assistant:ask` lets paired programs ask questions and receive the answer
  with its passages and citations.
- **Dependencies.** Queueing a resource also queues the resources it needs
  (a model queues the engine that runs it).

### Changed
- Text extracted from web pages keeps the cells of a table row on one line
  ("QTH | Location of the station"), so document search snippets and the
  assistant read tables correctly.

### Fixed
- **Your own documents no longer appear in searches from other devices.**
  Library search returned the names and text snippets of the documents you
  added (*Your documents*) to every device on the network, although the
  files themselves could only be opened on the computer running Arcana.
  Now devices on the network, signed-in administrators included, search
  only installed resources, and searching *Your documents* from them is
  refused; the computer running Arcana still finds everything. The
  integration API and the assistant already left your documents out for
  other devices; they now also stop them from taking up result slots.
- **Map region downloads no longer stall.** Since 0.6.2 a map region could
  occasionally stop partway and wait forever when one part of the map
  finished downloading a moment before the next part was asked for. It
  now carries on; a stalled download picks up where it stopped after
  updating.

## 0.7.0 — 2026-11-23

Directions and turn-by-turn navigation without internet, search inside the
archive or document you are reading, a reworked library search, measuring
on the map, and a choice of miles or kilometres. Archives that cannot be
opened now say why. Arcana 0.6.0 to 0.6.2 offer this version by themselves
within a day of being online: choose *Update now* in the banner at the top
of the page.

### Added
- **Units.** Choose kilometres and metres or miles and feet, in the map's
  Tools panel or under Settings → Appearance. The map scale bar, GPS speed
  and altitude, track lengths and measurements follow the choice. It is
  stored per browser, so each person on the network can pick their own;
  the default follows the browser's language (miles for the US, the UK,
  Liberia and Myanmar).
- **Measuring on the map.** *Measure distance* adds up straight-line
  (great-circle) distances between points you click and shows the bearing
  of the last leg; *Measure area* shows the area and perimeter of the shape
  you outline. Double-click or press *Done* to finish, *Undo point* (or
  Backspace) to take back a point, Escape to stop. Works offline.
- **Search inside what you are reading.** The reader has a search box for
  the open archive (press `/` to reach it). Titles complete as you type, so
  you can jump straight to an article; Enter lists every page that mentions
  the words beside the page you are reading, with *Show more results*.
  Archives without a full-text index are searched by title.
- **Find in documents.** PDFs, EPUBs and your own documents have a *Find in
  this document* bar that marks every match and steps through them with
  Enter, Shift+Enter or the arrows. Opening a document from search results
  marks the words you searched for.

- **Directions and turn-by-turn navigation, offline.** Install a *road
  network* for your country or state (Discover → Maps and navigation), then
  use *Directions* in the map view: choose a start (a place, your GPS
  position, or a point on the map) and a destination, and travel by car,
  bicycle or on foot. Arcana shows the route, its time and distance, and
  each turn. *Start navigation* follows your GPS position with the next
  turn and the distance to it, finds a new route if you leave this one, and
  can speak the instructions with the browser's voices. Routes can be saved
  as GPX. Road networks are built on your computer from OpenStreetMap
  extracts published by Geofabrik, checked against their checksum
  (ADR-0020). A route must stay inside one installed road network, and very
  large countries (such as Germany or France as a whole) are not offered
  yet because building them needs too much memory.

### Changed
- **Library search, reworked.** Results now start with articles whose
  titles match (an exact title first), then show each source separately
  with its number of matches, so a small archive such as a repair guide is
  no longer buried under an encyclopedia. *Show all in …* (or the new
  *Search in* menu) searches one source, a page at a time. Titles complete
  as you type, and the menu also offers your own documents.

### Fixed
- **Archives that cannot be opened now say why.** "Cannot open" in the
  Library is now a link, and the reader names the cause: the archive
  reader is still opening the archives, it could not start, or it skipped
  this archive as damaged (with kiwix-serve's own message and a hint to
  run a full check). Before, every case was blamed on "the archive reader
  is not available on this computer".
- The archive reader now gets more time to start when many or large
  archives are installed (30 s plus 20 s per archive, at most 15 minutes)
  instead of a fixed 30 s. Large archives such as iFixit take about a
  thousand scattered reads each to open, which on a slow external hard
  disk could run past 30 s and leave archives unopenable. A start that
  runs out of time is no longer retried from the beginning.
- In search results, a matching map opened the document viewer instead of
  the map.

## 0.6.2 — 2026-11-22

Downloads are much faster, and full-text search works again for common
words in larger archives. Arcana 0.6.0 and 0.6.1 offer this version by
themselves within a day of being online: choose *Update now* in the banner
at the top of the page.

### Changed
- **Much faster downloads.** Large files now download in 4 MiB pieces over
  up to six connections and several mirrors at once. Arcana measures each
  mirror and gives most of the work to the fastest one, and re-requests a
  piece from a faster mirror rather than waiting for a slow one. Pieces
  are still written and checked in order, so pausing, resuming after a
  crash and verification work as before. On a 306 MB Kiwix file this went
  from 2.4–5.8 MB/s to 64–84 MB/s (ADR-0019).
- **Faster map regions.** Map tiles are fetched with up to eight requests
  in flight instead of one at a time; a test region went from 30 s to 4 s.
- The Downloads view names the main mirror in use and how many others
  help.

### Fixed
- Full-text search failed when an archive reported 1,000 or more matches.
  kiwix-serve writes the count with a thousands separator ("1,800"), which
  Arcana could not read, so no results were shown at all. The count is now
  read whatever the separator, and an unreadable count no longer hides the
  results.

## 0.6.1 — 2026-11-22

Reading ZIM archives now works on Windows computers that do not have the
Microsoft Visual C++ Redistributable installed. Arcana 0.6.0 offers this
version by itself within a day of being online: choose *Update now* in the
banner at the top of the page.

### Added
- **The Windows build includes the Microsoft Visual C++ runtime**
  (msvcp140.dll, vcruntime140.dll, vcruntime140_1.dll, version
  14.52.37029.0) beside kiwix-serve, so ZIM archives open on computers
  without the Visual C++ Redistributable. The files are Microsoft's,
  unmodified, from the official installer and pinned by SHA-256
  (`VCRUNTIME-NOTICE.txt`). Windows drives updated with *Update now*
  receive them too.
- CI and the release workflow run the packaged Windows build on Windows
  and check that kiwix-serve loads these copies.

## 0.6.0 — 2026-11-22

The first public release. It includes everything from the development
milestones 0.1.0 to 0.5.0 (see CHANGELOG.md), which were not released
separately.

### Highlights
- **Portable library** on any drive (exFAT recommended): one program per
  platform, nothing to install, works without internet once filled.
- **Choose and download** from the Kiwix catalog (3,600+ archives),
  Project Gutenberg's practical shelves, a curated catalog and signed
  community catalogs; capacity planning before anything is downloaded;
  resumable downloads verified against the publisher's checksums.
- **Read and search** offline: ZIM archives (through kiwix-serve), PDFs,
  EPUBs and your own documents; bookmarks, notes and history.
- **Offline maps** of 541 regions plus custom areas, with place search;
  **GPS** from USB/serial receivers, gpsd, NMEA over UDP/TCP or a
  simulator; waypoints, tracks and GPX.
- **Share on the local network**, read-only for visitors, found as
  `arcana.local` or by QR code; optional HTTPS and password-protected
  remote administration.
- **Raspberry Pi / Linux appliance** installer with its own Wi-Fi network
  and captive portal.
- **Integration API** for programs such as RadioPost and Aether HF, and
  copying between Arcana libraries on a network.
- **One-click updates:** Arcana looks for a new version once a day while
  online and installs it, verified against the project's signature, with
  one click.

### Added for this release
- **Application updates in one click.** Once a day, starting two minutes
  after Arcana starts, Arcana checks the project's signed `release.json`
  (on by default; *Settings → Application updates* has the switch and
  *Check now*). A banner shows the new version and what is new; *Update
  now* downloads it, verifies the signature and hashes, installs it and
  restarts Arcana on the same port, and the page reloads into the new
  version. The previous version is kept for *Roll back*. The last check is
  kept on the drive, so the notice also appears offline. Copies that cannot
  replace themselves (the appliance service, copies outside the library)
  say how to update instead. This check is the only network request Arcana
  makes without being asked.
- **Signed releases.** The programs trust the project's release key; the
  release workflow signs each release's update manifest and checks the
  signature (`release-sign verify`). An unsigned release stays a draft.
- **Complete kiwix-serve sources.** Every release publishes the source of
  kiwix-serve and of every library built into it, in the exact versions
  shipped, with Kiwix's build recipes and patches and the GNU C library of
  the static Linux builds (`arcana-<version>-kiwix-sources.tar`, about
  170 MB, with a checksum list; see `licenses/SOURCES.txt`).
- **Release files** for each platform, an all-platform portable zip, and
  checksums; build provenance attestations for public releases.
- On Windows, when kiwix-serve cannot start because the Microsoft Visual
  C++ runtime is missing, the dashboard says so and what to install.

### Added in Phase 6 (integration and peer sync)
- **Integration API v1** (`/api/integration/v1/`) for programs such as
  RadioPost and Aether HF: search with citations, article text and bounded
  excerpts with an attribution footer, current position, coordinates with
  Maidenhead locator and nearby places, server-sent events, and short share
  links. Read-only; every call needs a scoped token. Programs pair by asking
  for access and the operator approves the request in *Settings →
  Integrations* after comparing a code. Tokens are stored hashed and can be
  revoked. Advertised on the network as `_arcana._tcp`.
- **Copy from another Arcana on the network** (*Settings → Other Arcana
  libraries*): browse its library, see what is new or newer, and copy
  through the download queue, verified against that library's checksums
  and the publisher's when known. Works without internet.
- **Signed catalogs:** a catalog feed can carry a publisher key; Arcana then
  accepts it only with a valid Ed25519 signature (`release-sign sign`
  produces it).
- docs/integration-api.md; docs/design/15-local-ai.md (design only).
- Acceptance step 11b: a second library copies from the first over the
  LAN address inside the offline namespace.

### Security
- Built with Go 1.26.11, which includes the standard-library security
  fixes released since Go 1.26.0 (found by govulncheck in CI).
- Token, pairing and peer management stay host-only even for signed-in
  network administrators. The integration API never accepts the UI's
  session tokens, and the UI API never accepts integration tokens.

### Known limitations
- Tested automatically on Linux; the Windows and macOS programs pass the
  test suite on GitHub's runners but have not been tried on real machines
  by the developers. Nothing has been run on Raspberry Pi hardware or with
  a physical GPS receiver yet.
- The programs are not code-signed: Windows SmartScreen and macOS
  Gatekeeper warn on first start (README-FIRST.txt explains how to proceed).
- On Windows, the ZIM reader (kiwix-serve, built by Kiwix) needs the
  Microsoft Visual C++ runtime, which Arcana does not include. Most
  computers have it; Arcana explains when it is missing.
- One-click updates start with this release: this version will offer the
  next one. The automatic restart has been tested on Linux; on Windows and
  macOS it is covered by the test suite only.
- Map regions are extracted from Protomaps planet builds on demand; they
  have no publisher checksum and are labelled unverified.
- On Windows and macOS, Arcana cannot create a Wi-Fi hotspot itself.

## 0.5.0 — development milestone (Phase 5: appliance)

### Added
- **Appliance installer** for Raspberry Pi OS (Bookworm) and other systemd
  Linux: `appliance/install.sh` in the release creates an unprivileged
  `arcana` service user, installs a confined `arcana.service` that starts
  at boot, and with `--ap` sets up a Wi-Fi access point (NetworkManager)
  with a captive portal that leads phones to the library. `--dry-run`
  previews every step; `--uninstall` removes the service but keeps the
  library.
- systemd integration: readiness notification and a watchdog that is fed
  only while the web server answers.
- `--captive-port`: a port that redirects every request (OS connectivity
  checks, typed addresses) to the library; it never serves the API.
- docs/appliance.md: installation, library drives, how phones connect,
  read-only system, updating, security notes.
- CI checks the installer (shellcheck, dry run), the units
  (`systemd-analyze verify`) and the firewall rules (nftables syntax).

## 0.4.0 — development milestone (Phase 4: network serving)

### Added
- **Remote administration** (off by default): the host sets a password in
  the Server view; people on the network sign in to manage downloads,
  catalog and settings. Drives, transfers, program updates, the password
  and the host's private notes stay host-only. Argon2id password hash,
  HttpOnly cookies with a per-session token, throttled sign-in.
- **Name on the network:** a built-in mDNS responder advertises
  `http://arcana.local:8470/` (configurable; renamed automatically if taken)
  and a `_http._tcp` service while sharing.
- **QR codes** for every network address in the Server view and a printable
  **connection card** (`#/connect`).
- **Optional HTTPS** on the same ports as HTTP, with a self-signed
  certificate (fingerprint shown) or your own in `user/tls/`. Phones get a
  secure context, so they can share their own location with the map.
- **Wi-Fi hotspot** control on Linux with NetworkManager (Server view and
  `arcana hotspot start|stop|status`), with a QR code to join it.
- Acceptance step 7b covers remote administration from a LAN address.

### Changed
- Opening a map from the Library goes to the Maps view, centred on it.
- Leaving the Maps view while updates are arriving no longer raises errors.

## 0.3.0 — development milestone (Phase 3: offline maps and GPS)

### Added
- **Maps** view with offline vector maps rendered in the browser by
  MapLibre GL JS 5 (vendored; no CDN), Protomaps light and dark styles,
  bundled Noto glyphs and sprites, and permanent OpenStreetMap attribution.
- A `maps` catalog provider with 541 regions: the world (zoom 6 and 7), six
  continents, 241 countries and 293 states and provinces at overview,
  standard and detailed levels, with sizes measured against a real planet
  build and bundled, so regions can be browsed offline.
- Region downloads extract only the needed tiles from the newest Protomaps
  planet build with HTTP range requests. They run in the download manager,
  resume after interruptions, restart cleanly if the build changes or is
  deleted, and record a SHA-256 for later integrity checks.
- "Download this area": measure the visible map area against the newest
  build (exact size before downloading) and queue it.
- Overlapping maps are merged into one map; where only a low-detail map
  exists, tiles are derived from it so zooming in never shows a blank map.
- Offline place search from the installed maps' place names.
- Position from USB/serial GPS receivers, gpsd, NMEA over UDP or TCP, a
  simulator that replays GPX tracks as NMEA, or a fixed position, with
  current/stale/lost states, live updates, coordinates in decimal degrees,
  DMS and Maidenhead locator, follow mode, and a dashboard readout.
- Waypoints (click the map or save the current position), track recording
  to GPX, GPX import and export.
- Settings → Maps and position.
- Acceptance steps 5 (offline maps) and 6 (simulated GPS and a UDP receiver)
  are implemented and pass without internet.

### Changed
- Resources marked as multilingual (`mul`), such as maps, now appear under
  every language filter in Discover.
- The content security policy allows `blob:` images and same-origin workers
  for the map renderer.

### Security
- Position, waypoints and tracks are shown to network clients only when the
  host turns on "Share position" (off by default). Planning a map area,
  which contacts the map source, is a host-only action.

## 0.2.0 — development milestone (Phase 2: content management and updates)

### Added
- User data in `user/` as plain, human-readable files: bookmarks, favorites,
  reading history and notes (Markdown with a small header). Bookmarks point at
  a page inside a resource rather than an edition, so they survive updates.
  Deleted notes and documents move to recoverable folders.
- Reader integration: archive pages report their location to the reader, which
  offers Bookmark and a Notes panel and records history.
- Your documents: upload or copy files into `user/documents`; PDF, EPUB,
  HTML, Markdown and text are extracted and indexed (SQLite FTS5,
  `index/documents.db`, rebuildable). Installed PDF and EPUB resources are
  indexed too. One search box returns archive articles and documents; documents
  have an in-app text preview.
- Collection transfer between drives with no network: export, import from an
  export or directly from another library, identical content skipped,
  every copy verified against the recorded SHA-256, user documents merged
  without overwriting. `arcana export`, `arcana import --from`, Settings →
  Transfer. Portable library manifest (`arcana manifest`).
- Signed application updates: Ed25519-signed `release.json`, staged and verified
  before a rename-based swap, previous version kept for rollback, offline
  installation from a folder copied from another drive, never automatic.
  `tools/release-sign` creates keys, manifests and signatures.
- Project Gutenberg provider (opt-in): about 6,500 practical public-domain books
  from the official mirror, with strong warnings on historical medical texts.
- 47 CFR Part 95 (GMRS, FRS, CB, MURS) in the curated catalog.
- Saved custom collections, "Select all shown" in Discover, optional scheduled
  catalog refresh (off by default).
- Background tasks with progress for transfers, full integrity checks and
  update downloads.
- Acceptance test step: offline transfer to a second drive and full check.

## 0.1.0 — development milestone (Phase 1 vertical slice)

### Added
- Portable launch on Windows (Arcana.exe), Linux and macOS (arcana.sh) from a
  library folder; no installation or administrator rights.
- Library layout with drive-resident settings, OS-level single-writer lock,
  read-only mode, and relocation-safe relative paths.
- Volume detection (capacity, file system, removable media) and capacity
  planning with a user-defined reserve.
- Provider-based catalog: bundled curated catalog, Kiwix OPDS catalog,
  Arcana JSON feeds; untrusted-input validation; offline catalog cache.
- Resumable, verified download manager with crash-safe checkpoints, mirror
  fallback, backoff, bandwidth limit and atomic publication.
- Offline import of archives from another drive, verified against catalog
  checksums.
- ZIM reading and full-text search through kiwix-serve (separate process,
  separate browser origin).
- Web interface: Library, Discover, Downloads, Updates, Server, Settings,
  first-run setup; light and dark themes.
- Integrity checker (quick and full) and database rebuild from sidecars.
- Offline acceptance test in an isolated network namespace.

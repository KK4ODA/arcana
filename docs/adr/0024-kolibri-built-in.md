# ADR-0024: Kolibri built in, for Khan Academy classrooms

- Status: Accepted (not yet tried on real hardware)
- Amended by: [ADR-0025](0025-more-classroom-channels.md), which offers
  60 channels instead of two
- Date: 2026-11-24

## Context
Teachers and students want Khan Academy with classes, lessons, quizzes and
progress. Kiwix's Khan Academy archives (March 2023, 180 GB for English) are
read-only and all-or-nothing. Learning Equality's Kolibri (MIT) serves Khan
Academy channels refreshed in December 2025 and January 2026, imports any
part of a channel, and has its own Learn (students) and Coach (teachers)
pages. Arcana could already search a Kolibri server someone else installed.

Kolibri is a Python program: version 0.19.5 is a 104 MB wheel that bundles
its dependencies (Django and others) and runs on Python 3.6 to 3.14. Its
compiled speed-ups ship for CPython 3.11 at the newest. It keeps its data
in `KOLIBRI_HOME`, listens on a port of its own, and serves channel HTML
apps ("zip content") from a second port, so that they run in a separate
browser origin.

The owner chose (2026-11-24): Kolibri built into Arcana; the English (US)
and Spanish Khan Academy channels, chosen by topic or grade with sizes; all
three systems from the drive; Arcana sets Kolibri up, teachers get
passwords and students sign in by username alone.

## Decision
- **One catalog resource, `arcana.kolibri`.** The Kolibri 0.19.5 wheel and
  CPython 3.11.17 (python-build-standalone 20261003, `install_only_stripped`)
  for each platform Arcana is released for: Windows x86-64, macOS ARM64 and
  x86-64, Linux x86-64 and ARM64. Six files, 245 MB, pinned by SHA-256 in
  the bundled catalog and fetched by the download manager like any other
  resource (ADR-0009). Mirrors: PyPI for the wheel; GitHub and Astral's
  mirror (`releases.astral.sh`) for Python.
- **Packed on the drive, unpacked on each computer.** The wheel unpacks to
  10,800 files and Python to about 4,900. On an exFAT drive with 128 KB
  clusters the wheel alone would take 1.4 GB, and small files read slowly
  over USB. Arcana keeps the six files packed in `content/` and unpacks the
  ones for the running platform into the computer's cache folder
  (`<user cache>/arcana/kolibri/<hash>/`), marked complete only after every
  file is written. This also sidesteps drives mounted without permission to
  run programs. Without a cache folder it unpacks under `state/kolibri/`.
- **Kolibri's data stays on the drive** in a new fixed area, `kolibri/`
  (`KOLIBRI_HOME`): its database, accounts, progress and imported channels.
  Only Kolibri writes there. A read-only library does not start Kolibri.
- **Arcana provisions Kolibri** with `kolibri manage provisiondevice`: a
  facility named after the library, the `formal` preset (students sign in
  with their username, without a password, and cannot change it), an
  administrator `admin` with a random password, guest access on (so
  Arcana's search can read Kolibri), and the interface language of the
  first channel chosen. The password is kept in `state/kolibri.json` and
  shown to Arcana's administrators, who use it to add teachers and
  students in Kolibri's own Facility page.
- **Arcana runs and supervises Kolibri** like kiwix-serve: loopback only
  (`KOLIBRI_LISTEN_ADDRESS=127.0.0.1`), ports chosen by Arcana, restarted
  if it stops, stopped with Arcana. Statistics pings and Zeroconf are off.
- **Kolibri gets its own origin.** The application origin is port P and
  the content origin P+1 (ADR-0013). Kolibri is a web application with its
  own accounts. Serving it beside Arcana's interface would let a flaw in
  Kolibri reach Arcana's administration. Serving it beside archive pages
  would let a hostile archive act as a signed-in teacher. Arcana therefore
  listens on P+2 as well and proxies it to Kolibri, with the Host check and
  HTTPS of the other two ports. Kolibri's zip content is read-only,
  untrusted material like archive pages. It goes to the content origin
  under `/kolibri-zip/` (`ZIP_CONTENT_ORIGIN` set to P+1,
  `ZIP_CONTENT_URL_PATH_PREFIX`), so it still runs apart from Kolibri.
  Kolibri puts the zip port into its CSP as a bare number, which is not a
  valid source. Arcana's proxy replaces it with the content origin in
  `frame-src` and `media-src`. (Corrected 2026-11-25, after the first run
  against Kolibri itself: Kolibri 0.19.5 puts the port in as an integer,
  which its CSP library cannot join, so every response failed with HTTP 500
  and the classroom never left "starting". Arcana runs Kolibri with its own
  settings module, `arcana_kolibri_settings`, served by the bootstrap
  without a file, which imports Kolibri's settings and turns every CSP
  source into text.)
- **Imports go through Kolibri.** Arcana fetches each channel's database
  from Kolibri Studio (`/content/databases/<id>.sqlite3`, 17 MB and 44 MB
  compressed), shows its first two levels with the size of each topic (the
  files under it, counted once however many topics share them), and asks
  Kolibri for a `remoteimport` task with the chosen topic IDs through its
  tasks API, signed in as the administrator. Kolibri downloads, resumes,
  checks disk space and shows the same import in its Device page. Arcana
  shows the task's progress and can cancel it.
- **Search** uses the running Kolibri through the existing client; results
  link to the Kolibri origin.

## Consequences
- One more port (P+2, 8472 by default). The firewall's rule for Arcana
  covers it; Kolibri itself never listens beyond loopback, so no prompt
  for Python.
- The first start on each computer unpacks about 340 MB (a few seconds on
  a local disk). The first start of Kolibri migrates its database, about a
  minute.
- If the owner changes the administrator's password in Kolibri, Arcana
  asks for the new one before it can import again.
- Khan Academy is licensed CC BY-NC-SA: non-commercial use only. Kolibri
  shows each channel's licence.
- Kolibri, its proxy and its CSP fix were first checked only against
  Kolibri's source and a stand-in. Run against Kolibri 0.19.5 itself on
  Linux (2026-11-25), three faults appeared that the stand-in hid: the CSP
  above; the device plugin's API is under `/device/api/`, not
  `/api/device/`; and Kolibri's CSRF cookie is `kolibri_csrftoken`, not
  Django's `csrftoken`, so every administrator POST was refused. The
  stand-in now behaves as Kolibri does in these. Windows and macOS are not
  yet checked.

# The classroom: Kolibri and Khan Academy

[Kolibri](https://learningequality.org/kolibri/), from Learning Equality,
is an offline learning platform. Students watch videos, read and practise
exercises; teachers make classes, lessons and quizzes and follow each
student's progress. Arcana can run Kolibri itself, from the drive, as its
**classroom**, with Khan Academy in English (US curriculum) or Spanish.
It can also search a Kolibri server you installed separately (at the end of
this page).

## Set up the classroom

1. Open **Classroom** in Arcana's top bar, on the computer running Arcana
   (or signed in as an administrator).
2. Choose Kolibri's language and press **Set up the classroom**. Arcana
   downloads Kolibri 0.19.5 with Python 3.11 for Windows, macOS and Linux:
   245 MB, checked against published SHA-256 checksums like every
   download. The drive's classroom then runs on any of those systems.
3. On each computer, the first start unpacks Kolibri onto that computer's
   disk (about 340 MB in its cache folder, a few seconds), and the first
   start ever prepares Kolibri's database (about a minute, longer on a
   Raspberry Pi). The Classroom page shows each step.
4. Under **Khan Academy**, press **Choose grades and subjects** for a
   channel. Arcana fetches the channel's list of topics from Kolibri Studio
   (17 MB for Spanish, 44 MB for English) and shows each subject with its
   size. Tick whole subjects, or open one (▸) and tick grades or courses.
   The total counts each video once, even when it appears under several
   topics. Press **Download chosen**.
5. Kolibri downloads the chosen courses. Progress shows on the Classroom
   page; downloads resume after an interruption. Choose more at any time.

| Channel | Whole channel | Published |
|---|---|---|
| Khan Academy (English, US curriculum) | 67.8 GB | 23 January 2026 |
| Khan Academy (Español) | 39.4 GB | 23 December 2025 |

Single grades are much smaller: English 3rd-grade math is 377 MB, Spanish
1° Primaria math 110 MB.

## Teachers and students

- **The classroom's address** is Arcana's address with the port after the
  content port: `http://<this computer>:8472/` by default. **Open the
  classroom** on the Classroom page, the Library page's tile and search
  results all go there. Other devices reach it once Arcana shares on the
  network (*Server*), the same way they reach the library.
- **The administrator.** Arcana creates the school (named after the library)
  and an administrator, `admin`, with a random password. The Classroom page
  shows it to Arcana's administrators (*Show*, *Copy*). Sign in to the
  classroom with it and open *Facility → Users* to add teachers (coaches) and
  students.
- **Students** sign in with their username alone: Arcana sets Kolibri's
  *formal* preset, so students have no password and cannot change their
  name. Anyone can also *explore without an account*; nothing is recorded
  then.
- **Teachers** sign in with their password and open **Coach** to make
  classes, lessons and quizzes and see each student's progress.
- If you change the administrator's password in Kolibri, enter the new one
  under *Changed this password in Kolibri?* on the Classroom page, so Arcana
  can still download courses.
- **Library search** shows matching courses in a *Courses in the classroom*
  section after the library's own results.

## Where things are kept

| What | Where |
|---|---|
| Kolibri and the Python runtimes, packed | `content/arcana.kolibri/` on the drive |
| Kolibri unpacked, for one computer | that computer's cache folder (`arcana/kolibri-…`), or the library's `state/runtime/` when there is none |
| Accounts, progress, imported courses | `kolibri/` on the drive (Kolibri's `KOLIBRI_HOME`) |
| The administrator's password, Arcana's settings for Kolibri | `state/kolibri.json` on the drive |
| Channel topic lists | `catalog/kolibri/` on the drive |

*Turn off the classroom* stops Kolibri and keeps all of it; turning it on
again picks everything up. A library opened read-only does not start
Kolibri. Arcana's *Library integrity* checks cover the packed runtime, not
the courses Kolibri imports.

## How it works

Arcana starts Kolibri on the loopback interface only, on ports it chooses,
and supervises it like the archive reader: it restarts Kolibri if it stops,
and Kolibri stops with Arcana, even if Arcana is killed (Kolibri ends when
its standard input closes). Kolibri's statistics pings and network
discovery are off.

Browsers reach Kolibri through Arcana, on its own port (8472 by default),
apart from Arcana's interface (8470) and from archive pages (8471). A
separate origin keeps a flaw in Kolibri away from Arcana's administration,
and keeps a hostile archive away from a signed-in teacher. Kolibri's
channel apps (HTML5 activities) are served on the content port under
`/kolibri-zip/`, as Kolibri's design asks: apart from Kolibri itself.
Arcana refuses changes sent to Kolibri from another origin, and does not
pass Arcana's own session cookie to Kolibri.

Arcana provisions Kolibri from a file Kolibri reads at its first start (the
school, the `formal` preset, guest access, the administrator), then deletes
the file. It downloads courses by asking Kolibri, signed in as the
administrator, to import the chosen topics (Kolibri's `remoteimport`
task), so Kolibri's own Device page shows the same imports.
[ADR-0024](adr/0024-kolibri-built-in.md) records the design.

## Licences

Kolibri is MIT-licensed and bundles its dependencies under their own
licences; Python is under the Python Software Foundation License. Khan
Academy material is licensed CC BY-NC-SA: **non-commercial use only**.
Other channels carry their own licences, shown in Kolibri.

## A Kolibri server installed separately

Arcana can also search a Kolibri you run yourself, on this computer or
another one on the network. When the classroom is set up, it takes this
server's place.

1. Install Kolibri following Learning Equality's instructions for your
   system and start it (by default on port 8080). Import channels in
   Kolibri (*Device → Channels*).
2. In Kolibri's facility settings, allow access to resources without
   signing in. Without guest access Kolibri refuses searches from Arcana,
   and Arcana says so.
3. In Arcana, open *Settings → Kolibri (courses)*, enter Kolibri's address
   (for example `http://192.168.1.20:8080`), press *Test*, then *Save
   settings*.

Its courses then appear in library search, and the Library page shows a
*Kolibri courses* tile. Arcana contacts only that address: `GET
/api/public/info/` for *Test* and the status, and `GET
/api/content/contentnode/?keywords=…` for search. Links point at
`/learn/#/topics/c/<id>` (content) and `/learn/#/topics/t/<id>` (topics).

# The classroom: Kolibri and Khan Academy

[Kolibri](https://learningequality.org/kolibri/), from Learning Equality,
is an offline learning platform. Students watch videos, read and practise
exercises; teachers make classes, lessons and quizzes and follow each
student's progress. Arcana can run Kolibri itself, from the drive, as its
**classroom**, with Khan Academy in English, Spanish and 25 other
languages, its test preparation, and 30 channels from other publishers for
high school, college and adults ([the channels](#the-channels)). It can
also search a Kolibri server you installed separately (at the end of this
page).

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
   Raspberry Pi or a slow drive). The Classroom page shows each step;
   while Kolibri starts it shows how long it has been starting and
   Kolibri's latest message, which changes as it works.
4. Find a channel under **Khan Academy**, **More courses for high
   school, college and adults** or **Khan Academy in other languages**
   (folded; open it to see them). Each says who it is for, what it holds,
   its size and its licence. Press **Choose grades and subjects** (or
   **Choose topics**). Arcana fetches the channel's list of topics from
   Kolibri Studio (up to 44 MB, for the largest channel) and shows each
   subject with its size. Tick **Everything in this channel**, whole
   subjects, or open one (▸) and tick grades or courses. The total counts
   each file once, even when it appears under several topics. Press
   **Download chosen**.
5. Kolibri downloads the chosen courses. Progress shows on the Classroom
   page; downloads resume after an interruption. Choose more at any time.

Single grades are much smaller than whole channels: English 3rd-grade math
is 377 MB, Spanish 1° Primaria math 110 MB, AP Calculus AB and BC together
1.9 GB.

## The channels

Sizes, dates and licences are Kolibri Studio's on 24 November 2026; the
picker shows each channel's current sizes. Many channels are licensed for
**non-commercial use only**. Kolibri shows each resource's own licence.
[ADR-0025](adr/0025-more-classroom-channels.md) records how they were
chosen.

**Khan Academy.** Its videos are CC BY-NC-SA; its exercises are
distributed through Kolibri by permission, for non-commercial use.

| Channel | Language | Levels | Whole channel | Published | Licence |
|---|---|---|---|---|---|
| Khan Academy (English, US curriculum) | English | Kindergarten to college, AP | 67.8 GB | 23 January 2026 | CC BY-NC-SA and permission (non-commercial) |
| Khan Academy (Español) | Spanish | Primary school to college, AP | 39.4 GB | 23 December 2025 | CC BY-NC-SA and permission (non-commercial) |
| Khan Academy: test preparation | English | High school to graduate school | 10.5 GB | 11 July 2020 | CC BY-NC-SA and permission (non-commercial) |
| Khan Academy (English, Philippines curriculum) | English | Grade 1 to senior high school | 14.4 GB | 23 January 2026 | CC BY-NC-SA and permission (non-commercial) |
| Khan Academy (English, India CBSE curriculum) | English | Class 1 to college | 54.8 GB | 23 January 2026 | CC BY-NC-SA and permission (non-commercial) |

**For high school, AP and college,** Khan Academy's English (US) channel
has AP Calculus AB and BC, AP Statistics, AP Biology, AP Physics 2, AP
Macroeconomics and Microeconomics, AP US History, AP US Government and AP
Art History, and college multivariable calculus, differential equations,
linear algebra, organic chemistry and electrical engineering. It does not
have AP Chemistry, AP Physics 1, AP Environmental Science, AP Computer
Science Principles, computing, or Khan Academy's articles (only videos and
exercises). Khan Academy's Spanish channel has AP Physics 1 and 2, AP
Chemistry and AP Biology; OpenStax has The AP Physics Collection and AP
editions of biology and economics; CK-12 and the Philippine channel have
high school chemistry and physics with exercises.

**More courses for high school, college and adults**, from other
publishers:

| Channel | Language | Levels | Whole channel | Published | Licence |
|---|---|---|---|---|---|
| CK-12 | English | Primary school to high school | 6.7 GB | 30 March 2023 | CC BY-NC (non-commercial) |
| OpenStax | English | College, AP | 2.1 GB | 12 December 2020 | CC BY; 36 resources CC BY-NC-SA (non-commercial) |
| MIT Blossoms | English | High school | 26.0 GB | 5 July 2023 | CC BY-NC-SA (non-commercial) |
| PhET Interactive Simulations | English | Primary school to college | 1.1 GB | 7 December 2024 | CC BY |
| EngageNY | English | Pre-kindergarten to grade 12 | 7.8 GB | 5 July 2023 | CC BY-NC-SA (non-commercial) |
| Thoughtful Learning | English | Grades 1 to 12 | 352 MB | 25 July 2018 | CC BY-NC-SA (non-commercial) |
| Sciensation | English | Secondary school | 125 MB | 5 March 2021 | CC BY-NC-SA (non-commercial) |
| School of Thought | English | High school and adults | 115 MB | 18 October 2021 | CC BY |
| BCcampus Open Textbooks | English | College and trades | 1.2 GB | 14 March 2025 | CC BY, CC BY-NC or CC BY-NC-SA by book (some non-commercial) |
| Osmosis | English | Medical and nursing school | 5.2 GB | 5 July 2023 | CC BY-SA |
| Goalkicker programming books | English | College and adults | 495 MB | 20 July 2020 | CC BY-SA |
| Career Girls | English | Middle school to college | 24.4 GB | 7 August 2019 | Career Girls' permission for use in Kolibri |
| HP LIFE courses | English | Adults | 771 MB | 11 February 2020 | CC BY |
| Sikana | English | Teenagers and adults | 15.8 GB | 10 June 2025 | CC BY-NC-ND (non-commercial) |
| Espresso English | English | Adults learning English | 4.0 GB | 5 July 2023 | CC BY |
| Digital Awareness | English | Secondary school | 260 MB | 25 July 2022 | CC BY-NC-ND (non-commercial) |
| Digital Discovery | English | Secondary school | 480 MB | 28 June 2022 | CC BY-NC-ND (non-commercial) |
| PhET: simulaciones interactivas | Spanish | Primary school to college | 504 MB | 10 December 2024 | CC BY |
| Proyecto Descartes | Spanish | Secondary school | 8.0 GB | 5 April 2019 | CC BY-NC-SA (non-commercial) |
| EngageNY (Español) | Spanish | Pre-kindergarten to grade 12 | 9.1 GB | 5 July 2023 | CC BY-NC-SA (non-commercial) |
| CommonLit (Español) | Spanish | Grades 3 to 12 | 179 MB | 13 March 2024 | CC BY-NC-SA (non-commercial) |
| Ciencia NASA | Spanish | Secondary school and adults | 3.4 GB | 5 July 2023 | CC BY |
| Ciensación | Spanish | Secondary school | 125 MB | 5 March 2021 | CC BY-NC-SA (non-commercial) |
| Ceibal | Spanish | Primary and secondary school | 16.9 GB | 13 July 2019 | Mostly CC BY-NC (non-commercial) |
| CREE+ | Spanish | Primary school to upper secondary | 26.8 GB | 28 August 2026 | CC BY-NC-SA, some by permission to distribute through Kolibri (non-commercial) |
| Biblioteca Elejandria | Spanish | All ages to university | 1.0 GB | 11 April 2021 | Public domain |
| WiiXii | Spanish | Primary school to adults | 86 MB | 17 June 2025 | CC BY-SA |
| HP LIFE: cursos | Spanish | Adults | 684 MB | 11 February 2020 | CC BY |
| Sikana (Español) | Spanish | Teenagers and adults | 10.1 GB | 22 April 2019 | CC BY-NC-ND (non-commercial) |
| Cultura Emprendedora | Spanish | Secondary school and adults | 226 MB | 25 July 2019 | CC BY-NC-SA (non-commercial) |

**Khan Academy in other languages** (CC BY-NC-SA and permission,
non-commercial). Several hold only early math; the Classroom page
describes each.

| Language | Levels | Whole channel | Published |
|---|---|---|---|
| Armenian | Kindergarten to grade 12 | 8.7 GB | 23 December 2025 |
| Azerbaijani | Preschool to college | 7.0 GB | 9 January 2026 |
| Bangla | Kindergarten to grade 8 | 3.3 GB | 19 December 2025 |
| Bulgarian | Preschool to college | 32.9 GB | 19 December 2025 |
| Burmese | Kindergarten to grade 2 | 248 MB | 24 December 2025 |
| Chinese | Kindergarten to college, AP | 14.8 GB | 27 December 2025 |
| French | Primary school to college | 23.5 GB | 23 December 2025 |
| Fulfulde | Kindergarten to grade 3 | 117 MB | 6 February 2026 |
| Georgian | Kindergarten to college | 19.1 GB | 24 December 2025 |
| Greek | Kindergarten to grade 8 | 2.2 GB | 19 December 2025 |
| Gujarati | Kindergarten to Class 12 | 7.5 GB | 23 December 2025 |
| Hindi | Classes 6 to 12 | 6.8 GB | 23 December 2025 |
| Indonesian | Kindergarten to grade 6 | 1.1 GB | 23 December 2025 |
| Italian | Kindergarten to grade 8, algebra | 2.8 GB | 23 December 2025 |
| Kannada | Kindergarten to Class 10 | 1.2 GB | 6 February 2026 |
| Khmer | Primary school | 814 MB | 24 December 2025 |
| Korean | Primary school to college | 8.2 GB | 24 December 2025 |
| Kyrgyz | Kindergarten to grade 2 | 213 MB | 24 December 2025 |
| Portuguese (Brazil) | Primary school to college, AP | 31.0 GB | 25 December 2025 |
| Portuguese (Portugal) | Preschool to grade 12 | 8.9 GB | 26 December 2025 |
| Swahili | Kindergarten to college, AP | 2.9 GB | 6 July 2023 |
| Tamil | Kindergarten to grade 5 | 1.2 GB | 26 December 2025 |
| Turkish | Grade 1 to college, AP | 36.6 GB | 26 December 2025 |
| Ukrainian | Preschool to grade 6 | 175 MB | 16 December 2022 |
| Uzbek | Kindergarten to college | 13.8 GB | 26 December 2025 |

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

## If the classroom does not start

The Classroom page says why: Kolibri stopped (with the error it printed),
it runs but answers every request with an error (Arcana gives up after 30
seconds of that, with the error), or it did not answer within 15 minutes.
*Try again* starts it once more. Kolibri's own log is
`kolibri/logs/kolibri.txt` on the drive; include its last lines when you
report a problem.

Arcana 0.10.1 and earlier could not start the classroom at all: Kolibri
answered every request with an error, so the page stayed on *Starting*.
Later versions fix this; a classroom set up with an earlier version starts
with them, keeping its administrator password.

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
Other channels carry their own licences, listed in [the
channels](#the-channels) and shown for each resource in Kolibri.

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

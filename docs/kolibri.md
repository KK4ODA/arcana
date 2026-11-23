# Kolibri courses alongside Arcana

[Kolibri](https://learningequality.org/kolibri/), from Learning Equality,
is an offline learning platform: Khan Academy and many other course
channels, exercises, and progress for each learner. It is a separate
program with its own database and accounts, so Arcana does not bundle it.
Arcana can *use* a Kolibri server you run:

- its courses appear in library search, in a *Courses in Kolibri* section
  after the library's own results;
- the Library page shows a *Kolibri courses* tile;
- results open in Kolibri's own Learn page.

## Set it up

1. Install Kolibri on the same computer or another one on the network,
   following Learning Equality's instructions for your system (installers
   for Windows and macOS; packages or `pip install kolibri` for Linux and
   Raspberry Pi OS). Start it (by default on port 8080).
2. In Kolibri, import the channels you want (Device → Channels), while
   online or from a drive.
3. In Kolibri's facility settings, allow access to resources without
   signing in. Without guest access Kolibri refuses searches from Arcana,
   and Arcana says so.
4. In Arcana, open *Settings → Kolibri (courses)*, enter Kolibri's address
   (for example `http://192.168.1.20:8080`, or `http://127.0.0.1:8080` on
   the same computer), press *Test*, then *Save settings*.

Kolibri keeps its data in `KOLIBRI_HOME` (by default a folder in your home
directory). Pointing it at a folder on the Arcana drive keeps courses on
the drive, but Kolibri itself still has to be installed on each computer.

## On the Raspberry Pi appliance

Kolibri runs well on a Raspberry Pi 4 or 5 next to Arcana. Install it as
Learning Equality describes for Raspberry Pi OS, enable its service, and
enter `http://127.0.0.1:8080` in Arcana. Devices on the appliance's Wi-Fi
reach Kolibri at `http://<appliance address>:8080/`; the captive portal
only redirects port 80, so it does not get in the way. (Not yet tried on
hardware, like the rest of the appliance.)

## Licences

Khan Academy material is licensed CC BY-NC-SA: non-commercial use only.
Other channels carry their own licences, shown in Kolibri.

## How it works

Arcana contacts only the address you entered: `GET /api/public/info/`
for *Test* and the status, and `GET /api/content/contentnode/?keywords=…`
for search (the parameter every Kolibri version with this API accepts).
Links point at `/learn/#/topics/c/<id>` (content) and `/learn/#/topics/t/<id>`
(topics), Kolibri's Learn routes since 0.15. Nothing is copied from
Kolibri into the library.

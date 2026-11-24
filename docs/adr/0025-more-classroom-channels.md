# ADR-0025: More channels in the classroom

- Status: Accepted (not yet tried on real hardware)
- Date: 2026-11-24
- Amends: [ADR-0024](0024-kolibri-built-in.md), which offered only Khan
  Academy in English (US curriculum) and Spanish

## Context
The owner wants the classroom to cover high school, AP and later courses
as fully as possible. Khan Academy's English (US) channel on Kolibri Studio
(23 January 2026) has AP Calculus AB and BC, AP Statistics, AP Biology,
AP Physics 2, AP Macroeconomics and Microeconomics, AP US History, AP US
Government and AP Art History. It lacks AP Chemistry, AP Physics 1, AP
Environmental Science, AP Computer Science Principles, computing, health
and medicine, high school English and test preparation. It also has none
of Khan Academy's articles, only videos and exercises. Kolibri Studio's
public library has 175 channels, among them other Khan Academy channels
and other publishers' textbooks, simulations and courses.

Arcana's picker, sizes and imports already work for any channel: they read
the channel's database from Kolibri Studio and ask Kolibri to import topic
IDs. What limited the classroom was the list of two channels.

Some channels keep resources directly under their root rather than in a
topic, and the picker offers only topics.

## Decision
- **Sixty channels, in three groups** (`kolibri.Channels`, in
  `internal/kolibri/channellist.go`):
  - *Khan Academy*: English with the US, Philippine and Indian (CBSE)
    curricula, Spanish (which has AP Physics 1 and 2, AP Chemistry and
    AP Biology), and Khan Academy's archive of test preparation (SAT before
    2024, MCAT, NCLEX-RN, LSAT, GMAT, Praxis Core; last published 2020).
  - *More courses for high school, college and adults*: 30 channels from
    other publishers in English and Spanish, among them CK-12, OpenStax
    (which has the AP Physics collection and AP editions of biology and
    economics), MIT Blossoms, PhET, EngageNY, BCcampus, Osmosis, Goalkicker
    and Career Girls.
  - *Khan Academy in other languages*: the other 25 Khan Academy channels.
    The Classroom page shows this group folded.
- **Chosen for high school and later.** Channels for younger children only
  are left out: CSpathshala (grades 1 to 8), Proyecto Biosfera (ages 12 to
  14) and TED-Ed (primary to lower secondary, under TED's usage policy).
  Other publishers' channels in languages other than English and Spanish
  are left out. Khan Academy's channels in other languages are included,
  as the owner asked.
- **Each channel says who it is for, what it holds and how it is
  licensed.** `Levels` and `About` are written from the channel's own
  topics; `License` sums up the licences its resources carry in its
  database, all checked on 24 November 2026. Khan Academy marks its videos
  CC BY-NC-SA and its exercises "Special Permissions: permission granted to
  distribute through Kolibri for non-commercial use". A licence with
  non-commercial terms says "non-commercial use only" in words; a test
  enforces this. Kolibri shows each resource's own licence.
- **Everything in a channel.** The picker offers the channel's root topic
  as *Everything in this channel*. Kolibri imports every resource under a
  topic ID it is given (`get_import_export_nodes` in Kolibri 0.19.5), so
  the root takes the whole channel, including resources outside its topics.

## Consequences
- The Classroom page lists 60 channels, about 596 GB if everything were
  downloaded. The descriptions and sizes help the owner choose; nothing is
  downloaded until they choose.
- Sizes, dates and licences are a snapshot. Kolibri shows the current
  channel when it imports, and the picker's sizes come from the channel's
  current database. The list itself changes only with Arcana.
- Kolibri's interface language at setup is still English or Spanish.
  Learners can change it in Kolibri.
- The new channels are tested in the same way as the first two: with real
  channel databases in the picker and a stand-in for Kolibri, not yet with
  Kolibri importing them.

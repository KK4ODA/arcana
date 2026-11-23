# P. Growing the amateur radio library, and searching manuals by page (design)

Status: **design and research, not implemented.** Sources and licenses
were checked on 2026-11-23; recheck each one when its catalog entry is
written. Builds on [14-radio-and-integrations.md](14-radio-and-integrations.md)
(radio content decisions) and [15-local-ai.md](15-local-ai.md) (question
answering).

## The problem

The *Amateur Radio Operator* collection is thin. It holds 47 CFR Parts 95
and 97, the Amateur Radio and Electronics Stack Exchange archives, and
general reference. Most of what operators reach for is missing: antenna
references, field guides such as the NIFOG, question pools, digital-mode
software manuals, and the manuals for their own radios.

Two things limit growth:

- **Few sources can be redistributed.** Most radio material is copyrighted:
  ARRL books, manufacturers' manuals, forum posts and YouTube. Arcana can
  only offer in its catalog what may legally be copied. Everything else has
  to come from the operator.
- **Arcana's document search is not yet good enough for manuals.**
  Measurements are below. A 300-page manual is indexed as one search
  record, so a match says "this manual" rather than "page 47". Scanned PDFs
  are indexed as empty without telling anyone, and the PDF text extractor
  runs some words together.

The plan has five parts, cheapest first:

1. **Add archives Kiwix already publishes** to the collection. This needs
   no code.
2. **Curate redistributable radio documents** as catalog entries:
   public-domain government manuals, the question pools and openly
   licensed books.
3. **Add a folder**: import a whole folder tree of PDFs (radio manuals,
   frequency lists, club documents) in one step, keeping its subfolders.
4. **Search by page**: make manuals searchable by page, fix the
   extraction problems, detect scanned PDFs and offer OCR.
5. **Ask questions of your manuals** in the NotebookLM style: semantic
   search first, then optional cited answers from a local model
   (doc 15).

Groups.io, YouTube and forums are covered under [Sources the operator
brings](#sources-the-operator-brings).

## 1. Archives Kiwix already publishes

Checked against the live Kiwix OPDS catalog on 2026-11-23 (3,682
entries). Arcana's Kiwix provider already offers each of these, so adding
them to the *Amateur Radio Operator* collection only changes
`internal/catalog/bundled/presets.json`.

| Archive | Size | Why | Suggested priority |
|---|---|---|---|
| `libretexts.org_en_eng` (Engineering LibreTexts, 2025-01) | 678 MB | Open engineering textbooks: circuits, electronics, electromagnetics | 2 |
| `dsp.stackexchange.com_en_all` (Signal Processing Q&A) | 379 MB | SDR, filtering, modulation | 3 |
| `physics.stackexchange.com_en_all` | 1.8 GB | Electromagnetism and propagation questions | 3 |
| `engineering.stackexchange.com_en_all` | 258 MB | Towers, masts, mechanical questions | 3 |
| `wikibooks_en_all` (`nopic` 3.5 GB, `maxi` 4.7 GB) | 3.5–4.7 GB | Open textbooks, including electronics and communication systems | 3 |
| `armypubs_en_all` (Army Publishing Directorate, 2024-12) | 8.2 GB | US Army publications: signal and radio-operation doctrine | 3 (large) |
| `solar.lowtechmagazine.com_mul_all` | 700 MB | Off-grid power for stations | 3 |

Not found: Kiwix has **no** archive of ham radio video, forums other than
Stack Exchange, or ham radio PDF collections. The `zimgit-*` prepper
collections cover medicine, water, food, knots and post-disaster topics,
not radio.

Which radio topics `armypubs_en_all` and `libretexts.org_en_eng` actually
contain could not be checked from here: Kiwix's browse server shows an
access-confirmation page to automated requests. Check by opening them in
Arcana before adding them to the collection.

## 2. Radio documents to curate

Publish these as a **signed radio feed** (Ed25519, 0.6.0) maintained
separately from releases:
- Curation can then follow new editions without an application update.
- Each entry is hash-pinned and carries its license and its
  `lastVerified` date.

Several publishers refuse automated downloads: FEMA, Ready.gov, cisa.gov,
aprs.org, wsjt.sourceforge.io and winlink.org did so during this
research.
- For redistributable items, the project can host a mirror, such as a
  release asset or an archive.org item, next to the publisher's URL.
- Free-download-only items can only ever be fetched from the publisher.

### 2a. Public-domain government and military documents

Checked 2026-11-23. US federal works are public domain under 17 U.S.C.
§105. The quoted markings are the documents' own. All are born-digital
unless noted.

| Document | Edition | Evidence | Size | Notes |
|---|---|---|---|---|
| **NIFOG**, National Interoperability Field Operations Guide (CISA) | v2.02, January 2025 | CISA work; no rights statement | 6.7 MB, 192 pp | Interoperability channels, ham bands, tables. One figure (an RJ-45 pinout) is "© 2021 ShowMeCables", so mention it in the entry's notes. The file's URL contains a date folder that changes with each version; landing page cisa.gov/safecom/field-operations-guides |
| **ATP 6-02.53**, *Techniques for Tactical Radios and Retransmission* (US Army) | 31 Jul 2025, superseding 2020 | "Approved for public release; distribution is unlimited." | 6.3 MB, 206 pp | **Antennas:** App. H "Antenna Selection and Techniques" (field-expedient antennas, field antenna repair) and App. E (HF antennas, improvised vertical half-wave). `armypubs.army.mil` works without `www`. The `armypubs_en_all` archive (2024-12) holds the 2020 edition at best |
| **NCVEC question pools**: Technician 2026–2030, General 2023–2027, Extra 2024–2028 | Technician in effect from 1 Jul 2026; General and Extra with their Feb 2026 errata | NCVEC's release pages: "hereby releases … into the public domain". The statement is on the web pages, not in the PDFs, so quote it in the entry | 0.5–1 MB each, plus figure files | Each pool has its own figures (PDF, JPG; Extra also SVG). File names change with every erratum, so the feed needs updating. The General pool expires 30 Jun 2027 |
| **NEETS** Modules 10 (*Wave Propagation, Transmission Lines, and Antennas*), 12 (*Modulation Principles*) and 17 (*Radio-Frequency Communications Principles*) (US Navy) | 1998 editions | "DISTRIBUTION STATEMENT A: Approved for public release; distribution is unlimited." | Mod 10 2.5 MB; Mod 17 5.9 MB | **Use the 1998 editions**: the 2012–2013 revisions (14182A, 14189A) contain copyrighted material restricted to DoD sites. No Navy host survives; archive.org `NEETSModules`. A project mirror is needed |
| **USCG Radiotelephone Handbook**, CGTTP 6-01.1B | 30 Jul 2018 | "Internet release authorized"; hosted by CISA | 0.7 MB, 54 pp | Voice procedure, prowords and phonetics. **Use it instead of ACP 125**, which it cites |
| **AUXFOG**, Auxiliary Communications Field Operations Guide (CISA) | v1.1, June 2016 | DHS work | 3.4 MB, 156 pp | No newer version, and no longer on the current SAFECOM page. It gets the automatic *outdated* warning |
| CISA AUXCOMM Position Task Book; Auxiliary Emergency Communications course units | 2020; 2017 | CISA works | 0.5 MB; 1.2 MB per unit | Skip the COMT Pre-Course Study Guide, whose distribution is restricted |
| **FEMA ICS forms** 205, 205A, 213, 214 (and 217A) | NIMS v3 / v3.1 | FEMA works | 25–420 KB each; booklet 3 MB | Fillable. Pin the URL after redirection; it contains spaces and commas. **ICS 309 is not a FEMA form**: only local ARES and RACES versions exist |
| **NTIA Redbook** (*Manual of Regulations and Procedures for Federal Radio Frequency Management*) | January 2023 revisions to the January 2021 edition, the version that 47 CFR 300.1 incorporates | NTIA work | 28.8 MB | redbook.ntia.gov also offers a June 2025 HTML edition without a PDF. Choose the legally referenced PDF and state the edition |
| **US Frequency Allocations chart** (NTIA) | Sept 2025 | NTIA work | 0.3 MB, one 27 × 39 in page | Its text extracts as garbage (Type 3 fonts), so treat it as a chart, not a searchable document |
| **TM 11-666**, *Antennas and Radio Propagation* (US Army) | 9 Feb 1953 | Army work; no restriction marking | 21.9 MB (OCR'd text PDF) | Scanned. Use archive.org's OCR derivative, as measured above. Obsolete but still sound on fundamentals, so it gets the automatic *outdated* warning |
| **SKYWARN Weather Spotter's Field Guide** (NWS) | June 2011 | NOAA repository rights field: "Public Domain" | 27.9 MB, 72 pp | "SKYWARN® is a registered trademark of NOAA". Text layer not yet checked |

**Excluded or unresolved:**
- **FM 24-18**, *Tactical Single-Channel Radio Communications
  Techniques* (1987). Marked "Distribution authorized to U.S. Government
  agencies and their contractors", never shown to be released. Its topic
  is now covered by ATP 6-02.53.
- **ACP 125**, radiotelephone procedures. "For official purposes only";
  no public release. Use CGTTP 6-01.1B instead.
- **NEC-2 manual** (Burke and Poggio, 1981). DTIC lists it as approved for
  public release, but Lawrence Livermore wrote it as a contractor, so
  §105 does not apply automatically. Confirm with DTIC before curating.

**A first radio feed:** NIFOG, ATP 6-02.53, the three NCVEC pools, NEETS
10/12/17, CGTTP 6-01.1B and the FEMA ICS forms. Together they are about
25 MB. They cover field reference, antennas, exam study, voice
procedure, message forms and theory, and every one can be shared on the
LAN and copied between libraries.

### 2b. Openly licensed books

Checked 2026-11-23 on the publishers' pages. **R** means redistributable;
**NC** means non-commercial only, which Arcana may offer as
`redistribution: noncommercial`.

| Work | Edition | License | Size | Radio content |
|---|---|---|---|---|
| Ellingson, *Electromagnetics* Vol. 2 (Virginia Tech Publishing, doi:10.21061/electromagnetics-vol-2) | 2020-01 | R: CC BY-SA 4.0 | 5.6 MB | **Antennas** (ch. 10), radiation (ch. 9), transmission lines (ch. 7) |
| Ellingson, *Electromagnetics* Vol. 1 (doi:10.21061/electromagnetics-vol-1) | 2018-08, with errata | R: CC BY-SA 4.0 | 10.8 MB | Transmission lines, waves, fundamentals |
| Kuphaldt, *Lessons in Electric Circuits* I–VI (ibiblio.org/kuphaldt/electricCircuits/) | 2023-02-03 | R: CC BY 4.0 (stated in appendix 3) | ≈22 MB for six PDFs | Vol. II ch. 14 covers transmission lines; circuits, semiconductors, test equipment |
| Kuphaldt, *Modular Electronics Learning* (ModEL) modules | Rolling; RF modules updated 2026 | R: CC BY 4.0 | 0.9–3.5 MB each | Modules on antennas, antenna systems, transmission lines, Smith chart, link budget, modulation, mixers, HF amplifiers, radio. Take individual modules; the full archive is 3.6 GB |
| Ellingson, *Radio Systems Engineering*, revised 1st ed. (doi:10.21061/radiosystemsengineering-revised1st) | 2023-12 | NC: CC BY-NC 4.0 | 14.8 MB (7.6 MB low resolution) | Antennas, propagation, noise, link analysis |
| Steer, *Microwave and RF Design* (NC State, five volumes) | 3rd ed., 2019 | NC: CC BY-NC (Vol. 1; check each volume) | — | Vol. 1 ch. 4, "Antennas and the RF link" |

- LibreTexts hosts both Ellingson volumes ("Electromagnetics I/II
  (Ellingson)", CC BY-SA 4.0), so `libretexts.org_en_eng` probably
  already contains them. Confirm before adding the PDFs as well.
- No other plain CC BY or CC BY-SA antenna textbook was found. Most RF
  texts are non-commercial or all rights reserved.
- **Wikibooks** is CC BY-SA and is included in `wikibooks_en_all`, but its
  radio books are thin:
  - *Amateur Radio Manual*: about 13 of 40 topics written; propagation,
    feed lines and antennas are empty.
  - *Communication Systems*: early stage, last edited 2019.
  - *Electronics*: little on RF.
- **Project Gutenberg** has US public-domain radio books, mostly from the
  1910s and 1920s and historical. Each gets the automatic *outdated*
  warning:
  - #6935, Collins, *The Radio Amateur's Hand Book* (illustrated)
  - #63345, Morgan, *Lessons in Wireless Telegraphy*
  - #64174, Morgan, *Wireless Telegraph Construction for Amateurs*
  - #66702, Morgan, *Wireless Telegraphy and Telephony Simply Explained*
  - #38526, Fleming, *Hertzian Wave Wireless Telegraphy*
  - #30688, Mills, *Letters of a Radio-Engineer to His Son*

  Gutenberg #66 (Joly) is a **copyrighted** eBook and must be excluded.

  The Gutenberg provider imports by bookshelf, and no shelf maps to radio.
  Add a subject match ("Radio", "Wireless telegraphy") that maps to
  `radio-communications`.

### 2c. Software manuals and specifications

| Document | Version | License | Size | Notes |
|---|---|---|---|---|
| WSJT-X User Guide | 2.7.0 in the repository; check whether a 3.0 guide exists | R: GPLv3; the guide says it may be redistributed under the GPL | — | One HTML or PDF file; AsciiDoc source is in the repository. wsjt.sourceforge.io refuses automated requests |
| JS8Call User Guide (JS8Call-improved) | Guide v4.0, 2026-09-06 | R: CC BY-SA ("© 2020 Jordan Sherer") | 2.6 MB | Markdown source is in the repository |
| fldigi help | 4.2.13 (2026-07-29) | R: GPLv3 program; help licensed LGPL v2+ | 14 MB PDF | w1hkj.org/files/fldigi/fldigi_help.pdf |
| flrig help | 2.0.12 (2026-07-30) | R: GPL | 3.9 MB | w1hkj.org/files/flrig/flrig-help.pdf |
| flmsg help | 4.0.24 (2025-06-23) | R: GPLv3 | 2.5 MB | Forms for ICS and Radiogram messages |
| Dire Wolf User Guide | "Version 1.8, October 2025" | R by the repository's GPLv2 (the PDF has no notice of its own) | 2.2 MB | `wb2osz/direwolf/doc/User-Guide.pdf`. Debian strips these PDFs from its source package, which shows the doubt; ask the author to confirm. The separate `direwolf-doc` repository has no licence (F) |
| Hamlib manuals | 4.7.2 (2026-06-21) | R: LGPL-2.1+ for the library, GPL for the utilities | Multi-page HTML | Man pages are in the repository |
| APRS Protocol Reference 1.0.1 (TAPR) | 2000-08-29 | NC: "may be copied for non-commercial purposes only", keeping the copyright and trademark statements in full | 3.2 MB | files.tapr.org/…/aprs101/APRS101.pdf. A file of the same name in `wb2osz/aprsspec` is a two-page *obsolete* notice, not the specification. Pin the TAPR copy |
| APRS 1.2, WB2OSZ compilation | Draft C, Nov 2024 | NC: same notice | 2.2 MB | Unofficial |

### 2d. Free to download, not to redistribute

Arcana can offer these as catalog entries whose only mirror is the
**publisher's own URL**, with `redistribution: restricted` (ARRL, IARU,
ITU, TAPR) or `noncommercial` (ISED). This means:
- the person downloads the file from the publisher, as a browser would;
- `arcana export` already warns for anything not `permitted`;
- the Library shows the terms.

When a publisher replaces a file, the hash no longer matches. Arcana
should then say "the publisher has changed this document" rather than
"corrupted", and the feed is re-verified.

| Document | Terms found | Notes |
|---|---|---|
| ARRL band chart (rev. 1/16/2026), ARES Plan, ARES Task Book, ARES Manual (2015), ARES Field Resources Manual | "Reproduction … without written permission is strictly prohibited"; "Additional permission is required to use this material in any training or product that will be redistributed" | ARRL allows only W1AW bulletins, *ARRL Audio News* and *The ARRL Letter* to be redistributed. Worth asking ARRL whether a publisher-link entry is acceptable to them |
| IARU Region 1, 2 and 3 band plans | Footer only: "may be subject to copyright"; no licence | Small PDFs (150–200 KB) |
| ITU Radio Regulations, 2024 edition | "Posted free of charge for personal use"; no reproduction without permission | 69.6 MB zip |
| AX.25 v2.2 (TAPR, 1998) | Copyright TAPR and ARRL; no permission statement | 928 KB |
| ISED (Canada): RIC-3, RBR-4, Basic and Advanced question banks | May be reproduced "for non-commercial purposes … without charge or further permission", stating that it is a copy, with the title and source | `noncommercial`; jurisdiction CA |
| CHIRP documentation, Pat wiki | No licence (CHIRP: "Copyright 2026 CHIRP Software LLC") | Multi-page wikis. A ZIM request would need permission |
| VARA, Winlink Express | No licence text found | Do not curate. Files hosted on mega.nz cannot be pinned reliably |

This is also where manufacturers' radio manuals belong. Yaesu, Icom and
Kenwood publish them free, under copyright. A community feed could list
publisher-link entries for popular radios, but manufacturers' download
pages change often and some require accepting terms. **Add a folder**
remains the dependable route for the manuals someone already has.

## Sources the operator brings

Material that is copyrighted, or whose terms forbid redistribution, can
still be useful to the person who has legitimate access to it. Arcana
fetches or imports it for that person only:
- It is stored with their documents and available only on the host
  computer.
- It is never offered in a catalog or shared on the LAN.
- It is never copied to another library unless they export their own
  documents.

### Groups.io: feasible with an API key

Checked 2026-11-23, without logging in.

- **API.** The API is "open to anyone who uses Groups.io", not only to
  paid groups.
  - API keys (groups.io/settings/apikeys) have been the recommended method
    since 2025-09-10.
  - Keys "bypass two-factor authentication requirements" and need no CSRF
    token.
  - Every call is limited by the group's own visibility settings for that
    member.
- **Endpoints a member can use:**

  | What | Endpoint | Needs the group to allow |
  |---|---|---|
  | Whole archive in one call: a zip containing one mbox file. Limited to one call per person per group every 24 hours | `downloadarchives` | `download_archives`, which the owner can turn off |
  | Messages, page by page (`getmessage` returns the raw MIME message) | `gettopics`, `getmessages`, `getmessage` | `archives_visible` |
  | Files section: manuals, mods, software (signed download URLs) | `getfiledirectory`, `getfile` | `files_visible` |
  | Wiki | `getwikipages`, `getwikipageset` | `wiki_visible` |

- **Terms of Service** (last revised May 2017):
  - They forbid "data mining, robots, scraping or similar data gathering
    or extraction methods", without mentioning the API.
  - Using the official API at a modest rate, only for groups the person
    belongs to, is the defensible reading, but it is a reading. **Ask
    Groups.io to confirm before shipping.**
  - The API is documented as "under active development and subject to
    change".

**Design.**
- *Settings → Connected accounts → Groups.io*, on the host computer
  only.
- **The person pastes an API key, never a password.**
  - The key is kept in memory for the sync by default.
  - "Remember on this drive" is a separate choice. Its warning says that
    anyone holding the drive could use the key, which bypasses
    two-factor authentication, and shows how to revoke it.
- Arcana lists the person's groups and they tick the ones to keep. For
  each group:
  1. Use `downloadarchives` when the group allows it. Otherwise page
     through the messages, backing off on HTTP 429, then fetch only
     newer messages on each sync.
  2. Store messages as mbox under
     `user/documents/Groups.io/<group>/`, indexed by the email import
     below.
  3. Download the Files section into the same folder and index it as in
     *Add a folder*. Radio groups keep their manuals and mods here.
  4. Save wiki pages as HTML.
- Every item records the group, the date it was retrieved and "personal
  copy". Message authors keep their copyright.

### Email archives: a route without credentials

Add `.mbox` and `.eml` to the document formats.
- Each message is one search record (see section 4), with sender, date,
  subject and thread.
- PDF and text attachments become documents of their own.

This one importer covers four sources:
- the Groups.io archives above;
- groups the person receives by email (Groups.io's default delivery is
  one email per message) and exports from their mail program;
- public mailing-list archives that publish monthly mbox files, such as
  the Elecraft and Boatanchors lists at `mailman.qth.net/pipermail/`;
- club mailing lists.

Email gives no history from before the person joined, and no Files or
wiki, which is why the Groups.io API is still worth doing.

### YouTube: no downloader; request openZIM archives instead

- **Arcana will not download from YouTube.**
  - YouTube's Terms of Service forbid downloading "except as expressly
    authorized by the Service".
  - Its API Developer Policies (updated 2026-11-06) say apps must not
    "download, … cache, or store copies of YouTube audiovisual content
    … [or] make content available for offline playback".
  - YouTube Premium downloads play only inside YouTube's own apps, while
    signed in to the same account, and need reconnecting every 29 days.
    They cannot be exported.
- **The legitimate route is openZIM.** openZIM's `youtube2zim` turns a
  channel or playlist into a ZIM archive. openZIM builds these on request
  through `github.com/openzim/zim-requests`, but only with the creator's
  permission, recorded in a Kiwix ticket. Corey Schafer's programming
  tutorials (CC BY-NC, with permission) are a live example.
  - Kiwix's library already has 138 archives made from YouTube; none
    are about radio.
  - **Action for the project or a ham club:** ask the makers of
    educational ham radio channels for permission and file the requests.
    Antenna building, licence-exam courses and emcomm procedures are the
    priority.
  - Once Kiwix publishes such an archive, Arcana's Kiwix provider offers
    it with no further work. Add it to the *Amateur Radio Operator*
    collection.
- **Other video and audio that can be used:**
  - **FOSDEM** radio and SDR talks, licensed CC BY 2.0 Belgium. These can
    be curated catalog entries.
  - **US government training films** on archive.org: the OSS SSTR-1
    training film (Public Domain Mark), and Signal Corps film TF 1-474,
    *Creation and Behavior of Radio Waves* (1942). Check each item; some
    re-uploads carry CC BY-NC-ND.
  - **Podcasts.** Ham podcasts publish mp3 episodes in their RSS feeds,
    and downloading them for personal listening is what feeds are for.
    Examples: QSO Today, Ham Radio Workbench, ICQ Podcast, Amateur Radio
    Newsline, ARRL Audio News.
    - A *podcast subscription* would download new episodes on the host
      computer.
    - Feeds list only recent episodes, so older ones cannot be fetched
      this way.
    - Episodes stay personal copies.
    - Later, speech-to-text (a whisper.cpp-class sidecar, as in doc 15)
      could make them searchable.
  - **The operator's own video files** can come in through *Add a
    folder*. They are listed and played, but cannot be searched without
    a transcript.

### Forums

| Source | Finding | Route |
|---|---|---|
| Amateur Radio Stack Exchange | Kiwix keeps rebuilding its archive (`ham.stackexchange.com_en_all_2026-10`, 74 MB). Its build schedule is twice a year, with extra builds in between | Already in the collection |
| QRZ.com forums | The terms forbid robots and automated copying, but allow downloading "one copy of a reasonable number of pages … for your own personal, non-commercial use". Posters keep their copyright | Save the threads you value as PDF or HTML, then *Add a folder* |
| eHam.net | The terms (2026-11-21) forbid scraping or bulk downloading "by automated means without our written permission" | As for QRZ |
| Reddit r/amateurradio | The User Agreement forbids scraping without consent. The Data API terms require deleting stored content. New API access has needed approval since 2025-11. There is no sanctioned data dump | Not pursued |
| Wikibooks *Amateur Radio Manual* | CC BY-SA 4.0 | Included in `wikibooks_en_all` |
| OARC wiki (wiki.oarc.uk) | CC BY-SA 4.0 | Its licence allows an openZIM request |
| Signal Identification Wiki, OpenWebRX wiki | No licence stated | Ask the maintainers first |

## 3. Add a folder

**Goal.** Point Arcana at a folder such as `D:\Radio\Manuals`. It finds
every supported document in it and its subfolders and adds them to the
library, keeping the folder structure. The result is searchable with
everything else.

**Today.** *My documents* accepts one file at a time through the browser.
Files land in one flat folder, `user/documents/`. `arcana import --from`
reads only Arcana exports.

### Behaviour

- **Start:** *Library → My documents → Add a folder…* on the computer
  running Arcana. On the command line:
  `arcana add-documents DIR [--into NAME] [--link]`.
- **Preview before copying.** Arcana walks the folder, then shows:
  - how many documents it found, by type, and their total size;
  - how many are duplicates of files already in the library (same
    SHA-256);
  - how many it will skip, and why: unsupported type, too large,
    unreadable, or over FAT32's 4 GiB limit;
  - the free space left afterwards, against the reserve.
- **Copy by default.** Files are copied into
  `user/documents/<folder name>/…` with their subfolders. A library must
  keep working when the drive moves to another computer, so it cannot
  depend on paths outside the drive. Identical files are stored once.
- **Linked folders (optional).** A large collection elsewhere on the same
  computer can instead be indexed where it is, without copying. Arcana
  records the folder in `user/linked-folders.json` and labels it *only on
  this computer*. When the folder is missing, for example because the
  drive moved, its documents are shown as unavailable rather than deleted
  from the index.
- **Rescan** adds new files and updates changed ones. Nothing is removed
  without a confirmation.
- **Subfolders become labels.** For example, `Radios › Yaesu › FT-991A`.
  Search can be limited to a label, and the label's documents can be read
  together.
- **Archives.** `.zip` files are common in Groups.io file sections and
  mods collections. They are opened, and the supported documents inside
  are added, with the same limits as everything else: no path escapes, a
  size cap and a file-count cap.
- **Private by default.** As with all of the operator's documents, they
  are available only on the host computer, not to LAN visitors.
  Manufacturers' manuals are usually copyrighted. Sharing a label on the
  LAN can be a deliberate per-label choice later.

### Safety and limits

- The folder walk runs only on the host computer (`localOnly`). It reads
  the host's file system, so neither a LAN visitor nor remote
  administration can start it.
- It does not follow symbolic links or junctions, skips hidden and system
  folders, and limits depth (32) and file count (100,000 per run).
- Each path component is checked with `fsutil.ValidateFileName`. Names
  that are invalid on another file system (for example `:` on exFAT) are
  renamed, and the renaming is reported.
- It runs as a background task (`internal/tasks`) with progress and
  cancel. Indexing starts when copying ends.

### What changes in the code

The flat `user/documents/` folder is assumed in four places:
- `app.UserDocuments`, `SaveUserDocument`, `DeleteUserDocument` and
  `UserDocumentPath` in `internal/app/userfiles.go`;
- document listing for the index, in the same file;
- `transfer` export and import (`internal/transfer/transfer.go`);
- `docs.UserDocURL`.

Each needs to accept a relative path whose components are validated
separately. Document keys become `user:<relative path>`. Existing flat
libraries keep working unchanged.

## 4. Search by page

### Measurements (2026-11-23)

Arcana's extractor (`ledongthuc/pdf`, `internal/docs/extract.go`) was run
on four public-domain radio PDFs and compared with Poppler's `pdftotext`
as a reference. *Term hits* counts the occurrences of 14 radio terms
(antenna, station, operator, frequency, license, …) that a search could
match.

| PDF | Pages | Time | Words extracted | Term hits vs. `pdftotext` |
|---|---|---|---|---|
| 47 CFR Part 97 (govinfo, born digital) | 38 | 59 ms | 23,982 | **87 %**; vocabulary 93 % |
| US Army IS1143 *Radio Wave Propagation and Antennas* (1986, text PDF) | 177 | 131 ms | 18,392 | 98 % |
| TM 11-666 *Antennas and Radio Propagation* (1953), archive.org OCR copy | 231 | 763 ms | 107,358 | 97 % |
| TM 11-666, archive.org image-only copy | 231 | 39 ms | **0** | — (no text layer) |

Findings:

1. **Speed is fine.** A 231-page manual takes under a second.
2. **Some words run together.** The extractor joins adjacent text runs
   without a space. This produces "97.15Station antenna structures",
   "OPERATORMOS" and "COURSEANDU.S.". FTS5 then indexes the token
   `15station`, so a search for *station* misses the heading. Rule texts
   lose the most (13 % of hits), and section headings are exactly what
   people search for.
3. **Scanned PDFs fail silently.** The image-only copy indexed "successfully"
   with no text. It is listed in the library but never matches a search,
   and nothing says why. Many radio manuals are scans: military TMs, older
   radio manuals and mods.
4. **A match points to the whole document.** Each document is one FTS5 row
   of up to 4 MB of text, so a hit opens the start of the extracted text
   with no page number.

### Changes

- **Index pages, not documents.**
  - PDFs are indexed one row per page. EPUBs are indexed one row per
    chapter or heading section, and other text in sections of about 400
    words.
  - Each row carries the document key, page number and nearest heading.
    The heading comes from the PDF outline (bookmarks), when the PDF has
    one.
  - Results are grouped by document, with each document's best pages
    shown, for example: *FT-991A Operating Manual · p. 47 · Menu mode 031
    CAT RATE … set the **CAT** rate to match …*.
- **Open at the page.**
  - Browsers' built-in PDF viewers accept `#page=N`, so *Open original*
    can go straight to the matching page.
  - The text view gets page markers and scrolls to the page, with the
    search words marked.
- **Fix word joining.** A prototype run against the same PDFs (not
  committed) made two changes:
  - **Spacing.** Rebuild each page from glyph positions
    (`Page.Content()`). Insert a space when the gap to the next glyph is
    wider than 0.15 × the font size, and a line break when the baseline
    moves by more than half the font size.
  - **Dehyphenation.** Rejoin `letter-⏎lowercase`. Part 97 is set in
    narrow columns, so "control op-⏎erator" was indexed as `op` and
    `erator`.

  Results, as term hits relative to `pdftotext`:

  | PDF | Today | + spacing | + spacing and dehyphenation |
  |---|---|---|---|
  | 47 CFR Part 97 | 87.5 % | 90.0 % | **99.7 %** |
  | Army IS1143 (1986) | 97.6 % | 100 % | 100 % |
  | TM 11-666 (OCR copy) | 96.9 % | 96.9 % | 96.9 % |

  - **Cost.** Per-glyph extraction is slower: 4.3 s instead of 0.13 s for
    the 177-page manual. That is acceptable for background indexing, but
    the 60 s extraction limit should scale with the page count.
  - **Not adopted.** A WebAssembly build of PDFium (for example
    `klippa-app/go-pdfium` on wazero) would keep Arcana free of cgo with
    better text extraction, but it is a larger dependency. Evaluate it only
    if the prototype's approach falls short on real manuals.
- **Detect scanned PDFs.**
  - A PDF averaging fewer than about 50 characters per page is marked
    *scanned: text search cannot see inside this document* in *My
    documents* and the library, with what to do about it.
- **Radio synonyms** (optional, cheap).
  - A small curated table expands queries, for example:
    SWR ↔ VSWR; CTCSS ↔ PL tone ↔ tone squelch; offset ↔ shift; CAT ↔
    CI-V; tuner ↔ ATU; feed line ↔ coax.
  - The results say what was added ("also searched: CTCSS").
  - This matters because each manufacturer names the same feature
    differently.

### OCR for scanned documents

- **First, documentation.** Explain how to run
  [OCRmyPDF](https://ocrmypdf.readthedocs.io/) (MPL-2.0, which uses
  Tesseract, Apache-2.0) on a scanned PDF before adding it. It adds a text
  layer and leaves the page images unchanged.
- **Later, built in.** Tesseract becomes an optional sidecar resource,
  installed like kiwix-serve, with its license and checksum.
  - Arcana runs it in the background on documents marked *scanned*.
  - It stores the text beside the index (`index/ocr/<sha256>.txt`) and
    never modifies the operator's file.
  - Tesseract typically takes a second or more per page on a laptop CPU,
    so a 231-page manual takes several minutes and a Raspberry Pi much
    longer. That is acceptable for background work if progress is shown.
- For catalog entries from archive.org, **choose the `_text.pdf` (OCR)
  derivative** where one exists. In the test above it indexed at 97 %,
  while the image-only original indexed nothing.

## 5. Ask questions of your manuals (NotebookLM-style)

What NotebookLM offers is answers drawn only from sources the person chose,
with each claim linked to the passage it came from. Three stages get Arcana
there. Each is useful by itself, and none needs the next.

| Stage | What it adds | Cost |
|---|---|---|
| **A. Search by page** (section 4) | "Where in my manuals does it say X", answered by page | Pure Go; no new dependency |
| **B. Semantic search** | Finds passages by meaning, not only by words. For example, *make the radio talk to my computer* finds *CAT control*, and *my SWR is high* finds the antenna-tuning section | A small embedding model (tens to a few hundred MB) run by a sidecar; vectors stored beside the index |
| **C. Cited answers** | A short answer written only from the retrieved pages, with numbered citations that open the page. If the pages do not answer the question, it says "Not found in your library" | A local language model sidecar (doc 15); slow on small hardware |

Design points:

- **Notebooks.** A notebook is a saved set of sources: for example *My
  station* (two radio manuals, a tuner manual, Part 97 and the NIFOG) or
  *Technician study* (the question pool and Part 97).
  - Search and questions can be limited to one notebook. `docs.SearchIn`
    already limits a search to one source; this extends it to a set.
  - Notebooks are plain JSON in `user/notebooks/`, like presets.
- **Hybrid retrieval.** Use BM25 (FTS5) and vector similarity together,
  merged by reciprocal-rank fusion. Exact terms such as menu numbers,
  frequencies and call signs need keyword search; meaning needs vectors.
- **No vector database.** Pages are embedded once, in the background, and
  stored as quantized vectors in a sidecar file. A brute-force scan is
  fast enough at library scale: 100,000 pages × 384 dimensions × 1 byte
  is about 38 MB, and scanning it takes tens of milliseconds in Go. The
  index stays a rebuildable cache (ADR-0004).
- **Models as sidecars**, as in doc 15: a llama.cpp-class server on
  loopback, installed as a catalog resource with its license and checksum,
  and never downloaded without being asked. Only models whose licenses
  permit redistribution can be catalog entries; MIT and Apache-2.0 are
  the clean cases.
- **Warnings travel with the citations.** An answer built from Part 97
  shows the regulatory notice, and an answer from a medical source shows
  the medical notice, as the integration API already does.
- **Other tools.** The integration API (`search:read`,
  `documents:read`) already lets an external question-answering tool use
  Arcana as its retriever, without changing Arcana.

## Order of work

| Step | Size | Depends on |
|---|---|---|
| Add Kiwix archives to the *Amateur Radio Operator* collection | Small: `presets.json` | — |
| Fix PDF word joining; detect scanned PDFs | Small | — |
| Search by page, open at the page, outline headings | Medium | — |
| Add a folder (copy, keep structure, duplicates, preview) | Medium | Subfolders in `user/documents` |
| Curated government and open documents (catalog or signed radio feed) | Medium, mostly curation | Mirrors for sites that block automated downloads |
| Email archive import (`.mbox`, `.eml`) | Small to medium | Search by page (one row per message) |
| Groups.io connector (API key; archive, Files, wiki) | Medium | Email import; *Add a folder*; confirmation from Groups.io |
| openZIM requests for ham radio channels and the OARC wiki | No code; needs outreach to creators | — |
| Podcast subscriptions | Small to medium | — |
| OCR documentation, then optional Tesseract sidecar | Small, then medium | Scanned-PDF detection |
| Semantic search (stage B) | Large | Search by page; sidecar supervision exists for kiwix-serve |
| Cited answers (stage C) | Large | Stage B; evaluation set (doc 15) |

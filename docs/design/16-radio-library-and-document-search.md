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

## 2. Redistributable radio documents to curate

*Research in progress:* public-domain government and military manuals,
question pools, openly licensed books and specifications, and
open-source software manuals, each with its license evidence.

## Sources the operator brings

*Research in progress:* Groups.io, YouTube and forums.

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
| OCR documentation, then optional Tesseract sidecar | Small, then medium | Scanned-PDF detection |
| Semantic search (stage B) | Large | Search by page; sidecar supervision exists for kiwix-serve |
| Cited answers (stage C) | Large | Stage B; evaluation set (doc 15) |

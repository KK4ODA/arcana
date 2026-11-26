# ADR-0028: Home care and herbs built into the medicines reference

- Status: Accepted (0.14.0)
- Date: 2026-11-26

## Context
A comparison with Project NOMAD v1.35.2 on 2026-11-26, running both
programs side by side, found that every Kiwix archive and map region NOMAD
offers is also in Arcana's catalog. Its medicines reference offers two
lists Arcana lacked:

- 20 home-care measures (honey for cough, oral rehydration, cool water on
  burns, hygiene against pinworm and others), each summarizing a CDC,
  MedlinePlus or FDA page;
- 18 herbs and supplements, each summarizing an NCCIH "Herbs at a Glance"
  page, with what the evidence shows and the cautions.

Each entry names the page it summarizes, and every one of those 36 pages
was online on 2026-11-26. NOMAD's lists also point to 30 everyday problems.
25 of them already had a situation in Arcana. Common cold, dry skin,
earache, dehydration and pinworm had none.

The FDA label reference (ADR-0022) is a 1.8 GB download. Home care for
diarrhea or a burn matters most on a drive that may never have had it.

## Decision
- **Built in, not downloaded.** NOMAD's `home_remedies.json` and
  `natural_remedies.json` (commit 8dcb8ea, Apache-2.0) are embedded in
  Arcana as `internal/drugs/remedies.json`, about 50 KB. NOMAD's condition
  names are replaced by Arcana's situation slugs, and the text is otherwise
  unchanged. They update with Arcana, not with the catalog.
- **Checked when loaded.** Every entry needs a name, uses, evidence and
  cautions, at least one known situation, and an HTTPS source on cdc.gov,
  medlineplus.gov, fda.gov or nccih.nih.gov. A failing entry stops the
  tests, not the program.
- **Five more situations.** "Common cold or flu", "Dry, chapped or cracked
  skin", "Earwax, water in the ear or earache", "Dehydration or heat
  cramps" and "Pinworm", with wording taken from current Drug Facts panels
  and checked against openFDA. Non-prescription ear products treat earwax
  or water in the ear; labels that name earache are almost all homeopathic,
  which situations leave out. Oral rehydration solutions are not drugs, so
  dehydration finds salt tablets among the labels and oral rehydration
  among the remedies. The bare word "dehydration" is not a search term:
  stemmed, it matched moisturizers for "dehydrated skin" and an insulin
  label in the 2026-11-20 export.
- **Shown with the labels.** "By situation" lists each situation's home
  care and herbs after its medicines. A "Home care and herbs" tab lists all
  38. Each remedy page puts the cautions first, then uses, how it is used
  and what the evidence shows, with its source page and NOMAD's
  attribution. Herbs carry a badge saying they are not approved as
  medicines.
- **Medicines is always offered.** Without the label reference, the tab
  shows the remedies by situation and says how to get the labels. The
  acknowledgement now mentions herbs and supplements.
- **Readable by every client**, like the labels: `GET /api/drugs/remedies`
  (optionally `?s=` with up to five situations) and
  `GET /api/drugs/remedies/{slug}`.

## Consequences
- The remedies are there on every drive, read-only ones included, from the
  first start.
- Arcana now carries health text it did not write. Its accuracy rests on
  NOMAD's summaries of government pages; the source page is named on every
  remedy so a reader can check it, and the summaries are dated (2026-06-10).
- An update to the remedies needs an Arcana release. Both of NOMAD's lists
  are still at the version they were first published with, 2026-06-10.

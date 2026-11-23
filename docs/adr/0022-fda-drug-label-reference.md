# ADR-0022: FDA drug label reference built on the user's computer

- Status: Accepted (unreleased)
- Date: 2026-11-23

## Context
Project NOMAD added an offline "Drug Reference" from the FDA's drug labels
(01-landscape.md): search by medicine, look up by situation, compare two
labels' warnings. Arcana's medical collections had WikiMed and other
encyclopedic archives but nothing that answers "what is in this box, how
much do I take, and when must I stop".

openFDA publishes every current label as 14 zip files (1.79 GB, 265 009
labels, export of 2026-11-20), public domain under CC0, with no
checksums. The raw JSON is about 8 GB; NOMAD's indexed copy takes 8–10 GB.
80% of labels lack openFDA's harmonised name, type and ingredient fields
(store brands, unapproved and homeopathic products, many prescription
drugs) and would be unsearchable by name if those fields were relied on.

## Decision
- **A provider and a build method**, like road networks (ADR-0020): the
  `openfda` provider reads openFDA's `download.json` and offers one
  resource whose version is the export date, so a newer export appears in
  Updates (and in scheduled updates). The `openfda-labels` method downloads
  the parts (resuming each across interruptions; HTTPS on fda.gov hosts
  only), checks every zip entry's CRC-32 while reading it, and builds an
  SQLite database. A damaged part is downloaded again by itself.
- **Keep what a reader needs.** Uses, directions, warnings (all Drug Facts
  panels), contraindications, interactions, specific populations,
  overdose, side effects and inactive ingredients. Clinical pharmacology,
  studies, tables and package text are dropped.
- **De-duplicate and compress.** Repackagers publish identical labels;
  each distinct text is stored once, deflate-compressed. FTS5 indexes names
  (brand, generic, ingredients, manufacturer) and, per distinct text, the
  purpose and uses (Porter stemming) for situations.
- **Names and ingredients from the label itself** when openFDA's fields
  are missing: the product name from the SPL data elements, ingredients
  from the Drug Facts "Active ingredient" panel; names are normalised
  ("HCl" → hydrochloride, "USP" dropped) so products group by ingredient.
  Homeopathic products (by wording or potency notation such as 30C) are
  marked and left out of situations unless asked for.
- **Situations** are Arcana's own list of 34 everyday problems with the
  wording common on Drug Facts panels, matched against purpose and uses
  only, never warnings.
- **Presentation.** A Medicines view behind an acknowledgement remembered
  per browser, the medical warning on every page, and a side-by-side view
  of two labels' warnings that says plainly it does not check interactions.
  Readable by every client, like any reference archive.

## Measurements (2026-11-20 export, this container)
- First part alone: 20 000 records → 19 434 labels, 17 426 distinct
  texts, 78 MB database (0.60× the zip) in 37 s.
- Full build: recorded in the CHANGELOG entry for the release.

## Consequences
- About 1 GB on the drive instead of 8–10 GB, and no server process.
- Name and ingredient extraction from label text is heuristic; some
  products end up in their own group or with a long name. The label text
  itself is always shown unchanged.
- openFDA publishes no checksums, so the install is recorded as built from
  a single version of the source, structure checked (like map regions);
  transport damage is caught by the zips' CRC-32.

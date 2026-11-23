# ADR-0023: Offline translation in the reader's browser

- Status: Accepted (unreleased)
- Date: 2026-11-23

## Context
Project NOMAD translates its Kiwix pages with Bergamot (the engine behind
Firefox Translations) running as a Python service in a container. That
build is x86-64 only and stops on processors without AVX. Arcana runs on
Windows, macOS, Linux and ARM boards from a USB drive and cannot ship a
native translation engine per platform.

Firefox publishes the same engine as WebAssembly, and its language models,
in Remote Settings: `translations-wasm-v2` (bergamot-translator 4.0,
release v0.6.0) and `translations-models-v2` (models 3.x, one record per
file, each with the SHA-256 of the zstd-compressed attachment and of the
decompressed content). Engine and models are MPL-2.0.

## Decision
- **Translate in the reader's browser.** A worker runs the WebAssembly
  engine with Firefox's loader (`bergamot-translator.js`, vendored
  unmodified from Firefox's release branch). The device reading a page
  does the work, so translation works on any computer or phone with a
  modern browser, and the Arcana host needs no extra processor.
- **Catalog resources.** `arcana.translation-engine` pins the engine by
  hash in the bundled catalog. The `translations` provider turns the model
  registry into one language pack per language (to and from English; the
  newest desktop-release version with a complete file set per direction;
  attachments only from Mozilla's CDN). Packs depend on the engine.
  Downloads are verified against the compressed SHA-256 like any file.
- **Serve decompressed, verified.** Files stay compressed on the drive.
  `/translate/engine.wasm` and `/translate/model/…` decompress them (zstd,
  `klauspost/compress`), after checking once per run that the result
  matches Mozilla's decompressed SHA-256; a mismatch is refused. ETags are
  the decompressed hash.
- **Pages.** The script Arcana already injects into archive pages collects
  leaf block elements (paragraphs, list items, headings, cells) and, on
  the reader's request only, replaces their markup with the translation
  (Bergamot's HTML mode keeps links and emphasis). Translated markup is
  cleaned before insertion (no scripts, embeds or event handlers).
  Letters and short codes are left alone. Pairs without English go through
  English when both packs are installed.
- **CSP.** The application origin adds `'wasm-unsafe-eval'` so the worker
  can compile WebAssembly; JavaScript `eval` stays forbidden.

## Consequences
- About 50 MB per language (both directions), 1.2 MB for the engine.
- The first translation after loading a page fetches and compiles the
  models (about a second on the sample archive in Chromium; longer for
  large articles and slow devices).
- Search still matches the archive's own language.
- Translation is literal machine translation; the reader says so.

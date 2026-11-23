# ADR-0021: Question answering over the library

- Status: Accepted (unreleased)
- Date: 2026-11-23

## Context
Project NOMAD, the most visible comparable project, made local AI over
its library its headline feature (01-landscape.md). People want to ask a
question in plain words instead of reading several articles. Arcana's
design note 15-local-ai.md set the rules: answers only from installed
sources, with citations a reader can open, never required, never a
download Arcana starts by itself.

NOMAD embeds every archive into a vector database (Qdrant) with an
embedding model. Embedding a large archive takes hours and a large share
of the disk, needs a second model and a database server, and must be
redone when an archive is updated. None of that fits a library that moves
between computers on a USB drive.

## Decision
- **Retrieval from Arcana's existing search.** Questions are reduced to
  topic words (stop words removed), searched with kiwix-serve's Xapian
  indexes and the documents' SQLite FTS5 index (all words first, then the
  most specific words alone when that finds little), and the articles found
  are cut into passages along line breaks. Passages are ranked by how much
  of the question they cover, each word weighted by its rarity among the
  passages read. Nothing new is stored on the drive.
- **A relevance floor.** When the best passage covers less than the
  owner's strictness setting (default half of the weighted words), Arcana
  answers "I could not find this in your library" without asking a model.
- **The model as a sidecar.** Mozilla.ai's llamafile (Apache-2.0): one
  portable executable for Windows, macOS and Linux on x86-64 and ARM64,
  running llama.cpp's OpenAI-compatible server. It and the models are
  ordinary catalog resources downloaded from Hugging Face at pinned
  revisions and checked against the publisher's SHA-256; a model depends on
  the engine, so queueing a model queues the engine. The engine starts on
  the first question, listens on loopback with a random API key, and stops
  after an idle time; on Linux it also dies with Arcana. Drives without
  execute permission (exFAT) and Windows' `.exe` rule are handled by
  running a cached copy.
- **Or any OpenAI-compatible server.** Ollama, LM Studio or a llama.cpp
  server, on this computer or another; the interface says when that server
  is not on the local network.
- **Answers carry their evidence.** Passages are numbered in the prompt;
  the answer must cite them. Citations to passages that do not exist are
  removed, an answer that cites nothing is flagged, and every passage is
  shown with its resource, edition, date, license and warnings. Medical
  sources add the medical notice to the answer.
- **Access.** Off by default. On the host and for signed-in
  administrators; devices on the network only when the owner allows it
  (an answer uses the host's processor and memory). A GET request with
  server-sent events, so read-only network clients can ask. The host's own
  documents are used only for questions asked on the host. Paired programs
  can ask through `GET /api/integration/v1/ask` with the new scope
  `assistant:ask`.
- **Evaluation.** A golden question set (`internal/assistant/testdata`)
  measures retrieval on the sample archive in the test suite; `arcana ask
  --eval FILE [--with-model]` scores any set, with a model when one is
  configured.

## Consequences
- No vector database, no embedding model, no index to rebuild when an
  archive changes, and retrieval works the moment an archive is installed.
- Retrieval is lexical: a question that uses different words from the
  article ("heart attack" against "myocardial infarction") can miss. The
  fallback to single words, title matches and the reader's ability to
  rephrase limit this; adding synonyms or a small embedding model later
  would not change the interface.
- One answer is generated at a time; others wait. Small models on a
  processor take tens of seconds; the interface streams the answer.
- llamafile's portable executable format is flagged by a few antivirus
  products (1 of 76 VirusTotal engines for 0.10.6 on 2026-11-23); the
  catalog entry says so.

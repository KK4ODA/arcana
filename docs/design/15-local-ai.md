# O. Optional local question answering

Status: **implemented** (0.8.0; ADR-0021). This note kept the rules
the implementation follows; the decisions and their trade-offs are in
[ADR-0021](../adr/0021-question-answering-over-the-library.md), and the
user-facing description is in the README and docs/portable-use.md.

## Goals and non-goals

- Answer questions in plain language **only from installed sources**, with
  citations a reader can open, and with visible uncertainty.
- Never required, never on by default, never a download Arcana starts by
  itself.
- Not a chatbot that knows things on its own: an answer without supporting
  passages is "I could not find this in your library".

## Architecture

```mermaid
flowchart LR
  Q[Question] --> R[Retrieval: Arcana search + documents index]
  R --> P[Top passages with citations]
  P --> M[Model sidecar, loopback only]
  M --> A[Answer with numbered citations]
  A --> V[Check: every claim cites a passage]
```

- **Retrieval first.** Arcana already has full-text search over ZIM
  archives (kiwix-serve) and documents (SQLite FTS5), and the integration
  API returns plain text with citations. Passages are selected from those,
  not from model memory.
- **Model as a sidecar**, like kiwix-serve: a separate process (a
  llama.cpp-class server) installed as a catalog resource with its own
  license and checksum, started on loopback only, supervised, and stopped
  when unused. No native code is loaded into Arcana.
- **Interface**: a question box in Library search; answers show the passages
  used, each linking to the article, and the citation's warnings (medical,
  outdated) above the answer.

## Constraints to resolve before building

| Question | Notes |
|---|---|
| Hardware | Small models (1–4 B parameters, 4-bit) run on laptops and a Pi 5 slowly; answers may take tens of seconds. Measure before promising anything. |
| Licenses | Model weights carry their own terms; only openly licensed models that permit redistribution can be offered in the catalog. |
| Safety | Medical and legal questions must show the source warnings and recommend professional help where the source does. Refuse to answer beyond the passages. |
| Languages | Retrieval works per language; the model must handle the library's languages or say it cannot. |
| Evaluation | A fixed question set over the sample library, scored for citation correctness and refusal when the answer is absent. |

## As built

- **Retrieval** (`internal/assistant/retrieve.go`): topic words, Xapian and
  FTS5 searches, passages along line breaks, coverage weighted by rarity,
  at most two passages per article, a character budget from the context
  window. Table rows are kept on one line ("QTH | Location of the station").
- **Relevance floor**: below it the answer is "I could not find this in
  your library" and no model runs.
- **Backends**: llamafile 0.10.6 as a supervised loopback sidecar (catalog
  resources `arcana.llamafile` and three Apache-2.0 models: Qwen2.5 0.5B,
  Granite 3.3 2B, Qwen3 4B), or any OpenAI-compatible server.
- **Checks**: invalid citations removed, uncited sentences counted, an
  answer without citations flagged, source warnings shown with the answer.
- **Evaluation**: `internal/assistant/testdata/sample-golden.json` (nine
  questions, three unanswerable) passes 9/9 for retrieval against the
  sample archive; `arcana ask --eval FILE --with-model` adds answer checks.
- **Not yet**: follow-up questions in a conversation, synonyms or
  embeddings for questions worded differently from the article, measured
  answer quality and speed on a Raspberry Pi.

# ADR-0028: Models from the Ollama registry, as many as llamafile runs

- Status: Accepted
- Date: 2026-11-26

## Context
The owner asked Arcana to offer everything Project NOMAD lists that Arcana
lacks; this decision covers the language models. NOMAD 1.35.2 runs Ollama
0.33.3 in Docker and offers the models its own service lists
(`api.projectnomad.us/api/v1/ollama/models`): 272 models on
2026-11-26, with 7,669 tags (sizes, quantizations and variants). NOMAD's
interface hides tags marked `cloud` and the models left without any, and
showed 253 models. Ollama pulls every tag from `registry.ollama.ai`.

Arcana offered three models (ADR-0021): GGUF files at pinned Hugging Face
revisions, checked against the publisher's SHA-256, run by llamafile
0.10.6. Arcana runs no Docker (ADR-0002) and verifies every download
(ADR-0009), so NOMAD's approach (a pull through a local Ollama server)
does not carry over.

Two sources could supply the files:
- **The Ollama registry.** Each tag has an OCI manifest that lists its
  layers by SHA-256 digest: the model (a GGUF file for most tags), its
  license text, chat template and parameters, and for some models a
  separate vision projector. A blob is fetched by its digest, so the URL
  names the content and the digest is the checksum ADR-0009 needs. These
  are the very files NOMAD's users get.
- **Hugging Face.** The publishers' own repositories rarely carry GGUF
  files; most GGUF builds there are third-party conversions, a different
  one per model, and many of NOMAD's models (Ollama's own conversions,
  older community models) have none at a stable address.

## Decision
- **The registry is the source.** Each catalog entry is one GGUF model
  layer, downloaded from `registry.ollama.ai/v2/library/<model>/blobs/
  sha256:<digest>` (the registry redirects to its storage) and verified
  against that digest. Hugging Face remains the source of the three
  curated models.
- **A built-in, generated provider.** `tools/ollama-models` reads NOMAD's
  list and the registry, and writes
  `internal/catalog/bundled/ollama-models.json`, compiled into Arcana as the
  provider `ollama` ("Ollama model library"). Like the curated catalog it
  works with no network; it is not fetched at run time, so neither
  NOMAD's service nor the registry's manifests are a runtime dependency,
  and a release's list can be reviewed in its pull request. Entries keep
  their version while their file's digest is unchanged. The entries pass
  the same validation as any catalog (ADR-0007).
- **One entry per distinct file behind `latest` and the plain size tags**
  (`7b`, `135m`, `e4b`, `8x7b`, `30b-a3b`). Ollama points each at its
  default quantization for that size, usually Q4_K_M. Other quantizations,
  base and instruction variants and fine-tune tags are not offered. A
  model whose list holds no plain tag is represented by its first tag.
- **Only files llamafile 0.10.6 loads.** The generator starts llamafile's
  server (the arguments Arcana uses, 4,096-token context, no warm-up) on a
  sparse copy of every candidate file: the real GGUF header followed by
  zeros up to the real size. llama.cpp checks the architecture, every
  tensor's name, shape and type, and the file length while it loads, so
  this decides loadability without downloading the weights. A model whose
  weights loaded but whose context did not fit the 15 GB test machine
  counts as loadable. A model that loads but whose chat template
  llama.cpp's server cannot parse is left out, because Arcana starts the
  server with the template on.
- **Excluded**, with the counts in [docs/ollama-models.md](../ollama-models.md):
  cloud-only models; tags stored only in Ollama's own tensor format (MLX,
  MXFP8), which is not GGUF; embedding models, which cannot write answers
  and which Arcana's lexical retrieval does not use (ADR-0021); and files
  llamafile cannot load: architectures llama.cpp lacks, or GGUF files with
  vision or audio tensors that only Ollama's engine reads.
- **Image-and-text models whose image part is a separate projector are
  offered for text.** The owner's brief expected these to be unloadable.
  The load test showed that llamafile loads their text model, and Arcana
  asks text questions only, so they are included; the description says
  the image part is not downloaded, and the generator records them.
- **Memory.** Each description states the memory the model needs: the file
  size, a 16-bit key/value cache for 4,096 tokens from the header's layer
  and head counts, and 300 MB plus 5% for llama.cpp's buffers.
  `stats.memoryBytes` carries the figure for the Discover filter.
- **Licenses.** The generator recognises the license layer's text
  (Apache-2.0, MIT, the Llama, Gemma, Qwen, DeepSeek, NVIDIA, Mistral and
  other model licenses). A license is marked verified only when the
  publisher's own statement (the license field of the publisher's
  Hugging Face repository listed in `tools/ollama-models/licenses.json`)
  names the same terms, or names them where the registry has no text.
  Everything else is recorded with redistribution "unknown" and a note
  saying what the registry's copy reads and that it is unconfirmed.
- **Choosers.** In Discover, *Question answering* gains a source filter
  (tested with Arcana, Ollama library) and a memory filter, and the list
  shows providers' ranks first when no words are searched, so the three
  tested models stay at the top. A model's detail lists its memory need,
  parameters and longest context, and its other sizes as editions.
  Settings → Assistant lists installed models only, grouped by source.

## Consequences
- 197 of NOMAD's 272 models can be downloaded, as
  379 entries, each verified by digest and run by the engine Arcana
  already ships.
- The registry is Ollama's service. If it moves or drops a blob, that entry
  fails to download until the list is regenerated; the digest guarantees
  that nothing else is installed in its place.
- The load test proves that llamafile loads a file, not that the model
  answers well with Arcana's prompt. 95 entries' files have no chat
  template; llama.cpp's server falls back to ChatML, and their descriptions
  say so. Two of them failed the real test below.
- Excluded, of 272: 19 cloud-only, 4 with no GGUF file, 13 embedding
  models, 39 that llamafile cannot load. Seven further files of offered
  models were left out (Qwen3-VL 2B, 30B, 32B and 235B, Qwen3.5 122B,
  Nemotron 3.5 Bolt 74B, d1 2B).
- Many licenses stay unconfirmed until someone adds the publisher's
  repository to `licenses.json`. Unconfirmed entries can still be
  downloaded; Arcana's transfer between drives refuses anything whose
  redistribution is not "permitted".
- The biggest models (hundreds of gigabytes) are listed because NOMAD lists
  them; their memory figure tells a reader they will not run on a laptop.

## Tested with real weights
On 2026-11-26, 24 entries were downloaded through `arcana get` (each
verified against its registry digest), run by llamafile 0.10.6 on four
x86-64 cores, and asked "What does QTH mean in radio?" through `arcana ask`
over the sample archive. Seconds include starting the engine.

| Entry | Architecture | Seconds | Result |
|---|---|---:|---|
| gemma3:270m | gemma3 | 4 | Correct, cited nothing (flagged) |
| smollm:135m | llama | 9 | Wrong, repeated itself |
| qwen2:0.5b | qwen2 | 5 | Wrong, cited nothing (flagged) |
| qwen3:0.6b | qwen3 | 8 | Correct, cited |
| qwen3.5:0.8b | qwen35 | 11 | Correct, cited |
| granite4:350m | granite | 4 | "I could not find this" |
| granite3-moe:1b | granitemoe | 7 | Correct, cited |
| llama3.2:1b | llama | 14 | Correct, cited |
| tinyllama:1.1b | llama | 18 | Copied the passage |
| stablelm2:1.6b | stablelm | 27 | Wrong, cited nothing (flagged) |
| internlm2:1.8b | internlm2 | 14 | Correct, cited nothing (flagged) |
| exaone3.5:2.4b | exaone | 13 | Correct, cited |
| gemma2:2b | gemma2 | 14 | Correct, cited |
| phi:2.7b | phi2 | 18 | Correct, with a stray `<\|im_end\|>` (no chat template) |
| gemma4:e2b | gemma4 | 13 | Correct, cited |
| nemotron-3-nano:4b | nemotron_h | 23 | Correct, cited a passage that does not exist (removed, flagged) |
| olmo2:7b | olmo2 | 28 | "I could not find this" |
| phi3:3.8b | phi3 | 25 | Correct, cited |
| gemma:2b | gemma | 13 | Correct, cited nothing (flagged) |
| starcoder:1b | starcoder | 44 | Echoed the prompt (code model, no chat template) |
| qwen3-vl:4b | qwen3vl | 72 | Correct, cited |
| nemotron-mini:4b | nemotron | 12 | No answer |
| d1:3b | lfm2 | 17 | Correct, cited |
| glm-ocr | glm4 | 39 | Looped on the question (no chat template) |

Architectures run for real: 22 of the 40 offered (gemma, gemma2, gemma3,
gemma4, glm4, granite, granitemoe, exaone, internlm2, lfm2, llama,
nemotron, nemotron_h, olmo2, phi2, phi3, qwen2, qwen3, qwen35, qwen3vl,
stablelm, starcoder). Only load-tested: chatglm, cohere2, cohere2moe,
command-r, dbrx, deepseek2, falcon, gemma3n, gpt-oss, laguna, lfm2moe,
mistral3, muse-glimmer, nemotron_h_moe, qwen35moe, qwen3moe, qwen3next,
starcoder2. Nothing was run on Windows, macOS, ARM64 or a graphics card.

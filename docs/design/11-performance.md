# Performance and resource requirements

## Measured (Phase 1, 2026-11-22)

Environment: a Linux x86-64 container (4 vCPU), local SSD-backed disk,
Arcana 0.1.0-dev, kiwix-serve 3.8.2. The catalog held all 3,684 entries
(the Kiwix library plus the bundled catalog). The library contained
`wikipedia_en_ray-charles_mini` (26 MB, 25,562 entries).

| Measurement | Result |
|---|---|
| Binary size per platform (stripped) | 12.5 MB |
| Portable bundle, five platforms incl. kiwix-serve | 62 MB zip, 153 MB unpacked |
| Cold start to "offline ready" (DB open, sidecar re-index, catalog cache import check, kiwix-serve started and answering) | **281 ms** |
| Resident memory, idle, one archive | arcana **43 MB**, kiwix-serve **54 MB** |
| Catalog refresh (download + parse + validate + index 3,682 entries) | 4.7 s, network-bound |
| Index size on drive | `arcana.db` 9.3 MB; `catalog/kiwix.json` 5.4 MB |
| Catalog text search (FTS5, language filter) | 2–3 ms |
| Catalog first page, unfiltered, sorted | 12 ms |
| Category and language facets | 6 ms |
| Full-text search in archive (kiwix `/search`) | 68 ms |
| Article through the content proxy | 2 ms |
| Download pipeline throughput: HTTP + SHA-256 + fsync checkpoints every 16 MiB + publish (`BenchmarkDownloadThroughput`, 256 MiB, local server) | **246 MB/s** |
| Real download of a 26 MB ZIM from a Kiwix mirror | ~5–7 MB/s, network-bound |

The download pipeline runs well above USB 2.0 (≈30–40 MB/s) and typical
internet speeds, so it is never the bottleneck.

## Targets by deployment

| | Portable Windows laptop | Low-power Linux mini PC (N100 class) | Raspberry Pi 4/5 appliance |
|---|---|---|---|
| Minimum | Windows 10 1809+, 4 GB RAM, USB 3 drive | 4 GB RAM, Linux 5.7+ (exFAT) | Pi 4 4 GB / Pi 5, 64-bit OS, USB 3 SSD or NVMe for the library |
| Start to ready | < 2 s from USB 3 SSD; < 5 s from USB 2 stick | < 2 s | < 5 s after boot service start |
| Memory (Arcana + kiwix-serve) | < 300 MB with 20 archives incl. full Wikipedia | same | < 400 MB, leaving room for tiles and future routing |
| Concurrent clients | 5 | 20 | 20 (Prepper-Disk-class target) |
| Page load (article) | < 200 ms | < 200 ms | < 500 ms on 5 GHz Wi-Fi |
| Full-text search across 10 English archives | < 1 s | < 1 s | < 2 s |
| Map tiles (Phase 3) | Client-rendered; server range reads < 10 ms | same | same |
| GPS processing (Phase 3) | < 1 % CPU at 10 Hz NMEA | same | < 2 % |
| Power | — | 6–15 W | Pi 5: 2.4–3.3 W idle, 9–10 W stress (measured by third parties) |

## Design choices that keep resource use low

- **Index only what is needed.** ZIM archives carry their own Xapian
  indexes, so Arcana never extracts or re-indexes them and never
  duplicates archive content. Arcana's own FTS5 index covers catalog
  metadata (about 9 MB) and, in Phase 2, user documents only.
- **Lazy, bounded work.** The integrity check is quick by default; the full
  re-hash runs only when the user asks. Catalog refresh happens only on
  request. No background indexing.
- **Drive wear.**
  - Checkpoints every 16 MiB or 10 s, not per write.
  - No writes while browsing.
  - Logs rotate at 5 MB.
  - WAL is checkpointed on exit.
- **Concurrency.** Two downloads by default (configurable 1–8). A global
  token-bucket bandwidth limit is shared by all transfers. kiwix-serve
  runs 4 worker threads.
- **Client-side map rendering.** Map rendering will happen in the clients
  (Phase 3), so a Pi only serves byte ranges.

## Benchmarks to add

- **Library scale.**
  - Full English Wikipedia maxi (127 GB) and 20 other archives on USB 3 SSD
    and USB 2 stick.
  - Measure start time, kiwix-serve memory and search latency across
    archives.
- **Clients.** 20 concurrent clients against a Pi 5 (wrk / k6 script in
  `test/perf/`): p95 article latency, CPU and memory.
- **Integrity.** Full-check throughput per drive class.
- **Maps (Phase 3).**
  - Tile serving throughput from a 10 GB regional PMTiles file.
  - MapLibre frame rate on a mid-range Android phone.

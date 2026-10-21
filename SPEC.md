# Lookback: specification

Lookback is a native desktop app that finds your images and documents by what they contain. You
type "the error dialog about the hotspot" or "invoice from the hosting company", and Lookback shows
the matching screenshots, PDFs and notes. Everything runs on your computer: the app downloads an
embedding model that fits your hardware, indexes the folders you pick, and keeps the index current.

This document is the source of truth for v1. [UI_GUIDE.md](UI_GUIDE.md) sets the bar for the
interface, and [PLAN.md](PLAN.md) splits the work for implementation agents. The appendix lists every
design decision with its ID (Q1–Q31). When code and this document disagree, fix one of them in the
same change.

## 1. Overview

### Goals

- Search images and text documents by meaning, with a text query or "find similar".
- Choose and download the runtime and model that fit the hardware, and let the user test
  combinations on their own machine before committing.
- Index user-selected folders in the background, with a visible speed and ETA, and keep the index
  current as files change.
- Feel native and fast: keyboard-first, instant theme changes, and full Omarchy theme support on Linux.
- Stay private: no account, no cloud, no telemetry. The network is used only for pinned downloads
  and app updates.

### v1 scope

- Platforms: Linux x64 and Windows x64 (Q1).
- Content: images and text documents (Q4, Q14). See [section 9](#9-extraction-and-chunking).

### Non-goals for v1

- macOS (planned later; MyGo and llama.cpp both support it).
- Cloud sync or shared indexes (Q7).
- OCR. Low-text PDF pages are embedded as images instead (Q11).
- Audio and video, even though EmbeddingGemma 2 supports them.
- HEIC images and legacy binary Office formats (.doc, .xls, .ppt) (Q14).

## 2. Stack

| Part | Choice | Notes |
|---|---|---|
| Language | Go 1.27+ | Required by MyGo |
| UI | [MyGo](https://github.com/egoist/mygo) native UI, built-in components only (Q3) | Pure Go, no cgo, GPU-drawn. No extra component library |
| Icons | ~15 [Lucide](https://lucide.dev) SVGs embedded with `embed`, drawn with `ui.Icon` | ISC license; keep the notice |
| Database | [tursogo](https://docs.turso.tech/connect/go) v0.8.3 (`turso.tech/database/tursogo`), driver name `turso`, local file (Q7) | `database/sql` driver, no cgo (purego). Embeds a ~16–21 MB native library and extracts it on first use; set `TURSO_GO_CACHE_DIR=<data root>/lib`. Vector search verified in Go: see `docs/spikes.md` |
| Embedding model | EmbeddingGemma 2 GGUF from `ggml-org/embeddinggemma-2-GGUF` | Text model + `mmproj` encoders |
| Runtime | `llama-server` from official llama.cpp releases, run as a child process (Q2) | One build per backend |
| PDF | [go-pdfium](https://github.com/klippa-app/go-pdfium) in WebAssembly mode (wazero) | No cgo; text extraction and page rendering |
| Office | `archive/zip` + `encoding/xml` (stdlib) | DOCX, PPTX, XLSX, ODT, ODP, ODS |
| Images | `image/png`, `image/jpeg`, `image/gif`, `golang.org/x/image/webp`, `golang.org/x/image/bmp` | Decoded in Go, re-encoded for the server |
| HTML | `golang.org/x/net/html` | Text without `script` and `style` |
| TOML | `github.com/BurntSushi/toml` | Omarchy `colors.toml` |

Add every dependency with `go get`. Never vendor by hand or symlink a dependency.

## 3. Architecture

One Go process runs the UI and the indexer. It starts one `llama-server` child process that does all
embedding work over HTTP on the loopback interface.

```
            ┌───────────────────────── Lookback (Go) ─────────────────────────┐
 folders ──▶│ watch ─▶ index ─▶ extract ─▶ chunk ─▶ embed queue ─┐            │
            │   ▲        │                                      ▼            │
            │ startup    │ progress (tokens/s, ETA)      runtime.Embedder ──HTTP──▶ llama-server
            │ rescan     ▼                                      │            │      (child process)
            │          ui ◀── search ◀── store (Turso) ◀────────┘            │
            │          ▲                                                    │
            │ theme ───┘   catalog ─▶ download ─▶ runtimes/, models/        │
            │ hw ─▶ bench (self-test) ─▶ active combination                 │
            └─────────────────────────────────────────────────────────────────┘
```

| Package | Responsibility |
|---|---|
| `internal/app` | Wiring, lifecycle, single instance, tray, hotkey, power events |
| `internal/catalog` | The pinned manifest of runtimes and model files (compiled in) |
| `internal/download` | Resumable, verified downloads and archive unpacking |
| `internal/hw` | CPU, RAM and GPU detection; device lists from `llama-server --list-devices` |
| `internal/runtime` | Sidecar lifecycle (start, health, restart, CPU fallback) and the `Embedder` client |
| `internal/bench` | The hardware self-test and the combination test lab |
| `internal/extract` | One extractor per file type; produces text segments and images |
| `internal/chunk` | Token-exact 512/64 windows |
| `internal/index` | Scan, queue, pipeline, progress and ETA, generations |
| `internal/watch` | Folder watchers and the startup rescan |
| `internal/search` | Query embedding, ranking, grouping, find similar |
| `internal/store` | Turso schema, migrations and queries |
| `internal/theme` | Built-in themes, the Omarchy source and its watcher |
| `internal/ui` | Every screen; reads state, sends commands; no I/O on the main thread |

Rules:

- The UI never blocks. Work runs on goroutines and changes UI state through `Window.Update`.
- Packages talk through the interfaces defined in M0 (see PLAN.md). Each interface has a fake for tests.

### Data locations

| | Linux | Windows |
|---|---|---|
| Data root | `$XDG_DATA_HOME/lookback` (default `~/.local/share/lookback`) | `%LOCALAPPDATA%\Lookback` |
| Contents | `lookback.db`, `runtimes/<tag>-<backend>/`, `models/<revision>/`, `thumbs/`, `logs/`, `downloads/` | same |

## 4. Catalog and pins

`internal/catalog` holds a JSON manifest compiled into the binary (`//go:embed catalog.json`). Each
app release pins exact versions (Q20). The app downloads only what the manifest names.

### Model files

Repository `ggml-org/embeddinggemma-2-GGUF`, pinned revision
`58faf90940886df2d62be555c8765d00dac362c5`. URL pattern:
`https://huggingface.co/ggml-org/embeddinggemma-2-GGUF/resolve/<revision>/<file>`.

| Part | Precision | File | Bytes | SHA-256 |
|---|---|---|---|---|
| Text model | Q8_0 | `embeddinggemma-2-Q8_0.gguf` | 309855456 | `2188ac1deca4b77dffefd603c2776a9d76d9d74ec01841392982ebb840b09135` |
| Text model | BF16 | `embeddinggemma-2-BF16.gguf` | 557950176 | `68bae29d62fb8c7d23e98d21fd4662753ddd636e6b62b8a70dfe059a9844f216` |
| Encoders | Q8_0 | `mmproj-embeddinggemma-2-Q8_0.gguf` | 554821024 | `c4a8a52691ecef40618438928bdf9e68379b854e24166f292592353db0aab64f` |
| Encoders | BF16 | `mmproj-embeddinggemma-2-BF16.gguf` | 982074784 | `d2033b3cd0223cfb2e7a2b50dc80a664c78c816d375f5b017ec3ef80ee0bc766` |

Never ship or offer F16: the model's activations overflow float16 and produce NaN or degraded vectors.

### Runtimes

llama.cpp release tag `b11755` (commit `704366d76f8748cf9ad3df62a464bfcc8d1ae6ad`). EmbeddingGemma 2
needs `b11452` or later. Assets from `https://github.com/ggml-org/llama.cpp/releases/download/<tag>/<asset>`:

| OS | Backend | Asset(s) | Approx. size |
|---|---|---|---|
| Linux | CPU | `llama-b11755-bin-ubuntu-x64.tar.gz` | 22 MB |
| Linux | Vulkan | `llama-b11755-bin-ubuntu-vulkan-x64.tar.gz` | 34 MB |
| Linux | CUDA 12.8 | `llama-b11755-bin-ubuntu-cuda-12.8-x64.tar.gz` + `cudart-llama-b11755-bin-ubuntu-cuda-12.8-x64.tar.gz` | 177 + 594 MB |
| Linux | CUDA 13.4 | `llama-b11755-bin-ubuntu-cuda-13.4-x64.tar.gz` + `cudart-llama-b11755-bin-ubuntu-cuda-13.4-x64.tar.gz` | 158 + 440 MB |
| Windows | CPU | `llama-b11755-bin-win-cpu-x64.zip` | 23 MB |
| Windows | Vulkan | `llama-b11755-bin-win-vulkan-x64.zip` | 36 MB |
| Windows | CUDA 12.4 | `llama-b11755-bin-win-cuda-12.4-x64.zip` + `cudart-llama-bin-win-cuda-12.4-x64.zip` | 271 + 391 MB |
| Windows | CUDA 13.4 | `llama-b11755-bin-win-cuda-13.4-x64.zip` + `cudart-llama-bin-win-cuda-13.4-x64.zip` | 159 + 423 MB |

The Windows `cudart` asset names have no tag. Record the SHA-256 of every asset in the manifest when
pinning (compute it from the downloaded release asset). ROCm is not offered: Vulkan covers AMD with a
much smaller download (Q9).

The CPU builds contain several CPU variants (`libggml-cpu-haswell.so`, `-zen4`, and so on) that
llama.cpp picks at run time.

## 5. Hardware detection

`internal/hw` produces a `Hardware` value: CPU model and thread count, total RAM, and a list of GPUs.

| What | Linux | Windows |
|---|---|---|
| CPU | `/proc/cpuinfo` | `GetLogicalProcessorInformationEx`, registry `ProcessorNameString` |
| RAM | `/proc/meminfo` `MemTotal`, `MemAvailable` | `GlobalMemoryStatusEx` |
| GPUs | `/sys/bus/pci/devices/*/{class,vendor,device}` (class `0x03xxxx`) | DXGI `EnumAdapters1` via `golang.org/x/sys/windows` |
| NVIDIA driver | `/proc/driver/nvidia/version` | DXGI driver version / registry |

Vendor IDs: `0x10de` NVIDIA, `0x1002` AMD, `0x8086` Intel.

**Ground truth** comes from the runtime itself. After a backend build is downloaded, run
`llama-server --list-devices` and parse lines like:

```
Vulkan0: AMD Radeon Graphics (RADV RENOIR) (12970 MiB, 6800 MiB free)
```

A backend is a **candidate** only when its `--list-devices` output lists at least one device.
Candidates (Q9):

- **CUDA**: an NVIDIA GPU is present and the driver is new enough. Use CUDA 13.4 when the driver
  supports CUDA 13, else CUDA 12.x. Verify the minimum driver versions in WP2 and record them in the catalog.
- **Vulkan**: any GPU, when the Vulkan build lists a device.
- **CPU**: always.

### Recommended combination (Q10, Q13)

- Backend: the fastest candidate from the self-test ([section 6](#6-onboarding)).
- Precision: BF16 for both parts if the chosen backend's device has 4 GB or more free VRAM, or, for
  the CPU backend, 16 GB or more total RAM. Otherwise Q8_0 for both.
- Decide once, during onboarding. Store the result. Never switch on its own; only the user switches
  precision, in Models & Hardware.

## 6. Onboarding

Onboarding runs on first launch and can be reopened from Models & Hardware. Its progress is stored,
so a restart returns to the same step (Q22).

### Step 1: Hardware

- Show CPU, RAM and each GPU with its free memory.
- Download the CPU and Vulkan builds right away (small) and run `--list-devices` to find candidates.
- Show the recommended combination and its total download size. Buttons: **Continue with
  recommended** and **Compare combinations** (opens the test lab in step 2).

### Step 2: Downloads and test lab (Q24, Q25)

A **combination** is backend × text model precision × encoder precision, for example "Vulkan, BF16
text, Q8_0 encoders". The user selects any set of combinations in a matrix. The lab downloads the
parts they need. Parts are shared, so selecting two backends with the same precisions downloads the
model once.

Each file row shows: name, size, progress, speed, and Pause/Resume. Downloads resume after a crash or
restart ([section 7](#7-downloads)).

When a combination's parts are ready, the lab tests it:

1. Start the sidecar with that combination.
2. Embed the bundled sample: 8 images of different sizes and 16 text chunks, in
   `assets/sample/` (self-made or CC0 content only).
3. Optionally embed up to 10 files the user picks.
4. Record:

| Column | How it is measured |
|---|---|
| Image ms (median) | Wall time per image request |
| Text tokens/s | `usage.prompt_tokens` ÷ wall time over the text chunks |
| Peak RAM | Linux `/proc/<pid>/status` `VmHWM`; Windows `PeakWorkingSetSize` |
| VRAM used | Free device memory from `--list-devices` before start minus while loaded (approximate) |
| Quality | Mean cosine similarity between this combination's sample vectors and the BF16 reference vectors |

The BF16 reference vectors for the bundled sample are computed at release time and embedded in the
binary, so the quality column works without downloading BF16. For user-picked files, quality needs a
BF16 combination installed; otherwise the column shows "n/a".

The lab marks the **recommended** combination: the fastest one with quality ≥ 0.99. The user can pick
any combination with **Use this combination**.

### Step 3: Folders

- **Add folder** opens `mygo.Dialog.Open` with `Directory: true`.
- For each folder, preview what will be indexed: counts by type, plus files skipped by the exclusion
  rules (Q15), with an editable ignore list.
- **Start indexing** finishes onboarding and opens Search. Search works while indexing runs.

## 7. Downloads

`internal/download` downloads one file per goroutine.

- Write to `downloads/<sha256>.part`, and save `downloads/<sha256>.part.json` with
  `{url, size, sha256, revision}` before the first byte.
- On start or resume, request `Range: bytes=<partSize>-`. If the server answers `200` instead of
  `206`, truncate and restart that file.
- Hash the whole file at the end. On a match, rename it atomically into place. On a mismatch, delete it
  and retry once from zero, then report the error.
- Retry network errors with exponential backoff (1 s up to 60 s). Pause while `mygo.Network` reports
  offline, and resume when it comes back.
- On app start, resume every `.part` file that has a `.part.json`, so a crash or kill loses nothing.
- Unpack runtime archives (`.tar.gz`, `.zip`) into `runtimes/<tag>-<backend>/`, refusing paths that
  leave that folder.
- Report progress as `{file, done, total, bytesPerSec}` at most 4 times per second.

## 8. Sidecar runtime

`internal/runtime` runs one `llama-server` for the active combination:

```
llama-server -m <text.gguf> --mmproj <mmproj.gguf> --embeddings --pooling mean
             -c 8192 -b 8192 -ub 8192 [-ngl 99] -t <physical cores>
             --host 127.0.0.1 --port <free port> --api-key <random 32 bytes, hex>
```

- `-ngl 99` only for GPU backends. Choose a free port by listening on `127.0.0.1:0` and closing it.
- The API key is required: llama-server warns that, without one, CORS allows all origins.
- Health: poll `GET /health` until `{"status":"ok"}`, with a timeout of 120 s. Watch for an early exit.
- Logs go to `logs/llama-server.log` (rotated at 10 MB).
- **Failure (Q23)**: if the process exits or a request fails with a server error, restart it once. If it
  fails again, switch to the CPU build of the same precisions (download it if needed), show a toast
  ("GPU backend failed; indexing continues on the CPU"), and keep going. The vectors stay compatible,
  because only the precision defines the vector space.
- Stop the process when the app quits, including on a crash path. On Linux, start it in its own process
  group and set `Pdeathsig` (SIGKILL), so it dies with the app even after `kill -9`; keep the forking thread
  alive with `runtime.LockOSThread`, because `Pdeathsig` follows that thread. On Windows, a job object.

### Embedder API

```
POST /v1/embeddings
{"input": ["task: search result | query: <q>"]}                              // text
{"input": [{"content": [{"type": "image_url",
             "image_url": {"url": "data:image/png;base64,<...>"}}]}]}      // image
```

The response has `data[i].embedding` (768 floats, L2-normalized) and `usage.prompt_tokens`. Token
counts for chunking come from `POST /tokenize` with `with_pieces: true`.

## 9. Extraction and chunking

### File types (Q14)

| Kind | Extensions | Unit |
|---|---|---|
| Image | `.png .jpg .jpeg .webp .gif .bmp` | One image (GIF: first frame) |
| Text | `.txt .md .markdown .rst .log .json .yaml .yml .toml .csv .tsv .html .htm .xml` + source code (`.go .py .js .ts .tsx .jsx .rs .c .h .cpp .java .kt .swift .rb .php .sh .fish .lua .sql .css .scss` and similar) | Whole file |
| PDF | `.pdf` | Page |
| Office | `.docx .pptx .xlsx .odt .odp .ods .rtf` | Document, slide or sheet |

### Exclusions (Q15)

Defaults, editable per folder: hidden files and folders (dot-prefixed; on Windows, also the hidden
attribute), `.git`, `node_modules`, `__pycache__`, `.venv`, `venv`, `target`, `build`, `dist`, `.cache`,
files over 50 MB, images under 64×64 px, and symlinks that resolve outside the indexed folder.

### Extractors

- **Images**: read the header for the dimensions (for the ETA). Decode, and downscale so the long edge
  is at most 1600 px (a setting; WP4 measures time against quality on the sample). Send PNG and JPEG
  as they are when no resize is needed. Re-encode everything else as PNG, because the server's decoder
  doesn't read WebP.
- **Text**: accept valid UTF-8, or UTF-16 with a BOM. Treat a file with a NUL byte in its first 8 KB as
  binary and skip it. Extract HTML text with `x/net/html`, skipping `script` and `style`.
- **PDF (Q11)**: extract the text of each page. If a page has fewer than 200 non-space characters, render
  it at 150 DPI (long edge capped at 1600 px) and embed it as an image. Each page is its own unit, and
  results show the page number.
- **DOCX**: the `w:t` runs of `word/document.xml`, with paragraph breaks at `w:p`.
- **PPTX**: the `a:t` runs of each `ppt/slides/slideN.xml`, one unit per slide.
- **XLSX**: `xl/sharedStrings.xml` plus each sheet, rows as `A | B | C` lines, one unit per sheet.
- **ODT/ODP/ODS**: the `text:p` and `text:span` elements of `content.xml`, one unit per document, slide
  (`draw:page`) or sheet (`table:table`).
- **RTF**: strip control words, decode `\uN` and `\'hh` escapes, and skip `{\*...}`, `fonttbl`,
  `colortbl` and `pict` groups.

### Chunking (Q12)

- Document prompt: `title: <file name> | text: <chunk>`. Query prompt: `task: search result | query: <q>`.
  Both come from the model's `config_sentence_transformers.json`. The file name as the title is allowed
  by the format and helps short chunks.
- Windows of 512 tokens including the prompt prefix, overlapping by 64 tokens. Use `/tokenize` with
  `with_pieces` to map each window back to byte offsets in the UTF-8 text. A token can split a character (byte-fallback
  pieces), so offsets are bytes, not runes.
- Store, per chunk: the unit (page, slide or sheet), the byte range in the text, and a
  snippet (the first 300 characters, whitespace collapsed).
- Send text chunks in batches of up to 8 per request. Send images one per request, to keep payloads small.

## 10. Storage (Turso)

One database file, `lookback.db`. Vectors are `float32[768]` blobs written with `vector32(?)`, where
the parameter is the JSON array (Q17).

```sql
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE combos (                 -- installed combinations and their test results
  id TEXT PRIMARY KEY,                -- "vulkan/bf16/q8_0"
  backend TEXT NOT NULL, text_quant TEXT NOT NULL, enc_quant TEXT NOT NULL,
  installed_at INTEGER NOT NULL, bench_json TEXT
);

CREATE TABLE generations (            -- one vector space per precision pair (Q26)
  id INTEGER PRIMARY KEY,
  text_quant TEXT NOT NULL, enc_quant TEXT NOT NULL, model_rev TEXT NOT NULL,
  state TEXT NOT NULL,                -- 'building' | 'active' | 'retired'
  created_at INTEGER NOT NULL
);

CREATE TABLE folders (id INTEGER PRIMARY KEY, path TEXT NOT NULL UNIQUE,
                      ignore TEXT NOT NULL DEFAULT '', added_at INTEGER NOT NULL);

CREATE TABLE files (
  id INTEGER PRIMARY KEY, folder_id INTEGER NOT NULL, path TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL,                 -- 'image' | 'text' | 'pdf' | 'office'
  size INTEGER NOT NULL, mtime INTEGER NOT NULL      -- mtime: Unix nanoseconds
);

CREATE TABLE file_index (             -- per generation: what was indexed, and how it went
  file_id INTEGER NOT NULL, gen INTEGER NOT NULL,
  size INTEGER NOT NULL, mtime INTEGER NOT NULL,     -- as the file was when indexed; nanoseconds
  status TEXT NOT NULL,               -- 'done' | 'error' | 'skipped'
  error TEXT, tokens INTEGER, ms INTEGER,
  PRIMARY KEY (file_id, gen)
);

CREATE TABLE items (
  id INTEGER PRIMARY KEY, gen INTEGER NOT NULL, file_id INTEGER NOT NULL,
  unit INTEGER NOT NULL DEFAULT 0,    -- page, slide or sheet number; 0 for whole files
  start INTEGER, "end" INTEGER,       -- byte range in the text; NULL for images
  modality TEXT NOT NULL,             -- 'text' | 'image'
  snippet TEXT, embedding BLOB NOT NULL
);
CREATE INDEX items_gen_file ON items (gen, file_id);
```

A file needs work for generation G when it has no `file_index` row for G, or the row's size or mtime
differs from `files`.

### Search query (Q18)

```sql
SELECT i.id, i.file_id, f.path, f.kind, i.unit, i.snippet, i.modality,
       vector_distance_cos(i.embedding, vector32(?1)) AS d
FROM items i JOIN files f ON f.id = i.file_id
WHERE i.gen = ?2            -- the active generation
ORDER BY d LIMIT 300;
```

In Go: group by file and keep each file's best item. Score = `1 − d` + filename boost (0.04 for each
query word of 3 or more letters found in the file name or the last two path segments, at most 0.12).
Return the top 60. A type filter adds `AND f.kind IN (...)`.

**Find similar (Q6)** uses the stored vector, so no embedding is needed. The seed must be an item of the
requested generation, because a vector from another generation is in another vector space. Otherwise
the query returns no rows and the store reports `ErrStaleItem`:
`vector_distance_cos(i.embedding, (SELECT embedding FROM items WHERE id = ?1 AND gen = ?2))`, excluding the
same file (`i.file_id != (SELECT file_id FROM items WHERE id = ?1 AND gen = ?2)`).

Search is a linear scan (Q17). Measured with tursogo v0.8.3 on the reference laptop: 223–229 ms at
100k items (spike 1, `docs/spikes.md`). Mark the query with a `ponytail:` comment, and revisit if
Turso adds a vector index.

### Generations (Q26)

- The active generation serves every search.
- When the user switches precision, create a `building` generation and index into it in the
  background. The old one keeps serving. When the new one is complete, one transaction marks it `active`
  and the old one `retired` (at most one generation is active at a time). Then the old one's items are
  deleted.
- Switching only the backend keeps the generation.

## 11. Indexing pipeline

- **Queue order**: files changed while the app runs, then new files, then the startup rescan's
  changes, then the rebuild of a `building` generation.
- **Steps**: stat → exclusion check → extract → chunk → embed → write the items and the
  `file_index` row in one transaction. Delete a file's rows when it disappears.
- **Errors**: record them in `file_index` (status `error`) and continue. Activity lists them with Retry.
  A file with an error row whose size and mtime did not change needs no work by itself, so Retry clears its
  row first. The indexer offers Retry, Retry all, Rescan (one folder) and Rebuild (a building generation for
  new precisions) as commands. They queue work and return.
- **Pause**: the user Pause button, battery power (Q16, configurable), and system suspend
  (`mygo.Power.OnSuspend` / `OnResume`). While indexing on AC power, hold `mygo.Power.KeepAwake`.

### Progress and ETA (Q27)

Every item gets an estimated cost in tokens before it runs:

- Text and Office: bytes ÷ 4 at first, corrected online by the measured ratio of
  `usage.prompt_tokens` to bytes.
- Images: pixels (after the 1600 px cap) × a tokens-per-megapixel ratio, seeded from the self-test and
  corrected online from `usage.prompt_tokens`.
- PDF: per page, as text or as an image according to its text length.

Keep separate moving averages (EWMA, α = 0.1 per finished item) of tokens per second for text and for
images, because image tokens are slower. ETA = Σ remaining text cost ÷ text rate + Σ remaining image
cost ÷ image rate.

Publish `Progress{filesDone, filesTotal, filesPerSec, tokensPerSec, eta, current, perFolder[], errors}`
at most 4 times per second. Show "estimating…" until 30 s or 10 items have passed.

## 12. Search

- Query box: runs on Enter, or after a 250 ms pause in typing.
- Results: one mixed list, ranked as in [section 10](#10-storage-turso). Chips: All, Images,
  Documents (Q19).
- Each result: thumbnail (images, rendered PDF pages) or document card (icon, name, page, snippet),
  plus the score on hover.
- Actions: Open (default app), Show in folder (`mygo.Shell.ShowItemInFolder`), Find similar, Copy path.
- Search works during indexing, over what is indexed so far.

Details of the layout, states and keyboard are in UI_GUIDE.md.

## 13. Background, tray and hotkey

- Closing the window hides it. The app keeps running in the tray, watching and indexing (Q16).
- Tray menu: Open Lookback, Pause or Resume indexing, Quit.
- Watching (Q5): one recursive `mygo.WatchFiles` per folder, with the exclusion rules as its `Ignore`.
  Batches of events are debounced for 1 s.
- Startup rescan: walk each folder and compare size and mtime with `files`; queue the differences.
- Global hotkey (Q30): Super+Shift+Space by default, configurable, through MyGo's global shortcuts.
- Single instance: `lookback --toggle` shows or hides the running window and exits. Use MyGo's
  single-instance support if it has one; otherwise a Unix socket (Linux) or named pipe (Windows) in the
  data folder. On Hyprland, where global shortcuts may not reach apps, the user binds:
  `bind = SUPER SHIFT, SPACE, exec, lookback --toggle`.

## 14. Settings

| Setting | Default |
|---|---|
| Theme | Omarchy if available, else System |
| Active combination | From onboarding |
| Installed combinations | Delete unused ones (frees disk) |
| Pause indexing on battery | On |
| Global hotkey | Super+Shift+Space |
| Max image edge | 1600 px |
| Folders and ignore lists | From onboarding |
| Data folder | Platform default (moving it moves the files) |
| Logs | Open the logs folder |

All settings live in the `settings` table and apply immediately.

## 15. Themes

Package `internal/theme`. A theme is a function returning `*ui.Theme`, built from a copy of MyGo's
`ui.LightTheme()` or `ui.DarkTheme()` with its color fields set. The root view calls `c.SetTheme`
with the active theme on every frame, so a change through `Window.Update` applies instantly
everywhere (Q28).

### Built-in themes

| Theme | Dark | Background | Surface | Text | TextMuted | Accent | AccentText | Danger | Warning | Success |
|---|---|---|---|---|---|---|---|---|---|---|
| Catppuccin Mocha | yes | `#1e1e2e` | `#313244` | `#cdd6f4` | `#a6adc8` | `#cba6f7` | `#1e1e2e` | `#f38ba8` | `#f9e2af` | `#a6e3a1` |
| Catppuccin Latte | no | `#eff1f5` | `#e6e9ef` | `#4c4f69` | `#5c5f77` | `#8839ef` | `#ffffff` | `#d20f39` | `#df8e1d` | `#40a02b` |
| One Dark | yes | `#282c34` | `#2c313a` | `#abb2bf` | `#9da5b4` | `#61afef` | `#282c34` | `#e06c75` | `#e5c07b` | `#98c379` |
| One Light | no | `#fafafa` | `#f0f0f1` | `#383a42` | `#696c77` | `#3468d6` | `#ffffff` | `#e45649` | `#c18401` | `#50a14f` |
| System | follows OS | MyGo default | | | | desktop accent | | | | |
| Omarchy | from file | see below | | | | | | | | |

Some values differ from the upstream palettes to pass contrast: Latte uses `subtext1` for muted text
and `mantle` as the surface, One Dark's muted text is `#9da5b4`, and One Light's accent is darkened so
white text on it reaches 4.5:1. Derive the other fields as Onevra does: SurfaceHover and SurfacePressed
mix Surface 8% and 16% toward Text, Border mixes Background 18% toward Text, AccentHover and
AccentPressed mix the Accent 12% toward white or black, Selection is the Accent at 24% alpha, Focus is
the Accent at 80% alpha, Scrollbar is the Text at 35% alpha, and Radius is 8.

**Contrast test (required)**: a unit test checks every built-in theme, and the Omarchy mapping on a set
of fixture files, for Text and TextMuted ≥ 4.5:1 against both Background and Surface, AccentText ≥ 4.5:1
against Accent, and Focus ≥ 3:1 against Background (WCAG 1.4.11 for the focus ring). Border is
decorative: it must reach ≥ 1.3:1 against Background so card edges stay visible, and no more is required.

### Omarchy theme (Linux only, build tag `linux`)

The source file is `<state>/omarchy/current/theme/colors.toml`, where `<state>` is `$XDG_STATE_HOME`
or `~/.local/state` (from `os.UserHomeDir`; never hard-code a user name). The entry "Omarchy" is
listed only if the file exists at startup, and it is the default then.

The file is flat TOML: `mode = "dark"` plus `key = "#rrggbb"` pairs. Mapping:

| `ui.Theme` field | From `colors.toml` |
|---|---|
| Dark | `mode == "dark"`; if `mode` is missing, background luminance < 0.5 |
| Background | `background` |
| Surface | `background` mixed 50% toward `lighter_background` |
| SurfaceHover | `lighter_background` |
| SurfacePressed | `lighter_background` mixed 15% toward `foreground` |
| Border | `background` mixed 18% toward `foreground` |
| Text | `foreground` |
| TextMuted | `muted`, mixed toward `foreground` in 5% steps until ≥ 4.5:1 against both Background and Surface |
| Accent | `accent`; Hover/Pressed mixed 12% toward white/black |
| AccentText | whichever of `background` and `bright_foreground` contrasts more with `accent` |
| Danger | `red` |
| Warning | `bright_yellow` if dark, else `yellow` (some themes' `yellow` is green) |
| Success | `green` |
| Selection | `selection` |
| Focus | Accent at 80% alpha |
| Scrollbar | Text at 35% alpha |

A missing key falls back to the System theme's value. Example: the theme on the reference machine has
`muted = "#53685B"` on `background = "#111c18"`, which is 2.91:1. The guard mixes it to about 35%
(`#7a8870`, 4.64:1 on Background), and further where Surface needs it.

### Reloading

On the reference machine, `current/theme` is a real directory. `omarchy-theme-set` builds
`current/next-theme/`, runs `rm -rf theme && mv next-theme theme`, and then writes
`current/theme.name`. On other setups, `theme` is a symlink replaced with `ln -nsf`. Both kinds of swap
change only the parent folder, so:

1. Watch `<state>/omarchy/current/` without recursion (`mygo.WatchFiles`). React to events on `theme`,
   `theme.name` and `next-theme`.
2. Also watch the folder that `theme` resolves to (`filepath.EvalSymlinks`), for in-place edits of
   `colors.toml`. Re-arm this watch after every swap.
3. Debounce for 200 ms, then read and parse `colors.toml`. If the file is missing or invalid (the swap
   is mid-way), keep the current colors and retry 3 times, 300 ms apart.
4. Apply through `Window.Update`. No restart, no flicker.

## 16. Security and privacy

- No telemetry and no analytics.
- Network use: the pinned downloads in the catalog, and app updates. Nothing else.
- The sidecar listens on 127.0.0.1 only, with a random API key per run.
- The UI and thumbnail code read only files that are in the index.
- Archive unpacking rejects path traversal.
- Logs never contain file contents, only paths and errors.

## 17. Packaging and updates

- Linux: a `.deb` and a Linux install script (MyGo distribution). Windows: an installer.
- App updates: MyGo's signed auto-updates (Q20).
- Each app release pins one llama.cpp tag and one model revision with checksums. When an update
  changes a pin, the new files download in the background. The index is rebuilt only if the model
  revision or the precision changes, as a `building` generation.

## 18. Performance budgets

Measured with the prototype on the reference laptop (Ryzen 7 5700U, Radeon RENOIR iGPU, 21 GB RAM,
BF16, llama.cpp b11755). Source: `~/Desktop/AI/embeddingGemma_test/bench_results.txt`.

| Workload | CPU (8 threads) | Vulkan iGPU |
|---|---|---|
| One query (~15 tokens) | 47 ms | 45 ms |
| 32 chunks of ~128 tokens | 6.1 s | 3.5 s |
| 8 chunks of ~512 tokens | 6.7 s | 3.5 s |
| One 2,048-token document | 3.9 s | 1.9 s |
| 40 screenshots (12,006 image tokens) | 281 s | 158 s |
| Median screenshot | 4.8 s | 2.7 s |
| Largest screenshot (1896×1030) | 24.2 s | 13.9 s |

Budgets:

- Search, from Enter to results drawn: < 300 ms at 10k items, < 1 s at 100k items (query embedding included).
- The UI keeps 60 fps while indexing at full speed.
- App start to an interactive window: < 1 s (the sidecar starts in the background).
- Idle with nothing to index: < 1% CPU.

## 19. Milestones

| Milestone | Content |
|---|---|
| M0 | Module, shared types, interfaces and fakes; 3 spikes (tursogo vectors in Go, MyGo image grid, sidecar on Linux and Windows) |
| M1 | Catalog, downloads, hardware detection, runtime, test lab, onboarding |
| M2 | Image and text indexing, search UI, themes |
| M3 | PDF and Office |
| M4 | Tray, watcher, startup rescan, battery and suspend, hotkey, `--toggle` |
| M5 | Packaging and auto-update |

PLAN.md maps these milestones to work packages.

## 20. Acceptance criteria

1. Kill the app during a model download. Restart it: the download continues from the same byte and the
   file verifies.
2. On the reference laptop, onboarding recommends Vulkan with BF16, and the test lab table fills in for
   every selected combination.
3. Index the 277 reference screenshots. "an error message" returns the "Connection activation failed"
   screenshot in the top 3.
4. The ETA is within 25% of the actual remaining time after the first 2 minutes of indexing the 40-image sample.
5. A scanned PDF page with no text is found by a description of its content.
6. Edit a watched text file. Its new content is searchable within 10 s.
7. Run `omarchy-theme-set <other theme>` while Lookback is open. The app's colors change within 1 s with no restart.
8. Kill `llama-server` twice during indexing. The app falls back to the CPU, shows a toast, and keeps indexing.
9. Unplug AC power. Indexing pauses; plugging it back in resumes it.
10. `lookback --toggle` shows or hides the running window.
11. Switching precision builds a new index while search keeps working on the old one.
12. Every screen passes the UI_GUIDE.md definition of done.

## 21. Risks

| Risk | Mitigation |
|---|---|
| ~~tursogo vector functions unverified~~ **Resolved 2026-10-21:** spike 1 passed in Go (results match brute force; 226 ms at 100k) | `spikes/tursogo` stays as a regression test |
| tursogo's extracted native DLL may be blocked by SmartScreen or antivirus on Windows | Run on real Windows in M0 spike 3 / WP10; the cache folder is set to the app's data folder |
| MyGo is pre-1.0; APIs may change | Pin its version in `go.mod`; spike 2 checks the image grid early |
| CUDA runtime downloads are large (400–600 MB) | Show sizes before download; CUDA is offered only when an NVIDIA GPU is found |
| go-pdfium in WASM may be slow on large PDFs | Measure in WP4; process pages lazily, one at a time |
| Vulkan driver differences on Windows | The self-test and the CPU fallback catch broken paths |
| Large images cost many tokens (24 s on the CPU) | The 1600 px cap, measured in WP4 |

## Appendix: decision log

| ID | Topic | Decision |
|---|---|---|
| Q1 | Platforms | Linux + Windows x64; macOS later |
| Q2 | Runtime | llama-server sidecar, official llama.cpp build per backend |
| Q3 | UI | MyGo native UI with built-in components only; Lucide SVG icons |
| Q4 | Text scope | Plain text, code, PDF and Office |
| Q5 | Freshness | Recursive `mygo.WatchFiles` + size/mtime rescan at startup |
| Q6 | Search input | Text query + Find similar from any result |
| Q7 | Database | Local tursogo file only |
| Q8 | Docs location | `~/Desktop/AI/lookback/` |
| Q9 | Backend choice | Detect candidates, self-test, keep the fastest; settings override; no ROCm |
| Q10 | Precision | Auto by RAM/VRAM |
| Q11 | PDF pages | Text per page; low-text pages embedded as images |
| Q12 | Chunking | 512 tokens, 64 overlap; grouped by file with the best snippet |
| Q13 | Precision timing | Decided once at setup; switching re-indexes |
| Q14 | File types | Modern set (section 9); no HEIC, no legacy Office |
| Q15 | Exclusions | Sensible defaults, editable per folder |
| Q16 | Background | Tray, pause on battery (configurable), Pause button |
| Q17 | Vectors | 768-d float32 |
| Q18 | Ranking | Cosine + filename boost |
| Q19 | Results | One mixed list + type chips; Open, Show in folder, Find similar, Copy path |
| Q20 | Updates | Pins per release; MyGo signed auto-update; .deb, install script, Windows installer |
| Q21 | Name | Lookback |
| Q22 | First run | 3-step onboarding; search works during indexing |
| Q23 | Failure | Restart once, then CPU fallback with a notice |
| Q24 | Mix and match | Backend × text precision × encoder precision |
| Q25 | Test lab | Speed, memory and quality against BF16 references |
| Q26 | Combinations | Many installed, one active; generations for precision switches |
| Q27 | ETA | Token-weighted, separate text and image rates |
| Q28 | Themes | Omarchy, System, Catppuccin Mocha/Latte, One Dark/Light; instant switching |
| Q29 | UI style | Keyboard-first, photo-library grid, preview panel, compact |
| Q30 | Hotkey | Global shortcut + `lookback --toggle` |
| Q31 | Work split | Contracts first, then parallel work packages |
| — | Downloads | Resumable after crashes, verified by SHA-256 |
| — | Omarchy | Follows `colors.toml` live, including directory and symlink swaps |

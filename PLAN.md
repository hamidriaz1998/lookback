# Lookback: implementation plan for worker agents

This plan splits [SPEC.md](SPEC.md) into work packages (WPs) that agents can build in parallel. The UI
must meet [UI_GUIDE.md](UI_GUIDE.md). Read both before you start a package.

## How the work flows

```
M0 contracts + spikes (1 agent, blocks everything)
   │
   ├── WP1 catalog + downloads ─────────┐
   ├── WP2 hw + runtime + test lab ─────┤
   ├── WP3 store ───────────────────────┼──▶ WP8 index pipeline ──┐
   ├── WP4 extract + chunk ─────────────┘                         │
   ├── WP5 theme ─────────────────────────────────────────────────┤
   ├── WP6 UI (against fakes) ────────────────────────────────────┼──▶ WP9 integration ──▶ WP10 packaging
   └── WP7 tray, hotkey, toggle, power ───────────────────────────┘
```

WP1–WP7 run in parallel after M0. WP8 starts when WP2, WP3 and WP4 have merged. WP9 wires the real
implementations into the UI. WP10 ships it.

## Rules for every agent

1. **Stay inside your owned paths.** If you need a change elsewhere, open an issue or ask the M0 owner.
2. **Interfaces are frozen after M0.** Change one only through a small PR to `internal/contract`,
   approved by the M0 owner, which updates the fakes in the same PR.
3. **Tests:** `go vet ./...` and `go test ./...` pass before every PR. Each package ships at least one
   test that fails if its main logic breaks. No test framework beyond the stdlib `testing` package.
4. **Dependencies** only through `go get`. Never vendor or symlink by hand.
5. **No placeholders** in merged code: no TODO stubs on the main path, no fake numbers in the UI.
6. **Pull requests:** use the `pr-body` skill for the title and body. UI PRs attach screenshots (at
   least one dark and one light theme), as the repository's PR rules describe; never commit them.
7. **Keep SPEC.md true.** If you find the spec wrong, fix the spec in the same PR and say so in the PR body.

## Repository

- Folder: `~/Desktop/AI/lookback`. Go module: `github.com/hamidriaz1998/lookback`.
- Layout:

```
cmd/lookback/            main: flags (--toggle), starts internal/app
internal/contract/       shared types and interfaces (M0 owns)
internal/fake/           fakes of every interface (M0 owns; contract changes update them)
internal/{app,catalog,download,hw,runtime,bench,extract,chunk,index,watch,search,store,theme,ui}/
assets/sample/           bench images + text chunks (self-made or CC0) and BF16 reference vectors
tools/mkref/             computes the BF16 reference vectors at release time
```

## M0: contracts and spikes (one agent, first)

**Goal:** a compiling skeleton that every other package builds against, and proof that the three
riskiest assumptions hold.

### Deliverables

1. `go.mod` with Go 1.27+, MyGo and tursogo pinned to exact versions.
2. `internal/contract` with these types and interfaces (names are binding; fields may grow):

```go
package contract

type Backend string        // "cpu" | "vulkan" | "cuda12" | "cuda13"
type Quant string          // "q8_0" | "bf16"

type Combo struct {
    Backend   Backend
    TextQuant Quant
    EncQuant  Quant
}

type Kind string           // "image" | "text" | "pdf" | "office"

type FileRec struct {
    ID, FolderID int64
    Path         string
    Kind         Kind
    Size, MTime  int64
}

// Unit is one thing to embed: a text chunk or an image.
type Unit struct {
    FileID     int64
    Unit       int          // page, slide or sheet; 0 for whole files
    Start, End int          // byte range in the text; -1 for images
    Text       string       // chunk text without the prompt prefix (text units)
    Image      []byte       // PNG or JPEG bytes (image units)
    Snippet    string
    EstTokens  int          // for the ETA
}

type Item struct {         // a stored vector
    ID, Gen, FileID int64
    Unit            int
    Start, End      int
    Modality        string  // "text" | "image"
    Snippet         string
    Vector          []float32 // 768
}

type Hit struct {
    ItemID, FileID int64
    Path           string
    Kind           Kind
    Unit           int
    Snippet        string
    Score          float64
}

type Progress struct {
    FilesDone, FilesTotal int
    FilesPerSec, TokensPerSec float64
    ETA       time.Duration // < 0 while estimating
    Current   string
    PerFolder []FolderProgress
    Errors    int
    Paused    PauseReason   // "" | "user" | "battery" | "suspend"
}

type Embedder interface {
    EmbedTexts(ctx context.Context, prompts []string) (vecs [][]float32, tokens int, err error)
    EmbedImage(ctx context.Context, img []byte, mime string) (vec []float32, tokens int, err error)
    Tokenize(ctx context.Context, text string) ([]Token, error) // with pieces, for offsets
}

type Store interface {
    // folders, files, file_index, items, generations, combos, settings (see SPEC §10)
    Search(ctx context.Context, gen int64, query []float32, kinds []Kind, limit int) ([]Hit, error)
    Similar(ctx context.Context, gen int64, itemID int64, kinds []Kind, limit int) ([]Hit, error)
    // ... the rest is defined in M0 from SPEC §10
}

type Extractor interface {
    // Extract streams the units of one file; images come back already resized and encoded.
    Extract(ctx context.Context, f FileRec, emit func(Unit) error) error
}

type Indexer interface {
    Start(ctx context.Context) error
    Pause(reason PauseReason)
    Resume(reason PauseReason)
    Progress() <-chan Progress
}

type ThemeSource interface {
    ID() string
    Name() string
    Theme(base *ui.Theme) *ui.Theme
    Changed() <-chan struct{} // nil for fixed themes
}
```

3. `internal/fake`: a fake for each interface. The UI fake data must be realistic: 300 indexed images,
   a running indexing job with a moving ETA, two installed combinations with test results, one error.
4. **Spike 1, tursogo vectors: DONE (2026-10-21), passed.** See `docs/spikes.md`. Move
   `spikes/tursogo/spike_test.go` into `internal/store` as a regression test, and pin tursogo v0.8.3.
   Set `TURSO_GO_CACHE_DIR` to `<data root>/lib` in `internal/app` before the database opens.
5. **Spike 2, MyGo image grid (blocks WP6):** a window with a virtualized Grid view of 1,000 thumbnails
   decoded off the main thread, scrolling at 60 fps. Report the APIs used (`ui.Image`, Grid view,
   `Window.Update`).
6. **Spike 3, sidecar (blocks WP2):** start `llama-server` from the CPU build with the Q8_0 model,
   wait for `/health`, embed one text and one image, and stop it. Run it on Linux, and on Windows
   through GitHub Actions.
7. A CI workflow: vet, test, and the "no hex colors outside `internal/theme`" grep from UI_GUIDE §5.

**Done when:** the module builds on Linux and Windows; `go test ./...` passes; the three spike
reports are in `docs/spikes.md`.

## Parallel work packages

Each package lists what it owns, what it uses, what it provides, and how to prove it works. All of
them start after M0 merges.

### WP1: catalog and downloads

- **Owns:** `internal/catalog`, `internal/download`.
- **Uses:** SPEC §4, §7. `mygo.Network` for offline detection.
- **Provides:** `catalog.Assets(os, combo)` → files with URL, size and SHA-256;
  `download.Manager` with `Start`, `Pause`, `Resume`, `Progress()`, and resume-on-launch.
- **Tasks:** fill `catalog.json` with the pins from SPEC §4, including SHA-256 values for the llama.cpp
  assets (download each once and hash it). Implement `.part` + `.part.json`, Range resume, the 200
  fallback, verification, atomic rename, backoff, offline pause, and safe archive unpacking.
- **Acceptance:** a test with a local HTTP server that kills the download mid-file and resumes from the
  same byte; a test where the server ignores Range; a test with a wrong checksum; a test where an archive
  tries `../` paths and is refused.

### WP2: hardware, runtime and test lab

- **Owns:** `internal/hw`, `internal/runtime`, `internal/bench`, `tools/mkref`, `assets/sample`.
- **Uses:** SPEC §5, §6 (step 1 and 2 logic), §8. WP1's catalog (through the contract).
- **Provides:** `hw.Detect()`; `runtime.Sidecar` implementing `contract.Embedder` with restart and CPU
  fallback; `bench.Run(combo, extraFiles)` → the result row; `bench.Recommend(results)`.
- **Tasks:** Linux and Windows detection; `--list-devices` parsing; the CUDA driver-version rule
  (verify and record the minimum driver versions); sidecar flags, free port, API key, health, logs,
  process-group/job-object cleanup; the sample set (8 images, 16 chunks); BF16 reference vectors via
  `tools/mkref`; peak RAM and VRAM measurement.
- **Acceptance:** on the reference laptop, detection lists the Radeon iGPU through Vulkan; the bench
  ranks Vulkan above CPU; killing the server twice switches to the CPU and the embedder keeps working;
  parsing tests for `--list-devices` output.

### WP3: store

- **Owns:** `internal/store`, `internal/search`.
- **Uses:** SPEC §10, §12. The spike 1 findings.
- **Provides:** the `contract.Store` implementation with migrations; `search.Run(query)` (embed the
  query, search, group by file, filename boost); `search.Similar(itemID)`.
- **Acceptance:** tests for grouping, the boost cap, type filters, generation isolation (a `building`
  generation's items never appear in search), deleting a file's rows, and the 100k-item timing.

### WP4: extraction and chunking

- **Owns:** `internal/extract`, `internal/chunk`.
- **Uses:** SPEC §9.
- **Provides:** one `contract.Extractor` per kind and a registry by extension; `chunk.Split` with
  token-exact windows (it takes a tokenizer function, so tests run without the sidecar).
- **Tasks:** images (resize, re-encode, GIF first frame, header-only size read); text (encoding check,
  binary skip, HTML); PDF via go-pdfium WASM (text per page, render low-text pages); DOCX, PPTX, XLSX,
  ODT, ODP, ODS, RTF. Measure the 1600 px image cap against the sample (time and quality) and record
  the numbers in the PR.
- **Acceptance:** fixture files for every type in `internal/extract/testdata` (self-made); tests check
  the unit count, page numbers and text; a scanned-page fixture comes back as an image unit; a chunking
  test checks the 512/64 windows and the byte offsets.

### WP5: themes

- **Owns:** `internal/theme`.
- **Uses:** SPEC §15.
- **Provides:** `theme.Builtins()`, `theme.Omarchy()` (Linux build tag; returns nil when the file is
  absent), and a `theme.Manager` holding the active `ThemeSource` and emitting changes.
- **Tasks:** the four fixed palettes and System; the Omarchy mapping with the contrast guard; the
  parent-folder watcher that handles directory replacement (`rm -rf` + `mv`), symlink swaps and in-place
  edits, with the debounce and retries.
- **Acceptance:** the contrast test from SPEC §15 over all built-ins and several `colors.toml` fixtures
  (including the reference machine's, which needs the muted guard); a watcher test in a temp folder
  that simulates both swap styles and an in-place edit, and expects exactly one change event per swap.

### WP6: UI

- **Owns:** `internal/ui`.
- **Uses:** UI_GUIDE.md (all of it), the contract interfaces, and `internal/fake` until WP9.
- **Provides:** every screen in UI_GUIDE §3, the theme picker, and the keyboard map.
- **Tasks:** build screen by screen against the fakes. Each screen meets the UI_GUIDE definition of
  done before the next one starts. Order: Search + preview, Activity, Onboarding 1–3, Folders, Models &
  Hardware, Settings.
- **Acceptance:** the UI_GUIDE §11 checklist per screen, with MyGo UI tests and screenshots in the PR.

### WP7: tray, hotkey, toggle, power

- **Owns:** `internal/app` (except wiring of other packages, which WP9 adds), `cmd/lookback`.
- **Uses:** SPEC §13, §16. MyGo tray, global shortcuts, `Power` and single-instance support.
- **Provides:** the tray menu; the global hotkey; `lookback --toggle` over the single-instance channel;
  battery, suspend and resume events forwarded as pause reasons; close-to-tray.
- **Acceptance:** `lookback --toggle` shows and hides a running instance on Linux and Windows; a test of
  the pause-reason logic (user, battery and suspend combine correctly); the Hyprland bind works on the
  reference machine.

## Sequential work packages

### WP8: indexing pipeline

- **Starts after:** WP2, WP3, WP4.
- **Owns:** `internal/index`, `internal/watch`.
- **Uses:** SPEC §11, §13 (watching), §10 (generations).
- **Provides:** the `contract.Indexer` implementation: the queue, the pipeline, generations, the
  startup rescan, folder watchers, pause reasons, and the token-weighted ETA.
- **Acceptance:** index the 40-image sample and check the ETA criterion (within 25% after 2 minutes);
  edit, add and delete files in a watched temp folder and see the index follow within 10 s; a precision
  switch builds a new generation while search serves the old one.

### WP9: integration

- **Starts after:** WP1–WP8.
- **Owns:** the wiring in `internal/app`.
- **Tasks:** replace the fakes with real implementations; run the whole first-run flow on a clean data
  folder; go through every SPEC §20 acceptance criterion and record the results in `docs/acceptance.md`.

### WP10: packaging and updates

- **Starts after:** WP9.
- **Tasks:** MyGo distribution for the `.deb`, the Linux install script and the Windows installer;
  signed auto-updates; a release workflow that also runs `tools/mkref` and embeds the reference vectors.
- **Acceptance:** install from each package on a clean machine (or VM) and complete onboarding.

## Suggested agent prompts

Start each worker with: "You implement `<WP>` of Lookback. Read SPEC.md, UI_GUIDE.md and PLAN.md in
`~/Desktop/AI/lookback`. Follow the rules in PLAN.md. Work only in your owned paths. Finish with the
acceptance checks passing and a PR."

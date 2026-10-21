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
2. **Interfaces are frozen after M0.** Change one only through a small commit to `internal/contract`,
   approved by the M0 owner, which updates the fakes in the same commit.
3. **Tests:** `go vet ./...` and `go test ./...` pass before every push. Each package ships at least one
   test that fails if its main logic breaks. No test framework beyond the stdlib `testing` package.
4. **Dependencies** only through `go get`. Never vendor or symlink by hand.
5. **No placeholders** in merged code: no TODO stubs on the main path, no fake numbers in the UI.
6. **Commits and pushes:** work on your own branch in your own worktree. Make a series of small,
   focused commits with clear messages, and push after each (`git push -u origin <branch>` the first
   time). Never push to `main` and never force-push. The orchestrator merges after an adversarial
   review. UI work saves screenshots (at least one dark and one light theme) to
   `/home/hamid/Desktop/AI/lookback/.review/screens/<branch>/`; never commit them.
7. **Keep SPEC.md true.** If you find the spec wrong, fix the spec in the same commit and say so in its message.
8. **Linux only for v1.** OS-specific code goes in `_linux.go` files; each gets a `_windows.go` twin that is
   a minimal stub returning `errors.ErrUnsupported`. Don't write Windows logic now (see SPEC.md "Windows
   phase"). `GOOS=windows CGO_ENABLED=0 go build ./...` must still pass.

## Checks

There is no hosted CI. Git hooks in `.githooks/` run the checks locally:

- **pre-commit** (a few seconds): `gofmt` on staged Go files, `go vet ./...`, `go test -short` on the
  touched packages, and `scripts/check-colors.sh`.
- **pre-push** (about 30–90 s): `go vet`, the full `go test ./...`, `GOOS=windows go build ./...`, the
  color check, and `go test -race -short` (with cgo) on the packages the push changes.

The hooks are enabled for this clone and every worktree with `git config core.hooksPath .githooks`. Run
that once after cloning, including on Windows. Never bypass them with `--no-verify`. The orchestrator
runs the full checks again before merging into `main`.

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
    Paused    PauseReason   // "" | "user" | "battery" | "suspend" | "lab"
}

type Embedder interface {
    EmbedTexts(ctx context.Context, prompts []string) (vecs [][]float32, tokens int, err error)
    EmbedImage(ctx context.Context, img []byte, mime string) (vec []float32, tokens int, err error)
    Tokenize(ctx context.Context, text string) ([]Token, error) // with pieces, for offsets
}

type Store interface {
    // folders, files, file_index, items, generations, combos, settings (see SPEC §10)
    GenerationComplete(ctx context.Context, gen int64) (bool, error)  // the handover rule
    ActivateGeneration(ctx context.Context, gen int64) error          // ErrIncomplete when not complete
    Search(ctx context.Context, gen int64, query []float32, kinds []Kind, limit int) ([]Hit, error)
    Similar(ctx context.Context, gen int64, itemID int64, kinds []Kind, limit int) ([]Hit, error)
    // ... the rest is defined in M0 from SPEC §10. Values are copied on commit and on save.
}

type Extractor interface {
    // Extract streams the units of one file; images come back already resized and encoded.
    Extract(ctx context.Context, f FileRec, emit func(Unit) error) error
}

type Indexer interface {
    Start(ctx context.Context) error   // ctx is the job's lifetime; a done ctx is rejected
    Pause(reason PauseReason)          // reasons: user, battery, suspend, lab
    Resume(reason PauseReason)
    Subscribe(ctx context.Context) <-chan Progress
    SubscribeRebuild(ctx context.Context) <-chan Progress
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
   `Window.Update`). **Status:** decode passed; frame rate open, owned by WP6 (measure with MyGo
   Inspector). Recorded in `docs/spikes.md`.
6. **Spike 3, sidecar (blocks WP2):** start `llama-server` from the CPU build with the Q8_0 model,
   wait for `/health`, embed one text and one image, and stop it. Run it on Linux. **Status:** Linux BF16 passed; Q8_0 open, owned by WP2; the Windows run moved to
   the Windows phase. Recorded
   in `docs/spikes.md`.
7. Checks: the git hooks described under "Checks" below, and the "no hex colors outside `internal/theme`"
   grep from UI_GUIDE §5 (`scripts/check-colors.sh`).

**Done when:** the module builds on Linux and cross-builds for Windows; `go test ./...` passes; the three spike
reports are in `docs/spikes.md`. Spikes 2 and 3 have open gates, recorded there and owned by WP6 and
WP2, so M0 closes with them open.

**Interfaces M0 freezes** (`internal/contract`, each with a fake in `internal/fake`): `Embedder`,
`Session` (Embedder, Space, Close), `Store` (adds GenerationComplete), `Extractor`, `Indexer` (Start,
Pause, Resume, Retry, RetryAll, Rescan, Rebuild, Subscribe, SubscribeRebuild), `ThemeSource`, `Catalog`
(Assets, Backends), `Downloader` (Ensure, Pause, Resume, PauseAll, ResumeAll, Retry, Subscribe; keyed by
`Asset.Key()`), `Detector` (Detect, Probe), `Lab` (Run returns a `RunID`, Subscribe, Recommend), and
`Runtime` (Backend, Status, Subscribe, Session, Use, RetryGPU, Stop; it no longer embeds `Embedder`).
Streams share one rule, documented in `internal/contract`: each Subscribe gets its own channel, starts
with the snapshot, keeps terminal values, and closes with its ctx.

## Parallel work packages

Each package lists what it owns, what it uses, what it provides, and how to prove it works. All of
them start after M0 merges.

### WP1: catalog and downloads

- **Owns:** `internal/catalog`, `internal/download`.
- **Uses:** SPEC §4, §7. `mygo.Network` for offline detection.
- **Provides:** `contract.Catalog` (`Assets(os, combo)` → files with URL, size and SHA-256;
  `Backends(os)`), and `contract.Downloader` (`Ensure`, `Pause`, `Resume`, `PauseAll`, `ResumeAll`,
  `Retry`, `Subscribe()` with the states queued, downloading, paused, verifying, done, failed, offline).
  Assets are keyed by `Asset.Key()` (the SHA-256; the URL while an archive is unpinned). User pause and
  connection are separate flags, and the visible state comes from both (SPEC §7). Resume-on-launch.
- **Tasks:** fill `catalog.json` with the pins from SPEC §4, including SHA-256 values for the llama.cpp
  assets (download each once and hash it). Implement `.part` + `.part.json`, Range resume, the 200
  fallback, verification, atomic rename, backoff, offline pause, and safe archive unpacking.
- **Acceptance:** a test with a local HTTP server that kills the download mid-file and resumes from the
  same byte; a test where the server ignores Range; a test with a wrong checksum; a test where an archive
  tries `../` paths and is refused.

### WP2: hardware, runtime and test lab

- **Owns:** `internal/hw`, `internal/runtime`, `internal/bench`, `tools/mkref`, `assets/sample`.
- **Uses:** SPEC §5, §6 (step 1 and 2 logic), §8. WP1's `contract.Catalog` and `contract.Downloader`.
- **Provides:** `contract.Detector` (`Detect`, `Probe` per backend); `contract.Lab` (`Run(combo, extra)`
  returns a `RunID`, `Subscribe`, `Recommend`; runs form one FIFO queue, and each one starts its own temporary
  llama-server with its combination's backend and precisions, separate from the runtime, and pauses the
  indexer with `PauseLab`); `contract.Runtime`, implemented by `runtime.Sidecar`,
  which adds `Backend`, `Status`, `Subscribe`, `Session`, `Use`, `RetryGPU` and `Stop`; `Session` returns
  when ready and `Session.Ready` waits after a restart. `Session` embeds
  for one vector space; the sidecar keeps at most two spaces open (SPEC §8).
- **Tasks:** Linux detection; `--list-devices` parsing; the CUDA driver-version rule
  (verify and record the minimum driver versions); sidecar flags, free port, API key, health, logs,
  process-group cleanup; the sample set (8 images, 16 chunks); BF16 reference vectors via
  `tools/mkref`; peak RAM and VRAM measurement.
- **Acceptance:** on the reference laptop, detection lists the Radeon iGPU through Vulkan; the bench
  ranks Vulkan above CPU; killing the server twice switches to the CPU and the embedder keeps working;
  parsing tests for `--list-devices` output. Startup wait uses one context with a 120 s deadline,
  canceled on child exit, with short per-attempt timeouts and a retry on port collision (review r1 #12;
  `cmd/spike-sidecar` does not yet do this).
- **Also:** the Q8_0 sidecar run that spike 3 leaves open. The Windows job object belongs to the
  Windows phase.

### WP3: store

- **Owns:** `internal/store`, `internal/search`.
- **Uses:** SPEC §10, §12. The spike 1 findings.
- **Provides:** the `contract.Store` implementation with migrations. Activation is
  `ActivateGeneration` (one transaction, which checks `GenerationComplete` and returns `ErrIncomplete`).
  `ClearFileIndex` backs Retry. `CommitFile` allocates item IDs and copies vectors.
  `RecentItems` filters to indexed files before the limit. `Similar` returns `ErrStaleItem` for a seed
  outside the generation. `search.Run(query)` (embed the query, search, group by file, filename boost);
  `search.Similar(itemID)`.
- **Acceptance:** tests for grouping, the boost cap, type filters, generation isolation (a `building`
  generation's items never appear in search), deleting a file's rows, and the 100k-item timing.

### WP4: extraction and chunking

- **Owns:** `internal/extract`, `internal/chunk`.
- **Uses:** SPEC §9. `contract.Embedder.Tokenize` returns byte offsets; the fake's `ByteFallback` mode
  emits byte-fallback tokens for WP4's cut-inside-a-character test.
- **Provides:** one `contract.Extractor` per kind and a registry by extension; `chunk.Split` with
  token-exact windows (it takes a tokenizer function, so tests run without the sidecar).
- **Tasks:** images (resize, re-encode, GIF first frame, header-only size read); text (encoding check,
  binary skip, HTML); PDF via go-pdfium WASM (text per page, render low-text pages); DOCX, PPTX, XLSX,
  ODT, ODP, ODS, RTF. Measure the 1600 px image cap against the sample (time and quality) and record
  the numbers in the commit message.
- **Acceptance:** fixture files for every type in `internal/extract/testdata` (self-made); tests check
  the unit count, page numbers and text; a scanned-page fixture comes back as an image unit; the chunk
  regression (SPEC section 9) checks coverage, valid UTF-8, termination, overlap of at least 64 tokens and
  the 512-token cap, with byte offsets, on text that includes byte-fallback characters.

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
- **Uses:** UI_GUIDE.md (all of it), the contract interfaces, and `internal/fake` until WP9. Progress
  streams carry `Gen`; the rebuild card reads `Indexer.SubscribeRebuild`.
- **Provides:** every screen in UI_GUIDE §3, the theme picker, and the keyboard map.
- **Tasks:** build screen by screen against the fakes. Each screen meets the UI_GUIDE definition of
  done before the next one starts. Order: Search + preview, Activity, Onboarding 1–3, Folders, Models &
  Hardware, Settings.
- **Acceptance:** the UI_GUIDE §11 checklist per screen, with MyGo UI tests and screenshots saved as rule 6 describes.
  The 1,000-thumbnail grid holds 60 fps while scrolling, measured with MyGo Inspector on the reference
  laptop (review r1 #14; spike 2 stays open until this is recorded in `docs/spikes.md`).

### WP7: tray, hotkey, toggle, power

- **Owns:** `internal/app` (except wiring of other packages, which WP9 adds), `cmd/lookback`.
- **Uses:** SPEC §13, §16. MyGo tray, global shortcuts, `Power` and single-instance support.
- **Provides:** the tray menu; the global hotkey; `lookback --toggle` over the single-instance channel;
  battery, suspend and resume events forwarded as pause reasons; close-to-tray.
- **Acceptance:** `lookback --toggle` shows and hides a running instance on Linux; a test of
  the pause-reason logic (user, battery and suspend combine correctly); the Hyprland bind works on the
  reference machine.

## Sequential work packages

### WP8: indexing pipeline

- **Starts after:** WP2, WP3, WP4.
- **Owns:** `internal/index`, `internal/watch`.
- **Uses:** SPEC §11, §13 (watching), §10 (generations).
- **Provides:** the `contract.Indexer` implementation: the queue, the pipeline, generations, the
  startup rescan, folder watchers, pause reasons, and the token-weighted ETA. The commands are
  `Retry`, `RetryAll`, `Rescan(folderID)` and `Rebuild(text, enc)`, and the streams are `Subscribe`
  (active generation) and `SubscribeRebuild`. Each file is embedded through a session for its
  generation's space. The handover rule is in SPEC §10.
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
- **Tasks:** MyGo distribution for the `.deb` and the Linux install script;
  signed auto-updates; a release workflow that also runs `tools/mkref` and embeds the reference vectors.
- **Acceptance:** install from each package on a clean machine (or VM) and complete onboarding.

## Windows phase

After the Linux build is tested, continue on a Windows boot of the same laptop: clone the repo,
then replace the `_windows.go` stubs with the items in SPEC.md "Windows phase". Plan it then, with
the same review loop.

## Suggested agent prompts

Start each worker with: "You implement `<WP>` of Lookback. Read SPEC.md, UI_GUIDE.md and PLAN.md in
`~/Desktop/AI/lookback`. Follow the rules in PLAN.md. Work only in your owned paths. Finish with the
acceptance checks passing and every commit pushed to your branch."

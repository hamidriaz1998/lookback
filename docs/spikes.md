# Spike results

## Spike 1: tursogo vector search (done, 2026-10-21)

**Result: passed.** tursogo supports everything SPEC §10 needs. The storage design stands.

- **Setup:** `turso.tech/database/tursogo` v0.8.3 (native library from
  `github.com/tursodatabase/turso-go-platform-libs` v0.8.3), Go 1.27.2, `CGO_ENABLED=0`, Linux x64,
  Ryzen 7 5700U.
- **Test:** `internal/store/vector_regression_test.go` (moved from `spikes/tursogo` in M0). Run with
  `CGO_ENABLED=0 go test -v ./internal/store/` (`-short` skips the 100k timing).

| Check | Result |
|---|---|
| `vector32(?)` with a JSON array parameter, stored as a BLOB | Works; the blob is 3,072 bytes (768 × float32) |
| The SPEC §10 search query (JOIN + `vector_distance_cos` + `ORDER BY d LIMIT 300` + generation filter) | Top 50 match a brute-force cosine in Go, in order, distances within 1e-5 |
| Generation isolation | No item from a `building` generation appears |
| Find similar with the `(SELECT embedding …)` subquery, excluding the same file | Top 10 match brute force |
| Search over 100k items (90k in the active generation) | 223–229 ms per query (budget: < 1 s) |
| Insert 100k items in one transaction | 15.5 s (0.15 ms per item; negligible next to embedding) |
| Cross-build `GOOS=windows GOARCH=amd64 CGO_ENABLED=0` | Builds (PE32+ x86-64). Not yet run on Windows |

### Packaging facts found on the way

- tursogo is pure Go at build time but loads a native library at run time through purego. The library
  is **embedded in the binary** (`libturso_sync_sdk_kit.so` 20.7 MB on Linux, `turso_sync_sdk_kit.dll`
  16.1 MB on Windows), so expect the app binary to grow by about that much.
- On first use it extracts the library to `os.UserCacheDir()/turso-go/<hash>/` and checks its
  SHA-256 on every later start. `TURSO_GO_CACHE_DIR` overrides the location.
- Decision for Lookback: set `TURSO_GO_CACHE_DIR` to `<data root>/lib` before opening the database, so
  all app files stay in one place and uninstall removes them.
- Open item for the Windows phase (SPEC.md, not WP10): run the Windows build on a real machine, and check
  that SmartScreen or antivirus doesn't block loading the extracted DLL.

## Spike 2: MyGo image grid

**Status: decode passed. The 60 fps scroll gate is open and is owned by WP9.** The grid draws 1,000
thumbnails from the real screenshots, decoded off the main thread, and the view updates through
`Window.Update`. The scroll measurement below covers a preloaded spike only; it does not close the gate.

- **Setup:** `github.com/egoist/mygo` v0.6.0 (`go.mod`), Go 1.27.1 toolchain, Linux (Wayland session,
  Hyprland), Ryzen 7 5700U (8 cores, 16 threads). Program: `cmd/spike-grid/main.go`. Run with
  `timeout 8 go run ./cmd/spike-grid`, or a `CGO_ENABLED=0` build.
- **Source:** the 293 `screenshot*.png` files in `~/Pictures`, repeated to 1,000 items. Each item is decoded
  on one of 16 worker goroutines and downscaled to a 320 px long edge (nearest-neighbour sampling).

| APIs used | Where |
|---|---|
| `mygo.App.WhenReady`, `mygo.NewWindow(mygo.WindowOptions{Title, Width, Height, Content})`, `mygo.App.Run` | `main` |
| `ui.View(func(c *ui.Context))` as `Content` | window |
| `ui.GridView(c, *ui.GridState, n, minWidth, height, item)`: virtualized, built only as it shows | view |
| `ui.Column(c).Fill().Padding().Gap().Children(fn)`, `ui.Text(c, s).SingleLine()` | view |
| `ui.NewBitmap(*image.RGBA)`, `ui.Image(c, bm).Fit(ui.Contain).Grow(1).Radius(8)` | view |
| `(*mygo.Window).Update(fn)`: one call per burst of decoded thumbnails, on the main thread | `load` |

| Measurement | Result |
|---|---|
| Thumbnails decoded and shown | 1,000 of 1,000 (log: "1000 of 1000 thumbnails decoded") |
| Time to first bitmap in the grid | 136 ms (`go run`), 10 ms and 180 ms (`CGO_ENABLED=0` builds, two runs) |
| Time to all 1,000 bitmaps | 1.24 s (`go run`), 1.23 s and 1.21 s (`CGO_ENABLED=0` builds) |
| Build | `CGO_ENABLED=0 go vet ./...` and `GOOS=windows CGO_ENABLED=0 go build ./...` pass |
| Screenshot (grim) | Grid shows real thumbnails with names, labels truncate on one line |

Findings and limits:

- **60 fps: open.** The first run had no frame counter and did not script scrolling. The later
  preloaded autoscroll run below measured frame work only. The full gate is still open for WP9: the
  production grid and thumbnail cache, with indexing running at full speed, measured on presentation
  cadence (UI guide section 10).
- **WP6 check (2026-10-21):** `MYGO_FRAME_STATS=8 go run ./cmd/spike-grid` decoded all 1,000 thumbnails
  in 2.954 s. The frame log included frames over 8 ms during decoding, including a 27.0 ms frame; this
  was not a scrolling measurement. The window session locked before Inspector-based scrolling could be
  performed, so the 60 fps gate remains open.
- **WP6 retry (2026-10-21):** built `cmd/spike-grid` with `CGO_ENABLED=0` and launched it on the owned
  Hyprland workspace (`16 silent`). The client was tiled at 1896×1025. The process was stopped after
  `timeout 5 grim -T <stableId>` timed out because the hidden static window did not produce a frame.
  No Inspector scrolling was performed and no FPS result was measured; the 60 fps gate remains open.
- **Scroll frame work, preloaded spike (orchestrator, 2026-10-21).** Build of `cmd/spike-grid` at wp6
  `aa4f1b3` with `LOOKBACK_AUTOSCROLL=8 MYGO_FRAME_STATS=all`, shown on workspace 16 at 1896×1025 on the
  60.05 Hz eDP-1 panel (temporary visible workspace, approved by the user; no simulated input).
  - **What was measured:** frame work time from MyGo's frame log (`time.Since(f.start)`), not
    display-to-display presentation intervals. Over 8 s: 60 frames in every full second; 472 frames in
    total, all under 16.7 ms of work (p50 4.7, p95 12.3, p99 14.3, max 16.2 ms). One GC cycle.
  - **What the run did not cover:** autoscroll started only after all 1,000 thumbnails were loaded, so
    no decoding overlapped it. No indexer, no production grid, and no thumbnail cache ran. No wheel or
    key input was simulated.
  - **Content changes:** 242 of the 472 frames drew new content. The run used a fixed step of 5 items.
    At 1896 px the grid fits 10 columns, so 5 items moved one row every other frame. About half the
    frames changed content, and the rest repainted the same rows. The spike now computes the step from
    the window's content width each frame, so it moves one row per frame. That change has not been run.
  - The margin at the slowest frame is thin (0.5 ms). WP9 should repeat the run with real thumbnails
    from the `thumbs/` cache and while indexing.
- `ui.NewBitmap` runs for each of the 1,000 items. Duplicates share no bitmap, so the GPU holds 1,000
  textures of 320 px, about 230 MB of RGBA in memory. The app needs the thumbnail cache of UI guide
  section 10 (`thumbs/`), and a bitmap budget.
- MyGo's docs say to make bitmaps once and not inside the view. The spike follows that: the view only
  reads the `*ui.Bitmap` slice.

## Spike 3: sidecar on Linux (Windows run moved to the Windows phase)

**Status: Linux BF16 passed; Q8_0 open, owned by WP2; Windows moved to the Windows phase (SPEC.md).** The Linux run used the BF16 pair,
which is the only pair on disk. The Q8_0 run and the Windows run are not done, so Spike 3 is not closed.

- **Program:** `cmd/spike-sidecar/main.go`, with `process_unix.go` (process group, `SIGTERM` then `SIGKILL`)
  and `process_windows.go`. Run: `go run ./cmd/spike-sidecar -log <file>`.
- **Command:** `llama-server` from `llama-b11755` (CPU build), with `-m embeddinggemma-2-BF16.gguf`,
  `--mmproj mmproj-embeddinggemma-2-BF16.gguf`, `--embeddings --pooling mean -c 8192 -t 8`,
  `--host 127.0.0.1 --port <free>`, `--api-key <32 random bytes, hex>`.
- **Models:** the BF16 pair. The PLAN text says Q8_0, but the command given for this spike and the local
  `models/` folder hold only BF16 files. Q8_0 has not been tested here.

| Check | Result |
|---|---|
| `GET /health` with Bearer key | `ok` after 2.26 s and 2.52 s (two runs) |
| Text `task: search result \| query: test` | 768 dims, L2 norm 1.0000, `prompt_tokens` 10, 30 to 33 ms |
| Image, 47.6 KB PNG (`Screenshot_2026-07-18_17-50.png`) | 768 dims, norm 1.0000, `prompt_tokens` 147, 2.98 s |
| Image, 1.59 MB PNG (`screenshot-2026-08-25_22-17-07.png`, largest in `~/Pictures`) | 768 dims, norm 1.0000, `prompt_tokens` 235, 5.11 s (sent at full size; no resize) |
| Stop | SIGTERM to the process group; the server exited with status 0 in both runs |

Findings and limits:

- The image request is one `data:image/png;base64` URL in the content array, as SPEC section 8 says.
  The reply has `data[0].embedding` (768 values) and `usage.prompt_tokens`.
- A server that exits during the health wait returns an error at once. The stop logic reads a closed
  channel, so it does not block (found in review and fixed before the run).
- **Windows:** `process_windows.go` only kills the process. It has no job object, so the server can
  outlive a crash of the app. The app needs a job object (SPEC section 8). The spike cross-compiles for
  `windows/amd64` only.
- **Checks:** there is no hosted CI. Git hooks in `.githooks/` run the checks locally (see PLAN.md
  "Checks"); the pre-push hook includes the Windows cross-build. Nothing starts `llama-server` on Windows,
  so the Windows sidecar path is still unverified (Windows phase).
- Failure-path tests (kill twice, CPU fallback) belong to WP2, not this spike.

### Follow-up checks (run before the contract froze)

- **Tokenizer offsets.** `/tokenize` with `with_pieces: true` on `naïve 日本語 🙂 ꙮ𓀀` returns byte-fallback
  pieces as byte arrays: `ꙮ` (3 bytes) comes back as three one-byte tokens `[234]`, `[153]`, `[174]`, and
  `𓀀` (4 bytes) as four. A token boundary can fall inside a character, so a rune range cannot describe
  every token. **Decision:** `Token`, `Unit` and `Item` offsets are byte offsets into the UTF-8 text, and
  `Token.Piece` holds the raw bytes. SPEC sections 9 and 10 say so.
- **Crash path.** With only `Setpgid`, `kill -9` on the app left `llama-server` running, re-parented to
  init. The sidecar now sets `Pdeathsig: SIGKILL` and locks the main thread. After `kill -9` the
  server is gone, and a normal run still stops it with SIGTERM. SPEC section 8 says so.
- **WP2 note:** `Pdeathsig` is Linux only. The Windows path needs a job object with
  `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`; it is not written yet.

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

**Status: decode passed; frame rate open, owned by WP6** (measure with MyGo Inspector). The grid draws
1,000 thumbnails from the real screenshots, decoded off the main thread, and the view updates through
`Window.Update`. The 60 fps scrolling gate of PLAN.md M0 was not measured, so Spike 2 is not closed.

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

- **Not measured: 60 fps.** The run had no frame counter, and the spike did not script scrolling. The
  budget (UI guide section 10) still needs a check: scroll with MyGo's Inspector, or a scripted run.
- `ui.NewBitmap` runs for each of the 1,000 items. Duplicates share no bitmap, so the GPU holds 1,000
  textures of 320 px, about 230 MB of RGBA in memory. The app needs the thumbnail cache of UI guide
  section 10 (`thumbs/`), and a bitmap budget.
- MyGo's docs say to make bitmaps once and not inside the view. The spike follows that: the view only
  reads the `*ui.Bitmap` slice.

## Spike 3: sidecar on Linux (Windows run moved to the Windows phase)

**Status: Linux BF16 and Q8_0 passed; Windows moved to the Windows phase (SPEC.md).** Both Linux precision pairs
were run with the CPU build. The Windows run remains open for that phase.

- **Program:** `cmd/spike-sidecar/main.go`, with `process_unix.go` (process group, `SIGTERM` then `SIGKILL`)
  and `process_windows.go`. Run: `go run ./cmd/spike-sidecar -log <file>`.
- **Command:** `llama-server` from `llama-b11755` (CPU build), with `-m embeddinggemma-2-BF16.gguf`,
  `--mmproj mmproj-embeddinggemma-2-BF16.gguf`, `--embeddings --pooling mean -c 8192 -t 8`,
  `--host 127.0.0.1 --port <free>`, `--api-key <32 random bytes, hex>`.
- **Models:** the BF16 pair for the original run. The follow-up Q8_0 run used only the already-present pair in
  `/home/hamid/Desktop/AI/lookback-data/models/`; no model was downloaded. Both file hashes match SPEC §4.

| Check | Result |
|---|---|
| `GET /health` with Bearer key | `ok` after 2.26 s and 2.52 s (two runs) |
| Text `task: search result \| query: test` | 768 dims, L2 norm 1.0000, `prompt_tokens` 10, 30 to 33 ms |
| Image, 47.6 KB PNG (`Screenshot_2026-07-18_17-50.png`) | 768 dims, norm 1.0000, `prompt_tokens` 147, 2.98 s |
| Image, 1.59 MB PNG (`screenshot-2026-08-25_22-17-07.png`, largest in `~/Pictures`) | 768 dims, norm 1.0000, `prompt_tokens` 235, 5.11 s (sent at full size; no resize) |
| Stop | SIGTERM to the process group; the server exited with status 0 in both runs |

### Q8_0 follow-up (2026-10-21)

**Result: passed.** Ran the CPU sidecar with the local `Q8_0.ready` model pair and bundled `img-01.png`:

```text
health: ok after 4.782s
text: dims=768 norm=1.0000 prompt_tokens=10 wall=74ms
image: img-01.png (6237 bytes) dims=768 norm=1.0000 prompt_tokens=134 wall=6.143s
stop: llama-server exited after SIGTERM (<nil>)
```

Command: `go run ./cmd/spike-sidecar -server /home/hamid/Desktop/AI/embeddingGemma_test/llama-cpu/llama-b11755/llama-server -m /home/hamid/Desktop/AI/lookback-data/models/embeddinggemma-2-Q8_0.gguf -mmproj /home/hamid/Desktop/AI/lookback-data/models/mmproj-embeddinggemma-2-Q8_0.gguf -image assets/sample/images/img-01.png -log /tmp/wp2-q8-sidecar.log`.
The model file SHA-256 values were `2188ac1deca4b77dffefd603c2776a9d76d9d74ec01841392982ebb840b09135` (text) and
`c4a8a52691ecef40618438928bdf9e68379b854e24166f292592353db0aab64f` (encoder). The server exited cleanly;
the temporary log is at `/tmp/wp2-q8-sidecar.log`.

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

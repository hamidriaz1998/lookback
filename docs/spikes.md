# Spike results

## Spike 1: tursogo vector search (done, 2026-10-21)

**Result: passed.** tursogo supports everything SPEC §10 needs. The storage design stands.

- **Setup:** `turso.tech/database/tursogo` v0.8.3 (native library from
  `github.com/tursodatabase/turso-go-platform-libs` v0.8.3), Go 1.27.2, `CGO_ENABLED=0`, Linux x64,
  Ryzen 7 5700U.
- **Test:** `spikes/tursogo/spike_test.go`. Run with `CGO_ENABLED=0 go test -v ./...` in that folder
  (`-short` skips the 100k timing).

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
- Open item for WP10: run the Windows build on a real machine, and check that SmartScreen or antivirus
  doesn't block loading the extracted DLL.

## Spike 2: MyGo image grid

Not started (M0).

## Spike 3: sidecar on Linux and Windows

Partly covered by the prototype on Linux (`~/Desktop/AI/embeddingGemma_test`); the Windows run is not started (M0).

# Acceptance (SPEC section 20)

Run on the reference laptop (Ryzen 7 5700U, Radeon RENOIR, 23 GB, Omarchy/Hyprland) on 2026-10-21 by the
orchestrator, against branch `wp9` (all work packages merged). The data folder was a separate smoke folder
(`lookback-data/smoke`) whose model files are SHA-256-verified reflink copies of the local BF16 and Q8_0
pairs; no model was downloaded. Items not run are open; each one has a followup in `docs/followups.md`.

| # | Criterion | Status | Evidence |
|---|---|---|---|
| 1 | Download resumes from the same byte after a kill, and verifies | Pass (test) | `internal/download` `TestRealCPURuntimeResume`: real 22 MB runtime archive interrupted at ~4.2 MB, resumed, SHA-256 verified (passes in the gates; one slow-network timeout recorded as a followup). Models not re-tested (no model downloads). |
| 2 | Onboarding recommends Vulkan with BF16; the lab table fills in | Partly | Live app, clean data folder: hardware detected, CPU and Vulkan runtimes downloaded in ~1 min, recommendation "Vulkan · BF16 · BF16" (`.review/screens/wp9/after-download.png`). The lab table was not driven live (keys cannot reach a window on a hidden workspace); lab logic is covered by WP2/WP6 tests. |
| 3 | 277 reference screenshots; "an error message" finds "Connection activation failed" in the top 3 | Not run | Needs a full index of the reference set (~25 min on CPU). Smaller live run below. |
| 4 | ETA within 25% after 2 min of the 40-image sample | Not run | ETA is reported live (see run below); the 40-image timing check is open. |
| 5 | Scanned PDF page found by description | Not run | PDF page rendering in the preview needs the WP9 renderer (followup); extraction of low-text pages as images is tested in WP4. |
| 6 | Edited watched text file searchable within 10 s | Pass (test) | WP8 `internal/index` watcher tests: edit/add/delete reach the index in about 1 s (fake embedder). |
| 7 | `omarchy-theme-set` recolors within 1 s, no restart | Pass (test) | WP5 theme watcher tests and WP6 `TestThemeLiveRecolor/omarchy` (colors.toml edited in place). Not run against the live desktop, to leave the user's theme alone. |
| 8 | Kill llama-server twice: CPU fallback, toast, indexing continues | Pass (test) | WP2 runtime tests (restart, fallback after repeated failures); earlier real kill/restart/CPU-fallback run in WP2 acceptance. Toast not checked live. |
| 9 | Unplug AC: indexing pauses; replug resumes | Partly | WP7 pause reasons and battery reconciliation tests; the real indexer now receives pause reasons. Not run with a physical unplug. |
| 10 | `lookback --toggle` shows or hides the window | Pass (live) | Live run on `main`: toggle hid the window, the second toggle showed it; a second plain launch exits (single instance). |
| 11 | Precision switch builds a new index while search uses the old one | Pass (test) | WP8 rebuild/handover tests; search keeps the old generation until handover. |
| 12 | Every screen passes UI_GUIDE definition of done | Partly | 133 tester snapshots in 7 themes (`.review/screens/wp6/`), two Codex review rounds. Open UI followups are listed. |

## Live end-to-end run (real services, headless)

`go run ./tools/e2e -data-dir lookback-data/smoke -folder lookback-data/smoke-folder <queries>` opens the same
services as the window (`app.OpenServices`), records a finished first run (CPU, Q8_0/Q8_0), and indexes a
folder of 19 real screenshots through extract → chunk → real llama-server → store, then searches through the
real Searcher.

- Indexed 17 of 17 tracked files, 0 errors, in 271 s on CPU Q8_0 while the machine was under heavy load
  (several test suites running). Progress and ETA streamed throughout. No llama-server was left running.
- Queries took 52–62 ms each. "shopping cart order total" → an order-queue screenshot with prices (top 1);
  "terminal error message" → a terminal with git output (top 1). "cat" and "bar chart" have no matching
  image in this sample and return low, flat scores.
- 19 files were in the folder but 17 were tracked; why 2 were skipped is a followup.

## Packaging (WP10)

`mygo build -platform linux/amd64` builds `lookback_0.1.0_amd64.deb` (depends on GTK 3 and
`libayatana-appindicator3-1`), a tarball, and `scripts/install.sh` (`--prefix`, `--uninstall`). Installed into
a temporary prefix only; not installed on the live system.

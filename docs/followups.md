# Follow-ups

Medium and low review findings deferred under the review rule (from round 3 on, only critical and high
findings block a merge). Each item names its owner. The full findings are in the review files under
`.review/` on the orchestrator's machine.

## Windows phase (open after the code work of 2026-11-09)

The code, tests and docs for the work order are on the `windows-support` branch. These checks and decisions
are still open. Steps 14 and 15 of the work order (the full acceptance run on a clean account, and the 0.3.0
release with the Linux and Windows packages) belong to the maintainer.

| Item | Owner | What to do |
|---|---|---|
| Acceptance list run by hand | maintainer | Done here: the app starts, onboarding shows the CPU, RAM and GPU, the hotkey registers and is released, `--toggle` works, the sidecar dies when the app is killed. Item 14 is checked at the `ShellExecuteW` level (the call MyGo's `OpenURL` makes) for names with spaces, `%`, `#`, brackets, accented and Japanese characters, and a UNC path through the admin share; Windows does not decode UTF-8 percent-escapes in `file:` URLs, so non-ASCII characters stay raw in them. Still to do: items 2, 3 (test lab and Vulkan recommendation), 6, 7, 8, 9 (10,000-file overflow), 10 (OneDrive file), 11 (mapped drive), 14 (click Open and Show in folder in the app), 15, 16 (sign out and in), 17. |
| Spike 3 with BF16 on Windows | maintainer | `docs/spikes.md` has the Q8_0 run only. Run `LOOKBACK_LIVE_DIR=<dir> go test -run TestLive ./internal/runtime` with the BF16 pair. |
| SmartScreen text and Defender check | maintainer | Record the SmartScreen prompt text for the README, and check that Defender accepts tursogo's DLL in `%LOCALAPPDATA%\Lookback\lib` on a clean account. |
| Uninstaller page after completion | maintainer | MyGo declares its uninstaller pages before the include, so the "delete the data" page comes after "Uninstallation Complete" (decided 2026-11-09). The page and the Run-value cleanup were checked by hand; deleting the data with the box ticked was not driven by a script. A page before the uninstall needs a MyGo hook; ask upstream. `resources/installer.nsh` is also copied into the install folder, because everything in `resources` ships. |
| Linux Start at login by hand | maintainer | Work-order step 10: check the XDG autostart entry on the next Linux boot (on, sign out and in, off). |
| Free GPU memory on the first screen | maintainer | On Windows the registry gives the total dedicated memory only, so the first screen shows the total and the backend probe shows the free memory later. `IDXGIAdapter3::QueryVideoMemoryInfo` could fill it in; it was tried and not verified. Left as is on purpose: SPEC section 5 makes `llama-server --list-devices` the ground truth for free memory, and DXGI's budget differs on integrated GPUs, which would skew the 4 GB BF16 recommendation. |
| Tar symlink chains on Windows | maintainer | `TestUnpackRefusesChainedSymlinkEscape` is skipped on Windows: Windows runtimes are zips, which refuse links, so the tar path never runs there. Check `os.Root` behavior if tars ever ship for Windows. |
| Windows watcher overflow with a real copy | maintainer | `TestOverflowMarksBurstDirty` forces the `ERROR_NOTIFY_ENUM_DIR` path with a small buffer. The 64 KiB production buffer is still only exercised by the 10,000-file copy of acceptance 9. |

## From M0 review round 3

| Item | Owner | What to do |
|---|---|---|
| R3-#4: pre-push package discovery fails open | WP9 (integration), or whoever next touches `.githooks/` | Check every git and `go list` exit status (no `|| true`); with no verified base (new branch without `origin/main`, unknown remote sha), run race on all packages instead of skipping; drop deleted package dirs explicitly; use NUL-delimited paths. Extend `scripts/test-hooks.sh` with a missing-base and a deleted-plus-modified-package case. |
| R3-#5: deletion-only and go.mod-only commits skip vet | same | Trigger vet and tests in pre-commit when a staged change deletes a `.go` file or touches `go.mod`/`go.sum`; a module change tests all packages. Add both cases to `scripts/test-hooks.sh`. |

## From WP3 review round 3

| Item | Owner | What to do |
|---|---|---|
| R3-#1 (medium): stale SaveFolder updates recreate removed folders | WP9 (integration) | Make nonzero-ID folder saves update-only in the store and fake, returning `contract.ErrUnknownFolder` for missing IDs. Cover an asynchronous rules edit completing after folder removal in the shared contract suite and integration flow. |
| R3-#2 (low): SaveFolder rewrites AddedAt after a normal insert | WP9 (integration) | Populate the caller's initially zero AddedAt after insertion and preserve creation time during rule edits. Test returned/listed timestamps and a later ignore-rule update. |

## From WP1 review round 3

| Item | Owner | What to do |
|---|---|---|
| R3-#1: extraction accepts symlinks that resolve outside the runtime folder | WP9 | `os.Root.Symlink` does not validate targets (`internal/download/unpack.go:85-110`). After extraction and before writing the completion marker, resolve every symlink target root-confined (chains, forward references) and reject escapes. Add a tar that ends with `a -> .`, `b -> a/..` and assert rejection without a later write. See `.review/wp1-r3.md`. |

## From WP7 review round 2 (deferred by user decision, 2026-10-21)

| Item | Owner | What to do |
|---|---|---|
| R2-#1: rejected Wayland hotkey change drops the working shortcut and reports success | WP10 | MyGo v0.6.0 `Register` returns before the portal answers and exposes no accept/reject result. Get binding confirmation from MyGo (upstream API if needed), keep or restore the previous shortcut on rejection, allow retry, and add a regression for async rejection, status and retry. See `.review/wp7-r2.md`. |

## From WP7 review round 3

| Item | Owner | What to do |
|---|---|---|
| R3-#1: tray pause state diverges from Activity pause state | WP9 | The tray toggles WP7's cached `PauseUser`; WP6 Activity calls `Indexer.Pause/Resume(PauseUser)` directly. Route both through one owner, refresh the tray on either change, keep battery/suspend/lab reasons and the frozen Indexer interface. Add an integration regression: pause via tray, resume via Activity, and the reverse. See `.review/wp7-r3.md`. |

## From orchestrator gate runs

| Item | Owner | What to do |
|---|---|---|

## From WP4 review round 3

| Item | Owner | What to do |
|---|---|---|
| R3-#1 (medium): RTF group nesting can allocate hundreds of MB below the file-size limit | WP9 | Cap group depth and per-group state in the RTF parser; add a deeply nested input under the size limit and assert bounded allocation or a typed error. |
| R3-#2 (medium): the real PDFium interruption test can pass without entering the operation it interrupts | WP9 | Signal from inside the render call (or measure that the call started) before cancelling, so the test fails if the call never began. |
| R3-#3 (medium): the detached-startup shutdown test does not verify resource disposal | WP9 | After Close, wait for the adopted startup to finish and assert the PDFium instance/pool is released (no leaked goroutines or module). |
| R3-#4 (low): the PDF waiter regression depends on a cold-start deadline (raised from 15 s to 2 min on 2026-10-21 because parallel load failed pushes) | WP9 | Replace the wall-clock bound with a readiness signal so parallel load cannot fail it. |

See `.review/wp4-r3.md`.

## From WP2 review round 3 (final round under the 2026-10-21 deadline)

| Item | Owner | What to do |
|---|---|---|
| R3-#1 (high, fixed): picked archive could exhaust memory | done | `extractPick` now enforces a 64 MiB extracted-output budget (`TestPickExtractionHasAnOutputBudget`). Still better: embed in bounded batches as units arrive instead of retaining them. |
| R3-#2 (medium): BF16 quality compares two different versions of a picked file | WP2/WP9 | Copy each pick once per lab run and reuse that copy for every combination, so the BF16 reference and the tested run see the same bytes. See `.review/wp2-r3.md`. |
| R3-#3 (medium): a crash during a response body still has a raw-error race | WP2 | Map body-read errors after a child crash to the restart/stopped errors under the same lifetime/epoch lock as the request path; add a regression that kills the child mid-body. |

## WP10 deferred (2026-10-21 deadline)

| Item | Owner | What to do |
|---|---|---|
| Signed auto-updates | WP10 | Configure `updates` in `mygo.json` (`github` or `url`, keys from `mygo keygen`), publish the signed update archives and manifests, and check that a delta update installs. Needs the signing key in CI secrets. |
| Release workflow | WP10 | A GitHub Actions job that runs `mygo build -platform linux/amd64,windows/amd64` and publishes the `.deb`, tarball and `install.sh`. Also runs `tools/mkref` and embeds the reference vectors. |
| Reference vectors embedding (`tools/mkref`) | WP10 | Generate the reference vectors in the release job and embed them in the binary. Not started. |
| Windows installer | WP10 (Windows phase) | Out of scope on Linux. |

## From WP10 review round 1 (final round under the 2026-10-21 deadline)

| Item | Owner | What to do |
|---|---|---|
| R1-#1 (high, fixed): `.deb` lacked the tray library | done | `mygo.json` now declares `libayatana-appindicator3-1`. Still to add: a packaging check that fails if a rebuild drops it, and a prerequisite check/message in `scripts/install.sh` for the tarball/source paths. |
| R1-#2 (medium): accepted installation paths produce a broken desktop command | WP10 | Quote or reject install prefixes with spaces/special characters in the desktop entry `Exec` line; test with such a prefix. See `.review/wp10-r1.md`. |
| R1-#3 (medium): source installer registers the launcher outside the configured XDG data dir | WP10 | Honor `XDG_DATA_HOME` for the desktop entry and icon; test with it set. |

## WP9 remaining (2026-10-21 deadline)

- Folder edits (ignore list, `SaveFolder` with an ID) do not reach the indexer, so a changed ignore list needs a restart.
- The indexer starts only after the first installed combination (or at launch when one exists), because
  `index.Start` opens the runtime. Folder adds before that go to the store directly. A Start that fails (for example
  a crashed sidecar at launch) is logged and not retried until restart.
- Models on disk: the downloader's layout is `models/<revision>/<name>` and it writes `downloads/<sha>.done`
  markers. The flat `lookback-data/models/` files from the spike are not found, so a fresh download is needed there.
- `max_image_edge` is not applied to the extractor registry (`SetImageEdge`).
- Installed combinations are recorded from the downloader's stream. A combination verified in an earlier run is
  recorded only if the stream reports its files as done at startup.
- The runtime's starting backend is the onboarding choice; a later `Use` does not persist across restarts.
- The live smoke test (window, hotkey, tray, first run on a clean data folder) is not run by WP9.

## From WP6 review round 2 (final round under the 2026-10-21 deadline)

| Item | Owner | What to do |
|---|---|---|
| R2-#2 (medium): completing onboarding can reopen it, or persist completion before initialization succeeds | WP6 | Keep an explicit finishing state; stop stale snapshots from reopening onboarding; save completion only after initialization succeeds; test gated load and init-failure restart. See `.review/wp6-r2.md`. |
| R2-#3 (medium): adding the default thumbnail directory discards an injected page renderer | WP6 | In `New`, set `fm.CacheDir` and assign `d.Media = fm` so `Pages` survives; test a renderer passed through `New` with CacheDir omitted. See `.review/wp6-r2.md`. |
| RAM recommendation threshold implemented as 15 GiB MemTotal for SPEC's 16 GB; confirm | user | Confirm the 15 GiB MemTotal threshold for SPEC section 5's 16 GB CPU rule. |
| 60 fps gate with the production grid and thumbnail cache while indexing is still open (docs/spikes.md) | WP9 | Measure the production grid and thumbnail cache at 60 fps during indexing, then close the gate in docs/spikes.md. |

## From WP8 review round 1 (final round under the 2026-10-21 deadline)

| Item | Owner | What to do |
|---|---|---|
| R1-#7 (medium): removing the configured root leaves deleted files searchable | WP9 | `scanFolder` returns on a missing root before the deletion pass, and its caller ignores the error. Distinguish a confirmed missing root from permission or transient I/O errors, delete the tracked files on confirmed disappearance, and surface the unavailable-folder error. Add a test that deletes the root, not only a file inside it. See `.review/wp8-r1.md`. |
| R1-#8 (medium): progress counts stale rows as done and drops the running file from the ETA | WP8 | `snapshotLocked` counts every existing row in FilesDone, including stale ones. Count only rows that match the current size and mtime (the `NeedsWork` rule), and include the running file's remaining cost in the ETA. Add a snapshot test with stale rows and one in-flight file. See `.review/wp8-r1.md`. |

## From the orchestrator (2026-10-21 deadline)

| Item | Owner | What to do |
|---|---|---|
| Pre-push hook takes 3–8 min per push | WP10/maintenance | The hook exports each pushed commit to a fresh temp dir, so the Go test cache never hits. Export to a stable path per branch so the test cache works. (The real download test now runs only with `LOOKBACK_REAL_NET=1`.) |
| Silent GUI driving does not work | WP9 | Hyprland `send_shortcut` to the window on a hidden workspace does not reach MyGo's keyboard handling, so onboarding cannot be driven without showing the window. Add a MyGo-Tester-based end-to-end test over the real `OpenServices` instead (models from a reflinked data dir). |
| Smoke data dir needed reflinked models | WP9 | The downloader expects `models/<revision>/<file>` and `downloads/<sha>.done`; the verified flat files in `lookback-data/models` are not found. Add an "import existing model files" path that verifies SHA-256 and records them. |
| Smoke run tracked 17 of 19 files in the folder | WP9 | Find why two screenshots in `lookback-data/smoke-folder` were not tracked (exclusion, duplicate content, or a scan race) and fix or document it. |
| SPEC §20 items 3, 4, 5 not run; 2, 9, 12 partly | WP9 | See `docs/acceptance.md`: full 277-image reference run, 40-image ETA check, scanned PDF query, live lab table, physical AC unplug. |

## From WP9 review round 1 (final round under the 2026-10-21 deadline)

| Item | Owner | What to do |
|---|---|---|
| R1-#4 (medium): first-building search does not refresh as files arrive | WP9 | While the first generation builds, re-run the current query (throttled) when progress reports new files, so results grow without retyping. See `.review/wp9-r1.md`. |
| R1-#5 (medium): settings changes never reach the app shell | WP9 | Route hotkey and battery-pause setting writes from the UI to `app.ApplySettings`, so the shell re-registers the shortcut and updates pause policy without restart. |
| R1-#2/#3 (high, fixed in code at 27b88e5, untested) | WP9 | Add the regression tests the fixes lack: a folder save during a gated `index.Start` is scanned and watched; `Services.Close` returns promptly while a PDF extraction/embedding is blocked, and the sidecar exits. |
| Data folders without `onboard_combo` no longer start indexing | WP9 | Installs made before the first-run fix have no `onboard_combo`; derive it from the active generation's space (or the only installed combination) at startup. |

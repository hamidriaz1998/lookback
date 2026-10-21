# Follow-ups

Medium and low review findings deferred under the review rule (from round 3 on, only critical and high
findings block a merge). Each item names its owner. The full findings are in the review files under
`.review/` on the orchestrator's machine.

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
| `TestRealCPURuntimeResume` times out on a slow network | WP9 | It failed once (57 s, "timed out waiting for" the runtime archive) and passed on two reruns (27 s, 33 s). Give the resume wait a deadline that scales with the archive size, or skip the real download under `-short`, so a slow link does not fail the gate. |

## From WP4 review round 3

| Item | Owner | What to do |
|---|---|---|
| R3-#1 (medium): RTF group nesting can allocate hundreds of MB below the file-size limit | WP9 | Cap group depth and per-group state in the RTF parser; add a deeply nested input under the size limit and assert bounded allocation or a typed error. |
| R3-#2 (medium): the real PDFium interruption test can pass without entering the operation it interrupts | WP9 | Signal from inside the render call (or measure that the call started) before cancelling, so the test fails if the call never began. |
| R3-#3 (medium): the detached-startup shutdown test does not verify resource disposal | WP9 | After Close, wait for the adopted startup to finish and assert the PDFium instance/pool is released (no leaked goroutines or module). |
| R3-#4 (low): the PDF waiter regression depends on a 15 s cold-start deadline | WP9 | Replace the wall-clock bound with a readiness signal so parallel load cannot fail it. |

See `.review/wp4-r3.md`.

## From WP8 review round 1 (final round under the 2026-10-21 deadline)

| Item | Owner | What to do |
|---|---|---|
| R1-#7 (medium): removing the configured root leaves deleted files searchable | WP9 | `scanFolder` returns on a missing root before the deletion pass, and its caller ignores the error. Distinguish a confirmed missing root from permission or transient I/O errors, delete the tracked files on confirmed disappearance, and surface the unavailable-folder error. Add a test that deletes the root, not only a file inside it. See `.review/wp8-r1.md`. |
| R1-#8 (medium): progress counts stale rows as done and drops the running file from the ETA | WP8 | `snapshotLocked` counts every existing row in FilesDone, including stale ones. Count only rows that match the current size and mtime (the `NeedsWork` rule), and include the running file's remaining cost in the ETA. Add a snapshot test with stale rows and one in-flight file. See `.review/wp8-r1.md`. |

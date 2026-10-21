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

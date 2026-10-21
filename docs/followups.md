# Follow-ups

Medium and low review findings deferred under the review rule (from round 3 on, only critical and high
findings block a merge). Each item names its owner. The full findings are in the review files under
`.review/` on the orchestrator's machine.

## From M0 review round 3

| Item | Owner | What to do |
|---|---|---|
| R3-#4: pre-push package discovery fails open | WP9 (integration), or whoever next touches `.githooks/` | Check every git and `go list` exit status (no `|| true`); with no verified base (new branch without `origin/main`, unknown remote sha), run race on all packages instead of skipping; drop deleted package dirs explicitly; use NUL-delimited paths. Extend `scripts/test-hooks.sh` with a missing-base and a deleted-plus-modified-package case. |
| R3-#5: deletion-only and go.mod-only commits skip vet | same | Trigger vet and tests in pre-commit when a staged change deletes a `.go` file or touches `go.mod`/`go.sum`; a module change tests all packages. Add both cases to `scripts/test-hooks.sh`. |

## From WP1 review round 3

| Item | Owner | What to do |
|---|---|---|
| R3-#1: extraction accepts symlinks that resolve outside the runtime folder | WP9 | `os.Root.Symlink` does not validate targets (`internal/download/unpack.go:85-110`). After extraction and before writing the completion marker, resolve every symlink target root-confined (chains, forward references) and reject escapes. Add a tar that ends with `a -> .`, `b -> a/..` and assert rejection without a later write. See `.review/wp1-r3.md`. |

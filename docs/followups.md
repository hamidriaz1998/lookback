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

# WP6 UI report

Branch: `wp6`  
Worktree: `/home/hamid/Desktop/AI/lookback-wt/wp6`

## Delivered

- Onboarding steps 1–3: hardware detection and recommendation, combination test lab, download controls and states, folder selection, skipped-file disclosure, and the start-indexing path.
- Models & Hardware: active vector space, installed combinations and actions, precision rebuild confirmation, rebuild progress, comparison entry point, and CPU fallback recovery.
- Settings: theme previews, indexing preferences, shortcut choices and copyable Hyprland binding, storage usage/log access, and version/model details.
- Lab rows mark the recommended result, prevent choosing failed tests, and explain unavailable quality comparisons. Download rows show paused/offline/resumed states and allow retry after failure.
- MyGo tester snapshots cover all existing screens plus onboarding empty/selected states and Models rebuild/switch/fallback states.

## Screenshot evidence

MyGo `ui.Tester` generated 38 PNGs (19 cases × system dark/light) at 1896×1025. They are in:

`/home/hamid/Desktop/AI/lookback/.review/screens/wp6/`

Files use `<case>-<dark|light>.png`; the onboarding and Models states are `onboarding-*`, `models-*`, and `settings-*`. These screenshots are local review artifacts and are not committed.

## Checks

- `go test ./internal/ui` — passed.
- `LOOKBACK_SNAPSHOTS=/home/hamid/Desktop/AI/lookback/.review/screens/wp6 go test ./internal/ui -run '^TestSnapshots$' -count=1` — passed; wrote the 38 tester snapshots.
- `go vet ./...` — passed.
- `go test ./...` — passed.
- `GOOS=windows CGO_ENABLED=0 go build ./...` — passed.
- `scripts/check-colors.sh` — passed.
- `go test -race -short ./internal/ui` — passed.
- `git diff --check` — passed.

## Remaining acceptance limits

- This worktree supplies system light and dark theme sources to the UI tests. The Omarchy source and Catppuccin/One palettes are not present in the WP6 theme providers here, so only system dark/light were visually checked; no live Omarchy switch was possible.
- The 1,000-thumbnail spike decoded all items in 2.954 s. `MYGO_FRAME_STATS=8` logged frames over 8 ms during decoding, including one 27.0 ms frame. The session locked before an Inspector scroll run, so the 60 fps scrolling acceptance remains unverified. This status is also recorded in `docs/spikes.md`.
- The Settings hotkey control stores one of the available choices; actual global hotkey registration belongs to WP7. The UI guide's key-recorder interaction is not available through the WP6 contracts in this worktree.
- Sideshow MCP publishing was unavailable in this session. The report and screenshots are present at the local paths above.

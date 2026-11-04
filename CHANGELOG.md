# Changelog

All notable changes to Lookback are in this file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [0.2.0] - 2026-11-04

### Added

- Add the `--toggle` option. It shows or hides a compact search launcher in the running app. Esc,
  Enter on a result, or a click outside the launcher closes it.
- Add previews for text, code, and Office files (Ctrl+P in the launcher).
- Add the Lookback logo as the app icon and in the README.
- Add screenshots of setup, folders, models, and the launcher to the README.

### Changed

- Show download sizes in MiB and GiB, with one row for each download in setup.
- Show the Hyprland hotkey bind in Lua syntax.
- Install the icon to the `scalable` and `512x512` icon folders in `scripts/install.sh`. Launchers
  ignored the old `1024x1024` folder.

### Fixed

- Fix a crash when you leave the test lab with Back from Models.

## [0.1.0] - 2026-10-21

First release: local semantic search for images and documents on Linux x64.

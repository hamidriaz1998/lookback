# Changelog

All notable changes to Lookback are in this file. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project uses
[Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added

- Add Windows x64 support. Lookback detects the CPU, memory and GPUs through the Windows registry and
  APIs. It runs `llama-server.exe` with no console window, inside a job object, so the server stops with the
  app, even after a crash or `taskkill /f`. It watches folders with `ReadDirectoryChangesW`, keeps its data in
  `%LOCALAPPDATA%\Lookback`, and the **Start at login** switch writes the `HKCU` Run value.
- Skip hidden and system files and online-only (OneDrive) files on Windows.
- Show **Open** and **Show in folder** with correct `file://` URLs for drive letters and network paths.
- Show a note on a network folder row: "Network folder: changes appear after a restart."
- Add `resources/installer.nsh` for the Windows installer: it stops a stray `llama-server.exe`, removes the
  Start at login value, and offers to delete the index and models after the uninstall.

### Changed

- Compare folder paths without case on Windows when Lookback checks for nested or repeated folders.
- Hide the console window of every child process on Windows.
- The pre-push hook skips `-race` on Windows when `gcc` is missing.
- Tests: open the Turso library from a shared folder, so that temporary data folders can be deleted on
  Windows.

## [0.2.2] - 2026-11-08

### Fixed

- Fix the end of the first-run setup. **Start indexing** could fail with "a rebuild is already running".
- Keep the first-run setup closed after you finish it. On a slow disk, an older read of the settings could
  bring the setup back to step 1.
- Show an error and a **Retry** button in setup step 1 when Lookback cannot queue the runtime downloads.
  Before, step 1 waited for ever.
- Stop the hotkey recording when you leave Settings.
- Move the model server to the CPU only when its restart fails again. A server that stays up for 60 seconds
  starts with a clean count, so a crash much later no longer counts as a second failure.
- Index a folder that was missing when Lookback started, such as a drive that was not mounted yet. Lookback
  checks again every 30 seconds, and no longer needs a restart.

## [0.2.1] - 2026-11-08

### Added

- Add the **Start at login** switch in Settings. It is off by default. When it is on, Lookback writes
  `~/.config/autostart/lookback.desktop` (or `$XDG_CONFIG_HOME/autostart`), and your session starts
  `lookback --background` at login.
- Add the `--background` option. It starts Lookback in the tray with no window. If Lookback is already
  running, the option does nothing.

### Changed

- Show the Hyprland bind tip on Linux only.
- `scripts/install.sh --uninstall` also removes the Start at login entry.

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

<p align="center"><img src="resources/icon.svg" alt="Lookback logo" width="128"></p>

# Lookback

Lookback is a native desktop app that finds your images and documents by what they contain. Type
"the error dialog about the hotspot" or "invoice from the hosting company", and Lookback shows the
matching screenshots, PDFs, and notes.

Everything runs on your computer. Lookback downloads an embedding model that fits your hardware,
indexes the folders that you choose, and keeps the index current as files change. It has no account,
no cloud, and no telemetry.

![Lookback search results for "error dialog about the hotspot": the matching error screenshot is first, followed by a troubleshooting note](docs/images/search.png)

> **Status:** early release (0.2.2). Lookback runs on **Linux x64** only. Windows support comes next;
> macOS is planned later.

## Features

- **Search by meaning.** Search your files with a text query, or select a result and find similar
  files. Images and documents appear in one list. Filter the list with **All**, **Images**, or
  **Documents**.
- **Search while it indexes.** Search works on the files that are indexed so far. Indexing runs in
  the background and shows its speed and an estimated time to finish.
- **Index updates automatically.** Lookback watches your folders. When a file is added, changed, or
  deleted, the index updates in a few seconds. On startup, Lookback finds the files that changed
  while it was closed.
- **Fits your hardware.** On first launch, Lookback detects your CPU, RAM, and GPUs, downloads a
  matching runtime (CPU, Vulkan, or CUDA), and recommends a model precision. You can test each
  combination on your computer before you choose one.
- **Stays out of the way.** When you close the window, Lookback keeps running in the system tray.
  A global hotkey (Super+. by default) shows the window. Indexing pauses when the computer runs on
  battery.
- **Keyboard first.** You can do every main task with the keyboard. See
  [Keyboard shortcuts](#keyboard-shortcuts).
- **Themes.** Lookback has six built-in themes: System, Catppuccin Mocha, Catppuccin Latte,
  One Dark, One Light, and Omarchy. On [Omarchy](https://omarchy.org), Lookback follows your desktop
  theme and recolors immediately when the theme changes.

## Supported files

| Kind | Extensions | What Lookback indexes |
|---|---|---|
| Images | `.png` `.jpg` `.jpeg` `.webp` `.gif` `.bmp` | Each image (the first frame of a GIF) |
| Text | `.txt` `.md` `.rst` `.log` `.json` `.yaml` `.toml` `.csv` `.html` `.xml`, and most source code | The file text |
| PDF | `.pdf` | Each page. Pages with little text are indexed as images. |
| Office | `.docx` `.pptx` `.xlsx` `.odt` `.odp` `.ods` `.rtf` | Each document, slide, or sheet |

By default, Lookback skips hidden files and folders, folders such as `.git`, `node_modules`, and
`build`, files larger than 50 MB, and images smaller than 64×64 pixels. You can change these rules
for each folder in **Folders**. In this release, a rule change takes effect after you restart Lookback.

Lookback does not do OCR, and it does not index audio, video, HEIC images, or legacy Office formats
(`.doc`, `.xls`, `.ppt`).

## Requirements

- Linux x64 with a graphical desktop (X11 or Wayland)
- GTK 3 and `libayatana-appindicator3-1` (for the tray icon)
- About 1–2 GB of free disk space for the model, plus space for the runtime and the index
- An internet connection on first launch, to download the runtime and the model

A GPU is optional. Vulkan works with most AMD, Intel, and NVIDIA GPUs. CUDA is offered for NVIDIA
GPUs with a recent driver.

## Install

Each [release](https://github.com/hamidriaz1998/lookback/releases) has a tarball that works on any
Linux x64 distribution, an `install.sh` script, and a `.deb` package for Debian and Ubuntu.

### Arch Linux and other distributions

1. Install the libraries. On Arch Linux:

   ```sh
   sudo pacman -S --needed gtk3 libayatana-appindicator
   ```

2. Download the tarball and the install script from the latest release, then run the script. You do
   not need root access.

   ```sh
   gh release download --repo hamidriaz1998/lookback --pattern '*.tar.gz' --pattern install.sh
   sh install.sh lookback-*-linux-amd64.tar.gz
   ```

The script installs Lookback in `~/.local/lookback.app`, adds the `lookback` command to `~/.local/bin`,
and adds Lookback to the applications menu. To remove Lookback, run `sh install.sh --uninstall`.
Uninstalling keeps your settings and index. If you turned on **Start at login** in Settings, turn it off
before you uninstall. This script cannot remove the autostart entry in `~/.config/autostart`.

### Debian and Ubuntu

Download the `.deb` package from the latest release, then install it:

```sh
sudo apt install ./lookback_*_amd64.deb
```

A package cannot edit your home folder. If you turned on **Start at login** in Settings, turn it off
before you remove the package. Otherwise, delete `~/.config/autostart/lookback.desktop` by hand.

### Build from source

You need [Go](https://go.dev/dl/) 1.27.3 or later.

```sh
git clone https://github.com/hamidriaz1998/lookback.git
cd lookback
scripts/install.sh
```

The script builds Lookback and installs the binary, a desktop entry, and the icon under `~/.local`.
To install somewhere else, add `--prefix DIR`. To remove Lookback, run
`scripts/install.sh --uninstall`. This also removes the Start at login entry.

To build the release packages, use the [MyGo](https://github.com/egoist/mygo) CLI at the version
that `go.mod` pins:

```sh
go build -o mygo github.com/egoist/mygo/cmd/mygo
./mygo build -platform linux/amd64
```

This command writes the tarball, `install.sh`, and the `.deb` package to `dist/linux-amd64/`.

## Get started

1. Start Lookback from your app launcher, or run `lookback`.
2. Follow the first-run setup:
   1. **Hardware.** Lookback shows the CPU, RAM, and GPUs that it found.

      ![The first-run setup shows the CPU, RAM, and GPU, and the recommended combination of backend and precision](docs/images/onboarding-hardware.png)

   2. **Downloads and test lab.** Lookback downloads the runtimes and recommends a combination of
      backend and precision. You can test other combinations before you continue.

      ![Progress bars for the CPU and Vulkan runtime downloads during setup](docs/images/onboarding-downloads.png)

   3. **Folders.** Choose the folders to index.

      ![The Folders page lists the indexed folder with its image and document counts](docs/images/folders.png)

3. Type a query in the search field. Results appear while indexing continues.

To open setup again, go to **Models & Hardware**.

![The Models & Hardware page shows the active combination and the installed combinations](docs/images/models.png)

### Use the global hotkey on Hyprland

On Hyprland, the global hotkey can fail to register. If it does, add this line to your Hyprland
Lua configuration (for example `~/.config/hypr/bindings.lua`):

```
hl.bind("SUPER + period", hl.dsp.exec_cmd("lookback --toggle"))
```

`lookback --toggle` shows or hides a compact search launcher (a small window; Esc, Enter on a result, or clicking away closes it).

![The compact launcher lists the results for "invoice from the hosting company"](docs/images/launcher.png)

Press Ctrl+P in the launcher to preview the selected result.

![The launcher shows a preview of the selected invoice image](docs/images/launcher-preview.png)

## Keyboard shortcuts

| Key | Action |
|---|---|
| `/` or Ctrl+K | Go to the search field |
| Arrow keys | Move the selection |
| Enter | Open the selected file |
| Ctrl+Enter | Show the file in its folder |
| Ctrl+Shift+F | Find similar files |
| Ctrl+C | Copy the file path |
| Space | Show or hide the preview |
| Z | Zoom the preview to 100% |
| 1, 2, 3 | Show all results, only images, or only documents |
| Ctrl+= and Ctrl+− | Make thumbnails larger or smaller |
| Ctrl+B | Collapse the sidebar |
| Ctrl+, | Open Settings |
| Ctrl+1 to Ctrl+5 | Go to a sidebar page |

## Command-line options

| Option | Description |
|---|---|
| `--toggle` | Show or hide the compact search launcher of the running app, then exit |
| `--data-dir DIR` | Use `DIR` as the data folder |

## Privacy

- Lookback does not collect telemetry or analytics.
- Lookback uses the network only to download the runtime and model files that are pinned in the app.
  It verifies each download with SHA-256.
- The model server listens only on `127.0.0.1`, with a new API key on each run.
- Logs contain file paths and errors, never file contents.

Lookback stores its data in `~/.local/share/lookback` (or `$XDG_DATA_HOME/lookback`). This folder
holds the index, thumbnails, downloaded runtimes and models, and logs. To remove all data, delete
this folder.

## How it works

Lookback is one Go process. It runs the interface and the indexer, and it starts one
[llama.cpp](https://github.com/ggml-org/llama.cpp) `llama-server` child process that creates the
embeddings.

- **Model:** [EmbeddingGemma 2](https://huggingface.co/ggml-org/embeddinggemma-2-GGUF), in BF16 or
  Q8_0 precision. It embeds text and images into one vector space.
- **Interface:** [MyGo](https://github.com/egoist/mygo), a native Go UI toolkit drawn on the GPU.
- **Index:** a local [Turso](https://turso.tech) database with vector search.
- **PDF:** [go-pdfium](https://github.com/klippa-app/go-pdfium), run in WebAssembly.

When the model server stops unexpectedly, Lookback restarts it. If it fails again, Lookback uses the
CPU backend and indexing continues.

## Develop

```sh
git config core.hooksPath .githooks   # run the checks on commit and push
CGO_ENABLED=0 go build ./cmd/lookback
go test ./...
```

The project does not use cgo. These documents describe the design:

- [SPEC.md](SPEC.md): the specification and design decisions
- [UI_GUIDE.md](UI_GUIDE.md): the interface guidelines
- [docs/acceptance.md](docs/acceptance.md): acceptance test results
- [docs/followups.md](docs/followups.md): known issues and planned work

## License

Lookback is released under the [MIT License](LICENSE). The bundled
[Lucide](https://lucide.dev) icons use the [ISC License](internal/ui/icons/LICENSE.lucide).

# Lookback: UI guide

This guide sets the bar for every screen. A screen is not done until it meets the
[definition of done](#11-definition-of-done). It complements [SPEC.md](SPEC.md), which says what the
app does; this guide says how it looks, moves and responds.

Read the MyGo native UI docs before you start: <https://mygo.egoist.dev/docs/ui> (in the repo:
`docs/ui/`). Run the MyGo gallery (`examples/gallery`) to see each component.

## 1. Principles

1. **Keyboard-first.** The search field has focus whenever the Search page shows. Every action has a
   key. A user can install, index and search without touching the mouse.
2. **Photo-library density.** Results are a tight thumbnail grid. Chrome is quiet. The content is the interface.
3. **Accent means focus and selection.** Use `Accent` for the focused or selected item and the one
   primary button per view, nothing else. No colored section headers, no accent borders on cards.
4. **Never block.** No disk, network or model call on the main thread. Long work shows progress; short
   work (< 150 ms) shows nothing.
5. **Every state is designed.** Each screen in [section 3](#3-screens) lists its states. Build all of
   them: first run, empty, loading, partial (indexing), no results, paused, offline, error, and
   sidecar restarting.
6. **Use MyGo's components.** Don't hand-roll a widget that MyGo has ([section 4](#4-component-map)).
7. **Honest numbers.** Speeds, sizes and ETAs come from real measurements. If something isn't known yet,
   say "estimating…", never a made-up number.

## 2. Layout

```
┌──────────────┬──────────────────────────────────────────────┬──────────────────────┐
│ ◉ Lookback   │ [🔍 Search your files…                  ⌘K]  │                      │
│              │ (All) (Images) (Documents)      1,284 results │   Preview panel      │
│ ▸ Search     │ ┌────┐ ┌────┐ ┌────┐ ┌────┐ ┌────┐ ┌────┐     │   (Split view,       │
│   Folders    │ │    │ │    │ │    │ │    │ │    │ │    │     │    resizable,        │
│   Models     │ └────┘ └────┘ └────┘ └────┘ └────┘ └────┘     │    Space toggles)    │
│   Activity ● │ ┌────┐ ┌────┐ ┌────┐ ┌────┐ ┌────┐ ┌────┐     │                      │
│   Settings   │ │    │ │    │ │    │ │    │ │    │ │    │     │                      │
│              │ └────┘ └────┘ └────┘ └────┘ └────┘ └────┘     │                      │
│ ───────────  │                                              │                      │
│ Indexing 42% │                                              │                      │
│ ETA ~12 min  │                                              │                      │
└──────────────┴──────────────────────────────────────────────┴──────────────────────┘
```

- **Sidebar** (MyGo Sidebar): Search, Folders, Models & Hardware, Activity, Settings. Collapsible
  (Ctrl+B). Its footer shows a compact indexing status (percent + ETA, or "Up to date", or "Paused").
  Activity shows a dot while indexing runs or errors exist.
- **Search page**: the query bar is sticky at the top, then the filter chips and result count, then
  the virtualized grid. The preview panel sits on the right in a MyGo Split view (default 360 px,
  240–50% of the width, remembered).
- **Minimum window**: 720 × 480. Below 960 px of width, the preview panel opens as an overlay instead of a split.

## 3. Screens

Each screen lists its wireframe, then its states. Build every state. Numbers in wireframes are
illustrative; the real screens show measured values.

### 3.1 Onboarding, step 1: Hardware

```
          Welcome to Lookback                                       Step 1 of 3
          Lookback finds your images and documents by what's in them.
          Everything runs on this computer.

          ┌─ This computer ──────────────────────────────────────────────┐
          │ CPU   AMD Ryzen 7 5700U · 16 threads                         │
          │ RAM   21 GB                                                  │
          │ GPU   AMD Radeon Graphics (Vulkan) · 6.8 GB free             │
          └──────────────────────────────────────────────────────────────┘
          ┌─ Recommended ────────────────────────────────────────────────┐
          │ Vulkan · BF16 text · BF16 encoders            Download 1.6 GB│
          │ Fastest on this computer in a 10-second test.                │
          └──────────────────────────────────────────────────────────────┘
                              [Compare combinations]  [Continue with recommended]
```

States: detecting (Spinner per row, "Checking your GPU…"); testing backends (Progress bar,
"Testing Vulkan… 4 s"); no GPU (the GPU row says "No supported GPU. Lookback will use the CPU." in
TextMuted, which is not an error); a probe failed (Warning badge on that backend, with the reason in a
Tooltip); offline (an inline message with Retry; the recommendation is still shown).

### 3.2 Onboarding, step 2: Downloads and test lab

```
  Choose combinations to try                                               Step 2 of 3
  ┌──────────┬─────────────────┬──────────────────┬──────────┐
  │ Backend  │ Text model      │ Encoders         │ Download │
  ├──────────┼─────────────────┼──────────────────┼──────────┤
  │ ☑ Vulkan │ ☑ BF16  ☐ Q8_0  │ ☑ BF16  ☑ Q8_0   │ 1.6 GB   │
  │ ☑ CPU    │ ☑ BF16  ☐ Q8_0  │ ☑ BF16  ☐ Q8_0   │ shared   │
  │ ☐ CUDA   │ not available: no NVIDIA GPU                   │
  └──────────┴─────────────────┴──────────────────┴──────────┘
  Downloads                                                      [Pause all]
   mmproj-embeddinggemma-2-BF16.gguf   ███████████░░░░  71%  612/982 MB  4.1 MB/s  [⏸]
   embeddinggemma-2-BF16.gguf          ✓ verified
  Test results
  ┌───────────────────────────┬──────────┬──────────┬────────┬────────┬─────────┐
  │ Combination               │ Image ms │ Tokens/s │ RAM    │ VRAM   │ Quality │
  ├───────────────────────────┼──────────┼──────────┼────────┼────────┼─────────┤
  │ ★ Vulkan · BF16 · Q8_0    │ 2,410    │ 312      │ 0.9 GB │ 1.2 GB │ 0.996   │
  │   Vulkan · BF16 · BF16    │ 2,683    │ 298      │ 1.1 GB │ 1.6 GB │ 1.000   │
  │   CPU · BF16 · BF16       │ 4,759    │ 160      │ 2.0 GB │ —      │ 1.000   │
  └───────────────────────────┴──────────┴──────────┴────────┴────────┴─────────┘
  [+ Test with my own files]                     [Back]  [Use ★ Vulkan · BF16 · Q8_0]
```

- The matrix uses Checkboxes in a Table. Unavailable backends show a one-line reason instead of controls.
- The Download column shows only the new bytes this row adds; shared files say "shared".
- File rows use a Progress bar with the numbers in tabular figures. Each row has its own Pause/Resume.
- The ★ marks the recommended row (fastest with quality ≥ 0.99). The primary button names the
  selected combination. Clicking a row selects it.

States: nothing selected (the primary button is disabled, with a hint); downloading; paused; resumed
after restart (the row says "Resumed at 612 MB" for 5 s); verifying ("Verifying…" with a Spinner);
checksum failed (a Danger row with Retry); offline (all rows paused, an "Offline. Downloads resume
when you're back online." banner); testing (that table row shows a Spinner and the live step, for
example "Embedding image 3 of 8"); test failed (a Danger badge, reason in a Tooltip, and the row can't
be chosen); quality n/a (TextMuted "n/a", and a Tooltip explains that BF16 is needed for your own files).

### 3.3 Onboarding, step 3: Folders

```
  What should Lookback index?                                              Step 3 of 3
  ┌──────────────────────────────────────────────────────────────────────────────┐
  │ 📁 ~/Pictures            2,104 images · 12 documents       [Edit rules] [✕] │
  │ 📁 ~/Documents           38 images · 1,207 documents        [Edit rules] [✕] │
  └──────────────────────────────────────────────────────────────────────────────┘
  [+ Add folder]
  Skipped by rules: 318 files (hidden, node_modules, over 50 MB…)  [Show]
                                                         [Back]  [Start indexing]
```

States: no folders yet (an empty state with one large **Add folder** button and a sentence on what
gets indexed); counting (counts show a Spinner and fill in live); a folder can't be read (Danger text
with the reason); a folder nested in another (merged, with a note).

### 3.4 Search

```
  [🔍 hotspot error dialog                                    ✕]
  (All) (Images) (Documents)                         24 results · 180 ms
  ┌────────┐ ┌────────┐ ┌────────┐ ┌──────────────────────────────┐
  │ thumb  │ │ thumb  │ │ thumb  │ │ 📄 network-notes.md          │
  │        │ │        │ │        │ │ "…the hotspot fails when the │
  └────────┘ └────────┘ └────────┘ │  device is not available…"   │
  screenshot… screenshot… scan.pdf  └──────────────────────────────┘
                                    p. 3
```

- Grid tiles: images and rendered PDF pages as thumbnails (`ui.Image`, `object-fit: contain` on a
  Surface background), file name below in one line with an ellipsis. Document results are tiles twice
  as wide: icon, name, page, and two lines of snippet.
- Selection: the arrow keys move a selection ring (Focus color, 2 px, Radius 8). The preview panel
  follows the selection.
- The result count and the search time are TextMuted and use tabular figures.
- Right-click opens a Context menu with the same actions as the keyboard (Open, Show in folder, Find
  similar, Copy path).
- **Find similar** replaces the query with a chip "Similar to: screenshot-…png ✕". Esc or ✕ returns to
  the previous query.

States:
- **No folders yet**: an empty state that points to Folders.
- **Index empty, indexing started**: "Indexing your files… results appear as they're ready", with the progress.
- **Idle with an index**: before the first query, show "Recent files" (the 24 most recently modified indexed files).
- **Searching**: keep the old results visible; show a Spinner in the query field only after 150 ms.
- **Partial**: while indexing runs, a one-line notice above the grid: "Still indexing: 1,240 of 3,100
  files. Results may change." in TextMuted.
- **No results**: "Nothing matches “…”." plus suggestions: try fewer words, describe what's in the
  picture, check the type filter.
- **Model not running** (sidecar starting or restarting): the query field stays usable; show "Starting
  the model…" and run the query when it's ready. Find similar still works (it uses stored vectors).
- **Error**: an inline Danger message with Retry. Never a modal for search errors.

### 3.5 Preview panel

- **Image**: the image fitted to the panel. Click or Z toggles 100% zoom with panning. Below it: name,
  folder (a link that shows it in the file manager), dimensions, size, modified date.
- **Document**: the matched page (PDF: rendered page; other types: the chunk text) with the query's
  literal words highlighted (Selection color). Below it: name, page or slide, size, modified date.
- Action row: Open, Show in folder, Find similar, Copy path (icon buttons with Tooltips and accessible labels).
- States: loading (a skeleton the size of the content, after 150 ms); file missing since indexing
  ("This file was moved or deleted." with **Remove from index**); unsupported preview (the file icon
  and metadata only).

### 3.6 Folders

A List of folders. Each row: path, counts by type, last change, status (Up to date, Indexing 42%,
Paused, Errors 3). Row actions: Edit rules (a Dialog with the ignore list, one pattern per line, and a
live "matches N files" count), Rescan, Remove (an Alert dialog: "Remove ~/Pictures from Lookback? Its
files stay on disk; only the index entries are deleted."). Toolbar: **Add folder**.

States: none (empty state with Add folder); folder missing (unmounted drive: Warning, "Not found. It
will be indexed again when it's back."); permission denied (Danger with the reason).

### 3.7 Models & Hardware

- The **active combination** card: backend, precisions, device, and the vector space it serves.
- **Installed combinations**: a Table with the test results, plus **Make active** and **Delete**.
  Deleting the active one is disabled.
- **Switch precision** shows an Alert dialog: "This rebuilds the index in the background (about 2 h
  for 3,100 files). Search keeps working with the current index until then." The estimate comes from
  the ETA model.
- **Compare combinations** reopens the test lab (3.2).
- States: rebuilding (a progress card for the `building` generation); CPU fallback active (a Warning
  card: "The GPU backend failed twice. Lookback is using the CPU." with **Try GPU again** and **Show log**).

### 3.8 Activity

```
  Indexing                                                   [Pause]
  ████████████████████░░░░░░░░░░░░░░░  1,240 / 3,100 files   40%
  ETA about 37 min · 0.8 files/s · 96 tokens/s
  Now: ~/Pictures/screenshot-2026-08-21_17-00-59.png  (image, 1896×1030)

  ~/Pictures     ████████████████████████░░░░  1,180 / 2,116
  ~/Documents    ███░░░░░░░░░░░░░░░░░░░░░░░░░     60 /   984

  Errors (3)                                                [Retry all]
  ⚠ report-old.pdf     Encrypted PDF: no text and the pages can't be rendered     [Retry]
```

- The numbers update at most 4 times per second and use tabular figures, so they don't jitter.
- ETA formatting: "estimating…" for the first 30 s or 10 items; then "less than a minute", "about
  N min" (under 90 min), "about N h M min". Never show seconds for ETAs over a minute.
- States: up to date ("Everything is indexed. Watching 2 folders for changes."); paused by the user
  (Resume button); paused on battery ("Paused on battery power." with **Index anyway**); paused for
  suspend; rebuilding a new generation (a separate progress card labeled with the precision); errors.

### 3.9 Settings

Sections, as a single scrolling page with headings: Appearance (theme), Indexing (battery pause, max
image edge), Shortcuts (global hotkey with a key recorder, and the `lookback --toggle` tip with the
Hyprland bind line and a copy button; the tip is shown on Linux only), Startup (the Start at login
switch, off by default, with a muted line that says the app starts in the tray), Storage (data folder, disk usage per part, Open logs), About
(version, pinned llama.cpp tag and model revision).

**Theme picker**: a row of tiles, one per theme. Each tile is a live mini-preview drawn with that
theme's colors (background, a surface card, a text line, a muted line, an accent dot). Selecting one
applies it instantly to the whole app. The Omarchy tile shows the current Omarchy theme name (from
`theme.name`) and "Follows your Omarchy theme". It is absent when `colors.toml` doesn't exist.

## 4. Component map

| Element | MyGo component |
|---|---|
| App navigation | Sidebar |
| Results + preview | Split view |
| Query field | Search field |
| Type filter | Segmented control |
| Results | Grid view (virtualized) |
| Folder and error lists | List (virtualized) |
| Combination matrix, test results | Table |
| Thumbnails, previews | Image |
| Icons | Icon with embedded Lucide SVGs |
| Download and indexing progress | Progress bar |
| Theme swatches, disk usage | Meter (usage) and custom tiles from Box/Grid layout |
| Busy indicators | Spinner |
| Status labels | Badge |
| Confirmations | Alert dialog |
| Edit rules | Dialog + Text input (multiline) |
| Notices | Toast |
| Hints on icon buttons | Tooltip |
| Right-click on results | Context menu |
| Toggles | Switch (settings), Checkbox (selection in lists and tables) |
| Choices | Select |

Write a custom widget only when nothing above fits. Note the reason in a comment at the top of the file.

### Icons

Embed these Lucide SVGs in `internal/ui/icons/` with the Lucide license file: `search`, `folder`,
`folder-plus`, `cpu`, `activity`, `settings`, `image`, `file-text`, `file-spreadsheet`, `presentation`,
`external-link`, `copy`, `sparkles` (find similar), `pause`, `play`, `refresh-cw`, `trash-2`,
`triangle-alert`, `check`, `x`, `download`. Draw them in `Text` or `TextMuted`, never in a hard-coded color.

## 5. Tokens

- **Colors**: only from `c.Theme()`. No hex literals outside `internal/theme/`. `scripts/check-colors.sh`,
  run by the pre-commit hook, rejects `ui.Hex(` or `ui.RGB(` outside that package.
- **Spacing**: `Theme.Spacing = 3` (compact). Use `t.Space(n)` for every gap and padding:
  `t.Space(1)` inside controls, `t.Space(2)` between related items and grid gaps, `t.Space(4)` between
  sections, `t.Space(6)` page padding.
- **Radius**: 8 for cards, tiles and dialogs; controls use the theme's radius.
- **Type**: `t.Rem(1)` body; `t.Rem(0.85)` metadata; `t.Rem(1.15)` section headings (semibold);
  `t.Rem(1.6)` onboarding titles (bold). Tabular figures for every number that updates.
- **Thumbnails**: 160 px tiles by default; Ctrl+= and Ctrl+− step through 120, 160, 200 and 240 px.
- **Motion**: 120 ms ease-out for hover and selection, 200 ms for panels. Respect the desktop's
  reduce-motion preference (no animation).

## 6. Keyboard map

| Key | Action |
|---|---|
| `/` or Ctrl+K | Focus the search field |
| Arrows, Home, End, PageUp, PageDown | Move the selection in the grid or a list |
| Enter | Open the selected file |
| Ctrl+Enter | Show in folder |
| Ctrl+Shift+F | Find similar |
| Ctrl+C (with a result selected and the field empty or unfocused) | Copy the path |
| Space (when the grid has focus) | Toggle the preview panel |
| Z | Zoom the preview to 100% |
| 1, 2, 3 (grid focused) | All, Images, Documents |
| Ctrl+= / Ctrl+− | Thumbnail size |
| Ctrl+B | Collapse the sidebar |
| Ctrl+, | Settings |
| Ctrl+1 … Ctrl+5 | Sidebar pages |
| Esc | Clear the query → close the preview → hide the window to the tray, in that order |
| Super+. (global, configurable) | Show or hide Lookback |

The focus order follows the visual order. The grid is one tab stop; arrows move within it.

## 7. Feedback and timing

- The query runs on Enter, or after 250 ms with no typing. Cancel the in-flight query when a new one starts.
- Show a Spinner only after 150 ms; keep it at least 300 ms to avoid flicker.
- Progress reaches the UI through `Window.Update` at most 4 times per second. Batch updates.
- Toasts: 4 s, at most one at a time; errors that need action are inline, not toasts.
- Units: MB and GB with one decimal place under 10 (4.1 MB/s, 1.6 GB); whole numbers above; thousands
  separators for counts. Downloads use MiB and GiB (412 MiB / 1.6 GiB).
- Tone: short, plain sentences. Say what happened and what the user can do. No exclamation marks.
  No "Oops". Use "Lookback", never "the app", in onboarding.

## 8. Theming rules

- Check every screen in all six themes (Omarchy, System light and dark, Catppuccin Mocha and Latte,
  One Dark, One Light) before calling it done.
- Switch the Omarchy theme live with `omarchy-theme-set` while the screen is open. Nothing may keep an
  old color (cached textures, thumbnails' background, icons).
- Contrast: text ≥ 4.5:1 on its background; the focus ring ≥ 3:1. The theme package's contrast test
  enforces the palette; you enforce how colors are combined (no TextMuted on Accent, no Accent text on Surface).
- Thumbnails sit on `Surface`, so transparent PNGs show cleanly in light and dark themes.

## 9. Accessibility

- Every icon button has an accessible label and a Tooltip with the same text plus the shortcut.
- Thumbnails have a label: file name, type and, for PDF pages, the page number.
- The focus ring is always visible on keyboard focus.
- Follow the desktop's text scale and high-contrast preference (MyGo's theme does; keep it working
  when you set custom colors).
- Progress values are exposed to screen readers (Progress bar does this; don't draw your own bars).

## 10. Performance

- Decode thumbnails off the main thread, downscaled to 2× the tile size, and cache them as files in
  `thumbs/<sha1 of path+mtime>.jpg`. Never decode a full-size image for a tile.
- Use virtualized Grid view and List for anything that can exceed 50 rows.
- Keep 60 fps while indexing at full speed. Check with MyGo's Inspector and performance docs.
- The first window paints in < 1 s. The sidecar and the index load after it.

## 11. Definition of done

A screen is done when all of these hold:

- [ ] Every state listed for it in [section 3](#3-screens) is built and reachable.
- [ ] The whole flow works with the keyboard only.
- [ ] It looks right in all six themes, and survives a live Omarchy switch.
- [ ] No hex colors outside `internal/theme/`; spacing only through `t.Space`.
- [ ] Accessible labels on icon buttons and thumbnails.
- [ ] A MyGo UI test (see `docs/ui/testing.md`) covers the main path and one error state.
- [ ] No placeholders: no "TODO", "Lorem", fake numbers or dead buttons.
- [ ] Screenshots of the screen in at least a dark and a light theme are saved to
      `/home/hamid/Desktop/AI/lookback/.review/screens/<branch>/`, as PLAN.md rule 6 says. They are never
      committed, and no pull request is opened for them.

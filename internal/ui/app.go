// Package ui holds every screen of the native MyGo interface (UI guide section 3). It reads state and
// sends commands, and does no I/O on the main thread (SPEC section 3).
//
// The App holds the state the screens show. Background goroutines load data and subscribe to the
// contract services; they hand each change to the main thread through dispatch (Window.Update).
package ui

import (
	"context"
	"log"
	"path/filepath"
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Deps are the services the screens use. Store, Indexer, Runtime, Lab, Downloader, Detector and Catalog are
// required. Themes lists the theme sources of the picker, in the order it shows them. Hide, when set, hides
// the window to the tray (UI guide section 6).
type Deps struct {
	Store      contract.Store
	Indexer    contract.Indexer
	Runtime    contract.Runtime
	Lab        contract.Lab
	Downloader contract.Downloader
	Detector   contract.Detector
	Catalog    contract.Catalog
	Themes     []contract.ThemeSource
	Media      Media
	OS         contract.OS
	DataDir    string
	Hide       func()
}

// page is one of the sidebar pages.
type page int

const (
	pageSearch page = iota
	pageFolders
	pageModels
	pageActivity
	pageSettings
)

// pages lists the sidebar pages in their order; Ctrl+1 to Ctrl+5 reach them in this order.
var pages = []struct {
	id, label, icon string
}{
	{"search", "Search", "search"},
	{"folders", "Folders", "folder"},
	{"models", "Models & Hardware", "cpu"},
	{"activity", "Activity", "activity"},
	{"settings", "Settings", "settings"},
}

// App is the state of the window. Only the main thread reads or writes it.
type App struct {
	d        Deps
	ctx      context.Context
	dispatch func(func())
	thumbs   *thumbs

	page        page
	sidebarOpen bool
	thumbSize   float32 // tile edge in DIPs; Ctrl+= and Ctrl+- step it

	// From the subscriptions.
	progress    contract.Progress // the active generation's latest snapshot
	hasProgress bool
	rebuild     *contract.Progress
	runtime     contract.RuntimeStatus
	downloads   map[string]contract.DownloadProgress
	labSteps    map[contract.RunID]contract.LabStep
	labOrder    []contract.RunID

	// From the data snapshot, reloaded in the background.
	data       snapshot
	reloading  bool
	reloadWant bool

	search   searchState
	folders  foldersState
	models   modelsState
	settings settingsState
	onb      *onboarding
	picked   *contract.Combo // the combination the user chose, waiting for its files (onboarding.go)
	theme    themeState
	notice   string // the last failed command, shown on the page it came from
}

// New returns the App for d. Call Start once the window exists, then use View as the window's content.
func New(d Deps) *App {
	// Grid tiles are cached under the data folder (UI guide section 10), unless the caller chose a folder.
	if fm, ok := d.Media.(FileMedia); ok && fm.CacheDir == "" && d.DataDir != "" {
		d.Media = FileMedia{CacheDir: filepath.Join(d.DataDir, "thumbs")}
	}
	return &App{
		d:           d,
		page:        pageSearch,
		sidebarOpen: true,
		thumbSize:   160,
		thumbs:      newThumbs(d.Media),
		downloads:   map[string]contract.DownloadProgress{},
		labSteps:    map[contract.RunID]contract.LabStep{},
		search:      newSearchState(),
		models:      modelsState{selected: -1},
	}
}

// ShowPage makes the page with sidebar ID id the one on screen. It is for the entry points that open a page.
func (a *App) ShowPage(id string) { a.page = pageFromID(id) }

// Start subscribes to the services and loads the data. ctx is the app's lifetime. dispatch runs a function
// on the main thread; in a window it is (*mygo.Window).Update.
func (a *App) Start(ctx context.Context, dispatch func(func())) {
	a.ctx = ctx
	a.dispatch = dispatch
	a.thumbs.start(ctx, dispatch)
	a.theme.start(ctx, dispatch, a.d.Themes)

	pump(ctx, dispatch, a.d.Indexer.Subscribe(ctx), func(p contract.Progress) {
		a.progress, a.hasProgress = p, true
		a.reload()
	})
	pump(ctx, dispatch, a.d.Indexer.SubscribeRebuild(ctx), func(p contract.Progress) {
		if p.FilesDone == p.FilesTotal && p.FilesTotal > 0 {
			a.rebuild = nil // terminal: the rebuild is over
			a.reload()
			return
		}
		a.rebuild = &p
	})
	pump(ctx, dispatch, a.d.Runtime.Subscribe(ctx), func(s contract.RuntimeStatus) { a.runtime = s })
	pump(ctx, dispatch, a.d.Downloader.Subscribe(ctx), func(p contract.DownloadProgress) {
		a.downloads[p.Key] = p
		a.probeWhenReady()
	})
	pump(ctx, dispatch, a.d.Lab.Subscribe(ctx), func(s contract.LabStep) {
		if _, seen := a.labSteps[s.RunID]; !seen {
			a.labOrder = append(a.labOrder, s.RunID)
		}
		a.labSteps[s.RunID] = s
	})
	a.reload()
}

// pump forwards every value of ch to apply, on the main thread, until ch closes.
func pump[T any](ctx context.Context, dispatch func(func()), ch <-chan T, apply func(T)) {
	go func() {
		for v := range ch {
			dispatch(func() { apply(v) })
		}
	}()
}

// reload loads the data snapshot in the background. Only one load runs at a time; a request that arrives
// while one runs starts another when it ends.
func (a *App) reload() {
	if a.reloading {
		a.reloadWant = true
		return
	}
	a.reloading = true
	ctx := a.ctx
	go func() {
		s := buildSnapshot(ctx, a.d)
		a.dispatch(func() {
			a.data = s
			a.reloading = false
			if a.reloadWant {
				a.reloadWant = false
				a.reload()
			}
		})
	}()
}

// setting saves a setting in the background and reloads the snapshot when the write ends.
func (a *App) setting(key, value string) {
	a.data.settings[key] = value
	ctx := a.ctx
	go func() {
		if err := a.d.Store.SetSetting(ctx, key, value); err != nil {
			log.Printf("save setting %s: %v", key, err)
		}
	}()
}

// View builds the window from the state, once per frame.
func (a *App) View(c *ui.Context) {
	a.theme.apply(c, a.data.setting("theme", ""))
	a.shortcuts(c)
	if !a.data.ready {
		a.loading(c)
		return
	}
	a.applyPick()
	if a.onb == nil && !a.data.onboarded() && a.page != pageSettings {
		a.startOnboarding()
	}
	if a.onb != nil {
		a.onboardingView(c)
		return
	}
	ui.Row(c).Fill().Children(func() {
		if a.sidebarOpen {
			a.sidebar(c)
		}
		ui.Column(c).Grow(1).Fill().Children(func() {
			switch a.page {
			case pageSearch:
				a.searchPage(c)
			case pageActivity:
				a.activityPage(c)
			case pageFolders:
				a.foldersPage(c)
			case pageModels:
				a.modelsPage(c)
			case pageSettings:
				a.settingsPage(c)
			}
		})
	})
}

// loading is the first frame, before the first snapshot. It shows nothing but the window, so the first
// paint is under a second (UI guide section 10).
func (a *App) loading(c *ui.Context) {
	ui.Column(c).Fill().Center().Children(func() {
		ui.Spinner(c)
	})
}

// onboarded reports whether the first-run flow is done.
func (s snapshot) onboarded() bool { return s.settings["onboarded"] == "1" }

// shortcuts handles the keys of UI guide section 6 that do not depend on a control.
func (a *App) shortcuts(c *ui.Context) {
	if a.page == pageSettings && a.settings.recording {
		return
	}
	if c.Shortcut(ui.Ctrl, ui.KeyB) {
		a.sidebarOpen = !a.sidebarOpen
	}
	if c.Shortcut(ui.Ctrl, ui.KeyComma) {
		a.page = pageSettings
	}
	for i := range pages {
		if c.Shortcut(ui.Ctrl, ui.Key1+ui.Key(i)) {
			a.page = page(i)
		}
	}
	if c.Shortcut(ui.Ctrl, ui.KeyEqual) {
		a.stepThumb(1)
	}
	if c.Shortcut(ui.Ctrl, ui.KeyMinus) {
		a.stepThumb(-1)
	}
}

// thumbSizes are the tile edges Ctrl+= and Ctrl+- step through, in DIPs.
var thumbSizes = []float32{120, 160, 200, 240}

// stepThumb moves the tile size one step up (dir 1) or down (dir -1).
func (a *App) stepThumb(dir int) {
	i := 0
	for j, s := range thumbSizes {
		if s == a.thumbSize {
			i = j
		}
	}
	i = min(max(i+dir, 0), len(thumbSizes)-1)
	a.thumbSize = thumbSizes[i]
	a.setting("thumb_size", strconv.Itoa(int(a.thumbSize)))
}

// sidebar draws the navigation and the indexing status at its foot (UI guide section 2).
func (a *App) sidebar(c *ui.Context) {
	t := c.Theme()
	sel := pages[a.page].id
	ui.Column(c).Width(sidebarWidth).FillHeight().Padding(t.Space(4), t.Space(2)).Gap(t.Space(1)).Children(func() {
		ui.Text(c, "Lookback").Bold().Padding(t.Space(1), t.Space(2))
		if ui.Sidebar(c, &sel, func() {
			for _, p := range pages {
				item := ui.SidebarItem(c, p.id, icon(p.icon), p.label)
				if p.id == "activity" && a.activityNeedsDot() {
					item.Children(func() {
						ui.Text(c, "●").TextColor(t.TextMuted)
					})
				}
			}
		}).Grow(1).Changed() {
			a.page = pageFromID(sel)
		}
		ui.Divider(c)
		a.sidebarStatus(c)
	})
}

// sidebarStatus shows the indexing percentage and ETA, "Up to date", or "Paused" (UI guide section 2).
func (a *App) sidebarStatus(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Padding(t.Space(2)).Gap(t.Space(1)).Children(func() {
		switch s := a.indexStatus(); {
		case s.paused:
			ui.Text(c, "Paused").SingleLine()
		case s.busy:
			ui.Text(c, "Indexing "+percent(s.fraction)).FontFeatures("tnum").SingleLine()
			ui.Text(c, "ETA "+eta(s.eta)).TextColor(t.TextMuted).FontSize(t.Rem(0.85)).FontFeatures("tnum").SingleLine()
		default:
			ui.Text(c, "Up to date").SingleLine()
		}
	})
}

// indexStatus is what the footer and the Activity dot show.
type indexState struct {
	busy, paused bool
	fraction     float64
	eta          time.Duration
}

func (a *App) indexStatus() indexState {
	p := a.progress
	if !a.hasProgress {
		return indexState{}
	}
	if p.Paused != contract.PauseNone {
		return indexState{paused: true}
	}
	if p.FilesTotal == 0 || p.FilesDone >= p.FilesTotal {
		return indexState{}
	}
	return indexState{busy: true, fraction: float64(p.FilesDone) / float64(p.FilesTotal), eta: p.ETA}
}

// sidebarWidth is the width of the sidebar in DIPs.
const sidebarWidth = 220

// pageFromID returns the page with the sidebar ID id.
func pageFromID(id string) page {
	for i, p := range pages {
		if p.id == id {
			return page(i)
		}
	}
	return pageSearch
}

// activityNeedsDot reports whether the Activity page has something to show: indexing or errors.
func (a *App) activityNeedsDot() bool {
	return a.indexStatus().busy || len(a.data.errors) > 0
}

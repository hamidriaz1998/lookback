package ui

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"
)

const launcherRows = 6 // results the launcher shows

// NewLauncher returns the App of the compact launcher window (lookback --toggle). It has its own state, and
// shows only the search field and a short result list.
func NewLauncher(d Deps) *App {
	a := New(d)
	a.launcher = true
	return a
}

// Reset clears the query and focuses the field. The window calls it each time the launcher shows.
func (a *App) Reset() {
	a.search = newSearchState()
	a.search.focus = true
	a.search.previewOn = false // Ctrl+P shows the preview of the highlighted result
}

// launcherView is the whole content of the launcher window.
func (a *App) launcherView(c *ui.Context) {
	t := c.Theme()
	s := &a.search
	a.maybeRunSearch(c)
	s.focus = true // the field keeps the keyboard, as in Raycast: arrows, Enter and Esc work from it
	items := a.items()
	items = items[:min(len(items), launcherRows)]

	if s.selected < 0 && len(items) > 0 {
		s.selected = 0 // the first result is the preview until the user moves
	}
	if c.Shortcut(ui.Modifiers(0), ui.KeyEscape) {
		if s.previewOn {
			s.previewOn = false
		} else if s.query != "" {
			s.query = ""
		} else if a.d.Hide != nil {
			a.d.Hide()
		}
	}
	if c.Shortcut(ui.Ctrl, ui.KeyP) {
		s.previewOn = !s.previewOn
	}
	if c.Shortcut(ui.Modifiers(0), ui.KeyDown) {
		s.selected = min(s.selected+1, len(items)-1)
	}
	if c.Shortcut(ui.Modifiers(0), ui.KeyUp) {
		s.selected = max(s.selected-1, 0)
	}
	open := func(i int) {
		openFile(c, items[i].Path)
		if a.d.Hide != nil {
			a.d.Hide()
		}
	}

	ui.Column(c).Fill().Padding(t.Space(3)).Gap(t.Space(2)).Children(func() {
		a.queryBar(c)
		ui.Segmented(c, &s.kind, "All", "Images", "Documents").Changed()
		if s.searching && len(items) == 0 {
			ui.Spinner(c)
		}
		if s.previewOn {
			ui.Column(c).Grow(1).Fill().Children(func() { a.previewPanel(c, items) })
		} else {
			for i, h := range items {
				row := ui.ButtonBase(c).Label(tileLabel(h)).Radius(8).Children(func() {
					ui.Row(c).Grow(1).Gap(t.Space(2)).AlignItems(ui.Center).Padding(t.Space(1)).Children(func() {
						a.thumbnail(c, h, 32, 64)
						ui.Column(c).Grow(1).Children(func() {
							ui.Text(c, filepath.Base(h.Path)).SingleLine()
							ui.Text(c, filepath.Dir(h.Path)).TextColor(t.TextMuted).FontSize(t.Rem(0.8)).SingleLine()
						})
					})
				})
				if i == s.selected {
					row.Background(t.Surface)
				}
				if row.Clicked() {
					open(i)
				}
			}
			if s.key() != "" && !s.searching && len(items) == 0 {
				ui.Text(c, "Nothing matches “"+s.query+"”.").TextColor(t.TextMuted)
			}
			ui.Spacer(c).Grow(1)
		}
		ui.Text(c, "↑↓ select · Enter open · Ctrl+P preview · Esc close").TextColor(t.TextMuted).FontSize(t.Rem(0.8)).SingleLine()
	})
	// Enter in the field opens the highlighted result once its query has run; maybeRunSearch consumed s.submit.
	if s.ranKey == s.key() && !s.searching && s.selected >= 0 && s.selected < len(items) &&
		c.Shortcut(ui.Modifiers(0), ui.KeyEnter) {
		open(s.selected)
	}
}

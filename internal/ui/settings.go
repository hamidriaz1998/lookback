package ui

import (
	"fmt"
	"path/filepath"
	"runtime/debug"
	"strconv"

	"github.com/egoist/mygo/ui"
)

// llamaTag is the pinned llama.cpp release (SPEC section 4), shown in About.
const llamaTag = "b11755"

// hotkeys are the global shortcuts the Settings page offers, as Hyprland names them.
var hotkeys = []string{"Super+Shift+Space", "Super+Space", "Ctrl+Alt+L"}

// edgeChoices are the largest picture edges the indexer keeps, in pixels.
var edgeChoices = []int{1024, 1600, 2400}

// settingsState is the Settings page's state.
type settingsState struct {
	copied bool
}

// settingsPage builds the Settings page (UI guide section 3.9): one scrolling page with headings.
func (a *App) settingsPage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Fill().Padding(t.Space(6)).Gap(t.Space(5)).Children(func() {
			a.appearanceSection(c)
			a.indexingSection(c)
			a.shortcutsSection(c)
			a.storageSection(c)
			a.aboutSection(c)
		})
	})
}

// section draws a heading and the rows of one settings section.
func section(c *ui.Context, title string, fn func()) {
	t := c.Theme()
	ui.Column(c).Fill().Gap(t.Space(3)).Children(func() {
		ui.Text(c, title).FontSize(t.Rem(1.15)).Bold()
		fn()
	})
}

// appearanceSection holds the theme picker.
func (a *App) appearanceSection(c *ui.Context) {
	section(c, "Appearance", func() {
		a.themePicker(c)
	})
}

// themePicker draws one tile per theme source. Each tile is a mini preview in that theme's colors. Choosing a
// tile applies the theme to the whole app at once.
func (a *App) themePicker(c *ui.Context) {
	t := c.Theme()
	current := a.data.setting("theme", "")
	ui.Row(c).Gap(t.Space(3)).Children(func() {
		for _, src := range a.d.Themes {
			th := src.Theme(a.theme.base)
			selected := src.ID() == current
			ui.Column(c).Gap(t.Space(1)).Children(func() {
				ui.Column(c).Size(180, 104).Radius(8).Background(th.Background).Padding(th.Space(2)).Gap(th.Space(1)).
					Border(map[bool]float32{true: 2, false: 1}[selected], map[bool]ui.Color{true: t.Focus, false: th.Border}[selected]).
					Label(src.Name()).Tooltip(src.Name()).Children(func() {
					ui.Row(c).Gap(th.Space(1)).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(10, 10).Radius(999).Background(th.Accent)
						ui.Text(c, src.Name()).TextColor(th.Text).SingleLine()
					})
					ui.Column(c).Grow(1).Padding(th.Space(1)).Radius(6).Background(th.Surface).Children(func() {
						ui.Text(c, "Sample text").TextColor(th.Text).SingleLine()
						ui.Text(c, "Muted line").TextColor(th.TextMuted).SingleLine()
					})
				})
				if ui.Button(c, "").Label("Use " + src.Name()).Children(func() {
					ui.Text(c, map[bool]string{true: "Selected", false: "Use"}[selected])
				}).Clicked() {
					a.setting("theme", src.ID())
				}
			})
		}
	})
}

// indexingSection holds the battery pause and the largest picture size.
func (a *App) indexingSection(c *ui.Context) {
	t := c.Theme()
	section(c, "Indexing", func() {
		battery := a.data.setting("battery_pause", "on") == "on"
		ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Pause indexing on battery power").Grow(1)
			if ui.Switch(c, &battery).Changed() {
				a.setting("battery_pause", map[bool]string{true: "on", false: "off"}[battery])
			}
		})
		edge := a.data.intSetting("max_image_edge", 1600)
		options := make([]string, len(edgeChoices))
		selected := 0
		for i, e := range edgeChoices {
			options[i] = fmt.Sprintf("%d px", e)
			if e == edge {
				selected = i
			}
		}
		choice := options[selected]
		ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Largest picture edge kept for the index").Grow(1)
			if ui.Select(c, &choice, options).Changed() {
				for i, o := range options {
					if o == choice {
						a.setting("max_image_edge", strconv.Itoa(edgeChoices[i]))
					}
				}
			}
		})
	})
}

// shortcutsSection holds the global hotkey and the tip for binding lookback --toggle.
func (a *App) shortcutsSection(c *ui.Context) {
	t := c.Theme()
	section(c, "Shortcuts", func() {
		hk := a.data.setting("hotkey", hotkeys[0])
		ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Show or hide Lookback").Grow(1)
			if ui.Select(c, &hk, hotkeys).Changed() {
				a.setting("hotkey", hk)
			}
		})
		bind := "bind = SUPER SHIFT, space, exec, lookback --toggle"
		ui.Column(c).Gap(t.Space(2)).Padding(t.Space(3)).Radius(8).Background(t.Surface).Children(func() {
			ui.Text(c, "Your desktop can bind the same key to Lookback. Add this line to your Hyprland config:").TextColor(t.TextMuted)
			ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, bind).FontFeatures("tnum").Grow(1).SingleLine()
				if ui.Button(c, "").Label("Copy bind line").Tooltip("Copy bind line").Children(func() {
					ui.Icon(c, icon("copy"))
				}).Clicked() {
					c.WriteClipboard(bind)
					a.settings.copied = true
				}
			})
			if a.settings.copied {
				ui.Text(c, "Copied.").TextColor(t.TextMuted).FontSize(t.Rem(0.85))
			}
		})
	})
}

// storageSection shows the data folder, what each part of it takes, and the logs.
func (a *App) storageSection(c *ui.Context) {
	t := c.Theme()
	section(c, "Storage", func() {
		ui.Text(c, a.d.DataDir).SingleLine().FontFeatures("tnum")
		var total int64
		for _, p := range a.data.usage {
			total += p.bytes
		}
		if len(a.data.usage) == 0 {
			ui.Text(c, "Nothing is stored yet.").TextColor(t.TextMuted)
		}
		for _, p := range a.data.usage {
			ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
				ui.Text(c, p.name).Basis(160)
				ui.Meter(c, float64(p.bytes), 0, float64(max(total, 1)), nil).Grow(1)
				ui.Text(c, size(p.bytes)).FontFeatures("tnum").MinWidth(90)
			})
		}
		ui.Row(c).Children(func() {
			if ui.Button(c, "Open logs").Clicked() {
				c.OpenURL(fileURL(filepath.Join(a.d.DataDir, "logs")))
			}
		})
	})
}

// aboutSection shows the version, the pinned llama.cpp tag and the model revision in use.
func (a *App) aboutSection(c *ui.Context) {
	t := c.Theme()
	section(c, "About", func() {
		version := "development build"
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		rev := "not indexed yet"
		if g := a.activeGen(); g != nil {
			rev = shortRev(g.Space.ModelRev)
		}
		ui.Text(c, "Lookback "+version).SingleLine()
		ui.Text(c, "llama.cpp "+llamaTag+" · model revision "+rev).TextColor(t.TextMuted).FontFeatures("tnum").SingleLine()
	})
}

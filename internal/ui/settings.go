package ui

import (
	"fmt"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// llamaTag is the pinned llama.cpp release (SPEC section 4), shown in About.
const llamaTag = "b11755"

// edgeChoices are the largest picture edges the indexer keeps, in pixels.
var edgeChoices = []int{1024, 1600, 2400}

// settingsState is the Settings page's state.
type settingsState struct {
	copied      bool
	recording   bool
	recordError string
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
		ui.Row(c).Gap(t.Space(3)).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Show or hide Lookback").Grow(1)
			label := a.data.setting("hotkey", "Super+Shift+Space")
			if a.settings.recording {
				label = "Press a shortcut…"
			}
			record := ui.Button(c, label).Label("Record global hotkey").Tooltip("Record global hotkey. Press Escape to cancel.")
			if a.settings.recording {
				record.AutoFocus().HandleInput(func(ev ui.InputEvent) bool {
					if ev.Kind != ui.InputKeyDown || ev.Repeat {
						return ev.Kind == ui.InputKeyUp
					}
					if ev.Key == ui.KeyEscape && ev.Mods == 0 {
						a.settings.recording, a.settings.recordError = false, ""
						return true
					}
					chord, ok := hotkeyFromEvent(ev.Mods, ev.Key)
					if !ok {
						a.settings.recordError = "Use a modifier with a supported key."
						return true
					}
					a.setting("hotkey", chord)
					a.settings.recording, a.settings.recordError = false, ""
					return true
				})
			}
			if record.Clicked() {
				a.settings.recording, a.settings.recordError = true, ""
			}
			if a.settings.recording && ui.Button(c, "Cancel recording").Clicked() {
				a.settings.recording = false
			}
		})
		if a.settings.recording {
			ui.Text(c, "Press a key combination. Escape cancels.").TextColor(t.TextMuted).FontSize(t.Rem(0.85))
		}
		if a.settings.recordError != "" {
			ui.Text(c, a.settings.recordError).TextColor(t.Warning).FontSize(t.Rem(0.85))
		}
		bind := hyprlandBind(a.data.setting("hotkey", "Super+Shift+Space"))
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

// hotkeyFromEvent formats one recorded chord in the stable setting syntax.
func hotkeyFromEvent(mods ui.Modifiers, key ui.Key) (string, bool) {
	if mods&(ui.Super|ui.Ctrl|ui.Alt) == 0 {
		return "", false
	}
	name := keyName(key)
	if name == "" {
		return "", false
	}
	var parts []string
	for _, mod := range []struct {
		flag ui.Modifiers
		name string
	}{{ui.Super, "Super"}, {ui.Ctrl, "Ctrl"}, {ui.Alt, "Alt"}, {ui.Shift, "Shift"}} {
		if mods&mod.flag != 0 {
			parts = append(parts, mod.name)
		}
	}
	return strings.Join(append(parts, name), "+"), true
}

// keyName returns the MyGo key's label in the stored shortcut syntax.
func keyName(key ui.Key) string {
	switch {
	case key >= ui.KeyA && key <= ui.KeyZ:
		return string(rune('A' + key - ui.KeyA))
	case key >= ui.Key0 && key <= ui.Key9:
		return string(rune('0' + key - ui.Key0))
	case key >= ui.KeyF1 && key <= ui.KeyF12:
		return "F" + strconv.Itoa(int(key-ui.KeyF1)+1)
	}
	switch key {
	case ui.KeySpace:
		return "Space"
	case ui.KeyEnter:
		return "Enter"
	case ui.KeyTab:
		return "Tab"
	case ui.KeyMinus:
		return "Minus"
	case ui.KeyEqual:
		return "Equal"
	case ui.KeyComma:
		return "Comma"
	case ui.KeyPeriod:
		return "Period"
	case ui.KeySlash:
		return "Slash"
	case ui.KeySemicolon:
		return "Semicolon"
	case ui.KeyQuote:
		return "Quote"
	case ui.KeyBracketLeft:
		return "BracketLeft"
	case ui.KeyBracketRight:
		return "BracketRight"
	case ui.KeyBackslash:
		return "Backslash"
	case ui.KeyBackquote:
		return "Backquote"
	}
	return ""
}

// hyprlandBind converts the persisted shortcut into the matching Hyprland bind line.
func hyprlandBind(hotkey string) string {
	parts := strings.Split(hotkey, "+")
	mods := make([]string, 0, len(parts)-1)
	for _, p := range parts[:max(0, len(parts)-1)] {
		mods = append(mods, strings.ToUpper(p))
	}
	key := strings.ToLower(parts[len(parts)-1])
	switch parts[len(parts)-1] {
	case "Enter":
		key = "Return"
	case "Quote":
		key = "apostrophe"
	case "Backquote":
		key = "grave"
	case "F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12":
		key = parts[len(parts)-1]
	}
	return "bind = " + strings.Join(mods, " ") + ", " + key + ", exec, lookback --toggle"
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

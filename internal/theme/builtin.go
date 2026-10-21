package theme

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// fixed is a built-in palette (SPEC section 15). Its theme is a copy of the base theme, which
// the caller passes as MyGo's default for the light or dark appearance, with the palette's colors
// set and the other fields derived as the SPEC says.
type fixed struct {
	id, name                                   string
	dark                                       bool
	bg, surface, text, muted, accent, accentTx string
	danger, warning, success                   string
}

func (f *fixed) ID() string   { return f.id }
func (f *fixed) Name() string { return f.name }

// Changed is nil: a fixed palette never changes.
func (f *fixed) Changed() <-chan struct{} { return nil }

func (f *fixed) Theme(base *ui.Theme) *ui.Theme {
	t := *base
	t.Dark = f.dark
	bg, sf, tx := mustHex(f.bg), mustHex(f.surface), mustHex(f.text)
	ac := mustHex(f.accent)
	t.Background = bg
	t.Surface = sf
	t.SurfaceHover = sf.Mix(tx, 0.08)
	t.SurfacePressed = sf.Mix(tx, 0.16)
	t.Border = bg.Mix(tx, 0.18)
	t.Text = tx
	t.TextMuted = mustHex(f.muted)
	t.Accent = ac
	t.AccentHover = ac.Mix(white, 0.12)
	t.AccentPressed = ac.Mix(black, 0.12)
	t.AccentText = mustHex(f.accentTx)
	t.Danger = mustHex(f.danger)
	t.Warning = mustHex(f.warning)
	t.Success = mustHex(f.success)
	t.Selection = ac.Alpha(0.24)
	t.Focus = ac.Alpha(0.8)
	t.Scrollbar = tx.Alpha(0.35)
	t.Radius = 8
	return &t
}

var (
	white = ui.RGB(255, 255, 255)
	black = ui.RGB(0, 0, 0)
)

// The four fixed palettes, values from SPEC section 15.
var (
	catppuccinMocha = &fixed{
		id: "catppuccin-mocha", name: "Catppuccin Mocha", dark: true,
		bg: "#1e1e2e", surface: "#313244", text: "#cdd6f4", muted: "#a6adc8",
		accent: "#cba6f7", accentTx: "#1e1e2e",
		danger: "#f38ba8", warning: "#f9e2af", success: "#a6e3a1",
	}
	catppuccinLatte = &fixed{
		id: "catppuccin-latte", name: "Catppuccin Latte",
		bg: "#eff1f5", surface: "#e6e9ef", text: "#4c4f69", muted: "#5c5f77",
		accent: "#8839ef", accentTx: "#ffffff",
		danger: "#d20f39", warning: "#df8e1d", success: "#40a02b",
	}
	oneDark = &fixed{
		id: "one-dark", name: "One Dark", dark: true,
		bg: "#282c34", surface: "#2c313a", text: "#abb2bf", muted: "#9da5b4",
		accent: "#61afef", accentTx: "#282c34",
		danger: "#e06c75", warning: "#e5c07b", success: "#98c379",
	}
	oneLight = &fixed{
		id: "one-light", name: "One Light",
		bg: "#fafafa", surface: "#f0f0f1", text: "#383a42", muted: "#696c77",
		accent: "#3468d6", accentTx: "#ffffff",
		danger: "#e45649", warning: "#c18401", success: "#50a14f",
	}
)

// system follows the OS: its theme is the base the caller passes, which is MyGo's default for the
// current appearance. Its Changed fires when MyGo reports a change of appearance.
type system struct {
	changed chan struct{}
}

// System returns the source that follows the OS appearance. Call it once per app: each call
// registers a listener with MyGo that lasts for the run.
func System() contract.ThemeSource {
	s := &system{changed: make(chan struct{}, 1)}
	mygo.Theme.OnUpdated(func() {
		select {
		case s.changed <- struct{}{}:
		default:
		}
	})
	return s
}

func (s *system) ID() string   { return "system" }
func (s *system) Name() string { return "System" }

func (s *system) Changed() <-chan struct{} { return s.changed }

func (s *system) Theme(base *ui.Theme) *ui.Theme {
	t := *base
	return &t
}

// Builtins returns the built-in sources in the order the picker lists them: the four fixed
// palettes and System. The Omarchy source is separate (see Omarchy).
func Builtins() []contract.ThemeSource {
	return []contract.ThemeSource{catppuccinMocha, catppuccinLatte, oneDark, oneLight, System()}
}

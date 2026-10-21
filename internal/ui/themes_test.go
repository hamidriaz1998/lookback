package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
	"github.com/hamidriaz1998/lookback/internal/theme"
)

// omarchyDark is a colors.toml for the Omarchy source in tests. Its state folder is a temporary one (see
// isolateOmarchy), so the user's theme is never read.
const omarchyDark = `mode = "dark"
background = "#1a1b26"
lighter_background = "#24283b"
foreground = "#c0caf5"
muted = "#565f89"
accent = "#7aa2f7"
red = "#f7768e"
yellow = "#e0af68"
bright_yellow = "#ff9e64"
green = "#9ece6a"
selection = "#33467c"
`

// omarchyLight is the same theme edited in place to a light one.
const omarchyLight = `mode = "light"
background = "#e1e2e7"
lighter_background = "#d0d5e3"
foreground = "#3760bf"
muted = "#6172b0"
accent = "#2e7de9"
red = "#f52a65"
yellow = "#8c6c3e"
bright_yellow = "#b15c00"
green = "#587539"
selection = "#b6bfe2"
`

// isolateOmarchy points the Omarchy source at a temporary state folder holding colors.toml with the given
// palette, and returns the theme folder. The user's ~/.config and ~/.local/state are never read.
func isolateOmarchy(t *testing.T, palette string) string {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	dir := filepath.Join(state, "omarchy", "current", "theme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeOmarchy(t, dir, palette)
	return dir
}

func writeOmarchy(t *testing.T, themeDir, palette string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(themeDir, "colors.toml"), []byte(palette), 0o644); err != nil {
		t.Fatal(err)
	}
}

// standInDesktop replaces the desktop's accent with none for the test. MyGo reads it only on the main
// thread of a running app, which tests do not have.
func standInDesktop(t *testing.T) {
	t.Helper()
	prev := theme.DesktopAccent
	theme.DesktopAccent = func() ui.Color { return ui.Color{} }
	t.Cleanup(func() { theme.DesktopAccent = prev })
}

// themeSources returns the sources the real app offers: the built-ins and, when colors.toml exists, Omarchy.
func themeSources(ctx context.Context) []contract.ThemeSource {
	sources := theme.Builtins()
	if om := theme.Omarchy(ctx); om != nil {
		sources = append(sources, om)
	}
	return sources
}

// newThemedHarness runs the App with the real theme sources, the setting themeID, and the desktop dark or
// light. setup, when set, changes the fixture as newHarness does.
func newThemedHarness(t *testing.T, themeID string, dark bool, setup func(*fake.Store) contract.Store) *harness {
	t.Helper()
	standInDesktop(t)
	return newHarnessWith(t, func(s *fake.Store) contract.Store {
		if err := s.SetSetting(context.Background(), "theme", themeID); err != nil {
			t.Fatal(err)
		}
		if setup != nil {
			return setup(s)
		}
		return s
	}, themeSources, dark)
}

// frameBackground returns the color of the frame's top-left pixel, which lies on the page background: the
// title and the sidebar start below it.
func frameBackground(h *harness) ui.Color {
	p := h.tt.Image().RGBAAt(2, 2)
	return ui.RGB(p.R, p.G, p.B)
}

// backgroundOf returns the background the theme source id draws for the appearance of base.
func backgroundOf(t *testing.T, id string, dark bool) ui.Color {
	t.Helper()
	base := ui.LightTheme()
	if dark {
		base = ui.DarkTheme()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	for _, s := range themeSources(ctx) {
		if s.ID() == id {
			return s.Theme(base).Background
		}
	}
	t.Fatalf("no theme source %q", id)
	return ui.Color{}
}

// waitBackground pumps until the frame shows the background want, or fails after a few seconds.
func waitBackground(t *testing.T, h *harness, what string, want ui.Color) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.pump(0)
		if got := frameBackground(h); got.R == want.R && got.G == want.G && got.B == want.B {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	got := frameBackground(h)
	t.Fatalf("%s: frame background = #%02x%02x%02x, want #%02x%02x%02x", what, got.R, got.G, got.B, want.R, want.G, want.B)
}

// TestThemeLiveRecolor proves that a theme change at runtime recolors the window without a restart: the
// setting, the desktop's appearance and an Omarchy theme edited in place.
func TestThemeLiveRecolor(t *testing.T) {
	themeDir := isolateOmarchy(t, omarchyDark)

	t.Run("setting", func(t *testing.T) {
		h := newThemedHarness(t, "catppuccin-mocha", true, nil)
		waitBackground(t, h, "catppuccin-mocha", backgroundOf(t, "catppuccin-mocha", true))
		h.app.setting("theme", "catppuccin-latte")
		waitBackground(t, h, "after switch to catppuccin-latte", backgroundOf(t, "catppuccin-latte", false))
		h.app.setting("theme", "omarchy")
		waitBackground(t, h, "after switch to omarchy", backgroundOf(t, "omarchy", true))
	})

	t.Run("desktop appearance", func(t *testing.T) {
		h := newThemedHarness(t, "system", false, nil)
		waitBackground(t, h, "system light", backgroundOf(t, "system", false))
		h.tt.SetDark(true)
		waitBackground(t, h, "system after desktop switched to dark", backgroundOf(t, "system", true))
	})

	t.Run("omarchy edited in place", func(t *testing.T) {
		h := newThemedHarness(t, "omarchy", true, nil)
		waitBackground(t, h, "omarchy dark", backgroundOf(t, "omarchy", true))
		writeOmarchy(t, themeDir, omarchyLight)
		waitBackground(t, h, "omarchy after edit to light", backgroundOf(t, "omarchy", false))
	})
}

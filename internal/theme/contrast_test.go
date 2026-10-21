package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// checkContrast applies the SPEC section 15 contrast rules to one theme.
func checkContrast(t *testing.T, name string, th *ui.Theme) {
	t.Helper()
	const text, graphic = 4.5, 3.0
	for field, pair := range map[string][2]ui.Color{
		"Text on Background":      {th.Text, th.Background},
		"Text on Surface":         {th.Text, th.Surface},
		"TextMuted on Background": {th.TextMuted, th.Background},
		"TextMuted on Surface":    {th.TextMuted, th.Surface},
		"AccentText on Accent":    {th.AccentText, th.Accent},
	} {
		if r := contrast(pair[0], pair[1]); r < text {
			t.Errorf("%s: %s is %.2f:1, want >= %.1f", name, field, r, text)
		}
	}
	focus := th.Focus.Over(th.Background)
	if r := contrast(focus, th.Background); r < graphic {
		t.Errorf("%s: Focus on Background is %.2f:1, want >= %.1f", name, r, graphic)
	}
	if r := contrast(th.Border, th.Background); r < 1.3 {
		t.Errorf("%s: Border on Background is %.2f:1, want >= 1.3", name, r)
	}
}

// System in light and dark, after the guard.
func TestSystemContrast(t *testing.T) {
	for _, base := range []*ui.Theme{ui.LightTheme(), ui.DarkTheme()} {
		checkContrast(t, "System", systemTheme(base, ui.Color{}))
	}
}

func TestSystemUsesDesktopAccent(t *testing.T) {
	for _, accent := range []ui.Color{mustHex("#b91c1c"), mustHex("#047857")} {
		got := systemTheme(ui.LightTheme(), accent)
		if got.Accent != accent {
			t.Errorf("System Accent = %v, want desktop accent %v", got.Accent, accent)
		}
	}
}

// The guard changes only what SPEC section 15 allows.
func TestGuardLeavesOtherFieldsAlone(t *testing.T) {
	for _, base := range []*ui.Theme{ui.LightTheme(), ui.DarkTheme()} {
		before := *base
		after := *systemTheme(base, ui.Color{})
		if after.Text != before.Text || after.Background != before.Background || after.Surface != before.Surface || after.Danger != before.Danger || after.Warning != before.Warning || after.Success != before.Success {
			t.Errorf("guard changed Text, Background, Surface or a semantic color")
		}
	}
}

func TestOmarchyFixtureContrast(t *testing.T) {
	files, err := filepath.Glob("testdata/omarchy/*.toml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		kv, err := parseColors(data)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		for _, base := range []*ui.Theme{ui.LightTheme(), ui.DarkTheme()} {
			checkContrast(t, filepath.Base(f), omarchyTheme(base, kv))
		}
	}
}

// The reference machine's muted color is 2.91:1 on its background, so the guard must move it.
func TestMutedGuardOnReferenceMachine(t *testing.T) {
	data, err := os.ReadFile("testdata/omarchy/reference-machine.toml")
	if err != nil {
		t.Fatal(err)
	}
	kv, err := parseColors(data)
	if err != nil {
		t.Fatal(err)
	}
	raw := mustHex(strings.ToLower(kv["muted"]))
	bg := mustHex(kv["background"])
	if r := contrast(raw, bg); r >= 4.5 {
		t.Fatalf("fixture muted is already %.2f:1; the guard is not exercised", r)
	}
	th := omarchyTheme(ui.DarkTheme(), kv)
	if th.TextMuted == raw {
		t.Fatal("guard did not move TextMuted")
	}
	if r := contrast(th.TextMuted, th.Surface); r < 4.5 {
		t.Errorf("guarded TextMuted on Surface is %.2f:1", r)
	}
}

// Accepted data must not panic the guard (review r1 #1): an explicit mode that conflicts with the
// background, and a sparse file that falls back to the light base background.
func TestGuardRegressions(t *testing.T) {
	cases := map[string]struct {
		base *ui.Theme
		kv   map[string]string
	}{
		"light mode on a dark background, accent equal to background": {
			base: ui.LightTheme(),
			kv: map[string]string{
				"mode": "light", "background": "#111c18", "lighter_background": "#23372b",
				"foreground": "#c1c497", "accent": "#111c18",
			},
		},
		"dark mode, accent white, fallback light background": {
			base: ui.LightTheme(),
			kv:   map[string]string{"mode": "dark", "accent": "#ffffff"},
		},
		"dark mode, accent white, dark base": {
			base: ui.DarkTheme(),
			kv:   map[string]string{"mode": "dark", "accent": "#ffffff"},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			checkContrast(t, name, omarchyTheme(c.base, c.kv))
		})
	}
}

// Focus is adjusted separately: it must not change an accent whose text already passes.
func TestFocusAdjustmentLeavesReadableAccentUnchanged(t *testing.T) {
	th := *ui.LightTheme()
	th.Background = mustHex("#fafafa")
	th.Surface = mustHex("#f4f4f5")
	th.Text = mustHex("#18181b")
	th.TextMuted = mustHex("#71717a")
	th.Accent = mustHex("#aaaaaa")
	guard(&th)
	if th.Accent != mustHex("#aaaaaa") {
		t.Errorf("Accent = %v, want unchanged #aaaaaa", th.Accent)
	}
	if r := contrast(th.AccentText, th.Accent); r < 4.5 {
		t.Errorf("AccentText contrast = %.2f:1, want >= 4.5:1", r)
	}
	if r := contrast(th.Focus, th.Background); r < 3 {
		t.Errorf("Focus contrast = %.2f:1, want >= 3:1", r)
	}
}

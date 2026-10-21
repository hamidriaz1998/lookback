package theme

import (
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

// The fixed palettes pass the contrast rules as they are (wp5-decisions: no guard runs on them).
func TestFixedPaletteContrast(t *testing.T) {
	for _, src := range []*fixed{catppuccinMocha, catppuccinLatte, oneDark, oneLight} {
		for _, base := range []*ui.Theme{ui.LightTheme(), ui.DarkTheme()} {
			checkContrast(t, src.Name(), src.Theme(base))
		}
	}
}

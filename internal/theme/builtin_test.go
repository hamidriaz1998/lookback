package theme

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The fixed palettes pass the contrast rules as they are (wp5-decisions: no guard runs on them).
func TestFixedPaletteContrast(t *testing.T) {
	for _, src := range []*fixed{catppuccinMocha, catppuccinLatte, oneDark, oneLight} {
		for _, base := range []*ui.Theme{ui.LightTheme(), ui.DarkTheme()} {
			checkContrast(t, src.Name(), src.Theme(base))
		}
	}
}

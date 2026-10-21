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
	src := System()
	for _, base := range []*ui.Theme{ui.LightTheme(), ui.DarkTheme()} {
		checkContrast(t, src.Name(), src.Theme(base))
	}
}

// The guard changes only what SPEC section 15 allows.
func TestGuardLeavesOtherFieldsAlone(t *testing.T) {
	for _, base := range []*ui.Theme{ui.LightTheme(), ui.DarkTheme()} {
		before := *base
		after := *System().Theme(base)
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

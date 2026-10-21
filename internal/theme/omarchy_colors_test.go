package theme

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestParseColorsRejectsBadInput(t *testing.T) {
	for name, src := range map[string]string{
		"bad hex":   `background = "#GGGGGG"`,
		"short hex": `background = "#fff"`,
		"unquoted":  `background = #111111`,
		"no equals": `background 111111`,
		"unclosed":  `background = "#111111`,
	} {
		if _, err := parseColors([]byte(src)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestParseColorsAcceptsCommentsAndTrailingText(t *testing.T) {
	kv, err := parseColors([]byte("# c\n\nmode = \"dark\" # note\nforeground = \"#AABBCC\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if kv["mode"] != "dark" || kv["foreground"] != "#AABBCC" {
		t.Fatalf("got %v", kv)
	}
}

func TestModeDecidesDark(t *testing.T) {
	light := map[string]string{"background": "#ffffff"}
	if omarchyTheme(ui.DarkTheme(), light).Dark {
		t.Error("no mode key on a light background: want light")
	}
	dark := map[string]string{"background": "#101010"}
	if !omarchyTheme(ui.LightTheme(), dark).Dark {
		t.Error("no mode key on a dark background: want dark")
	}
	forced := map[string]string{"mode": "light", "background": "#101010"}
	if omarchyTheme(ui.DarkTheme(), forced).Dark {
		t.Error("mode = light must win over the background")
	}
}

func TestWarningFollowsMode(t *testing.T) {
	kv := map[string]string{"mode": "dark", "yellow": "#459451", "bright_yellow": "#E5C736"}
	if got := omarchyTheme(ui.LightTheme(), kv).Warning; got != mustHex("#E5C736") {
		t.Errorf("dark: Warning = %v, want bright_yellow", got)
	}
	kv["mode"] = "light"
	if got := omarchyTheme(ui.LightTheme(), kv).Warning; got != mustHex("#459451") {
		t.Errorf("light: Warning = %v, want yellow", got)
	}
}

// The first step of the guard on the reference machine: #53685B mixed 35% toward #C1C497 is
// about #7a8870 (SPEC section 15).
func TestMutedGuardFirstStepExample(t *testing.T) {
	muted := mustHex("#53685B")
	fg := mustHex("#C1C497")
	bg := mustHex("#111c18")
	surface := bg.Mix(mustHex("#23372B"), 0.5)
	got := mutedGuard(muted, fg, bg, surface)
	if r := contrast(got, bg); r < 4.5 {
		t.Fatalf("guard result %v is %.2f:1 on Background", got, r)
	}
	if r := contrast(got, surface); r < 4.5 {
		t.Fatalf("guard result %v is %.2f:1 on Surface", got, r)
	}
	// The 35% step passes on Background; Surface needs a further step (SPEC section 15).
	first := muted.Mix(fg, 0.35)
	if contrast(first, bg) < 4.5 {
		t.Errorf("35%% step %v is %.2f:1 on Background, want >= 4.5", first, contrast(first, bg))
	}
	if contrast(first, surface) >= 4.5 {
		t.Errorf("35%% step already passes on Surface; the further step is not exercised")
	}
}

// The derived colors follow the SPEC formulas.
func TestDerivedFieldsFromSpec(t *testing.T) {
	th := (&fixed{
		id: "x", dark: true, bg: "#1e1e2e", surface: "#313244", text: "#cdd6f4",
		muted: "#a6adc8", accent: "#cba6f7", accentTx: "#1e1e2e",
		danger: "#f38ba8", warning: "#f9e2af", success: "#a6e3a1",
	}).Theme(ui.DarkTheme())
	if th.Radius != 8 {
		t.Errorf("Radius = %v, want 8", th.Radius)
	}
	if want := mustHex("#cba6f7").Alpha(0.24); th.Selection != want {
		t.Errorf("Selection = %v, want %v", th.Selection, want)
	}
	if want := mustHex("#cba6f7").Alpha(0.8); th.Focus != want {
		t.Errorf("Focus = %v, want %v", th.Focus, want)
	}
	if want := mustHex("#cdd6f4").Alpha(0.35); th.Scrollbar != want {
		t.Errorf("Scrollbar = %v, want %v", th.Scrollbar, want)
	}
}

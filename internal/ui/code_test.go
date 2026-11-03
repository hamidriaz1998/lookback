package ui

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestCodeSpansColorsTokens(t *testing.T) {
	th := ui.DarkTheme()
	spans := codeSpans("def f(): # note\n\treturn \"x\" + 42\n", "a.py", th)
	want := map[string]ui.Color{"def": th.Accent, "# note": th.TextMuted, "return": th.Accent, `"x"`: th.Success, "42": th.Warning}
	got := map[string]ui.Color{}
	joined := ""
	for _, s := range spans {
		got[s.Text] = s.Color
		joined += s.Text
	}
	for text, c := range want {
		if got[text] != c {
			t.Errorf("%q has color %v, want %v", text, got[text], c)
		}
	}
	if joined != "def f(): # note\n\treturn \"x\" + 42\n" {
		t.Errorf("spans lose text: %q", joined)
	}
	if plain := codeSpans("a # b", "notes.md", th); len(plain) != 1 {
		t.Errorf("markdown was colored: %d spans", len(plain))
	}
}

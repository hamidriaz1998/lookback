package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

// spanText joins the spans' text, which must equal the original snippet.
func spanText(spans []ui.Span) string {
	var b strings.Builder
	for _, s := range spans {
		b.WriteString(s.Text)
	}
	return b.String()
}

// TestHighlightUnicodeCaseChanges checks highlight on text whose lowercase form has a different byte length:
// U+023A (2 bytes) lowers to U+2C65 (3 bytes), and U+1E9E (3 bytes) lowers to U+00DF (2 bytes). The spans must
// cover the original text exactly, on valid UTF-8 boundaries, and the match must be highlighted.
func TestHighlightUnicodeCaseChanges(t *testing.T) {
	th := ui.LightTheme()
	cases := []struct {
		text, query string
		want        []string
	}{
		{"Ⱥ", "Ⱥ", []string{"Ⱥ"}},
		{"xⱥy Ⱥ", "ⱥ", []string{"ⱥ", "Ⱥ"}},
		{"Straẞe road", "straße", []string{"Straẞe"}},
		{"Ⱥ and ẞ", "ẞ", []string{"ẞ"}},
	}
	for _, c := range cases {
		spans := highlight(c.text, c.query, th)
		if got := spanText(spans); got != c.text {
			t.Fatalf("highlight(%q) spans cover %q, want the original text", c.text, got)
		}
		if !utf8.ValidString(spanText(spans)) {
			t.Fatalf("highlight(%q) produced invalid UTF-8", c.text)
		}
		var hit []string
		for _, s := range spans {
			if s.Background != (ui.Color{}) {
				hit = append(hit, s.Text)
			}
		}
		if strings.Join(hit, "|") != strings.Join(c.want, "|") {
			t.Errorf("highlight(%q, %q) highlighted %q, want %q", c.text, c.query, hit, c.want)
		}
	}
}

package theme

import "github.com/egoist/mygo/ui"

// guard makes a dynamic theme (System, Omarchy) meet the contrast rules of SPEC section 15. It
// changes only TextMuted, AccentText, Accent and its hover and pressed colors, and Focus. Text,
// Background, Surface and the semantic colors are left as they are.
func guard(t *ui.Theme) {
	bg, surface, fg := t.Background, t.Surface, t.Text

	// 1. TextMuted: toward Text in 5% steps until 4.5:1 on Background and Surface.
	t.TextMuted = mutedGuard(t.TextMuted, fg, bg, surface)

	// 2 and 3. Accent: move it in 5% steps toward black (light) or white (dark) until AccentText
	// reaches 4.5:1 and Focus reaches 3:1 on Background. Focus is the Accent at 80% alpha if that
	// passes, else the opaque Accent. The loop ends at the target itself, which contrasts with
	// both candidates by 21:1 or more.
	target := white
	if !t.Dark {
		target = black
	}
	orig := t.Accent
	for i := range 21 {
		ac := orig.Mix(target, float32(i)*0.05)
		at, ok := accentText(ac, bg, fg)
		if !ok {
			continue
		}
		focus, ok := focusFor(ac, bg)
		if !ok {
			continue
		}
		t.Accent = ac
		t.AccentHover = ac.Mix(white, 0.12)
		t.AccentPressed = ac.Mix(black, 0.12)
		t.AccentText = at
		t.Focus = focus
		return
	}
	panic("theme: guard found no accent") // unreachable: the target passes both rules
}

// accentText picks the candidate with the highest contrast on accent, and reports whether it
// reaches 4.5:1.
func accentText(accent, bg, fg ui.Color) (ui.Color, bool) {
	best, bestRatio := bg, 0.0
	for _, c := range []ui.Color{bg, fg, white, black} {
		if r := contrast(c, accent); r > bestRatio {
			best, bestRatio = c, r
		}
	}
	return best, bestRatio >= 4.5
}

// focusFor returns the Accent at 80% alpha over Background when that is 3:1, else the opaque
// Accent when that is 3:1; ok is false when neither is.
func focusFor(accent, bg ui.Color) (ui.Color, bool) {
	if f := accent.Alpha(0.8); contrast(f.Over(bg), bg) >= 3 {
		return f, true
	}
	if contrast(accent, bg) >= 3 {
		return accent, true
	}
	return ui.Color{}, false
}

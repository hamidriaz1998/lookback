package theme

import "github.com/egoist/mygo/ui"

// guard makes a dynamic theme (System, Omarchy) meet the contrast rules of SPEC section 15. It
// changes only TextMuted, AccentText, Accent and its hover and pressed colors, and Focus. Text,
// Background, Surface and the semantic colors are left as they are.
func guard(t *ui.Theme) {
	originalAccent := t.Accent
	bg, fg := t.Background, t.Text

	// 1. TextMuted: toward Text in 5% steps until 4.5:1 on Background and Surface.
	t.TextMuted = mutedGuard(t.TextMuted, fg, bg, t.Surface)

	// 2. Accent moves only as needed for AccentText. Choose a direction from Background so it works
	// even when an explicit mode conflicts with the actual color.
	target := black
	if contrast(white, bg) > contrast(black, bg) {
		target = white
	}
	for i := range 21 {
		ac := originalAccent.Mix(target, float32(i)*0.05)
		at, ok := accentText(ac, bg, fg)
		if ok {
			setAccent(t, ac, at)
			break
		}
	}

	// 3. Focus is separate: use translucent Accent, then opaque Accent, then move Focus alone.
	if focus, ok := focusFor(t.Accent, bg); ok {
		t.Focus = focus
		return
	}
	for i := 1; i <= 20; i++ {
		focus := t.Accent.Mix(target, float32(i)*0.05)
		if contrast(focus, bg) >= 3 {
			t.Focus = focus
			return
		}
	}
	t.Focus = target
}

// setAccent sets Accent with the hover and pressed colors derived from it, and the accent-text and
// focus colors.
func setAccent(t *ui.Theme, ac, at ui.Color) {
	t.Accent = ac
	t.AccentHover = ac.Mix(white, 0.12)
	t.AccentPressed = ac.Mix(black, 0.12)
	t.AccentText = at
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

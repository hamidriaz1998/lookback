package theme

import (
	"bufio"
	"bytes"
	"fmt"

	"github.com/egoist/mygo/ui"
)

// omarchyKeys are the colors.toml keys the mapping reads (SPEC section 15). Each one that is
// present must be a #rrggbb color, or the file is invalid.
var omarchyKeys = []string{
	"background", "lighter_background", "foreground", "muted", "accent", "red",
	"yellow", "bright_yellow", "green", "selection",
}

// parseColors reads the flat TOML of an Omarchy theme: blank lines, # comments and `key = "value"`
// lines. Anything else is an error, and so is a color value that is not #rrggbb.
func parseColors(data []byte) (map[string]string, error) {
	kv := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		eq := bytes.IndexByte(line, '=')
		if eq < 0 {
			return nil, fmt.Errorf("colors.toml line %d: no '='", n)
		}
		key := string(bytes.TrimSpace(line[:eq]))
		val := bytes.TrimSpace(line[eq+1:])
		if len(val) < 2 || val[0] != '"' {
			return nil, fmt.Errorf("colors.toml line %d: value of %s is not quoted", n, key)
		}
		end := bytes.IndexByte(val[1:], '"')
		if end < 0 {
			return nil, fmt.Errorf("colors.toml line %d: unclosed string", n)
		}
		kv[key] = string(val[1 : 1+end])
		// After the closing quote only whitespace and an optional # comment may follow.
		if rest := bytes.TrimSpace(val[1+end+1:]); len(rest) > 0 && rest[0] != '#' {
			return nil, fmt.Errorf("colors.toml line %d: text after the value of %s", n, key)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	for _, k := range omarchyKeys {
		if v, ok := kv[k]; ok {
			if _, err := parseHex(v); err != nil {
				return nil, fmt.Errorf("colors.toml key %s: %w", k, err)
			}
		}
	}
	return kv, nil
}

// omarchyTheme maps a parsed colors.toml onto a copy of base (SPEC section 15). A key that is
// missing takes the base's value.
func omarchyTheme(base *ui.Theme, kv map[string]string) *ui.Theme {
	if kv == nil {
		t := *base
		return &t
	}
	col := func(key string, def ui.Color) ui.Color {
		if v, ok := kv[key]; ok {
			if c, err := parseHex(v); err == nil {
				return c
			}
		}
		return def
	}
	bg := col("background", base.Background)
	dark := relativeLuminance(bg) < 0.5
	if m, ok := kv["mode"]; ok {
		dark = m == "dark"
	}
	fg := col("foreground", base.Text)
	lighter := col("lighter_background", base.SurfaceHover)
	ac := col("accent", base.Accent)
	surface := bg.Mix(lighter, 0.5)

	t := *base
	t.Dark = dark
	t.Background = bg
	t.Surface = surface
	t.SurfaceHover = lighter
	t.SurfacePressed = lighter.Mix(fg, 0.15)
	t.Border = bg.Mix(fg, 0.18)
	t.Text = fg
	t.TextMuted = col("muted", base.TextMuted)
	t.Accent = ac
	t.AccentHover = ac.Mix(white, 0.12)
	t.AccentPressed = ac.Mix(black, 0.12)
	t.Danger = col("red", base.Danger)
	yellow := "yellow"
	if dark {
		yellow = "bright_yellow"
	}
	t.Warning = col(yellow, base.Warning)
	t.Success = col("green", base.Success)
	t.Selection = col("selection", base.Selection)
	t.Focus = ac.Alpha(0.8)
	t.Scrollbar = fg.Alpha(0.35)
	guard(&t)
	return &t
}

// mutedGuard moves muted toward fg in 5% steps until it reaches 4.5:1 on both the background and
// the surface. At 100% it is fg itself.
func mutedGuard(muted, fg, bg, surface ui.Color) ui.Color {
	for i := range 20 {
		c := muted.Mix(fg, float32(i)*0.05)
		if contrast(c, bg) >= 4.5 && contrast(c, surface) >= 4.5 {
			return c
		}
	}
	return fg
}

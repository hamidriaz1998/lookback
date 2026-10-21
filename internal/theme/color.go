package theme

import (
	"fmt"
	"math"
	"strconv"

	"github.com/egoist/mygo/ui"
)

// parseHex reads "#rrggbb" in either case. Unlike ui.Hex it returns an error, because the
// values come from files that the user or a theme tool writes.
func parseHex(s string) (ui.Color, error) {
	if len(s) != 7 || s[0] != '#' {
		return ui.Color{}, fmt.Errorf("color %q is not #rrggbb", s)
	}
	var v [3]uint8
	for i := range v {
		n, err := strconv.ParseUint(s[1+2*i:3+2*i], 16, 8)
		if err != nil {
			return ui.Color{}, fmt.Errorf("color %q is not #rrggbb", s)
		}
		v[i] = uint8(n)
	}
	return ui.RGB(v[0], v[1], v[2]), nil
}

// mustHex is parseHex for the palettes compiled into the program, which are checked by tests.
func mustHex(s string) ui.Color {
	c, err := parseHex(s)
	if err != nil {
		panic(err)
	}
	return c
}

// relativeLuminance is the WCAG relative luminance of an opaque color.
func relativeLuminance(c ui.Color) float64 {
	lin := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

// contrast is the WCAG contrast ratio of two opaque colors, from 1 to 21.
func contrast(a, b ui.Color) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// brightness is the gray level of a color, from 0 (black) to 1 (white), as Rec. 709 luma of its
// sRGB values.
func brightness(c ui.Color) float64 {
	return (0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)) / 255
}

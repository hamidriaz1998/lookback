package main

import "testing"

// TestGridColumnsAtReferenceWidth checks the column count the autoscroll step uses. The window is 1896 DIPs
// wide when tiled (1920x1080 minus the bar), where MyGo's GridView fits 10 columns of 180 DIPs, not the 5 that
// the 1100 DIP window the spike was opened with would give.
func TestGridColumnsAtReferenceWidth(t *testing.T) {
	const gap = 8 // the theme's Space(2) at the default spacing
	cases := []struct {
		windowWidth float32
		want        int
	}{
		{1896, 10},
		{1100, 5},
		{100, 1},
	}
	for _, c := range cases {
		if got := gridColumns(c.windowWidth-2*pad, tileWidth, gap); got != c.want {
			t.Errorf("window %v DIPs: %d columns, want %d", c.windowWidth, got, c.want)
		}
	}
}

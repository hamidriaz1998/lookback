package ui

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestLauncherTypesWithoutClick checks that keys typed as the launcher opens reach the search field. With
// MyGo v0.6.0, SearchField.Focus focused the field's frame, not its text input, so typing did nothing.
func TestLauncherTypesWithoutClick(t *testing.T) {
	h := newHarness(t, nil)
	h.wait("snapshot", func() bool { return h.app.data.ready })
	h.app.launcher = true
	h.app.Reset()
	h.tt.Frame()
	h.tt.Type("cat")
	h.tt.Frame()
	if got := h.app.search.query; got != "cat" {
		t.Fatalf("query = %q, want %q", got, "cat")
	}
}

// TestLauncherShowsResultsWithoutChrome checks that the launcher lists a query's results and has no sidebar.
func TestLauncherShowsResultsWithoutChrome(t *testing.T) {
	h := newHarness(t, nil)
	h.wait("snapshot", func() bool { return h.app.data.ready })
	h.app.launcher = true
	h.app.Reset()
	h.app.search.query = "hotspot error dialog"
	h.app.search.submit = true
	h.wait("results", func() bool { return len(h.app.search.hits) > 0 && !h.app.search.searching })
	h.wantText(filepath.Base(h.app.search.hits[0].Path))
	for _, txt := range h.tt.Texts() {
		if strings.Contains(txt, "Models & Hardware") {
			t.Errorf("the launcher shows the sidebar: %q", txt)
		}
	}
}

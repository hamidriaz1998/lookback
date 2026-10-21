package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// TestSnapshots writes the screenshots of every screen and state in a dark and a light theme, offscreen from
// the tester. It runs only when LOOKBACK_SNAPSHOTS names the output folder.
func TestSnapshots(t *testing.T) {
	dir := os.Getenv("LOOKBACK_SNAPSHOTS")
	if dir == "" {
		t.Skip("set LOOKBACK_SNAPSHOTS=<dir> to write the screenshots")
	}
	for _, theme := range []string{"dark", "light"} {
		for _, sc := range snapshotCases() {
			h := newHarness(t, sc.setup)
			h.app.data.settings["theme"] = theme
			h.app.page = sc.page
			if sc.prepare != nil {
				sc.prepare(h)
			}
			h.wait(sc.name, func() bool { return h.app.data.ready && (sc.ready == nil || sc.ready(h)) })
			h.pump(0)
			writePNG(t, filepath.Join(dir, sc.name+"-"+theme+".png"), h)
		}
	}
}

// snapshotCase is one screen or state to capture.
type snapshotCase struct {
	name    string
	page    page
	setup   func(*fake.Store) contract.Store
	prepare func(h *harness)
	ready   func(h *harness) bool
}

func snapshotCases() []snapshotCase {
	return []snapshotCase{
		{name: "search-recent", page: pageSearch, ready: func(h *harness) bool { return len(h.app.data.recent) > 0 }},
		{name: "search-results", page: pageSearch, prepare: func(h *harness) {
			h.app.search.query, h.app.search.submit = "hotspot error dialog", true
		}, ready: func(h *harness) bool { return len(h.app.search.hits) > 0 && !h.app.search.searching }},
		{name: "activity", page: pageActivity, ready: func(h *harness) bool { return h.app.hasProgress }},
		{name: "folders", page: pageFolders, ready: func(h *harness) bool { return len(h.app.data.folders) == 2 }},
		{name: "folders-rules", page: pageFolders, prepare: func(h *harness) {
			h.app.folders.editOpen, h.app.folders.editID, h.app.folders.editText = true, 2, "*.md"
		}, ready: func(h *harness) bool { return len(h.app.data.folders) == 2 }},
	}
}

// writePNG saves the tester's current frame to path.
func writePNG(t *testing.T, path string, h *harness) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, h.tt.Image()); err != nil {
		t.Fatal(err)
	}
}

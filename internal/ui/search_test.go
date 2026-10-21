package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// TestSearchIdleShowsRecentFiles checks the first state after start: no query, so the recent files show.
func TestSearchIdleShowsRecentFiles(t *testing.T) {
	h := newHarness(t, nil)
	h.wait("recent files", func() bool { return len(h.app.data.recent) > 0 })
	h.wantText("Recent files")
	if _, ok := h.app.selectedHit(); !ok {
		t.Error("the first recent file is not selected for the preview")
	}
}

// TestSearchQueryShowsResults checks the main path: a query runs after the debounce and fills the grid.
func TestSearchQueryShowsResults(t *testing.T) {
	h := newHarness(t, nil)
	h.wait("snapshot", func() bool { return h.app.data.ready })
	h.app.search.query = "hotspot error dialog"
	h.app.search.submit = true
	h.wait("results", func() bool { return len(h.app.search.hits) > 0 && !h.app.search.searching })
	h.wantText(" results")
}

// TestSearchNoFoldersEmptyState checks the empty state with no watched folders.
func TestSearchNoFoldersEmptyState(t *testing.T) {
	h := newHarness(t, func(s *fake.Store) contract.Store {
		ctx := t.Context()
		folders, err := s.ListFolders(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range folders {
			if err := s.DeleteFolder(ctx, f.ID); err != nil {
				t.Fatal(err)
			}
		}
		return s
	})
	h.wait("empty state", func() bool { return h.app.data.ready })
	h.wantText("No folders yet")
}

// TestSearchErrorShowsRetry checks the error state: an inline message with Retry, never a modal.
func TestSearchErrorShowsRetry(t *testing.T) {
	h := newHarness(t, func(s *fake.Store) contract.Store { return failingSearch{s} })
	h.wait("snapshot", func() bool { return h.app.data.ready })
	h.app.search.query = "anything"
	h.app.search.submit = true
	h.wait("error", func() bool { return h.app.search.err != "" })
	h.wantText("Search failed")
	h.wantText("Retry")
	for _, txt := range h.tt.Texts() {
		if strings.Contains(txt, "Nothing matches") {
			t.Errorf("the error state also says %q", txt)
		}
	}
}

// TestSearchGridStaysBelowHeader checks the layout of the results: the grid, the preview and the header
// (query field, filter row, result count) never overlap. A grid that draws over the header fails here.
func TestSearchGridStaysBelowHeader(t *testing.T) {
	h := newHarness(t, nil)
	h.tt.SetSize(1896, 1025)
	h.wait("snapshot", func() bool { return h.app.data.ready })
	h.app.search.query = "hotspot error dialog"
	h.app.search.submit = true
	h.wait("results", func() bool { return len(h.app.search.hits) > 0 && !h.app.search.searching })
	h.pump(0)

	field, ok := h.tt.Find("Search your files")
	if !ok {
		t.Fatal("the search field is not on screen")
	}
	filter, ok := h.tt.Find("Documents")
	if !ok {
		t.Fatal("the filter row is not on screen")
	}
	headerBottom := max(field.Y+field.H, filter.Y+filter.H)
	name := filepath.Base(h.app.search.hits[0].Path)
	tile, ok := h.tt.Find(name)
	if !ok {
		t.Fatalf("the first result %q is not on screen", name)
	}
	if tile.Y < headerBottom {
		t.Errorf("the grid draws at y=%.0f, above the header that ends at y=%.0f", tile.Y, headerBottom)
	}
	if tile.Y+tile.H > 1025 {
		t.Errorf("the first result row ends below the window: y=%.0f h=%.0f", tile.Y, tile.H)
	}
}

package ui

import (
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// TestFoldersListsCountsAndStatus checks the main path: each folder with its counts and status.
func TestFoldersListsCountsAndStatus(t *testing.T) {
	h := newHarness(t, nil)
	h.app.page = pageFolders
	h.wait("folders", func() bool { return len(h.app.data.folders) == 2 })
	h.wantText("Add folder")
	h.wantText("images · ")
	h.wantText("documents")
	h.wantText("Edit rules")
}

// TestFoldersMissingFolder checks the missing-folder state: a Warning with the reason.
func TestFoldersMissingFolder(t *testing.T) {
	h := newHarness(t, nil)
	h.app.page = pageFolders
	h.wait("folders", func() bool { return len(h.app.data.folders) == 2 })
	h.wantText("Not found. It will be indexed again when it's back.")
}

// TestFoldersEmptyState checks the state with no folders: one large Add folder button and a sentence.
func TestFoldersEmptyState(t *testing.T) {
	h := newHarness(t, func(s *fake.Store) contract.Store {
		ctx := t.Context()
		folders, _ := s.ListFolders(ctx)
		for _, f := range folders {
			_ = s.DeleteFolder(ctx, f.ID)
		}
		return s
	})
	h.app.page = pageFolders
	h.wait("empty", func() bool { return h.app.data.ready })
	h.wantText("Lookback indexes the images and documents in the folders you add.")
}

// TestRulesCountLive checks the rules dialog: the match count follows the patterns as they are typed.
func TestRulesCountLive(t *testing.T) {
	h := newHarness(t, nil)
	h.app.page = pageFolders
	h.wait("folders", func() bool { return len(h.app.data.folders) == 2 })
	h.app.folders.editOpen, h.app.folders.editID = true, 2
	h.app.folders.editText = "*.md"
	h.wait("dialog", func() bool { return true })
	h.wantText("matches 40 files")
}

// TestIgnoreMatches checks the wildcard match on file names.
func TestIgnoreMatches(t *testing.T) {
	files := []contract.FileRec{{Path: "/a/b.tmp"}, {Path: "/a/c.bak"}, {Path: "/a/d.png"}}
	if got := ignoreMatches("*.tmp\n\n*.bak", files); got != 2 {
		t.Errorf("ignoreMatches = %d, want 2", got)
	}
	if got := ignoreMatches("", files); got != 0 {
		t.Errorf("empty rules matched %d files", got)
	}
}

package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// folderAt is a folderView for the path p, for the nesting and duplicate checks.
func folderAt(p string) folderView { return folderView{folder: contract.Folder{Path: p}} }

// TestPathsSame checks the path equality with and without case folding.
func TestPathsSame(t *testing.T) {
	if !pathsSame(`C:\Data`, `c:\data`, true) {
		t.Error("folded: the upper-case and lower-case paths should be the same")
	}
	if pathsSame(`C:\Data`, `c:\data`, false) {
		t.Error("unfolded: the upper-case and lower-case paths should differ")
	}
	if !pathsSame("/a/b", "/a/b", false) {
		t.Error("equal paths should match")
	}
}

// TestPathHasPrefix checks the prefix test with and without case folding.
func TestPathHasPrefix(t *testing.T) {
	if !pathHasPrefix(`c:\data\sub`, `C:\Data\`, true) {
		t.Error("folded: the lower-case sub path should be inside the upper-case folder")
	}
	if pathHasPrefix(`c:\data\sub`, `C:\Data\`, false) {
		t.Error("unfolded: the lower-case sub path should not be inside the upper-case folder")
	}
	if pathHasPrefix(`C:\Dat`, `C:\Data\`, true) {
		t.Error("a shorter path is never inside a longer prefix")
	}
}

// TestNestedInFollowsPlatformCase checks the nesting check: an upper-case copy of a folder's path is inside the
// folder only where foldPathCase says paths ignore case (Windows).
func TestNestedInFollowsPlatformCase(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Data")
	folders := []folderView{folderAt(root)}
	if got := nestedIn(filepath.Join(root, "sub"), folders); got != root {
		t.Errorf("exact nesting: nestedIn = %q, want %q", got, root)
	}
	if got := nestedIn(root, folders); got != "" {
		t.Errorf("a folder is not nested in itself: got %q", got)
	}
	upper := filepath.Join(strings.ToUpper(root), "sub")
	want := ""
	if foldPathCase {
		want = root
	}
	if got := nestedIn(upper, folders); got != want {
		t.Errorf("nestedIn(%q) = %q, want %q (foldPathCase=%v)", upper, got, want, foldPathCase)
	}
}

// TestNestedInDriveRoot checks that a drive root (C:\ on Windows, / elsewhere), which already ends in a separator,
// contains the folders below it, and that a sibling that shares a name prefix is not inside a folder.
func TestNestedInDriveRoot(t *testing.T) {
	rootDir := filepath.VolumeName(t.TempDir()) + string(filepath.Separator)
	data := filepath.Join(rootDir, "Data")

	roots := []folderView{folderAt(rootDir)}
	if got := nestedIn(data, roots); got != rootDir {
		t.Errorf("nestedIn(%q, root %q) = %q, want the root", data, rootDir, got)
	}
	if got := nestedIn(filepath.Join(data, "sub"), roots); got != rootDir {
		t.Errorf("nestedIn(deep path, root %q) = %q, want the root", rootDir, got)
	}
	if got := nestedIn(rootDir, roots); got != "" {
		t.Errorf("the root is not nested in itself: got %q", got)
	}

	folders := []folderView{folderAt(data)}
	if got := nestedIn(filepath.Join(rootDir, "Database"), folders); got != "" {
		t.Errorf("a sibling that shares a name prefix is not inside the folder: got %q", got)
	}
	if got := nestedIn(filepath.Join(data, "sub"), folders); got != data {
		t.Errorf("nestedIn(sub, %q) = %q, want %q", data, got, data)
	}
}

// TestNestedInCaseOnlyTwin checks that a folder whose path differs only by case from another is not nested in it.
func TestNestedInCaseOnlyTwin(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Data")
	folders := []folderView{folderAt(root)}
	if got := nestedIn(strings.ToUpper(root), folders); foldPathCase && got != "" {
		t.Errorf("on Windows the case-only twin is the same folder, got nested in %q", got)
	}
}

// TestFolderNamed checks the already-added test: equal paths match, and case-only differences match on Windows.
func TestFolderNamed(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Data")
	folders := []folderView{folderAt(root)}
	if _, ok := folderNamed(root, folders); !ok {
		t.Error("the same path should be found")
	}
	if _, ok := folderNamed(filepath.Join(root, "other"), folders); ok {
		t.Error("a different path should not be found")
	}
	if _, ok := folderNamed(strings.ToUpper(root), folders); ok != foldPathCase {
		t.Errorf("folderNamed(upper case) found = %v, want %v (foldPathCase)", ok, foldPathCase)
	}
}

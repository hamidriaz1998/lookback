package index

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// TestWindowsExcludedAttributes checks that a file or folder with the hidden, system or offline attribute is
// excluded, and that its neighbours are not.
func TestWindowsExcludedAttributes(t *testing.T) {
	root := t.TempDir()
	fo := contract.Folder{Path: root}
	setAttr := func(p string, attr uint32) {
		t.Helper()
		name, err := windows.UTF16PtrFromString(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := windows.SetFileAttributes(name, attr); err != nil {
			t.Fatal(err)
		}
	}
	visible := filepath.Join(root, "visible.txt")
	hiddenFile := filepath.Join(root, "secret.txt")
	systemFile := filepath.Join(root, "boot.txt")
	offlineFile := filepath.Join(root, "cloud.txt")
	hiddenDir := filepath.Join(root, "private")
	inHidden := filepath.Join(hiddenDir, "note.txt")
	for _, p := range []string{visible, hiddenFile, systemFile, offlineFile} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(hiddenDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inHidden, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	setAttr(hiddenFile, windows.FILE_ATTRIBUTE_HIDDEN)
	setAttr(systemFile, windows.FILE_ATTRIBUTE_SYSTEM)
	setAttr(offlineFile, attrOffline)
	setAttr(hiddenDir, windows.FILE_ATTRIBUTE_HIDDEN)

	for _, tc := range []struct {
		path  string
		isDir bool
		want  bool
	}{
		{visible, false, false},
		{hiddenFile, false, true},
		{systemFile, false, true},
		{offlineFile, false, true},
		{hiddenDir, true, true},
		{inHidden, false, true}, // below a hidden folder
	} {
		if got := excludedPath(fo, tc.path, tc.isDir); got != tc.want {
			t.Errorf("excludedPath(%s) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

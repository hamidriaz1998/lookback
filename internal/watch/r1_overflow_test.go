//go:build linux

package watch

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOverflowRepairsDirectoryWatches checks that after an inotify queue overflow, a directory whose creation
// event was lost is watched again.
func TestOverflowRepairsDirectoryWatches(t *testing.T) {
	root := t.TempDir()
	fd, err := unixInit()
	if err != nil {
		t.Fatal(err)
	}
	defer closeFD(fd)
	w := &watcher{fd: fd, root: root, dirs: map[int32]string{}, parentWd: -1}
	if err := w.addTree(root); err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(root, "d")
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	drain(fd) // the creation event is lost
	w.parse(overflowEvent())
	w.reconcile()
	found := false
	for _, p := range w.dirs {
		if p == d {
			found = true
		}
	}
	if !found {
		t.Fatalf("directory %s is not watched after an overflow; watched: %v", d, w.dirs)
	}
}

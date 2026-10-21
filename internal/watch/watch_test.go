//go:build linux

package watch

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestWatchBurstsAndSkips checks that a burst of writes in a new subdirectory fires once, and that changes in
// a skipped directory fire nothing.
func TestWatchBurstsAndSkips(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the debounce window")
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "skipped"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var fired atomic.Int32
	skip := func(p string, isDir bool) bool { return isDir && filepath.Base(p) == "skipped" }
	if err := Watch(ctx, root, skip, func() { fired.Add(1) }); err != nil {
		t.Fatal(err)
	}

	// A new directory is watched too, so writes inside it are seen.
	sub := filepath.Join(root, "new")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	for i := 0; i < 20; i++ {
		if err := os.WriteFile(filepath.Join(sub, "f.txt"), []byte{byte(i)}, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for fired.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond) // one burst, quiet for one second, must not split
	if n := fired.Load(); n != 1 {
		t.Fatalf("callbacks for one burst = %d, want 1", n)
	}

	// Changes under the skipped directory are not watched.
	if err := os.WriteFile(filepath.Join(root, "skipped", "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if n := fired.Load(); n != 1 {
		t.Fatalf("callbacks after a change in a skipped directory = %d, want 1", n)
	}
}

// TestWatchMissingRoot checks that a root that does not exist is an error.
func TestWatchMissingRoot(t *testing.T) {
	if err := Watch(context.Background(), filepath.Join(t.TempDir(), "nope"), nil, func() {}); err == nil {
		t.Fatal("Watch on a missing root returned no error")
	}
}

package watch

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// waitCount waits until fired exceeds n, or fails after d.
func waitCount(t *testing.T, fired *atomic.Int32, n int32, d time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for fired.Load() <= n {
		if time.Now().After(deadline) {
			t.Fatalf("no change callback after %s within %v", what, d)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRootReplacedIsWatchedAgain checks that a root renamed away and recreated is watched again, so an edit in
// the new root is seen.
func TestRootReplacedIsWatchedAgain(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the debounce window")
	}
	base := t.TempDir()
	root := filepath.Join(base, "root")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var fired atomic.Int32
	if err := Watch(ctx, root, nil, func() { fired.Add(1) }); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(root, filepath.Join(base, "root2")); err != nil {
		t.Fatal(err)
	}
	waitCount(t, &fired, 0, 5*time.Second, "renaming the root")
	time.Sleep(2 * time.Second) // the burst settles
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second) // the recreated root is watched after the burst settles
	base0 := fired.Load()
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitCount(t, &fired, base0, 5*time.Second, "an edit in the recreated root")
}

// TestMovedSubtreeIsUnwatched checks that a watched directory moved out of the root stops triggering scans of it.
func TestMovedSubtreeIsUnwatched(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the debounce window")
	}
	base := t.TempDir()
	root := filepath.Join(base, "root")
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var fired atomic.Int32
	if err := Watch(ctx, root, nil, func() { fired.Add(1) }); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "outside")
	if err := os.Rename(sub, outside); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond) // the move burst settles
	before := fired.Load()
	if err := os.WriteFile(filepath.Join(outside, "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond)
	if n := fired.Load(); n != before {
		t.Fatalf("a change outside the root fired %d callbacks", n-before)
	}
}

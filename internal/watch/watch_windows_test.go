package watch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestOverflowMarksBurstDirty checks the ERROR_NOTIFY_ENUM_DIR path. The read buffer is small and skip drops every
// event, so a normal batch fires nothing; only a lost-event overflow can. skip blocks on the first event while many
// files are created, which fills the kernel's buffer past the size of the next read.
func TestOverflowMarksBurstDirty(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for the debounce window")
	}
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first := make(chan struct{})
	release := make(chan struct{})
	var once atomic.Bool
	skip := func(string, bool) bool {
		if once.CompareAndSwap(false, true) {
			close(first)
			<-release
		}
		return true
	}
	var fired atomic.Int32
	if err := watchWithBuffer(ctx, root, skip, func() { fired.Add(1) }, 1024); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "first.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("the first event never arrived")
	}
	for i := 0; i < 400; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-with-a-longer-name-%04d.txt", i)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if fired.Load() != 0 {
		t.Fatalf("a callback fired before the overflow, with every event skipped")
	}
	close(release)

	deadline := time.Now().Add(8 * time.Second)
	for fired.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if fired.Load() == 0 {
		t.Fatal("a buffer overflow did not make the burst fire")
	}
}

package bench

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/extract"
)

// mkfifo returns a named pipe in dir, which has no writer: opening it for reading would block.
func mkfifo(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "pipe.txt")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestExtractPickRefusesAFIFOWithoutBlocking: extraction of a FIFO that replaced a picked file returns an error at
// once. The helper is called directly, so checkPick and Run are not part of the test.
func TestExtractPickRefusesAFIFOWithoutBlocking(t *testing.T) {
	fifo := mkfifo(t, t.TempDir())
	done := make(chan error, 1)
	go func() {
		_, err := extractPick(context.Background(), extract.NewRegistry(), fifo)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("extractPick accepted a FIFO")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("extractPick blocked on a FIFO")
	}
}

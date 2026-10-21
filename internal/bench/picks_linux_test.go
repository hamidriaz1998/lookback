package bench

import (
	"path/filepath"
	"syscall"
	"testing"
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

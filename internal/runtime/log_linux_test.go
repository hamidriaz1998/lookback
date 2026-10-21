//go:build linux

package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

// TestLogRotatesWhileChildrenWrite: children that write past 10 MB during their lifetime, and two children that share
// one log path, leave a rotated log behind and lose no bytes.
func TestLogRotatesWhileChildrenWrite(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "llama-server.log")
	script := writeScript(t, "head -c 4000000 /dev/zero\nhead -c 4000000 /dev/zero >&2\n")
	opts := Options{Files: Files{Server: script}, LogPath: logPath}
	p1, err := startProcess(opts, 1, "k")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := startProcess(opts, 2, "k")
	if err != nil {
		t.Fatal(err)
	}
	<-p1.exited
	<-p2.exited
	rotated, err := os.Stat(logPath + ".1")
	if err != nil {
		t.Fatalf("no rotated log after 16 MB of child output: %v", err)
	}
	cur, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Size() > logRotateSize || rotated.Size() > logRotateSize {
		t.Fatalf("log sizes %d and %d exceed the %d byte limit", cur.Size(), rotated.Size(), logRotateSize)
	}
	if total := cur.Size() + rotated.Size(); total != 16_000_000 {
		t.Fatalf("log holds %d bytes, want the 16000000 the children wrote", total)
	}
}

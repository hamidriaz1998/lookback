//go:build linux

package runtime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestStartRetriesAfterBindCollision(t *testing.T) {
	testBin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	marker := filepath.Join(dir, "first-attempt")
	script := filepath.Join(dir, "llama-server")
	body := fmt.Sprintf("#!/bin/sh\nif [ ! -e %q ]; then\n touch %q\n echo 'bind: address already in use' >&2\n exit 1\nfi\nexec %q \"$@\"\n", marker, marker, testBin)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	srv, err := Start(context.Background(), Options{
		Files:   Files{Server: script, Model: "text.gguf", MMProj: "mmproj.gguf"},
		Threads: 1, LogPath: filepath.Join(dir, "llama-server.log"),
	})
	if err != nil {
		t.Fatalf("Start after bind collision: %v", err)
	}
	defer srv.Stop(context.Background())
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("first colliding attempt did not run: %v", err)
	}
}

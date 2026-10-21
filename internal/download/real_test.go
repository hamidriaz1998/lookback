package download

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/catalog"
	"github.com/hamidriaz1998/lookback/internal/contract"
)

// TestRealCPURuntimeResume downloads the pinned llama.cpp CPU archive from GitHub, interrupts it after
// 4 MB, resumes it in a new downloader, and checks that the verified archive was unpacked. It uses a
// temporary folder only. It runs only with LOOKBACK_REAL_NET=1, so a slow link does not fail the gates.
func TestRealCPURuntimeResume(t *testing.T) {
	if testing.Short() || os.Getenv("LOOKBACK_REAL_NET") != "1" {
		t.Skip("downloads 22 MB from GitHub; set LOOKBACK_REAL_NET=1 to run it")
	}
	cat, err := catalog.New()
	if err != nil {
		t.Fatal(err)
	}
	assets, err := cat.Assets("linux", contract.Combo{Backend: contract.BackendCPU, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16})
	if err != nil {
		t.Fatal(err)
	}
	var runtime []contract.Asset
	for _, a := range assets {
		if a.Part == contract.PartRuntime {
			runtime = append(runtime, a)
		}
	}
	a := runtime[0]
	dir := t.TempDir()
	part := partOf(dir, a)

	ctx1, cancel1 := context.WithCancel(context.Background())
	d1, err := New(ctx1, Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := d1.Ensure(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Minute)
	for fileSize(part) < 4<<20 {
		if time.Now().After(deadline) {
			t.Fatalf("only %d bytes after 2 minutes", fileSize(part))
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop(t, cancel1, d1)
	killedAt := fileSize(part)
	if killedAt <= 0 || killedAt >= a.Bytes {
		t.Fatalf("interrupted at %d of %d bytes; the test needs a partial file", killedAt, a.Bytes)
	}
	t.Logf("interrupted at %d of %d bytes", killedAt, a.Bytes)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	d2, err := New(ctx2, Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := d2.Ensure(context.Background(), runtime); err != nil {
		t.Fatal(err)
	}
	// Done means the SHA-256 matched the pin and the archive was unpacked (SPEC section 7).
	done := waitFor(t, d2, a.Key(), isState(contract.FileDone))
	t.Logf("done: %d bytes verified", done.Done)

	server := filepath.Join(dir, "runtimes", "b11755-cpu", "llama-b11755", "llama-server")
	if _, err := os.Stat(server); err != nil {
		t.Fatalf("llama-server was not unpacked: %v", err)
	}
	if fileSize(part) != -1 {
		t.Error("the part was not removed after install")
	}
}

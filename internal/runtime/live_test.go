package runtime

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests run the real llama-server on real model files, so they run only when LOOKBACK_LIVE_DIR names a
// folder with the runtime unpacked in llama/ (llama-server, or llama-server.exe on Windows) and the Q8_0 or BF16
// model pair as text.gguf and mmproj.gguf. They check what the fake server cannot: that the platform's process
// code starts the real server, measures it, stops it, and takes it down when the app dies (SPEC section 8).

// liveFiles returns the files under LOOKBACK_LIVE_DIR, or skips the test when it is not set.
func liveFiles(t testing.TB) Files {
	t.Helper()
	dir := os.Getenv("LOOKBACK_LIVE_DIR")
	if dir == "" {
		t.Skip("LOOKBACK_LIVE_DIR is not set")
	}
	return liveFilesIn(dir)
}

func liveFilesIn(dir string) Files {
	exe := "llama-server"
	if goruntime.GOOS == "windows" {
		exe += ".exe"
	}
	return Files{
		Server: filepath.Join(dir, "llama", exe),
		Model:  filepath.Join(dir, "text.gguf"),
		MMProj: filepath.Join(dir, "mmproj.gguf"),
	}
}

// liveParent is the "app" of TestLiveSidecarDiesWithApp: it starts a real server, prints the server's PID, and
// waits to be killed. The test binary runs it when LOOKBACK_LIVE_PARENT is set (see TestMain).
func liveParent() {
	srv, err := Start(context.Background(), Options{
		Files:   liveFilesIn(os.Getenv("LOOKBACK_LIVE_DIR")),
		Threads: 4,
		LogPath: filepath.Join(os.Getenv("LOOKBACK_LIVE_LOG")),
	})
	if err != nil {
		fmt.Println("ERROR", err)
		os.Exit(1)
	}
	fmt.Println("PID", srv.PID())
	time.Sleep(10 * time.Minute)
}

func TestLiveSidecar(t *testing.T) {
	files := liveFiles(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	srv, err := Start(ctx, Options{Files: files, Threads: 4, LogPath: filepath.Join(t.TempDir(), "llama-server.log")})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Stop(context.Background())
	pid := srv.PID()
	if !alive(pid) {
		t.Fatal("the server is not running after Start")
	}

	vecs, tokens, err := srv.EmbedTexts(ctx, []string{"task: search result | query: the error dialog about the hotspot"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 1 || len(vecs[0]) != 768 || tokens <= 0 {
		t.Fatalf("got %d vectors of %d dims and %d tokens", len(vecs), len(vecs[0]), tokens)
	}
	var norm float64
	for _, v := range vecs[0] {
		norm += float64(v) * float64(v)
	}
	if math.Abs(math.Sqrt(norm)-1) > 0.01 {
		t.Errorf("vector norm %.4f, want 1", math.Sqrt(norm))
	}
	if peak := srv.PeakRAM(); peak < 100<<20 {
		t.Errorf("peak RAM %d bytes, want at least 100 MiB for a loaded model", peak)
	} else {
		t.Logf("peak RAM %d MiB", peak>>20)
	}

	srv.Stop(ctx)
	select {
	case <-srv.Exited():
	case <-time.After(15 * time.Second):
		t.Fatal("Exited did not close after Stop")
	}
	if alive(pid) {
		t.Error("the server still runs after Stop")
	}
}

// TestLiveSidecarDiesWithApp kills the app without a clean shutdown, as kill -9 or a crash does, and checks that
// llama-server goes with it: Pdeathsig on Linux, the kill-on-close job object on Windows.
func TestLiveSidecarDiesWithApp(t *testing.T) {
	liveFiles(t)
	parent := exec.Command(os.Args[0], "-test.run=^$")
	parent.Env = append(os.Environ(), "LOOKBACK_LIVE_PARENT=1", "LOOKBACK_LIVE_LOG="+filepath.Join(t.TempDir(), "llama-server.log"))
	out, err := parent.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	defer parent.Process.Kill()
	line := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(out)
		if sc.Scan() {
			line <- sc.Text()
		}
		close(line)
	}()
	var pid int
	select {
	case l, ok := <-line:
		f := strings.Fields(l)
		if !ok || len(f) != 2 || f[0] != "PID" {
			t.Fatalf("the app printed %q", l)
		}
		pid, _ = strconv.Atoi(f[1])
	case <-time.After(3 * time.Minute):
		t.Fatal("the app did not start its server")
	}
	if !alive(pid) {
		t.Fatal("the server is not running")
	}
	if err := parent.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the server to die with its app", func() bool { return !alive(pid) })
}

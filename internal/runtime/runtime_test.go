package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

var (
	spaceA = contract.VectorSpace{TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0, ModelRev: "rev"}
	spaceB = contract.VectorSpace{TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16, ModelRev: "rev"}
	spaceC = contract.VectorSpace{TextQuant: contract.QuantQ8_0, EncQuant: contract.QuantQ8_0, ModelRev: "rev"}
)

// testRuntime returns a Runtime whose sidecars are the fake llama-server (see main_test.go).
func testRuntime(t *testing.T, b contract.Backend) (*Runtime, context.CancelFunc) {
	t.Helper()
	return countedRuntime(t, b, func(_ int, _ contract.Backend, s contract.VectorSpace) (Files, error) {
		return fakeFiles(t, s), nil
	})
}

// countedRuntime returns a Runtime whose Files function is files. The call number is 1 for the first launch.
func countedRuntime(t *testing.T, b contract.Backend,
	files func(call int, b contract.Backend, s contract.VectorSpace) (Files, error)) (*Runtime, context.CancelFunc) {
	t.Helper()
	life, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var calls atomic.Int32
	cfg := Config{
		Life:    life,
		Backend: b,
		LogPath: filepath.Join(t.TempDir(), "logs", "llama-server.log"),
		Threads: 2,
		Files: func(bk contract.Backend, s contract.VectorSpace) (Files, error) {
			return files(int(calls.Add(1)), bk, s)
		},
	}
	return New(cfg), cancel
}

// fakeFiles runs the test binary as the fake llama-server for space s.
func fakeFiles(t *testing.T, s contract.VectorSpace) Files {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Files{Server: self, Model: "/fake/text-" + string(s.TextQuant) + ".gguf",
		MMProj: "/fake/mmproj-" + string(s.EncQuant) + ".gguf"}
}

// waitFor polls cond for up to 30 s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// pidOf returns the process ID of the sidecar of space s, or 0.
func pidOf(r *Runtime, s contract.VectorSpace) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, sp := range r.open {
		if sp.key == s && sp.srv != nil {
			return sp.srv.PID()
		}
	}
	return 0
}

func cosine(a, b []float32) float64 {
	var dot float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
	}
	return dot
}

func TestSessionsEmbedInTheirOwnSpace(t *testing.T) {
	r, _ := testRuntime(t, contract.BackendVulkan)
	ctx := context.Background()
	sa, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()
	sb, err := r.Session(ctx, spaceB)
	if err != nil {
		t.Fatal(err)
	}
	defer sb.Close()
	if _, err := r.Session(ctx, spaceC); !errors.Is(err, contract.ErrTooManySpaces) {
		t.Fatalf("third space: got %v, want ErrTooManySpaces", err)
	}
	va, _, err := sa.EmbedTexts(ctx, []string{"hello"})
	if err != nil || len(va) != 1 || len(va[0]) != embeddingDims {
		t.Fatalf("space A embed: %v, %d vectors", err, len(va))
	}
	vb, _, err := sb.EmbedTexts(ctx, []string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	if sa.Space() != spaceA || sb.Space() != spaceB {
		t.Fatalf("sessions report the wrong spaces")
	}
	if c := cosine(va[0], vb[0]); c > 0.99 {
		t.Fatalf("vectors of two spaces agree (cosine %.3f); spaces must not mix", c)
	}
	st := r.Status()
	if len(st.Spaces) != 2 || st.State != contract.RuntimeReady {
		t.Fatalf("status: %+v", st)
	}
	if err := sa.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sa.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	waitFor(t, "space A to close", func() bool { return len(r.Status().Spaces) == 1 })
	if _, _, err := sa.EmbedTexts(ctx, []string{"x"}); !errors.Is(err, contract.ErrNotReady) {
		t.Fatalf("closed session: got %v, want ErrNotReady", err)
	}
}

func TestCanceledAcquisitionKeepsOtherHandle(t *testing.T) {
	r, _ := testRuntime(t, contract.BackendVulkan)
	ctx := context.Background()
	keep, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer keep.Close()
	dead, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.Session(dead, spaceA); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled acquisition: got %v", err)
	}
	if _, _, err := keep.EmbedTexts(ctx, []string{"still works"}); err != nil {
		t.Fatalf("other handle after a canceled acquisition: %v", err)
	}
	if n := len(r.Status().Spaces); n != 1 {
		t.Fatalf("spaces open: %d, want 1", n)
	}
}

func TestColdSpaceAcquisitionCanceledStopsItsSidecar(t *testing.T) {
	r, _ := testRuntime(t, contract.BackendVulkan)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	sess, err := r.Session(ctx, spaceB)
	if err == nil {
		// The start won the race. The handle must still work, and closing it ends the space.
		sess.Close()
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled or a session", err)
	}
	waitFor(t, "no open spaces", func() bool { return len(r.Status().Spaces) == 0 })
	if st := r.Status().State; st != contract.RuntimeStopped {
		t.Fatalf("state with no space open: %s", st)
	}
}

func TestUseRestartsOpenSidecars(t *testing.T) {
	r, _ := testRuntime(t, contract.BackendVulkan)
	ctx := context.Background()
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	before := pidOf(r, spaceA)
	if err := r.Use(ctx, contract.BackendCPU); err != nil {
		t.Fatalf("Use: %v", err)
	}
	if r.Backend() != contract.BackendCPU {
		t.Fatalf("backend %s after Use(cpu)", r.Backend())
	}
	if pidOf(r, spaceA) == before {
		t.Fatal("Use did not restart the sidecar")
	}
	if err := sess.Ready(ctx); err != nil {
		t.Fatalf("Ready after Use: %v", err)
	}
	if _, _, err := sess.EmbedTexts(ctx, []string{"after use"}); err != nil {
		t.Fatalf("embed after Use: %v", err)
	}
}

// TestKillTwiceFallsBackToCPU is the acceptance check of SPEC section 8: the first unexpected exit restarts the
// sidecar, the second moves it to the CPU, and embedding keeps working.
func TestKillTwiceFallsBackToCPU(t *testing.T) {
	r, _ := testRuntime(t, contract.BackendVulkan)
	ctx := context.Background()
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	first := pidOf(r, spaceA)
	if err := killPID(first); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "restart after the first kill", func() bool {
		p := pidOf(r, spaceA)
		return p != 0 && p != first && r.Status().State == contract.RuntimeReady
	})
	if r.Backend() != contract.BackendVulkan {
		t.Fatalf("backend %s after one kill, want vulkan", r.Backend())
	}

	second := pidOf(r, spaceA)
	if err := killPID(second); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "CPU fallback after the second kill", func() bool {
		return r.Status().State == contract.RuntimeCPUFallback
	})
	if r.Backend() != contract.BackendCPU {
		t.Fatalf("backend %s after two kills, want cpu", r.Backend())
	}
	if err := sess.Ready(ctx); err != nil {
		t.Fatalf("Ready on the fallback: %v", err)
	}
	if _, _, err := sess.EmbedTexts(ctx, []string{"on the cpu"}); err != nil {
		t.Fatalf("embed on the CPU fallback: %v", err)
	}
	if st := r.Status().Spaces[0]; st.State != contract.RuntimeCPUFallback {
		t.Fatalf("space state %s, want cpu-fallback", st.State)
	}
	// The user can go back to the GPU build.
	if err := r.RetryGPU(ctx); err != nil {
		t.Fatalf("RetryGPU: %v", err)
	}
	if r.Backend() != contract.BackendVulkan || r.Status().State != contract.RuntimeReady {
		t.Fatalf("after RetryGPU: backend %s, state %s", r.Backend(), r.Status().State)
	}
}

// TestBadImageRestartsTheSidecar: SPEC section 8 restarts the sidecar once after a request fails with a server
// error. The fake answers HTTP 500 for a bad image, so the request reports ErrRestarting and the sidecar comes back.
func TestBadImageRestartsTheSidecar(t *testing.T) {
	r, _ := testRuntime(t, contract.BackendVulkan)
	ctx := context.Background()
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	pid := pidOf(r, spaceA)
	_, _, err = sess.EmbedImage(ctx, []byte("BAD image"), "image/png")
	if !errors.Is(err, contract.ErrRestarting) {
		t.Fatalf("bad image: got %v, want ErrRestarting", err)
	}
	if err := sess.Ready(ctx); err != nil {
		t.Fatalf("Ready after the restart: %v", err)
	}
	if pidOf(r, spaceA) == pid {
		t.Fatal("a server error did not restart the sidecar")
	}
	if r.Backend() != contract.BackendVulkan {
		t.Fatalf("backend %s after one server error, want vulkan", r.Backend())
	}
	if _, _, err := sess.EmbedTexts(ctx, []string{"after the restart"}); err != nil {
		t.Fatalf("embed after the restart: %v", err)
	}
}

func TestStopThenSessionStartsAgain(t *testing.T) {
	r, _ := testRuntime(t, contract.BackendVulkan)
	ctx := context.Background()
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := r.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, err := sess.EmbedTexts(ctx, []string{"x"}); !errors.Is(err, contract.ErrNotReady) {
		t.Fatalf("after Stop: got %v, want ErrNotReady", err)
	}
	if st := r.Status().State; st != contract.RuntimeStopped {
		t.Fatalf("state after Stop: %s", st)
	}
	sess2, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatalf("Session after Stop: %v", err)
	}
	defer sess2.Close()
	if _, _, err := sess2.EmbedTexts(ctx, []string{"x"}); err != nil {
		t.Fatal(err)
	}
}

func TestStatusStreamStartsWithSnapshotAndEndsWithLifetime(t *testing.T) {
	r, endLife := testRuntime(t, contract.BackendVulkan)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := r.Subscribe(ctx)
	if first := <-ch; first.Backend != contract.BackendVulkan || first.State != contract.RuntimeStopped {
		t.Fatalf("first status %+v, want the snapshot", first)
	}
	sess, err := r.Session(context.Background(), spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	pid := pidOf(r, spaceA)
	sawReady := false
	for st := range ch {
		if st.State == contract.RuntimeReady {
			sawReady = true
			break
		}
	}
	if !sawReady {
		t.Fatal("stream did not report ready")
	}
	// Ending the lifetime stops the sidecar, closes the stream and ends Session calls with ErrStopped.
	endLife()
	for range ch {
	}
	waitFor(t, "sidecar to exit", func() bool { return !alive(pid) })
	if _, err := r.Session(context.Background(), spaceA); !errors.Is(err, contract.ErrStopped) {
		t.Fatalf("Session after lifetime end: %v", err)
	}
}

func TestTokenizeOffsetsCoverTheText(t *testing.T) {
	r, _ := testRuntime(t, contract.BackendVulkan)
	ctx := context.Background()
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	text := "naïve 日本語 🙂 ꙮ𓀀"
	toks, err := sess.Tokenize(ctx, text)
	if err != nil {
		t.Fatal(err)
	}
	var joined strings.Builder
	pos := 0
	for _, tk := range toks {
		if tk.Start != pos || tk.End != pos+len(tk.Piece) {
			t.Fatalf("token %q: range [%d,%d) after %d", tk.Piece, tk.Start, tk.End, pos)
		}
		joined.WriteString(tk.Piece)
		pos = tk.End
	}
	if joined.String() != text {
		t.Fatalf("pieces joined = %q, want %q", joined.String(), text)
	}
}

func TestStartWaitEndsWhenTheChildExits(t *testing.T) {
	// /bin/false exits at once. The startup wait must end then, not after its 120 s deadline.
	begin := time.Now()
	_, err := Start(context.Background(), Options{Files: Files{Server: falseBin}, Threads: 1,
		LogPath: filepath.Join(t.TempDir(), "llama-server.log")})
	if err == nil {
		t.Fatal("Start of a child that exits returned no error")
	}
	if d := time.Since(begin); d > 20*time.Second {
		t.Fatalf("startup wait took %s after the child exited", d)
	}
}

func TestArgvCarriesTheSpecFlags(t *testing.T) {
	cpu := strings.Join(argv(Options{Files: Files{Model: "m.gguf", MMProj: "p.gguf"}, Threads: 8}, 1234, "key"), " ")
	for _, want := range []string{"--embeddings", "--pooling mean", "-c 8192", "-t 8", "--host 127.0.0.1",
		"--port 1234", "--api-key key"} {
		if !strings.Contains(cpu, want) {
			t.Fatalf("argv %q lacks %q", cpu, want)
		}
	}
	if strings.Contains(cpu, "-ngl") {
		t.Fatal("CPU build got -ngl")
	}
	gpu := strings.Join(argv(Options{Files: Files{}, GPU: true, Threads: 8}, 1, "k"), " ")
	if !strings.Contains(gpu, "-ngl 99") {
		t.Fatalf("GPU build lacks -ngl 99: %q", gpu)
	}
}

func TestModelPathsUseSpecNames(t *testing.T) {
	text, mm, err := ModelPaths("/d", contract.VectorSpace{TextQuant: contract.QuantQ8_0, EncQuant: contract.QuantBF16})
	if err != nil {
		t.Fatal(err)
	}
	if text != filepath.Join("/d", "embeddinggemma-2-Q8_0.gguf") || mm != filepath.Join("/d", "mmproj-embeddinggemma-2-BF16.gguf") {
		t.Fatalf("paths %s %s", text, mm)
	}
	if _, _, err := ModelPaths("/d", contract.VectorSpace{TextQuant: "f16"}); err == nil {
		t.Fatal("F16 accepted")
	}
}

func TestStreamDeliversSnapshotAndClosesWithContext(t *testing.T) {
	s := NewStream(func(v int) string { return "k" }, func(int) bool { return false })
	s.Publish(1)
	ctx, cancel := context.WithCancel(context.Background())
	ch := s.Subscribe(ctx)
	if v := <-ch; v != 1 {
		t.Fatalf("snapshot %d, want 1", v)
	}
	s.Publish(2)
	if v := <-ch; v != 2 {
		t.Fatalf("update %d, want 2", v)
	}
	cancel()
	for range ch {
	}
}

// TestSessionCallsAfterShutdownReturnStopped: once the lifetime ends, session calls return ErrStopped, not
// ErrNotReady, even before the shutdown has stopped the sidecar.
func TestSessionCallsAfterShutdownReturnStopped(t *testing.T) {
	r, endLife := testRuntime(t, contract.BackendVulkan)
	ctx := context.Background()
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	endLife()
	waitFor(t, "stopped", func() bool { return r.Status().State == contract.RuntimeStopped })
	if _, _, err := sess.EmbedTexts(ctx, []string{"x"}); !errors.Is(err, contract.ErrStopped) {
		t.Fatalf("EmbedTexts after shutdown: %v, want ErrStopped", err)
	}
	if _, err := sess.Tokenize(ctx, "x"); !errors.Is(err, contract.ErrStopped) {
		t.Fatalf("Tokenize after shutdown: %v, want ErrStopped", err)
	}
}

// TestClosingOneSharedHandleWakesItsReady: closing one of two handles to the same space ends that handle's Ready
// with ErrNotReady, while the other handle keeps the space.
func TestClosingOneSharedHandleWakesItsReady(t *testing.T) {
	r, _ := testRuntime(t, contract.BackendVulkan)
	ctx := context.Background()
	a, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := r.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- a.Ready(ctx) }()
	time.Sleep(100 * time.Millisecond)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, contract.ErrNotReady) {
			t.Fatalf("Ready on a closed handle: %v, want ErrNotReady", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Ready of a closed handle stayed blocked")
	}
}

// TestUseWithoutSessionsPublishesBackend: with no open space, a Use still changes the status that subscribers see.
func TestUseWithoutSessionsPublishesBackend(t *testing.T) {
	r, _ := testRuntime(t, contract.BackendVulkan)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := r.Subscribe(ctx)
	if first := <-ch; first.Backend != contract.BackendVulkan {
		t.Fatalf("snapshot backend %s", first.Backend)
	}
	if err := r.Use(context.Background(), contract.BackendCPU); err != nil {
		t.Fatalf("Use with no sessions: %v", err)
	}
	select {
	case st := <-ch:
		if st.Backend != contract.BackendCPU {
			t.Fatalf("update after Use: backend %s, want cpu", st.Backend)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("no status update after Use")
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	if st := <-r.Subscribe(ctx2); st.Backend != contract.BackendCPU {
		t.Fatalf("new subscription after Use starts with backend %s", st.Backend)
	}
}

// TestReadyOnReadySessionHonorsCanceledContext: Ready reports the caller's ctx error when that context has ended,
// even when the sidecar is already ready.
func TestReadyOnReadySessionHonorsCanceledContext(t *testing.T) {
	r, _ := testRuntime(t, contract.BackendVulkan)
	sess, err := r.Session(context.Background(), spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	dead, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sess.Ready(dead); !errors.Is(err, context.Canceled) {
		t.Fatalf("Ready with a canceled context on a ready session: %v, want context.Canceled", err)
	}
}

// TestStableRestartForgetsTheFailure checks SPEC section 8 (Q23): the sidecar restarts once, and the CPU fallback
// comes when that restart fails again. A restarted sidecar that stays up has not failed again, so a crash much
// later restarts it once more and does not move it to the CPU.
func TestStableRestartForgetsTheFailure(t *testing.T) {
	life, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	r := New(Config{
		Life:        life,
		Backend:     contract.BackendVulkan,
		LogPath:     filepath.Join(t.TempDir(), "logs", "llama-server.log"),
		Threads:     2,
		StableAfter: 300 * time.Millisecond,
		Files: func(_ contract.Backend, s contract.VectorSpace) (Files, error) {
			return fakeFiles(t, s), nil
		},
	})
	sess, err := r.Session(context.Background(), spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()

	for i := 1; i <= 3; i++ { // three crashes, each long after the one before
		pid := pidOf(r, spaceA)
		if err := killPID(pid); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "a restart", func() bool {
			p := pidOf(r, spaceA)
			return p != 0 && p != pid && r.Status().State == contract.RuntimeReady
		})
		if r.Backend() != contract.BackendVulkan {
			t.Fatalf("backend %s after crash %d, want vulkan: a stable sidecar must not fall back", r.Backend(), i)
		}
		waitFor(t, "the restarted sidecar to count as stable", func() bool {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.open[0].failures == 0
		})
	}
}

package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// writeScript writes a /bin/sh llama-server stand-in with body and returns its path.
func writeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "llama-server")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// deadFiles is a launch whose server exits at once.
func deadFiles() Files { return Files{Server: "/bin/false", Model: "m", MMProj: "p"} }

// blockedFiles is a launch whose server writes its PID to pidFile and never becomes ready.
func blockedFiles(t *testing.T, pidFile string) Files {
	body := fmt.Sprintf("echo $$ > %q\nexec sleep 600\n", pidFile)
	return Files{Server: writeScript(t, body), Model: "m", MMProj: "p"}
}

// readPID returns the PID that a blockedFiles server wrote.
func readPID(t *testing.T, pidFile string) int {
	t.Helper()
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

// launchFailsOn returns a Files function: launch number call fails at once, the others are the fake server.
func launchFailsOn(t *testing.T, call int) func(int, contract.Backend, contract.VectorSpace) (Files, error) {
	return func(n int, _ contract.Backend, s contract.VectorSpace) (Files, error) {
		if n == call {
			return deadFiles(), nil
		}
		return fakeFiles(t, s), nil
	}
}

// TestUseWaitsThroughItsOwnRecovery: a GPU launch that fails is restarted by the same Use, which then returns nil
// with the sidecar ready. It does not return ErrRestarting for its own restart.
func TestUseWaitsThroughItsOwnRecovery(t *testing.T) {
	r, _ := countedRuntime(t, contract.BackendCPU, launchFailsOn(t, 2)) // launch 1 is the session's CPU sidecar
	ctx := context.Background()
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if err := r.Use(ctx, contract.BackendVulkan); err != nil {
		t.Fatalf("Use after a failed first launch: %v", err)
	}
	if st := r.Status().State; st != contract.RuntimeReady || r.Backend() != contract.BackendVulkan {
		t.Fatalf("after Use: state %s, backend %s", st, r.Backend())
	}
	if _, _, err := sess.EmbedTexts(ctx, []string{"recovered"}); err != nil {
		t.Fatalf("embed after the recovery: %v", err)
	}
}

// TestUseCancelStopsItsBlockedRecovery: when the Use's context ends while its restart is blocked, Use returns the
// context's error and the blocked server is gone.
func TestUseCancelStopsItsBlockedRecovery(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	r, _ := countedRuntime(t, contract.BackendCPU, func(n int, _ contract.Backend, s contract.VectorSpace) (Files, error) {
		switch n {
		case 2:
			return deadFiles(), nil
		case 3:
			return blockedFiles(t, pidFile), nil
		}
		return fakeFiles(t, s), nil
	})
	ctx := context.Background()
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	uctx, cancel := context.WithCancel(ctx)
	defer cancel()
	useErr := make(chan error, 1)
	go func() { useErr <- r.Use(uctx, contract.BackendVulkan) }()
	waitFor(t, "the blocked restart", func() bool { _, err := os.Stat(pidFile); return err == nil })
	cancel()
	select {
	case err := <-useErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Use after cancel: %v, want context.Canceled", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Use did not return after its context ended")
	}
	pid := readPID(t, pidFile)
	waitFor(t, "the blocked server to exit", func() bool { return syscall.Kill(pid, 0) != nil })
}

// TestUseIsSupersededOnlyByLaterUseOrStop: while its restart is blocked and its context is alive, Use keeps
// waiting. A later Stop supersedes it.
func TestUseIsSupersededOnlyByLaterUseOrStop(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	r, _ := countedRuntime(t, contract.BackendCPU, func(n int, _ contract.Backend, s contract.VectorSpace) (Files, error) {
		switch n {
		case 2:
			return deadFiles(), nil
		case 3:
			return blockedFiles(t, pidFile), nil
		}
		return fakeFiles(t, s), nil
	})
	ctx := context.Background()
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	useErr := make(chan error, 1)
	go func() { useErr <- r.Use(ctx, contract.BackendVulkan) }()
	waitFor(t, "the blocked restart", func() bool { _, err := os.Stat(pidFile); return err == nil })
	select {
	case err := <-useErr:
		t.Fatalf("Use returned %v before a later Stop", err)
	case <-time.After(500 * time.Millisecond):
	}
	if err := r.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-useErr:
		if !errors.Is(err, contract.ErrRestarting) {
			t.Fatalf("superseded Use: %v, want ErrRestarting", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Use did not return after a later Stop")
	}
}

// TestConcurrentServerErrorsCountOnce: four requests of one epoch all fail with a server error. Each returns a
// controlled serverError after all four have captured the epoch, so the outcome does not depend on HTTP timing. Only
// the first counts: the sidecar restarts once (two launches in all) and stays on the GPU backend. Removing the epoch
// guard in serverFailed makes the second failure fall back to the CPU, and this test fails.
func TestConcurrentServerErrorsCountOnce(t *testing.T) {
	var launches atomic.Int32
	r, _ := countedRuntime(t, contract.BackendVulkan, func(_ int, _ contract.Backend, s contract.VectorSpace) (Files, error) {
		launches.Add(1)
		return fakeFiles(t, s), nil
	})
	ctx := context.Background()
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	const n = 4
	var arrived sync.WaitGroup
	arrived.Add(n)
	var errs [n]error
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = sess.(*session).call(ctx, func(*Server) error {
				arrived.Done()
				arrived.Wait() // every request has captured the epoch before any of them fails
				return &serverError{status: http.StatusInternalServerError, msg: "runtime: server error (HTTP 500)"}
			})
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if !errors.Is(err, contract.ErrRestarting) {
			t.Fatalf("request %d: %v, want ErrRestarting", i, err)
		}
	}
	if err := sess.Ready(ctx); err != nil {
		t.Fatalf("Ready: %v", err)
	}
	if r.Backend() != contract.BackendVulkan || r.Status().State != contract.RuntimeReady {
		t.Fatalf("after four errors of one epoch: backend %s, state %s; want one restart on vulkan",
			r.Backend(), r.Status().State)
	}
	if n := launches.Load(); n != 2 {
		t.Fatalf("%d launches, want 2 (the first start and one restart)", n)
	}
}

// TestUseOverlappingWithAnotherUseNeverOverlapsServers: a server that takes about two seconds to exit must be gone
// before the next Use launches its own server. The Files function sees each previous server's wrapper process.
func TestUseOverlappingWithAnotherUseNeverOverlapsServers(t *testing.T) {
	dir := t.TempDir()
	pids := filepath.Join(dir, "pids")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The wrapper runs the fake server and ignores TERM for two seconds before it exits.
	wrapper := writeScript(t, fmt.Sprintf(`trap 'sleep 2; exit 0' TERM
echo $$ >> %q
%q "$@" &
wait $!
`, pids, self))
	var overlaps atomic.Int32
	r, _ := countedRuntime(t, contract.BackendVulkan, func(int, contract.Backend, contract.VectorSpace) (Files, error) {
		if raw, err := os.ReadFile(pids); err == nil {
			for _, f := range strings.Fields(string(raw)) {
				pid, _ := strconv.Atoi(f)
				if syscall.Kill(pid, 0) == nil {
					overlaps.Add(1)
				}
			}
		}
		return Files{Server: wrapper, Model: "m", MMProj: "p"}, nil
	})
	ctx := context.Background()
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	first := make(chan error, 1)
	go func() { first <- r.Use(ctx, contract.BackendCPU) }()
	time.Sleep(300 * time.Millisecond)
	if err := r.Use(ctx, contract.BackendVulkan); err != nil {
		t.Fatalf("second Use: %v", err)
	}
	<-first
	if n := overlaps.Load(); n != 0 {
		t.Fatalf("%d launches started while an earlier server was still running", n)
	}
	if _, _, err := sess.EmbedTexts(ctx, []string{"after overlap"}); err != nil {
		t.Fatalf("embed after overlapping Use calls: %v", err)
	}
}

// TestPartialReplyInterruptedByRestartIsRestarting: a reply that has sent HTTP 200 and part of its body, when a Use
// restarts the sidecar, is cut off by the restart. The caller gets ErrRestarting, not a decode error.
func TestPartialReplyInterruptedByRestartIsRestarting(t *testing.T) {
	r, _ := testRuntime(t, contract.BackendVulkan)
	ctx := context.Background()
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	marker := filepath.Join(t.TempDir(), "partial")
	req := make(chan error, 1)
	go func() {
		_, _, err := sess.EmbedImage(ctx, []byte("PARTIAL:"+marker), "image/png")
		req <- err
	}()
	waitFor(t, "the partial reply", func() bool { _, err := os.Stat(marker); return err == nil })
	if err := r.Use(ctx, contract.BackendCPU); err != nil {
		t.Fatalf("Use: %v", err)
	}
	select {
	case err := <-req:
		if !errors.Is(err, contract.ErrRestarting) {
			t.Fatalf("interrupted reply: %v, want ErrRestarting", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the interrupted request did not return")
	}
}

// TestSuccessfulCallEndingWithLifetimeIsStopped: a request that completes successfully after the runtime's lifetime
// has ended reports ErrStopped, not success or ErrRestarting.
func TestSuccessfulCallEndingWithLifetimeIsStopped(t *testing.T) {
	r, endLife := testRuntime(t, contract.BackendVulkan)
	ctx := context.Background()
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	err = sess.(*session).call(ctx, func(srv *Server) error {
		_, _, e := srv.EmbedTexts(ctx, []string{"finished"})
		endLife() // the lifetime ends while the request is completing
		return e
	})
	if !errors.Is(err, contract.ErrStopped) {
		t.Fatalf("call completing after shutdown: %v, want ErrStopped", err)
	}
}

// TestUseDeadlineBoundsRetiringSlowServer: a ready server that ignores TERM for a minute is retired at once when the
// Use that replaces it runs out of time. The next launch then starts only after that server has gone.
func TestUseDeadlineBoundsRetiringSlowServer(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out the 10 s stop grace of the final cleanup")
	}
	dir := t.TempDir()
	pids := filepath.Join(dir, "pids")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The wrapper keeps running for a minute after TERM, far longer than the stop grace of 10 s.
	wrapper := writeScript(t, fmt.Sprintf(`trap 'sleep 60; exit 0' TERM
echo $$ >> %q
%q "$@" &
wait $!
`, pids, self))
	var overlaps atomic.Int32
	r, _ := countedRuntime(t, contract.BackendVulkan, func(int, contract.Backend, contract.VectorSpace) (Files, error) {
		if raw, err := os.ReadFile(pids); err == nil {
			for _, f := range strings.Fields(string(raw)) {
				pid, _ := strconv.Atoi(f)
				if syscall.Kill(pid, 0) == nil {
					overlaps.Add(1)
				}
			}
		}
		return Files{Server: wrapper, Model: "m", MMProj: "p"}, nil
	})
	ctx := context.Background()
	sess, err := r.Session(ctx, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	slow := pidOf(r, spaceA)

	uctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	begin := time.Now()
	if err := r.Use(uctx, contract.BackendCPU); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Use past its deadline: %v, want DeadlineExceeded", err)
	}
	if d := time.Since(begin); d > 5*time.Second {
		t.Fatalf("Use returned %s after a 300 ms deadline", d)
	}
	if syscall.Kill(slow, 0) == nil {
		t.Fatal("the slow server still runs after Use returned")
	}

	if err := r.Use(ctx, contract.BackendCPU); err != nil {
		t.Fatalf("next Use: %v", err)
	}
	if n := overlaps.Load(); n != 0 {
		t.Fatalf("%d launches started while an earlier server was still running", n)
	}
	if _, _, err := sess.EmbedTexts(ctx, []string{"after the deadline"}); err != nil {
		t.Fatalf("embed after the next Use: %v", err)
	}
}

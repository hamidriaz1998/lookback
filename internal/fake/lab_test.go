package fake

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// nextStep returns the next lab step, or fails the test after timeout.
func nextStep(t *testing.T, ch <-chan contract.LabStep, timeout time.Duration) contract.LabStep {
	t.Helper()
	select {
	case s, ok := <-ch:
		if !ok {
			t.Fatal("lab stream closed")
		}
		return s
	case <-time.After(timeout):
		t.Fatal("no lab step in time")
	}
	return contract.LabStep{}
}

// lockedPause returns the indexer's pause reason under its lock, as a test may read it while a run pauses it.
func lockedPause(x *Indexer) contract.PauseReason {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.pauseReason()
}

// Two runs of the same combination are serialized, each step carries its run's ID, and the indexer is
// paused while a run uses the runtime (review finding 7).
func TestLabRunsAreSerializedByID(t *testing.T) {
	ctx := context.Background()
	rt := NewRuntime(context.Background())
	ix := NewIndexer(NewStore(), rt)
	l := NewLab(ctx, ix)
	events := l.Subscribe(ctx)

	first, err := l.Run(ctx, vulkanBF16Q8, nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := l.Run(ctx, vulkanBF16Q8, []string{"a.pdf", "b.png"})
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two runs got one ID")
	}

	pausedDuringRun := false
	firstDone, secondStarted := -1, -1
	finished := map[contract.RunID]contract.LabResult{}
	for i := 0; len(finished) < 2; i++ {
		s := nextStep(t, events, 10*time.Second)
		if s.RunID != first && s.RunID != second {
			t.Fatalf("step with unknown run %d", s.RunID)
		}
		if s.Combo != vulkanBF16Q8 {
			t.Fatalf("run %d reports combo %s", s.RunID, s.Combo.ID())
		}
		if s.RunID == first && s.State == contract.LabEmbedding && lockedPause(ix) == contract.PauseLab {
			pausedDuringRun = true
		}
		if s.RunID == second && secondStarted < 0 {
			secondStarted = i
		}
		if s.RunID == first && s.State == contract.LabDone {
			firstDone = i
		}
		if s.State == contract.LabDone {
			finished[s.RunID] = *s.Result
		}
	}
	if !pausedDuringRun {
		t.Fatal("the indexer was not paused while a run embedded")
	}
	if firstDone < 0 || secondStarted < firstDone {
		t.Fatalf("second run started at step %d before the first finished at %d", secondStarted, firstDone)
	}
	for id, r := range finished {
		if r.RunID != id {
			t.Fatalf("result of run %d carries run %d", id, r.RunID)
		}
	}
	deadline := time.Now().Add(time.Second)
	for lockedPause(ix) != contract.PauseNone && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if lockedPause(ix) != contract.PauseNone {
		t.Fatalf("the indexer is still paused after the runs: %s", lockedPause(ix))
	}
}

// Canceling a run emits a canceled step with its ID, and a run canceled while it waits never starts.
func TestLabCancelEmitsTerminalStep(t *testing.T) {
	ctx := context.Background()
	rt := NewRuntime(context.Background())
	ix := NewIndexer(NewStore(), rt)
	l := NewLab(ctx, ix)
	events := l.Subscribe(ctx)

	blocker, err := l.Run(ctx, vulkanBF16Q8, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitCtx, cancelWaiting := context.WithCancel(ctx)
	queued, err := l.Run(waitCtx, vulkanBF16Q8, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancelWaiting()
	for {
		s := nextStep(t, events, 10*time.Second)
		if s.RunID == queued {
			if s.State != contract.LabCanceled {
				t.Fatalf("queued run that was canceled emitted %s", s.State)
			}
			break
		}
		if s.RunID == blocker && s.State == contract.LabDone {
			t.Fatal("the blocking run finished before the canceled run got its terminal step")
		}
	}

	// Cancel a run that is embedding: its terminal step is canceled, and no done step follows.
	runCtx, cancelRun := context.WithCancel(ctx)
	running, err := l.Run(runCtx, vulkanBF16Q8, nil)
	if err != nil {
		t.Fatal(err)
	}
	for {
		s := nextStep(t, events, 10*time.Second)
		if s.RunID == running && s.State == contract.LabEmbedding {
			break
		}
	}
	cancelRun()
	for {
		s := nextStep(t, events, 10*time.Second)
		if s.RunID != running {
			continue
		}
		if s.State == contract.LabCanceled {
			break
		}
		if s.State == contract.LabDone {
			t.Fatal("a canceled run finished")
		}
	}
}

// lockedTemp returns the number of temporary sidecars the lab runs, under its lock.
func lockedTemp(l *Lab) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.temp
}

// A run canceled while it waits leaves the FIFO queue without letting a later run overlap the active one, and
// the indexer stays paused until the last run releases the lab (review finding 6, pending decision 1). Each run
// starts its own temporary sidecar, and none is left after the queue empties.
func TestLabCanceledQueuedRunKeepsOrder(t *testing.T) {
	ctx := context.Background()
	rt := NewRuntime(context.Background())
	ix := NewIndexer(NewStore(), rt)
	l := NewLab(ctx, ix)
	events := l.Subscribe(ctx)

	a, err := l.Run(ctx, vulkanBF16Q8, nil)
	if err != nil {
		t.Fatal(err)
	}
	bCtx, cancelB := context.WithCancel(ctx)
	b, err := l.Run(bCtx, vulkanBF16Q8, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := l.Run(ctx, vulkanBF16Q8, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancelB()

	aDone, cStarted := -1, -1
	bCanceled := false
	cDone := false
	for i := 0; !cDone; i++ {
		s := nextStep(t, events, 10*time.Second)
		if s.RunID == c && s.State == contract.LabStarting && aDone < 0 {
			t.Fatal("run C started before run A finished")
		}
		if s.RunID == b && s.State != contract.LabCanceled {
			t.Fatalf("canceled run B emitted %s", s.State)
		}
		if s.RunID == b {
			bCanceled = true
		}
		if s.RunID == a && s.State == contract.LabDone {
			aDone = i
		}
		if s.RunID == c && s.State == contract.LabStarting {
			cStarted = i
		}
		cDone = s.RunID == c && s.State == contract.LabDone
		if s.State == contract.LabDone || s.State == contract.LabCanceled {
			if lockedPause(ix) != contract.PauseLab && !(s.RunID == c && s.State == contract.LabDone) {
				t.Fatalf("the indexer is not paused after run %d's %s step", s.RunID, s.State)
			}
		}
	}
	if cStarted < 0 || aDone < 0 || aDone > cStarted {
		t.Fatalf("run order: A done at %d, C started at %d", aDone, cStarted)
	}
	if !bCanceled {
		t.Fatal("canceled run B emitted no terminal step")
	}
	deadline := time.Now().Add(time.Second)
	for lockedPause(ix) != contract.PauseNone && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if lockedPause(ix) != contract.PauseNone {
		t.Fatal("the indexer stayed paused after the last run")
	}
	if lockedTemp(l) != 0 {
		t.Fatal("a temporary sidecar outlived its run")
	}
}

// When the lab's lifetime ends, a running and a queued run each emit a canceled step, seen by subscribers
// with their own live contexts, and then the streams close (review finding 7). No run starts afterward.
func TestLabLifetimeEndCancelsEveryRun(t *testing.T) {
	bg := context.Background()
	rt := NewRuntime(context.Background())
	ix := NewIndexer(NewStore(), rt)
	life, endLife := context.WithCancel(bg)
	l := NewLab(life, ix)
	subA, cancelSubA := context.WithCancel(bg)
	subB, cancelSubB := context.WithCancel(bg)
	defer cancelSubA()
	defer cancelSubB()
	first := l.Subscribe(subA)
	second := l.Subscribe(subB)

	running, err := l.Run(bg, vulkanBF16Q8, nil)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := l.Run(bg, vulkanBF16Q8, nil)
	if err != nil {
		t.Fatal(err)
	}
	for {
		s := nextStep(t, first, 10*time.Second)
		if s.RunID == running && s.State == contract.LabEmbedding {
			break
		}
	}
	endLife()

	for name, ch := range map[string]<-chan contract.LabStep{"first": first, "second": second} {
		terminal := map[contract.RunID]bool{}
		for s := range ch {
			if s.State == contract.LabDone {
				t.Fatalf("%s subscriber: run %d finished after the lifetime ended", name, s.RunID)
			}
			if s.State == contract.LabCanceled {
				terminal[s.RunID] = true
			}
		}
		if !terminal[running] || !terminal[queued] {
			t.Fatalf("%s subscriber: terminal steps %v, want canceled for runs %d and %d", name, terminal, running, queued)
		}
	}
	if _, err := l.Run(bg, vulkanBF16Q8, nil); !errors.Is(err, contract.ErrStopped) {
		t.Fatalf("Run after the lifetime ended: %v, want ErrStopped", err)
	}
}

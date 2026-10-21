package fake

import (
	"context"
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
	rt := NewRuntime()
	ix := NewIndexer(NewStore())
	l := NewLab(ctx, rt, ix)
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
	rt := NewRuntime()
	ix := NewIndexer(NewStore())
	l := NewLab(ctx, rt, ix)
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

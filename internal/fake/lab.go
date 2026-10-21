package fake

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// labResults are the measured rows of the reference laptop (UI guide section 3.2): the four
// combinations of the test matrix that have a result.
var labResults = []contract.LabResult{
	{Combo: vulkanBF16Q8, Bench: contract.BenchResult{ImageMS: 2410, TextTokensPerSec: 312, PeakRAM: 900 << 20, VRAM: 1200 << 20, Quality: fp(0.996)}},
	{Combo: contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16},
		Bench: contract.BenchResult{ImageMS: 2683, TextTokensPerSec: 298, PeakRAM: 1100 << 20, VRAM: 1600 << 20, Quality: fp(1.0)}},
	{Combo: contract.Combo{Backend: contract.BackendCPU, TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0},
		Bench: contract.BenchResult{ImageMS: 4759, TextTokensPerSec: 160, PeakRAM: 2000 << 20, Quality: fp(0.996)}},
	{Combo: contract.Combo{Backend: contract.BackendCPU, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16},
		Bench: contract.BenchResult{ImageMS: 4850, TextTokensPerSec: 158, PeakRAM: 2100 << 20, Quality: fp(1.0)}},
}

// labInstalled returns the two installed combinations of the fixture, with their lab rows.
func labInstalled() []contract.Installed {
	installed := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	var out []contract.Installed
	for _, r := range labResults {
		if r.Combo == vulkanBF16Q8 || r.Combo == (contract.Combo{Backend: contract.BackendCPU, TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0}) {
			b := r.Bench
			out = append(out, contract.Installed{Combo: r.Combo, InstalledAt: installed, Bench: &b})
		}
	}
	return out
}

// fp returns a pointer to v.
func fp(v float64) *float64 { return &v }

// Lab is a contract.Lab that replays the fixture results. A run emits one step every 200 ms. Runs
// are serialized in the order they were started, and each run waits for the one before it.
type Lab struct {
	rt      contract.Runtime
	ix      contract.Indexer
	life    context.Context
	streams *stream[contract.LabStep]

	mu     sync.Mutex
	nextID contract.RunID
	last   chan struct{} // closed when the most recent run ends
}

// NewLab returns a Lab with the fixture results. A run opens its session on rt and pauses ix while it
// runs. The lab's streams close, and its runs stop, when ctx ends.
func NewLab(ctx context.Context, rt contract.Runtime, ix contract.Indexer) *Lab {
	l := &Lab{
		rt:   rt,
		ix:   ix,
		life: ctx,
		streams: newStream(
			func(s contract.LabStep) string { return fmt.Sprint(s.RunID) },
			func(s contract.LabStep) bool {
				return s.State == contract.LabDone || s.State == contract.LabFailed || s.State == contract.LabCanceled
			},
		),
		last: closedChan(),
	}
	go func() {
		<-ctx.Done()
		l.streams.close()
	}()
	return l
}

// closedChan returns a channel that is already closed.
func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// spaceOf returns the vector space of combination c.
func spaceOf(c contract.Combo) contract.VectorSpace {
	return contract.VectorSpace{TextQuant: c.TextQuant, EncQuant: c.EncQuant, ModelRev: modelRev}
}

// Run queues a test of combination c. It fails at once for a combination without a fixture result, for
// more than 10 extra files, and after the lab's lifetime ends.
func (l *Lab) Run(ctx context.Context, c contract.Combo, extra []string) (contract.RunID, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if l.life.Err() != nil {
		return 0, contract.ErrStopped
	}
	if len(extra) > 10 {
		return 0, errors.New("lab: at most 10 extra files")
	}
	var row *contract.LabResult
	for i := range labResults {
		if labResults[i].Combo == c {
			row = &labResults[i]
		}
	}
	if row == nil {
		return 0, fmt.Errorf("lab: no fixture result for %s: %w", c.ID(), contract.ErrUnknownCombo)
	}
	steps := []string{}
	for i := 1; i <= 8; i++ {
		steps = append(steps, fmt.Sprintf("Embedding image %d of 8", i))
	}
	steps = append(steps, "Embedding 16 text chunks")
	for i := range extra {
		steps = append(steps, fmt.Sprintf("Embedding your file %d of %d", i+1, len(extra)))
	}

	l.mu.Lock()
	id := l.nextID
	l.nextID++
	prev := l.last
	done := make(chan struct{})
	l.last = done
	l.mu.Unlock()

	go l.execute(ctx, id, c, *row, steps, prev, done)
	return id, nil
}

// execute runs one test once the run before it (prev) has ended. It emits the run's steps and ends with
// exactly one terminal step: done, failed or canceled.
func (l *Lab) execute(ctx context.Context, id contract.RunID, c contract.Combo, row contract.LabResult, steps []string, prev, done chan struct{}) {
	defer close(done)
	emit := func(s contract.LabStep) {
		s.RunID, s.Combo = id, c
		l.streams.publish(s)
	}
	cancel := func() { emit(contract.LabStep{State: contract.LabCanceled, Step: "Canceled"}) }

	select {
	case <-prev:
	case <-ctx.Done():
		cancel()
		return
	case <-l.life.Done():
		cancel()
		return
	}

	// The indexer waits while the run uses the runtime, and the session closes when the run ends.
	l.ix.Pause(contract.PauseLab)
	defer l.ix.Resume(contract.PauseLab)
	sess, err := l.rt.Session(ctx, spaceOf(c))
	if err != nil {
		if ctx.Err() != nil {
			cancel()
			return
		}
		emit(contract.LabStep{State: contract.LabFailed, Err: err.Error()})
		return
	}
	defer sess.Close()

	emit(contract.LabStep{State: contract.LabStarting, Step: "Starting " + c.ID()})
	if !sleepOrEnd(ctx, l.life, 200*time.Millisecond) {
		cancel()
		return
	}
	for _, s := range steps {
		emit(contract.LabStep{State: contract.LabEmbedding, Step: s})
		if !sleepOrEnd(ctx, l.life, 200*time.Millisecond) {
			cancel()
			return
		}
	}
	result := row
	result.RunID = id
	emit(contract.LabStep{State: contract.LabDone, Step: "Done", Result: &result})
}

// sleepOrEnd sleeps for d and reports false when either context ends first.
func sleepOrEnd(a, b context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-a.Done():
		return false
	case <-b.Done():
		return false
	}
}

// Subscribe returns a stream of the steps of all runs.
func (l *Lab) Subscribe(ctx context.Context) <-chan contract.LabStep {
	return l.streams.subscribe(ctx)
}

// Recommend returns the fastest result whose quality is at least 0.99. Results with n/a quality do not qualify.
func (l *Lab) Recommend(results []contract.LabResult) (contract.Combo, bool) {
	var best contract.LabResult
	found := false
	for _, r := range results {
		q := r.Bench.Quality
		if q == nil || *q < 0.99 {
			continue
		}
		if !found || r.Bench.ImageMS < best.Bench.ImageMS {
			best, found = r, true
		}
	}
	return best.Combo, found
}

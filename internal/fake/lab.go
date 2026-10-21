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

// Lab is a contract.Lab that replays the fixture results. It has no runtime: a run stands for its own
// temporary sidecar, which the fake only counts (Lab.temp). Runs form one FIFO queue, and a run emits a
// step every 200 ms.
type Lab struct {
	ix      contract.Indexer
	life    context.Context
	streams *stream[contract.LabStep]

	mu     sync.Mutex
	nextID contract.RunID
	queue  []*labRun // waiting runs, first to start first
	active *labRun   // the run that holds the lab, or nil
	closed bool      // the lifetime ended, so no run starts
	temp   int       // temporary sidecars running
	runs   sync.WaitGroup
}

// labRun is one queued or running test. A run is promoted once: it then holds the lab until it ends.
type labRun struct {
	id       contract.RunID
	combo    contract.Combo
	row      contract.LabResult
	steps    []string
	ctx      context.Context
	cancel   context.CancelFunc
	ready    chan struct{} // closed when the run is promoted
	promoted bool          // guarded by Lab.mu
}

// NewLab returns a Lab with the fixture results. A run pauses ix with PauseLab while the queue is busy. When
// ctx ends, every run is canceled, each emits its terminal step, and the lab's streams close.
func NewLab(ctx context.Context, ix contract.Indexer) *Lab {
	l := &Lab{
		ix:   ix,
		life: ctx,
		streams: newStream(
			func(s contract.LabStep) string { return fmt.Sprint(s.RunID) },
			func(s contract.LabStep) bool {
				return s.State == contract.LabDone || s.State == contract.LabFailed || s.State == contract.LabCanceled
			},
		),
	}
	go func() {
		<-ctx.Done()
		l.shutdown()
	}()
	return l
}

// shutdown stops the lab: no run starts, every run is canceled, and the streams close after the last terminal step.
func (l *Lab) shutdown() {
	l.mu.Lock()
	l.closed = true
	for _, r := range l.queue {
		r.cancel()
	}
	if l.active != nil {
		l.active.cancel()
	}
	l.mu.Unlock()
	l.runs.Wait()
	l.streams.close()
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

	rctx, cancel := context.WithCancel(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		cancel()
		return 0, contract.ErrStopped
	}
	r := &labRun{id: l.nextID, combo: c, row: *row, steps: steps, ctx: rctx, cancel: cancel, ready: make(chan struct{})}
	l.nextID++
	l.runs.Add(1)
	go l.runOne(r)
	if l.active == nil {
		l.ix.Pause(contract.PauseLab)
		l.giveLocked(r)
	} else {
		l.queue = append(l.queue, r)
	}
	return r.id, nil
}

// giveLocked promotes r: it holds the lab from now on. The caller holds l.mu.
func (l *Lab) giveLocked(r *labRun) {
	l.active = r
	r.promoted = true
	close(r.ready)
}

// releaseLocked passes the lab to the next queued run, or frees it and resumes the indexer when the queue is
// empty. The caller holds l.mu.
func (l *Lab) releaseLocked() {
	if len(l.queue) == 0 {
		l.active = nil
		l.ix.Resume(contract.PauseLab)
		return
	}
	next := l.queue[0]
	l.queue = l.queue[1:]
	l.giveLocked(next)
}

// dequeueLocked removes a queued run that never started. The caller holds l.mu.
func (l *Lab) dequeueLocked(r *labRun) {
	for i, q := range l.queue {
		if q == r {
			l.queue = append(l.queue[:i], l.queue[i+1:]...)
			return
		}
	}
}

// runOne waits for its turn. A run canceled while it waits leaves the queue and emits canceled; the others keep
// their order. A run that holds the lab executes, emits exactly one terminal step, and then passes the lab on.
func (l *Lab) runOne(r *labRun) {
	defer l.runs.Done()
	defer r.cancel()
	select {
	case <-r.ready:
	case <-r.ctx.Done():
	}
	l.mu.Lock()
	if !r.promoted {
		l.dequeueLocked(r)
		l.mu.Unlock()
		l.emit(r, contract.LabStep{State: contract.LabCanceled, Step: "Canceled"})
		return
	}
	l.mu.Unlock()
	l.execute(r)
	l.mu.Lock()
	l.releaseLocked()
	l.mu.Unlock()
}

// execute runs the steps of r in its own temporary sidecar, which stops before the terminal step is published.
func (l *Lab) execute(r *labRun) {
	l.setTemp(1)
	last := l.embed(r)
	l.setTemp(-1)
	l.emit(r, last)
}

// embed emits the live steps of r and returns its terminal step: done, or canceled when r.ctx ends first.
func (l *Lab) embed(r *labRun) contract.LabStep {
	canceled := contract.LabStep{State: contract.LabCanceled, Step: "Canceled"}
	if r.ctx.Err() != nil {
		return canceled
	}
	l.emit(r, contract.LabStep{State: contract.LabStarting, Step: "Starting " + r.combo.ID()})
	if !sleepOrEnd(r.ctx, l.life, 200*time.Millisecond) {
		return canceled
	}
	for _, s := range r.steps {
		l.emit(r, contract.LabStep{State: contract.LabEmbedding, Step: s})
		if !sleepOrEnd(r.ctx, l.life, 200*time.Millisecond) {
			return canceled
		}
	}
	result := r.row
	result.RunID = r.id
	return contract.LabStep{State: contract.LabDone, Step: "Done", Result: &result}
}

// setTemp adds d temporary sidecars.
func (l *Lab) setTemp(d int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.temp += d
}

// emit publishes a step of r.
func (l *Lab) emit(r *labRun, s contract.LabStep) {
	s.RunID, s.Combo = r.id, r.combo
	l.streams.publish(s)
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

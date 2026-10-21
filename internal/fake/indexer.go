package fake

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

const (
	meanTokensRate = 96.0 // tokens per second, the long-run mean; the ETA divides by it
	estimatingFor  = 30 * time.Second
)

// job is a queue of files to index into one generation.
type job struct {
	gen      int64 // the generation to write; 0 means the active generation at commit time
	from     int64 // for a rebuild, the generation it replaces
	queue    []contract.FileRec
	next     int     // index of the next file to complete
	consumed float64 // tokens processed by this job
}

// Indexer is a contract.Indexer over a Store. It simulates the job: tokens per second vary around
// 96, and each completed file commits its items and file_index row to the store, so Search and the
// progress counts see the same state. The active job and an optional rebuild share the rate: each
// gets half while both have work. The job pauses while any reason is set.
type Indexer struct {
	mu       sync.Mutex
	store    *Store
	interval time.Duration
	streams  *stream[contract.Progress] // the active job
	rstreams *stream[contract.Progress] // the rebuild
	started  bool
	reasons  map[contract.PauseReason]bool
	active   *job
	rebuild  *job

	elapsed time.Duration
	tick    int
	rate    float64 // tokens per second this tick, which varies
}

// NewIndexer returns an Indexer over store that publishes every 250 ms, the SPEC section 11 maximum rate.
func NewIndexer(store *Store) *Indexer { return newIndexer(store, 250*time.Millisecond) }

// newIndexer returns an Indexer that ticks every interval.
func newIndexer(store *Store, interval time.Duration) *Indexer {
	return &Indexer{
		store:    store,
		interval: interval,
		streams: newStream(
			func(p contract.Progress) string { return fmt.Sprint(p.Gen) },
			func(contract.Progress) bool { return false },
		),
		rstreams: newStream(
			func(p contract.Progress) string { return fmt.Sprint(p.Gen) },
			func(p contract.Progress) bool { return p.FilesDone == p.FilesTotal },
		),
		reasons:  map[contract.PauseReason]bool{},
	}
}

// Start begins the job in the background. It may be called once; the job stops, and both progress
// channels close, when ctx ends.
func (x *Indexer) Start(ctx context.Context) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.started {
		return errors.New("fake indexer: Start may be called once")
	}
	x.started = true
	x.active = &job{queue: x.store.needingWork(x.store.activeGenID())}
	go x.run(ctx)
	return nil
}

// run publishes one snapshot per stream per tick until ctx ends.
func (x *Indexer) run(ctx context.Context) {
	defer x.streams.close()
	defer x.rstreams.close()
	ticker := time.NewTicker(x.interval)
	defer ticker.Stop()
	for {
		p, rp := x.step()
		x.streams.publish(p)
		if rp != nil {
			x.rstreams.publish(*rp)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// step advances the simulated job by one tick and returns the active snapshot and, during a rebuild,
// the rebuild snapshot. The rebuild snapshot that finishes a rebuild is returned once, then it stops.
func (x *Indexer) step() (contract.Progress, *contract.Progress) {
	x.mu.Lock()
	defer x.mu.Unlock()
	dt := x.interval.Seconds()
	paused := x.pauseReason() != contract.PauseNone
	x.rate = meanTokensRate + 14*math.Sin(float64(x.tick)/9) + 6*math.Sin(float64(x.tick)/2.3)
	if paused {
		x.rate = 0
	} else {
		x.elapsed += x.interval
		activeWork := x.active.next < len(x.active.queue)
		rebuildWork := x.rebuild != nil && x.rebuild.next < len(x.rebuild.queue)
		switch {
		case activeWork && rebuildWork:
			x.active.consumed += x.rate * dt / 2
			x.rebuild.consumed += x.rate * dt / 2
		case activeWork:
			x.active.consumed += x.rate * dt
		case rebuildWork:
			x.rebuild.consumed += x.rate * dt
		}
	}
	x.tick++

	x.commitDue(x.active)
	active := x.snapshot(x.active, x.store.activeGenID(), true)
	if x.rebuild == nil {
		return active, nil
	}
	x.commitDue(x.rebuild)
	if x.rebuild.next < len(x.rebuild.queue) {
		rp := x.snapshot(x.rebuild, x.rebuild.gen, false)
		return active, &rp
	}
	// The rebuild is complete: its generation takes over, and the old one's items go.
	rp := x.snapshot(x.rebuild, x.rebuild.gen, false)
	_ = x.store.ActivateGeneration(context.Background(), x.rebuild.gen)
	_ = x.store.DeleteItems(context.Background(), x.rebuild.from)
	x.rebuild = nil
	return x.snapshot(x.active, x.store.activeGenID(), true), &rp
}

// commitDue completes every file whose tokens are processed and commits its items and row.
// Commit cannot fail here: the generation exists and the items match the file.
func (x *Indexer) commitDue(j *job) {
	for j.next < len(j.queue) && j.consumed >= float64(j.next+1)*tokensPerFile {
		f := j.queue[j.next]
		gen := j.gen
		if gen == 0 {
			gen = x.store.activeGenID()
		}
		ix, items := simulate(f, gen)
		_ = x.store.CommitFile(context.Background(), ix, items)
		j.next++
	}
}

// snapshot builds the Progress for a job. Active snapshots count every file in the store; a
// rebuild snapshot counts the files it was asked to rebuild.
func (x *Indexer) snapshot(j *job, gen int64, active bool) contract.Progress {
	p := contract.Progress{
		Gen:          gen,
		FilesPerSec:  x.rate / tokensPerFile,
		TokensPerSec: x.rate,
		Paused:       x.pauseReason(),
	}
	remaining := float64(len(j.queue))*tokensPerFile - j.consumed
	if active {
		p.FilesDone, p.Errors, p.FilesTotal, p.PerFolder = x.store.progressCounts(gen)
	} else {
		p.FilesDone, p.FilesTotal = j.next, len(j.queue)
	}
	if j.next < len(j.queue) {
		p.Current = j.queue[j.next].Path
	}
	p.ETA = -1
	if j.next >= len(j.queue) {
		p.ETA = 0
	} else if x.elapsed >= estimatingFor {
		p.ETA = time.Duration(remaining / meanTokensRate * float64(time.Second))
	}
	return p
}

// pauseReason returns the first active reason in the order user, battery, suspend, lab. The caller holds x.mu.
func (x *Indexer) pauseReason() contract.PauseReason {
	for _, r := range []contract.PauseReason{contract.PauseUser, contract.PauseBattery, contract.PauseSuspend, contract.PauseLab} {
		if x.reasons[r] {
			return r
		}
	}
	return contract.PauseNone
}

// enqueueLocked appends files to the active queue unless they are already waiting in it.
func (x *Indexer) enqueueLocked(files ...contract.FileRec) {
	for _, f := range files {
		waiting := false
		for _, q := range x.active.queue[x.active.next:] {
			if q.ID == f.ID {
				waiting = true
				break
			}
		}
		if !waiting {
			x.active.queue = append(x.active.queue, f)
		}
	}
}

// checkStarted returns the error for a command that needs a started indexer, or the context's error.
func (x *Indexer) checkStarted(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !x.started {
		return contract.ErrNotStarted
	}
	return nil
}

// Retry clears the rows of the given files in the active generation and queues them.
func (x *Indexer) Retry(ctx context.Context, fileIDs []int64) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if err := x.checkStarted(ctx); err != nil {
		return err
	}
	files := make([]contract.FileRec, 0, len(fileIDs))
	for _, id := range fileIDs {
		f, ok := x.store.fileByID(id)
		if !ok {
			return contract.ErrUnknownFile
		}
		files = append(files, f)
	}
	gen := x.store.activeGenID()
	for _, f := range files {
		if err := x.store.ClearFileIndex(ctx, f.ID, gen); err != nil {
			return err
		}
	}
	x.enqueueLocked(files...)
	return nil
}

// RetryAll retries every file with an error row in the active generation.
func (x *Indexer) RetryAll(ctx context.Context) error {
	x.mu.Lock()
	ids := x.store.errRows(x.store.activeGenID())
	x.mu.Unlock()
	return x.Retry(ctx, ids)
}

// Rescan queues the files of a folder that need work in the active generation.
func (x *Indexer) Rescan(ctx context.Context, folderID int64) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if err := x.checkStarted(ctx); err != nil {
		return err
	}
	if !x.store.folderKnown(folderID) {
		return contract.ErrUnknownFolder
	}
	gen := x.store.activeGenID()
	var due []contract.FileRec
	for _, f := range x.store.needingWork(gen) {
		if f.FolderID == folderID {
			due = append(due, f)
		}
	}
	x.enqueueLocked(due...)
	return nil
}

// Rebuild creates a building generation for the precisions and queues every file indexed in the
// active generation for it. The rebuild gets half the rate while the active job has work.
func (x *Indexer) Rebuild(ctx context.Context, text, enc contract.Quant) (int64, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if err := x.checkStarted(ctx); err != nil {
		return 0, err
	}
	if x.rebuild != nil {
		return 0, contract.ErrRebuildBusy
	}
	from := x.store.activeGenID()
	g := contract.Generation{
		Space: contract.VectorSpace{TextQuant: text, EncQuant: enc, ModelRev: modelRev},
		CreatedAt: time.Date(2026, 10, 21, 9, 30, 0, 0, time.UTC),
	}
	if err := x.store.NewGeneration(ctx, &g); err != nil {
		return 0, err
	}
	x.rebuild = &job{gen: g.ID, from: from, queue: x.store.doneFiles(from)}
	return g.ID, nil
}

// Pause stops the job for reason.
func (x *Indexer) Pause(reason contract.PauseReason) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if reason != contract.PauseNone {
		x.reasons[reason] = true
	}
}

// Resume lifts the pause for reason.
func (x *Indexer) Resume(reason contract.PauseReason) {
	x.mu.Lock()
	defer x.mu.Unlock()
	delete(x.reasons, reason)
}

// Subscribe streams the snapshots of the active generation's job.
func (x *Indexer) Subscribe(ctx context.Context) <-chan contract.Progress { return x.streams.subscribe(ctx) }

// SubscribeRebuild streams the snapshots of the building generation. It is quiet when no rebuild runs.
func (x *Indexer) SubscribeRebuild(ctx context.Context) <-chan contract.Progress {
	return x.rstreams.subscribe(ctx)
}

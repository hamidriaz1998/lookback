package fake

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

const (
	meanTokensRate = 96.0 // tokens per second, the long-run mean; the ETA divides by it
	estimatingFor  = 30 * time.Second
)

// Indexer is a contract.Indexer that simulates the job over pendingFiles. Progress moves on a
// timer: tokens per second vary around 96, the ETA counts down steadily, and Current is the next
// file in the queue. The job pauses while any pause reason is set and stops when the queue is empty.
type Indexer struct {
	mu       sync.Mutex
	interval time.Duration
	ch       chan contract.Progress
	started  bool
	reasons  map[contract.PauseReason]bool
	pending  []contract.FileRec

	elapsed  time.Duration
	tick     int
	rate     float64 // tokens per second this tick, which varies
	consumed float64 // tokens processed so far
}

// NewIndexer returns an Indexer that publishes progress every 250 ms, the SPEC section 11 maximum rate.
func NewIndexer() *Indexer { return newIndexer(250 * time.Millisecond) }

// newIndexer returns an Indexer that ticks every interval.
func newIndexer(interval time.Duration) *Indexer {
	return &Indexer{
		interval: interval,
		ch:       make(chan contract.Progress, 1),
		reasons:  map[contract.PauseReason]bool{},
		pending:  pendingFiles(),
	}
}

// Start begins the job in a goroutine. It stops, and closes Progress, when ctx ends or the queue empties.
func (x *Indexer) Start(ctx context.Context) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.started {
		return errors.New("fake indexer: already started")
	}
	x.started = true
	go x.run(ctx)
	return nil
}

// run publishes one snapshot per tick until ctx ends or the job finishes.
func (x *Indexer) run(ctx context.Context) {
	defer close(x.ch)
	ticker := time.NewTicker(x.interval)
	defer ticker.Stop()
	for {
		p, finished := x.step()
		// Keep only the newest snapshot: drop the stale one if the reader has not taken it.
		select {
		case <-x.ch:
		default:
		}
		x.ch <- p
		if finished {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// step advances the simulated job by one tick and returns the snapshot.
func (x *Indexer) step() (contract.Progress, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	dt := x.interval.Seconds()
	paused := x.pauseReason() != contract.PauseNone
	x.rate = meanTokensRate + 14*math.Sin(float64(x.tick)/9) + 6*math.Sin(float64(x.tick)/2.3)
	if paused {
		x.rate = 0
	} else {
		x.consumed += x.rate * dt
		x.elapsed += x.interval
	}
	x.tick++

	total := float64(len(x.pending)) * tokensPerFile
	finished := x.consumed >= total
	if finished {
		x.consumed = total
		x.rate = 0
	}
	return x.snapshot(finished), finished
}

// snapshot builds the Progress for the current state. The caller holds x.mu.
func (x *Indexer) snapshot(finished bool) contract.Progress {
	processed := int(x.consumed / tokensPerFile)
	if processed > len(x.pending) {
		processed = len(x.pending)
	}
	// The store holds the indexed files already; these counts add the queue to them.
	picturesDone, picturesTotal := imageCount, imageCount+pendingPictures
	documentsDone, documentsTotal := indexedDocuments, indexedDocuments+pendingDocuments
	for _, f := range x.pending[:processed] {
		if f.FolderID == 1 {
			picturesDone++
		} else {
			documentsDone++
		}
	}

	eta := time.Duration(-1)
	// The ETA divides by the long-run mean, not the tick's rate, so it counts down steadily.
	if x.elapsed >= estimatingFor {
		remaining := float64(len(x.pending))*tokensPerFile - x.consumed
		eta = time.Duration(remaining / meanTokensRate * float64(time.Second))
	}
	current := ""
	if finished {
		eta = 0
	} else {
		current = x.pending[processed].Path
	}
	return contract.Progress{
		FilesDone:    picturesDone + documentsDone,
		FilesTotal:   picturesTotal + documentsTotal,
		FilesPerSec:  x.rate / tokensPerFile,
		TokensPerSec: x.rate,
		ETA:          eta,
		Current:      current,
		PerFolder: []contract.FolderProgress{
			{FolderID: 1, Path: picturesFolder, FilesDone: picturesDone, FilesTotal: picturesTotal},
			{FolderID: 2, Path: documentFolder, FilesDone: documentsDone, FilesTotal: documentsTotal},
		},
		Errors: indexedDocuments,
		Paused: x.pauseReason(),
	}
}

// pauseReason returns the first active reason in the order user, battery, suspend. The caller holds x.mu.
func (x *Indexer) pauseReason() contract.PauseReason {
	for _, r := range []contract.PauseReason{contract.PauseUser, contract.PauseBattery, contract.PauseSuspend} {
		if x.reasons[r] {
			return r
		}
	}
	return contract.PauseNone
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

// Progress returns the channel of snapshots. It holds at most the newest one.
func (x *Indexer) Progress() <-chan contract.Progress { return x.ch }

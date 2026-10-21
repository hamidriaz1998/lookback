package fake

import (
	"context"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// Fixture job: 3,100 files, of which 1,240 are done. The remaining work is 1,860 files of
// about 1,000 tokens each, so the ETA starts near 5 hours and counts down while the job runs.
const (
	totalFiles     = 3100
	startFilesDone = 1240
	picturesStart  = 1180
	picturesTotal  = 2116
	documentsDone  = 60
	documentsTotal = 984
	tokensPerFile  = 1000.0
	estimatingFor  = 30 * time.Second
	meanTokensRate = 96.0
)

// Indexer is a contract.Indexer that runs a simulated job. Progress moves on a timer: the
// tokens per second vary around 96, the ETA counts down steadily, and the current file changes.
// The job pauses while any pause reason is set, and never finishes within a UI session.
type Indexer struct {
	mu       sync.Mutex
	interval time.Duration
	ch       chan contract.Progress
	started  bool
	reasons  map[contract.PauseReason]bool

	elapsed   time.Duration
	tick      int
	rate      float64 // tokens per second this tick, which varies
	remaining float64 // tokens left
	done      float64 // files done, fractional
}

// NewIndexer returns an Indexer that publishes progress every 250 ms, the SPEC §11 maximum rate.
func NewIndexer() *Indexer { return newIndexer(250 * time.Millisecond) }

// newIndexer returns an Indexer that ticks every interval.
func newIndexer(interval time.Duration) *Indexer {
	return &Indexer{
		interval:  interval,
		ch:        make(chan contract.Progress, 1),
		reasons:   map[contract.PauseReason]bool{},
		remaining: float64(totalFiles-startFilesDone) * tokensPerFile,
		done:      startFilesDone,
	}
}

// Start begins the job in a goroutine. It stops, and closes Progress, when ctx ends.
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
		x.remaining -= x.rate * dt
		x.done += x.rate * dt / tokensPerFile
		x.elapsed += x.interval
	}
	x.tick++

	finished := x.remaining <= 0
	if finished {
		x.remaining = 0
		x.done = totalFiles
	}
	return x.snapshot(finished), finished
}

// snapshot builds the Progress for the current state. The caller holds x.mu.
func (x *Indexer) snapshot(finished bool) contract.Progress {
	done := int(x.done)
	extra := done - startFilesDone
	picturesDone := picturesStart + extra
	if picturesDone > picturesTotal {
		picturesDone = picturesTotal
	}
	eta := time.Duration(-1)
	// The ETA divides by the long-run mean, not the tick's rate, so it counts down steadily.
	if x.elapsed >= estimatingFor {
		eta = time.Duration(x.remaining / meanTokensRate * float64(time.Second))
	}
	if finished {
		eta = 0
	}
	current := ""
	if !finished {
		current = picturesFolder + "/" + screenshotName(done%imageCount)
	}
	return contract.Progress{
		FilesDone:    done,
		FilesTotal:   totalFiles,
		FilesPerSec:  x.rate / tokensPerFile,
		TokensPerSec: x.rate,
		ETA:          eta,
		Current:      current,
		PerFolder: []contract.FolderProgress{
			{FolderID: 1, Path: picturesFolder, FilesDone: picturesDone, FilesTotal: picturesTotal},
			{FolderID: 2, Path: documentFolder, FilesDone: documentsDone, FilesTotal: documentsTotal},
		},
		Errors: 1,
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

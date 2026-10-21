package ui

import (
	"context"
	"fmt"
	"sync"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// instantDownloader reports every asset it is asked for as verified at once, so onboarding tests need no bytes.
// With hold set, Ensure queues the assets and release verifies them, which tests the state between the two.
type instantDownloader struct {
	contract.Downloader // the fixture, for the commands the flow does not use
	built               func([]contract.Asset)

	mu   sync.Mutex
	hold bool
	held []contract.Asset
	out  chan contract.DownloadProgress
}

func newInstantDownloader(d contract.Downloader) *instantDownloader {
	return &instantDownloader{Downloader: d, out: make(chan contract.DownloadProgress, 1024)}
}

// Ensure verifies the assets now, or, while held, when release is called. built runs first, as a real
// download leaves its runtime build on disk.
func (d *instantDownloader) Ensure(_ context.Context, assets []contract.Asset) error {
	if d.built != nil {
		d.built(assets)
	}
	d.mu.Lock()
	if d.hold {
		d.held = append(d.held, assets...)
		d.mu.Unlock()
		return nil
	}
	d.mu.Unlock()
	d.verify(assets)
	return nil
}

// setHold makes the following Ensure calls queue their assets, until release.
func (d *instantDownloader) setHold(on bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.hold = on
}

// release verifies the held assets.
func (d *instantDownloader) release() {
	d.mu.Lock()
	held := d.held
	d.held, d.hold = nil, false
	d.mu.Unlock()
	d.verify(held)
}

func (d *instantDownloader) verify(assets []contract.Asset) {
	for _, a := range assets {
		d.out <- contract.DownloadProgress{Key: a.Key(), Name: a.Name, State: contract.FileDone, Done: a.Bytes, Total: a.Bytes}
	}
}

// Subscribe streams the verifications. The App subscribes once.
func (d *instantDownloader) Subscribe(context.Context) <-chan contract.DownloadProgress { return d.out }

// gatedDetector refuses to probe a backend until its runtime build is on disk, as llama-server --list-devices
// does. Probes made earlier fail, so a candidate that is never probed never appears.
type gatedDetector struct {
	contract.Detector
	mu     sync.Mutex
	built  map[contract.Backend]bool
	probed []contract.Backend
}

func newGatedDetector() *gatedDetector {
	return &gatedDetector{Detector: fake.NewDetector(), built: map[contract.Backend]bool{}}
}

// markBuilt records the runtime builds in assets as on disk.
func (d *gatedDetector) markBuilt(assets []contract.Asset) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, a := range assets {
		if a.Part == contract.PartRuntime {
			d.built[a.Backend] = true
		}
	}
}

// Probe fails for a backend whose build is not on disk.
func (d *gatedDetector) Probe(ctx context.Context, b contract.Backend) (contract.BackendProbe, error) {
	d.mu.Lock()
	ok := d.built[b]
	if ok {
		d.probed = append(d.probed, b)
	}
	d.mu.Unlock()
	if !ok {
		return contract.BackendProbe{}, fmt.Errorf("%s: runtime build is not on disk", b)
	}
	return d.Detector.Probe(ctx, b)
}

// countingLab counts the runs it is asked to start, and keeps their contexts.
type countingLab struct {
	contract.Lab
	mu   sync.Mutex
	runs []context.Context
}

func (l *countingLab) Run(ctx context.Context, c contract.Combo, extra []string) (contract.RunID, error) {
	l.mu.Lock()
	l.runs = append(l.runs, ctx)
	l.mu.Unlock()
	return l.Lab.Run(ctx, c, extra)
}

func (l *countingLab) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.runs)
}

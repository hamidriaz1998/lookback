package index

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/extract"
	"github.com/hamidriaz1998/lookback/internal/fake"
	"github.com/hamidriaz1998/lookback/internal/store"
)

// gateStore lets a test hold the handover after ActivateGeneration has committed, once armed.
type gateStore struct {
	contract.Store
	armed     atomic.Bool
	activated chan struct{}
	release   chan struct{}
	once      sync.Once
}

func (g *gateStore) ActivateGeneration(ctx context.Context, gen int64) error {
	err := g.Store.ActivateGeneration(ctx, gen)
	if err == nil && g.armed.Load() {
		g.once.Do(func() { close(g.activated) })
		<-g.release
	}
	return err
}

// gateRuntime holds Session calls, once armed, until the test releases them.
type gateRuntime struct {
	contract.Runtime
	armed   atomic.Bool
	entered chan struct{}
	block   chan struct{}
	once    sync.Once
}

func (g *gateRuntime) Session(ctx context.Context, sp contract.VectorSpace) (contract.Session, error) {
	if g.armed.Load() {
		g.once.Do(func() { close(g.entered) })
		<-g.block
	}
	return g.Runtime.Session(ctx, sp)
}

// newGateRig returns a rig whose store and runtime can be gated. The runtime outlives life, as Runtime allows.
func newGateRig(t *testing.T, life context.Context, gs *gateStore, gr *gateRuntime) *rig {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	reg := extract.NewRegistry()
	t.Cleanup(func() { reg.Close() })
	gs.Store = st
	gr.Runtime = fake.NewRuntime(context.Background())
	return &rig{st: st, ix: New(gs, reg, gr, testSpace), life: life}
}

// TestHandoverKeepsWorkQueuedDuringActivation checks that a file changed while the handover runs is still
// indexed in the new generation, without a further filesystem event.
func TestHandoverKeepsWorkQueuedDuringActivation(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a rebuild and waits for handover")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "alpha lanterns")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gs := &gateStore{activated: make(chan struct{}), release: make(chan struct{})}
	r := newGateRig(t, ctx, gs, &gateRuntime{})
	fo := r.addFolder(t, dir)
	if err := r.ix.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "the first generation", func() bool { return r.activeGen(t) != 0 })

	gs.armed.Store(true)
	newGen, err := r.ix.Rebuild(ctx, contract.QuantQ8_0, contract.QuantQ8_0)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-gs.activated:
	case <-time.After(30 * time.Second):
		t.Fatal("the handover never ran")
	}
	writeFile(t, filepath.Join(dir, "b.txt"), "beta kumquats")
	if err := r.ix.Rescan(ctx, fo.ID); err != nil {
		t.Fatal(err)
	}
	close(gs.release)
	// The watcher would index b.txt one second after its write, so 800 ms shows the handover kept the work.
	waitFor(t, 800*time.Millisecond, "b.txt indexed right after the handover", func() bool {
		_, ok := r.hitPaths(t, newGen)["b.txt"]
		return ok
	})
}

// TestRebuildEndedByShutdownIsNotInstalled checks that a Rebuild whose session returns after the lifetime ends
// creates no generation and installs no job.
func TestRebuildEndedByShutdownIsNotInstalled(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a first index")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "alpha lanterns")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	gr := &gateRuntime{entered: make(chan struct{}), block: make(chan struct{})}
	r := newGateRig(t, ctx, &gateStore{activated: make(chan struct{}), release: make(chan struct{})}, gr)
	r.addFolder(t, dir)
	if err := r.ix.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "the first generation", func() bool { return r.activeGen(t) != 0 })

	gr.armed.Store(true)
	errc := make(chan error, 1)
	go func() {
		_, err := r.ix.Rebuild(context.Background(), contract.QuantQ8_0, contract.QuantQ8_0)
		errc <- err
	}()
	select {
	case <-gr.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("Rebuild never opened its session")
	}
	cancel()
	close(gr.block)
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("Rebuild succeeded after the lifetime ended")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Rebuild did not return")
	}
	gens, err := r.st.ListGenerations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range gens {
		if g.State == contract.GenBuilding {
			t.Fatalf("generation %d is building after shutdown", g.ID)
		}
	}
}

// TestRescanEndedByShutdownWritesNothing checks that a Rescan that waited for the scan lock after the lifetime
// ended saves no file.
func TestRescanEndedByShutdownWritesNothing(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a first index")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "alpha lanterns")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newGateRig(t, ctx, &gateStore{activated: make(chan struct{}), release: make(chan struct{})}, &gateRuntime{})
	fo := r.addFolder(t, dir)
	if err := r.ix.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "the first generation", func() bool { return r.activeGen(t) != 0 })

	r.ix.scanMu.Lock()
	errc := make(chan error, 1)
	go func() { errc <- r.ix.Rescan(context.Background(), fo.ID) }()
	time.Sleep(200 * time.Millisecond) // the Rescan has passed its entry checks and waits for scanMu
	cancel()
	writeFile(t, filepath.Join(dir, "c.txt"), "gamma after shutdown")
	r.ix.scanMu.Unlock()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("Rescan returned success after the lifetime ended")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Rescan did not return")
	}
	files, err := r.st.ListFiles(context.Background(), fo.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if filepath.Base(f.Path) == "c.txt" {
			t.Fatal("Rescan saved a file after the lifetime ended")
		}
	}
}

// TestQueuedFileReplacedByExternalSymlinkIsNotIndexed checks that a file queued while paused is re-checked at
// execution: replaced by a link to a file outside the folder, it is not embedded.
func TestQueuedFileReplacedByExternalSymlinkIsNotIndexed(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a first index")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.txt"), "alpha lanterns")
	outside := filepath.Join(t.TempDir(), "secret.txt")
	writeFile(t, outside, "leaked secret words")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newGateRig(t, ctx, &gateStore{activated: make(chan struct{}), release: make(chan struct{})}, &gateRuntime{})
	fo := r.addFolder(t, dir)
	if err := r.ix.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "the first generation", func() bool { return r.activeGen(t) != 0 })
	gen := r.activeGen(t)

	b := filepath.Join(dir, "b.txt")
	r.ix.Pause(contract.PauseUser)
	writeFile(t, b, "regular words")
	if err := r.ix.Rescan(ctx, fo.ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, b); err != nil {
		t.Fatal(err)
	}
	r.ix.Resume(contract.PauseUser)
	time.Sleep(300 * time.Millisecond) // before the watcher's one-second debounce rescans the folder
	if h, ok := r.hitPaths(t, gen)["b.txt"]; ok {
		t.Fatalf("a queued file that became an external symlink was indexed: %q", h.Snippet)
	}
}

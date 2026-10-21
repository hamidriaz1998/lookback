package ui

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// liveIndex is a store whose search results and index state a test changes while the app runs. Hits are per
// generation; a generation with a gate waits until the gate is closed, as a slow query does.
type liveIndex struct {
	contract.Store
	mu       sync.Mutex
	activeID int64 // the generation ListGenerations reports as active; 0 keeps the store's
	hits     map[int64][]contract.Hit
	gate     map[int64]chan struct{}
	extra    int // done index rows added to the store's, so the index state changes
	reads    int // ListFileIndex calls: one per data snapshot
}

func (l *liveIndex) ListGenerations(ctx context.Context) ([]contract.Generation, error) {
	gens, err := l.Store.ListGenerations(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.activeID != 0 {
		for i := range gens {
			switch {
			case gens[i].ID == l.activeID:
				gens[i].State = contract.GenActive
			case gens[i].State == contract.GenActive:
				gens[i].State = contract.GenRetired
			}
		}
	}
	return gens, err
}

func (l *liveIndex) Search(ctx context.Context, gen int64, _ []float32, _ []contract.Kind, _ int) ([]contract.Hit, error) {
	l.mu.Lock()
	gate := l.gate[gen]
	l.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]contract.Hit(nil), l.hits[gen]...), nil
}

func (l *liveIndex) ListFileIndex(ctx context.Context, gen int64) ([]contract.FileIndex, error) {
	l.mu.Lock()
	l.reads++
	extra := l.extra
	l.mu.Unlock()
	rows, err := l.Store.ListFileIndex(ctx, gen)
	for i := 0; i < extra; i++ {
		rows = append(rows, contract.FileIndex{FileID: int64(100000 + i), Gen: gen, Status: contract.StatusDone})
	}
	return rows, err
}

func (l *liveIndex) set(f func(*liveIndex)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f(l)
}

// hitAt returns a hit whose path names the generation, so a test can tell which generation's results show.
func hitAt(gen int64, i int) contract.Hit {
	return contract.Hit{FileID: gen*100 + int64(i), ItemID: gen*100 + int64(i), Kind: contract.KindImage, Modality: "image",
		Path: "/pics/gen" + itoa(int(gen)) + "-" + itoa(i) + ".png", Score: 0.5}
}

// TestQueryWaitsForFirstMatch checks that a query with no match is refreshed when the index gains a match: the
// empty result must not stay on screen after indexing has added the file.
func TestQueryWaitsForFirstMatch(t *testing.T) {
	live := &liveIndex{}
	h := newHarness(t, func(s *fake.Store) contract.Store { live.Store = s; return live })
	h.wait("snapshot", func() bool { return h.app.data.ready })
	h.app.search.query = "receipt"
	h.app.search.submit = true
	h.wait("no match", func() bool { return h.app.search.ranKey != "" && !h.app.search.searching })
	h.wantText("Nothing matches")

	gen := h.app.data.active.ID
	live.set(func(l *liveIndex) { l.hits = map[int64][]contract.Hit{gen: {hitAt(gen, 1)}}; l.extra = 1 })
	h.app.reload()
	h.wait("the new match", func() bool { return len(h.app.search.hits) == 1 && !h.app.search.searching })
	if h.app.search.hits[0].Path != hitAt(gen, 1).Path {
		t.Errorf("the refreshed result is %q", h.app.search.hits[0].Path)
	}
}

// TestActivationDropsInFlightResults checks that a query started on the old generation never shows its results
// once the new generation is active: the old query finishes late, and only the new generation's results show.
func TestActivationDropsInFlightResults(t *testing.T) {
	live := &liveIndex{}
	var oldGen, newGen int64
	h := newHarness(t, func(s *fake.Store) contract.Store {
		live.Store = s
		return live
	})
	h.wait("snapshot", func() bool { return h.app.data.ready })
	oldGen = h.app.data.active.ID
	ctx := t.Context()
	space := h.app.data.active.Space
	g := contract.Generation{Space: space, State: contract.GenBuilding}
	if err := h.store.NewGeneration(ctx, &g); err != nil {
		t.Fatal(err)
	}
	newGen = g.ID
	release := make(chan struct{})
	live.set(func(l *liveIndex) {
		l.hits = map[int64][]contract.Hit{oldGen: {hitAt(oldGen, 1)}, newGen: {hitAt(newGen, 2)}}
		l.gate = map[int64]chan struct{}{oldGen: release}
	})
	h.app.search.query = "receipt"
	h.app.search.submit = true
	h.wait("old query running", func() bool { return h.app.search.searching })

	live.set(func(l *liveIndex) { l.activeID = newGen; l.gate = nil })
	h.app.reload()
	h.wait("new generation shown", func() bool {
		return h.app.data.active != nil && h.app.data.active.ID == newGen && len(h.app.search.hits) == 1 && !h.app.search.searching
	})
	close(release)
	time.Sleep(20 * time.Millisecond)
	h.pump(0)
	for _, hit := range h.app.search.hits {
		if hit.FileID/100 == oldGen {
			t.Fatalf("the old generation's result %q is shown after activation", hit.Path)
		}
	}
	if len(h.app.search.hits) != 1 || h.app.search.hits[0].FileID != newGen*100+2 {
		t.Errorf("results after activation: %+v", h.app.search.hits)
	}
}

// TestProgressBurstDoesNotReloadEachTime checks that a burst of progress updates does not reload the whole library
// for each one. A burst costs at most one reload, and the reload that is due runs once the refresh interval passes.
func TestProgressBurstDoesNotReloadEachTime(t *testing.T) {
	live := &liveIndex{}
	progress := make(chan contract.Progress, 100)
	themes := func(context.Context) []contract.ThemeSource {
		return []contract.ThemeSource{fixedTheme("light", false), fixedTheme("dark", true)}
	}
	h := newHarnessTweak(t, func(s *fake.Store) contract.Store { live.Store = s; return live }, themes, false, func(d *Deps) {
		d.Indexer = progressIndexer{Indexer: d.Indexer, progress: progress}
	})
	h.wait("snapshot", func() bool { return h.app.data.ready })
	gen := h.app.data.active.ID
	before := readsOf(live)
	for i := 1; i <= 50; i++ {
		progress <- contract.Progress{Gen: gen, FilesDone: i, FilesTotal: 100}
	}
	h.wait("burst applied", func() bool { return h.app.progress.FilesDone == 50 })
	h.pump(0)
	mid := readsOf(live)
	if n := mid - before; n > 1 {
		t.Errorf("%d data snapshots for a burst of 50 progress updates, want at most one", n)
	}
	h.tt.Advance(reloadInterval)
	h.wait("the due reload", func() bool { return readsOf(live) > mid })
}

func readsOf(l *liveIndex) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.reads
}

// progressIndexer is an Indexer whose progress stream is the test's channel.
type progressIndexer struct {
	contract.Indexer
	progress chan contract.Progress
}

func (p progressIndexer) Subscribe(ctx context.Context) <-chan contract.Progress {
	out := make(chan contract.Progress)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case v := <-p.progress:
				select {
				case out <- v:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

// TestStorageUsageIsReadOnSettings checks that the data folder is not walked for every snapshot: its sizes are read
// when the Settings page shows them.
func TestStorageUsageIsReadOnSettings(t *testing.T) {
	h := newHarness(t, nil)
	h.wait("snapshot", func() bool { return h.app.data.ready })
	if err := os.WriteFile(filepath.Join(h.app.d.DataDir, "index.db"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	h.app.reload()
	h.wait("a reload", func() bool { return !h.app.reloading })
	if len(h.app.usage) != 0 {
		t.Fatalf("a reload walked the data folder: %+v", h.app.usage)
	}
	h.app.page = pageSettings
	h.wait("storage sizes", func() bool { return len(h.app.usage) == 1 })
	if h.app.usage[0].name != "index.db" || h.app.usage[0].bytes != 4096 {
		t.Errorf("usage %+v, want index.db at 4096 bytes", h.app.usage)
	}
}

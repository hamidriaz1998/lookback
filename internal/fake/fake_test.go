package fake

import (
	"context"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// The UI depends on this fixture: 300 indexed images, two installed combinations, one error.
func TestStoreFixture(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	idx, err := s.ListFileIndex(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	var done, failed int
	for _, r := range idx {
		switch r.Status {
		case contract.StatusDone:
			done++
		case contract.StatusError:
			failed++
		}
	}
	if done != 300 || failed != 1 {
		t.Fatalf("file_index: %d done, %d error; want 300 and 1", done, failed)
	}
	combos, _ := s.ListCombos(ctx)
	if len(combos) != 2 || combos[0].Bench == nil {
		t.Fatalf("combos: got %d, want 2 with bench results", len(combos))
	}
	hits, err := s.Search(ctx, 1, unitVector(7), nil, 60)
	if err != nil || len(hits) != 60 {
		t.Fatalf("search: %d hits, err %v; want 60", len(hits), err)
	}
	for i := 1; i < len(hits); i++ {
		if hits[i].Score > hits[i-1].Score {
			t.Fatal("search results are not sorted by score")
		}
	}
}

// The ETA must count down while the job runs, and the tokens per second must vary.
func TestIndexerProgressMoves(t *testing.T) {
	x := newIndexer(time.Millisecond)
	var etas []time.Duration
	rates := map[float64]bool{}
	for i := 0; i < 40000; i++ {
		p, _ := x.step()
		if i == 10000 && p.ETA >= 0 {
			t.Fatalf("ETA %v at 10 s, want negative while estimating", p.ETA)
		}
		if i >= 31000 && i%1000 == 0 {
			etas = append(etas, p.ETA)
		}
		rates[float64(int(p.TokensPerSec))] = true
	}
	for i := 1; i < len(etas); i++ {
		if etas[i] >= etas[i-1] {
			t.Fatalf("ETA did not count down: %v then %v", etas[i-1], etas[i])
		}
	}
	if len(rates) < 5 {
		t.Fatalf("tokens/s took %d distinct values, want it to vary", len(rates))
	}
}

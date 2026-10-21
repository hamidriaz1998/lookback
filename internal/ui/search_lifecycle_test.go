package ui

import (
	"context"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// gatedSearch holds every Search until release is closed, then returns hits or err. A held search whose context
// ends returns that context's error, as the real store does.
type gatedSearch struct {
	contract.Store
	release chan struct{}
	hits    []contract.Hit
	err     error
}

func (g *gatedSearch) Search(ctx context.Context, gen int64, q []float32, kinds []contract.Kind, limit int) ([]contract.Hit, error) {
	select {
	case <-g.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return g.hits, g.err
}

// TestClearingQueryDropsItsSearch checks that a query cleared with Esc while it runs cannot write its outcome into
// the state the user now sees. A late failure must not show "Search failed" over Recent files, and a late success
// must not replace them.
func TestClearingQueryDropsItsSearch(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		hits int
	}{
		{"late failure", testError("the index is unreadable"), 0},
		{"late success", nil, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gate := &gatedSearch{release: make(chan struct{}), err: tc.err}
			for i := 0; i < tc.hits; i++ {
				gate.hits = append(gate.hits, contract.Hit{FileID: int64(i + 1), Path: "/x/receipt.png", Kind: contract.KindImage, Modality: "image"})
			}
			h := newHarness(t, func(s *fake.Store) contract.Store { gate.Store = s; return gate })
			h.wait("snapshot", func() bool { return h.app.data.ready })
			h.app.search.query = "receipt"
			h.app.search.submit = true
			h.wait("query running", func() bool { return h.app.search.searching })
			h.tt.Key(ui.Modifiers(0), ui.KeyEscape)
			h.wait("query cleared", func() bool { return h.app.search.query == "" && !h.app.search.searching })
			close(gate.release)
			h.pump(0)
			h.pump(0)
			if h.app.search.err != "" {
				t.Errorf("the cleared query set its error %q", h.app.search.err)
			}
			if len(h.app.search.hits) != 0 {
				t.Errorf("the cleared query kept %d hits", len(h.app.search.hits))
			}
			for _, txt := range h.tt.Texts() {
				if tc.err != nil && txt == "Search failed: "+tc.err.Error() {
					t.Errorf("the cleared query shows %q", txt)
				}
			}
		})
	}
}

// stubSearch returns hits from Search, whatever the query, so a test controls the store's order and scores.
type stubSearch struct {
	contract.Store
	hits []contract.Hit
}

func (s stubSearch) Search(context.Context, int64, []float32, []contract.Kind, int) ([]contract.Hit, error) {
	return append([]contract.Hit(nil), s.hits...), nil
}

// TestSearchAppliesFilenameBoostAndPageLimit checks that a query goes through the search service: a file whose
// name holds a query word is boosted past a slightly closer unrelated file, and at most 60 results show.
func TestSearchAppliesFilenameBoostAndPageLimit(t *testing.T) {
	var hits []contract.Hit
	hits = append(hits,
		contract.Hit{FileID: 1, ItemID: 1, Path: "/pics/other/unrelated.png", Kind: contract.KindImage, Modality: "image", Score: 0.82},
		contract.Hit{FileID: 2, ItemID: 2, Path: "/pics/receipt.png", Kind: contract.KindImage, Modality: "image", Score: 0.80},
	)
	for i := 0; i < 80; i++ {
		hits = append(hits, contract.Hit{FileID: int64(100 + i), ItemID: int64(100 + i),
			Path: "/pics/img.png", Kind: contract.KindImage, Modality: "image", Score: 0.5 - float64(i)/1000})
	}
	h := newHarness(t, func(s *fake.Store) contract.Store { return stubSearch{Store: s, hits: hits} })
	h.wait("snapshot", func() bool { return h.app.data.ready })
	h.app.search.query = "receipt"
	h.app.search.submit = true
	h.wait("results", func() bool { return len(h.app.search.hits) > 0 && !h.app.search.searching })
	if got := h.app.search.hits[0].Path; got != "/pics/receipt.png" {
		t.Errorf("first result is %q; the filename boost should put receipt.png first", got)
	}
	if n := len(h.app.search.hits); n != 60 {
		t.Errorf("%d results, want the top 60", n)
	}
}

// mixedRecent returns recent files of both kinds, newest first: even positions are images, odd ones documents.
type mixedRecent struct{ contract.Store }

func (m mixedRecent) RecentItems(_ context.Context, _ int64, limit int) ([]contract.Hit, error) {
	var out []contract.Hit
	for i := 0; i < min(limit, 240); i++ {
		h := contract.Hit{FileID: int64(i + 1), ItemID: int64(i + 1), Path: "/pics/file.png", Kind: contract.KindImage, Modality: "image"}
		if i%2 == 1 {
			h.Path, h.Kind, h.Modality = "/docs/notes.txt", contract.KindText, "text"
		}
		out = append(out, h)
	}
	return out, nil
}

// TestIdleFilterShowsOnlyChosenKind checks that the type filter applies to the recent files before a query:
// Documents shows documents and Images shows images, each up to the recent-file limit.
func TestIdleFilterShowsOnlyChosenKind(t *testing.T) {
	h := newHarness(t, func(s *fake.Store) contract.Store { return mixedRecent{s} })
	h.wait("recent files", func() bool { return len(h.app.data.recent) > 0 })
	h.tt.Click("Documents")
	h.pump(0)
	docs := h.app.items()
	if len(docs) != recentLimit {
		t.Errorf("Documents shows %d files, want %d", len(docs), recentLimit)
	}
	for _, r := range docs {
		if r.Kind == contract.KindImage {
			t.Errorf("Documents shows the image %s", r.Path)
		}
	}
	h.tt.Click("Images")
	h.pump(0)
	imgs := h.app.items()
	if len(imgs) != recentLimit {
		t.Errorf("Images shows %d files, want %d", len(imgs), recentLimit)
	}
	for _, r := range imgs {
		if r.Kind != contract.KindImage {
			t.Errorf("Images shows the %s %s", r.Kind, r.Path)
		}
	}
}

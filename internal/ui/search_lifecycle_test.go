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

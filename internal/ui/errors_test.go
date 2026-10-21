package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// manyErrors adds failed files to the first folder: n files whose last indexing failed in the active generation.
type manyErrors struct {
	contract.Store
	n int
}

func (m manyErrors) ListFiles(ctx context.Context, folder int64) ([]contract.FileRec, error) {
	files, err := m.Store.ListFiles(ctx, folder)
	if folder != 1 {
		return files, err
	}
	for i := 0; i < m.n; i++ {
		files = append(files, contract.FileRec{ID: errFileID(i), FolderID: 1, Path: fmt.Sprintf("/pics/bad-%04d.png", i), Kind: contract.KindImage, Size: 1, MTime: 1})
	}
	return files, err
}

func (m manyErrors) ListFileIndex(ctx context.Context, gen int64) ([]contract.FileIndex, error) {
	rows, err := m.Store.ListFileIndex(ctx, gen)
	for i := 0; i < m.n; i++ {
		rows = append(rows, contract.FileIndex{FileID: errFileID(i), Gen: gen, Status: contract.StatusError, Error: "unsupported format"})
	}
	return rows, err
}

func errFileID(i int) int64 { return int64(100000 + i) }

// retryLog records the files Retry is asked for.
type retryLog struct {
	contract.Indexer
	mu  sync.Mutex
	ids []int64
}

func (r *retryLog) Retry(ctx context.Context, ids []int64) error {
	r.mu.Lock()
	r.ids = append(r.ids, ids...)
	r.mu.Unlock()
	return nil
}

// TestLargeErrorListIsVirtualAndKeyboardRetries checks that 5,000 failed files build only the rows on screen, and that
// Down then Enter retries the file below the chosen row.
func TestLargeErrorListIsVirtualAndKeyboardRetries(t *testing.T) {
	const n = 5000
	log := &retryLog{}
	themes := func(context.Context) []contract.ThemeSource {
		return []contract.ThemeSource{fixedTheme("light", false), fixedTheme("dark", true)}
	}
	h := newHarnessTweak(t, func(s *fake.Store) contract.Store { return manyErrors{Store: s, n: n} }, themes, false,
		func(d *Deps) { log.Indexer = d.Indexer; d.Indexer = log })
	h.wait("the errors", func() bool { return len(h.app.data.errors) >= n })
	h.app.page = pageActivity
	h.pump(0)
	rows := 0
	for _, txt := range h.tt.Texts() {
		if strings.HasPrefix(txt, "bad-") {
			rows++
		}
	}
	if rows == 0 || rows > 100 {
		t.Fatalf("%d error rows are built for %d errors, want only the rows on screen", rows, n)
	}
	h.tt.Click("bad-0000.png")
	h.pump(0)
	if h.app.errSel != 0 {
		t.Fatalf("clicking the first row selected %d", h.app.errSel)
	}
	h.tt.Key(ui.Modifiers(0), ui.KeyDown)
	h.pump(0)
	h.tt.Key(ui.Modifiers(0), ui.KeyEnter)
	h.wait("the retry", func() bool {
		log.mu.Lock()
		defer log.mu.Unlock()
		return len(log.ids) == 1
	})
	if log.ids[0] != errFileID(1) {
		t.Errorf("Enter retried file %d, want %d", log.ids[0], errFileID(1))
	}
}

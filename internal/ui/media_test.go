package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// pageRenderer draws each page as a solid picture whose red value is the page number, so a test can tell the
// pages apart. It is the fake PDF renderer; WP9 supplies the real one.
type pageRenderer struct{}

func (pageRenderer) RenderPage(_ context.Context, _ string, page, edge int) (image.Image, error) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+3] = uint8(page), 0xff
	}
	return img, nil
}

// redOf returns the red value of the top-left pixel of img.
func redOf(img image.Image) uint8 {
	r, _, _, _ := img.At(0, 0).RGBA()
	return uint8(r >> 8)
}

// TestFileMediaDrawsTheRequestedPage checks that a document page is drawn from the renderer, page by page, and that
// the tile cache keeps each page apart: a second request reads the page it asked for, not another one's tile.
func TestFileMediaDrawsTheRequestedPage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "scan.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := FileMedia{CacheDir: filepath.Join(dir, "thumbs"), Pages: pageRenderer{}}
	for round := 0; round < 2; round++ {
		for _, page := range []int{2, 7} {
			img, err := m.Image(context.Background(), Request{Path: path, Page: page, Edge: 200})
			if err != nil {
				t.Fatalf("round %d page %d: %v", round, page, err)
			}
			if got := redOf(img); got != uint8(page) {
				t.Errorf("round %d: page %d drew page %d", round, page, got)
			}
		}
	}
}

// TestFileMediaPagesNeedARenderer checks that a document page without a renderer has no picture, so its result
// shows its text instead.
func TestFileMediaPagesNeedARenderer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scan.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.4"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := FileMedia{}.Image(context.Background(), Request{Path: path, Page: 2, Edge: 200})
	if err == nil || !errors.Is(err, ErrNoPicture) {
		t.Fatalf("a page without a renderer returned %v, want ErrNoPicture", err)
	}
}

// pageMedia records the requests it gets and draws them with pageRenderer, for the UI tests.
type pageMedia struct {
	mu       sync.Mutex
	requests []Request
}

func (m *pageMedia) Image(_ context.Context, req Request) (image.Image, error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	m.mu.Unlock()
	if req.Page > 0 {
		return pageRenderer{}.RenderPage(context.Background(), req.Path, req.Page, req.Edge)
	}
	return solidMedia{}.Image(context.Background(), req)
}

func (m *pageMedia) asked(page int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.requests {
		if r.Page == page {
			return true
		}
	}
	return false
}

// TestDocumentPagesAndTextResults checks the result tiles of document hits: two pages of one PDF are two pictures,
// and a text document shows its matched text rather than a blank icon tile.
func TestDocumentPagesAndTextResults(t *testing.T) {
	media := &pageMedia{}
	hits := []contract.Hit{
		{FileID: 1, ItemID: 1, Path: "/docs/scan.pdf", Kind: contract.KindPDF, Modality: "image", Unit: 2, Score: 0.9},
		{FileID: 1, ItemID: 2, Path: "/docs/scan.pdf", Kind: contract.KindPDF, Modality: "image", Unit: 7, Score: 0.8},
		{FileID: 3, ItemID: 3, Path: "/docs/budget.txt", Kind: contract.KindText, Modality: "text", Snippet: "quarterly budget review", Score: 0.7},
	}
	h := newHarnessTweak(t, func(s *fake.Store) contract.Store { return stubSearch{Store: s, hits: hits} },
		func(context.Context) []contract.ThemeSource {
			return []contract.ThemeSource{fixedTheme("light", false), fixedTheme("dark", true)}
		}, false, func(d *Deps) { d.Media = media })
	h.wait("snapshot", func() bool { return h.app.data.ready })
	h.app.search.query = "budget"
	h.app.search.submit = true
	h.wait("results", func() bool { return len(h.app.search.hits) == 3 && !h.app.search.searching })
	h.pump(0)
	if !media.asked(2) || !media.asked(7) {
		t.Errorf("the PDF pages were requested as %+v; want pages 2 and 7", media.requests)
	}
	h.wantText("quarterly budget review")
}

// scriptMedia draws every picture as one solid color, and reports every file as missing while missing is set.
type scriptMedia struct {
	mu      sync.Mutex
	missing bool
	asks    int
}

func (m *scriptMedia) Image(ctx context.Context, req Request) (image.Image, error) {
	m.mu.Lock()
	m.asks++
	missing := m.missing
	m.mu.Unlock()
	if missing {
		return nil, fmt.Errorf("%s: %w", req.Path, os.ErrNotExist)
	}
	return solidMedia{}.Image(ctx, req)
}

func (m *scriptMedia) set(f func(*scriptMedia)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	f(m)
}

// pinnedRecent is the store with its recent files cut to one hit, so the preview keeps its selection while the
// file behind it changes or goes.
type pinnedRecent struct {
	contract.Store
	hit contract.Hit
}

func (p pinnedRecent) RecentItems(context.Context, int64, int) ([]contract.Hit, error) {
	return []contract.Hit{p.hit}, nil
}

// TestPreviewFollowsTheFile checks that the preview tracks its file: a changed file decodes again, a deleted one
// shows as moved or deleted, and a restored one shows its picture again rather than the old error.
func TestPreviewFollowsTheFile(t *testing.T) {
	ctx := context.Background()
	media := &scriptMedia{}
	var hit contract.Hit
	h := newHarnessTweak(t, func(s *fake.Store) contract.Store {
		gens, err := s.ListGenerations(ctx)
		if err != nil {
			t.Fatal(err)
		}
		recent, err := s.RecentItems(ctx, gens[len(gens)-1].ID, 1)
		if err != nil || len(recent) == 0 {
			t.Fatalf("no recent file: %v", err)
		}
		hit = recent[0]
		return pinnedRecent{Store: s, hit: hit}
	}, func(context.Context) []contract.ThemeSource {
		return []contract.ThemeSource{fixedTheme("light", false), fixedTheme("dark", true)}
	}, false, func(d *Deps) { d.Media = media })
	h.wait("snapshot", func() bool { return h.app.data.ready })
	pictured := func() bool {
		bm, err, ok := h.app.thumbs.get(h.app.pictureKey(hit, 0, h.app.data.intSetting("max_image_edge", 1600)))
		return ok && err == nil && bm != nil
	}
	h.wait("the first picture", pictured)

	rec := recordOf(t, h.store, hit.FileID)
	rec.MTime++
	if err := h.store.SaveFile(ctx, &rec); err != nil {
		t.Fatal(err)
	}
	h.app.reload()
	h.wait("the changed file decodes again", func() bool { return pictured() && media.asks >= 2 })

	media.set(func(m *scriptMedia) { m.missing = true })
	if err := h.store.DeleteFile(ctx, hit.FileID); err != nil {
		t.Fatal(err)
	}
	h.app.reload()
	h.wait("the moved or deleted state", func() bool {
		for _, txt := range h.tt.Texts() {
			if txt == "This file was moved or deleted." {
				return true
			}
		}
		return false
	})

	media.set(func(m *scriptMedia) { m.missing = false })
	restored := rec
	restored.ID = 0
	if err := h.store.SaveFile(ctx, &restored); err != nil {
		t.Fatal(err)
	}
	h.app.reload()
	h.wait("the restored file shows its picture", func() bool {
		bm, err, ok := h.app.thumbs.get(thumbKey{path: hit.Path, edge: h.app.data.intSetting("max_image_edge", 1600)})
		return ok && err == nil && bm != nil
	})
}

// recordOf returns the stored file with id.
func recordOf(t *testing.T, s contract.Store, id int64) contract.FileRec {
	t.Helper()
	folders, err := s.ListFolders(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range folders {
		files, err := s.ListFiles(t.Context(), f.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range files {
			if r.ID == id {
				return r
			}
		}
	}
	t.Fatalf("no file %d", id)
	return contract.FileRec{}
}

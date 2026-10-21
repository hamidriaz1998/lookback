package ui

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// solidMedia returns a small picture for every path, so tests need no files on disk.
type solidMedia struct{}

func (solidMedia) Image(ctx context.Context, path string, edge int) (image.Image, error) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 6))
	for i := range img.Pix {
		img.Pix[i] = 0x80
	}
	return img, nil
}

// harness runs the App on the fakes. Background results arrive on queue; pump runs them on the test goroutine,
// as the window's main thread would.
type harness struct {
	t     *testing.T
	app   *App
	tt    *ui.Tester
	queue chan func()
	store contract.Store
}

// newHarness builds the App over the fixture store, starts it, and returns its tester. setup, when set,
// changes the fixture or wraps it in the store the App uses.
func newHarness(t *testing.T, setup func(*fake.Store) contract.Store) *harness {
	t.Helper()
	return newHarnessWith(t, setup, func(context.Context) []contract.ThemeSource {
		return []contract.ThemeSource{fixedTheme("light", false), fixedTheme("dark", true)}
	}, false)
}

// newHarnessWith is newHarness with the theme sources built by themes, and the desktop starting dark
// when dark is set.
func newHarnessWith(t *testing.T, setup func(*fake.Store) contract.Store, themes func(context.Context) []contract.ThemeSource, dark bool) *harness {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	base := fake.NewStore()
	var store contract.Store = base
	// The fixture is past first run, as the entry point leaves it; a test may clear the setting in setup.
	if err := base.SetSetting(ctx, "onboarded", "1"); err != nil {
		t.Fatal(err)
	}
	if setup != nil {
		store = setup(base)
	}
	// As the entry point does: the first folder is the real ~/Pictures, so its status is real.
	if home, err := os.UserHomeDir(); err == nil {
		if folders, _ := store.ListFolders(ctx); len(folders) > 0 {
			f := contract.Folder{ID: 1, Path: filepath.Join(home, "Pictures")}
			if err := store.SaveFolder(ctx, &f); err != nil {
				t.Fatal(err)
			}
		}
	}
	rt := fake.NewRuntime(ctx)
	ix := fake.NewIndexer(base, rt)
	if err := ix.Start(ctx); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, queue: make(chan func(), 256), store: store}
	h.app = New(Deps{
		Store:      store,
		Indexer:    ix,
		Runtime:    rt,
		Lab:        fake.NewLab(ctx, ix),
		Downloader: fake.NewDownloader(ctx),
		Detector:   fake.NewDetector(),
		Catalog:    fake.NewCatalog(),
		Themes:     themes(ctx),
		Media:      solidMedia{},
		DataDir:    t.TempDir(),
		OS:         "linux",
	})
	h.app.Start(ctx, func(f func()) { h.queue <- f })
	h.tt = ui.NewTester(h.app.View, 1280, 800)
	if dark {
		h.tt.SetDark(true)
	}
	h.pump(0)
	return h
}

// pump runs the queued results and frames until nothing else is queued, or for at most a second.
func (h *harness) pump(_ int) {
	h.t.Helper()
	deadline := time.After(time.Second)
	for {
		h.tt.Frame()
		select {
		case f := <-h.queue:
			f()
		case <-deadline:
			h.tt.Frame()
			return
		default:
			h.tt.Frame()
			if len(h.queue) == 0 && h.quiet() {
				return
			}
			time.Sleep(time.Millisecond)
		}
	}
}

// quiet reports whether the App has no background work in flight.
func (h *harness) quiet() bool {
	return !h.app.reloading && !h.app.search.searching
}

// wait pumps until cond holds or the test fails.
func (h *harness) wait(what string, cond func() bool) {
	h.t.Helper()
	for i := 0; i < 2000; i++ {
		h.pump(0)
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	h.t.Fatalf("timed out waiting for %s; texts: %q", what, h.tt.Texts())
}

// wantText fails when no text on the screen contains s.
func (h *harness) wantText(s string) {
	h.t.Helper()
	for _, txt := range h.tt.Texts() {
		if strings.Contains(txt, s) {
			return
		}
	}
	h.t.Errorf("no text contains %q; texts: %q", s, h.tt.Texts())
}

// failingSearch is the fixture store with Search failing, for the error state.
type failingSearch struct{ contract.Store }

func (failingSearch) Search(context.Context, int64, []float32, []contract.Kind, int) ([]contract.Hit, error) {
	return nil, errTestSearch
}

var errTestSearch = testError("the index is unreadable")

type testError string

func (e testError) Error() string { return string(e) }

// fixedTheme is a contract.ThemeSource with the built-in light or dark palette.
type fixedTheme2 struct {
	id   string
	dark bool
}

func fixedTheme(id string, dark bool) contract.ThemeSource { return fixedTheme2{id, dark} }

func (f fixedTheme2) ID() string   { return f.id }
func (f fixedTheme2) Name() string { return f.id }
func (f fixedTheme2) Theme(*ui.Theme) *ui.Theme {
	if f.dark {
		return ui.DarkTheme()
	}
	return ui.LightTheme()
}
func (f fixedTheme2) Changed() <-chan struct{} { return nil }

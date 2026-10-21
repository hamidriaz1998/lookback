package main

import (
	"context"
	"fmt"
	"hash/fnv"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
	lookui "github.com/hamidriaz1998/lookback/internal/ui"
)

// fakeDeps wires the UI to the fakes. The fixture's file paths do not exist on disk, so thumbnails come
// from the real screenshots in ~/Pictures, chosen by a hash of the path.
func fakeDeps(ctx context.Context, firstRun bool, theme string) (lookui.Deps, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return lookui.Deps{}, err
	}
	shots, err := filepath.Glob(filepath.Join(home, "Pictures", "screenshot*.png"))
	if err != nil {
		return lookui.Deps{}, err
	}
	if len(shots) == 0 {
		return lookui.Deps{}, fmt.Errorf("no screenshot*.png files in %s", filepath.Join(home, "Pictures"))
	}
	sort.Strings(shots)

	store := fake.NewStore()
	if !firstRun {
		if err := store.SetSetting(ctx, "onboarded", "1"); err != nil {
			return lookui.Deps{}, err
		}
	}
	if theme != "" {
		if err := store.SetSetting(ctx, "theme", theme); err != nil {
			return lookui.Deps{}, err
		}
	}
	// The fixture's first folder is the real ~/Pictures, so its status is real. The second one stays missing.
	pics := &contract.Folder{ID: 1, Path: filepath.Join(home, "Pictures")}
	if err := store.SaveFolder(ctx, pics); err != nil {
		return lookui.Deps{}, err
	}
	rt := fake.NewRuntime(ctx)
	ix := fake.NewIndexer(store, rt)
	if err := ix.Start(ctx); err != nil {
		return lookui.Deps{}, err
	}
	return lookui.Deps{
		Store:      store,
		Indexer:    ix,
		Runtime:    rt,
		Lab:        fake.NewLab(ctx, ix),
		Downloader: fake.NewDownloader(ctx),
		Detector:   fake.NewDetector(),
		Catalog:    fake.NewCatalog(),
		Themes:     fakeThemes(),
		Media:      fakeMedia{pictures: shots},
		OS:         contract.OS("linux"),
		DataDir:    filepath.Join(home, ".local", "share", "lookback"),
	}, nil
}

// fakeMedia serves the fixture's pictures from real screenshots. A few paths report a missing file, so the
// moved-or-deleted state is reachable.
type fakeMedia struct{ pictures []string }

func (m fakeMedia) Image(ctx context.Context, req lookui.Request) (image.Image, error) {
	path, edge := req.Path, req.Edge
	h := fnv.New32a()
	_, _ = h.Write([]byte(path))
	sum := h.Sum32()
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".txt":
		return nil, lookui.ErrNoPicture
	case ".png":
		if sum%97 == 0 {
			return nil, fmt.Errorf("%s: %w", path, os.ErrNotExist)
		}
	}
	return lookui.FileMedia{}.Image(ctx, lookui.Request{Path: m.pictures[int(sum)%len(m.pictures)], Edge: edge})
}

// fakeThemes returns two fixed sources. They stand in for the theme package (WP5) until it merges.
func fakeThemes() []contract.ThemeSource {
	return []contract.ThemeSource{
		fixedTheme{id: "light", name: "System light", dark: false},
		fixedTheme{id: "dark", name: "System dark", dark: true},
	}
}

// fixedTheme is a fixed contract.ThemeSource that ignores the desktop's theme.
type fixedTheme struct {
	id, name string
	dark     bool
}

func (f fixedTheme) ID() string   { return f.id }
func (f fixedTheme) Name() string { return f.name }
func (f fixedTheme) Theme(*ui.Theme) *ui.Theme {
	if f.dark {
		return ui.DarkTheme()
	}
	return ui.LightTheme()
}
func (f fixedTheme) Changed() <-chan struct{} { return nil }

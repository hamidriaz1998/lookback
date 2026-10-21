package ui

import (
	"context"

	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
)

// themeState applies the theme chosen in Settings to the window, and rebuilds it when a source changes
// (an Omarchy theme switched live, UI guide section 8).
type themeState struct {
	sources []contract.ThemeSource
	base    *ui.Theme // the desktop's default theme as of the last frame
	applied string    // the setting the current theme was built from; "" is the desktop's default
	cur     *ui.Theme // the theme of the applied setting
	dirty   bool
}

// start watches every source that reports changes. dispatch runs on the main thread.
func (ts *themeState) start(ctx context.Context, dispatch func(func()), sources []contract.ThemeSource) {
	ts.sources = sources
	for _, s := range sources {
		ch := s.Changed()
		if ch == nil {
			continue
		}
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-ch:
					dispatch(func() { ts.dirty = true })
				}
			}
		}()
	}
}

// apply sets the theme that id names for this frame. SetTheme covers one frame only, so the desktop's default
// is read on every frame. The theme is rebuilt when the choice, a source or the desktop's light or dark
// appearance changed.
func (ts *themeState) apply(c *ui.Context, id string) {
	base := *c.Theme()
	if ts.base == nil || base.Dark != ts.base.Dark {
		ts.dirty = true
	}
	ts.base = &base
	if ts.cur == nil || id != ts.applied || ts.dirty {
		ts.applied, ts.dirty = id, false
		ts.cur = ts.build(id)
	}
	c.SetTheme(ts.cur)
}

// build returns the theme of source id, or a copy of the desktop's theme when id names none.
func (ts *themeState) build(id string) *ui.Theme {
	if s := ts.source(id); s != nil {
		return s.Theme(ts.base)
	}
	t := *ts.base
	return &t
}

// source returns the theme source that id names, or nil.
func (ts *themeState) source(id string) contract.ThemeSource {
	for _, s := range ts.sources {
		if s.ID() == id {
			return s
		}
	}
	return nil
}

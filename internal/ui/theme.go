package ui

import (
	"context"

	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
)

// themeState applies the theme chosen in Settings to the window, and reapplies it when a source changes
// (an Omarchy theme switched live, UI guide section 8).
type themeState struct {
	sources []contract.ThemeSource
	base    *ui.Theme // the desktop's default theme, copied on the first frame
	applied string    // the setting last applied; "" is the desktop's default
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

// apply sets the theme that id names. It runs each frame and does work only when the choice or a source changed.
func (ts *themeState) apply(c *ui.Context, id string) {
	if ts.base == nil {
		t := *c.Theme()
		ts.base = &t
	}
	if id == ts.applied && !ts.dirty {
		return
	}
	ts.applied, ts.dirty = id, false
	for _, s := range ts.sources {
		if s.ID() == id {
			c.SetTheme(s.Theme(ts.base))
			return
		}
	}
	t := *ts.base
	c.SetTheme(&t)
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

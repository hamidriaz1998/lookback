package ui

import (
	"context"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// snapshotThemes are the themes the screenshots cover: each fixed palette, the desktop in both
// appearances, and an Omarchy theme. Each one gets a folder of its own under LOOKBACK_SNAPSHOTS.
var snapshotThemes = []struct {
	folder, id string
	dark       bool
}{
	{"catppuccin-mocha", "catppuccin-mocha", true},
	{"catppuccin-latte", "catppuccin-latte", false},
	{"one-dark", "one-dark", true},
	{"one-light", "one-light", false},
	{"system-light", "system", false},
	{"system-dark", "system", true},
	{"omarchy", "omarchy", true},
}

// TestSnapshots writes the screenshots of every screen and state in each snapshot theme, offscreen from the
// tester. It runs only when LOOKBACK_SNAPSHOTS names the output folder.
func TestSnapshots(t *testing.T) {
	dir := os.Getenv("LOOKBACK_SNAPSHOTS")
	if dir == "" {
		t.Skip("set LOOKBACK_SNAPSHOTS=<dir> to write the screenshots")
	}
	isolateOmarchy(t, omarchyDark)
	for _, th := range snapshotThemes {
		folder := filepath.Join(dir, th.folder)
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, sc := range snapshotCases() {
			h := newThemedHarness(t, th.id, th.dark, func(s *fake.Store) contract.Store {
				if sc.setup != nil {
					return sc.setup(s)
				}
				return s
			})
			h.app.page = sc.page
			if sc.prepare != nil {
				sc.prepare(h)
			}
			h.tt.SetSize(1896, 1025)
			h.wait(sc.name, func() bool { return h.app.data.ready && (sc.ready == nil || sc.ready(h)) })
			h.pump(0)
			writePNG(t, filepath.Join(folder, sc.name+".png"), h)
		}
	}
}

// snapshotCase is one screen or state to capture.
type snapshotCase struct {
	name    string
	page    page
	setup   func(*fake.Store) contract.Store
	prepare func(h *harness)
	ready   func(h *harness) bool
}

func snapshotCases() []snapshotCase {
	noFolders := func(s *fake.Store) contract.Store {
		ctx := context.Background()
		folders, _ := s.ListFolders(ctx)
		for _, f := range folders {
			_ = s.DeleteFolder(ctx, f.ID)
		}
		return s
	}
	firstRunSetup := func(s *fake.Store) contract.Store {
		_ = s.SetSetting(context.Background(), "onboarded", "")
		return s
	}
	failing := func(s *fake.Store) contract.Store { return failingSearch{s} }
	labsDone := func(h *harness) bool {
		n := 0
		for _, s := range h.app.labSteps {
			if s.State == contract.LabDone {
				n++
			}
		}
		return n >= 2
	}
	return []snapshotCase{
		{name: "search-recent", page: pageSearch, ready: func(h *harness) bool { return len(h.app.data.recent) > 0 }},
		{name: "search-results", page: pageSearch, prepare: func(h *harness) {
			h.app.search.query, h.app.search.submit = "hotspot error dialog", true
		}, ready: func(h *harness) bool { return len(h.app.search.hits) > 0 && !h.app.search.searching }},
		{name: "search-no-folders", page: pageSearch, setup: noFolders, ready: func(h *harness) bool { return h.app.data.ready }},
		{name: "search-error", page: pageSearch, setup: failing, prepare: func(h *harness) {
			h.app.search.query, h.app.search.submit = "anything", true
		}, ready: func(h *harness) bool { return h.app.search.err != "" }},
		{name: "activity", page: pageActivity, ready: func(h *harness) bool { return h.app.hasProgress }},
		{name: "activity-battery", page: pageActivity, prepare: func(h *harness) {
			h.app.d.Indexer.Pause(contract.PauseBattery)
		}, ready: func(h *harness) bool { return h.app.progress.Paused == contract.PauseBattery }},
		{name: "folders", page: pageFolders, ready: func(h *harness) bool { return len(h.app.data.folders) == 2 }},
		{name: "folders-rules", page: pageFolders, prepare: func(h *harness) {
			h.app.folders.editOpen, h.app.folders.editID, h.app.folders.editText = true, 2, "*.md"
		}, ready: func(h *harness) bool { return len(h.app.data.folders) == 2 }},
		{name: "folders-empty", page: pageFolders, setup: noFolders, ready: func(h *harness) bool { return h.app.data.ready }},
		{name: "models", page: pageModels, ready: func(h *harness) bool { return len(h.app.data.combos) > 0 }},
		{name: "models-rebuilding", page: pageModels, prepare: func(h *harness) {
			h.app.rebuild = &contract.Progress{FilesDone: 1240, FilesTotal: 3100}
		}, ready: func(h *harness) bool { return len(h.app.data.combos) > 0 }},
		{name: "models-switch-precision", page: pageModels, prepare: func(h *harness) {
			h.wait("combos", func() bool { return len(h.app.data.combos) > 0 })
			h.app.makeActive(contract.Combo{Backend: contract.BackendCPU, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16})
		}, ready: func(h *harness) bool { return len(h.app.data.combos) > 0 }},
		{name: "models-fallback", page: pageModels, prepare: func(h *harness) {
			h.app.d.Runtime.(*fake.Runtime).FallBackToCPU()
		}, ready: func(h *harness) bool { return h.app.runtime.State == contract.RuntimeCPUFallback }},
		{name: "onboarding-1", page: pageSearch, setup: firstRunSetup, ready: func(h *harness) bool {
			return h.app.onb != nil && !h.app.onb.detecting
		}},
		{name: "onboarding-2", page: pageSearch, setup: firstRunSetup, prepare: func(h *harness) {
			h.wait("detection", func() bool { return h.app.onb != nil && !h.app.onb.detecting })
			h.app.onb.step = 2
		}, ready: labsDone},
		{name: "onboarding-2-selected", page: pageSearch, setup: firstRunSetup, prepare: func(h *harness) {
			h.wait("detection", func() bool { return h.app.onb != nil && !h.app.onb.detecting })
			h.app.onb.step = 2
			h.wait("lab result", func() bool {
				for _, s := range h.app.labSteps {
					if s.State == contract.LabDone {
						h.app.onb.chosen[s.Combo.ID()] = true
						return true
					}
				}
				return false
			})
		}, ready: func(h *harness) bool { return h.app.onb != nil && h.app.onb.step == 2 && h.app.chosenCombo() != nil }},
		{name: "onboarding-3", page: pageSearch, setup: firstRunSetup, prepare: func(h *harness) {
			h.wait("detection", func() bool { return h.app.onb != nil && !h.app.onb.detecting })
			h.app.onb.step = 3
		}, ready: func(h *harness) bool { return h.app.onb != nil && h.app.onb.step == 3 }},
		{name: "onboarding-3-empty", page: pageSearch, setup: func(s *fake.Store) contract.Store {
			_ = s.SetSetting(context.Background(), "onboarded", "")
			return noFolders(s)
		}, prepare: func(h *harness) {
			h.wait("detection", func() bool { return h.app.onb != nil && !h.app.onb.detecting })
			h.app.onb.step = 3
		}, ready: func(h *harness) bool { return h.app.onb != nil && h.app.onb.step == 3 && len(h.app.data.folders) == 0 }},
		{name: "settings", page: pageSettings, ready: func(h *harness) bool { return h.app.data.ready && !h.app.usageBusy && !h.app.usageAt.IsZero() }},
	}
}

// writePNG saves the tester's current frame to path.
func writePNG(t *testing.T, path string, h *harness) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, h.tt.Image()); err != nil {
		t.Fatal(err)
	}
}

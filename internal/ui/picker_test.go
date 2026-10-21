package ui

import (
	"context"
	"sync"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// fakePicker stands in for the native directory dialog: it returns dir, or "" to model a cancel.
type fakePicker struct {
	mu    sync.Mutex
	dir   string
	calls int
}

func (p *fakePicker) pick(context.Context) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.dir, nil
}

func (p *fakePicker) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// hasFolder reports whether the store lists a folder at path.
func hasFolder(t *testing.T, s contract.Store, path string) bool {
	t.Helper()
	folders, err := s.ListFolders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range folders {
		if f.Path == path {
			return true
		}
	}
	return false
}

// TestAddFolderSavesThePickedFolder checks that Add folder opens the directory dialog and saves the folder it returns.
func TestAddFolderSavesThePickedFolder(t *testing.T) {
	picker := &fakePicker{dir: "/pics/new"}
	h := newHarnessTweak(t, nil, fixedThemes, false, func(d *Deps) { d.PickFolder = picker.pick })

	h.wait("the data", func() bool { return h.app.data.ready })
	h.app.page = pageFolders
	h.pump(0)
	h.tt.Click("Add folder")
	h.wait("the folder is saved", func() bool { return hasFolder(t, h.store, "/pics/new") })
	if n := picker.count(); n != 1 {
		t.Errorf("the dialog opened %d times, want once", n)
	}
}

// TestCanceledPickerChangesNothing checks that a dialog the user cancels saves no folder and shows no failure.
func TestCanceledPickerChangesNothing(t *testing.T) {
	picker := &fakePicker{}
	h := newHarnessTweak(t, nil, fixedThemes, false, func(d *Deps) { d.PickFolder = picker.pick })
	h.wait("the data", func() bool { return h.app.data.ready })
	before := len(h.app.data.folders)
	h.app.page = pageFolders
	h.pump(0)
	h.tt.Click("Add folder")
	h.wait("the dialog returns", func() bool { return picker.count() == 1 })
	h.pump(0)
	if h.app.notice != nil {
		t.Errorf("a canceled dialog shows %q", h.app.notice.msg)
	}
	if n := len(h.app.data.folders); n != before {
		t.Errorf("%d folders after a canceled dialog, want %d", n, before)
	}
}

// TestOnboardingEditsRulesBeforeStart checks that the first run's folder step offers the rules editor, and that the
// rules are saved before indexing starts.
func TestOnboardingEditsRulesBeforeStart(t *testing.T) {
	h, _ := newFirstRunHarness(t, nil, nil)
	waitDetection(h)
	h.app.setStep(3)
	h.pump(0)
	h.tt.Click("Edit rules")
	h.pump(0)
	h.app.folders.editText = "*.png"
	h.tt.Click("Save rules")
	h.wait("the rules are saved", func() bool {
		folders, _ := h.store.ListFolders(context.Background())
		for _, f := range folders {
			if f.Ignore == "*.png" {
				return true
			}
		}
		return false
	})
	if h.app.onb == nil {
		t.Fatal("saving rules left onboarding")
	}
}

// fixedThemes is the light and dark theme sources the harness uses by default.
func fixedThemes(context.Context) []contract.ThemeSource {
	return []contract.ThemeSource{fixedTheme("light", false), fixedTheme("dark", true)}
}

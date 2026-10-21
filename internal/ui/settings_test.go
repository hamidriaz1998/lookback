package ui

import "testing"

// TestSettingsThemeChoiceApplies checks that choosing a theme tile stores it, so the window takes it at once.
func TestSettingsThemeChoiceApplies(t *testing.T) {
	h := newHarness(t, nil)
	h.app.page = pageSettings
	h.pump(0)
	h.wantText("Appearance")
	if err := h.tt.Click("Use dark"); err != nil {
		t.Fatal(err)
	}
	h.pump(0)
	if got := h.app.data.settings["theme"]; got != "dark" {
		t.Errorf("theme setting = %q, want dark", got)
	}
}

// TestSettingsShowsAboutAndStorage checks the sections that show measured values.
func TestSettingsShowsAboutAndStorage(t *testing.T) {
	h := newHarness(t, nil)
	h.app.page = pageSettings
	h.wait("snapshot", func() bool { return h.app.data.ready })
	h.wantText("Storage")
	h.wantText("Open logs")
	h.wantText("llama.cpp b11755")
	h.wantText("Copy bind line")
}

package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
)

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
	h.wantText("Super+.")
	h.wantText(`hl.bind("SUPER + period", hl.dsp.exec_cmd("lookback --toggle"))`)
}

// TestSettingsHotkeyRecorderCapturesAndPersists checks an actual MyGo key event reaches the settings store.
func TestSettingsHotkeyRecorderCapturesAndPersists(t *testing.T) {
	h := newHarness(t, nil)
	h.app.page = pageSettings
	h.wait("settings", func() bool { return h.app.data.ready })
	if err := h.tt.Click("Record global hotkey"); err != nil {
		t.Fatal(err)
	}
	h.pump(0)
	if !h.app.settings.recording {
		t.Fatal("hotkey recording did not start")
	}
	h.tt.Key(ui.Ctrl|ui.Alt, ui.KeyL)
	h.pump(0)
	if got := h.app.data.settings["hotkey"]; got != "Ctrl+Alt+L" {
		t.Fatalf("hotkey setting = %q, want Ctrl+Alt+L", got)
	}
	h.wait("hotkey persistence", func() bool {
		got, ok, err := h.store.GetSetting(context.Background(), "hotkey")
		return err == nil && ok && got == "Ctrl+Alt+L"
	})
	h.wantText(`hl.bind("CTRL + ALT + l", hl.dsp.exec_cmd("lookback --toggle"))`)
}

func TestHotkeyFromEventRequiresModifierAndFormatsKey(t *testing.T) {
	if _, ok := hotkeyFromEvent(0, ui.KeyA); ok {
		t.Fatal("unmodified key was accepted")
	}
	if got, ok := hotkeyFromEvent(ui.Super|ui.Shift, ui.KeySpace); !ok || got != "Super+Shift+Space" {
		t.Fatalf("recorded chord = %q, %v; want Super+Shift+Space", got, ok)
	}
	if got := hyprlandBind("Super+Shift+Space"); got != `hl.bind("SUPER + SHIFT + space", hl.dsp.exec_cmd("lookback --toggle"))` {
		t.Fatalf("Hyprland binding = %q", got)
	}
	if got, ok := hotkeyFromEvent(ui.Super, ui.KeyPeriod); !ok || got != "Super+." {
		t.Fatalf("period chord = %q, %v; want Super+.", got, ok)
	}
	if got := hyprlandBind("Super+."); got != `hl.bind("SUPER + period", hl.dsp.exec_cmd("lookback --toggle"))` {
		t.Fatalf("default Hyprland binding = %q", got)
	}
}

// TestSettingsHyprlandTipIsLinuxOnly checks that a Windows build shows the shortcut recorder but not the Hyprland bind.
func TestSettingsHyprlandTipIsLinuxOnly(t *testing.T) {
	h := newHarnessTweak(t, nil, func(context.Context) []contract.ThemeSource {
		return []contract.ThemeSource{fixedTheme("light", false)}
	}, false, func(d *Deps) { d.OS = "windows" })
	h.app.page = pageSettings
	h.wait("settings", func() bool { return h.app.data.ready })
	h.wantText("Record global hotkey")
	for _, txt := range h.tt.Texts() {
		if strings.Contains(txt, "Hyprland Lua") || strings.Contains(txt, "hl.bind") || txt == "Copy bind line" {
			t.Fatalf("Windows shows the Linux bind tip: %q", txt)
		}
	}
}

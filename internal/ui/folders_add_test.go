package ui

import (
	"context"
	"strings"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// TestAddFolderAlreadyAdded checks that picking a folder that is already added changes nothing and says so. On
// Windows a path that differs only by case is the same folder.
func TestAddFolderAlreadyAdded(t *testing.T) {
	pick := ""
	themes := func(context.Context) []contract.ThemeSource {
		return []contract.ThemeSource{fixedTheme("light", false), fixedTheme("dark", true)}
	}
	h := newHarnessTweak(t, nil, themes, false, func(d *Deps) {
		d.PickFolder = func(context.Context) (string, error) { return pick, nil }
	})
	h.app.page = pageFolders
	h.wait("folders", func() bool { return len(h.app.data.folders) == 2 })
	existing := h.app.data.folders[0].folder.Path

	check := func(name, dir string, wantNotice bool) {
		t.Helper()
		h.app.notice = nil
		pick = dir
		h.app.addFolder(true)
		h.pump(0)
		got := h.app.notice != nil && strings.Contains(h.app.notice.msg, "already added")
		if got != wantNotice {
			t.Errorf("%s: already-added notice = %v, want %v", name, got, wantNotice)
		}
		if n := len(h.app.data.folders); n != 2 && wantNotice {
			t.Errorf("%s: a duplicate was saved, folder count %d", name, n)
		}
	}
	check("same path", existing, true)
	if foldPathCase {
		check("other case", strings.ToUpper(existing), true)
	}
}

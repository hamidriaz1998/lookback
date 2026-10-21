package ui

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"github.com/hamidriaz1998/lookback/internal/contract"
)

// modelsState is the Models & Hardware page's state.
type modelsState struct {
	table      ui.ListState
	selected   int
	switchTo   *contract.Combo // a precision switch waiting for confirmation
	switchOpen bool
}

// modelsPage builds the Models & Hardware page (UI guide section 3.7).
func (a *App) modelsPage(c *ui.Context) {
	t := c.Theme()
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Fill().Padding(t.Space(6)).Gap(t.Space(4)).Children(func() {
			ui.Text(c, "Models & Hardware").FontSize(t.Rem(1.15)).Bold()
			a.fallbackCard(c)
			a.activeCard(c)
			a.rebuildCard(c)
			a.installedCombos(c)
		})
	})
	a.switchDialog(c)
}

// activeCard shows the backend, the precisions and the vector space the index is built in.
func (a *App) activeCard(c *ui.Context) {
	t := c.Theme()
	st := a.runtime
	ui.Column(c).Gap(t.Space(2)).Padding(t.Space(3)).Radius(8).Background(t.Surface).Children(func() {
		ui.Text(c, "Active combination").Bold()
		ui.Text(c, "Backend "+backendName(st.Backend)+" · runtime "+string(st.State)).FontFeatures("tnum")
		if g := a.activeGen(); g != nil {
			ui.Text(c, fmt.Sprintf("Text model %s · encoders %s · serves a vector space of model revision %s",
				precisionName(g.Space.TextQuant), precisionName(g.Space.EncQuant), shortRev(g.Space.ModelRev))).
				TextColor(t.TextMuted).FontSize(t.Rem(0.85))
		} else {
			ui.Text(c, "No index yet. Choose a combination in onboarding.").TextColor(t.TextMuted)
		}
	})
}

// fallbackCard is the warning when the GPU backend failed twice and the CPU does the work.
func (a *App) fallbackCard(c *ui.Context) {
	if a.runtime.State != contract.RuntimeCPUFallback {
		return
	}
	t := c.Theme()
	ui.Column(c).Gap(t.Space(2)).Padding(t.Space(3)).Radius(8).Background(t.Surface).Children(func() {
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon("triangle-alert")).TextColor(t.Warning)
			ui.Text(c, "The GPU backend failed twice. Lookback is using the CPU.").Bold().Grow(1)
		})
		if e := spaceErr(a.runtime); e != "" {
			ui.Text(c, e).TextColor(t.TextMuted).FontSize(t.Rem(0.85))
		}
		ui.Row(c).Gap(t.Space(2)).Children(func() {
			if ui.PrimaryButton(c, "Try GPU again").Clicked() {
				a.run(func(ctx context.Context) error { return a.d.Runtime.RetryGPU(ctx) })
			}
			if ui.Button(c, "Show log").Clicked() {
				c.OpenURL(fileURL(filepath.Join(a.d.DataDir, "logs")))
			}
		})
	})
}

// installedCombos lists the installed combinations with their test results, and the actions on the chosen one.
func (a *App) installedCombos(c *ui.Context) {
	t := c.Theme()
	m := &a.models
	ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Installed combinations").FontSize(t.Rem(1.15)).Bold().Grow(1)
		if ui.Button(c, "Compare combinations").Clicked() {
			a.startLab()
		}
	})
	if len(a.data.combos) == 0 {
		ui.Text(c, "No combination is installed yet.").TextColor(t.TextMuted)
		return
	}
	combos := a.data.combos
	m.table.Selected = &m.selected
	cols := []ui.TableColumn{
		{Title: "Combination", Width: 260}, {Title: "Image ms", Width: 110, Align: ui.End},
		{Title: "Tokens/s", Width: 100, Align: ui.End}, {Title: "RAM", Width: 90, Align: ui.End},
		{Title: "VRAM", Width: 90, Align: ui.End}, {Title: "Quality", Width: 100, Align: ui.End},
	}
	ui.Table(c, &m.table, cols, len(combos), func(row, col int) {
		in := combos[row]
		switch col {
		case 0:
			ui.Text(c, comboLabel(in.Combo)).SingleLine()
		case 1, 2, 3, 4, 5:
			ui.Text(c, benchCell(in.Bench, col)).FontFeatures("tnum").SingleLine()
		}
	}).Grow(1)
	if m.selected < 0 || m.selected >= len(combos) {
		m.selected = -1
		return
	}
	a.comboActions(c, combos[m.selected])
}

// comboActions are Make active and Delete for the chosen combination. Delete is off for the active one.
func (a *App) comboActions(c *ui.Context, in contract.Installed) {
	t := c.Theme()
	active := a.isActive(in.Combo)
	ui.Row(c).Gap(t.Space(2)).Children(func() {
		if ui.PrimaryButton(c, "Make active").Disabled(active).Clicked() {
			combo := in.Combo
			a.makeActive(combo)
		}
		if ui.Button(c, "Delete").Disabled(active).Clicked() {
			combo := in.Combo
			a.run(func(ctx context.Context) error { return a.d.Store.DeleteCombo(ctx, combo) })
		}
		if active {
			ui.Text(c, "The active combination can't be deleted.").TextColor(t.TextMuted).SingleLine()
		}
	})
}

// makeActive switches the backend at once; a change of precisions asks first, then rebuilds the index.
func (a *App) makeActive(combo contract.Combo) {
	g := a.activeGen()
	if g != nil && (g.Space.TextQuant != combo.TextQuant || g.Space.EncQuant != combo.EncQuant) {
		cb := combo
		a.models.switchTo, a.models.switchOpen = &cb, true
		return
	}
	a.run(func(ctx context.Context) error { return a.d.Runtime.Use(ctx, combo.Backend) })
}

// switchDialog asks before a precision change rebuilds the index in the background (UI guide section 3.7).
func (a *App) switchDialog(c *ui.Context) {
	m := &a.models
	if m.switchTo == nil || !m.switchOpen {
		return
	}
	combo := *m.switchTo
	msg := "This rebuilds the index in the background. Search keeps working with the current index until then."
	i := ui.AlertDialog(c, &m.switchOpen, "Switch precision", msg, "Cancel", "Rebuild")
	if !m.switchOpen {
		m.switchTo = nil
	}
	if i == 1 {
		m.switchTo = nil
		a.run(func(ctx context.Context) error {
			if _, err := a.d.Indexer.Rebuild(ctx, combo.TextQuant, combo.EncQuant); err != nil {
				return err
			}
			return a.d.Runtime.Use(ctx, combo.Backend)
		})
	}
}

// isActive reports whether combo is the one the active index serves.
func (a *App) isActive(combo contract.Combo) bool {
	g := a.activeGen()
	return g != nil && combo.Backend == a.runtime.Backend &&
		g.Space.TextQuant == combo.TextQuant && g.Space.EncQuant == combo.EncQuant
}

// activeGen returns the active generation of the snapshot, or nil.
func (a *App) activeGen() *contract.Generation { return a.data.active }

// comboLabel names a combination: "Vulkan · BF16 · Q8_0".
func comboLabel(c contract.Combo) string {
	return backendName(c.Backend) + " · " + precisionName(c.TextQuant) + " · " + precisionName(c.EncQuant)
}

// backendName names a backend for display.
func backendName(b contract.Backend) string {
	switch b {
	case contract.BackendVulkan:
		return "Vulkan"
	case contract.BackendCUDA12:
		return "CUDA 12"
	case contract.BackendCUDA13:
		return "CUDA 13"
	case contract.BackendCPU:
		return "CPU"
	}
	return string(b)
}

// benchCell formats one column of a test result. Quality n/a is TextMuted text; unknown numbers say so.
func benchCell(b *contract.BenchResult, col int) string {
	if b == nil {
		return "not tested"
	}
	switch col {
	case 1:
		return count(int(b.ImageMS + 0.5))
	case 2:
		return count(int(b.TextTokensPerSec + 0.5))
	case 3:
		return size(b.PeakRAM)
	case 4:
		if b.VRAM == 0 {
			return "—"
		}
		return size(b.VRAM)
	case 5:
		if b.Quality == nil {
			return "n/a"
		}
		return fmt.Sprintf("%.3f", *b.Quality)
	}
	return ""
}

// shortRev shortens a model revision to its first 7 characters.
func shortRev(rev string) string {
	if len(rev) > 7 {
		return rev[:7]
	}
	return rev
}

// startLab opens the test lab (onboarding step 2) over the Models page. The lab starts once detection ends.
func (a *App) startLab() {
	a.startOnboarding()
	a.onb.step, a.onb.fromModels = 2, true
}

// spaceErr returns the reason the first open space is on the CPU fallback, or "".
func spaceErr(st contract.RuntimeStatus) string {
	for _, sp := range st.Spaces {
		if sp.Err != "" {
			return sp.Err
		}
	}
	return ""
}

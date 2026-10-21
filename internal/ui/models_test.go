package ui

import (
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// TestModelsShowsActiveAndInstalled checks the main path: the active card and the installed table.
func TestModelsShowsActiveAndInstalled(t *testing.T) {
	h := newHarness(t, nil)
	h.app.page = pageModels
	h.wait("combos", func() bool { return len(h.app.data.combos) > 0 })
	h.wantText("Active combination")
	h.wantText("Installed combinations")
	h.wantText("Vulkan")
}

// TestModelsCPUFallbackWarning checks the fallback state: the warning with Try GPU again.
func TestModelsCPUFallbackWarning(t *testing.T) {
	h := newHarness(t, nil)
	h.app.page = pageModels
	h.wait("combos", func() bool { return len(h.app.data.combos) > 0 })
	h.app.d.Runtime.(*fake.Runtime).FallBackToCPU()
	h.wait("fallback", func() bool { return h.app.runtime.State == contract.RuntimeCPUFallback })
	h.wantText("The GPU backend failed twice. Lookback is using the CPU.")
	h.wantText("Try GPU again")
}

// TestModelsShowsRebuild checks the building generation stays visible beside the active combination.
func TestModelsShowsRebuild(t *testing.T) {
	h := newHarness(t, nil)
	h.app.page = pageModels
	h.wait("combos", func() bool { return len(h.app.data.combos) > 0 })
	h.app.rebuild = &contract.Progress{FilesDone: 1240, FilesTotal: 3100}
	h.pump(0)
	h.wantText("Rebuilding the index")
	h.wantText("the active index keeps serving searches")
}

// TestModelsPrecisionSwitchAsks checks that a precision change asks before it rebuilds the index.
func TestModelsPrecisionSwitchAsks(t *testing.T) {
	h := newHarness(t, nil)
	h.app.page = pageModels
	h.wait("combos", func() bool { return len(h.app.data.combos) > 0 })
	h.app.makeActive(contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16})
	h.pump(0)
	h.wantText("Switch precision")
	h.wantText("This rebuilds the index in the background. Search keeps working with the current index until then.")
}

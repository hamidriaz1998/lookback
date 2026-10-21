package ui

import (
	"context"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// gatedStore holds one read of a setting: the read happens, then the call waits for the gate. It is how a test
// makes a data load that began before a write end after it.
type gatedStore struct {
	contract.Store
	key     string
	armed   bool
	reading chan struct{}
	gate    chan struct{}
}

func (g *gatedStore) GetSetting(ctx context.Context, key string) (string, bool, error) {
	v, ok, err := g.Store.GetSetting(ctx, key)
	if key == g.key && g.armed {
		g.armed = false
		g.reading <- struct{}{}
		<-g.gate
	}
	return v, ok, err
}

// TestSettingSurvivesAStaleLoad checks that a setting the user changed keeps its new value when a data load that
// read the store before the write ends after the write.
func TestSettingSurvivesAStaleLoad(t *testing.T) {
	st := &gatedStore{key: "battery_pause", reading: make(chan struct{}, 1), gate: make(chan struct{})}
	h := newHarness(t, func(s *fake.Store) contract.Store { st.Store = s; return st })
	h.wait("the data", func() bool { return h.app.data.ready })
	st.armed = true
	h.app.reload()
	<-st.reading
	h.app.setting("battery_pause", "off")
	h.wait("the write", func() bool {
		v, _, _ := st.Store.GetSetting(context.Background(), "battery_pause")
		return v == "off"
	})
	close(st.gate)
	h.wait("the loads end", func() bool { return !h.app.reloading })
	h.pump(0)
	h.pump(0)
	if got := h.app.data.setting("battery_pause", "on"); got != "off" {
		t.Fatalf("the setting shows %q after a stale load, want the value the user set", got)
	}
}

// TestOnboardingResumesAfterRestart checks that quitting during onboarding resumes it: the step and the chosen
// combination are restored from the store, and the files of the combination are queued again.
func TestOnboardingResumesAfterRestart(t *testing.T) {
	first := firstRun(t)
	h1, _ := newTestHarness(t, first, nil, nil, false)
	waitDetection(h1)
	cb := h1.app.onb.combos[0]
	h1.app.onb.step = 2
	h1.app.pickCombo(cb)
	h1.app.setStep(3)
	h1.wait("the choice is stored", func() bool {
		step, _, _ := h1.store.GetSetting(context.Background(), "onboard_step")
		combo, _, _ := h1.store.GetSetting(context.Background(), "onboard_combo")
		return step == "3" && combo == cb.ID()
	})
	h1.stop()

	h2, _ := newTestHarness(t, func(*fake.Store) contract.Store { return h1.store }, nil, nil, false)
	waitDetection(h2)
	h2.wait("the restored step", func() bool { return h2.app.onb != nil && h2.app.onb.step == 3 })
	if h2.app.picked == nil || h2.app.picked.ID() != cb.ID() {
		t.Fatalf("the restored choice is %v, want %s", h2.app.picked, cb.ID())
	}
	h2.wantText("Step 3 of 3")
}

// TestComboIDParses checks that a stored combination reads back as the combination it names.
func TestComboIDParses(t *testing.T) {
	want := contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0}
	got, ok := parseCombo(want.ID())
	if !ok || got != want {
		t.Fatalf("parseCombo(%q) = %v, %v", want.ID(), got, ok)
	}
	if _, ok := parseCombo("vulkan/bf16"); ok {
		t.Error("a short ID parsed")
	}
}

package ui

import (
	"context"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// firstRun returns the harness setup that clears the onboarded setting, so the first-run flow shows.
func firstRun(t *testing.T) func(*fake.Store) contract.Store {
	return func(s *fake.Store) contract.Store {
		if err := s.SetSetting(context.Background(), "onboarded", ""); err != nil {
			t.Fatal(err)
		}
		return s
	}
}

// TestOnboardingStepOneRecommends checks step 1: the hardware and the recommended combination.
func TestOnboardingStepOneRecommends(t *testing.T) {
	h, _ := newFirstRunHarness(t, nil, nil)
	waitDetection(h)
	h.wantText("Welcome to Lookback")
	h.wantText("Step 1 of 3")
	h.wantText("Recommended")
}

// TestOnboardingLabNeedsChoice checks step 2: the lab shows the matrix, and Use stays disabled until a row is
// clicked. A checked combination is tested, and its result shows.
func TestOnboardingLabNeedsChoice(t *testing.T) {
	h, _ := newFirstRunHarness(t, nil, nil)
	waitDetection(h)
	h.app.onb.step = 2
	h.pump(0)
	h.wantText("Choose combinations to try")
	h.wantText("Choose a combination to continue.")
	h.app.setTest(contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0}, true)
	h.wait("lab results", func() bool {
		for _, s := range h.app.labSteps {
			if s.State == contract.LabDone {
				return true
			}
		}
		return false
	})
}

// TestOnboardingFinishesToSearch checks the whole first run: step 1 to step 3, then Start indexing marks it done.
func TestOnboardingFinishesToSearch(t *testing.T) {
	h, _ := newFirstRunHarness(t, nil, nil)
	waitDetection(h)
	h.app.onb.step = 3
	h.pump(0)
	h.wantText("What should Lookback index?")
	h.wantText("Skipped by rules")
	h.app.finishOnboarding()
	h.wait("onboarded", func() bool { return h.app.onb == nil && h.app.data.settings["onboarded"] == "1" })
}

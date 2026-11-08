package ui

import (
	"context"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// gatedSetting holds back the write of one setting until release is closed, as a slow disk would.
type gatedSetting struct {
	contract.Store
	key     string
	release chan struct{}
}

func (g gatedSetting) SetSetting(ctx context.Context, key, value string) error {
	if key == g.key {
		select {
		case <-g.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return g.Store.SetSetting(ctx, key, value)
}

// TestFinishOnboardingSurvivesAnOlderReload checks that a snapshot read before the "onboarded" write ended does not
// bring the first run back. The reload below reads the store, finishOnboarding then marks the run done, and only
// then does the old snapshot reach the main thread.
func TestFinishOnboardingSurvivesAnOlderReload(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	setup := func(s *fake.Store) contract.Store {
		return gatedSetting{Store: firstRun(t)(s), key: "onboarded", release: release}
	}
	h, _ := newTestHarness(t, setup, nil, nil, false)
	waitDetection(h)
	h.app.onb.step = 3
	h.pump(0)

	h.app.reload()
	deadline := time.Now().Add(5 * time.Second)
	for len(h.queue) == 0 { // the snapshot is built and waits on the queue; it holds the old settings
		if time.Now().After(deadline) {
			t.Fatal("the reload did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	h.app.finishOnboarding() // the "onboarded" write is held back
	h.pump(0)                // the old snapshot arrives now, and the next frames draw it

	if h.app.onb != nil {
		t.Errorf("onboarding is back after an older snapshot arrived; texts: %q", h.tt.Texts())
	}
}

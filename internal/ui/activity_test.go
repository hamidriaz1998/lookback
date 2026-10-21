package ui

import (
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// TestActivityShowsRunningJob checks the main path: the job's numbers, ETA and the folders.
func TestActivityShowsRunningJob(t *testing.T) {
	h := newHarness(t, nil)
	h.app.page = pageActivity
	h.wait("job", func() bool { return h.app.hasProgress })
	h.wantText("Indexing")
	h.wantText("files ·")
	h.wantText("ETA")
	h.wantText("/home/demo/Pictures")
}

// TestActivityErrorRetry checks the error state: the failed file is listed with Retry.
func TestActivityErrorRetry(t *testing.T) {
	h := newHarness(t, nil)
	h.app.page = pageActivity
	h.wait("errors", func() bool { return len(h.app.data.errors) > 0 })
	h.wantText("Errors (1)")
	h.wantText("report-old.pdf")
	h.wantText("Retry all")
}

// TestActivityBatteryPauseResumes checks the battery pause: its notice and the action that lifts it.
func TestActivityBatteryPauseResumes(t *testing.T) {
	h := newHarness(t, nil)
	h.app.page = pageActivity
	h.wait("job", func() bool { return h.app.hasProgress })
	h.app.d.Indexer.Pause(contract.PauseBattery)
	h.wait("battery notice", func() bool { return h.app.progress.Paused == contract.PauseBattery })
	h.wantText("Paused on battery power.")
	if err := h.tt.Click("Index anyway"); err != nil {
		t.Fatal(err)
	}
	h.wait("resumed", func() bool { return h.app.progress.Paused == contract.PauseNone })
}

package ui

import (
	"context"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// flaky is a store whose reads or writes fail while the matching flag is set.
type flaky struct {
	contract.Store
	failRead, failWrite, failList bool
}

func (f *flaky) GetSetting(ctx context.Context, key string) (string, bool, error) {
	if f.failRead {
		return "", false, testError("the settings are unreadable")
	}
	return f.Store.GetSetting(ctx, key)
}

func (f *flaky) SetSetting(ctx context.Context, key, value string) error {
	if f.failWrite {
		return testError("the disk is full")
	}
	return f.Store.SetSetting(ctx, key, value)
}

func (f *flaky) ListFolders(ctx context.Context) ([]contract.Folder, error) {
	if f.failList {
		return nil, testError("the folder list is unreadable")
	}
	return f.Store.ListFolders(ctx)
}

// TestFirstLoadFailureShowsRetry checks that an unreadable store does not open onboarding: the window says the data
// could not be read, and Retry reads it again.
func TestFirstLoadFailureShowsRetry(t *testing.T) {
	st := &flaky{failRead: true}
	h := newHarness(t, func(s *fake.Store) contract.Store { st.Store = s; return st })
	h.wait("the load failure", func() bool { return h.app.loadErr != nil })
	h.wantText("could not read its data")
	if h.app.onb != nil {
		t.Fatal("an unreadable store opened onboarding")
	}
	st.failRead = false
	h.tt.Click("Retry")
	h.wait("the data", func() bool { return h.app.data.ready })
	h.wantText("Search your files")
}

// TestLoadFailureKeepsLastGoodData checks that a failed refresh keeps the data already on screen and says so.
func TestLoadFailureKeepsLastGoodData(t *testing.T) {
	st := &flaky{}
	h := newHarness(t, func(s *fake.Store) contract.Store { st.Store = s; return st })
	h.wait("the data", func() bool { return h.app.data.ready && len(h.app.data.folders) > 0 })
	st.failList = true
	h.app.reload()
	h.wait("the refresh failure", func() bool { return h.app.loadErr != nil })
	if len(h.app.data.folders) == 0 {
		t.Fatal("a failed refresh dropped the folders on screen")
	}
	h.wantText("could not refresh its data")
}

// TestSettingWriteFailureIsShownAndRetried checks that a setting that did not save is shown on the Settings page,
// and that Retry saves it.
func TestSettingWriteFailureIsShownAndRetried(t *testing.T) {
	st := &flaky{}
	h := newHarness(t, func(s *fake.Store) contract.Store { st.Store = s; return st })
	h.wait("the data", func() bool { return h.app.data.ready })
	h.app.page = pageSettings
	st.failWrite = true
	h.app.setting("battery_pause", "off")
	h.wait("the notice", func() bool { return h.app.notice != nil })
	h.pump(0)
	h.wantText("Could not finish: the disk is full")
	st.failWrite = false
	h.tt.Click("Retry")
	h.wait("the setting saved", func() bool {
		v, _, _ := st.GetSetting(context.Background(), "battery_pause")
		return v == "off" && h.app.notice == nil
	})
}

// TestCommandFailureShowsOnItsPageAndRetries checks that a failed command is shown on the page it started on, and
// that Retry runs the command again.
func TestCommandFailureShowsOnItsPageAndRetries(t *testing.T) {
	h := newHarness(t, nil)
	h.wait("the data", func() bool { return h.app.data.ready })
	h.app.page = pageActivity
	calls := 0
	h.app.run(func(context.Context) error {
		calls++
		if calls == 1 {
			return testError("the folder is busy")
		}
		return nil
	})
	h.wait("the notice", func() bool { return h.app.notice != nil })
	h.pump(0)
	h.wantText("Could not finish: the folder is busy")
	h.tt.Click("Retry")
	h.wait("the retry", func() bool { return calls == 2 && h.app.notice == nil })
	h.app.page = pageSearch
	h.pump(0)
	for _, txt := range h.tt.Texts() {
		if txt == "Could not finish: the folder is busy" {
			t.Fatal("the notice shows on another page")
		}
	}
}

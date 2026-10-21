package fake

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// bf16Text is the BF16 text model of the fixture, which is already verified on disk.
func bf16Text(t *testing.T) contract.Asset {
	t.Helper()
	for _, a := range mustAssets(contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16}) {
		if a.Part == contract.PartText {
			return a
		}
	}
	t.Fatal("no text model in the fixture")
	return contract.Asset{}
}

// Assets are identified by SHA-256, so duplicate callers share one download, a new pin downloads
// again, and a conflicting descriptor is refused (review finding 3).
func TestEnsureIsKeyedBySHA(t *testing.T) {
	bg := context.Background()
	d := newDownloader()

	// Duplicate callers with different contexts: one entry, and the second call changes nothing.
	fresh := bf16Text(t)
	fresh.Name = "other-name.gguf"
	fresh.SHA256 = strings.Repeat("ab", 32)
	ctxA, cancelA := context.WithCancel(bg)
	if err := d.Ensure(ctxA, []contract.Asset{fresh}); err != nil {
		t.Fatal(err)
	}
	d.tick()
	cancelA() // the call's context ends; the download does not
	ctxB := bg
	if err := d.Ensure(ctxB, []contract.Asset{fresh}); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	df := d.files[fresh.Key()]
	count := len(d.order)
	state := d.state(df)
	d.mu.Unlock()
	if state != contract.FileDownloading || count != len(mustAssets(contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16}))+1 {
		t.Fatalf("duplicate Ensure changed the download: state %s, %d entries", state, count)
	}
	d.tick()
	d.mu.Lock()
	if d.files[fresh.Key()].done <= 0 {
		t.Fatal("the download stopped when the caller's context ended")
	}
	d.mu.Unlock()

	// A changed pin: same file name, new URL and SHA-256. It downloads; the old verified file stays done.
	old := bf16Text(t)
	pinned := old
	pinned.URL = old.URL + "?rev=next"
	pinned.SHA256 = strings.Repeat("cd", 32)
	if err := d.Ensure(bg, []contract.Asset{pinned}); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	if d.state(d.files[pinned.Key()]) != contract.FileQueued || d.state(d.files[old.Key()]) != contract.FileDone {
		t.Fatal("a new pin did not queue a new download beside the verified file")
	}
	d.mu.Unlock()

	// A conflict: same key, different descriptor. It is refused and the batch queues nothing.
	clash := old
	clash.URL = "https://example.com/elsewhere"
	extra := fresh
	extra.Name = "not-queued.gguf"
	extra.SHA256 = strings.Repeat("ef", 32)
	if err := d.Ensure(bg, []contract.Asset{extra, clash}); !errors.Is(err, contract.ErrConflict) {
		t.Fatalf("Ensure with a conflict: %v, want ErrConflict", err)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, queued := d.files[extra.Key()]; queued {
		t.Fatal("a refused Ensure queued another asset")
	}
	if d.files[old.Key()].asset.URL != old.URL {
		t.Fatal("a conflict replaced the tracked descriptor")
	}
}

// A repeated Ensure leaves each state as it is (the Downloader doc): queued, downloading, paused,
// offline and verifying keep their progress, done stays done, and failed stays failed until Retry.
func TestRepeatedEnsureKeepsEveryState(t *testing.T) {
	bg := context.Background()
	d := newDownloader()
	a := bf16Text(t)
	a.SHA256 = strings.Repeat("12", 32)
	if err := d.Ensure(bg, []contract.Asset{a}); err != nil {
		t.Fatal(err)
	}
	for _, st := range []contract.FileState{contract.FileQueued, contract.FileDownloading, contract.FilePaused,
		contract.FileOffline, contract.FileVerifying, contract.FileDone, contract.FileFailed} {
		d.mu.Lock()
		df := d.files[a.Key()]
		df.phase, df.userPaused, df.done = contract.FileDownloading, false, 7
		d.off = st == contract.FileOffline
		switch st {
		case contract.FilePaused:
			df.userPaused = true
		case contract.FileQueued:
			df.phase = st
		case contract.FileDownloading, contract.FileOffline:
		default:
			df.phase = st
		}
		d.mu.Unlock()
		if err := d.Ensure(bg, []contract.Asset{a}); err != nil {
			t.Fatal(err)
		}
		d.mu.Lock()
		got, done := d.state(df), df.done
		d.off = false
		d.mu.Unlock()
		if got != st || done != 7 {
			t.Fatalf("Ensure in state %s moved it to %s with %d bytes", st, got, done)
		}
	}
}

// The user's pause and the connection are independent (review finding 5). A file paused with PauseAll
// stays paused across a connection change, and bytes do not move until an explicit resume.
func TestPauseSurvivesConnectionChanges(t *testing.T) {
	bg := context.Background()
	d := newDownloader()
	a := bf16Text(t)
	a.SHA256 = strings.Repeat("34", 32)

	// A new file ensured during PauseAll starts paused and does not move.
	d.PauseAll()
	if err := d.Ensure(bg, []contract.Asset{a}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		d.tick()
	}
	d.mu.Lock()
	if st, done := d.state(d.files[a.Key()]), d.files[a.Key()].done; st != contract.FilePaused || done != 0 {
		t.Fatalf("new file during PauseAll: state %s with %d bytes, want paused with 0", st, done)
	}
	d.mu.Unlock()
	d.ResumeAll()
	d.tick()
	d.mu.Lock()
	if st := d.state(d.files[a.Key()]); st != contract.FileDownloading {
		t.Fatalf("after ResumeAll the file is %s, want downloading", st)
	}
	d.mu.Unlock()

	// Offline, then PauseAll, then back online: the file stays paused and its bytes do not change.
	d.SetOffline(true)
	d.PauseAll()
	d.SetOffline(false)
	d.mu.Lock()
	before := d.files[a.Key()].done
	if st := d.state(d.files[a.Key()]); st != contract.FilePaused {
		t.Fatalf("back online after PauseAll: state %s, want paused", st)
	}
	d.mu.Unlock()
	for i := 0; i < 5; i++ {
		d.tick()
	}
	d.mu.Lock()
	if d.files[a.Key()].done != before {
		t.Fatal("a paused file moved after the connection came back")
	}
	d.mu.Unlock()

	// Individual pause while offline: the file shows offline, and after the network returns it is still paused.
	d.ResumeAll()
	d.SetOffline(true)
	if err := d.Pause(a.Key()); err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	if st := d.state(d.files[a.Key()]); st != contract.FileOffline {
		t.Fatalf("offline paused file shows %s, want offline", st)
	}
	d.mu.Unlock()
	d.SetOffline(false)
	d.mu.Lock()
	if st := d.state(d.files[a.Key()]); st != contract.FilePaused {
		t.Fatalf("back online after an individual pause: %s, want paused", st)
	}
	d.mu.Unlock()
	if err := d.Resume(a.Key()); err != nil {
		t.Fatal(err)
	}
	d.tick()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.files[a.Key()].done <= before {
		t.Fatal("an explicit resume did not move the bytes")
	}
}

// Two descriptors with one new key in the same batch conflict in either order, and the batch queues nothing
// (review finding 8). Identical duplicates in one batch are not a conflict.
func TestSameBatchConflictQueuesNothing(t *testing.T) {
	bg := context.Background()
	base := bf16Text(t)
	base.SHA256 = strings.Repeat("9a", 32)
	other := base
	other.URL = base.URL + "?other"
	d := newDownloader()
	before := len(d.order)
	for _, batch := range [][]contract.Asset{{base, other}, {other, base}} {
		if err := d.Ensure(bg, batch); !errors.Is(err, contract.ErrConflict) {
			t.Fatalf("Ensure(%v): %v, want ErrConflict", batch, err)
		}
		d.mu.Lock()
		_, queued := d.files[base.Key()]
		n := len(d.order)
		d.mu.Unlock()
		if queued || n != before {
			t.Fatalf("a refused batch queued %d assets", n-before)
		}
	}
	if err := d.Ensure(bg, []contract.Asset{base, base}); err != nil {
		t.Fatalf("identical duplicates: %v", err)
	}
}

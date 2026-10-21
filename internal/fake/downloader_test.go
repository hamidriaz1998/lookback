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
	state := df.state
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
	if d.files[pinned.Key()].state != contract.FileQueued || d.files[old.Key()].state != contract.FileDone {
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
		d.files[a.Key()].state = st
		d.files[a.Key()].done = 7
		d.mu.Unlock()
		if err := d.Ensure(bg, []contract.Asset{a}); err != nil {
			t.Fatal(err)
		}
		d.mu.Lock()
		got, done := d.files[a.Key()].state, d.files[a.Key()].done
		d.mu.Unlock()
		if got != st || done != 7 {
			t.Fatalf("Ensure in state %s moved it to %s with %d bytes", st, got, done)
		}
	}
}

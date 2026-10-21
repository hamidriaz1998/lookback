package fake

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// A backend switch during a rebuild restarts the sidecars. Commits that meet the restart wait for the
// next tick, and every item that is stored has the vector of its own generation's space (review finding 1,
// indexing side).
func TestBackendSwitchDuringIndexingKeepsSpaces(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	rt := NewRuntime()
	x := newIndexer(s, rt, time.Second)
	life, stop := context.WithCancel(ctx)
	defer stop()
	if err := x.Start(life); err != nil {
		t.Fatal(err)
	}
	gen, err := x.Rebuild(ctx, contract.QuantQ8_0, contract.QuantQ8_0)
	if err != nil {
		t.Fatal(err)
	}

	var switching atomic.Bool
	switching.Store(true)
	done := make(chan error, 1)
	go func() {
		done <- rt.Use(ctx, cpuBF)
		switching.Store(false)
	}()
	stepsDuringSwitch := 0
	var final *contract.Progress
	for i := 0; i < 40000 && final == nil; i++ {
		if switching.Load() {
			stepsDuringSwitch++
		}
		_, rp := x.step()
		if rp != nil && rp.FilesDone == rp.FilesTotal {
			final = rp
		}
	}
	if err := <-done; err != nil {
		t.Fatalf("Use: %v", err)
	}
	if final == nil {
		t.Fatal("the rebuild never finished")
	}
	if stepsDuringSwitch == 0 {
		t.Fatal("no indexing step overlapped the backend switch")
	}

	// Every item of the building generation carries the vector of its space, not the other space's.
	spaceB := contract.VectorSpace{TextQuant: contract.QuantQ8_0, EncQuant: contract.QuantQ8_0, ModelRev: modelRev}
	wantSess, err := rt.Session(ctx, spaceB)
	if err != nil {
		t.Fatal(err)
	}
	defer wantSess.Close()
	otherSess, err := rt.Session(ctx, fixtureSpace)
	if err != nil {
		t.Fatal(err)
	}
	defer otherSess.Close()

	s.mu.Lock()
	var items []contract.Item
	for _, it := range s.items {
		if it.Gen == gen {
			items = append(items, it)
		}
	}
	s.mu.Unlock()
	if len(items) == 0 {
		t.Fatal("the building generation has no items")
	}
	for _, it := range items {
		key := fmt.Sprintf("file %d unit %d", it.FileID, it.Unit)
		want := embedKey(t, wantSess, key, it.Modality)
		other := embedKey(t, otherSess, key, it.Modality)
		if !reflect.DeepEqual(it.Vector, want) || reflect.DeepEqual(it.Vector, other) {
			t.Fatalf("item %d of file %d is not from the space of generation %d", it.ID, it.FileID, gen)
		}
	}
}

// embedKey embeds key the way the indexer does for an item of the given modality.
func embedKey(t *testing.T, sess contract.Session, key, modality string) []float32 {
	t.Helper()
	ctx := context.Background()
	if modality == "image" {
		v, _, err := sess.EmbedImage(ctx, []byte(key), "image/png")
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	vs, _, err := sess.EmbedTexts(ctx, []string{key})
	if err != nil {
		t.Fatal(err)
	}
	return vs[0]
}

// Start rejects a context that is already done, and changes nothing (review finding 8).
func TestStartRejectsCanceledContext(t *testing.T) {
	x := newIndexer(NewStore(), NewRuntime(), time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := x.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Start with a canceled context: %v", err)
	}
	life, stop := context.WithCancel(context.Background())
	defer stop()
	if err := x.Start(life); err != nil {
		t.Fatalf("Start after the rejected one: %v", err)
	}
}

// After the lifetime ends, every command returns ErrStopped and changes the store and the pauses nothing
// (review finding 8).
func TestCommandsAfterStopChangeNothing(t *testing.T) {
	s := NewStore()
	x := newIndexer(s, NewRuntime(), time.Second)
	life, stop := context.WithCancel(context.Background())
	if err := x.Start(life); err != nil {
		t.Fatal(err)
	}
	ch := x.Subscribe(context.Background())
	stop()
	for range ch { // the stream closes when the job has stopped
	}

	bg := context.Background()
	rowsBefore, _ := s.ListFileIndex(bg, 1)
	gensBefore, _ := s.ListGenerations(bg)
	filesBefore, _ := s.ListFiles(bg, 1)
	fileID := filesBefore[0].ID

	if err := x.Retry(bg, []int64{fileID}); !errors.Is(err, contract.ErrStopped) {
		t.Fatalf("Retry after stop: %v, want ErrStopped", err)
	}
	if err := x.RetryAll(bg); !errors.Is(err, contract.ErrStopped) {
		t.Fatalf("RetryAll after stop: %v, want ErrStopped", err)
	}
	if err := x.Rescan(bg, 1); !errors.Is(err, contract.ErrStopped) {
		t.Fatalf("Rescan after stop: %v, want ErrStopped", err)
	}
	if _, err := x.Rebuild(bg, contract.QuantQ8_0, contract.QuantQ8_0); !errors.Is(err, contract.ErrStopped) {
		t.Fatalf("Rebuild after stop: %v, want ErrStopped", err)
	}
	x.Pause(contract.PauseUser)
	if x.pauseReason() != contract.PauseNone {
		t.Fatal("Pause after stop took effect")
	}

	rowsAfter, _ := s.ListFileIndex(bg, 1)
	gensAfter, _ := s.ListGenerations(bg)
	if len(rowsAfter) != len(rowsBefore) || len(gensAfter) != len(gensBefore) {
		t.Fatal("a command after stop changed the store")
	}
	if ix, ok := s.fileIndexRow(fileID, 1); !ok || ix.Status != contract.StatusDone {
		t.Fatal("Retry after stop cleared the file's row")
	}
	if errs := s.errRows(1); len(errs) != 1 {
		t.Fatalf("RetryAll after stop changed the error rows: %d left", len(errs))
	}
}

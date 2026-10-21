package fake

import (
	"context"
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
	if err := x.Start(ctx); err != nil {
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

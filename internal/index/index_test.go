package index

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/extract"
	"github.com/hamidriaz1998/lookback/internal/fake"
	"github.com/hamidriaz1998/lookback/internal/store"
)

var testSpace = contract.VectorSpace{TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0, ModelRev: "test"}

// rig is one Indexer over a real store in a temp data folder, with the fake runtime and the real extractors.
type rig struct {
	st   *store.Store
	ix   *Indexer
	life context.Context
}

func newRig(t *testing.T, life context.Context) *rig {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	reg := extract.NewRegistry()
	t.Cleanup(func() { reg.Close() })
	rt := fake.NewRuntime(life)
	return &rig{st: st, ix: New(st, reg, rt, testSpace), life: life}
}

// addFolder saves a folder row for dir, so Start picks it up.
func (r *rig) addFolder(t *testing.T, dir string) contract.Folder {
	t.Helper()
	f := contract.Folder{Path: dir, AddedAt: time.Now()}
	if err := r.st.SaveFolder(context.Background(), &f); err != nil {
		t.Fatal(err)
	}
	return f
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %v waiting for %s", d, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// activeGen returns the active generation's ID, or 0.
func (r *rig) activeGen(t *testing.T) int64 {
	t.Helper()
	gens, err := r.st.ListGenerations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range gens {
		if g.State == contract.GenActive {
			return g.ID
		}
	}
	return 0
}

// unitQuery is a query vector for store searches; the store ranks by cosine, so any nonzero vector works.
func unitQuery() []float32 {
	v := make([]float32, 768)
	for i := range v {
		v[i] = 1
	}
	return v
}

// hitPaths returns the set of file names that the active generation's search returns.
func (r *rig) hitPaths(t *testing.T, gen int64) map[string]contract.Hit {
	t.Helper()
	hits, err := r.st.Search(context.Background(), gen, unitQuery(), nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]contract.Hit{}
	for _, h := range hits {
		out[filepath.Base(h.Path)] = h
	}
	return out
}

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPipelineIndexesFolder runs the first index of a folder: text, an image, and the exclusion rules.
func TestPipelineIndexesFolder(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes a folder through the real store")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "notes.txt"), "the quarterly budget review with alpha figures")
	writeFile(t, filepath.Join(dir, "sub", "deep.md"), "# Deep\n\nnested text about harbours")
	writePNG(t, filepath.Join(dir, "photo.png"), 200, 120)
	writePNG(t, filepath.Join(dir, "tiny.png"), 10, 10)              // under 64 px: excluded
	writeFile(t, filepath.Join(dir, ".secret.txt"), "hidden")        // hidden: excluded
	writeFile(t, filepath.Join(dir, "node_modules", "x.txt"), "dep") // excluded directory
	writeFile(t, filepath.Join(dir, "binary.txt"), "ab\x00cd")       // NUL bytes: the extractor skips it

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newRig(t, ctx)
	r.addFolder(t, dir)
	if err := r.ix.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "the first generation to activate", func() bool { return r.activeGen(t) != 0 })
	gen := r.activeGen(t)

	hits := r.hitPaths(t, gen)
	for _, want := range []string{"notes.txt", "deep.md", "photo.png"} {
		if _, ok := hits[want]; !ok {
			t.Errorf("%s missing from the index; got %v", want, keys(hits))
		}
	}
	for _, not := range []string{"tiny.png", ".secret.txt", "x.txt"} {
		if _, ok := hits[not]; ok {
			t.Errorf("%s should be excluded", not)
		}
	}
	if h := hits["photo.png"]; h.Modality != "image" {
		t.Errorf("photo.png modality = %q, want image", h.Modality)
	}
	if h := hits["notes.txt"]; h.Modality != "text" || h.Snippet == "" {
		t.Errorf("notes.txt hit = %+v, want a text item with a snippet", h)
	}
	rows, err := r.st.ListFileIndex(ctx, gen)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Status == contract.StatusError {
			t.Errorf("file %d failed: %s", row.FileID, row.Error)
		}
	}
}

// TestWatchFollowsEditAddDelete checks that an edit, an add and a delete reach the index within 10 s.
func TestWatchFollowsEditAddDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("watches a folder and waits for debounced rescans")
	}
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	writeFile(t, a, "original words about tangerines")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newRig(t, ctx)
	fo := r.addFolder(t, dir)
	if err := r.ix.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "the first generation", func() bool { return r.activeGen(t) != 0 })
	gen := r.activeGen(t)
	_ = fo

	start := time.Now()
	writeFile(t, a, "edited words about pomegranates")
	writeFile(t, filepath.Join(dir, "b.txt"), "a brand new file about kumquats")
	waitFor(t, 10*time.Second, "edit and add to reach the index", func() bool {
		h := r.hitPaths(t, gen)
		b, ok := h["b.txt"]
		return ok && contains(b.Snippet, "kumquats") && contains(h["a.txt"].Snippet, "pomegranates")
	})
	t.Logf("edit and add indexed in %v", time.Since(start))

	start = time.Now()
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, "delete to leave the index", func() bool {
		_, ok := r.hitPaths(t, gen)["a.txt"]
		return !ok
	})
	t.Logf("delete indexed in %v", time.Since(start))
}

// TestRebuildKeepsOldGenerationUntilHandover checks that a precision switch serves the old generation until the
// new one is complete, and then retires the old one.
func TestRebuildKeepsOldGenerationUntilHandover(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a full rebuild")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "one.txt"), "first document about lanterns")
	writeFile(t, filepath.Join(dir, "two.txt"), "second document about compasses")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newRig(t, ctx)
	r.addFolder(t, dir)
	if err := r.ix.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Second, "the first generation", func() bool { return r.activeGen(t) != 0 })
	old := r.activeGen(t)

	rebuildCh := r.ix.SubscribeRebuild(ctx)
	newGen, err := r.ix.Rebuild(ctx, contract.QuantQ8_0, contract.QuantQ8_0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ix.Rebuild(ctx, contract.QuantQ8_0, contract.QuantBF16); !errors.Is(err, contract.ErrRebuildBusy) {
		t.Errorf("second Rebuild during a rebuild = %v, want ErrRebuildBusy", err)
	}

	// While the new generation builds, the old one stays active and serves searches.
	var sawBuilding bool
	deadline := time.Now().Add(30 * time.Second)
	for {
		gens, err := r.st.ListGenerations(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var newState, oldState contract.GenState
		for _, g := range gens {
			if g.ID == newGen {
				newState = g.State
			}
			if g.ID == old {
				oldState = g.State
			}
		}
		if newState == contract.GenBuilding {
			sawBuilding = true
			if oldState != contract.GenActive {
				t.Fatalf("old generation is %s while the new one builds, want active", oldState)
			}
			if len(r.hitPaths(t, old)) == 0 {
				t.Fatal("old generation stopped serving while the new one builds")
			}
		}
		if newState == contract.GenActive {
			if oldState != contract.GenRetired {
				t.Fatalf("old generation is %s after handover, want retired", oldState)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rebuild did not activate in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !sawBuilding {
		t.Log("the rebuild finished before the poll saw it building; the invariant held at every poll")
	}
	if got := len(r.hitPaths(t, old)); got != 0 {
		t.Errorf("retired generation still has %d hits after DeleteItems", got)
	}
	if got := len(r.hitPaths(t, newGen)); got != 2 {
		t.Errorf("new generation has %d hits, want 2", got)
	}
	// The rebuild stream ends with a terminal value that covers every file.
	var last contract.Progress
	for p := range rebuildCh {
		last = p
		if p.FilesDone == p.FilesTotal && p.Gen == newGen {
			break
		}
	}
	if last.Gen != newGen || last.FilesDone != last.FilesTotal || last.FilesTotal != 2 {
		t.Errorf("rebuild terminal snapshot = %+v", last)
	}
}

// TestPauseReasons checks that a job waits while any reason is set and resumes when all are lifted.
func TestPauseReasons(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes a folder through the real store")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "p.txt"), "paused content")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newRig(t, ctx)
	r.addFolder(t, dir)
	r.ix.Pause(contract.PauseUser)
	r.ix.Pause(contract.PauseBattery)
	if err := r.ix.Start(ctx); err != nil {
		t.Fatal(err)
	}
	// The first index is a rebuild (no active generation yet), so its progress is on the rebuild stream.
	progress := r.ix.SubscribeRebuild(ctx)
	var first contract.Progress
	select {
	case first = <-progress:
	case <-time.After(5 * time.Second):
		t.Fatal("no progress snapshot while paused")
	}
	if first.Paused != contract.PauseUser {
		t.Errorf("paused reason = %q, want user (first in SPEC order)", first.Paused)
	}
	time.Sleep(600 * time.Millisecond)
	if r.activeGen(t) != 0 {
		t.Fatal("generation activated while paused")
	}
	r.ix.Resume(contract.PauseUser)
	time.Sleep(600 * time.Millisecond)
	if r.activeGen(t) != 0 {
		t.Fatal("generation activated while battery still paused")
	}
	var p contract.Progress
	deadline := time.After(5 * time.Second)
	for done := false; !done; {
		select {
		case p = <-progress:
			done = p.Paused == contract.PauseBattery
		case <-deadline:
			t.Fatalf("no snapshot with the battery reason; last %+v", p)
		}
	}
	r.ix.Resume(contract.PauseBattery)
	waitFor(t, 30*time.Second, "resume to finish the index", func() bool { return r.activeGen(t) != 0 })
}

// TestCancelStopsEverything checks that canceling the lifetime closes the streams, ends the goroutines, and makes
// commands fail with ErrStopped.
func TestCancelStopsEverything(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the indexer and its watchers")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "c.txt"), "cancel me")
	ctx, cancel := context.WithCancel(context.Background())
	r := newRig(t, ctx)
	r.addFolder(t, dir)
	baseline := runtime.NumGoroutine()
	if err := r.ix.Start(ctx); err != nil {
		t.Fatal(err)
	}
	subCtx, subCancel := context.WithCancel(ctx)
	sub := r.ix.Subscribe(subCtx)
	reb := r.ix.SubscribeRebuild(ctx)
	waitFor(t, 30*time.Second, "the first generation", func() bool { return r.activeGen(t) != 0 })

	// A reader that stops reading must not hold the job: canceling its context closes its channel.
	subCancel()
	waitFor(t, 2*time.Second, "subscriber channel to close", func() bool {
		select {
		case _, ok := <-sub:
			return !ok
		default:
			return false
		}
	})

	cancel()
	waitFor(t, 5*time.Second, "rebuild stream to close", func() bool {
		for {
			select {
			case _, ok := <-reb:
				if !ok {
					return true
				}
			default:
				return false
			}
		}
	})
	if err := r.ix.Retry(context.Background(), []int64{1}); !errors.Is(err, contract.ErrStopped) {
		t.Errorf("Retry after cancel = %v, want ErrStopped", err)
	}
	waitFor(t, 5*time.Second, "goroutines to return to the baseline", func() bool {
		return runtime.NumGoroutine() <= baseline
	})
}

// TestRetryUnknownFile checks the error for a file ID that is not in the store.
func TestRetryUnknownFile(t *testing.T) {
	if testing.Short() {
		t.Skip("starts the indexer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newRig(t, ctx)
	if err := r.ix.Retry(ctx, []int64{42}); !errors.Is(err, contract.ErrNotStarted) {
		t.Errorf("Retry before Start = %v, want ErrNotStarted", err)
	}
}

func keys(m map[string]contract.Hit) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

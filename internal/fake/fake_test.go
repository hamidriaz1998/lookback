package fake

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// The UI depends on this fixture: 300 indexed screenshots, one failed PDF, 101 pending files, and
// two installed combinations with test results.
func TestStoreFixture(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	idx, _ := s.ListFileIndex(ctx, 1)
	var done, failed int
	for _, r := range idx {
		switch r.Status {
		case contract.StatusDone:
			done++
		case contract.StatusError:
			failed++
		}
	}
	if done != 300 || failed != 1 {
		t.Fatalf("file_index: %d done, %d error; want 300 and 1", done, failed)
	}
	combos, _ := s.ListCombos(ctx)
	if len(combos) != 2 || combos[0].Bench == nil || combos[1].Bench == nil {
		t.Fatalf("combos: got %d, want 2 with bench results", len(combos))
	}
	hits, err := s.Search(ctx, 1, unitVector(7), nil, 60)
	if err != nil || len(hits) != 60 {
		t.Fatalf("search: %d hits, err %v; want 60", len(hits), err)
	}
	for i := 1; i < len(hits); i++ {
		if hits[i].Score > hits[i-1].Score {
			t.Fatal("search results are not sorted by score")
		}
	}
}

// RecentItems returns only indexed files, and filters them before the limit, even though the
// 101 newer pending files are not indexed (review finding 8).
func TestRecentItemsAreIndexedOnly(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	recent, err := s.RecentItems(ctx, 1, 24)
	if err != nil || len(recent) != 24 {
		t.Fatalf("recent: %d hits, err %v; want 24", len(recent), err)
	}
	for _, h := range recent {
		if h.ItemID == 0 {
			t.Fatalf("hit %s has no item ID", h.Path)
		}
		if strings.Contains(h.Path, "meeting-notes") || strings.Contains(h.Path, "hosting-invoice") {
			t.Fatalf("pending file %s listed as recent", h.Path)
		}
	}
	first, _ := s.fileByID(recent[0].FileID)
	second, _ := s.fileByID(recent[1].FileID)
	if first.MTime < second.MTime {
		t.Fatal("recent items are not ordered by modification time")
	}
}

// CommitFile allocates item IDs and ignores the caller's (review finding 5). A rejected commit to a
// building generation leaves the active results intact.
func TestCommitFileAllocatesAndIsAtomic(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	gen2 := contract.Generation{TextQuant: contract.QuantQ8_0, EncQuant: contract.QuantQ8_0}
	if err := s.NewGeneration(ctx, &gen2); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Search(ctx, 1, unitVector(3), nil, 300)

	// A caller that reuses item 1 for file 2 in gen 2 must not overwrite gen 1's item 1.
	ix, items := simulate(indexedFiles()[1], gen2.ID)
	items[0].ID = 1
	if err := s.CommitFile(ctx, ix, items); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Search(ctx, 1, unitVector(3), nil, 300)
	if len(after) != len(before) {
		t.Fatalf("active hits changed from %d to %d after a building commit", len(before), len(after))
	}

	// A mismatched item must fail the whole commit and change nothing.
	bad := items[0]
	bad.FileID = 999
	ix1, items1 := simulate(indexedFiles()[2], gen2.ID)
	items1 = append(items1, bad)
	rowsBefore, _ := s.ListFileIndex(ctx, gen2.ID)
	if err := s.CommitFile(ctx, ix1, items1); err == nil {
		t.Fatal("mismatched item was accepted")
	}
	rowsAfter, _ := s.ListFileIndex(ctx, gen2.ID)
	if len(rowsAfter) != len(rowsBefore) {
		t.Fatal("a rejected commit changed file_index")
	}
}

// Writes with a done context change nothing (review finding 13).
func TestCanceledWritesChangeNothing(t *testing.T) {
	s := NewStore()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := indexedFiles()[0]
	if err := s.DeleteFile(ctx, f.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteFile err %v, want context.Canceled", err)
	}
	if err := s.ActivateGeneration(ctx, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("ActivateGeneration err %v", err)
	}
	live := context.Background()
	if files, _ := s.ListFiles(live, 1); len(files) != imageCount+pendingPictures {
		t.Fatalf("canceled DeleteFile removed files: %d left", len(files))
	}
	if hits, _ := s.Search(live, 1, unitVector(1), nil, 300); len(hits) != 300 {
		t.Fatalf("search returned %d hits after canceled writes", len(hits))
	}
}

// ActivateGeneration leaves at most one active generation, and the previous one is retired.
func TestActivateGenerationIsSingle(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	g := contract.Generation{TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16}
	if err := s.NewGeneration(ctx, &g); err != nil {
		t.Fatal(err)
	}
	if err := s.ActivateGeneration(ctx, g.ID); err != nil {
		t.Fatal(err)
	}
	gens, _ := s.ListGenerations(ctx)
	var active []int64
	for _, x := range gens {
		if x.State == contract.GenActive {
			active = append(active, x.ID)
		}
	}
	if len(active) != 1 || active[0] != g.ID {
		t.Fatalf("active generations %v, want only %d", active, g.ID)
	}
	if err := s.DeleteItems(ctx, 1); err != nil {
		t.Fatalf("retired generation could not be deleted: %v", err)
	}
	if err := s.DeleteItems(ctx, g.ID); err == nil {
		t.Fatal("the active generation was deleted")
	}
}

// Similar returns exactly 10 rows for a valid seed, and ErrStaleItem for a seed from another generation
// (review findings 10 and 15).
func TestSimilarStaleAndCount(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	rows, err := s.Similar(ctx, 1, 1, nil, 10)
	if err != nil || len(rows) != 10 {
		t.Fatalf("similar: %d rows, err %v; want 10", len(rows), err)
	}
	gen2 := contract.Generation{TextQuant: contract.QuantQ8_0, EncQuant: contract.QuantQ8_0}
	_ = s.NewGeneration(ctx, &gen2)
	ix, items := simulate(indexedFiles()[0], gen2.ID)
	_ = s.CommitFile(ctx, ix, items)
	seed := int64(0)
	for _, it := range s.items {
		if it.Gen == gen2.ID {
			seed = it.ID
		}
	}
	if _, err := s.Similar(ctx, 1, seed, nil, 10); !errors.Is(err, contract.ErrStaleItem) {
		t.Fatalf("stale seed gave %v, want ErrStaleItem", err)
	}
}

// The fake tokenizer emits byte-fallback tokens that split a character, and the pieces rebuild the text.
func TestByteFallbackTokenizer(t *testing.T) {
	text := "ꙮ x"
	toks, err := NewByteFallbackEmbedder().Tokenize(context.Background(), text)
	if err != nil {
		t.Fatal(err)
	}
	if len(toks) != 4 {
		t.Fatalf("got %d tokens, want 4 (3 bytes of ꙮ, then x)", len(toks))
	}
	var rebuilt strings.Builder
	for i, tk := range toks {
		if text[tk.Start:tk.End] != tk.Piece {
			t.Fatalf("token %d: piece %q does not match text[%d:%d]", i, tk.Piece, tk.Start, tk.End)
		}
		rebuilt.WriteString(tk.Piece)
	}
	if rebuilt.String() != "ꙮx" {
		t.Fatalf("pieces rebuild %q", rebuilt.String())
	}
	if toks[0].End == len(text) || toks[1].Start != toks[0].End {
		t.Fatal("the first character's bytes do not form separate tokens")
	}
}

// The indexer's totals match the store, and as files complete, their rows and items appear in the store.
func TestIndexerCompletesIntoStore(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	x := newIndexer(s, time.Second)
	if err := x.Start(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := x.step()
	if p.FilesTotal != 402 || p.FilesDone != 301 || p.Errors != 1 {
		t.Fatalf("start: %d/%d done, %d errors; want 301/402 and 1", p.FilesDone, p.FilesTotal, p.Errors)
	}
	if p.ETA >= 0 {
		t.Fatalf("ETA %v while estimating", p.ETA)
	}

	var last contract.Progress
	for i := 0; i < 2600; i++ { // enough simulated seconds for every queued file
		last, _ = x.step()
	}
	if last.FilesDone != last.FilesTotal || last.ETA != 0 {
		t.Fatalf("job did not finish: %d/%d, ETA %v", last.FilesDone, last.FilesTotal, last.ETA)
	}
	rows, _ := s.ListFileIndex(ctx, 1)
	if len(rows) != last.FilesDone {
		t.Fatalf("store has %d rows, progress says %d", len(rows), last.FilesDone)
	}
	var pdfOK, pdfErr bool
	for _, r := range rows {
		f, _ := s.fileByID(r.FileID)
		if f.Path == invoicePDF && r.Status == contract.StatusDone {
			pdfOK = true
		}
		if f.Path == brokenPDF && r.Status == contract.StatusError {
			pdfErr = true
		}
	}
	if !pdfOK || !pdfErr {
		t.Fatal("the successful PDF or the failed PDF is missing from the store")
	}
	hits, _ := s.Search(ctx, 1, unitVector(9), []contract.Kind{contract.KindPDF}, 10)
	if len(hits) != 1 || hits[0].Path != invoicePDF {
		t.Fatalf("PDF search returned %d hits", len(hits))
	}
	// The scanned page of the invoice is an image item, while its Kind stays pdf.
	var imagePages int
	for _, it := range s.items {
		if it.Gen == 1 && it.FileID == hits[0].FileID && it.Modality == "image" {
			imagePages++
		}
	}
	if imagePages != 1 {
		t.Fatalf("invoice has %d image items, want 1", imagePages)
	}
}

// The ETA counts down steadily once estimating ends.
func TestIndexerETACountsDown(t *testing.T) {
	ctx := context.Background()
	x := newIndexer(NewStore(), time.Second)
	_ = x.Start(ctx)
	var etas []time.Duration
	for i := 0; i < 200; i++ {
		p, _ := x.step()
		if i == 10 && p.ETA >= 0 {
			t.Fatalf("ETA %v at 10 s, want negative", p.ETA)
		}
		if i >= 40 && i%20 == 0 {
			etas = append(etas, p.ETA)
		}
	}
	for i := 1; i < len(etas); i++ {
		if etas[i] >= etas[i-1] {
			t.Fatalf("ETA did not count down: %v then %v", etas[i-1], etas[i])
		}
	}
}

// A rebuild writes a second generation while the active job keeps running. Both streams report
// their own generation, and the rebuild ends by activating the new generation and deleting the old items.
func TestRebuildRunsBesideActiveJob(t *testing.T) {
	ctx := context.Background()
	s := NewStore()
	x := newIndexer(s, time.Second)
	if _, err := x.Rebuild(ctx, contract.QuantBF16, contract.QuantBF16); !errors.Is(err, contract.ErrNotStarted) {
		t.Fatalf("Rebuild before Start: %v", err)
	}
	_ = x.Start(ctx)
	gen, err := x.Rebuild(ctx, contract.QuantBF16, contract.QuantBF16)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.Rebuild(ctx, contract.QuantQ8_0, contract.QuantQ8_0); !errors.Is(err, contract.ErrRebuildBusy) {
		t.Fatalf("second Rebuild: %v, want ErrRebuildBusy", err)
	}
	var activeBefore, activeAfter int
	for i := 0; i < 200; i++ { // at half of 96 tokens per second, about four files finish
		p, rp := x.step()
		if i == 0 {
			activeBefore = p.FilesDone
		}
		if rp == nil || rp.Gen != gen {
			t.Fatal("no rebuild snapshot for the building generation")
		}
		if p.Gen != 1 {
			t.Fatalf("active snapshot describes generation %d, want 1", p.Gen)
		}
		activeAfter = p.FilesDone
	}
	if activeAfter <= activeBefore {
		t.Fatal("the active job did not progress during the rebuild")
	}

	// An update to the active generation during the rebuild: retry the broken PDF.
	broken := indexedFiles()[imageCount]
	if err := x.Retry(ctx, []int64{broken.ID}); err != nil {
		t.Fatal(err)
	}

	var final *contract.Progress
	for i := 0; i < 20000 && final == nil; i++ {
		_, rp := x.step()
		if rp != nil && rp.FilesDone == rp.FilesTotal {
			final = rp
		}
	}
	if final == nil {
		t.Fatal("the rebuild never finished")
	}
	gens, _ := s.ListGenerations(ctx)
	for _, g := range gens {
		if g.ID == gen && g.State != contract.GenActive {
			t.Fatalf("rebuilt generation state %s, want active", g.State)
		}
		if g.ID == 1 && g.State != contract.GenRetired {
			t.Fatalf("old generation state %s, want retired", g.State)
		}
	}
	if rows, _ := s.ListFileIndex(ctx, 1); len(rows) != 0 {
		t.Fatalf("retired generation keeps %d rows", len(rows))
	}
	if hits, _ := s.Search(ctx, gen, unitVector(5), nil, 300); len(hits) != 300 {
		t.Fatalf("new generation search returned %d hits, want 300", len(hits))
	}
}

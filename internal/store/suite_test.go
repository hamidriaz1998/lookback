package store

import (
	"context"
	"errors"
	"math/rand"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
)

// The contract suite runs against the Turso store and against the fake. It uses only the contract API and
// makes no assumption about IDs or fixtures, so an empty store from fresh starts every subtest.

type freshFunc func(t *testing.T) contract.Store

// runSuite runs every contract case on a new store from fresh.
func runSuite(t *testing.T, fresh freshFunc) {
	cases := []struct {
		name string
		fn   func(*testing.T, contract.Store)
	}{
		{"settings", caseSettings},
		{"folders and files", caseFoldersAndFiles},
		{"recent items are indexed only", caseRecentItems},
		{"commit allocates items and is atomic", caseCommitAllocates},
		{"canceled writes change nothing", caseCanceledWrites},
		{"activate keeps one active generation", caseActivateSingle},
		{"similar excludes its file and refuses stale seeds", caseSimilar},
		{"search filters by kind and orders by score", caseSearchKinds},
		{"store owns its values", caseOwnsValues},
		{"activate refuses an incomplete generation", caseActivateIncomplete},
		{"activate refuses a retired generation", caseActivateRetired},
		{"commit refuses a bad status or item", caseCommitRefuses},
		{"unknown IDs", caseUnknownIDs},
		{"clear file index and delete items", caseClearAndDelete},
		{"combos round trip", caseCombos},
		{"deleting a folder removes its rows", caseDeleteFolder},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.fn(t, fresh(t)) })
	}
}

// TestStoreContract runs the suite against the Turso store.
func TestStoreContract(t *testing.T) {
	runSuite(t, func(t *testing.T) contract.Store {
		s, err := Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	})
}

// TestFakeContract runs the same suite against the fake, emptied of its fixture folders, so the fake is held to the
// same contract. A failure here means the fake and the Turso store differ.
func TestFakeContract(t *testing.T) {
	runSuite(t, func(t *testing.T) contract.Store {
		s := fake.NewStore()
		ctx := context.Background()
		folders, _ := s.ListFolders(ctx)
		for _, f := range folders {
			if err := s.DeleteFolder(ctx, f.ID); err != nil {
				t.Fatal(err)
			}
		}
		return s
	})
}

var bg = context.Background()

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func addFolder(t *testing.T, s contract.Store, path string) contract.Folder {
	t.Helper()
	f := contract.Folder{Path: path, AddedAt: time.Unix(1_700_000_000, 0)}
	must(t, s.SaveFolder(bg, &f))
	return f
}

func addFile(t *testing.T, s contract.Store, folder int64, name string, kind contract.Kind, mtime int64) contract.FileRec {
	t.Helper()
	f := contract.FileRec{FolderID: folder, Path: "/suite/" + name, Kind: kind, Size: 1000 + mtime%997, MTime: mtime}
	must(t, s.SaveFile(bg, &f))
	return f
}

func newGen(t *testing.T, s contract.Store) contract.Generation {
	t.Helper()
	g := contract.Generation{Space: contract.VectorSpace{TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0, ModelRev: "rev-a"}}
	must(t, s.NewGeneration(bg, &g))
	return g
}

// vecFor returns a deterministic unit vector for seed.
func vecFor(seed int64) []float32 { return randUnit(rand.New(rand.NewSource(seed))) }

// commitOne indexes f in gen with one item whose vector is vecFor(seed). Images get no byte range.
func commitOne(t *testing.T, s contract.Store, gen int64, f contract.FileRec, seed int64) {
	t.Helper()
	ix := contract.FileIndex{FileID: f.ID, Gen: gen, Size: f.Size, MTime: f.MTime, Status: contract.StatusDone, Tokens: 10, Ms: 5}
	it := contract.Item{Gen: gen, FileID: f.ID, Unit: 0, Start: 0, End: 10, Modality: "text", Snippet: f.Path, Vector: vecFor(seed)}
	if f.Kind == contract.KindImage {
		it.Modality, it.Start, it.End = "image", -1, -1
	}
	must(t, s.CommitFile(bg, ix, []contract.Item{it}))
}

// fillGen indexes every tracked file in gen, so the generation covers every file.
func fillGen(t *testing.T, s contract.Store, gen int64) {
	t.Helper()
	folders, err := s.ListFolders(bg)
	must(t, err)
	for _, fo := range folders {
		files, err := s.ListFiles(bg, fo.ID)
		must(t, err)
		for _, f := range files {
			commitOne(t, s, gen, f, f.ID)
		}
	}
}

// activeGens returns the IDs of the generations in state active.
func activeGens(t *testing.T, s contract.Store) []int64 {
	t.Helper()
	gens, err := s.ListGenerations(bg)
	must(t, err)
	var out []int64
	for _, g := range gens {
		if g.State == contract.GenActive {
			out = append(out, g.ID)
		}
	}
	return out
}

// itemIDs maps each file of gen to its first item, as RecentItems reports it.
func itemIDs(t *testing.T, s contract.Store, gen int64) map[int64]int64 {
	t.Helper()
	hits, err := s.RecentItems(bg, gen, 100000)
	must(t, err)
	out := map[int64]int64{}
	for _, h := range hits {
		out[h.FileID] = h.ItemID
	}
	return out
}

func caseSettings(t *testing.T, s contract.Store) {
	if _, ok, err := s.GetSetting(bg, "suite.unset"); err != nil || ok {
		t.Fatalf("unset key: ok=%v err=%v", ok, err)
	}
	must(t, s.SetSetting(bg, "hotkey", "Super+K"))
	must(t, s.SetSetting(bg, "hotkey", "Super+J"))
	v, ok, err := s.GetSetting(bg, "hotkey")
	if err != nil || !ok || v != "Super+J" {
		t.Fatalf("GetSetting = %q %v %v, want Super+J", v, ok, err)
	}
}

func caseFoldersAndFiles(t *testing.T, s contract.Store) {
	f := addFolder(t, s, "/suite/pics")
	if f.ID == 0 || f.AddedAt.IsZero() {
		t.Fatalf("SaveFolder did not set ID and AddedAt: %+v", f)
	}
	f.Ignore = "*.tmp"
	must(t, s.SaveFolder(bg, &f))
	folders, _ := s.ListFolders(bg)
	if len(folders) != 1 || folders[0].Ignore != "*.tmp" || folders[0].ID != f.ID {
		t.Fatalf("ListFolders after update: %+v", folders)
	}
	const mtime = 1_700_000_000_123_456_789 // nanoseconds survive the round trip
	a := addFile(t, s, f.ID, "a.png", contract.KindImage, mtime)
	again := contract.FileRec{FolderID: f.ID, Path: a.Path, Kind: contract.KindPDF, Size: 5, MTime: mtime + 1}
	must(t, s.SaveFile(bg, &again))
	if again.ID != a.ID {
		t.Fatalf("SaveFile by path gave ID %d, want %d", again.ID, a.ID)
	}
	files, _ := s.ListFiles(bg, f.ID)
	if len(files) != 1 || files[0].MTime != mtime+1 || files[0].Kind != contract.KindPDF {
		t.Fatalf("ListFiles: %+v", files)
	}
	addFile(t, s, f.ID, "b.txt", contract.KindText, mtime)
	counts, _ := s.CountFiles(bg, f.ID)
	if counts[contract.KindPDF] != 1 || counts[contract.KindText] != 1 || counts[contract.KindImage] != 0 {
		t.Fatalf("CountFiles: %v", counts)
	}
}

func caseRecentItems(t *testing.T, s contract.Store) {
	fo := addFolder(t, s, "/suite/docs")
	g := newGen(t, s)
	old := addFile(t, s, fo.ID, "old.txt", contract.KindText, 100)
	failed := addFile(t, s, fo.ID, "broken.pdf", contract.KindPDF, 400)
	newest := addFile(t, s, fo.ID, "new.txt", contract.KindText, 300)
	addFile(t, s, fo.ID, "pending.txt", contract.KindText, 900) // no row in g: not listed
	commitOne(t, s, g.ID, old, 1)
	commitOne(t, s, g.ID, newest, 2)
	must(t, s.CommitFile(bg, contract.FileIndex{FileID: failed.ID, Gen: g.ID, Size: failed.Size, MTime: failed.MTime,
		Status: contract.StatusError, Error: "encrypted"}, nil))

	hits, err := s.RecentItems(bg, g.ID, 1)
	if err != nil || len(hits) != 1 || hits[0].FileID != newest.ID {
		t.Fatalf("RecentItems limit 1: %+v, err %v; want the newest done file", hits, err)
	}
	hits, _ = s.RecentItems(bg, g.ID, 10)
	if len(hits) != 2 || hits[0].FileID != newest.ID || hits[1].FileID != old.ID {
		t.Fatalf("RecentItems: %+v; want new then old, without the error or pending file", hits)
	}
	for _, h := range hits {
		if h.ItemID == 0 {
			t.Fatalf("hit %s has no item ID", h.Path)
		}
	}
}

func caseCommitAllocates(t *testing.T, s contract.Store) {
	fo := addFolder(t, s, "/suite/pics")
	g1 := newGen(t, s)
	a := addFile(t, s, fo.ID, "a.png", contract.KindImage, 1)
	b := addFile(t, s, fo.ID, "b.png", contract.KindImage, 2)
	commitOne(t, s, g1.ID, a, 10)
	first := itemIDs(t, s, g1.ID)
	// Item IDs are the store's: a caller's ID is ignored and a second commit gets a new ID.
	ix := contract.FileIndex{FileID: b.ID, Gen: g1.ID, Size: b.Size, MTime: b.MTime, Status: contract.StatusDone}
	must(t, s.CommitFile(bg, ix, []contract.Item{{ID: first[a.ID], Gen: g1.ID, FileID: b.ID, Modality: "image", Start: -1, End: -1, Vector: vecFor(11)}}))
	second := itemIDs(t, s, g1.ID)
	if second[b.ID] == 0 || second[b.ID] == first[a.ID] {
		t.Fatalf("item IDs: a=%d b=%d, want b to get its own ID", first[a.ID], second[b.ID])
	}
	// A commit to a building generation leaves the active results as they were.
	g2 := newGen(t, s)
	before, _ := s.Search(bg, g1.ID, vecFor(10), nil, 10)
	commitOne(t, s, g2.ID, b, 12)
	after, _ := s.Search(bg, g1.ID, vecFor(10), nil, 10)
	if len(after) != len(before) || len(after) != 2 {
		t.Fatalf("active hits changed from %d to %d after a building commit", len(before), len(after))
	}
}

func caseCanceledWrites(t *testing.T, s contract.Store) {
	fo := addFolder(t, s, "/suite/pics")
	g := newGen(t, s)
	f := addFile(t, s, fo.ID, "a.png", contract.KindImage, 1)
	commitOne(t, s, g.ID, f, 1)
	ctx, cancel := context.WithCancel(bg)
	cancel()
	if err := s.DeleteFile(ctx, f.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("DeleteFile err %v, want context.Canceled", err)
	}
	if err := s.ActivateGeneration(ctx, g.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("ActivateGeneration err %v, want context.Canceled", err)
	}
	if err := s.CommitFile(ctx, contract.FileIndex{FileID: f.ID, Gen: g.ID, Status: contract.StatusDone}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("CommitFile err %v, want context.Canceled", err)
	}
	if files, _ := s.ListFiles(bg, fo.ID); len(files) != 1 {
		t.Fatalf("canceled DeleteFile removed the file: %d left", len(files))
	}
	if hits, _ := s.Search(bg, g.ID, vecFor(1), nil, 10); len(hits) != 1 {
		t.Fatalf("search returned %d hits after canceled writes, want 1", len(hits))
	}
	if ok, _ := s.GenerationComplete(bg, g.ID); !ok {
		t.Fatal("a canceled ActivateGeneration changed the generation")
	}
}

func caseActivateSingle(t *testing.T, s contract.Store) {
	fo := addFolder(t, s, "/suite/pics")
	addFile(t, s, fo.ID, "a.png", contract.KindImage, 1)
	addFile(t, s, fo.ID, "b.png", contract.KindImage, 2)
	g1 := newGen(t, s)
	fillGen(t, s, g1.ID)
	must(t, s.ActivateGeneration(bg, g1.ID))
	g2 := newGen(t, s)
	fillGen(t, s, g2.ID)
	must(t, s.ActivateGeneration(bg, g2.ID))
	if act := activeGens(t, s); len(act) != 1 || act[0] != g2.ID {
		t.Fatalf("active generations %v, want only %d", act, g2.ID)
	}
	if err := s.DeleteItems(bg, g1.ID); err != nil {
		t.Fatalf("retired generation could not be deleted: %v", err)
	}
	if err := s.DeleteItems(bg, g2.ID); err == nil {
		t.Fatal("the active generation was deleted")
	}
	if hits, _ := s.Search(bg, g2.ID, vecFor(1), nil, 10); len(hits) != 2 {
		t.Fatalf("active generation lost its items: %d hits", len(hits))
	}
}

func caseSimilar(t *testing.T, s contract.Store) {
	fo := addFolder(t, s, "/suite/pics")
	g := newGen(t, s)
	var files []contract.FileRec
	for i := 0; i < 12; i++ {
		f := addFile(t, s, fo.ID, "f"+string(rune('a'+i))+".png", contract.KindImage, int64(i))
		files = append(files, f)
		commitOne(t, s, g.ID, f, int64(100+i))
	}
	must(t, s.ActivateGeneration(bg, g.ID))
	ids := itemIDs(t, s, g.ID)
	seed := files[0]
	rows, err := s.Similar(bg, g.ID, ids[seed.ID], nil, 10)
	if err != nil || len(rows) != 10 {
		t.Fatalf("similar: %d rows, err %v; want 10", len(rows), err)
	}
	for _, r := range rows {
		if r.FileID == seed.ID {
			t.Fatal("similar returned the seed's own file")
		}
	}
	g2 := newGen(t, s)
	commitOne(t, s, g2.ID, seed, 999)
	if _, err := s.Similar(bg, g.ID, itemIDs(t, s, g2.ID)[seed.ID], nil, 10); !errors.Is(err, contract.ErrStaleItem) {
		t.Fatalf("a seed from another generation gave %v, want ErrStaleItem", err)
	}
	if _, err := s.Similar(bg, g.ID, 1<<40, nil, 10); !errors.Is(err, contract.ErrStaleItem) {
		t.Fatalf("an unknown seed gave %v, want ErrStaleItem", err)
	}
}

func caseSearchKinds(t *testing.T, s contract.Store) {
	fo := addFolder(t, s, "/suite/mix")
	g := newGen(t, s)
	for i := 0; i < 6; i++ {
		kind := contract.KindImage
		if i%2 == 1 {
			kind = contract.KindText
		}
		commitOne(t, s, g.ID, addFile(t, s, fo.ID, "m"+string(rune('a'+i)), kind, int64(i)), int64(200+i))
	}
	all, err := s.Search(bg, g.ID, vecFor(200), nil, 4)
	if err != nil || len(all) != 4 {
		t.Fatalf("limit 4: %d hits, err %v", len(all), err)
	}
	for i := 1; i < len(all); i++ {
		if all[i].Score > all[i-1].Score {
			t.Fatal("hits are not ordered by score")
		}
	}
	images, _ := s.Search(bg, g.ID, vecFor(200), []contract.Kind{contract.KindImage}, 10)
	if len(images) != 3 {
		t.Fatalf("image filter: %d hits, want 3", len(images))
	}
	for _, h := range images {
		if h.Kind != contract.KindImage {
			t.Fatalf("image filter returned a %s", h.Kind)
		}
	}
	both, _ := s.Search(bg, g.ID, vecFor(200), []contract.Kind{contract.KindImage, contract.KindText}, 10)
	if len(both) != 6 {
		t.Fatalf("two-kind filter: %d hits, want 6", len(both))
	}
}

func caseOwnsValues(t *testing.T, s contract.Store) {
	fo := addFolder(t, s, "/suite/pics")
	g := newGen(t, s)
	f := addFile(t, s, fo.ID, "a.png", contract.KindImage, 1)
	buf := vecFor(7)
	ix := contract.FileIndex{FileID: f.ID, Gen: g.ID, Size: f.Size, MTime: f.MTime, Status: contract.StatusDone}
	must(t, s.CommitFile(bg, ix, []contract.Item{{Gen: g.ID, FileID: f.ID, Modality: "image", Start: -1, End: -1, Vector: buf}}))
	want := append([]float32(nil), buf...)
	for i := range buf {
		buf[i] = 0 // the caller reuses its buffer
	}
	hits, _ := s.Search(bg, g.ID, want, nil, 1)
	if len(hits) != 1 || hits[0].FileID != f.ID || hits[0].Score < 0.999 {
		t.Fatalf("stored vector changed after the caller reused its buffer: %+v", hits)
	}

	combo := contract.Combo{Backend: contract.BackendCPU, TextQuant: contract.QuantQ8_0, EncQuant: contract.QuantQ8_0}
	q := 0.5
	in := contract.Installed{Combo: combo, Bench: &contract.BenchResult{ImageMS: 100, Quality: &q}}
	must(t, s.SaveCombo(bg, in))
	in.Bench.ImageMS, *in.Bench.Quality = 1, 1 // the caller changes its value after saving
	saved := findCombo(t, s, combo)
	if saved.Bench == nil || saved.Bench.ImageMS != 100 || *saved.Bench.Quality != 0.5 {
		t.Fatalf("SaveCombo kept the caller's value: %+v", saved.Bench)
	}
	saved.Bench.ImageMS, *saved.Bench.Quality = 2, 2 // the caller changes a returned value
	again := findCombo(t, s, combo)
	if again.Bench.ImageMS != 100 || *again.Bench.Quality != 0.5 {
		t.Fatalf("ListCombos returned a value that aliases the store: %+v", again.Bench)
	}
}

// findCombo returns the stored combination c.
func findCombo(t *testing.T, s contract.Store, c contract.Combo) contract.Installed {
	t.Helper()
	all, err := s.ListCombos(bg)
	must(t, err)
	for _, in := range all {
		if in.Combo == c {
			return in
		}
	}
	t.Fatalf("combo %s not listed", c.ID())
	return contract.Installed{}
}

func caseActivateIncomplete(t *testing.T, s contract.Store) {
	fo := addFolder(t, s, "/suite/pics")
	f := addFile(t, s, fo.ID, "a.png", contract.KindImage, 1)
	g1 := newGen(t, s)
	fillGen(t, s, g1.ID)
	must(t, s.ActivateGeneration(bg, g1.ID))

	g2 := newGen(t, s)
	if ok, _ := s.GenerationComplete(bg, g2.ID); ok {
		t.Fatal("an empty generation reports complete")
	}
	if err := s.ActivateGeneration(bg, g2.ID); !errors.Is(err, contract.ErrIncomplete) {
		t.Fatalf("activating an empty generation: %v, want ErrIncomplete", err)
	}
	if act := activeGens(t, s); len(act) != 1 || act[0] != g1.ID {
		t.Fatalf("a refused activation changed the active generation: %v", act)
	}
	fillGen(t, s, g2.ID)
	if ok, _ := s.GenerationComplete(bg, g2.ID); !ok {
		t.Fatal("a fully indexed generation reports incomplete")
	}
	// The file changes after it was indexed: its row is stale until it is indexed again.
	f.MTime++
	must(t, s.SaveFile(bg, &f))
	if ok, _ := s.GenerationComplete(bg, g2.ID); ok {
		t.Fatal("a stale row reports complete")
	}
	if err := s.ActivateGeneration(bg, g2.ID); !errors.Is(err, contract.ErrIncomplete) {
		t.Fatalf("a stale row: %v, want ErrIncomplete", err)
	}
	if act := activeGens(t, s); len(act) != 1 || act[0] != g1.ID {
		t.Fatalf("a refused activation changed the active generation: %v", act)
	}
}

func caseActivateRetired(t *testing.T, s contract.Store) {
	fo := addFolder(t, s, "/suite/pics")
	addFile(t, s, fo.ID, "a.png", contract.KindImage, 1)
	g1 := newGen(t, s)
	fillGen(t, s, g1.ID)
	must(t, s.ActivateGeneration(bg, g1.ID))
	g2 := newGen(t, s)
	fillGen(t, s, g2.ID)
	must(t, s.ActivateGeneration(bg, g2.ID))
	if err := s.ActivateGeneration(bg, g1.ID); err == nil {
		t.Fatal("a retired generation was activated again")
	}
	if act := activeGens(t, s); len(act) != 1 || act[0] != g2.ID {
		t.Fatalf("active generations %v after a refused reactivation, want only %d", act, g2.ID)
	}
}

func caseCommitRefuses(t *testing.T, s contract.Store) {
	fo := addFolder(t, s, "/suite/pics")
	f := addFile(t, s, fo.ID, "a.png", contract.KindImage, 1)
	g := newGen(t, s)
	commitOne(t, s, g.ID, f, 1)
	before, _ := s.ListFileIndex(bg, g.ID)
	bad := contract.FileIndex{FileID: f.ID, Gen: g.ID, Size: f.Size, MTime: f.MTime, Status: ""}
	if err := s.CommitFile(bg, bad, nil); err == nil {
		t.Fatal("CommitFile accepted an empty status")
	}
	ok := contract.FileIndex{FileID: f.ID, Gen: g.ID, Size: f.Size, MTime: f.MTime, Status: contract.StatusDone}
	mismatch := contract.Item{Gen: g.ID, FileID: f.ID + 1, Vector: vecFor(1)}
	if err := s.CommitFile(bg, ok, []contract.Item{mismatch}); err == nil {
		t.Fatal("CommitFile accepted an item of another file")
	}
	after, _ := s.ListFileIndex(bg, g.ID)
	if len(after) != len(before) || len(after) != 1 || after[0].Status != contract.StatusDone {
		t.Fatalf("a refused commit changed file_index: %+v", after)
	}
	if hits, _ := s.Search(bg, g.ID, vecFor(1), nil, 10); len(hits) != 1 {
		t.Fatalf("a refused commit changed the items: %d hits", len(hits))
	}
}

func caseUnknownIDs(t *testing.T, s contract.Store) {
	fo := addFolder(t, s, "/suite/pics")
	f := addFile(t, s, fo.ID, "a.png", contract.KindImage, 1)
	g := newGen(t, s)
	ok := contract.FileIndex{FileID: f.ID, Gen: g.ID, Size: f.Size, MTime: f.MTime, Status: contract.StatusDone}
	if err := s.CommitFile(bg, contract.FileIndex{FileID: 1 << 40, Gen: g.ID, Status: contract.StatusDone}, nil); !errors.Is(err, contract.ErrUnknownFile) {
		t.Fatalf("unknown file: %v, want ErrUnknownFile", err)
	}
	if err := s.CommitFile(bg, contract.FileIndex{FileID: f.ID, Gen: 1 << 40, Status: contract.StatusDone}, nil); !errors.Is(err, contract.ErrUnknownGen) {
		t.Fatalf("unknown generation: %v, want ErrUnknownGen", err)
	}
	if err := s.ClearFileIndex(bg, 1<<40, g.ID); !errors.Is(err, contract.ErrUnknownFile) {
		t.Fatalf("ClearFileIndex unknown file: %v, want ErrUnknownFile", err)
	}
	if _, err := s.GenerationComplete(bg, 1<<40); !errors.Is(err, contract.ErrUnknownGen) {
		t.Fatalf("GenerationComplete unknown generation: %v, want ErrUnknownGen", err)
	}
	_ = ok
}

func caseClearAndDelete(t *testing.T, s contract.Store) {
	fo := addFolder(t, s, "/suite/pics")
	f := addFile(t, s, fo.ID, "a.png", contract.KindImage, 1)
	g1 := newGen(t, s)
	fillGen(t, s, g1.ID)
	must(t, s.ActivateGeneration(bg, g1.ID))
	must(t, s.ClearFileIndex(bg, f.ID, g1.ID))
	rows, _ := s.ListFileIndex(bg, g1.ID)
	if len(rows) != 0 {
		t.Fatalf("ClearFileIndex left %d rows", len(rows))
	}
	if hits, _ := s.Search(bg, g1.ID, vecFor(1), nil, 10); len(hits) != 0 {
		t.Fatalf("ClearFileIndex left %d items", len(hits))
	}
	if ok, _ := s.GenerationComplete(bg, g1.ID); ok {
		t.Fatal("a generation with a cleared file reports complete")
	}
	g2 := newGen(t, s)
	commitOne(t, s, g2.ID, f, 5)
	must(t, s.DeleteItems(bg, g2.ID))
	if rows, _ := s.ListFileIndex(bg, g2.ID); len(rows) != 0 {
		t.Fatalf("DeleteItems left %d rows", len(rows))
	}
}

func caseCombos(t *testing.T, s contract.Store) {
	c := contract.Combo{Backend: contract.BackendVulkan, TextQuant: contract.QuantBF16, EncQuant: contract.QuantBF16}
	installed := time.Unix(1_700_000_000, 5)
	must(t, s.SaveCombo(bg, contract.Installed{Combo: c, InstalledAt: installed}))
	got := findCombo(t, s, c)
	if got.Bench != nil || !got.InstalledAt.Equal(installed) {
		t.Fatalf("combo without bench: %+v", got)
	}
	must(t, s.DeleteCombo(bg, c))
	all, _ := s.ListCombos(bg)
	for _, in := range all {
		if in.Combo == c {
			t.Fatal("DeleteCombo kept the combination")
		}
	}
}

func caseDeleteFolder(t *testing.T, s contract.Store) {
	fo := addFolder(t, s, "/suite/pics")
	keep := addFolder(t, s, "/suite/keep")
	f := addFile(t, s, fo.ID, "a.png", contract.KindImage, 1)
	k := addFile(t, s, keep.ID, "k.png", contract.KindImage, 2)
	g := newGen(t, s)
	commitOne(t, s, g.ID, f, 1)
	commitOne(t, s, g.ID, k, 2)
	must(t, s.DeleteFolder(bg, fo.ID))
	stale := f
	if err := s.SaveFile(bg, &stale); !errors.Is(err, contract.ErrUnknownFile) {
		t.Fatalf("SaveFile for deleted file: %v, want ErrUnknownFile", err)
	}
	discovery := f
	discovery.ID = 0
	if err := s.SaveFile(bg, &discovery); !errors.Is(err, contract.ErrUnknownFolder) {
		t.Fatalf("SaveFile into deleted folder: %v, want ErrUnknownFolder", err)
	}
	missing := contract.FileRec{ID: 1 << 40, FolderID: keep.ID, Path: "/suite/missing.png", Kind: contract.KindImage}
	if err := s.SaveFile(bg, &missing); !errors.Is(err, contract.ErrUnknownFile) {
		t.Fatalf("SaveFile for unknown ID: %v, want ErrUnknownFile", err)
	}
	if files, _ := s.ListFiles(bg, fo.ID); len(files) != 0 {
		t.Fatalf("DeleteFolder left %d files", len(files))
	}
	rows, _ := s.ListFileIndex(bg, g.ID)
	if len(rows) != 1 || rows[0].FileID != k.ID {
		t.Fatalf("file_index after DeleteFolder: %+v, want only the other folder's file", rows)
	}
	if hits, _ := s.Search(bg, g.ID, vecFor(1), nil, 10); len(hits) != 1 || hits[0].FileID != k.ID {
		t.Fatalf("search after DeleteFolder: %+v", hits)
	}
	folders, _ := s.ListFolders(bg)
	if len(folders) != 1 || folders[0].ID != keep.ID {
		t.Fatalf("ListFolders after DeleteFolder: %+v", folders)
	}
}

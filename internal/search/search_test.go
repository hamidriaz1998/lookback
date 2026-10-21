package search

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
	"github.com/hamidriaz1998/lookback/internal/fake"
	"github.com/hamidriaz1998/lookback/internal/store"
)

var bg = context.Background()

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// space is the vector space of every generation in these tests.
var space = contract.VectorSpace{TextQuant: contract.QuantBF16, EncQuant: contract.QuantQ8_0, ModelRev: "rev-s"}

// unitAt returns the unit vector e_k.
func unitAt(k int) []float32 {
	v := make([]float32, 768)
	v[k] = 1
	return v
}

// mix returns a*e0 + sqrt(1-a²)*e_k, so its cosine with e0 is a.
func mix(a float64, k int) []float32 {
	v := make([]float32, 768)
	v[0] = float32(a)
	v[k] = float32(math.Sqrt(1 - a*a))
	return v
}

// stubSession is a session whose query vector is fixed, so cosines are known. It can also fail its first
// embed with ErrRestarting.
type stubSession struct {
	contract.Session
	vec       []float32
	restarts  int
	readyCall int
	onEmbed   func()
}

func (s *stubSession) EmbedTexts(ctx context.Context, prompts []string) ([][]float32, int, error) {
	if s.onEmbed != nil {
		s.onEmbed()
	}
	if s.restarts > 0 {
		s.restarts--
		return nil, 0, contract.ErrRestarting
	}
	return [][]float32{s.vec}, 1, nil
}

// Space returns the active generation's space, so the stub passes the space check.
func (s *stubSession) Space() contract.VectorSpace { return space }

func (s *stubSession) Ready(ctx context.Context) error {
	s.readyCall++
	return nil
}

// fixture is a store with one active generation of the given files, each committed with its vector.
type fixture struct {
	st  contract.Store
	gen contract.Generation
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(t.TempDir())
	must(t, err)
	t.Cleanup(func() { st.Close() })
	g := contract.Generation{Space: space}
	must(t, st.NewGeneration(bg, &g))
	return &fixture{st: st, gen: g}
}

// add creates a folder file with one text item of vector vec in generation gen.
func (fx *fixture) add(t *testing.T, gen int64, path string, kind contract.Kind, vec []float32) contract.FileRec {
	t.Helper()
	f := contract.FileRec{FolderID: fx.folder(t), Path: path, Kind: kind, Size: 10, MTime: 1}
	if err := fx.st.SaveFile(bg, &f); err != nil {
		t.Fatal(err)
	}
	fx.commit(t, gen, f, vec)
	return f
}

func (fx *fixture) folder(t *testing.T) int64 {
	t.Helper()
	fo := contract.Folder{Path: "/root"}
	folders, _ := fx.st.ListFolders(bg)
	if len(folders) > 0 {
		return folders[0].ID
	}
	must(t, fx.st.SaveFolder(bg, &fo))
	return fo.ID
}

func (fx *fixture) commit(t *testing.T, gen int64, f contract.FileRec, vec []float32) {
	t.Helper()
	ix := contract.FileIndex{FileID: f.ID, Gen: gen, Size: f.Size, MTime: f.MTime, Status: contract.StatusDone}
	it := contract.Item{Gen: gen, FileID: f.ID, Modality: "text", Start: 0, End: 5, Snippet: f.Path, Vector: vec}
	must(t, fx.st.CommitFile(bg, ix, []contract.Item{it}))
}

// activate covers every file in gen (a file missing from it would block activation) and makes it active.
func (fx *fixture) activate(t *testing.T, gen int64) {
	t.Helper()
	files := fx.allFiles(t)
	for _, f := range files {
		if ix, _ := fx.st.ListFileIndex(bg, gen); !hasRow(ix, f.ID) {
			fx.commit(t, gen, f, unitAt(900))
		}
	}
	must(t, fx.st.ActivateGeneration(bg, gen))
}

func hasRow(rows []contract.FileIndex, id int64) bool {
	for _, r := range rows {
		if r.FileID == id {
			return true
		}
	}
	return false
}

func (fx *fixture) allFiles(t *testing.T) []contract.FileRec {
	t.Helper()
	folders, _ := fx.st.ListFolders(bg)
	var out []contract.FileRec
	for _, fo := range folders {
		fs, err := fx.st.ListFiles(bg, fo.ID)
		must(t, err)
		out = append(out, fs...)
	}
	return out
}

// The result list groups by file: each file appears once, with its best item's score.
func TestRunGroupsByFile(t *testing.T) {
	fx := newFixture(t)
	// a has three items; only its best (0.9) may count, and it must add one result, not three
	af := contract.FileRec{FolderID: fx.folder(t), Path: "/root/a.txt", Kind: contract.KindText, Size: 10, MTime: 1}
	must(t, fx.st.SaveFile(bg, &af))
	a := af
	must(t, fx.st.CommitFile(bg, contract.FileIndex{FileID: a.ID, Gen: fx.gen.ID, Size: 10, MTime: 1, Status: contract.StatusDone},
		[]contract.Item{
			{Gen: fx.gen.ID, FileID: a.ID, Modality: "text", Start: 0, End: 1, Vector: mix(0.2, 2)},
			{Gen: fx.gen.ID, FileID: a.ID, Modality: "text", Start: 2, End: 3, Vector: mix(0.5, 3)},
			{Gen: fx.gen.ID, FileID: a.ID, Modality: "text", Start: 4, End: 5, Vector: mix(0.9, 1)},
		}))
	b := fx.add(t, fx.gen.ID, "/root/b.txt", contract.KindText, mix(0.5, 4))
	fx.activate(t, fx.gen.ID)

	sess := &stubSession{vec: unitAt(0)}
	res, err := New(fx.st).Run(bg, sess, "unrelated", nil)
	must(t, err)
	if len(res) != 2 {
		t.Fatalf("got %d results, want one per file (2)", len(res))
	}
	if res[0].FileID != a.ID || res[1].FileID != b.ID {
		t.Fatalf("order %d,%d; want a then b", res[0].FileID, res[1].FileID)
	}
	if math.Abs(res[0].Score-0.9) > 1e-4 {
		t.Fatalf("a's score %.4f, want its best item's 0.9", res[0].Score)
	}
}

// The boost (SPEC section 10) reorders files, and it is applied to the candidates beyond the top 60: a file that
// is only 61st by similarity rises into the top 60 once boosted.
func TestBoostLiftsCandidateFromBeyondTopPage(t *testing.T) {
	fx := newFixture(t)
	var lifted contract.FileRec
	for k := 0; k < 70; k++ {
		name := fmt.Sprintf("/root/plain%02d.png", k)
		if k == 65 {
			name = "/root/cat-dog-owl.png"
		}
		f := fx.add(t, fx.gen.ID, name, contract.KindImage, mix(0.9-0.005*float64(k), k+1))
		if k == 65 {
			lifted = f
		}
	}
	fx.activate(t, fx.gen.ID)
	res, err := New(fx.st).Run(bg, &stubSession{vec: unitAt(0)}, "cat dog owl", nil)
	must(t, err)
	if len(res) != PageSize {
		t.Fatalf("got %d results, want %d", len(res), PageSize)
	}
	found := -1
	for i, r := range res {
		if r.FileID == lifted.ID {
			found = i
		}
	}
	if found < 0 {
		t.Fatal("the boosted file outside the raw top 60 is missing")
	}
	if math.Abs(res[found].Score-(0.9-0.005*65+0.12)) > 1e-4 {
		t.Fatalf("boosted score %.4f", res[found].Score)
	}
}

// The boost is capped at 0.12 however many words match, and each word of 3 or more letters adds 0.04.
func TestBoostCapAndWordRules(t *testing.T) {
	fx := newFixture(t)
	f := fx.add(t, fx.gen.ID, "/root/cat-dog-owl-fox-elk.png", contract.KindImage, mix(0.5, 1))
	fx.activate(t, fx.gen.ID)
	res, err := New(fx.st).Run(bg, &stubSession{vec: unitAt(0)}, "cat dog owl fox elk", nil)
	must(t, err)
	if len(res) != 1 || res[0].FileID != f.ID {
		t.Fatalf("results %+v", res)
	}
	if math.Abs(res[0].Score-(0.5+0.12)) > 1e-4 {
		t.Fatalf("five matching words scored %.6f, want 0.5 + the 0.12 cap", res[0].Score)
	}
	// "to" and "a" have fewer than three letters: they add nothing.
	if got := boost(queryWords("to a"), "/root/to-a.png"); got != 0 {
		t.Fatalf("short words boosted %.2f", got)
	}
}

func TestQueryWords(t *testing.T) {
	got := queryWords("Cat cat, CAT! to the a-b dog2 café")
	want := []string{"cat", "the", "dog", "café"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("queryWords = %v, want %v", got, want)
	}
}

// A match in the parent folder counts; one two folders up does not (the last two segments only).
func TestBoostPathSegments(t *testing.T) {
	words := []string{"cats"}
	if got := boost(words, "/home/u/cats/x.png"); got != 0.04 {
		t.Fatalf("parent folder match = %.2f, want 0.04", got)
	}
	if got := boost(words, "/cats/pics/x.png"); got != 0 {
		t.Fatalf("grandparent match = %.2f, want 0", got)
	}
	if got := boost([]string{"report"}, "/x/REPORT.pdf"); got != 0.04 {
		t.Fatalf("match is case-sensitive: %.2f", got)
	}
}

// Kind filters limit the files to the chosen kinds.
func TestRunKindFilter(t *testing.T) {
	fx := newFixture(t)
	fx.add(t, fx.gen.ID, "/root/p.png", contract.KindImage, mix(0.9, 1))
	doc := fx.add(t, fx.gen.ID, "/root/d.pdf", contract.KindPDF, mix(0.8, 2))
	fx.activate(t, fx.gen.ID)
	sess := &stubSession{vec: unitAt(0)}
	docs, err := New(fx.st).Run(bg, sess, "q", []contract.Kind{contract.KindPDF})
	must(t, err)
	if len(docs) != 1 || docs[0].FileID != doc.ID {
		t.Fatalf("pdf filter: %+v", docs)
	}
}

// A building generation's items never appear in a search.
func TestBuildingGenerationIsIsolated(t *testing.T) {
	fx := newFixture(t)
	a := fx.add(t, fx.gen.ID, "/root/a.png", contract.KindImage, mix(0.1, 1))
	g2 := contract.Generation{Space: space}
	must(t, fx.st.NewGeneration(bg, &g2))
	// The building generation has the query vector for a; the active one does not.
	fx.commit(t, g2.ID, a, unitAt(0))
	fx.activate(t, fx.gen.ID)
	res, err := New(fx.st).Run(bg, &stubSession{vec: unitAt(0)}, "zzz", nil)
	must(t, err)
	if len(res) != 1 || res[0].Score > 0.2 {
		t.Fatalf("the building generation leaked into search: %+v", res)
	}
}

// Deleting a file removes it from the results.
func TestDeletedFileLeavesResults(t *testing.T) {
	fx := newFixture(t)
	a := fx.add(t, fx.gen.ID, "/root/a.png", contract.KindImage, unitAt(0))
	fx.add(t, fx.gen.ID, "/root/b.png", contract.KindImage, mix(0.3, 2))
	fx.activate(t, fx.gen.ID)
	must(t, fx.st.DeleteFile(bg, a.ID))
	res, err := New(fx.st).Run(bg, &stubSession{vec: unitAt(0)}, "q", nil)
	must(t, err)
	for _, r := range res {
		if r.FileID == a.ID {
			t.Fatal("a deleted file is still in the results")
		}
	}
}

func TestRunErrors(t *testing.T) {
	fx := newFixture(t)
	s := New(fx.st)
	if _, err := s.Run(bg, &stubSession{vec: unitAt(0)}, "q", nil); !errors.Is(err, ErrNoActive) {
		t.Fatalf("no active generation: %v", err)
	}
	fx.activate(t, fx.gen.ID)
	other := &stubSession{vec: unitAt(0)}
	if _, err := s.Run(bg, otherSpace{other}, "q", nil); err == nil {
		t.Fatal("a session of another space was accepted")
	}
	if res, err := s.Run(bg, other, "   ", nil); err != nil || res != nil {
		t.Fatalf("empty query: %v %v", res, err)
	}
}

// otherSpace reports a different vector space than the active generation.
type otherSpace struct{ *stubSession }

func (otherSpace) Space() contract.VectorSpace {
	return contract.VectorSpace{TextQuant: contract.QuantQ8_0, EncQuant: contract.QuantQ8_0, ModelRev: "other"}
}

// A restarting sidecar is waited for once and retried.
func TestRunWaitsForRestart(t *testing.T) {
	fx := newFixture(t)
	fx.add(t, fx.gen.ID, "/root/a.png", contract.KindImage, unitAt(0))
	fx.activate(t, fx.gen.ID)
	sess := &stubSession{vec: unitAt(0), restarts: 1}
	if _, err := New(fx.st).Run(bg, sess, "q", nil); err != nil {
		t.Fatalf("Run after a restart: %v", err)
	}
	if sess.readyCall != 1 {
		t.Fatalf("Ready called %d times, want 1", sess.readyCall)
	}
}

// Run reports a retry if the old generation is retired while the query is being embedded.
func TestRunDetectsGenerationHandoverDuringEmbedding(t *testing.T) {
	fx := newFixture(t)
	f := fx.add(t, fx.gen.ID, "/root/a.png", contract.KindImage, unitAt(0))
	fx.activate(t, fx.gen.ID)
	g2 := contract.Generation{Space: space}
	must(t, fx.st.NewGeneration(bg, &g2))
	sess := &stubSession{vec: unitAt(0), onEmbed: func() {
		fx.commit(t, g2.ID, f, unitAt(0))
		must(t, fx.st.ActivateGeneration(bg, g2.ID))
		must(t, fx.st.DeleteItems(bg, fx.gen.ID))
	}}
	if _, err := New(fx.st).Run(bg, sess, "q", nil); !errors.Is(err, ErrGenerationChanged) {
		t.Fatalf("Run during handover: %v, want ErrGenerationChanged", err)
	}
}

// Similar uses the seed's stored vector, excludes its file, and refuses a seed of another generation.
func TestSimilar(t *testing.T) {
	fx := newFixture(t)
	seed := fx.add(t, fx.gen.ID, "/root/seed.png", contract.KindImage, unitAt(0))
	near := fx.add(t, fx.gen.ID, "/root/near.png", contract.KindImage, mix(0.9, 1))
	fx.activate(t, fx.gen.ID)
	ids := map[int64]int64{}
	rec, _ := fx.st.RecentItems(bg, fx.gen.ID, 10)
	for _, h := range rec {
		ids[h.FileID] = h.ItemID
	}
	s := New(fx.st)
	res, err := s.Similar(bg, ids[seed.ID], nil)
	must(t, err)
	if len(res) != 1 || res[0].FileID != near.ID {
		t.Fatalf("similar: %+v", res)
	}
	g2 := contract.Generation{Space: space}
	must(t, fx.st.NewGeneration(bg, &g2))
	fx.commit(t, g2.ID, seed, unitAt(0))
	stale, _ := fx.st.RecentItems(bg, g2.ID, 10)
	if _, err := s.Similar(bg, stale[0].ItemID, nil); !errors.Is(err, contract.ErrStaleItem) {
		t.Fatalf("stale seed: %v, want ErrStaleItem", err)
	}
}

// Run with the fake runtime: the query embeds through a real session of the fake sidecar.
func TestRunWithFakeRuntime(t *testing.T) {
	ctx := context.Background()
	rt := fake.NewRuntime(ctx)
	sess, err := rt.Session(ctx, space)
	must(t, err)
	defer sess.Close()
	fx := newFixture(t)
	vecs, _, err := sess.EmbedTexts(ctx, []string{queryPrefix + "beach"})
	must(t, err)
	fx.add(t, fx.gen.ID, "/root/beach.png", contract.KindImage, vecs[0])
	fx.add(t, fx.gen.ID, "/root/other.png", contract.KindImage, unitAt(7))
	fx.activate(t, fx.gen.ID)
	res, err := New(fx.st).Run(ctx, sess, "beach", nil)
	must(t, err)
	if len(res) == 0 || res[0].Path != "/root/beach.png" {
		t.Fatalf("top result %+v", res)
	}
}

// TestRun100k times Run and Similar at the SPEC budget size (SPEC sections 10 and 18). It asserts the result
// counts and logs the timings. Skipped under -short.
func TestRun100k(t *testing.T) {
	if testing.Short() {
		t.Skip("100k items")
	}
	fx := newFixture(t)
	r := rand.New(rand.NewSource(3))
	folder := fx.folder(t)
	const files, perFile = 1000, 100
	start := time.Now()
	for f := 0; f < files; f++ {
		fi := contract.FileRec{FolderID: folder, Path: fmt.Sprintf("/root/bulk/%d.png", f), Kind: contract.KindImage, Size: 1, MTime: 1}
		must(t, fx.st.SaveFile(bg, &fi))
		items := make([]contract.Item, perFile)
		for i := range items {
			items[i] = contract.Item{Gen: fx.gen.ID, FileID: fi.ID, Modality: "image", Start: -1, End: -1, Vector: randVec(r)}
		}
		must(t, fx.st.CommitFile(bg, contract.FileIndex{FileID: fi.ID, Gen: fx.gen.ID, Size: 1, MTime: 1, Status: contract.StatusDone}, items))
	}
	t.Logf("commit %d items in %d files: %s", files*perFile, files, time.Since(start).Round(time.Millisecond))
	fx.activate(t, fx.gen.ID)
	s := New(fx.st)
	sess := &stubSession{vec: randVec(r)}
	for run := 1; run <= 3; run++ {
		start = time.Now()
		res, err := s.Run(bg, sess, "a query about things", nil)
		must(t, err)
		if len(res) != PageSize {
			t.Fatalf("Run returned %d results, want %d", len(res), PageSize)
		}
		t.Logf("search.Run run %d: %s", run, time.Since(start).Round(time.Millisecond))
	}
}

// randVec returns a random unit vector.
func randVec(r *rand.Rand) []float32 {
	v := make([]float32, 768)
	var n float64
	for i := range v {
		v[i] = float32(r.NormFloat64())
		n += float64(v[i]) * float64(v[i])
	}
	for i := range v {
		v[i] /= float32(math.Sqrt(n))
	}
	return v
}

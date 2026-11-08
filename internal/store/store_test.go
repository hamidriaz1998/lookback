package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// childRootEnv tells a re-executed test binary to run TestOpenChild on the data root in its value.
const childRootEnv = "LOOKBACK_STORE_CHILD_ROOT"

// TestOpenChild is the child half of TestOpenUsesDataRootLib. It only runs when re-executed with childRootEnv set.
func TestOpenChild(t *testing.T) {
	root := os.Getenv(childRootEnv)
	if root == "" {
		t.Skip("child process only")
	}
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.ListFolders(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestOpenUsesDataRootLib checks that the native library lands in <root>/lib. The driver reads
// TURSO_GO_CACHE_DIR once per process, so the check runs in a fresh process.
func TestOpenUsesDataRootLib(t *testing.T) {
	if testing.Short() {
		t.Skip("re-executes the test binary and copies the native library")
	}
	root := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestOpenChild$")
	cmd.Env = append(os.Environ(), childRootEnv+"="+root, "TURSO_GO_CACHE_DIR=")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child failed: %v\n%s", err, out)
	}
	found := false
	filepath.Walk(filepath.Join(root, "lib"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(info.Name(), "turso") {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatalf("no native library under %s", filepath.Join(root, "lib"))
	}
	if _, err := os.Stat(filepath.Join(root, dbFile)); err != nil {
		t.Fatalf("database not created in the data root: %v", err)
	}
}

// TestOpenMigratesOnce checks that the schema version is recorded and that reopening does not migrate again.
func TestOpenMigratesOnce(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != len(migrations) {
		t.Fatalf("user_version %d (err %v), want %d", v, err, len(migrations))
	}
	must(t, s.SetSetting(context.Background(), "kept", "yes"))
	must(t, s.Close())
	s, err = Open(root)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s.Close()
	if v, ok, _ := s.GetSetting(context.Background(), "kept"); !ok || v != "yes" {
		t.Fatalf("value lost on reopen: %q %v", v, ok)
	}
}

func TestOpenRejectsQuestionMarkInDataRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "lookback?x")
	if _, err := Open(root); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("Open(%q) = %v, want explicit unsupported path error", root, err)
	}
	for _, p := range []string{root, filepath.Join(parent, "lookback"), filepath.Join(root, "lib")} {
		// Any error means the path does not exist. Windows rejects a name with "?" with an invalid-name error
		// instead of "not exist", and a path that cannot be named cannot have been created either.
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("Open touched %q before rejecting the root", p)
		}
	}
}

// TestRawEmptyStatusIsIncomplete covers a row whose status is not terminal, which the write path refuses. Only
// the database can hold one, so the test writes it directly. Completeness and activation must refuse it.
func TestRawEmptyStatusIsIncomplete(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	fo := addFolder(t, s, "/suite/raw")
	f := addFile(t, s, fo.ID, "a.png", contract.KindImage, 1)
	g := newGen(t, s)
	commitOne(t, s, g.ID, f, 1)
	if _, err := s.db.Exec(`UPDATE file_index SET status = '' WHERE gen = ?`, g.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.GenerationComplete(ctx, g.ID); err != nil || ok {
		t.Fatalf("empty status: complete=%v err=%v", ok, err)
	}
	if err := s.ActivateGeneration(ctx, g.ID); !errors.Is(err, contract.ErrIncomplete) {
		t.Fatalf("activating an empty status: %v, want ErrIncomplete", err)
	}
}

// TestImageRangeIsNull checks that an image item stores no byte range (SPEC section 10: NULL for images).
func TestImageRangeIsNull(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fo := addFolder(t, s, "/suite/img")
	f := addFile(t, s, fo.ID, "a.png", contract.KindImage, 1)
	g := newGen(t, s)
	commitOne(t, s, g.ID, f, 1)
	var start, end any
	if err := s.db.QueryRow(`SELECT start, "end" FROM items WHERE gen = ?`, g.ID).Scan(&start, &end); err != nil {
		t.Fatal(err)
	}
	if start != nil || end != nil {
		t.Fatalf("image range stored as %v..%v, want NULL", start, end)
	}
}

// TestSearchAndCommitConcurrently runs searches and commits at once on the one connection. Every call must
// succeed, and the race detector must stay quiet (the pre-push hook runs it with -race).
func TestSearchAndCommitConcurrently(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	fo := addFolder(t, s, "/suite/race")
	g := newGen(t, s)
	var files []contract.FileRec
	for i := 0; i < 40; i++ {
		files = append(files, addFile(t, s, fo.ID, "r"+string(rune('a'+i%26))+string(rune('a'+i/26))+".png", contract.KindImage, int64(i)))
	}
	for i, f := range files[:20] {
		commitOne(t, s, g.ID, f, int64(i))
	}
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				if _, err := s.Search(context.Background(), g.ID, vecFor(int64(i)), nil, 10); err != nil {
					errs <- err
				}
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i, f := range files[20:] {
			ix := contract.FileIndex{FileID: f.ID, Gen: g.ID, Size: f.Size, MTime: f.MTime, Status: contract.StatusDone}
			it := contract.Item{Gen: g.ID, FileID: f.ID, Modality: "image", Start: -1, End: -1, Vector: vecFor(int64(100 + i))}
			if err := s.CommitFile(context.Background(), ix, []contract.Item{it}); err != nil {
				errs <- err
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent call: %v", err)
	}
	hits, err := s.Search(context.Background(), g.ID, vecFor(0), nil, 100)
	if err != nil || len(hits) != 40 {
		t.Fatalf("after the commits: %d hits, err %v; want 40", len(hits), err)
	}
}

// Similar must observe either a complete seed snapshot or a missing seed while indexing clears and replaces it.
func TestSimilarAndSeedReplacementConcurrently(t *testing.T) {
	s, err := Open(t.TempDir())
	must(t, err)
	defer s.Close()
	ctx := context.Background()
	fo := addFolder(t, s, "/suite/similar-race")
	seed := addFile(t, s, fo.ID, "seed.png", contract.KindImage, 1)
	other := addFile(t, s, fo.ID, "other.png", contract.KindImage, 2)
	g := newGen(t, s)
	commitOne(t, s, g.ID, seed, 1)
	commitOne(t, s, g.ID, other, 2)
	must(t, s.ActivateGeneration(ctx, g.ID))
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if err := s.ClearFileIndex(ctx, seed.ID, g.ID); err != nil {
				errs <- err
				return
			}
			if err := s.CommitFile(ctx, contract.FileIndex{FileID: seed.ID, Gen: g.ID, Size: seed.Size, MTime: seed.MTime, Status: contract.StatusDone}, []contract.Item{{Gen: g.ID, FileID: seed.ID, Modality: "image", Start: -1, End: -1, Vector: vecFor(1)}}); err != nil {
				errs <- err
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			recent, err := s.RecentItems(ctx, g.ID, 10)
			if err != nil {
				errs <- err
				return
			}
			var seedID int64
			for _, h := range recent {
				if h.FileID == seed.ID {
					seedID = h.ItemID
					break
				}
			}
			if seedID == 0 {
				continue
			}
			hits, err := s.Similar(ctx, g.ID, seedID, nil, 10)
			if errors.Is(err, contract.ErrStaleItem) {
				continue
			}
			if err != nil {
				errs <- err
				return
			}
			if len(hits) != 1 || hits[0].FileID != other.ID {
				errs <- fmt.Errorf("inconsistent Similar result: %+v", hits)
				return
			}
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// TestSearch100k times the search at the SPEC budget size (SPEC sections 10 and 18). The row counts are
// asserted; the timings are only logged, because they depend on the machine. Skipped under -short.
func TestStoreSearch100k(t *testing.T) {
	if testing.Short() {
		t.Skip("100k items")
	}
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	g := newGen(t, s)
	r := rand.New(rand.NewSource(2))
	const files, perFile = 25_000, 4

	start := time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for f := 1; f <= files; f++ {
		if _, err := tx.Exec(`INSERT INTO files (id, folder_id, path, kind, size, mtime) VALUES (?, 1, ?, 'image', 1, 1)`,
			f, fmt.Sprintf("/bulk/%d.png", f)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= files*perFile; i++ {
		if _, err := tx.Exec(`INSERT INTO items (gen, file_id, modality, snippet, embedding)
			VALUES (?, ?, 'image', 's', vector32(?))`, g.ID, (i-1)/perFile+1, vecJSON(randUnit(r))); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Logf("insert %d items in %d files: %s", files*perFile, files, time.Since(start).Round(time.Millisecond))

	q := randUnit(r)
	for run := 1; run <= 3; run++ {
		start = time.Now()
		hits, err := s.Search(ctx, g.ID, q, nil, 60)
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 60 {
			t.Fatalf("search returned %d hits, want 60", len(hits))
		}
		t.Logf("Store.Search run %d: %d files in %s", run, len(hits), time.Since(start).Round(time.Millisecond))
	}
	seed := int64(1)
	start = time.Now()
	sim, err := s.Similar(ctx, g.ID, seed, nil, 60)
	if err != nil || len(sim) != 60 {
		t.Fatalf("similar: %d rows, err %v", len(sim), err)
	}
	t.Logf("Store.Similar: %d files in %s", len(sim), time.Since(start).Round(time.Millisecond))
}

// TestIDsAreNotReused checks that a deleted row's file or item ID is never handed out again (migration 2).
// A job that still holds an old ID must find nothing, not a new object with the same number.
func TestIDsAreNotReused(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	must(t, err)
	defer s.Close()
	fo := addFolder(t, s, "/suite/reuse")

	t.Run("deleted highest file during an outstanding commit", func(t *testing.T) {
		a := addFile(t, s, fo.ID, "reuse-a.png", contract.KindImage, 1)
		b := addFile(t, s, fo.ID, "reuse-b.png", contract.KindImage, 2)
		g := newGen(t, s)
		commitOne(t, s, g.ID, a, 1)
		// The indexing job for b holds b's row: it has stat'd b and is about to commit.
		held := contract.FileIndex{FileID: b.ID, Gen: g.ID, Size: b.Size, MTime: b.MTime, Status: contract.StatusDone}
		must(t, s.DeleteFile(ctx, b.ID))
		c := addFile(t, s, fo.ID, "reuse-c.png", contract.KindImage, 3)
		if c.ID == b.ID {
			t.Fatalf("new file reused deleted file ID %d", b.ID)
		}
		if err := s.CommitFile(ctx, held, []contract.Item{{Gen: g.ID, FileID: b.ID, Modality: "image", Start: -1, End: -1, Vector: vecFor(9)}}); !errors.Is(err, contract.ErrUnknownFile) {
			t.Fatalf("outstanding commit for the deleted file: %v, want ErrUnknownFile", err)
		}
		if rows, _ := s.ListFileIndex(ctx, g.ID); len(rows) != 1 || rows[0].FileID != a.ID {
			t.Fatalf("the outstanding commit wrote rows: %+v", rows)
		}
	})

	t.Run("replacing the highest item", func(t *testing.T) {
		f := addFile(t, s, fo.ID, "reuse-d.png", contract.KindImage, 4)
		g := newGen(t, s)
		commitOne(t, s, g.ID, f, 4)
		old := itemIDs(t, s, g.ID)[f.ID]
		commitOne(t, s, g.ID, f, 5) // replaces the file's items
		if fresh := itemIDs(t, s, g.ID)[f.ID]; fresh == old {
			t.Fatalf("replacement item reused ID %d", old)
		}
	})

	t.Run("no item ID recycled across generations", func(t *testing.T) {
		// The seed is the highest item ever allocated. Its generation is deleted (not active, so allowed), then
		// another generation indexes the file: it must not get the seed's ID.
		f := addFile(t, s, fo.ID, "reuse-e.png", contract.KindImage, 5)
		g1 := newGen(t, s)
		commitOne(t, s, g1.ID, f, 5)
		seed := itemIDs(t, s, g1.ID)[f.ID]
		must(t, s.DeleteItems(ctx, g1.ID))
		g2 := newGen(t, s)
		commitOne(t, s, g2.ID, f, 6)
		if fresh := itemIDs(t, s, g2.ID)[f.ID]; fresh == seed {
			t.Fatalf("a new item reused the deleted generation's ID %d", seed)
		}
		if _, err := s.Similar(ctx, g2.ID, seed, nil, 10); !errors.Is(err, contract.ErrStaleItem) {
			t.Fatalf("a deleted item's ID in another generation: %v, want ErrStaleItem", err)
		}
	})
}

// SaveFile cannot resurrect rows from scans that finish after a folder or file was removed.
func TestSaveFileRejectsStaleWrites(t *testing.T) {
	ctx := context.Background()
	s, err := Open(t.TempDir())
	must(t, err)
	defer s.Close()
	folder := addFolder(t, s, "/suite/removed")
	keepFolder := addFolder(t, s, "/suite/kept")
	removed := addFile(t, s, folder.ID, "removed.png", contract.KindImage, 1)
	kept := addFile(t, s, keepFolder.ID, "kept.png", contract.KindImage, 2)
	g := newGen(t, s)
	commitOne(t, s, g.ID, removed, 1)
	commitOne(t, s, g.ID, kept, 2)
	must(t, s.ActivateGeneration(ctx, g.ID))
	newDiscovery := removed
	newDiscovery.ID = 0
	staleUpdate := removed
	must(t, s.DeleteFolder(ctx, folder.ID))
	if err := s.SaveFile(ctx, &newDiscovery); !errors.Is(err, contract.ErrUnknownFolder) {
		t.Fatalf("zero-ID discovery after folder removal: %v, want ErrUnknownFolder", err)
	}
	if err := s.SaveFile(ctx, &staleUpdate); !errors.Is(err, contract.ErrUnknownFile) {
		t.Fatalf("nonzero-ID update after folder removal: %v, want ErrUnknownFile", err)
	}
	missing := contract.FileRec{ID: 1 << 40, FolderID: keepFolder.ID, Path: "/suite/kept/missing.png", Kind: contract.KindImage}
	if err := s.SaveFile(ctx, &missing); !errors.Is(err, contract.ErrUnknownFile) {
		t.Fatalf("update of missing file: %v, want ErrUnknownFile", err)
	}
	files, err := s.ListFiles(ctx, keepFolder.ID)
	if err != nil || len(files) != 1 || files[0].ID != kept.ID {
		t.Fatalf("kept folder files after stale writes: %+v, err %v", files, err)
	}
	indexes, err := s.ListFileIndex(ctx, g.ID)
	if err != nil || len(indexes) != 1 || indexes[0].FileID != kept.ID {
		t.Fatalf("generation rows after stale writes: %+v, err %v", indexes, err)
	}
	hits, err := s.Search(ctx, g.ID, vecFor(1), nil, 10)
	if err != nil || len(hits) != 1 || hits[0].FileID != kept.ID {
		t.Fatalf("search after stale writes: %+v, err %v", hits, err)
	}
}

// TestMigrationKeepsIDs checks that a database written by migration 1 keeps its IDs, and that its sequences
// continue above them after migration 2 runs.
func TestMigrationKeepsIDs(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("turso", filepath.Join(root, dbFile))
	must(t, err)
	db.SetMaxOpenConns(1)
	_, err = db.Exec(migrations[0])
	must(t, err)
	_, err = db.Exec(`PRAGMA user_version = 1`)
	must(t, err)
	_, err = db.Exec(`INSERT INTO folders (id, path, added_at) VALUES (7, '/old', 1);
		INSERT INTO files (id, folder_id, path, kind, size, mtime) VALUES (9, 7, '/old/a.png', 'image', 1, 1);
		INSERT INTO generations (id, text_quant, enc_quant, model_rev, state, created_at) VALUES (3, 'bf16', 'q8_0', 'r', 'building', 1);
		INSERT INTO items (id, gen, file_id, modality, embedding) VALUES (11, 3, 9, 'image', vector32('[1,0]'))`)
	must(t, err)
	must(t, db.Close())

	s, err := Open(root)
	must(t, err)
	defer s.Close()
	f := contract.FileRec{FolderID: 7, Path: "/old/b.png", Kind: contract.KindImage, Size: 1, MTime: 1}
	must(t, s.SaveFile(context.Background(), &f))
	if f.ID != 10 {
		t.Fatalf("new file got ID %d after migration, want 10", f.ID)
	}
	folders, _ := s.ListFolders(context.Background())
	if len(folders) != 1 || folders[0].ID != 7 {
		t.Fatalf("folder ID changed: %+v", folders)
	}
	gens, _ := s.ListGenerations(context.Background())
	if len(gens) != 1 || gens[0].ID != 3 {
		t.Fatalf("generation ID changed: %+v", gens)
	}
}

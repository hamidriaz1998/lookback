package store

import (
	"context"
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

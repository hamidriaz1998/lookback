package spike

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"sort"
	"testing"
	"time"

	_ "turso.tech/database/tursogo"
)

const dim = 768

// The SPEC §10 tables the search touches.
const schema = `
CREATE TABLE files (id INTEGER PRIMARY KEY, folder_id INTEGER NOT NULL, path TEXT NOT NULL UNIQUE,
  kind TEXT NOT NULL, size INTEGER NOT NULL, mtime INTEGER NOT NULL);
CREATE TABLE items (id INTEGER PRIMARY KEY, gen INTEGER NOT NULL, file_id INTEGER NOT NULL,
  unit INTEGER NOT NULL DEFAULT 0, start INTEGER, "end" INTEGER, modality TEXT NOT NULL,
  snippet TEXT, embedding BLOB NOT NULL);
CREATE INDEX items_gen_file ON items (gen, file_id);`

// The SPEC §10 search query.
const searchSQL = `
SELECT i.id, i.file_id, f.path, f.kind, i.unit, i.snippet, i.modality,
       vector_distance_cos(i.embedding, vector32(?1)) AS d
FROM items i JOIN files f ON f.id = i.file_id
WHERE i.gen = ?2
ORDER BY d LIMIT 300`

const similarSQL = `
SELECT i.id, vector_distance_cos(i.embedding, (SELECT embedding FROM items WHERE id = ?1)) AS d
FROM items i WHERE i.gen = ?2 AND i.file_id != (SELECT file_id FROM items WHERE id = ?1)
ORDER BY d LIMIT 10`

func randUnit(r *rand.Rand) []float32 {
	v := make([]float32, dim)
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

func cosDist(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return 1 - dot/math.Sqrt(na*nb)
}

func vecJSON(v []float32) string { b, _ := json.Marshal(v); return string(b) }

// fill inserts n items across 2 generations (gen 1 = active, gen 2 = building) and returns gen-1 vectors by id.
func fill(t testing.TB, db *sql.DB, n int, r *rand.Rand) map[int64][]float32 {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	vecs := map[int64][]float32{}
	for f := 0; f < n/4; f++ { // 4 items per file
		if _, err := tx.Exec(`INSERT INTO files VALUES (?, 1, ?, 'image', 1, 1)`, f+1, fmt.Sprintf("/p/%d.png", f)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= n; i++ {
		v := randUnit(r)
		gen := 1
		if i%10 == 0 {
			gen = 2
		}
		if _, err := tx.Exec(`INSERT INTO items (id, gen, file_id, modality, snippet, embedding)
			VALUES (?, ?, ?, 'image', 's', vector32(?))`, i, gen, (i-1)/4+1, vecJSON(v)); err != nil {
			t.Fatal(err)
		}
		if gen == 1 {
			vecs[int64(i)] = v
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return vecs
}

func open(t testing.TB) *sql.DB {
	db, err := sql.Open("turso", filepath.Join(t.TempDir(), "spike.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	return db
}

// The search order must match a brute-force cosine in Go, and gen 2 must never appear.
func TestSearchMatchesBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	db := open(t)
	vecs := fill(t, db, 1000, r)
	q := randUnit(r)

	rows, err := db.Query(searchSQL, vecJSON(q), 1)
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	var dists []float64
	for rows.Next() {
		var id, fileID, unit int64
		var path, kind, snippet, modality string
		var d float64
		if err := rows.Scan(&id, &fileID, &path, &kind, &unit, &snippet, &modality, &d); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
		dists = append(dists, d)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	type pair struct {
		id int64
		d  float64
	}
	var want []pair
	for id, v := range vecs {
		want = append(want, pair{id, cosDist(q, v)})
	}
	sort.Slice(want, func(i, j int) bool { return want[i].d < want[j].d })

	if len(got) != 300 {
		t.Fatalf("got %d rows, want 300", len(got))
	}
	for i := 0; i < 50; i++ {
		if got[i] != want[i].id || math.Abs(dists[i]-want[i].d) > 1e-5 {
			t.Fatalf("rank %d: got id %d d=%.6f, want id %d d=%.6f", i, got[i], dists[i], want[i].id, want[i].d)
		}
	}
	for _, id := range got {
		if id%10 == 0 {
			t.Fatalf("building-generation item %d leaked into search", id)
		}
	}
	t.Logf("top-50 order and distances match brute force (first d=%.6f)", dists[0])

	// Find similar: the item itself is excluded with its file, and the ranking matches brute force.
	const seed = 1
	srows, err := db.Query(similarSQL, seed, 1)
	if err != nil {
		t.Fatal(err)
	}
	var sim []int64
	for srows.Next() {
		var id int64
		var d float64
		if err := srows.Scan(&id, &d); err != nil {
			t.Fatal(err)
		}
		sim = append(sim, id)
	}
	var wantSim []pair
	for id, v := range vecs {
		if (id-1)/4 != (seed-1)/4 {
			wantSim = append(wantSim, pair{id, cosDist(vecs[seed], v)})
		}
	}
	sort.Slice(wantSim, func(i, j int) bool { return wantSim[i].d < wantSim[j].d })
	for i := range sim {
		if sim[i] != wantSim[i].id {
			t.Fatalf("similar rank %d: got %d want %d", i, sim[i], wantSim[i].id)
		}
	}
	t.Logf("find-similar subquery matches brute force (%d rows)", len(sim))

	// Round trip: what is stored is float32[768].
	var blob []byte
	if err := db.QueryRow(`SELECT embedding FROM items WHERE id = 1`).Scan(&blob); err != nil {
		t.Fatal(err)
	}
	if len(blob) != dim*4 {
		t.Fatalf("blob is %d bytes, want %d", len(blob), dim*4)
	}
}

// Timing at the SPEC budget size.
func TestSearch100k(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	r := rand.New(rand.NewSource(2))
	db := open(t)
	start := time.Now()
	fill(t, db, 100_000, r)
	t.Logf("insert 100k items: %s", time.Since(start).Round(time.Millisecond))
	q := vecJSON(randUnit(r))
	for run := 0; run < 3; run++ {
		start = time.Now()
		rows, err := db.Query(searchSQL, q, 1)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for rows.Next() {
			n++
		}
		t.Logf("search run %d: %d rows in %s", run+1, n, time.Since(start).Round(time.Millisecond))
	}
}

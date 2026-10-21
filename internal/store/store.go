// Package store implements contract.Store on Turso (tursogo). It holds the schema, its migrations,
// the queries of SPEC section 10 and the transactional write of a file's items and file_index row.
// The vector regression test from spike 1 lives in this package.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
	_ "turso.tech/database/tursogo" // registers the "turso" driver
)

// dbFile is the database file in the data root (SPEC section 10).
const dbFile = "lookback.db"

// vecDim is the length of every stored vector (float32[768], SPEC section 9).
const vecDim = 768

// Store is the Turso implementation of contract.Store.
type Store struct {
	db *sql.DB
}

var _ contract.Store = (*Store)(nil)

// Open opens or creates <dataRoot>/lookback.db, applies the migrations and returns the store. It sets
// TURSO_GO_CACHE_DIR to <dataRoot>/lib (SPEC section 2) first. The driver reads that variable once, when
// it loads its native library on the first Open of the process, so the first root wins for the process:
// call Open with the data root before any other Turso use.
func Open(dataRoot string) (*Store, error) {
	if strings.Contains(dataRoot, "?") {
		return nil, fmt.Errorf("store: data root %q contains unsupported '?'", dataRoot)
	}
	lib := filepath.Join(dataRoot, "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		return nil, fmt.Errorf("store: create %s: %w", lib, err)
	}
	if err := os.Setenv("TURSO_GO_CACHE_DIR", lib); err != nil {
		return nil, fmt.Errorf("store: set TURSO_GO_CACHE_DIR: %w", err)
	}
	db, err := sql.Open("turso", filepath.Join(dataRoot, dbFile))
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// One connection: the store serializes its reads and writes on it, so a search and a commit never
	// run on two database handles over one file. Every query inside a transaction must use the transaction,
	// and rows are closed before the next query.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// migrate applies the migrations that the database has not run yet, each in its own transaction.
func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("store: read schema version: %w", err)
	}
	for v := version; v < len(migrations); v++ {
		err := s.write(ctx, func(tx *sql.Tx) error {
			if _, err := tx.Exec(migrations[v]); err != nil {
				return err
			}
			_, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v+1))
			return err
		})
		if err != nil {
			return fmt.Errorf("store: migration %d: %w", v+1, err)
		}
	}
	return nil
}

// write runs fn in one transaction. A context that is done before the commit, or any error from fn, rolls
// the transaction back, so the write changes nothing.
func (s *Store) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after a successful Commit
	if err := fn(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// vectorJSON encodes a vector as the JSON array that vector32 reads (SPEC section 10, Q17).
func vectorJSON(v []float32) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// nanos converts a time to Unix nanoseconds; the zero time is stored as now.
func nanos(t time.Time) int64 {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UnixNano()
}

// GetSetting returns the value of key; ok is false when it is not set.
func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return v, true, nil
}

// SetSetting stores a value for key.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
		return err
	})
}

// SaveFolder inserts f when f.ID is 0 and upserts it otherwise. It sets f.ID and f.AddedAt.
func (s *Store) SaveFolder(ctx context.Context, f *contract.Folder) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		added := nanos(f.AddedAt)
		if f.ID == 0 {
			return tx.QueryRow(`INSERT INTO folders (path, ignore, added_at) VALUES (?, ?, ?) RETURNING id`,
				f.Path, f.Ignore, added).Scan(&f.ID)
		}
		_, err := tx.Exec(`INSERT INTO folders (id, path, ignore, added_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET path = excluded.path, ignore = excluded.ignore, added_at = excluded.added_at`,
			f.ID, f.Path, f.Ignore, added)
		if err == nil {
			f.AddedAt = time.Unix(0, added)
		}
		return err
	})
}

// ListFolders returns every folder, oldest first.
func (s *Store) ListFolders(ctx context.Context) ([]contract.Folder, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, path, ignore, added_at FROM folders ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.Folder
	for rows.Next() {
		var f contract.Folder
		var added int64
		if err := rows.Scan(&f.ID, &f.Path, &f.Ignore, &added); err != nil {
			return nil, err
		}
		f.AddedAt = time.Unix(0, added)
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteFolder removes a folder, its files and every row of those files.
func (s *Store) DeleteFolder(ctx context.Context, id int64) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		const files = `SELECT id FROM files WHERE folder_id = ?`
		if _, err := tx.Exec(`DELETE FROM items WHERE file_id IN (`+files+`)`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM file_index WHERE file_id IN (`+files+`)`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM files WHERE folder_id = ?`, id); err != nil {
			return err
		}
		_, err := tx.Exec(`DELETE FROM folders WHERE id = ?`, id)
		return err
	})
}

// SaveFile inserts f when f.ID is 0, keyed by path, and upserts it otherwise. It sets f.ID.
func (s *Store) SaveFile(ctx context.Context, f *contract.FileRec) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if f.ID == 0 {
			return tx.QueryRow(`INSERT INTO files (folder_id, path, kind, size, mtime) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (path) DO UPDATE SET folder_id = excluded.folder_id, kind = excluded.kind,
				  size = excluded.size, mtime = excluded.mtime
				RETURNING id`, f.FolderID, f.Path, string(f.Kind), f.Size, f.MTime).Scan(&f.ID)
		}
		_, err := tx.Exec(`INSERT INTO files (id, folder_id, path, kind, size, mtime) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET folder_id = excluded.folder_id, path = excluded.path,
			  kind = excluded.kind, size = excluded.size, mtime = excluded.mtime`,
			f.ID, f.FolderID, f.Path, string(f.Kind), f.Size, f.MTime)
		return err
	})
}

// ListFiles returns the files of one folder, by ID.
func (s *Store) ListFiles(ctx context.Context, folderID int64) ([]contract.FileRec, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, folder_id, path, kind, size, mtime FROM files WHERE folder_id = ? ORDER BY id`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.FileRec
	for rows.Next() {
		var f contract.FileRec
		var kind string
		if err := rows.Scan(&f.ID, &f.FolderID, &f.Path, &kind, &f.Size, &f.MTime); err != nil {
			return nil, err
		}
		f.Kind = contract.Kind(kind)
		out = append(out, f)
	}
	return out, rows.Err()
}

// DeleteFile removes a file and its file_index and items rows in every generation.
func (s *Store) DeleteFile(ctx context.Context, id int64) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		return deleteFileRows(tx, id)
	})
}

// deleteFileRows removes a file's items, file_index rows and file row.
func deleteFileRows(tx *sql.Tx, id int64) error {
	for _, q := range []string{
		`DELETE FROM items WHERE file_id = ?`,
		`DELETE FROM file_index WHERE file_id = ?`,
		`DELETE FROM files WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return nil
}

// CountFiles returns the number of files of each kind in one folder.
func (s *Store) CountFiles(ctx context.Context, folderID int64) (map[contract.Kind]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT kind, COUNT(*) FROM files WHERE folder_id = ? GROUP BY kind`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[contract.Kind]int{}
	for rows.Next() {
		var kind string
		var n int
		if err := rows.Scan(&kind, &n); err != nil {
			return nil, err
		}
		out[contract.Kind(kind)] = n
	}
	return out, rows.Err()
}

// ListFileIndex returns the file_index rows of one generation, by file ID.
func (s *Store) ListFileIndex(ctx context.Context, gen int64) ([]contract.FileIndex, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT file_id, gen, size, mtime, status, COALESCE(error, ''),
		COALESCE(tokens, 0), COALESCE(ms, 0) FROM file_index WHERE gen = ? ORDER BY file_id`, gen)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.FileIndex
	for rows.Next() {
		var ix contract.FileIndex
		var status string
		if err := rows.Scan(&ix.FileID, &ix.Gen, &ix.Size, &ix.MTime, &status, &ix.Error, &ix.Tokens, &ix.Ms); err != nil {
			return nil, err
		}
		ix.Status = contract.Status(status)
		out = append(out, ix)
	}
	return out, rows.Err()
}

// ClearFileIndex removes a file's file_index row and items in one generation.
func (s *Store) ClearFileIndex(ctx context.Context, fileID, gen int64) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := requireFile(tx, fileID); err != nil {
			return err
		}
		for _, q := range []string{
			`DELETE FROM items WHERE file_id = ? AND gen = ?`,
			`DELETE FROM file_index WHERE file_id = ? AND gen = ?`,
		} {
			if _, err := tx.Exec(q, fileID, gen); err != nil {
				return err
			}
		}
		return nil
	})
}

// requireFile returns contract.ErrUnknownFile when no file has the ID.
func requireFile(tx *sql.Tx, id int64) error {
	var one int
	err := tx.QueryRow(`SELECT 1 FROM files WHERE id = ?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return contract.ErrUnknownFile
	}
	return err
}

// requireGen returns contract.ErrUnknownGen when no generation has the ID.
func requireGen(tx *sql.Tx, id int64) (string, error) {
	var state string
	err := tx.QueryRow(`SELECT state FROM generations WHERE id = ?`, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", contract.ErrUnknownGen
	}
	return state, err
}

// CommitFile replaces the items of idx.FileID in idx.Gen and upserts its file_index row, in one transaction.
// The store assigns the item IDs. It copies each vector. Nothing changes on error.
func (s *Store) CommitFile(ctx context.Context, idx contract.FileIndex, items []contract.Item) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if err := requireFile(tx, idx.FileID); err != nil {
			return err
		}
		if _, err := requireGen(tx, idx.Gen); err != nil {
			return err
		}
		if !terminal(idx.Status) {
			return fmt.Errorf("store: status %q must be done, error or skipped", idx.Status)
		}
		for _, it := range items {
			if it.Gen != idx.Gen || it.FileID != idx.FileID {
				return errors.New("store: item does not belong to the file and generation")
			}
			if len(it.Vector) != vecDim {
				return fmt.Errorf("store: vector has %d values, want %d", len(it.Vector), vecDim)
			}
		}
		if _, err := tx.Exec(`DELETE FROM items WHERE file_id = ? AND gen = ?`, idx.FileID, idx.Gen); err != nil {
			return err
		}
		if err := upsertFileIndex(tx, idx); err != nil {
			return err
		}
		ins, err := tx.Prepare(`INSERT INTO items (gen, file_id, unit, start, "end", modality, snippet, embedding)
			VALUES (?, ?, ?, ?, ?, ?, ?, vector32(?))`)
		if err != nil {
			return err
		}
		defer ins.Close()
		for _, it := range items {
			vec, err := vectorJSON(it.Vector)
			if err != nil {
				return err
			}
			if _, err := ins.Exec(it.Gen, it.FileID, it.Unit, nullIfNegative(it.Start), nullIfNegative(it.End),
				it.Modality, it.Snippet, vec); err != nil {
				return err
			}
		}
		return nil
	})
}

// terminal reports whether st is a status a finished file_index row has (SPEC section 10).
func terminal(st contract.Status) bool {
	return st == contract.StatusDone || st == contract.StatusError || st == contract.StatusSkipped
}

// nullIfNegative stores the byte range of an image, which has none (-1), as NULL.
func nullIfNegative(v int) any {
	if v < 0 {
		return nil
	}
	return v
}

// upsertFileIndex writes one file_index row.
func upsertFileIndex(tx *sql.Tx, ix contract.FileIndex) error {
	var errText any
	if ix.Error != "" {
		errText = ix.Error
	}
	_, err := tx.Exec(`INSERT INTO file_index (file_id, gen, size, mtime, status, error, tokens, ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (file_id, gen) DO UPDATE SET size = excluded.size, mtime = excluded.mtime,
		  status = excluded.status, error = excluded.error, tokens = excluded.tokens, ms = excluded.ms`,
		ix.FileID, ix.Gen, ix.Size, ix.MTime, string(ix.Status), errText, ix.Tokens, ix.Ms)
	return err
}

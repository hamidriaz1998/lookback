package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// RecentItems returns the limit most recently modified files that are indexed (status done) in gen, one hit
// each, with the item ID of the file's first item. Files without items are left out before the limit applies.
func (s *Store) RecentItems(ctx context.Context, gen int64, limit int) ([]contract.Hit, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.id, f.path, f.kind, i.id, i.modality, i.unit, COALESCE(i.snippet, '')
		FROM file_index x
		JOIN files f ON f.id = x.file_id
		JOIN items i ON i.gen = x.gen AND i.file_id = f.id
		  AND i.id = (SELECT MIN(id) FROM items WHERE gen = x.gen AND file_id = f.id)
		WHERE x.gen = ? AND x.status = 'done'
		ORDER BY f.mtime DESC, f.id
		LIMIT ?`, gen, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.Hit
	for rows.Next() {
		var h contract.Hit
		var kind string
		if err := rows.Scan(&h.FileID, &h.Path, &kind, &h.ItemID, &h.Modality, &h.Unit, &h.Snippet); err != nil {
			return nil, err
		}
		h.Kind = contract.Kind(kind)
		out = append(out, h)
	}
	return out, rows.Err()
}

// Search returns the best item of each file in gen, nearest to query first, at most limit hits. kinds, when
// not empty, limits the files to those kinds. Score is the cosine similarity, 1 minus the cosine distance.
//
// ponytail: a linear scan over every item of gen; about 230 ms at 100k items (spike 1). Use a vector index
// when Turso has one.
func (s *Store) Search(ctx context.Context, gen int64, query []float32, kinds []contract.Kind, limit int) ([]contract.Hit, error) {
	if len(query) != vecDim {
		return nil, fmt.Errorf("store: query has %d values, want %d", len(query), vecDim)
	}
	q, err := vectorJSON(query)
	if err != nil {
		return nil, err
	}
	where, args := kindFilter(kinds)
	sqlText := `SELECT i.id, i.file_id, f.path, f.kind, i.modality, i.unit, COALESCE(i.snippet, ''),
		vector_distance_cos(i.embedding, vector32(?)) AS d
		FROM items i JOIN files f ON f.id = i.file_id
		WHERE i.gen = ?` + where + ` ORDER BY d`
	return s.hits(ctx, sqlText, append([]any{q, gen}, args...), limit)
}

// Similar returns the files nearest to the stored vector of itemID, excluding its own file. The seed must be
// an item of gen; otherwise it returns contract.ErrStaleItem, since its vector is in another space.
func (s *Store) Similar(ctx context.Context, gen int64, itemID int64, kinds []contract.Kind, limit int) ([]contract.Hit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var seedFile int64
	err := s.db.QueryRowContext(ctx, `SELECT file_id FROM items WHERE id = ? AND gen = ?`, itemID, gen).Scan(&seedFile)
	if errors.Is(err, sql.ErrNoRows) {
		if _, gerr := s.generation(ctx, gen); gerr != nil {
			return nil, gerr
		}
		return nil, contract.ErrStaleItem
	}
	if err != nil {
		return nil, err
	}
	where, args := kindFilter(kinds)
	sqlText := `SELECT i.id, i.file_id, f.path, f.kind, i.modality, i.unit, COALESCE(i.snippet, ''),
		vector_distance_cos(i.embedding, (SELECT embedding FROM items WHERE id = ? AND gen = ?)) AS d
		FROM items i JOIN files f ON f.id = i.file_id
		WHERE i.gen = ? AND i.file_id <> ?` + where + ` ORDER BY d`
	return s.hits(ctx, sqlText, append([]any{itemID, gen, gen, seedFile}, args...), limit)
}

// generation returns contract.ErrUnknownGen when gen does not exist.
func (s *Store) generation(ctx context.Context, gen int64) (string, error) {
	var state string
	err := s.db.QueryRowContext(ctx, `SELECT state FROM generations WHERE id = ?`, gen).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", contract.ErrUnknownGen
	}
	return state, err
}

// hits runs a query whose rows are items ordered by distance d, and keeps the first (nearest) item of each
// file, up to limit files. It stops reading once it has them, so it never builds the full list.
// Each row is: item id, file id, path, kind, modality, unit, snippet, d.
func (s *Store) hits(ctx context.Context, sqlText string, args []any, limit int) ([]contract.Hit, error) {
	rows, err := s.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.Hit
	seen := map[int64]bool{}
	for len(out) < limit && rows.Next() {
		var h contract.Hit
		var kind string
		var d float64
		if err := rows.Scan(&h.ItemID, &h.FileID, &h.Path, &kind, &h.Modality, &h.Unit, &h.Snippet, &d); err != nil {
			return nil, err
		}
		if seen[h.FileID] {
			continue
		}
		seen[h.FileID] = true
		h.Kind = contract.Kind(kind)
		h.Score = 1 - d
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// kindFilter returns the SQL for a kind filter and its arguments. Empty kinds means no filter.
func kindFilter(kinds []contract.Kind) (string, []any) {
	if len(kinds) == 0 {
		return "", nil
	}
	marks := make([]string, len(kinds))
	args := make([]any, len(kinds))
	for i, k := range kinds {
		marks[i] = "?"
		args[i] = string(k)
	}
	return " AND f.kind IN (" + strings.Join(marks, ", ") + ")", args
}

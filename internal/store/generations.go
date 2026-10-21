package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hamidriaz1998/lookback/internal/contract"
)

// NewGeneration inserts g in state building and sets g.ID.
func (s *Store) NewGeneration(ctx context.Context, g *contract.Generation) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		created := nanos(g.CreatedAt)
		if err := tx.QueryRow(`INSERT INTO generations (text_quant, enc_quant, model_rev, state, created_at)
			VALUES (?, ?, ?, ?, ?) RETURNING id`,
			string(g.Space.TextQuant), string(g.Space.EncQuant), g.Space.ModelRev, string(contract.GenBuilding), created,
		).Scan(&g.ID); err != nil {
			return err
		}
		g.State = contract.GenBuilding
		g.CreatedAt = time.Unix(0, created)
		return nil
	})
}

// ListGenerations returns every generation, oldest first.
func (s *Store) ListGenerations(ctx context.Context) ([]contract.Generation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, text_quant, enc_quant, model_rev, state, created_at FROM generations ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.Generation
	for rows.Next() {
		var g contract.Generation
		var tq, eq, state string
		var created int64
		if err := rows.Scan(&g.ID, &tq, &eq, &g.Space.ModelRev, &state, &created); err != nil {
			return nil, err
		}
		g.Space.TextQuant, g.Space.EncQuant = contract.Quant(tq), contract.Quant(eq)
		g.State = contract.GenState(state)
		g.CreatedAt = time.Unix(0, created)
		out = append(out, g)
	}
	return out, rows.Err()
}

// completeSQL is 1 when every file has a file_index row in the generation ?1 whose size and mtime equal the
// file's, with a terminal status (SPEC section 10). It returns 0 for a generation with a file that is not
// covered.
const completeSQL = `SELECT NOT EXISTS (
	SELECT 1 FROM files f LEFT JOIN file_index x ON x.file_id = f.id AND x.gen = ?1
	WHERE x.file_id IS NULL OR x.status NOT IN ('done', 'error', 'skipped')
	   OR x.size <> f.size OR x.mtime <> f.mtime)`

// GenerationComplete reports whether every tracked file has a file_index row in gen whose size and mtime
// equal the file's current ones, with status done, error or skipped (SPEC section 10).
func (s *Store) GenerationComplete(ctx context.Context, gen int64) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM generations WHERE id = ?`, gen).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, contract.ErrUnknownGen
	}
	if err != nil {
		return false, err
	}
	return complete(ctx, s.db, gen)
}

// queryRower is the part of *sql.DB and *sql.Tx that complete needs.
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// complete runs completeSQL on q.
func complete(ctx context.Context, q queryRower, gen int64) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx, completeSQL, gen).Scan(&ok)
	return ok, err
}

// ActivateGeneration makes gen the active generation and retires the previous active one, in one transaction.
// It returns contract.ErrIncomplete, and changes nothing, when gen does not cover every file. The retired
// generation's items stay until DeleteItems removes them.
func (s *Store) ActivateGeneration(ctx context.Context, gen int64) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		state, err := requireGen(tx, gen)
		if err != nil {
			return err
		}
		if contract.GenState(state) == contract.GenRetired {
			return fmt.Errorf("store: generation %d is retired and cannot be activated", gen)
		}
		ok, err := complete(ctx, tx, gen)
		if err != nil {
			return err
		}
		if !ok {
			return contract.ErrIncomplete
		}
		if _, err := tx.Exec(`UPDATE generations SET state = ? WHERE state = ? AND id <> ?`,
			string(contract.GenRetired), string(contract.GenActive), gen); err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE generations SET state = ? WHERE id = ?`, string(contract.GenActive), gen)
		return err
	})
}

// DeleteItems removes the items and file_index rows of one generation. It refuses the active one.
func (s *Store) DeleteItems(ctx context.Context, gen int64) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var state string
		err := tx.QueryRow(`SELECT state FROM generations WHERE id = ?`, gen).Scan(&state)
		if err == nil && contract.GenState(state) == contract.GenActive {
			return fmt.Errorf("store: refusing to delete the active generation %d", gen)
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM items WHERE gen = ?`, gen); err != nil {
			return err
		}
		_, err = tx.Exec(`DELETE FROM file_index WHERE gen = ?`, gen)
		return err
	})
}

// SaveCombo inserts or replaces an installed combination, keyed by Combo.ID. It stores a copy of the
// benchmark (as JSON).
func (s *Store) SaveCombo(ctx context.Context, in contract.Installed) error {
	var bench any
	if in.Bench != nil {
		b, err := json.Marshal(in.Bench)
		if err != nil {
			return err
		}
		bench = string(b)
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO combos (id, backend, text_quant, enc_quant, installed_at, bench_json)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (id) DO UPDATE SET backend = excluded.backend, text_quant = excluded.text_quant,
			  enc_quant = excluded.enc_quant, installed_at = excluded.installed_at, bench_json = excluded.bench_json`,
			in.Combo.ID(), string(in.Combo.Backend), string(in.Combo.TextQuant), string(in.Combo.EncQuant),
			nanos(in.InstalledAt), bench)
		return err
	})
}

// ListCombos returns every installed combination, by ID. Each one is a copy.
func (s *Store) ListCombos(ctx context.Context) ([]contract.Installed, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT backend, text_quant, enc_quant, installed_at, bench_json
		FROM combos ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.Installed
	for rows.Next() {
		var in contract.Installed
		var backend, tq, eq string
		var installed int64
		var bench sql.NullString
		if err := rows.Scan(&backend, &tq, &eq, &installed, &bench); err != nil {
			return nil, err
		}
		in.Combo = contract.Combo{Backend: contract.Backend(backend), TextQuant: contract.Quant(tq), EncQuant: contract.Quant(eq)}
		in.InstalledAt = time.Unix(0, installed)
		if bench.Valid {
			in.Bench = &contract.BenchResult{}
			if err := json.Unmarshal([]byte(bench.String), in.Bench); err != nil {
				return nil, fmt.Errorf("store: combo %s bench: %w", in.Combo.ID(), err)
			}
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// DeleteCombo removes an installed combination.
func (s *Store) DeleteCombo(ctx context.Context, c contract.Combo) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM combos WHERE id = ?`, c.ID())
		return err
	})
}

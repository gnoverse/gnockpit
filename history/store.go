// Package history persists per-block validator signing outcomes in SQLite and
// answers per-window "how many blocks did this validator miss" queries. Data is
// recorded forward as blocks are observed; it is never backfilled.
package history

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

const schema = `
CREATE TABLE IF NOT EXISTS signing_blocks (
	height     INTEGER PRIMARY KEY,
	block_time INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS signing_misses (
	height      INTEGER NOT NULL,
	val_address TEXT NOT NULL,
	block_time  INTEGER NOT NULL,
	PRIMARY KEY (height, val_address)
);
CREATE INDEX IF NOT EXISTS idx_signing_blocks_time ON signing_blocks(block_time);
CREATE INDEX IF NOT EXISTS idx_signing_misses_time ON signing_misses(block_time);
`

// Store persists block-signing history in SQLite. It is safe for use by the
// single-connection database the rest of gnockpit shares.
type Store struct {
	db *sql.DB
}

// Block is one block's signing outcome to record: its height, its time, and the
// addresses of validators that were expected to sign but did not.
type Block struct {
	Height  int64
	Time    time.Time
	Missing []string
}

// WindowCounts summarizes missed blocks within a time window: the per-validator
// missed count and the total number of blocks recorded in the window.
type WindowCounts struct {
	Missed map[string]int
	Blocks int
}

// NewStore initializes the history schema on db and returns a Store. db is
// expected to already be configured (a single shared connection is fine).
func NewStore(ctx context.Context, db *sql.DB) (*Store, error) {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("init history schema: %w", err)
	}
	return &Store{db: db}, nil
}

// RecordBlocks persists each block and its missing validators, ignoring any
// block height already recorded so overlapping fetch windows never double-count.
func (s *Store) RecordBlocks(ctx context.Context, blocks []Block) error {
	if len(blocks) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	for _, b := range blocks {
		unix := b.Time.Unix()
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO signing_blocks(height, block_time) VALUES(?, ?)`,
			b.Height, unix,
		); err != nil {
			return fmt.Errorf("insert block %d: %w", b.Height, err)
		}
		for _, addr := range b.Missing {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO signing_misses(height, val_address, block_time) VALUES(?, ?, ?)`,
				b.Height, addr, unix,
			); err != nil {
				return fmt.Errorf("insert miss %d/%s: %w", b.Height, addr, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit blocks: %w", err)
	}
	return nil
}

// MissedInWindow returns per-validator missed-block counts and the total blocks
// recorded within [now-window, now]. A window of zero or less means all history.
func (s *Store) MissedInWindow(ctx context.Context, window time.Duration, now time.Time) (WindowCounts, error) {
	var cutoff int64
	if window > 0 {
		cutoff = now.Add(-window).Unix()
	}
	wc := WindowCounts{Missed: make(map[string]int)}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM signing_blocks WHERE block_time >= ?`, cutoff,
	).Scan(&wc.Blocks); err != nil {
		return wc, fmt.Errorf("count blocks: %w", err)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT val_address, COUNT(*) FROM signing_misses WHERE block_time >= ? GROUP BY val_address`,
		cutoff,
	)
	if err != nil {
		return wc, fmt.Errorf("query misses: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var addr string
		var n int
		if err := rows.Scan(&addr, &n); err != nil {
			return wc, fmt.Errorf("scan miss: %w", err)
		}
		wc.Missed[addr] = n
	}
	if err := rows.Err(); err != nil {
		return wc, fmt.Errorf("iterate misses: %w", err)
	}
	return wc, nil
}

// WindowReport holds missed-block data across several time windows. Blocks[i]
// and Missed[addr][i] correspond to Windows[i].
type WindowReport struct {
	Windows []time.Duration
	Blocks  []int
	Missed  map[string][]int
}

// MissedByWindows returns, for each window, the total blocks recorded and the
// per-validator missed count. A window of zero or less means all history.
func (s *Store) MissedByWindows(ctx context.Context, windows []time.Duration, now time.Time) (WindowReport, error) {
	rep := WindowReport{
		Windows: windows,
		Blocks:  make([]int, len(windows)),
		Missed:  make(map[string][]int),
	}
	for i, w := range windows {
		wc, err := s.MissedInWindow(ctx, w, now)
		if err != nil {
			return rep, err
		}
		rep.Blocks[i] = wc.Blocks
		for addr, n := range wc.Missed {
			if rep.Missed[addr] == nil {
				rep.Missed[addr] = make([]int, len(windows))
			}
			rep.Missed[addr][i] = n
		}
	}
	return rep, nil
}

// EarliestRecorded returns the time of the oldest block still retained, or the
// zero time if no blocks have been recorded. After pruning this advances, so it
// marks how far back the counts reach — not when recording first began.
func (s *Store) EarliestRecorded(ctx context.Context) (time.Time, error) {
	var earliest sql.NullInt64
	if err := s.db.QueryRowContext(ctx,
		`SELECT MIN(block_time) FROM signing_blocks`,
	).Scan(&earliest); err != nil {
		return time.Time{}, fmt.Errorf("min block_time: %w", err)
	}
	if !earliest.Valid {
		return time.Time{}, nil
	}
	return time.Unix(earliest.Int64, 0).UTC(), nil
}

// Prune deletes all blocks and misses recorded before cutoff, atomically so the
// two tables never diverge.
func (s *Store) Prune(ctx context.Context, cutoff time.Time) error {
	c := cutoff.Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin prune tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM signing_misses WHERE block_time < ?`, c); err != nil {
		return fmt.Errorf("prune misses: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM signing_blocks WHERE block_time < ?`, c); err != nil {
		return fmt.Errorf("prune blocks: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit prune: %w", err)
	}
	return nil
}

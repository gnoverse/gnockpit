// Package eta estimates when the chain reaches a block height, and which height
// it reaches at a given time, with a margin learned from the chain's own block
// times. It keeps a sampled history of block times in SQLite, backfilled from
// an RPC source, and tracks the halt governance schedules on chain.
package eta

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"
)

const schema = `
CREATE TABLE IF NOT EXISTS eta_block_times (
	height     INTEGER PRIMARY KEY,
	block_time INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS eta_meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS eta_halts (
	height     INTEGER PRIMARY KEY,
	first_seen INTEGER NOT NULL
);
`

const (
	metaChainID = "chain_id"
	metaFloor   = "floor"
)

// Sample is one block's height and time.
type Sample struct {
	Height int64
	Time   time.Time
}

// Store persists sampled block times, the chain they belong to, and when each
// scheduled halt was first seen. Times are kept to the millisecond.
type Store struct {
	db *sql.DB
}

// NewStore initializes the schema on db, which may be shared with other stores.
func NewStore(ctx context.Context, db *sql.DB) (*Store, error) {
	if _, err := db.ExecContext(ctx, schema); err != nil {
		return nil, fmt.Errorf("init eta schema: %w", err)
	}
	return &Store{db: db}, nil
}

// AddSamples stores block times; a height already stored keeps its time.
func (s *Store) AddSamples(ctx context.Context, samples []Sample) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	for _, sm := range samples {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO eta_block_times(height, block_time) VALUES(?, ?)`,
			sm.Height, sm.Time.UnixMilli(),
		); err != nil {
			return fmt.Errorf("insert block time %d: %w", sm.Height, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit block times: %w", err)
	}
	return nil
}

// Samples returns every stored block time, by ascending height.
func (s *Store) Samples(ctx context.Context) ([]Sample, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT height, block_time FROM eta_block_times ORDER BY height`)
	if err != nil {
		return nil, fmt.Errorf("query block times: %w", err)
	}
	defer rows.Close()
	var out []Sample
	for rows.Next() {
		var h, ms int64
		if err := rows.Scan(&h, &ms); err != nil {
			return nil, fmt.Errorf("scan block time: %w", err)
		}
		out = append(out, Sample{Height: h, Time: time.UnixMilli(ms).UTC()})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate block times: %w", err)
	}
	return out, nil
}

// ChainID returns the chain the stored data belongs to, or "" before Reset.
func (s *Store) ChainID(ctx context.Context) (string, error) {
	return s.meta(ctx, metaChainID)
}

// Floor returns the lowest height the RPC source could serve, or 0 when the
// backfill never hit one.
func (s *Store) Floor(ctx context.Context) (int64, error) {
	v, err := s.meta(ctx, metaFloor)
	if err != nil || v == "" {
		return 0, err
	}
	h, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("floor %q: %w", v, err)
	}
	return h, nil
}

// SetFloor records the lowest height the RPC source could serve.
func (s *Store) SetFloor(ctx context.Context, height int64) error {
	return s.setMeta(ctx, metaFloor, strconv.FormatInt(height, 10))
}

// NoteHalt records that the halt at height was seen at seen, unless it was
// seen before, and returns when it was first seen.
func (s *Store) NoteHalt(ctx context.Context, height int64, seen time.Time) (time.Time, error) {
	if _, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO eta_halts(height, first_seen) VALUES(?, ?)`, height, seen.UnixMilli(),
	); err != nil {
		return time.Time{}, fmt.Errorf("insert halt %d: %w", height, err)
	}
	var ms int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT first_seen FROM eta_halts WHERE height = ?`, height,
	).Scan(&ms); err != nil {
		return time.Time{}, fmt.Errorf("query halt %d: %w", height, err)
	}
	return time.UnixMilli(ms).UTC(), nil
}

// Reset drops all stored data and marks the store as holding chainID's.
func (s *Store) Reset(ctx context.Context, chainID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin reset tx: %w", err)
	}
	defer tx.Rollback()
	for _, stmt := range []string{
		`DELETE FROM eta_block_times`,
		`DELETE FROM eta_halts`,
		`DELETE FROM eta_meta`,
	} {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("reset: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO eta_meta(key, value) VALUES(?, ?)`, metaChainID, chainID,
	); err != nil {
		return fmt.Errorf("reset chain id: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit reset: %w", err)
	}
	return nil
}

func (s *Store) meta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM eta_meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", key, err)
	}
	return v, nil
}

func (s *Store) setMeta(ctx context.Context, key, value string) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO eta_meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	); err != nil {
		return fmt.Errorf("write %s: %w", key, err)
	}
	return nil
}

package blocks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Run is a validator's consecutive heights absent from the commit or voting
// for another block.
type Run struct {
	First, Last int64
}

// runsOfQuery reads a validator's runs of at least a length, by key.
const runsOfQuery = `SELECT first, last FROM blocks_absence_runs WHERE validator = ? AND last - first + 1 >= ? ORDER BY first`

// runsOf returns the validator's runs of at least minLen heights, ascending.
func runsOf(ctx context.Context, q querier, validator, minLen int64) ([]Run, error) {
	rows, err := q.QueryContext(ctx, runsOfQuery, validator, minLen)
	if err != nil {
		return nil, fmt.Errorf("absence runs of %d: %w", validator, err)
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.First, &r.Last); err != nil {
			return nil, fmt.Errorf("absence runs of %d: %w", validator, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// runAt returns the validator's run holding height, if any.
func runAt(ctx context.Context, q queryRower, validator, height int64) (Run, bool, error) {
	var r Run
	err := q.QueryRowContext(ctx,
		`SELECT first, last FROM blocks_absence_runs WHERE validator = ? AND first <= ? ORDER BY first DESC LIMIT 1`,
		validator, height,
	).Scan(&r.First, &r.Last)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, false, nil
	}
	if err != nil {
		return Run{}, false, fmt.Errorf("absence run of %d at %d: %w", validator, height, err)
	}
	return r, r.Last >= height, nil
}

// extendRun adds, in tx, an absence of the validator at height to its runs:
// it extends the run ending at height−1 or the run starting at height+1, or
// merges both, or starts a run. The fill writes a height once, in either
// direction, so either neighbour may be written first.
func extendRun(ctx context.Context, tx *sql.Tx, validator, height int64) error {
	below, hasBelow, err := runAt(ctx, tx, validator, height-1)
	if err != nil {
		return err
	}
	var aboveLast int64
	err = tx.QueryRowContext(ctx,
		`SELECT last FROM blocks_absence_runs WHERE validator = ? AND first = ?`, validator, height+1,
	).Scan(&aboveLast)
	hasAbove := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("absence run of %d from %d: %w", validator, height+1, err)
	}
	last := height
	if hasAbove {
		last = aboveLast
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM blocks_absence_runs WHERE validator = ? AND first = ?`, validator, height+1,
		); err != nil {
			return fmt.Errorf("merge absence run of %d at %d: %w", validator, height, err)
		}
	}
	if hasBelow && below.Last == height-1 {
		_, err = tx.ExecContext(ctx,
			`UPDATE blocks_absence_runs SET last = ? WHERE validator = ? AND first = ?`, last, validator, below.First)
	} else {
		_, err = tx.ExecContext(ctx,
			`INSERT INTO blocks_absence_runs(validator, first, last) VALUES(?, ?, ?)`, validator, height, last)
	}
	if err != nil {
		return fmt.Errorf("absence run of %d at %d: %w", validator, height, err)
	}
	return nil
}

// fillRuns records the runs of absences of the blocks stored before
// blocks_absence_runs existed, once: one scan of the stored absences, each
// run an island of consecutive heights.
func (s *Store) fillRuns(ctx context.Context) error {
	filled, err := s.meta(ctx, metaRunsFilled)
	if err != nil || filled != "" {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO blocks_absence_runs(validator, first, last)
		 SELECT validator, MIN(height), MAX(height) FROM (
		   SELECT validator, height, height - ROW_NUMBER() OVER (PARTITION BY validator ORDER BY height) AS island
		   FROM blocks_absences WHERE kind IN (?, ?))
		 GROUP BY validator, island`, Absent, OtherBlock,
	); err != nil {
		return fmt.Errorf("fill absence runs: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO blocks_meta(key, value) VALUES(?, '1')`, metaRunsFilled,
	); err != nil {
		return fmt.Errorf("record the absence runs filled: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit absence runs: %w", err)
	}
	return nil
}

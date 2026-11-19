package blocks

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"
)

// RestartGap is how far apart two consecutive blocks' times must be for the
// chain to have restarted between them (the scoring spec's rule).
const RestartGap = 2 * time.Minute

// gapsIn returns the restart gaps from height from to height to, ascending:
// each the height b whose block b+1 came more than RestartGap after it.
func gapsIn(ctx context.Context, q querier, from, to int64) ([]int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT height FROM blocks_gaps WHERE height BETWEEN ? AND ? ORDER BY height`, from, to)
	if err != nil {
		return nil, fmt.Errorf("restart gaps %d-%d: %w", from, to, err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var h int64
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("restart gaps %d-%d: %w", from, to, err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// restartGapMs is RestartGap in the blocks table's milliseconds.
const restartGapMs = int64(RestartGap / time.Millisecond)

// markGaps records, in tx, the restart gaps that writing heights settles: a
// gap at b needs blocks b and b+1 stored, so a written height settles the gap
// below it and its own, whichever direction the fill goes.
func markGaps(ctx context.Context, tx *sql.Tx, heights []int64) error {
	if len(heights) == 0 {
		return nil
	}
	low, high := slices.Min(heights), slices.Max(heights)+1
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO blocks_gaps(height)
		 SELECT p.height FROM blocks b JOIN blocks p ON p.height = b.height - 1
		 WHERE b.height BETWEEN ? AND ? AND b.time_ms - p.time_ms > ?`, low, high, restartGapMs,
	); err != nil {
		return fmt.Errorf("mark restart gaps %d-%d: %w", low, high, err)
	}
	return nil
}

// fillGaps records the restart gaps of the blocks stored before blocks_gaps
// existed, once: one scan of the stored blocks.
func (s *Store) fillGaps(ctx context.Context) error {
	filled, err := s.meta(ctx, metaGapsFilled)
	if err != nil || filled != "" {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO blocks_gaps(height)
		 SELECT below FROM (
		   SELECT height, time_ms, LAG(height) OVER w AS below, LAG(time_ms) OVER w AS below_ms
		   FROM blocks WINDOW w AS (ORDER BY height))
		 WHERE below = height - 1 AND time_ms - below_ms > ?`, restartGapMs,
	); err != nil {
		return fmt.Errorf("fill restart gaps: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO blocks_meta(key, value) VALUES(?, '1')`, metaGapsFilled,
	); err != nil {
		return fmt.Errorf("record the restart gaps filled: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit restart gaps: %w", err)
	}
	return nil
}

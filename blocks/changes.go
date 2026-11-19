package blocks

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
)

// markValsetChanges records, in tx, the validator set changes that writing
// heights settles: a height is a change when it and the height below are both
// stored on different sets, so a written height settles itself and the height
// above, whichever direction the fill goes.
func markValsetChanges(ctx context.Context, tx *sql.Tx, heights []int64) error {
	if len(heights) == 0 {
		return nil
	}
	low, high := slices.Min(heights), slices.Max(heights)+1
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO blocks_valset_changes(height)
		 SELECT b.height FROM blocks b JOIN blocks p ON p.height = b.height - 1
		 WHERE b.height BETWEEN ? AND ? AND b.valset != p.valset`, low, high,
	); err != nil {
		return fmt.Errorf("mark validator set changes %d-%d: %w", low, high, err)
	}
	return nil
}

// fillValsetChanges records the validator set changes of the blocks stored
// before blocks_valset_changes existed, once: one scan of the stored blocks.
func (s *Store) fillValsetChanges(ctx context.Context) error {
	filled, err := s.meta(ctx, metaChangesFilled)
	if err != nil || filled != "" {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO blocks_valset_changes(height)
		 SELECT height FROM (
		   SELECT height, valset, LAG(height) OVER w AS below, LAG(valset) OVER w AS below_valset
		   FROM blocks WINDOW w AS (ORDER BY height))
		 WHERE below = height - 1 AND below_valset != valset`,
	); err != nil {
		return fmt.Errorf("fill validator set changes: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO blocks_meta(key, value) VALUES(?, ?)`, metaChangesFilled, "1",
	); err != nil {
		return fmt.Errorf("record the validator set changes filled: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit validator set changes: %w", err)
	}
	return nil
}

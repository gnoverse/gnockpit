package blocks

import (
	"context"
	"database/sql"
	"fmt"
)

// kindColumns are the blocks_days columns counting each absence kind.
var kindColumns = map[Kind]string{Absent: "absent", Nil: "nil_votes", OtherBlock: "other_block"}

// countAbsence counts, in tx, one absence of kind by validator on day.
func countAbsence(ctx context.Context, tx *sql.Tx, day, validator int64, kind Kind) error {
	col, ok := kindColumns[kind]
	if !ok {
		return fmt.Errorf("unknown absence kind %d", kind)
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO blocks_days(day, validator, `+col+`) VALUES(?, ?, 1)
		 ON CONFLICT(day, validator) DO UPDATE SET `+col+` = `+col+` + 1`, day, validator)
	return err
}

// fillDays counts the absences of the blocks stored before blocks_days
// existed, once: one scan of the stored absences.
func (s *Store) fillDays(ctx context.Context) error {
	filled, err := s.meta(ctx, metaDaysFilled)
	if err != nil || filled != "" {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO blocks_days(day, validator, absent, nil_votes, other_block)
		 SELECT b.time_ms / 86400000, a.validator, SUM(a.kind = ?), SUM(a.kind = ?), SUM(a.kind = ?)
		 FROM blocks_absences a JOIN blocks b ON b.height = a.height
		 WHERE true GROUP BY 1, 2`, Absent, Nil, OtherBlock,
	); err != nil {
		return fmt.Errorf("fill day counters: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO blocks_meta(key, value) VALUES(?, '1')`, metaDaysFilled,
	); err != nil {
		return fmt.Errorf("record the day counters filled: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit day counters: %w", err)
	}
	return nil
}

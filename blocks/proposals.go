package blocks

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"slices"
	"time"
)

// proposalCounts returns, for the block r stored, the rounds each validator
// was the expected proposer of until the block was made, and those it
// missed. k is the earliest round at or below the commit round whose expected
// proposer is the header's: rounds 0 to k count as expected, rounds 0 to k−1
// as missed. A round-0 block is its proposer's alone. ok is false, and
// nothing counts, when k is unknown: the rounds were not computed, a round at
// or below k has no known proposer, or no round's proposer is the header's.
func proposalCounts(r Record) (expected, missed map[string]int64, ok bool) {
	if r.Round == 0 {
		return map[string]int64{r.Proposer: 1}, map[string]int64{}, true
	}
	if len(r.Rounds) == 0 {
		return nil, nil, false
	}
	k := slices.Index(r.Rounds, r.Proposer)
	if k < 0 || k > r.Round || slices.Contains(r.Rounds[:k], "") {
		return nil, nil, false
	}
	expected, missed = map[string]int64{}, map[string]int64{}
	for i, p := range r.Rounds[:k+1] {
		expected[p]++
		if i < k {
			missed[p]++
		}
	}
	return expected, missed, true
}

// countProposals adds, in tx, the expected and missed proposals of a stored
// block to its day's counters.
func countProposals(ctx context.Context, tx *sql.Tx, ids *validatorIDs, r Record) error {
	expected, missed, ok := proposalCounts(r)
	if !ok {
		return nil
	}
	day := dayOf(r.Time)
	for addr, n := range expected {
		vid, err := ids.id(ctx, addr)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO blocks_days(day, validator, proposals_expected, proposals_missed) VALUES(?, ?, ?, ?)
			 ON CONFLICT(day, validator) DO UPDATE SET
			 proposals_expected = proposals_expected + excluded.proposals_expected,
			 proposals_missed = proposals_missed + excluded.proposals_missed`,
			day, vid, n, missed[addr],
		); err != nil {
			return fmt.Errorf("count proposals %d/%s: %w", r.Height, addr, err)
		}
	}
	return nil
}

// fillProposals counts the proposals of the blocks stored before blocks_days
// counted them, once: round-0 blocks by one aggregate, late blocks one by one
// from their stored rounds.
func (s *Store) fillProposals(ctx context.Context) error {
	filled, err := s.meta(ctx, metaProposalsFilled)
	if err != nil || filled != "" {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO blocks_days(day, validator, proposals_expected)
		 SELECT time_ms / 86400000, proposer, COUNT(*) FROM blocks WHERE round = 0 GROUP BY 1, 2
		 ON CONFLICT(day, validator) DO UPDATE SET proposals_expected = proposals_expected + excluded.proposals_expected`,
	); err != nil {
		return fmt.Errorf("fill round-0 proposals: %w", err)
	}
	late, err := lateBlocks(ctx, tx, 0, math.MaxInt64)
	if err != nil {
		return err
	}
	ids, err := loadValidatorIDs(ctx, tx)
	if err != nil {
		return err
	}
	for _, r := range late {
		if err := countProposals(ctx, tx, ids, r); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO blocks_meta(key, value) VALUES(?, '1')`, metaProposalsFilled,
	); err != nil {
		return fmt.Errorf("record the proposals filled: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit proposals: %w", err)
	}
	return nil
}

// lateBlocks reads, in tx, the stored blocks from height from to height to
// made after round 0, with their rounds' expected proposers ("" when
// unknown), ascending.
func lateBlocks(ctx context.Context, tx *sql.Tx, from, to int64) ([]Record, error) {
	var out []Record
	index := map[int64]int{}
	rows, err := tx.QueryContext(ctx,
		`SELECT b.height, b.time_ms, p.address, b.round FROM blocks b
		 JOIN blocks_validators p ON p.id = b.proposer WHERE b.height BETWEEN ? AND ? AND b.round > 0 ORDER BY b.height`, from, to)
	if err != nil {
		return nil, fmt.Errorf("late blocks: %w", err)
	}
	for rows.Next() {
		var r Record
		var ms int64
		if err := rows.Scan(&r.Height, &ms, &r.Proposer, &r.Round); err != nil {
			rows.Close()
			return nil, fmt.Errorf("late blocks: %w", err)
		}
		r.Time = time.UnixMilli(ms).UTC()
		index[r.Height] = len(out)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("late blocks: %w", err)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("late blocks: %w", err)
	}
	rows, err = tx.QueryContext(ctx,
		`SELECT r.height, v.address FROM blocks_rounds r LEFT JOIN blocks_validators v ON v.id = r.proposer
		 WHERE r.height BETWEEN ? AND ? ORDER BY r.height, r.round`, from, to)
	if err != nil {
		return nil, fmt.Errorf("late block rounds: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h int64
		var p sql.NullString
		if err := rows.Scan(&h, &p); err != nil {
			return nil, fmt.Errorf("late block rounds: %w", err)
		}
		if i, ok := index[h]; ok {
			out[i].Rounds = append(out[i].Rounds, p.String)
		}
	}
	return out, rows.Err()
}

package blocks

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"time"
)

// ScoringWindow is the scoring spec's window as of a day: its days, and the
// stored heights in it.
type ScoringWindow struct {
	FirstDay, LastDay time.Time // the 30 complete UTC days before today, midnight UTC
	SpanStart         time.Time // the first of the 180 complete UTC days before today, which recovery reads
	First, Last       int64     // the window's first and last stored heights, 0 when it holds none
	Complete          bool      // a block timed at or after today 00:00 UTC is stored: Last is the window's last height
}

// ScoringWindow returns the scoring window as of now, read in one
// transaction.
func (s *Store) ScoringWindow(ctx context.Context, now time.Time) (ScoringWindow, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ScoringWindow{}, fmt.Errorf("begin read: %w", err)
	}
	defer tx.Rollback()
	low, high, err := storedRange(ctx, tx)
	if err != nil {
		return ScoringWindow{}, err
	}
	return scoringWindow(ctx, tx, low, high, now)
}

// scoringWindow returns the scoring window as of now over the stored heights
// low to high.
func scoringWindow(ctx context.Context, q queryRower, low, high int64, now time.Time) (ScoringWindow, error) {
	today := now.UTC().Truncate(24 * time.Hour)
	w := ScoringWindow{
		FirstDay:  today.AddDate(0, 0, -scoringDays),
		LastDay:   today.AddDate(0, 0, -1),
		SpanStart: today.AddDate(0, 0, -recoveryDays),
	}
	first, ok, err := firstFrom(ctx, q, w.FirstDay.UnixMilli(), low, high)
	if err != nil {
		return ScoringWindow{}, fmt.Errorf("first block of the scoring window: %w", err)
	}
	if !ok {
		return w, nil
	}
	last := high
	todays, ok, err := firstFrom(ctx, q, today.UnixMilli(), first, high)
	if err != nil {
		return ScoringWindow{}, fmt.Errorf("first block of today: %w", err)
	}
	if ok {
		w.Complete, last = true, todays-1
	}
	if last >= first {
		w.First, w.Last = first, last
	}
	return w, nil
}

// ProposalFlags returns the heights from to to whose proposals cannot be
// checked, ascending: those flagged when stored, and the late blocks whose
// rounds give no k (proposalCounts). The scoring spec disables the proposals
// sub-score over a window holding one.
func (s *Store) ProposalFlags(ctx context.Context, from, to int64) ([]int64, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin read: %w", err)
	}
	defer tx.Rollback()
	out, err := flagsIn(ctx, tx, from, to)
	if err != nil {
		return nil, err
	}
	late, err := lateBlocks(ctx, tx, from, to)
	if err != nil {
		return nil, err
	}
	for _, r := range late {
		if _, _, ok := proposalCounts(r); !ok {
			out = append(out, r.Height)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// flagsIn returns the heights from to to flagged when stored, ascending.
func flagsIn(ctx context.Context, q querier, from, to int64) ([]int64, error) {
	rows, err := q.QueryContext(ctx, `SELECT height FROM blocks_flags WHERE height BETWEEN ? AND ? ORDER BY height`, from, to)
	if err != nil {
		return nil, fmt.Errorf("flags %d-%d: %w", from, to, err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var h int64
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("flags %d-%d: %w", from, to, err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

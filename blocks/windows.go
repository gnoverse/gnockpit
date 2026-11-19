package blocks

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// windowMissesQuery counts each validator's absent and other-block precommits
// in a span of heights, searching the absences by height.
const windowMissesQuery = `SELECT blocks_validators.address, COUNT(*) FROM blocks_absences
	JOIN blocks_validators ON blocks_validators.id = blocks_absences.validator
	WHERE blocks_absences.height BETWEEN ? AND ? AND blocks_absences.kind IN (?, ?)
	GROUP BY blocks_absences.validator`

// dayMissesQuery sums each validator's misses from the per-day counters, from
// a UTC day on.
const dayMissesQuery = `SELECT blocks_validators.address, SUM(blocks_missed_days.missed) FROM blocks_missed_days
	JOIN blocks_validators ON blocks_validators.id = blocks_missed_days.validator
	WHERE blocks_missed_days.day >= ? GROUP BY blocks_missed_days.validator`

// WindowReport holds missed-block counts across windows: Blocks[i] and
// Missed[addr][i] correspond to Windows[i].
type WindowReport struct {
	Windows []time.Duration
	Blocks  []int
	Missed  map[string][]int
}

// MissedByWindows returns, for each window up to now, the stored blocks and
// each validator's missed ones: absent, or precommitted for another block. A
// window of zero or less means all the history.
//
// The stored heights run unbroken (the tracker fills outward from one range)
// and block times grow with height, so a window's first block is found by
// bisecting the heights, and its blocks are counted from it to the highest
// without reading them. Its misses are read from the absences of its first,
// partial UTC day, and from the per-day counters after it; a window from the
// lowest stored block reads only the counters. The report is read in one
// transaction, so concurrent writes do not skew it.
func (s *Store) MissedByWindows(ctx context.Context, windows []time.Duration, now time.Time) (WindowReport, error) {
	rep := WindowReport{Windows: windows, Blocks: make([]int, len(windows)), Missed: map[string][]int{}}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return rep, fmt.Errorf("begin read: %w", err)
	}
	defer tx.Rollback()
	low, high, err := storedRange(ctx, tx)
	if err != nil {
		return rep, err
	}
	add := func(i int, query string, args ...any) error {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var addr string
			var n int
			if err := rows.Scan(&addr, &n); err != nil {
				return err
			}
			if rep.Missed[addr] == nil {
				rep.Missed[addr] = make([]int, len(windows))
			}
			rep.Missed[addr][i] += n
		}
		return rows.Err()
	}
	for i, w := range windows {
		var cutoff int64
		if w > 0 {
			cutoff = now.Add(-w).UnixMilli()
		}
		from, ok, err := firstFrom(ctx, tx, cutoff, low, high)
		if err != nil {
			return rep, fmt.Errorf("first block of the %s window: %w", w, err)
		}
		if !ok {
			continue
		}
		rep.Blocks[i] = int(high - from + 1)
		fromMs, err := timeOf(ctx, tx, from)
		if err != nil {
			return rep, err
		}
		firstDay := dayOf(time.UnixMilli(fromMs))
		countedFrom := firstDay
		if from > low {
			// The first day is partial: its misses come from its absences.
			last := high
			next, ok, err := firstFrom(ctx, tx, (firstDay+1)*secondsPerDay*1000, from, high)
			if err != nil {
				return rep, fmt.Errorf("first block after the %s window's first day: %w", w, err)
			}
			if ok {
				last = next - 1
			}
			if err := add(i, windowMissesQuery, from, last, Absent, OtherBlock); err != nil {
				return rep, fmt.Errorf("misses of the %s window's first day: %w", w, err)
			}
			countedFrom = firstDay + 1
		}
		if err := add(i, dayMissesQuery, countedFrom); err != nil {
			return rep, fmt.Errorf("misses of the %s window: %w", w, err)
		}
	}
	return rep, nil
}

// queryRower runs a query for one row: a *sql.DB or a *sql.Tx.
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// timeOf returns the block time of the stored height, in Unix ms.
func timeOf(ctx context.Context, q queryRower, h int64) (ms int64, err error) {
	if err := q.QueryRowContext(ctx, `SELECT time_ms FROM blocks WHERE height = ?`, h).Scan(&ms); err != nil {
		return 0, fmt.Errorf("time of %d: %w", h, err)
	}
	return ms, nil
}

// firstFrom returns the lowest of the stored heights low to high whose block
// time is cutoffMs or later, bisecting them: they run unbroken and block
// times grow with height. ok is false when no stored block is that late.
func firstFrom(ctx context.Context, q queryRower, cutoffMs, low, high int64) (from int64, ok bool, err error) {
	if high == 0 {
		return 0, false, nil
	}
	last, err := timeOf(ctx, q, high)
	if err != nil || last < cutoffMs {
		return 0, false, err
	}
	for low < high {
		mid := low + (high-low)/2
		ms, err := timeOf(ctx, q, mid)
		if err != nil {
			return 0, false, err
		}
		if ms >= cutoffMs {
			high = mid
		} else {
			low = mid + 1
		}
	}
	return low, true, nil
}

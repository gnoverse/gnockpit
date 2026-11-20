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

// dayMissesQuery sums each validator's misses (absent or other block) from the
// per-day counters, from a UTC day on; a validator with nil votes alone is no
// miss.
const dayMissesQuery = `SELECT blocks_validators.address, SUM(blocks_days.absent + blocks_days.other_block) FROM blocks_days
	JOIN blocks_validators ON blocks_validators.id = blocks_days.validator
	WHERE blocks_days.day >= ? GROUP BY blocks_days.validator
	HAVING SUM(blocks_days.absent + blocks_days.other_block) > 0`

// WindowReport holds missed-block counts across windows: Blocks[i] and
// Missed[addr][i] correspond to Windows[i].
type WindowReport struct {
	Windows []time.Duration
	Blocks  []int
	Missed  map[string][]int
	// ProposalsExpected and ProposalsMissed are each validator's proposals
	// in the window: the rounds it was the expected proposer of until the
	// block was made, and those it missed (proposalCounts).
	ProposalsExpected, ProposalsMissed map[string][]int
}

// windowProposalsQuery counts each validator's round-0 blocks in a span of
// heights, searching the blocks by height; the span's late blocks are counted
// from their rounds.
const windowProposalsQuery = `SELECT p.address, COUNT(*), 0 FROM blocks b
	JOIN blocks_validators p ON p.id = b.proposer
	WHERE b.height BETWEEN ? AND ? AND b.round = 0 GROUP BY b.proposer`

// dayProposalsQuery sums each validator's proposals from the per-day
// counters, from a UTC day on.
const dayProposalsQuery = `SELECT blocks_validators.address, SUM(blocks_days.proposals_expected), SUM(blocks_days.proposals_missed)
	FROM blocks_days JOIN blocks_validators ON blocks_validators.id = blocks_days.validator
	WHERE blocks_days.day >= ? GROUP BY blocks_days.validator
	HAVING SUM(blocks_days.proposals_expected) > 0 OR SUM(blocks_days.proposals_missed) > 0`

// MissedByWindows returns, for each window up to now, the stored blocks, each
// validator's missed ones (absent, or precommitted for another block), and
// its proposals expected and missed. A window of zero or less means all the
// history.
//
// The stored heights run unbroken (the tracker fills outward from one range)
// and block times grow with height, so a window's first block is found by
// bisecting the heights, and its blocks are counted from it to the highest
// without reading them. Its misses and proposals are read from the absences
// and blocks of its first, partial UTC day, and from the per-day counters
// after it; a window from the lowest stored block reads only the counters. The report is read in one
// transaction, so concurrent writes do not skew it.
func (s *Store) MissedByWindows(ctx context.Context, windows []time.Duration, now time.Time) (WindowReport, error) {
	rep := WindowReport{
		Windows: windows, Blocks: make([]int, len(windows)), Missed: map[string][]int{},
		ProposalsExpected: map[string][]int{}, ProposalsMissed: map[string][]int{},
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return rep, fmt.Errorf("begin read: %w", err)
	}
	defer tx.Rollback()
	low, high, err := storedRange(ctx, tx)
	if err != nil {
		return rep, err
	}
	count := func(m map[string][]int, addr string, i, n int) {
		if m[addr] == nil {
			m[addr] = make([]int, len(windows))
		}
		m[addr][i] += n
	}
	addProposals := func(i int, query string, args ...any) error {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var addr string
			var expected, missed int
			if err := rows.Scan(&addr, &expected, &missed); err != nil {
				return err
			}
			count(rep.ProposalsExpected, addr, i, expected)
			count(rep.ProposalsMissed, addr, i, missed)
		}
		return rows.Err()
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
			count(rep.Missed, addr, i, n)
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
			if err := addProposals(i, windowProposalsQuery, from, last); err != nil {
				return rep, fmt.Errorf("proposals of the %s window's first day: %w", w, err)
			}
			late, err := lateBlocks(ctx, tx, from, last)
			if err != nil {
				return rep, err
			}
			for _, r := range late {
				expected, missed, ok := proposalCounts(r)
				if !ok {
					continue
				}
				for addr, n := range expected {
					count(rep.ProposalsExpected, addr, i, int(n))
					count(rep.ProposalsMissed, addr, i, int(missed[addr]))
				}
			}
			countedFrom = firstDay + 1
		}
		if err := add(i, dayMissesQuery, countedFrom); err != nil {
			return rep, fmt.Errorf("misses of the %s window: %w", w, err)
		}
		if err := addProposals(i, dayProposalsQuery, countedFrom); err != nil {
			return rep, fmt.Errorf("proposals of the %s window: %w", w, err)
		}
	}
	return rep, nil
}

// queryRower runs a query for one row: a *sql.DB or a *sql.Tx.
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// querier runs a query for rows: a *sql.DB or a *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
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

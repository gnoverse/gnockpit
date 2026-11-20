package blocks

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

func TestProposalCounts(t *testing.T) {
	for _, tc := range []struct {
		name             string
		rounds           []string
		round            int
		expected, missed map[string]int64
		ok               bool
	}{
		{"round 0", nil, 0, map[string]int64{"P": 1}, map[string]int64{}, true},
		{"two rounds missed", []string{"A", "B", "P"}, 2, map[string]int64{"A": 1, "B": 1, "P": 1}, map[string]int64{"A": 1, "B": 1}, true},
		{"the earliest match", []string{"P", "A", "P"}, 2, map[string]int64{"P": 1}, map[string]int64{}, true},
		{"one proposer twice", []string{"A", "A", "P"}, 2, map[string]int64{"A": 2, "P": 1}, map[string]int64{"A": 2}, true},
		{"rounds above k unknown", []string{"P", ""}, 1, map[string]int64{"P": 1}, map[string]int64{}, true},
		{"a round below k unknown", []string{"", "B", "P"}, 2, nil, nil, false},
		{"no round matches", []string{"A", "B", "C"}, 2, nil, nil, false},
		{"rounds not computed", nil, 2, nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Record{Proposer: "P", Round: tc.round, Rounds: tc.rounds}
			expected, missed, ok := proposalCounts(r)
			if ok != tc.ok {
				t.Fatalf("proposalCounts(%v, round %d) ok = %v, want %v", tc.rounds, tc.round, ok, tc.ok)
			}
			if !ok {
				return
			}
			if !reflect.DeepEqual(expected, tc.expected) || !reflect.DeepEqual(missed, tc.missed) {
				t.Errorf("proposalCounts(%v, round %d) = %v, %v; want %v, %v", tc.rounds, tc.round, expected, missed, tc.expected, tc.missed)
			}
		})
	}
}

// propRow is a blocks_days row's proposal counts, by address.
type propRow struct {
	Day              int64
	Address          string
	Expected, Missed int64
}

func propRows(t *testing.T, s *Store) []propRow {
	t.Helper()
	var out []propRow
	err := s.each(context.Background(), func(rows *sql.Rows) error {
		var r propRow
		if err := rows.Scan(&r.Day, &r.Address, &r.Expected, &r.Missed); err != nil {
			return err
		}
		out = append(out, r)
		return nil
	}, `SELECT d.day, v.address, d.proposals_expected, d.proposals_missed FROM blocks_days d
	    JOIN blocks_validators v ON v.id = d.validator
	    WHERE d.proposals_expected > 0 OR d.proposals_missed > 0 ORDER BY d.day, v.address`)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// writeProposals stores a round-0 block, a late block whose round 0 was
// val15T's, and a flagged late block whose round 0's proposer is unknown, all
// proposed by valG0Y.
func writeProposals(t *testing.T, s *Store) {
	t.Helper()
	putTestSet(t, s)
	late := rec(2)
	late.Round, late.Rounds = 1, []string{val15T, valG0Y}
	flagged := rec(3)
	flagged.Round, flagged.Rounds, flagged.Flag = 1, []string{"", valG0Y}, "round 0's proposer unknown"
	if err := s.Write(context.Background(), []Record{rec(1), late, flagged}); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCountsProposalsByDay(t *testing.T) {
	s := newTestStore(t)
	writeProposals(t, s)
	want := []propRow{{dayOf(t0), val15T, 1, 1}, {dayOf(t0), valG0Y, 2, 0}}
	if got := propRows(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("proposal rows = %+v, want %+v", got, want)
	}
}

func TestStoreFillsProposalsOnce(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	writeProposals(t, s)
	want := propRows(t, s)
	clear := func() {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, `UPDATE blocks_days SET proposals_expected = 0, proposals_missed = 0`); err != nil {
			t.Fatal(err)
		}
	}
	clear()
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blocks_meta WHERE key = ?`, metaProposalsFilled); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	if got := propRows(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("filled proposal rows = %+v, want the rows written live %+v", got, want)
	}
	clear()
	if _, err := NewStore(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	if got := propRows(t, s); len(got) != 0 {
		t.Errorf("proposal rows after a second NewStore = %+v, want none: the fill runs once", got)
	}
}

func TestStoreForgetsTheProposalFillOnReset(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.Reset(ctx, "test-chain"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.meta(ctx, metaProposalsFilled); err != nil || v == "" {
		t.Errorf("meta %s after Reset = %q, %v; want it set", metaProposalsFilled, v, err)
	}
}

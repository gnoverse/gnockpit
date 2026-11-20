package blocks

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"
)

// dayRow is a blocks_days row's absence counts, by address.
type dayRow struct {
	Day                     int64
	Address                 string
	Absent, Nil, OtherBlock int64
}

func dayRows(t *testing.T, s *Store) []dayRow {
	t.Helper()
	var out []dayRow
	err := s.each(context.Background(), func(rows *sql.Rows) error {
		var r dayRow
		if err := rows.Scan(&r.Day, &r.Address, &r.Absent, &r.Nil, &r.OtherBlock); err != nil {
			return err
		}
		out = append(out, r)
		return nil
	}, `SELECT d.day, v.address, d.absent, d.nil_votes, d.other_block FROM blocks_days d
	    JOIN blocks_validators v ON v.id = d.validator ORDER BY d.day, v.address`)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func writeKinds(t *testing.T, s *Store) {
	t.Helper()
	putTestSet(t, s)
	if err := s.Write(context.Background(), []Record{
		rec(1, Absence{valG0Y, Absent}),
		rec(2, Absence{valG0Y, Nil}),
		rec(3, Absence{valG0Y, OtherBlock}),
		rec(4, Absence{val15T, Absent}),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestStoreCountsEachAbsenceKindByDay(t *testing.T) {
	s := newTestStore(t)
	writeKinds(t, s)
	want := []dayRow{{dayOf(t0), val15T, 1, 0, 0}, {dayOf(t0), valG0Y, 1, 1, 1}}
	if got := dayRows(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("day rows = %+v, want %+v", got, want)
	}
}

func TestStoreFillsTheDayCountersOfAnOlderDatabaseOnce(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	writeKinds(t, s)
	want := dayRows(t, s)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blocks_days`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blocks_meta WHERE key = ?`, metaDaysFilled); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	if got := dayRows(t, s); !reflect.DeepEqual(got, want) {
		t.Errorf("filled day rows = %+v, want the rows written live %+v", got, want)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blocks_days WHERE day = ?`, dayOf(t0)); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	if got := dayRows(t, s); len(got) != 0 {
		t.Errorf("day rows after a second NewStore = %+v, want none: the fill runs once", got)
	}
}

func TestStoreDropsTheMissedDaysTable(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if _, err := s.db.ExecContext(ctx,
		`CREATE TABLE blocks_missed_days (day INTEGER, validator INTEGER, missed INTEGER, PRIMARY KEY (day, validator)) WITHOUT ROWID`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name = 'blocks_missed_days'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("blocks_missed_days is still there")
	}
}

func TestStoreForgetsTheDayCountersOnReset(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	writeKinds(t, s)
	if err := s.Reset(ctx, "test-chain"); err != nil {
		t.Fatal(err)
	}
	if got := dayRows(t, s); len(got) != 0 {
		t.Errorf("day rows after Reset = %+v, want none", got)
	}
	if v, err := s.meta(ctx, metaDaysFilled); err != nil || v == "" {
		t.Errorf("meta %s after Reset = %q, %v; want it set, so NewStore does not scan", metaDaysFilled, v, err)
	}
}

func TestStoreMissedTotalsLeaveOutNilVotesAlone(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	if err := s.Write(ctx, []Record{rec(1, Absence{valG0Y, Nil}), rec(2, Absence{val15T, Absent})}); err != nil {
		t.Fatal(err)
	}
	got, err := s.MissedByWindows(ctx, []time.Duration{0}, t0.Add(10*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string][]int{val15T: {1}}; !reflect.DeepEqual(got.Missed, want) {
		t.Errorf("missed = %v, want %v: a nil vote signs", got.Missed, want)
	}
}

package blocks

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func validatorID(t *testing.T, s *Store, addr string) int64 {
	t.Helper()
	var id int64
	if err := s.db.QueryRowContext(context.Background(), `SELECT id FROM blocks_validators WHERE address = ?`, addr).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func runs(t *testing.T, s *Store, addr string) []Run {
	t.Helper()
	got, err := runsOf(context.Background(), s.db, validatorID(t, s, addr), 1)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// absences is one Write: each height's kind for valG0Y, signed when absent
// from the map.
type absences map[int64]Kind

func writeAbsences(t *testing.T, s *Store, from, to int64, kinds absences) {
	t.Helper()
	var recs []Record
	for h := from; h <= to; h++ {
		r := rec(h)
		if k, ok := kinds[h]; ok {
			r.Absences = []Absence{{valG0Y, k}}
		}
		recs = append(recs, r)
	}
	if err := s.Write(context.Background(), recs); err != nil {
		t.Fatal(err)
	}
}

func TestStoreKeepsAbsenceRuns(t *testing.T) {
	type write struct {
		from, to int64
		kinds    absences
	}
	every := func(from, to int64) absences {
		a := absences{}
		for h := from; h <= to; h++ {
			a[h] = Absent
		}
		return a
	}
	for _, tc := range []struct {
		name   string
		writes []write
		want   []Run
	}{
		{"one write", []write{{1, 5, absences{2: Absent, 3: Absent}}}, []Run{{2, 3}}},
		{"batch edge going down", []write{{10, 12, every(10, 12)}, {7, 9, absences{8: Absent, 9: Absent}}}, []Run{{8, 12}}},
		{"a nil vote signs", []write{{1, 7, absences{4: Absent, 5: Nil, 6: Absent}}}, []Run{{4, 4}, {6, 6}}},
		{"another block misses", []write{{1, 5, absences{3: Absent, 4: OtherBlock}}}, []Run{{3, 4}}},
		{"merges both sides", []write{{1, 2, every(1, 2)}, {4, 5, every(4, 5)}, {3, 3, every(3, 3)}}, []Run{{1, 5}}},
		{"absent in every block", []write{{1, 100, every(1, 100)}, {201, 300, every(201, 300)}, {101, 200, every(101, 200)}}, []Run{{1, 300}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			putTestSet(t, s)
			for _, w := range tc.writes {
				writeAbsences(t, s, w.from, w.to, w.kinds)
			}
			if got := runs(t, s, valG0Y); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("runs = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestStoreFillsTheAbsenceRunsOnce(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	pattern := func(from, to int64) []Record {
		var recs []Record
		for h := from; h <= to; h++ {
			r := rec(h)
			if h%7 < 3 {
				r.Absences = append(r.Absences, Absence{valG0Y, Absent})
			}
			if h%5 == 0 || h%5 == 1 {
				r.Absences = append(r.Absences, Absence{val15T, OtherBlock})
			}
			recs = append(recs, r)
		}
		return recs
	}
	for _, span := range [][2]int64{{40, 60}, {1, 39}, {61, 90}} {
		if err := s.Write(ctx, pattern(span[0], span[1])); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string][]Run{valG0Y: runs(t, s, valG0Y), val15T: runs(t, s, val15T)}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blocks_absence_runs`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blocks_meta WHERE key = ?`, metaRunsFilled); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	for addr, w := range want {
		if got := runs(t, s, addr); !reflect.DeepEqual(got, w) {
			t.Errorf("%s: filled runs = %v, want the runs kept live %v", addr, got, w)
		}
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blocks_absence_runs`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	if got := runs(t, s, valG0Y); len(got) != 0 {
		t.Errorf("runs after a second NewStore = %v, want none: the fill runs once", got)
	}
}

func TestStoreForgetsTheAbsenceRunsOnReset(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	writeAbsences(t, s, 1, 3, absences{2: Absent})
	if err := s.Reset(ctx, "test-chain"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocks_absence_runs`).Scan(&n); err != nil || n != 0 {
		t.Errorf("runs after Reset = %d, %v; want none", n, err)
	}
	if v, err := s.meta(ctx, metaRunsFilled); err != nil || v == "" {
		t.Errorf("meta %s after Reset = %q, %v; want it set", metaRunsFilled, v, err)
	}
}

func TestStoreReadsRunsByValidator(t *testing.T) {
	plan := queryPlan(t, newTestStore(t), runsOfQuery, 1, 10)
	if !slices.ContainsFunc(plan, func(d string) bool {
		return strings.HasPrefix(d, "SEARCH blocks_absence_runs USING PRIMARY KEY (validator=?)")
	}) {
		t.Errorf("plan = %q, want a search of blocks_absence_runs by validator", plan)
	}
}

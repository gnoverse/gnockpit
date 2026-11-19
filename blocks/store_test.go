package blocks

import (
	"context"
	"database/sql"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// A single connection keeps the in-memory database alive across queries.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	s, err := NewStore(context.Background(), db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

var t0 = time.Date(2026, 11, 16, 10, 51, 11, 208_000_000, time.UTC)

var testSet = []Member{{Address: valG0Y, PubKey: "KA", Power: 10}, {Address: val15T, PubKey: "KB", Power: 20}}

// rec is the record of height, at t0 + height seconds, on testSet.
func rec(height int64, absences ...Absence) Record {
	return Record{Height: height, Time: t0.Add(time.Duration(height) * time.Second), Proposer: valG0Y, Txs: 1, ValsetHash: "SET", Absences: absences}
}

func putTestSet(t *testing.T, s *Store) {
	t.Helper()
	if err := s.PutValset(context.Background(), "SET", testSet); err != nil {
		t.Fatal(err)
	}
}

func TestStoreWriteAndRead(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	if err := s.Write(ctx, []Record{rec(11, Absence{val15T, Nil}), rec(10), rec(12, Absence{val15T, Absent})}); err != nil {
		t.Fatal(err)
	}
	if low, high, err := s.Range(ctx); err != nil || low != 10 || high != 12 {
		t.Errorf("Range() = %d, %d, %v; want 10, 12, nil", low, high, err)
	}
	got, sets, err := s.Blocks(ctx, 9, 11)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Record{rec(10), rec(11, Absence{val15T, Nil})}; !reflect.DeepEqual(got, want) {
		t.Errorf("Blocks(9, 11) =\n%+v\nwant\n%+v", got, want)
	}
	if want := map[string][]Member{"SET": testSet}; !reflect.DeepEqual(sets, want) {
		t.Errorf("Blocks(9, 11) sets = %+v, want %+v", sets, want)
	}
	if hash, ok, err := s.ValsetHashAt(ctx, 12); err != nil || !ok || hash != "SET" {
		t.Errorf("ValsetHashAt(12) = %q, %v, %v; want SET", hash, ok, err)
	}
	if _, ok, err := s.ValsetHashAt(ctx, 13); err != nil || ok {
		t.Errorf("ValsetHashAt(13) found = %v, %v; want not found", ok, err)
	}
	if at, ok, err := s.TimeAt(ctx, 10); err != nil || !ok || !at.Equal(t0.Add(10*time.Second)) {
		t.Errorf("TimeAt(10) = %v, %v, %v", at, ok, err)
	}
	if members, ok, err := s.Valset(ctx, "SET"); err != nil || !ok || !reflect.DeepEqual(members, testSet) {
		t.Errorf("Valset(SET) = %+v, %v, %v", members, ok, err)
	}
}

func TestStoreWriteNeedsTheSet(t *testing.T) {
	s := newTestStore(t)
	if err := s.Write(context.Background(), []Record{rec(10)}); err == nil {
		t.Error("Write() of a record on an unknown set succeeded")
	}
}

func TestStoreWriteCountsAHeightOnce(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	r := rec(10)
	r.Delays = []Delay{{valG0Y, 300}, {val15T, -1200}}
	for range 2 {
		if err := s.Write(ctx, []Record{r}); err != nil {
			t.Fatal(err)
		}
	}
	var votes, sum, b0, b3 int64
	day := t0.Add(10*time.Second).Unix() / 86400
	if err := s.db.QueryRowContext(ctx,
		`SELECT SUM(votes), SUM(sum_ms), SUM(b0), SUM(b3) FROM blocks_vote_delays WHERE day = ?`, day,
	).Scan(&votes, &sum, &b0, &b3); err != nil {
		t.Fatal(err)
	}
	// 300 ms falls in [250, 1000), -1200 ms in ≤ -1000.
	if votes != 2 || sum != -900 || b0 != 1 || b3 != 1 {
		t.Errorf("delays: votes %d, sum %d, b0 %d, b3 %d; want 2, -900, 1, 1", votes, sum, b0, b3)
	}
}

func TestStoreRoundsAndFlags(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	r := rec(10)
	r.Round, r.Rounds, r.Flag = 1, []string{val15T, valG0Y}, "test flag"
	if err := s.Write(ctx, []Record{r}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT round, proposer FROM blocks_rounds WHERE height = 10 ORDER BY round`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var round int
		var p string
		if err := rows.Scan(&round, &p); err != nil {
			t.Fatal(err)
		}
		got = append(got, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{val15T, valG0Y}) {
		t.Errorf("rounds = %v, want [%s %s]", got, val15T, valG0Y)
	}
	var reason string
	if err := s.db.QueryRowContext(ctx, `SELECT reason FROM blocks_flags WHERE height = 10`).Scan(&reason); err != nil || reason != "test flag" {
		t.Errorf("flag = %q, %v; want test flag", reason, err)
	}
}

func TestStoreMissedByWindows(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	now := t0.Add(10 * time.Hour)
	r1, r2, r3 := rec(1, Absence{val15T, Absent}), rec(2, Absence{val15T, OtherBlock}, Absence{valG0Y, Nil}), rec(3)
	r1.Time, r2.Time, r3.Time = now.Add(-2*time.Hour), now.Add(-30*time.Minute), now.Add(-10*time.Minute)
	if err := s.Write(ctx, []Record{r1, r2, r3}); err != nil {
		t.Fatal(err)
	}
	got, err := s.MissedByWindows(ctx, []time.Duration{time.Hour, 0}, now)
	if err != nil {
		t.Fatal(err)
	}
	// A nil vote counts as signed.
	want := WindowReport{Windows: []time.Duration{time.Hour, 0}, Blocks: []int{2, 3}, Missed: map[string][]int{val15T: {1, 2}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MissedByWindows() = %+v, want %+v", got, want)
	}
}

func TestStoreMissedByWindowsSearchesTheWindowOnly(t *testing.T) {
	s := newTestStore(t)
	rows, err := s.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+windowMissesQuery, 1, Absent, OtherBlock)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	// The 24h window is read on every publish: it must cost its own blocks'
	// absences, not a pass over the whole history's.
	if !slices.ContainsFunc(plan, func(d string) bool {
		return strings.HasPrefix(d, "SEARCH blocks_absences") && strings.Contains(d, "height>?")
	}) {
		t.Errorf("plan = %q, want a search of blocks_absences by height", plan)
	}
}

func TestStoreEarliest(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if at, err := s.Earliest(ctx); err != nil || !at.IsZero() {
		t.Errorf("Earliest() on an empty store = %v, %v; want zero", at, err)
	}
	putTestSet(t, s)
	if err := s.Write(ctx, []Record{rec(12), rec(11)}); err != nil {
		t.Fatal(err)
	}
	if at, err := s.Earliest(ctx); err != nil || !at.Equal(t0.Add(11*time.Second)) {
		t.Errorf("Earliest() = %v, %v; want %v", at, err, t0.Add(11*time.Second))
	}
}

func TestStorePutEvents(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	evs := []Event{{Type: "ValidatorAdded", Operator: "g1op", Address: valH97, Power: 10, Applied: true}}
	for range 2 {
		if err := s.PutEvents(ctx, 200, 198, evs); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	var source int64
	var typ, addr string
	var applied bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), MAX(source_height), MAX(type), MAX(address), MAX(applied) FROM blocks_events WHERE height = 200`,
	).Scan(&n, &source, &typ, &addr, &applied); err != nil {
		t.Fatal(err)
	}
	if n != 1 || source != 198 || typ != "ValidatorAdded" || addr != valH97 || !applied {
		t.Errorf("events = %d rows, source %d, %s %s applied %v; want 1 row from 198, ValidatorAdded %s applied", n, source, typ, addr, applied, valH97)
	}
}

func TestStoreMetaAndReset(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if id, err := s.ChainID(ctx); err != nil || id != "" {
		t.Fatalf("ChainID() on a new store = %q, %v", id, err)
	}
	if err := s.Reset(ctx, "gnoland-1"); err != nil {
		t.Fatal(err)
	}
	putTestSet(t, s)
	if err := s.Write(ctx, []Record{rec(10)}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFloor(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.PutEvents(ctx, 10, 8, []Event{{Type: "ValidatorAdded"}}); err != nil {
		t.Fatal(err)
	}
	if f, err := s.Floor(ctx); err != nil || f != 5 {
		t.Errorf("Floor() = %d, %v; want 5", f, err)
	}

	if err := s.Reset(ctx, "test-13"); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.ChainID(ctx); id != "test-13" {
		t.Errorf("ChainID() after Reset = %q, want test-13", id)
	}
	if low, high, _ := s.Range(ctx); low != 0 || high != 0 {
		t.Errorf("Range() after Reset = %d, %d; want empty", low, high)
	}
	if f, _ := s.Floor(ctx); f != 0 {
		t.Errorf("Floor() after Reset = %d, want 0", f)
	}
	if _, ok, _ := s.Valset(ctx, "SET"); ok {
		t.Error("Valset(SET) found after Reset")
	}
	var events int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocks_events`).Scan(&events); err != nil || events != 0 {
		t.Errorf("events after Reset = %d, %v; want 0", events, err)
	}
}

func TestNewStoreDropsTheSigningHistoryTables(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.ExecContext(ctx, `CREATE TABLE signing_blocks (height INTEGER PRIMARY KEY); CREATE TABLE signing_misses (height INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(ctx, db); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('signing_blocks', 'signing_misses')`,
	).Scan(&n); err != nil || n != 0 {
		t.Errorf("signing history tables left: %d, %v", n, err)
	}
}

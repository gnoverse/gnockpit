package blocks

import (
	"context"
	"database/sql"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// otherSet is testSet with one more validator and valG0Y's power raised.
var otherSet = []Member{{Address: valG0Y, PubKey: "KA", Power: 15}, {Address: val15T, PubKey: "KB", Power: 20}, {Address: valHQH, PubKey: "KC", Power: 5}}

// putChangeSets stores testSet as "A" and otherSet as "B".
func putChangeSets(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	if err := s.PutValset(ctx, "A", testSet); err != nil {
		t.Fatal(err)
	}
	if err := s.PutValset(ctx, "B", otherSet); err != nil {
		t.Fatal(err)
	}
}

// onSets returns the records of heights from to to, each on the set hash
// sets gives it.
func onSets(from, to int64, sets func(h int64) string) []Record {
	var out []Record
	for h := from; h <= to; h++ {
		r := rec(h)
		r.ValsetHash = sets(h)
		out = append(out, r)
	}
	return out
}

func on(hash string) func(int64) string { return func(int64) string { return hash } }

func writeRecs(t *testing.T, s *Store, recs []Record) {
	t.Helper()
	if err := s.Write(context.Background(), recs); err != nil {
		t.Fatal(err)
	}
}

func changeHeights(t *testing.T, s *Store) []int64 {
	t.Helper()
	out := []int64{}
	err := s.each(context.Background(), func(rows *sql.Rows) error {
		var h int64
		if err := rows.Scan(&h); err != nil {
			return err
		}
		out = append(out, h)
		return nil
	}, `SELECT height FROM blocks_valset_changes ORDER BY height`)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func wantChanges(t *testing.T, s *Store, want ...int64) {
	t.Helper()
	if want == nil {
		want = []int64{}
	}
	if got := changeHeights(t, s); !slices.Equal(got, want) {
		t.Errorf("change heights = %v, want %v", got, want)
	}
}

func TestStoreRecordsAValsetChangeGoingUp(t *testing.T) {
	s := newTestStore(t)
	putChangeSets(t, s)
	writeRecs(t, s, onSets(1, 3, on("A")))
	writeRecs(t, s, onSets(4, 5, on("B")))
	wantChanges(t, s, 4)
}

func TestStoreRecordsAValsetChangeAtABatchEdgeGoingDown(t *testing.T) {
	s := newTestStore(t)
	putChangeSets(t, s)
	writeRecs(t, s, onSets(5, 10, on("B")))
	writeRecs(t, s, onSets(1, 4, on("A")))
	wantChanges(t, s, 5)
}

func TestStoreRecordsAdjacentValsetChanges(t *testing.T) {
	s := newTestStore(t)
	putChangeSets(t, s)
	writeRecs(t, s, onSets(1, 3, func(h int64) string {
		if h == 2 {
			return "B"
		}
		return "A"
	}))
	wantChanges(t, s, 2, 3)
}

func TestStoreFillsTheValsetChangesOfAnOlderDatabaseOnce(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putChangeSets(t, s)
	writeRecs(t, s, onSets(1, 3, on("A")))
	writeRecs(t, s, onSets(4, 6, on("B")))
	// A database from before the table: no change rows, no fill recorded.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blocks_valset_changes; DELETE FROM blocks_meta WHERE key = ?`, metaChangesFilled); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(ctx, s.db)
	if err != nil {
		t.Fatal(err)
	}
	wantChanges(t, reopened, 4)
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blocks_valset_changes`); err != nil {
		t.Fatal(err)
	}
	again, err := NewStore(ctx, s.db)
	if err != nil {
		t.Fatal(err)
	}
	wantChanges(t, again) // filled once: the second open does not scan
}

func TestStoreForgetsTheValsetChangesOnReset(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putChangeSets(t, s)
	writeRecs(t, s, onSets(1, 3, on("A")))
	writeRecs(t, s, onSets(4, 5, on("B")))
	if err := s.Reset(ctx, "test-13"); err != nil {
		t.Fatal(err)
	}
	wantChanges(t, s)
	if v, err := s.meta(ctx, metaChangesFilled); err != nil || v == "" {
		t.Errorf("meta %s = %q, %v; want it set so the next open does not scan", metaChangesFilled, v, err)
	}
}

// putSets stores each set under its hash.
func putSets(t *testing.T, s *Store, sets map[string][]Member) {
	t.Helper()
	for hash, members := range sets {
		if err := s.PutValset(context.Background(), hash, members); err != nil {
			t.Fatal(err)
		}
	}
}

// oneChange stores 1..3 on set A and 4..5 on set B, and returns the change
// read at 4.
func oneChange(t *testing.T, s *Store, a, b []Member) ValsetChange {
	t.Helper()
	putSets(t, s, map[string][]Member{"A": a, "B": b})
	writeRecs(t, s, onSets(1, 3, on("A")))
	writeRecs(t, s, onSets(4, 5, on("B")))
	return changeAt(t, s, 4)
}

func changeAt(t *testing.T, s *Store, h int64) ValsetChange {
	t.Helper()
	got, err := s.ValsetChangesIn(context.Background(), h, h)
	if err != nil || len(got) != 1 {
		t.Fatalf("ValsetChangesIn(%d, %d) = %v, %v; want one change", h, h, got, err)
	}
	return got[0]
}

func heightsOf(cs []ValsetChange) []int64 {
	out := []int64{}
	for _, c := range cs {
		out = append(out, c.Height)
	}
	return out
}

func m1(addr string, power int64) Member {
	return Member{Address: addr, PubKey: "K" + addr[:4], Power: power}
}

func TestValsetChangesGiveTheDiff(t *testing.T) {
	s := newTestStore(t)
	c := oneChange(t, s,
		[]Member{m1(valG0Y, 1), m1(val15T, 1), m1(valHQH, 1)},
		[]Member{m1(valG0Y, 1), m1(val15T, 2), m1(valH97, 1)})
	if c.Height != 4 || !c.Time.Equal(t0.Add(4*time.Second)) {
		t.Errorf("height, time = %d, %v; want 4, %v", c.Height, c.Time, t0.Add(4*time.Second))
	}
	if !reflect.DeepEqual(c.Added, []Member{m1(valH97, 1)}) || !reflect.DeepEqual(c.Removed, []Member{m1(valHQH, 1)}) {
		t.Errorf("added %v, removed %v; want [%s], [%s]", c.Added, c.Removed, valH97, valHQH)
	}
	if !reflect.DeepEqual(c.Repowered, []PowerChange{{Address: val15T, Before: 1, After: 2}}) {
		t.Errorf("repowered = %v, want %s 1 → 2", c.Repowered, val15T)
	}
	if c.SizeBefore != 3 || c.SizeAfter != 3 || c.PowerBefore != 3 || c.PowerAfter != 4 {
		t.Errorf("size %d → %d, power %d → %d; want 3 → 3, 3 → 4", c.SizeBefore, c.SizeAfter, c.PowerBefore, c.PowerAfter)
	}
	if c.Rotated != nil || c.Events != nil || c.EventsPending {
		t.Errorf("rotated %v, events %v, pending %v; want none", c.Rotated, c.Events, c.EventsPending)
	}
}

func TestValsetChangesShowARotationAsOne(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putSets(t, s, map[string][]Member{"A": {m1(valG0Y, 1), m1(val15T, 1)}, "B": {m1(valG0Y, 1), m1(valHQH, 1)}})
	writeRecs(t, s, onSets(1, 3, on("A")))
	writeRecs(t, s, onSets(4, 5, on("B")))
	rotated := Event{Type: "ValoperRotated", Operator: "g1op", OldAddress: val15T, NewAddress: valHQH, Applied: true}
	if err := s.PutEvents(ctx, 4, 2, []Event{rotated}); err != nil {
		t.Fatal(err)
	}
	c := changeAt(t, s, 4)
	if !reflect.DeepEqual(c.Rotated, []Rotation{{Operator: "g1op", OldAddress: val15T, NewAddress: valHQH, Power: 1}}) {
		t.Errorf("rotated = %v, want g1op %s → %s", c.Rotated, val15T, valHQH)
	}
	if len(c.Added) != 0 || len(c.Removed) != 0 {
		t.Errorf("added %v, removed %v; want none: the rotation holds them", c.Added, c.Removed)
	}
	if !reflect.DeepEqual(c.Events, []Event{rotated}) {
		t.Errorf("events = %v, want the rotation", c.Events)
	}
}

func TestValsetChangesSayEventsArePending(t *testing.T) {
	s := newTestStore(t)
	putSets(t, s, map[string][]Member{"A": {m1(valG0Y, 1)}, "B": {m1(valG0Y, 1), m1(val15T, 1)}})
	writeRecs(t, s, onSets(1, 3, on("A")))
	writeRecs(t, s, onSets(4, 5, on("B")))
	if err := s.AddEventGap(context.Background(), 4); err != nil {
		t.Fatal(err)
	}
	if c := changeAt(t, s, 4); !c.EventsPending || len(c.Events) != 0 {
		t.Errorf("pending %v, events %v; want pending, none", c.EventsPending, c.Events)
	}
}

// pagedChanges stores 1..10 with changes at 4, 7 and 9.
func pagedChanges(t *testing.T) *Store {
	t.Helper()
	s := newTestStore(t)
	putChangeSets(t, s)
	writeRecs(t, s, onSets(1, 10, func(h int64) string {
		if (h >= 4 && h <= 6) || h >= 9 {
			return "B"
		}
		return "A"
	}))
	return s
}

func TestValsetChangesPageNewestFirst(t *testing.T) {
	ctx := context.Background()
	s := pagedChanges(t)
	first, err := s.ValsetChanges(ctx, 11, 2)
	if err != nil || !slices.Equal(heightsOf(first), []int64{9, 7}) {
		t.Fatalf("ValsetChanges(11, 2) = %v, %v; want 9, 7", heightsOf(first), err)
	}
	next, err := s.ValsetChanges(ctx, 7, 5)
	if err != nil || !slices.Equal(heightsOf(next), []int64{4, 1}) {
		t.Fatalf("ValsetChanges(7, 5) = %v, %v; want 4, 1", heightsOf(next), err)
	}
	genesis := next[1]
	if !reflect.DeepEqual(genesis.Added, testSet) || genesis.SizeBefore != 0 || genesis.SizeAfter != 2 || genesis.PowerAfter != 30 {
		t.Errorf("genesis = %+v; want every member of the set added, 0 → 2, power 30", genesis)
	}
}

func TestValsetChangesInGoUp(t *testing.T) {
	got, err := pagedChanges(t).ValsetChangesIn(context.Background(), 5, 9)
	if err != nil || !slices.Equal(heightsOf(got), []int64{7, 9}) {
		t.Errorf("ValsetChangesIn(5, 9) = %v, %v; want 7, 9", heightsOf(got), err)
	}
}

func TestValsetChangeReadsSearchTheKey(t *testing.T) {
	s := pagedChanges(t)
	for _, q := range []struct {
		query string
		args  []any
	}{{valsetChangesBeforeQuery, []any{11, 2}}, {valsetChangesInQuery, []any{5, 9}}} {
		for _, line := range queryPlan(t, s, q.query, q.args...) {
			if strings.Contains(line, "SCAN") {
				t.Errorf("plan of %q has %q, want a search of the key", q.query, line)
			}
		}
	}
}

package blocks

import (
	"context"
	"database/sql"
	"slices"
	"testing"
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

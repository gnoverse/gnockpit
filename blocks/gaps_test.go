package blocks

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"
)

func storedGaps(t *testing.T, s *Store) []int64 {
	t.Helper()
	got, err := gapsIn(context.Background(), s.db, 1, math.MaxInt64)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// gapRecs are heights 1 to 6 one second apart, but block 4 comes gap after block 3.
func gapRecs(gap time.Duration) []Record {
	recs := make([]Record, 6)
	for i := range recs {
		recs[i] = rec(int64(i + 1))
		if i >= 3 {
			recs[i].Time = recs[i].Time.Add(gap - time.Second)
		}
	}
	return recs
}

func TestStoreRecordsAGapGoingUp(t *testing.T) {
	s := newTestStore(t)
	putTestSet(t, s)
	recs := gapRecs(2*time.Minute + time.Second)
	if err := s.Write(context.Background(), recs[:4]); err != nil {
		t.Fatal(err)
	}
	if got := storedGaps(t, s); !reflect.DeepEqual(got, []int64{3}) {
		t.Errorf("gaps = %v, want [3]", got)
	}
}

func TestStoreRecordsAGapAtABatchEdgeGoingDown(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	recs := gapRecs(2*time.Minute + time.Second)
	if err := s.Write(ctx, recs[3:]); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, recs[:3]); err != nil {
		t.Fatal(err)
	}
	if got := storedGaps(t, s); !reflect.DeepEqual(got, []int64{3}) {
		t.Errorf("gaps = %v, want [3]", got)
	}
}

func TestStoreTwoMinutesIsNoGap(t *testing.T) {
	s := newTestStore(t)
	putTestSet(t, s)
	if err := s.Write(context.Background(), gapRecs(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := storedGaps(t, s); len(got) != 0 {
		t.Errorf("gaps = %v, want none: a gap is more than %s", got, RestartGap)
	}
}

func TestStoreFillsTheGapsOnce(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	if err := s.Write(ctx, gapRecs(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blocks_gaps`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blocks_meta WHERE key = ?`, metaGapsFilled); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	if got := storedGaps(t, s); !reflect.DeepEqual(got, []int64{3}) {
		t.Errorf("filled gaps = %v, want [3]", got)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blocks_gaps`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(ctx, s.db); err != nil {
		t.Fatal(err)
	}
	if got := storedGaps(t, s); len(got) != 0 {
		t.Errorf("gaps after a second NewStore = %v, want none: the fill runs once", got)
	}
}

func TestStoreForgetsTheGapsOnReset(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	if err := s.Write(ctx, gapRecs(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(ctx, "test-chain"); err != nil {
		t.Fatal(err)
	}
	if got := storedGaps(t, s); len(got) != 0 {
		t.Errorf("gaps after Reset = %v, want none", got)
	}
	if v, err := s.meta(ctx, metaGapsFilled); err != nil || v == "" {
		t.Errorf("meta %s after Reset = %q, %v; want it set", metaGapsFilled, v, err)
	}
}

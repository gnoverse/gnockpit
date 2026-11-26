package blocks

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func windowAsOf(t *testing.T, s *Store, now time.Time) ScoringWindow {
	t.Helper()
	w, err := s.ScoringWindow(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// windowDays is the scoring window's days as of now, without heights.
func windowDays(now time.Time) ScoringWindow {
	today := now.Truncate(24 * time.Hour)
	return ScoringWindow{FirstDay: today.AddDate(0, 0, -30), LastDay: today.AddDate(0, 0, -1), SpanStart: today.AddDate(0, 0, -180)}
}

func TestScoringWindowHeights(t *testing.T) {
	// Four blocks a day, at 03:00, 09:00, 15:00 and 21:00, from day −31 to
	// today's 09:00: day −30 starts at 5, day −1 ends at 31 × 4.
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	var blocks []chainBlock
	for at := now.Truncate(24*time.Hour).AddDate(0, 0, -31).Add(3 * time.Hour); at.Before(now); at = at.Add(6 * time.Hour) {
		blocks = append(blocks, chainBlock{at: at})
	}
	s := newTestStore(t)
	writeChain(t, s, blocks)
	want := windowDays(now)
	want.First, want.Last, want.Complete = 5, 124, true
	if got := windowAsOf(t, s, now); !reflect.DeepEqual(got, want) {
		t.Errorf("ScoringWindow() = %+v, want %+v", got, want)
	}
}

func TestScoringWindowIncompleteBeforeTodaysFirstBlock(t *testing.T) {
	now := time.Date(2026, 11, 19, 0, 0, 30, 0, time.UTC)
	lastDay := time.Date(2026, 11, 18, 23, 59, 50, 0, time.UTC)
	s := newTestStore(t)
	writeChain(t, s, chainOf(5, func(h int64) time.Time { return lastDay.Add(time.Duration(h-1) * 2 * time.Second) }, nil))
	want := windowDays(now)
	want.First, want.Last = 1, 5
	if got := windowAsOf(t, s, now); !reflect.DeepEqual(got, want) {
		t.Errorf("ScoringWindow() = %+v, want %+v: the newest block at 23:59:58, today's first not stored", got, want)
	}
}

func TestScoringWindowWithoutBlocksInside(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	everyHourFrom := func(start time.Time) func(int64) time.Time {
		return func(h int64) time.Time { return start.Add(time.Duration(h-1) * time.Hour) }
	}
	for _, tc := range []struct {
		name     string
		blocks   []chainBlock
		complete bool
	}{
		{"nothing stored", nil, false},
		{"before it only", chainOf(5, everyHourFrom(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)), nil), false},
		{"today only", chainOf(5, everyHourFrom(time.Date(2026, 11, 19, 1, 0, 0, 0, time.UTC)), nil), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			if len(tc.blocks) > 0 {
				writeChain(t, s, tc.blocks)
			}
			want := windowDays(now)
			want.Complete = tc.complete
			if got := windowAsOf(t, s, now); !reflect.DeepEqual(got, want) {
				t.Errorf("ScoringWindow() = %+v, want %+v", got, want)
			}
		})
	}
}

func TestProposalFlags(t *testing.T) {
	lateBlock := func(h int64, proposer string, rounds ...string) Record {
		r := rec(h)
		r.Proposer, r.Round, r.Rounds = proposer, len(rounds)-1, rounds
		return r
	}
	flagged := func(r Record) Record {
		r.Flag = "round 0's proposer unknown"
		return r
	}
	s := newTestStore(t)
	putTestSet(t, s)
	writeRecs(t, s, []Record{
		flagged(rec(1)),                           // below the span
		lateBlock(2, valG0Y, val15T, val15T),      // no round expects the header's proposer
		lateBlock(3, valG0Y, val15T, valG0Y),      // k = 1
		flagged(rec(4)),                           // a round-0 block flagged
		flagged(lateBlock(5, valG0Y, "", valG0Y)), // flagged, round 0's proposer unknown
		lateBlock(6, valG0Y, val15T, val15T),      // above the span
	})
	got, err := s.ProposalFlags(context.Background(), 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	if want := []int64{2, 4, 5}; !reflect.DeepEqual(got, want) {
		t.Errorf("ProposalFlags(2, 5) = %v, want %v", got, want)
	}
}

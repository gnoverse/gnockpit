package history

import (
	"context"
	"database/sql"
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
	store, err := NewStore(context.Background(), db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

// blk builds one block record with the given height, time, and missing-validator
// addresses.
func blk(height int64, t time.Time, missing ...string) Block {
	return Block{Height: height, Time: t, Missing: missing}
}

func TestRecordAndMissedInWindow(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	// Three blocks inside 24h, one outside.
	blocks := []Block{
		blk(100, now.Add(-30*time.Minute), "valA"),
		blk(101, now.Add(-2*time.Hour), "valA", "valB"),
		blk(102, now.Add(-10*time.Hour), "valB"),
		blk(103, now.Add(-26*time.Hour), "valA", "valB"), // outside 24h
	}
	if err := s.RecordBlocks(ctx, blocks); err != nil {
		t.Fatalf("RecordBlocks: %v", err)
	}

	wc, err := s.MissedInWindow(ctx, 24*time.Hour, now)
	if err != nil {
		t.Fatalf("MissedInWindow: %v", err)
	}
	if wc.Blocks != 3 {
		t.Errorf("MissedInWindow(24h).Blocks = %d, want 3", wc.Blocks)
	}
	if got := wc.Missed["valA"]; got != 2 {
		t.Errorf("Missed[valA] = %d, want 2", got)
	}
	if got := wc.Missed["valB"]; got != 2 {
		t.Errorf("Missed[valB] = %d, want 2", got)
	}

	// window <= 0 means "all history": block 103 and its misses count too.
	all, err := s.MissedInWindow(ctx, 0, now)
	if err != nil {
		t.Fatalf("MissedInWindow(all): %v", err)
	}
	if all.Blocks != 4 {
		t.Errorf("MissedInWindow(all).Blocks = %d, want 4", all.Blocks)
	}
	if got := all.Missed["valA"]; got != 3 {
		t.Errorf("all Missed[valA] = %d, want 3", got)
	}
	if got := all.Missed["valB"]; got != 3 {
		t.Errorf("all Missed[valB] = %d, want 3", got)
	}
}

func TestRecordBlocksDedup(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	blocks := []Block{
		blk(200, now.Add(-time.Minute), "valA"),
		blk(201, now.Add(-2*time.Minute), "valA", "valB"),
	}
	if err := s.RecordBlocks(ctx, blocks); err != nil {
		t.Fatalf("RecordBlocks (first): %v", err)
	}
	// Re-record the same heights (overlapping fetch windows do this every cycle).
	if err := s.RecordBlocks(ctx, blocks); err != nil {
		t.Fatalf("RecordBlocks (second): %v", err)
	}
	wc, err := s.MissedInWindow(ctx, time.Hour, now)
	if err != nil {
		t.Fatalf("MissedInWindow: %v", err)
	}
	if wc.Blocks != 2 {
		t.Errorf("Blocks after re-record = %d, want 2 (dedup by height)", wc.Blocks)
	}
	if got := wc.Missed["valA"]; got != 2 {
		t.Errorf("Missed[valA] after re-record = %d, want 2", got)
	}
}

func TestEarliestRecorded(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	if got, err := s.EarliestRecorded(ctx); err != nil {
		t.Fatalf("EarliestRecorded (empty): %v", err)
	} else if !got.IsZero() {
		t.Errorf("EarliestRecorded (empty) = %v, want zero", got)
	}

	earliest := now.Add(-5 * time.Hour)
	if err := s.RecordBlocks(ctx, []Block{
		blk(300, now.Add(-time.Hour)),
		blk(301, earliest, "valA"),
	}); err != nil {
		t.Fatalf("RecordBlocks: %v", err)
	}
	got, err := s.EarliestRecorded(ctx)
	if err != nil {
		t.Fatalf("EarliestRecorded: %v", err)
	}
	if !got.Equal(earliest) {
		t.Errorf("EarliestRecorded() = %v, want %v", got, earliest)
	}
}

func TestPrune(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if err := s.RecordBlocks(ctx, []Block{
		blk(400, now.Add(-40*24*time.Hour), "valA"), // old
		blk(401, now.Add(-1*time.Hour), "valB"),     // recent
	}); err != nil {
		t.Fatalf("RecordBlocks: %v", err)
	}
	cutoff := now.Add(-31 * 24 * time.Hour)
	if err := s.Prune(ctx, cutoff); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	wc, err := s.MissedInWindow(ctx, 0, now)
	if err != nil {
		t.Fatalf("MissedInWindow: %v", err)
	}
	if wc.Blocks != 1 {
		t.Errorf("Blocks after prune = %d, want 1", wc.Blocks)
	}
	if _, ok := wc.Missed["valA"]; ok {
		t.Errorf("valA miss survived prune, want removed")
	}
	if got := wc.Missed["valB"]; got != 1 {
		t.Errorf("Missed[valB] after prune = %d, want 1", got)
	}
}

func TestRecordBlocksEmpty(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.RecordBlocks(ctx, nil); err != nil {
		t.Fatalf("RecordBlocks(nil): %v", err)
	}
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	wc, err := s.MissedInWindow(ctx, time.Hour, now)
	if err != nil {
		t.Fatalf("MissedInWindow: %v", err)
	}
	if wc.Blocks != 0 || len(wc.Missed) != 0 {
		t.Errorf("empty store window = %+v, want zero", wc)
	}
}

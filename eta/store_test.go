package eta

import (
	"context"
	"database/sql"
	"slices"
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

var t0 = time.Date(2026, 11, 16, 10, 51, 11, 208_000_000, time.UTC)

func TestStoreSamples(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if err := s.AddSamples(ctx, []Sample{
		{Height: 300, Time: t0.Add(700 * time.Second)},
		{Height: 100, Time: t0},
	}); err != nil {
		t.Fatal(err)
	}
	// A height already stored keeps its first time.
	if err := s.AddSamples(ctx, []Sample{
		{Height: 200, Time: t0.Add(350 * time.Second)},
		{Height: 100, Time: t0.Add(time.Hour)},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Samples(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []Sample{
		{Height: 100, Time: t0},
		{Height: 200, Time: t0.Add(350 * time.Second)},
		{Height: 300, Time: t0.Add(700 * time.Second)},
	}
	if !slices.EqualFunc(got, want, func(a, b Sample) bool { return a.Height == b.Height && a.Time.Equal(b.Time) }) {
		t.Errorf("Samples() = %v, want %v (ascending, millisecond precision, first time kept)", got, want)
	}
}

func TestStoreFloor(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if got, err := s.Floor(ctx); err != nil || got != 0 {
		t.Fatalf("Floor() on a new store = %d, %v; want 0, nil", got, err)
	}
	if err := s.SetFloor(ctx, 5000); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Floor(ctx); err != nil || got != 5000 {
		t.Errorf("Floor() = %d, %v; want 5000, nil", got, err)
	}
}

func TestStoreHaltFirstSeen(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	first, err := s.NoteHalt(ctx, 1651200, t0)
	if err != nil {
		t.Fatal(err)
	}
	again, err := s.NoteHalt(ctx, 1651200, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equal(t0) || !again.Equal(t0) {
		t.Errorf("NoteHalt() = %v then %v, want the first sighting %v both times", first, again, t0)
	}
}

func TestStoreReset(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	if got, err := s.ChainID(ctx); err != nil || got != "" {
		t.Fatalf("ChainID() on a new store = %q, %v; want empty, nil", got, err)
	}
	if err := s.AddSamples(ctx, []Sample{{Height: 100, Time: t0}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFloor(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.NoteHalt(ctx, 900, t0); err != nil {
		t.Fatal(err)
	}

	if err := s.Reset(ctx, "gnoland-1"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.ChainID(ctx); err != nil || got != "gnoland-1" {
		t.Errorf("ChainID() after Reset = %q, %v; want gnoland-1, nil", got, err)
	}
	if got, _ := s.Samples(ctx); len(got) != 0 {
		t.Errorf("Samples() after Reset = %v, want none", got)
	}
	if got, _ := s.Floor(ctx); got != 0 {
		t.Errorf("Floor() after Reset = %d, want 0", got)
	}
	if seen, _ := s.NoteHalt(ctx, 900, t0.Add(time.Hour)); !seen.Equal(t0.Add(time.Hour)) {
		t.Errorf("NoteHalt() after Reset = %v, want the new sighting %v", seen, t0.Add(time.Hour))
	}
}

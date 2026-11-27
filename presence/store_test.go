package presence

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/node"
	_ "modernc.org/sqlite"
)

const chain = "test-a"

var now = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func newTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close the test database: %v", err)
		}
	})
	s, err := NewStore(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

func record(t *testing.T, s *Store, name string, at time.Time, ids ...string) {
	t.Helper()
	if err := s.Record(context.Background(), name, at, ids); err != nil {
		t.Fatalf("Record(%s, %s, %v): %v", name, at, ids, err)
	}
}

func rowCount(t *testing.T, db *sql.DB, table, day string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE day = ?`, day).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRecordCountsEachListingOnce(t *testing.T) {
	s, _ := newTestStore(t)
	record(t, s, chain, now, "a", "b", "a", "")
	record(t, s, chain, now.Add(time.Second), "a")
	record(t, s, chain, now.Add(2*time.Second), "a", "b")

	got, err := s.Figures(context.Background(), chain, now, []string{"a", "b", "c"})
	if err != nil {
		t.Fatal(err)
	}
	if got["a"].Listed != 3 || got["a"].Polls != 3 {
		t.Errorf("a = %+v, want 3 of 3", got["a"])
	}
	if got["b"].Listed != 2 || got["b"].Polls != 3 {
		t.Errorf("b = %+v, want 2 of 3", got["b"])
	}
	if _, ok := got["c"]; ok {
		t.Errorf("c has an entry: %+v", got["c"])
	}
}

func TestFiguresCoverTodayAndThe29DaysBefore(t *testing.T) {
	s, _ := newTestStore(t)
	record(t, s, chain, time.Date(2026, 10, 19, 23, 59, 59, 0, time.UTC), "a")
	record(t, s, chain, time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC), "a")
	record(t, s, chain, now, "b")

	got, err := s.Figures(context.Background(), chain, now, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if got["a"].Polls != 2 || got["a"].Listed != 1 || got["b"].Listed != 1 {
		t.Errorf("a = %+v, b = %+v, want 1 of 2 each", got["a"], got["b"])
	}
}

func TestFiguresMarkTheChainsFirstPoll(t *testing.T) {
	s, _ := newTestStore(t)
	t0 := now
	t1 := now.Add(time.Minute)
	record(t, s, chain, t0, "a")
	record(t, s, chain, t1, "a", "b")

	got, err := s.Figures(context.Background(), chain, now, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if got["a"].FirstSeen != t0.Format(time.RFC3339) || !got["a"].FromStart {
		t.Errorf("a = %+v, want first seen %s from start", got["a"], t0.Format(time.RFC3339))
	}
	if got["b"].FirstSeen != t1.Format(time.RFC3339) || got["b"].FromStart {
		t.Errorf("b = %+v, want first seen %s, not from start", got["b"], t1.Format(time.RFC3339))
	}
}

func TestFirstSeenOutlivesTheWindow(t *testing.T) {
	s, _ := newTestStore(t)
	old := now.Add(-40 * 24 * time.Hour)
	record(t, s, chain, old, "a")
	record(t, s, chain, now, "a")

	got, err := s.Figures(context.Background(), chain, now, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	want := presenceOf(1, 1, old.Format(time.RFC3339), true)
	if got["a"] != want {
		t.Errorf("a = %+v, want %+v", got["a"], want)
	}
}

func TestRecordDropsOldDaysAtTheFirstPollOfADay(t *testing.T) {
	s, db := newTestStore(t)
	record(t, s, chain, time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC), "a")
	record(t, s, chain, time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC), "a")
	record(t, s, chain, now, "a")

	for _, table := range []string{"presence_polls", "presence_listed"} {
		if n := rowCount(t, db, table, "2026-10-14"); n != 0 {
			t.Errorf("%s keeps %d rows of 2026-10-14, want 0", table, n)
		}
		if n := rowCount(t, db, table, "2026-10-15"); n != 1 {
			t.Errorf("%s keeps %d rows of 2026-10-15, want 1", table, n)
		}
	}

	if _, err := db.Exec(`INSERT INTO presence_polls(chain, day, polls) VALUES(?, '2026-10-01', 1)`, chain); err != nil {
		t.Fatal(err)
	}
	record(t, s, chain, now.Add(time.Second), "a")
	if n := rowCount(t, db, "presence_polls", "2026-10-01"); n != 1 {
		t.Errorf("a second poll of the day pruned: %d rows of 2026-10-01, want 1", n)
	}

	record(t, s, chain, now.Add(24*time.Hour), "a")
	if n := rowCount(t, db, "presence_polls", "2026-10-01"); n != 0 {
		t.Errorf("the first poll of the next day kept %d rows of 2026-10-01, want 0", n)
	}
}

func TestChainsKeepTheirOwnPresence(t *testing.T) {
	s, _ := newTestStore(t)
	t0 := now
	t1 := now.Add(time.Minute)
	record(t, s, "x", t0, "a")
	record(t, s, "y", t1, "a")

	y, err := s.Figures(context.Background(), "y", now, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	want := presenceOf(1, 1, t1.Format(time.RFC3339), true)
	if y["a"] != want {
		t.Errorf("y: a = %+v, want %+v", y["a"], want)
	}
	x, err := s.Figures(context.Background(), "x", now, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if x["a"].FirstSeen != t0.Format(time.RFC3339) {
		t.Errorf("x: a first seen %s, want %s", x["a"].FirstSeen, t0.Format(time.RFC3339))
	}
}

func TestFiguresWithoutPolls(t *testing.T) {
	s, _ := newTestStore(t)
	got, err := s.Figures(context.Background(), chain, now, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("Figures on an empty store = %+v, want empty", got)
	}
}

func presenceOf(listed, polls int, firstSeen string, fromStart bool) node.Presence {
	return node.Presence{Listed: listed, Polls: polls, FirstSeen: firstSeen, FromStart: fromStart}
}

func TestFiguresWithoutIDsOnAStoreWithPolls(t *testing.T) {
	s, _ := newTestStore(t)
	record(t, s, chain, now, "a")
	got, err := s.Figures(context.Background(), chain, now, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("Figures without ids = %+v, want empty", got)
	}
}

func TestRecordOfEmptyIDsStillCountsThePoll(t *testing.T) {
	s, _ := newTestStore(t)
	record(t, s, chain, now, "a")
	record(t, s, chain, now.Add(time.Second), "", "")
	got, err := s.Figures(context.Background(), chain, now, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if got["a"].Listed != 1 || got["a"].Polls != 2 {
		t.Errorf("a = %+v, want 1 of 2", got["a"])
	}
}

package web

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/node"
)

const (
	histValA = "g1g0yrmdhney8pcaf9rfut4604equz604pszdsyt"
	histValB = "g15t7f9q6km3ldt885duwl8xu5dncs98528amk4f"
)

func newBlocksStore(t *testing.T) *blocks.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	s, err := blocks.NewStore(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutValset(context.Background(), "SET", []blocks.Member{{Address: histValA, Power: 1}, {Address: histValB, Power: 1}}); err != nil {
		t.Fatal(err)
	}
	return s
}

// histBlock is the record of height at, on the test set.
func histBlock(height int64, at time.Time, absences ...blocks.Absence) blocks.Record {
	return blocks.Record{Height: height, Time: at, Proposer: histValA, ValsetHash: "SET", Absences: absences}
}

// writeMissedBlocks stores: 2 h ago, A absent; 30 min ago, A for another
// block and B nil; 10 min ago, all signed.
func writeMissedBlocks(t *testing.T, s *blocks.Store, now time.Time) {
	t.Helper()
	if err := s.Write(context.Background(), []blocks.Record{
		histBlock(1, now.Add(-2*time.Hour), blocks.Absence{Address: histValA, Kind: blocks.Absent}),
		histBlock(2, now.Add(-30*time.Minute), blocks.Absence{Address: histValA, Kind: blocks.OtherBlock}, blocks.Absence{Address: histValB, Kind: blocks.Nil}),
		histBlock(3, now.Add(-10*time.Minute)),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestBuildStatsMissedWindows(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	s := newBlocksStore(t)
	writeMissedBlocks(t, s, now)
	snap := &node.Snapshot{Validators: []node.Validator{{Address: histValA}, {Address: histValB}}}
	st := buildStats(context.Background(), snap, s, now)
	if got := st.Validators.WindowBlocks; got["1h"] != 2 || got["24h"] != 3 || got["total"] != 3 {
		t.Errorf("window blocks = %v, want 1h 2, 24h 3, total 3", got)
	}
	missed := map[string]map[string]int{}
	for _, v := range st.Validators.Missed {
		missed[v.Address] = v.Missed
	}
	// A nil vote counts as signed; a vote for another block as missed.
	if a := missed[histValA]; a["1h"] != 1 || a["24h"] != 2 {
		t.Errorf("A missed = %v, want 1h 1, 24h 2", a)
	}
	if b := missed[histValB]; b["24h"] != 0 {
		t.Errorf("B missed = %v, want none", b)
	}
	if want := now.Add(-2 * time.Hour).Format(time.RFC3339); st.RecordedSince != want {
		t.Errorf("recorded since = %q, want %q", st.RecordedSince, want)
	}
}

func TestRefreshMissed(t *testing.T) {
	s := newBlocksStore(t)
	writeMissedBlocks(t, s, time.Now())
	srv := &Server{Blocks: s}
	srv.refreshMissed(context.Background())
	if srv.missed24h[histValA] != 2 || srv.missed24h[histValB] != 0 {
		t.Errorf("missed 24h = %v, want A 2, B 0", srv.missed24h)
	}
	if srv.missedSince.IsZero() {
		t.Error("missed since unset")
	}
}

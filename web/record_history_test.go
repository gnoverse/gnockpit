package web

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/history"
	"github.com/gnoverse/gnockpit/node"

	_ "modernc.org/sqlite"
)

// TestRecordHistorySkipsNewestBlock is the regression test for a permanent
// miscount in missed_24h.
//
// /commit at the chain tip returns canonical:false and only the precommits the
// observing node has received so far, so a validator whose vote is still in
// flight reads as absent. The live windows heal themselves because the same
// height is complete when read again; the history store does not, because it
// writes INSERT OR IGNORE and never backfills. A transient in-flight vote
// therefore became a permanent missed block.
//
// Here "gB" is absent only from the newest block in the window. Nothing about
// it may reach the store.
func TestRecordHistorySkipsNewestBlock(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	ctx := context.Background()
	store, err := history.NewStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	snap := &node.Snapshot{Signing: &node.SigningStats{RecentBlocks: []node.BlockInfo{
		{Height: "100", Time: now.Add(-6 * time.Second).Format(time.RFC3339Nano)},
		{Height: "101", Time: now.Add(-3 * time.Second).Format(time.RFC3339Nano),
			Missing: []node.MissingValidator{{Address: "gA"}}},
		// The tip. gB's vote is simply still in flight.
		{Height: "102", Time: now.Format(time.RFC3339Nano),
			Missing: []node.MissingValidator{{Address: "gB"}}},
	}}}

	srv := &Server{History: store}
	srv.recordHistory(ctx, snap)

	counts, err := store.MissedInWindow(ctx, 24*time.Hour, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got := counts.Missed["gB"]; got != 0 {
		t.Errorf("gB missed = %d, want 0: the unfinalized tip must never be persisted", got)
	}
	if got := counts.Missed["gA"]; got != 1 {
		t.Errorf("gA missed = %d, want 1: finalized heights must still be recorded", got)
	}
}

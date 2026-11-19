package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
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
	if err := s.PutValset(context.Background(), "SET", []blocks.Member{{Address: histValA, PubKey: "KA", Power: 1}, {Address: histValB, PubKey: "KB", Power: 1}}); err != nil {
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

type fakeFill blocks.Progress

func (f fakeFill) Progress() blocks.Progress { return blocks.Progress(f) }

func getHistory(t *testing.T, srv *Server, query string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleHistory(rec, httptest.NewRequest(http.MethodGet, "/api/history"+query, nil))
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS header = %q, want *", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return rec.Code, body
}

func TestHistoryDisabled(t *testing.T) {
	code, body := getHistory(t, &Server{}, "")
	if code != http.StatusServiceUnavailable || body["error"] != "block history disabled" {
		t.Errorf("got %d %v, want 503 block history disabled", code, body)
	}
}

func TestHistoryProgress(t *testing.T) {
	from := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	srv := &Server{Blocks: newBlocksStore(t), BlockFill: fakeFill{ChainID: "gnoland-1", From: 10, To: 12, FromTime: from, Floor: 5, Rate: 12.5}}
	code, body := getHistory(t, srv, "")
	want := map[string]any{
		"chain":  "gnoland-1",
		"filled": map[string]any{"from": 10.0, "from_time": "2026-11-03T08:00:00.000Z", "to": 12.0, "floor": 5.0, "complete": false, "rate": 12.5},
	}
	if code != http.StatusOK || !reflect.DeepEqual(body, want) {
		t.Errorf("got %d %v, want 200 %v", code, body, want)
	}
}

func TestHistoryRange(t *testing.T) {
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	s := newBlocksStore(t)
	writeMissedBlocks(t, s, now)
	srv := &Server{Blocks: s, BlockFill: fakeFill{ChainID: "gnoland-1", From: 1, To: 3, FromTime: now.Add(-2 * time.Hour), Complete: true}}
	// Height 4 is outside the held range: only 2 and 3 come back.
	code, body := getHistory(t, srv, "?from=2&to=4")
	if code != http.StatusOK {
		t.Fatalf("status = %d, body %v", code, body)
	}
	wantBlocks := []any{
		map[string]any{
			"height": 2.0, "time": "2026-11-19T11:30:00.000Z", "proposer": histValA, "round": 0.0, "txs": 0.0, "valset": "SET",
			"absent": []any{}, "nil": []any{histValB}, "other_block": []any{histValA},
		},
		map[string]any{
			"height": 3.0, "time": "2026-11-19T11:50:00.000Z", "proposer": histValA, "round": 0.0, "txs": 0.0, "valset": "SET",
			"absent": []any{}, "nil": []any{}, "other_block": []any{},
		},
	}
	if !reflect.DeepEqual(body["blocks"], wantBlocks) {
		t.Errorf("blocks = %v, want %v", body["blocks"], wantBlocks)
	}
	wantSets := map[string]any{"SET": []any{
		map[string]any{"address": histValA, "pub_key": "KA", "power": 1.0},
		map[string]any{"address": histValB, "pub_key": "KB", "power": 1.0},
	}}
	if !reflect.DeepEqual(body["valsets"], wantSets) {
		t.Errorf("valsets = %v, want %v", body["valsets"], wantSets)
	}
}

func TestHistoryRangeNotHeld(t *testing.T) {
	s := newBlocksStore(t)
	writeMissedBlocks(t, s, time.Now())
	srv := &Server{Blocks: s, BlockFill: fakeFill{ChainID: "gnoland-1", From: 1, To: 3}}
	// A range the fill has not reached is empty, not missing.
	code, body := getHistory(t, srv, "?from=10&to=12")
	if code != http.StatusOK || !reflect.DeepEqual(body["blocks"], []any{}) || !reflect.DeepEqual(body["valsets"], map[string]any{}) {
		t.Errorf("got %d %v, want 200 with empty blocks and valsets", code, body)
	}
}

func TestHistoryBadRange(t *testing.T) {
	srv := &Server{Blocks: newBlocksStore(t), BlockFill: fakeFill{}}
	for _, q := range []string{"?from=1", "?to=2", "?from=a&to=2", "?from=0&to=2", "?from=5&to=4", "?from=1&to=1001", "?days=0", "?days=32", "?days=x"} {
		if code, body := getHistory(t, srv, q); code != http.StatusBadRequest || body["error"] == nil {
			t.Errorf("%s: got %d %v, want 400 with an error", q, code, body)
		}
	}
}

func TestHistoryOmitsAnUnknownRate(t *testing.T) {
	srv := &Server{Blocks: newBlocksStore(t), BlockFill: fakeFill{ChainID: "gnoland-1", From: 10, To: 12}}
	_, body := getHistory(t, srv, "")
	if filled, _ := body["filled"].(map[string]any); filled == nil || filled["rate"] != nil {
		t.Errorf("filled = %v, want no rate", body["filled"])
	}
}

func TestHistoryRoundsFlagsAndSetChanges(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	s := newBlocksStore(t)
	late := histBlock(6, now)
	late.Round, late.Rounds, late.Flag = 1, []string{"", histValA}, "test flag"
	if err := s.Write(ctx, []blocks.Record{histBlock(5, now.Add(-time.Second)), late}); err != nil {
		t.Fatal(err)
	}
	if err := s.PutEvents(ctx, 6, 4, []blocks.Event{{Type: "ValidatorAdded", Operator: "g1op", Address: histValB, Power: 1, Applied: true}}); err != nil {
		t.Fatal(err)
	}
	srv := &Server{Blocks: s, BlockFill: fakeFill{ChainID: "gnoland-1", From: 5, To: 6}}
	_, body := getHistory(t, srv, "?from=5&to=6")
	got, _ := body["blocks"].([]any)
	if len(got) != 2 {
		t.Fatalf("blocks = %v, want 2", body["blocks"])
	}
	first, second := got[0].(map[string]any), got[1].(map[string]any)
	for _, k := range []string{"rounds", "flag", "set_change"} {
		if _, ok := first[k]; ok {
			t.Errorf("block 5 has %q, want none", k)
		}
	}
	if want := []any{"", histValA}; !reflect.DeepEqual(second["rounds"], want) {
		t.Errorf("rounds = %v, want %v", second["rounds"], want)
	}
	if second["flag"] != "test flag" {
		t.Errorf("flag = %v, want test flag", second["flag"])
	}
	wantChange := map[string]any{"source": 4.0, "events": []any{
		map[string]any{"type": "ValidatorAdded", "operator": "g1op", "address": histValB, "power": 1.0, "applied": true},
	}}
	if !reflect.DeepEqual(second["set_change"], wantChange) {
		t.Errorf("set_change = %v, want %v", second["set_change"], wantChange)
	}
}

func TestHistoryVoteDelays(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	s := newBlocksStore(t)
	old, yesterday, today := histBlock(1, now.Add(-40*24*time.Hour)), histBlock(2, now.Add(-24*time.Hour)), histBlock(3, now)
	old.Delays = []blocks.Delay{{Address: histValA, Ms: 0}}
	yesterday.Delays = []blocks.Delay{{Address: histValA, Ms: 300}}
	today.Delays = []blocks.Delay{{Address: histValA, Ms: -1200}, {Address: histValB, Ms: 100}, {Address: histValB, Ms: 300}}
	if err := s.Write(ctx, []blocks.Record{old, yesterday, today}); err != nil {
		t.Fatal(err)
	}
	srv := &Server{Blocks: s, BlockFill: fakeFill{ChainID: "gnoland-1", From: 1, To: 3}}
	code, body := getHistory(t, srv, "?days=31")
	if code != http.StatusOK {
		t.Fatalf("status = %d, body %v", code, body)
	}
	counts := func(addr string, votes, mean float64, buckets ...float64) map[string]any {
		bs := make([]any, len(buckets))
		for i, b := range buckets {
			bs[i] = b
		}
		return map[string]any{"address": addr, "votes": votes, "mean_ms": mean, "buckets": bs}
	}
	want := map[string]any{
		"edges_ms": []any{-1000.0, -250.0, 250.0, 1000.0, 2500.0, 5000.0},
		// The day 40 days back is outside the 31 asked.
		"days": []any{
			map[string]any{"day": yesterday.Time.Format(time.DateOnly), "validators": []any{counts(histValA, 1, 300, 0, 0, 0, 1, 0, 0, 0)}},
			// By address within a day: g15t… (B) before g1g0… (A).
			map[string]any{"day": today.Time.Format(time.DateOnly), "validators": []any{
				counts(histValB, 2, 200, 0, 0, 1, 1, 0, 0, 0),
				counts(histValA, 1, -1200, 1, 0, 0, 0, 0, 0, 0),
			}},
		},
	}
	if !reflect.DeepEqual(body["vote_delays"], want) {
		t.Errorf("vote_delays =\n%v\nwant\n%v", body["vote_delays"], want)
	}
}

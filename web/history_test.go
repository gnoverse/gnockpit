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
	srv := &Server{Blocks: newBlocksStore(t), BlockFill: fakeFill{ChainID: "gnoland-1", From: 10, To: 12, FromTime: from, Floor: 5}}
	code, body := getHistory(t, srv, "")
	want := map[string]any{
		"chain":  "gnoland-1",
		"filled": map[string]any{"from": 10.0, "from_time": "2026-11-03T08:00:00.000Z", "to": 12.0, "floor": 5.0, "complete": false},
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
	// Heights 0 and 4 are outside the held range: only 1 to 3 come back.
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
		map[string]any{"address": histValA, "power": 1.0},
		map[string]any{"address": histValB, "power": 1.0},
	}}
	if !reflect.DeepEqual(body["valsets"], wantSets) {
		t.Errorf("valsets = %v, want %v", body["valsets"], wantSets)
	}
}

func TestHistoryBadRange(t *testing.T) {
	srv := &Server{Blocks: newBlocksStore(t), BlockFill: fakeFill{}}
	for _, q := range []string{"?from=1", "?to=2", "?from=a&to=2", "?from=0&to=2", "?from=5&to=4", "?from=1&to=1001"} {
		if code, body := getHistory(t, srv, q); code != http.StatusBadRequest || body["error"] == nil {
			t.Errorf("%s: got %d %v, want 400 with an error", q, code, body)
		}
	}
}

package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/location"
	"github.com/gnoverse/gnockpit/node"
)

const (
	valOperator    = "g1dacx2unpw3hhytt0dejjut3w9chzut3wnddcuk"
	valOperatorTwo = "g1dacx2unpw3hhytt5wahjut3w9chzut3wwgrww2"
	valOldKey      = "g1dakxgttnd9nku6twvuhzut3w9chzut3wqwqg2q"
	valNewKey      = "g1dejhwttnd9nku6twvuhzut3w9chzut3wu4nqp0"
	valLoneKey     = "g1d3hkuefdwd5kwmnfdenjut3w9chzut3whjn2u4"
	valUnknown     = "g1vdhhxmt0wvkhgetnwskkzerywfjhxuep35wa6p" // valid, in no set
)

// valNow is the tests' clock: the fixtures' days and the reports' windows
// read one instant, whatever midnight a run spans.
var valNow = time.Now().UTC()

func getValidator(t *testing.T, srv *Server, query string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.serveValidator(rec, httptest.NewRequest(http.MethodGet, "/api/validator"+query, nil), valNow)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS header = %q, want *", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return rec.Code, body
}

// valDay is midnight UTC two days ago: a complete day of the scoring window.
func valDay() time.Time {
	return valNow.Truncate(24*time.Hour).AddDate(0, 0, -2)
}

// secondsFrom returns the time of height h, h seconds after base.
func secondsFrom(base time.Time) func(int64) time.Time {
	return func(h int64) time.Time { return base.Add(time.Duration(h) * time.Second) }
}

// putBlocks stores heights from to to on the set hash of members, at at(h),
// proposed by its first member. absent names who misses which height; every
// other member votes on time.
func putBlocks(t *testing.T, s *blocks.Store, at func(int64) time.Time, hash string, members []blocks.Member, from, to int64, absent map[int64][]string) {
	t.Helper()
	ctx := context.Background()
	if err := s.PutValset(ctx, hash, members); err != nil {
		t.Fatal(err)
	}
	var recs []blocks.Record
	for h := from; h <= to; h++ {
		r := blocks.Record{Height: h, Time: at(h), Proposer: members[0].Address, ValsetHash: hash}
		missing := map[string]bool{}
		for _, a := range absent[h] {
			missing[a] = true
			r.Absences = append(r.Absences, blocks.Absence{Address: a, Kind: blocks.Absent})
		}
		for _, m := range members {
			if !missing[m.Address] {
				r.Delays = append(r.Delays, blocks.Delay{Address: m.Address})
			}
		}
		recs = append(recs, r)
	}
	if err := s.Write(ctx, recs); err != nil {
		t.Fatal(err)
	}
}

func putValEvents(t *testing.T, s *blocks.Store, height int64, evs ...blocks.Event) {
	t.Helper()
	if err := s.PutEvents(context.Background(), height, height-2, evs); err != nil {
		t.Fatal(err)
	}
}

// rotationServer serves a chain where the operator's key OLD, in the genesis
// set, rotates to NEW at 100; OLD misses 98, NEW misses 101; LONE signs all.
func rotationServer(t *testing.T) *Server {
	t.Helper()
	s := newBlocksStore(t)
	at := secondsFrom(valDay())
	putBlocks(t, s, at, "S1", []blocks.Member{{Address: valLoneKey, Power: 1}, {Address: valOldKey, Power: 1}}, 1, 99, map[int64][]string{98: {valOldKey}})
	putBlocks(t, s, at, "S2", []blocks.Member{{Address: valLoneKey, Power: 1}, {Address: valNewKey, Power: 1}}, 100, 101, map[int64][]string{101: {valNewKey}})
	putValEvents(t, s, 100, blocks.Event{Type: "ValoperRotated", Operator: valOperator, OldAddress: valOldKey, NewAddress: valNewKey, Power: 1, Applied: true})
	return &Server{Blocks: s, BlockFill: fakeFill{ChainID: "onyx-1", From: 1, To: 101, Complete: true}}
}

func timeOf(at time.Time) string { return at.UTC().Format(historyTime) }

func TestValidatorDisabled(t *testing.T) {
	code, body := getValidator(t, &Server{}, "?val="+valOperator)
	if code != http.StatusServiceUnavailable || body["error"] != "block history disabled" {
		t.Errorf("got %d %v, want 503 block history disabled", code, body)
	}
}

func TestValidatorRejectsAMalformedAddress(t *testing.T) {
	srv := rotationServer(t)
	// The last is a g bech32 string of 32 bytes, not an address's 20.
	for _, q := range []string{"", "?val=", "?val=g1nope", "?val=cosmos1vdhhxmt0wvkhgetnwskkzerywfjhxuepzgj7m9", "?val=g1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqemfmay"} {
		code, body := getValidator(t, srv, q)
		if code != http.StatusBadRequest || body["error"] != "val must be a g1 address" {
			t.Errorf("%q: got %d %v, want 400 val must be a g1 address", q, code, body)
		}
	}
}

func TestValidatorUnknownIs404(t *testing.T) {
	code, body := getValidator(t, rotationServer(t), "?val="+valUnknown)
	if code != http.StatusNotFound || body["error"] != "unknown validator" {
		t.Errorf("got %d %v, want 404 unknown validator", code, body)
	}
}

func TestValidatorOperatorWithARotation(t *testing.T) {
	at := secondsFrom(valDay())
	code, body := getValidator(t, rotationServer(t), "?val="+valOperator)
	if code != http.StatusOK {
		t.Fatalf("status = %d, body %v", code, body)
	}
	edges := blocks.DelayEdges()
	want := map[string]any{
		"chain": "onyx-1", "address": valOperator, "operator": valOperator, "moniker": "",
		// The instant the report is as of, which its windows and ages read.
		"now": timeOf(valNow),
		"keys": []any{
			map[string]any{"address": valOldKey, "from": 0.0, "to": 99.0},
			map[string]any{"address": valNewKey, "from": 100.0},
		},
		"timeline": []any{
			map[string]any{"kind": "rotated", "height": 100.0, "time": timeOf(at(100)), "old_address": valOldKey, "new_address": valNewKey},
			map[string]any{"kind": "genesis", "height": 1.0, "time": timeOf(at(1)), "power": 1.0},
		},
		"vote_delay_edges_ms": []any{float64(edges[0]), float64(edges[1]), float64(edges[2]), float64(edges[3]), float64(edges[4]), float64(edges[5])},
		// The two keys' heights summed: 99 on OLD, 2 on NEW, one absence each.
		"days": []any{map[string]any{
			"day": valDay().Format(time.DateOnly), "in_set": 101.0, "signed": 99.0, "absent": 2.0, "nil": 0.0, "other_block": 0.0,
			"proposals_expected": 0.0, "proposals_missed": 0.0, "votes": 99.0, "vote_delay_mean_ms": 0.0,
			"vote_delay_buckets": []any{0.0, 0.0, 99.0, 0.0, 0.0, 0.0, 0.0},
		}},
		"recoveries": []any{},
	}
	for k, v := range want {
		if !reflect.DeepEqual(body[k], v) {
			t.Errorf("%s = %v, want %v", k, body[k], v)
		}
	}
	for _, k := range []string{"status", "location", "profile"} {
		if _, ok := body[k]; ok {
			t.Errorf("%s = %v, want none: no snapshot, no peer, no profile", k, body[k])
		}
	}
}

func TestValidatorResolvesALinkedSigningAddress(t *testing.T) {
	srv := rotationServer(t)
	_, byOperator := getValidator(t, srv, "?val="+valOperator)
	for _, key := range []string{valOldKey, valNewKey} {
		code, body := getValidator(t, srv, "?val="+key)
		if code != http.StatusOK || body["address"] != key {
			t.Fatalf("%s: got %d, address %v", key, code, body["address"])
		}
		body["address"] = valOperator
		if !reflect.DeepEqual(body, byOperator) {
			t.Errorf("%s: report %v, want the operator's %v", key, body, byOperator)
		}
	}
	// A key no event links, a profile names.
	srv.valopers = map[string]node.Valoper{valOperatorTwo: {Operator: valOperatorTwo, SigningAddress: valLoneKey}}
	code, body := getValidator(t, srv, "?val="+valLoneKey)
	wantKeys := []any{map[string]any{"address": valLoneKey, "from": 0.0}}
	if code != http.StatusOK || body["operator"] != valOperatorTwo || !reflect.DeepEqual(body["keys"], wantKeys) {
		t.Errorf("profile key: got %d operator %v keys %v, want %s %v", code, body["operator"], body["keys"], valOperatorTwo, wantKeys)
	}
}

func TestValidatorUnlinkedSigningAddress(t *testing.T) {
	srv := rotationServer(t)
	code, body := getValidator(t, srv, "?val="+valLoneKey)
	wantKeys := []any{map[string]any{"address": valLoneKey, "from": 0.0}}
	if code != http.StatusOK || body["operator"] != "" || !reflect.DeepEqual(body["keys"], wantKeys) {
		t.Errorf("stored key: got %d operator %q keys %v, want 200 \"\" %v", code, body["operator"], body["keys"], wantKeys)
	}
	// In the current set only: no stored block names it yet.
	srv.setSnapshot(&node.Snapshot{Validators: []node.Validator{{Address: valUnknown, VotingPower: "1"}}})
	code, body = getValidator(t, srv, "?val="+valUnknown)
	wantKeys = []any{map[string]any{"address": valUnknown, "from": 0.0}}
	if code != http.StatusOK || body["operator"] != "" || !reflect.DeepEqual(body["keys"], wantKeys) {
		t.Errorf("current set: got %d operator %q keys %v, want 200 \"\" %v", code, body["operator"], body["keys"], wantKeys)
	}
}

func TestValidatorProfileState(t *testing.T) {
	srv := rotationServer(t)
	profile := node.Valoper{
		Operator: valOperator, Moniker: "one", Description: "runs one", ServerType: "cloud",
		SigningAddress: valNewKey, SigningPubKey: "gpub1one",
		State: &node.ValoperState{KeepRunning: false, LastRotationHeight: 100},
	}
	srv.valopers = map[string]node.Valoper{valOperator: profile}
	want := map[string]any{
		"moniker": "one", "description": "runs one", "server_type": "cloud", "operator": valOperator,
		"signing_address": valNewKey, "signing_pubkey": "gpub1one", "keep_running": false, "last_rotation_height": 100.0,
	}
	_, body := getValidator(t, srv, "?val="+valOperator)
	if !reflect.DeepEqual(body["profile"], want) || body["moniker"] != "one" {
		t.Errorf("profile = %v, moniker %v; want %v, one", body["profile"], body["moniker"], want)
	}
	profile.State, profile.StateError = nil, "qeval refused"
	srv.valopers = map[string]node.Valoper{valOperator: profile}
	delete(want, "keep_running")
	delete(want, "last_rotation_height")
	_, body = getValidator(t, srv, "?val="+valOperator)
	if !reflect.DeepEqual(body["profile"], want) {
		t.Errorf("profile without state = %v, want %v", body["profile"], want)
	}
	_, body = getValidator(t, srv, "?val="+valLoneKey)
	if _, ok := body["profile"]; ok {
		t.Errorf("profile of an unlinked key = %v, want none", body["profile"])
	}
}

func TestValidatorLive(t *testing.T) {
	srv := rotationServer(t)
	snap := &node.Snapshot{
		Validators: []node.Validator{{Address: valLoneKey, VotingPower: "1"}, {Address: valNewKey, VotingPower: "1"}},
		Signing: &node.SigningStats{ValidatorSigning: map[string]node.ValSigning{
			valNewKey: {Missed: 3, Nil: 1, OtherBlock: 2},
		}},
	}
	srv.setSnapshot(snap)
	srv.missed24h = map[string]int{valNewKey: 5}
	srv.proposals24h = map[string][2]int{valNewKey: {7, 1}}
	want := map[string]any{
		"vote_delay_ms": nil, "missed_100": 3.0, "nil_100": 1.0, "other_block_100": 2.0,
		"missed_24h": 5.0, "missed_proposals_24h": 1.0, "expected_proposals_24h": 7.0,
	}
	if _, body := getValidator(t, srv, "?val="+valOperator); !reflect.DeepEqual(body["live"], want) {
		t.Errorf("live without precommits = %v, want %v", body["live"], want)
	}
	snap.Signing.ValidatorSigning[valNewKey] = node.ValSigning{Missed: 3, Nil: 1, OtherBlock: 2, Votes: 4, VoteDelayMs: 210}
	want["vote_delay_ms"] = 210.0
	if _, body := getValidator(t, srv, "?val="+valOperator); !reflect.DeepEqual(body["live"], want) {
		t.Errorf("live = %v, want %v", body["live"], want)
	}
}

func TestValidatorLocation(t *testing.T) {
	srv := rotationServer(t)
	srv.setSnapshot(&node.Snapshot{
		Validators: []node.Validator{{Address: valLoneKey, VotingPower: "1"}, {Address: valNewKey, VotingPower: "1"}},
		Peers:      []node.Peer{{ValAddress: valNewKey, Country: "DE", City: "Berlin", Provider: "Hetzner"}},
	})
	want := map[string]any{"country": "DE", "city": "Berlin", "provider": "Hetzner", "source": "ip"}
	if _, body := getValidator(t, srv, "?val="+valOperator); !reflect.DeepEqual(body["location"], want) {
		t.Errorf("location = %v, want %v", body["location"], want)
	}
	if _, body := getValidator(t, srv, "?val="+valLoneKey); body["location"] != nil {
		t.Errorf("location without a matched peer = %v, want none", body["location"])
	}
}

func TestValidatorStatusSince(t *testing.T) {
	at := secondsFrom(valDay())
	lone := blocks.Member{Address: valLoneKey, Power: 6}
	s := newBlocksStore(t)
	putBlocks(t, s, at, "S0", []blocks.Member{lone}, 1, 49, nil)
	putBlocks(t, s, at, "S1", []blocks.Member{lone, {Address: valOldKey, Power: 4}}, 50, 99, nil)
	putBlocks(t, s, at, "S2", []blocks.Member{lone, {Address: valNewKey, Power: 4}}, 100, 101, nil)
	putValEvents(t, s, 50, blocks.Event{Type: "ValidatorAdded", Operator: valOperator, Address: valOldKey, Power: 4, Applied: true})
	putValEvents(t, s, 100, blocks.Event{Type: "ValoperRotated", Operator: valOperator, OldAddress: valOldKey, NewAddress: valNewKey, Power: 4, Applied: true})
	srv := &Server{Blocks: s, BlockFill: fakeFill{ChainID: "onyx-1", From: 1, To: 101, Complete: true}}
	srv.setSnapshot(&node.Snapshot{Validators: []node.Validator{{Address: valLoneKey, VotingPower: "6"}, {Address: valNewKey, VotingPower: "4"}}})
	// The rotation at 100 is no leave: in the set since it joined at 50.
	want := map[string]any{"in_set": true, "since_height": 50.0, "since_time": timeOf(at(50)), "signing_address": valNewKey, "power": 4.0, "share": 0.4}
	if _, body := getValidator(t, srv, "?val="+valOperator); !reflect.DeepEqual(body["status"], want) {
		t.Errorf("joined, rotated: status = %v, want %v", body["status"], want)
	}

	putBlocks(t, s, at, "S2", []blocks.Member{lone, {Address: valNewKey, Power: 4}}, 102, 199, nil)
	putBlocks(t, s, at, "S0", []blocks.Member{lone}, 200, 201, nil)
	putValEvents(t, s, 200, blocks.Event{Type: "ValidatorRemoved", Operator: valOperator, Address: valNewKey, Applied: true})
	srv.setSnapshot(&node.Snapshot{Validators: []node.Validator{{Address: valLoneKey, VotingPower: "6"}}})
	want = map[string]any{"in_set": false, "since_height": 200.0, "since_time": timeOf(at(200)), "power": 0.0, "share": 0.0}
	_, body := getValidator(t, srv, "?val="+valOperator)
	if !reflect.DeepEqual(body["status"], want) {
		t.Errorf("left: status = %v, want %v", body["status"], want)
	}
	wantTimeline := []any{
		map[string]any{"kind": "left", "height": 200.0, "time": timeOf(at(200)), "power": 4.0},
		map[string]any{"kind": "rotated", "height": 100.0, "time": timeOf(at(100)), "old_address": valOldKey, "new_address": valNewKey},
		map[string]any{"kind": "joined", "height": 50.0, "time": timeOf(at(50)), "power": 4.0},
	}
	if !reflect.DeepEqual(body["timeline"], wantTimeline) {
		t.Errorf("timeline = %v, want %v", body["timeline"], wantTimeline)
	}

	// In the genesis set, never left: since 1. LONE is unlinked.
	srv.setSnapshot(&node.Snapshot{Validators: []node.Validator{{Address: valLoneKey, VotingPower: "6"}}})
	want = map[string]any{"in_set": true, "since_height": 1.0, "since_time": timeOf(at(1)), "signing_address": valLoneKey, "power": 6.0, "share": 1.0}
	if _, body := getValidator(t, srv, "?val="+valLoneKey); !reflect.DeepEqual(body["status"], want) {
		t.Errorf("genesis: status = %v, want %v", body["status"], want)
	}
}

// TestValidatorStatusUnknownPower checks the status while a member of the
// node's set has a voting power that reads as no number: the set's total is
// unknown, so share is null for every validator, and that member's own
// power is null too.
func TestValidatorStatusUnknownPower(t *testing.T) {
	s := newBlocksStore(t)
	putBlocks(t, s, secondsFrom(valDay()), "S", []blocks.Member{{Address: valLoneKey, Power: 6}, {Address: valNewKey, Power: 4}}, 1, 10, nil)
	srv := &Server{Blocks: s, BlockFill: fakeFill{ChainID: "onyx-1", From: 1, To: 10, Complete: true}}
	srv.setSnapshot(&node.Snapshot{Validators: []node.Validator{{Address: valLoneKey, VotingPower: "6"}, {Address: valNewKey, VotingPower: "lots"}}})
	for _, tc := range []struct {
		key   string
		power any
	}{
		{valLoneKey, 6.0},
		{valNewKey, nil},
	} {
		_, body := getValidator(t, srv, "?val="+tc.key)
		st, ok := body["status"].(map[string]any)
		if !ok {
			t.Fatalf("%s: status = %v, want one", tc.key, body["status"])
		}
		if power, ok := st["power"]; !ok || power != tc.power {
			t.Errorf("%s: power = %v (present %v), want %v", tc.key, power, ok, tc.power)
		}
		if share, ok := st["share"]; !ok || share != nil {
			t.Errorf("%s: share = %v (present %v), want null", tc.key, share, ok)
		}
	}
}

func TestValidatorTimeline(t *testing.T) {
	base := valDay()
	// Heights 5 s apart; blocks 26 and 36 come 10 min late: restarts after
	// 25 and 35, the second one with the key still down at the head.
	at := func(h int64) time.Time {
		ts := base.Add(time.Duration(h) * 5 * time.Second)
		if h >= 26 {
			ts = ts.Add(10 * time.Minute)
		}
		if h >= 36 {
			ts = ts.Add(10 * time.Minute)
		}
		return ts
	}
	absent := map[int64][]string{}
	for h := int64(5); h <= 16; h++ {
		absent[h] = []string{valOldKey}
	}
	for _, h := range []int64{25, 26, 27} {
		absent[h] = []string{valOldKey}
	}
	lone := blocks.Member{Address: valLoneKey, Power: 1}
	s := newBlocksStore(t)
	putBlocks(t, s, at, "T1", []blocks.Member{lone, {Address: valOldKey, Power: 1}}, 1, 30, absent)
	down := map[int64][]string{}
	for h := int64(35); h <= 40; h++ {
		down[h] = []string{valOldKey}
	}
	putBlocks(t, s, at, "T2", []blocks.Member{lone, {Address: valOldKey, Power: 2}}, 31, 40, down)
	srv := &Server{Blocks: s, BlockFill: fakeFill{ChainID: "onyx-1", From: 1, To: 40, Complete: true}}

	_, body := getValidator(t, srv, "?val="+valOldKey)
	recovery := float64(at(29).Sub(at(26)).Milliseconds())
	wantTimeline := []any{
		map[string]any{"kind": "recovering", "height": 35.0, "time": timeOf(at(36)), "restart_height": 35.0},
		map[string]any{"kind": "power", "height": 31.0, "time": timeOf(at(31)), "before": 1.0, "after": 2.0},
		map[string]any{"kind": "recovered", "height": 28.0, "time": timeOf(at(29)), "restart_height": 25.0, "duration_ms": recovery},
		map[string]any{"kind": "outage", "height": 5.0, "time": timeOf(at(5)), "to": 16.0, "blocks": 12.0, "end_time": timeOf(at(17)), "ongoing": false},
		map[string]any{"kind": "genesis", "height": 1.0, "time": timeOf(at(1)), "power": 1.0},
	}
	if !reflect.DeepEqual(body["timeline"], wantTimeline) {
		t.Errorf("timeline = %v, want %v", body["timeline"], wantTimeline)
	}
	wantRecoveries := []any{
		map[string]any{"restart_height": 25.0, "restart_time": timeOf(at(26)), "signed_at": 28.0, "duration_ms": recovery, "pending": false, "scored": true},
		map[string]any{"restart_height": 35.0, "restart_time": timeOf(at(36)), "signed_at": 0.0, "duration_ms": 0.0, "pending": true, "scored": false},
	}
	if !reflect.DeepEqual(body["recoveries"], wantRecoveries) {
		t.Errorf("recoveries = %v, want %v", body["recoveries"], wantRecoveries)
	}
	today := valNow.Truncate(24 * time.Hour)
	wantSummary := map[string]any{
		"first_day": today.AddDate(0, 0, -30).Format(time.DateOnly), "last_day": today.AddDate(0, 0, -1).Format(time.DateOnly),
		"in_set": 40.0, "signed": 19.0, "absent": 21.0, "nil": 0.0, "other_block": 0.0, "outages": 1.0,
		"proposals_expected": 0.0, "proposals_missed": 0.0, "votes": 19.0, "vote_delay_mean_ms": 0.0,
		"recovering": 9.0, "longest_absence": 12.0, "in_set_ms": 1395000.0,
		"recovery": map[string]any{"samples": 1.0, "median_ms": recovery, "slowest_ms": recovery},
	}
	if !reflect.DeepEqual(body["summary"], wantSummary) {
		t.Errorf("summary = %v, want %v", body["summary"], wantSummary)
	}
}

// TestValidatorSummaryLeavesRecoveryOutOfUptime checks the summary's scoring
// figures on a key that is down after block 15 (block 16 comes 10 min late)
// and absent from 15 to 17, then restarts after block 25 (block 26 comes
// 10 min late) having signed it, with an absence of 5 to 7 apart from both.
// The recovery run, 15 to 17, is counted in recovering and left out of
// longest_absence; the proposals, all the key's, skip the absent heights and
// the restart height 25 it signed.
func TestValidatorSummaryLeavesRecoveryOutOfUptime(t *testing.T) {
	base := valDay()
	at := func(h int64) time.Time {
		ts := base.Add(time.Duration(h) * 5 * time.Second)
		if h >= 16 {
			ts = ts.Add(10 * time.Minute)
		}
		if h >= 26 {
			ts = ts.Add(10 * time.Minute)
		}
		return ts
	}
	absent := map[int64][]string{}
	for _, h := range []int64{5, 6, 7, 15, 16, 17} {
		absent[h] = []string{valOldKey}
	}
	s := newBlocksStore(t)
	putBlocks(t, s, at, "T1", []blocks.Member{{Address: valOldKey, Power: 1}, {Address: valLoneKey, Power: 1}}, 1, 30, absent)
	srv := &Server{Blocks: s, BlockFill: fakeFill{ChainID: "onyx-1", From: 1, To: 30, Complete: true}}

	_, body := getValidator(t, srv, "?val="+valOldKey)
	summary, ok := body["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary = %v, want an object", body["summary"])
	}
	want := map[string]float64{
		"in_set": 30, "signed": 24, "absent": 6,
		"recovering": 3, "longest_absence": 3, "in_set_ms": float64(at(30).Sub(at(1)).Milliseconds()),
		"proposals_expected": 23, "proposals_missed": 0,
	}
	for field, w := range want {
		if got := summary[field]; got != w {
			t.Errorf("summary[%s] = %v, want %v", field, got, w)
		}
	}
}

func TestValidatorLiveOutOfTheSet(t *testing.T) {
	// Out of the current set: the live figures are its newest key's.
	srv := rotationServer(t)
	srv.setSnapshot(&node.Snapshot{Validators: []node.Validator{{Address: valLoneKey, VotingPower: "1"}}})
	srv.missed24h = map[string]int{valOldKey: 2, valNewKey: 5}
	if _, body := getValidator(t, srv, "?val="+valOperator); body["live"].(map[string]any)["missed_24h"] != 5.0 {
		t.Errorf("live = %v, want the newest key's 5 missed", body["live"])
	}
}

func TestValidatorStatusSinceUnknown(t *testing.T) {
	at := secondsFrom(valDay())
	lone := blocks.Member{Address: valLoneKey, Power: 6}
	s := newBlocksStore(t)
	putBlocks(t, s, at, "S0", []blocks.Member{lone}, 1, 49, nil)
	putBlocks(t, s, at, "S1", []blocks.Member{lone, {Address: valOldKey, Power: 4}}, 50, 99, nil)
	putBlocks(t, s, at, "S0", []blocks.Member{lone}, 100, 101, nil)
	putValEvents(t, s, 50, blocks.Event{Type: "ValidatorAdded", Operator: valOperator, Address: valOldKey, Power: 4, Applied: true})
	putValEvents(t, s, 100, blocks.Event{Type: "ValidatorRemoved", Operator: valOperator, Address: valOldKey, Applied: true})
	srv := &Server{Blocks: s, BlockFill: fakeFill{ChainID: "onyx-1", From: 1, To: 101, Complete: true}}
	// In the node's set, though the stored history ends with its leave: its
	// rejoin is not stored yet.
	srv.setSnapshot(&node.Snapshot{Validators: []node.Validator{{Address: valLoneKey, VotingPower: "6"}, {Address: valOldKey, VotingPower: "4"}}})
	want := map[string]any{"in_set": true, "signing_address": valOldKey, "power": 4.0, "share": 0.4}
	if _, body := getValidator(t, srv, "?val="+valOperator); !reflect.DeepEqual(body["status"], want) {
		t.Errorf("in the set after a stored leave: status = %v, want %v", body["status"], want)
	}

	// Out of the node's set, the stored history ending with its join.
	s2 := newBlocksStore(t)
	putBlocks(t, s2, at, "S0", []blocks.Member{lone}, 1, 49, nil)
	putBlocks(t, s2, at, "S1", []blocks.Member{lone, {Address: valOldKey, Power: 4}}, 50, 99, nil)
	putValEvents(t, s2, 50, blocks.Event{Type: "ValidatorAdded", Operator: valOperator, Address: valOldKey, Power: 4, Applied: true})
	srv2 := &Server{Blocks: s2, BlockFill: fakeFill{ChainID: "onyx-1", From: 1, To: 99, Complete: true}}
	srv2.setSnapshot(&node.Snapshot{Validators: []node.Validator{{Address: valLoneKey, VotingPower: "6"}}})
	want = map[string]any{"in_set": false, "power": 0.0, "share": 0.0}
	if _, body := getValidator(t, srv2, "?val="+valOperator); !reflect.DeepEqual(body["status"], want) {
		t.Errorf("out of the set after a stored join: status = %v, want %v", body["status"], want)
	}

	// In the node's set, its join below the stored history: since before
	// the history's first block.
	s3 := newBlocksStore(t)
	putBlocks(t, s3, at, "S1", []blocks.Member{lone, {Address: valOldKey, Power: 4}}, 20, 30, nil)
	srv3 := &Server{Blocks: s3, BlockFill: fakeFill{ChainID: "onyx-1", From: 20, To: 30}}
	srv3.setSnapshot(&node.Snapshot{Validators: []node.Validator{{Address: valLoneKey, VotingPower: "6"}, {Address: valOldKey, VotingPower: "4"}}})
	want = map[string]any{"in_set": true, "since_height": 20.0, "since_time": timeOf(at(20)), "since_before": true, "signing_address": valOldKey, "power": 4.0, "share": 0.4}
	if _, body := getValidator(t, srv3, "?val="+valOldKey); !reflect.DeepEqual(body["status"], want) {
		t.Errorf("joined below the history: status = %v, want %v", body["status"], want)
	}

	// Out of the node's set while the stored history ends with it in the
	// set: its leave is not stored yet, so no since.
	srv3.setSnapshot(&node.Snapshot{Validators: []node.Validator{{Address: valLoneKey, VotingPower: "6"}}})
	want = map[string]any{"in_set": false, "power": 0.0, "share": 0.0}
	if _, body := getValidator(t, srv3, "?val="+valOldKey); !reflect.DeepEqual(body["status"], want) {
		t.Errorf("leave not stored yet: status = %v, want %v", body["status"], want)
	}
}

func TestStatusSince(t *testing.T) {
	join := timelineEntry{Kind: "joined", Height: 50}
	gen := timelineEntry{Kind: "genesis", Height: 1}
	leave := timelineEntry{Kind: "left", Height: 80}
	rot := timelineEntry{Kind: "rotated", Height: 90}
	for _, tc := range []struct {
		name    string
		entries []timelineEntry
		inSet   bool
		want    int64 // the since height; 0 when none
	}{
		{"in: its join", []timelineEntry{join, rot}, true, 50},
		{"in: genesis", []timelineEntry{gen, rot}, true, 1},
		{"in: left last", []timelineEntry{join, leave}, true, 0},
		{"in: none stored", []timelineEntry{rot}, true, 0},
		{"out: its leave", []timelineEntry{join, leave}, false, 80},
		{"out: joined last", []timelineEntry{leave, {Kind: "joined", Height: 85}}, false, 0},
		{"out: none stored", nil, false, 0},
	} {
		got, ok := statusSince(tc.entries, tc.inSet)
		if ok != (tc.want > 0) || (ok && got.Height != tc.want) {
			t.Errorf("%s: since %d, %v; want %d", tc.name, got.Height, ok, tc.want)
		}
	}
}

// locationReportServer is locationServer with a block history: K1 and K2
// sign blocks 1 to 3, and a stored event links operator D, which has no
// valopers profile, to the key K4.
func locationReportServer(t *testing.T) *Server {
	t.Helper()
	srv := locationServer(t)
	srv.Blocks = openBlocksStore(t)
	members := []blocks.Member{{Address: locationAddr("K1"), Power: 10}, {Address: locationAddr("K2"), Power: 10}}
	putBlocks(t, srv.Blocks, secondsFrom(valDay()), "S", members, 1, 3, nil)
	putValEvents(t, srv.Blocks, 3, blocks.Event{Type: "ValidatorAdded", Operator: locationAddr("D"), Address: locationAddr("K4"), Power: 1, Applied: true})
	return srv
}

func TestValidatorDeclaredLocation(t *testing.T) {
	srv := locationReportServer(t)
	opA, opB, opD := locationAddr("A"), locationAddr("B"), locationAddr("D")
	// undeclared checks A's report without a declaration that shows: its
	// unlocated peer's provider, and A may declare.
	undeclared := func(when string) {
		t.Helper()
		_, body := getValidator(t, srv, "?val="+opA)
		if want := map[string]any{"provider": "OVH SAS", "source": "ip"}; !reflect.DeepEqual(body["location"], want) || body["declarable"] != true {
			t.Errorf("A %s: location %v, declarable %v; want %v, true", when, body["location"], body["declarable"], want)
		}
	}
	undeclared("without a declaration")

	signed := signedLocation(t, "A", locationWith("A", "Equinix"), location.Legacy)
	declareOK(t, srv, signed)
	storeDeclaration(t, srv, locationAt("B", locationT))
	for _, c := range []struct {
		name       string
		operator   string
		location   any
		declarable bool
	}{{
		name: "a declared location", operator: opA,
		location: map[string]any{
			"country": "SG", "lat": 1.3, "lon": 103.8, "provider": "Equinix", "source": "declared",
			"declared": map[string]any{"at": "2026-11-18T11:00:00Z", "signed": string(signed)},
		},
		declarable: true,
	}, {
		name: "a validator located by its IP", operator: opB,
		location:   map[string]any{"country": "DE", "lat": 50.1, "lon": 8.7, "provider": "Hetzner Online GmbH", "source": "ip"},
		declarable: false,
	}, {
		name: "an operator without a profile", operator: opD,
		location:   nil,
		declarable: false,
	}} {
		t.Run(c.name, func(t *testing.T) {
			code, body := getValidator(t, srv, "?val="+c.operator)
			if code != http.StatusOK {
				t.Fatalf("status = %d, body %v", code, body)
			}
			if !reflect.DeepEqual(body["location"], c.location) {
				t.Errorf("location = %v, want %v", body["location"], c.location)
			}
			if body["declarable"] != c.declarable {
				t.Errorf("declarable = %v, want %t", body["declarable"], c.declarable)
			}
		})
	}

	// A declaration naming no provider: none shown, its peer's aside.
	r := locationWith("A", "")
	r.At = locationT.Add(time.Second)
	signed = signedLocation(t, "A", r, location.Legacy)
	declareOK(t, srv, signed)
	want := map[string]any{
		"country": "SG", "lat": 1.3, "lon": 103.8, "source": "declared",
		"declared": map[string]any{"at": "2026-11-18T11:00:01Z", "signed": string(signed)},
	}
	if _, body := getValidator(t, srv, "?val="+opA); !reflect.DeepEqual(body["location"], want) {
		t.Errorf("A declared without a provider: location %v, want %v", body["location"], want)
	}

	declareOK(t, srv, signedLocation(t, "A", removalAt("A", locationT.Add(2*time.Second)), location.Legacy))
	undeclared("after its removal")
}

// TestValidatorNotDeclarableUnavailable checks that /api/validator's
// operator may not declare, and its stored declaration does not show, without
// a store or a chain id.
func TestValidatorNotDeclarableUnavailable(t *testing.T) {
	for _, c := range []struct {
		name  string
		unset func(srv *Server)
	}{
		{"no store", func(srv *Server) { srv.Locations = nil }},
		{"no chain id", func(srv *Server) { srv.BlockFill = fakeFill{} }},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := locationReportServer(t)
			declareOK(t, srv, signedLocation(t, "A", locationWith("A", "Equinix"), location.Legacy))
			c.unset(srv)
			code, body := getValidator(t, srv, "?val="+locationAddr("A"))
			if code != http.StatusOK {
				t.Fatalf("status = %d, body %v", code, body)
			}
			if want := map[string]any{"provider": "OVH SAS", "source": "ip"}; !reflect.DeepEqual(body["location"], want) {
				t.Errorf("location = %v, want %v: its peer's", body["location"], want)
			}
			if body["declarable"] != false {
				t.Errorf("declarable = %v, want false", body["declarable"])
			}
		})
	}
}

// TestValidatorLocationWithoutAKey checks that a validator without a signing
// key (its profile names none, no event links one) has no location: a
// located peer that belongs to no validator, such as a sentry, never stands
// for it.
func TestValidatorLocationWithoutAKey(t *testing.T) {
	srv := locationReportServer(t)
	opE := locationAddr("E")
	srv.valopers[opE] = node.Valoper{Operator: opE, Moniker: "echo"}
	snap := *srv.getSnapshot()
	lat, lon := 48.8, 2.3
	snap.Peers = append(slices.Clone(snap.Peers), node.Peer{NodeID: "sentry", Lat: &lat, Lon: &lon, Country: "FR", City: "Paris", Provider: "Scaleway"})
	srv.setSnapshot(&snap)

	code, body := getValidator(t, srv, "?val="+opE)
	if code != http.StatusOK || !reflect.DeepEqual(body["keys"], []any{}) {
		t.Fatalf("got %d, keys %v; want 200 and no key", code, body["keys"])
	}
	if loc, ok := body["location"]; ok {
		t.Errorf("location = %v, want none: E's validator has no key", loc)
	}
}

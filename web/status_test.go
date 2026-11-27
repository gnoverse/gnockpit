package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/location"
	"github.com/gnoverse/gnockpit/node"
)

// statusTestServer is a server whose client reaches no node, with a name
// registry of its own: enough to build the status report from a snapshot.
func statusTestServer(t *testing.T) *Server {
	t.Helper()
	s := &Server{Client: node.NewClient("http://localhost:1", time.Second)}
	s.Client.Names = node.NewNameRegistryWithPersist(filepath.Join(t.TempDir(), "names.json"))
	return s
}

func TestIsSPOF(t *testing.T) {
	tests := []struct {
		name      string
		vp, total int
		want      bool
	}{
		{"exactly 1/3", 10, 30, true}, // losing it drops the rest below quorum
		{"below 1/3", 9, 30, false},
		{"just over 1/3", 34, 100, true},
		{"just under 1/3", 33, 100, false},
		{"no power", 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSPOF(tc.vp, tc.total); got != tc.want {
				t.Errorf("isSPOF(%d, %d) = %v, want %v", tc.vp, tc.total, got, tc.want)
			}
		})
	}
}

func TestBuildStatusReport(t *testing.T) {
	s := statusTestServer(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	snap := &node.Snapshot{
		Status: &node.Status{
			NodeInfo: node.NodeInfo{Network: "test-x"},
			SyncInfo: node.SyncInfo{LatestBlockHeight: "100", LatestBlockTime: now.Format(time.RFC3339Nano)},
		},
		Consensus: &node.ConsensusState{
			Height: "100", Round: "0", Step: "1", Proposer: "g1a",
			Votes: []node.VoteInfo{{Address: "g1a", Name: "val-a"}, {Address: "g1b", Name: "val-b"}},
		},
		Validators: []node.Validator{
			{Address: "g1a", VotingPower: "10"},
			{Address: "g1b", VotingPower: "1"},
		},
		Peers: []node.Peer{
			{
				NodeID: "n1", ValAddress: "g1a", RPCURL: "http://x", Provider: "AWS",
				Country: "US", City: "Ashburn", Lat: float64Ptr(39.0438), Lon: float64Ptr(-77.4874),
			},
			{NodeID: "n2"}, // not geolocated
			{NodeID: "n3", Country: "EC", City: "Quito", Lat: float64Ptr(0), Lon: float64Ptr(-78.5)},
		},
		Signing: &node.SigningStats{WindowSize: 100, ValidatorSigning: map[string]node.ValSigning{
			"g1a": {Signed: 100, Eligible: 100, SignedInARow: 100},
			"g1b": {Signed: 100, Eligible: 100, SignedInARow: 100},
		}},
	}
	rep := s.buildStatusReport(snap, now)

	// Retrocompat: status/chain/height/time must serialize at the top level via
	// the embedded StatusInfo, alongside the other sections.
	body, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"status", "chain", "height", "time", "network", "peers", "validators"} {
		if _, ok := top[k]; !ok {
			t.Errorf("missing top-level key %q", k)
		}
	}

	if rep.Network == nil || rep.Network.Round != "0" || rep.Network.Step != "1" {
		t.Errorf("network = %+v, want round 0 step 1", rep.Network)
	}

	var a, b *StatusValidator
	for i := range rep.Validators {
		switch rep.Validators[i].Address {
		case "g1a":
			a = &rep.Validators[i]
		case "g1b":
			b = &rep.Validators[i]
		}
	}
	if a == nil || b == nil {
		t.Fatalf("validators = %+v, want g1a and g1b", rep.Validators)
	}
	// g1a is matched to a peer → country/provider; holds 10/11 VP → SPOF.
	if a.Country != "US" || a.Provider != "AWS" {
		t.Errorf("g1a country/provider = %q/%q, want US/AWS", a.Country, a.Provider)
	}
	if a.SPOF == nil || !*a.SPOF {
		t.Errorf("g1a spof = %v, want true (10 of 11 total VP)", a.SPOF)
	}
	if a.Missed100 != 0 {
		t.Errorf("g1a missed_100 = %d, want 0", a.Missed100)
	}
	// g1b has no matched peer and only 1/11 VP.
	if b.Country != "" || b.Provider != "" {
		t.Errorf("g1b country/provider = %q/%q, want empty", b.Country, b.Provider)
	}
	if b.SPOF == nil || *b.SPOF {
		t.Errorf("g1b spof = %v, want false (1 of 11 total VP)", b.SPOF)
	}

	// Location (country, city, coordinates, provider): on a geolocated peer and
	// on the validator matched to it; omitted where unknown.
	t.Run("location", func(t *testing.T) {
		var payload struct {
			Peers      []map[string]any `json:"peers"`
			Validators []map[string]any `json:"validators"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		rowWith := func(rows []map[string]any, key, val string) map[string]any {
			t.Helper()
			for _, r := range rows {
				if r[key] == val {
					return r
				}
			}
			t.Fatalf("no row with %s=%q in %v", key, val, rows)
			return nil
		}
		wantLoc := map[string]any{
			"country": "US", "city": "Ashburn", "lat": 39.0438, "lon": -77.4874, "provider": "AWS",
		}
		for _, row := range []map[string]any{
			rowWith(payload.Peers, "node_id", "n1"),
			rowWith(payload.Validators, "address", "g1a"),
		} {
			for k, want := range wantLoc {
				if row[k] != want {
					t.Errorf("%v: %s = %v, want %v", row, k, row[k], want)
				}
			}
		}
		for _, row := range []map[string]any{
			rowWith(payload.Peers, "node_id", "n2"),
			rowWith(payload.Validators, "address", "g1b"),
		} {
			for k := range wantLoc {
				if v, ok := row[k]; ok {
					t.Errorf("%v: %s = %v, want it omitted", row, k, v)
				}
			}
		}
		// A peer on the equator keeps its zero latitude.
		equator := rowWith(payload.Peers, "node_id", "n3")
		for k, want := range map[string]any{"lat": 0.0, "lon": -78.5} {
			if v, ok := equator[k]; !ok || v != want {
				t.Errorf("%v: %s = %v (present %v), want %v", equator, k, v, ok, want)
			}
		}
	})
}

// TestBuildStatusReportUnknownPower checks that while one validator's voting
// power is unknown, unreadable or out of the set the source read, so is the
// total, and so is whether each validator is a single point of failure, though
// one holds all the power that reads: spof is omitted.
func TestBuildStatusReportUnknownPower(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  []node.Validator
	}{
		{"unreadable", []node.Validator{{Address: "g1a", VotingPower: "10"}, {Address: "g1b", VotingPower: "lots"}}},
		{"out of the set", []node.Validator{{Address: "g1a", VotingPower: "10"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := statusTestServer(t)
			now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
			snap := &node.Snapshot{
				Consensus: &node.ConsensusState{
					Height: "100", Round: "0", Step: "1",
					Votes: []node.VoteInfo{{Address: "g1a", Name: "val-a"}, {Address: "g1b", Name: "val-b"}},
				},
				Validators: tc.set,
			}
			rep := s.buildStatusReport(snap, now)
			if len(rep.Validators) != 2 {
				t.Fatalf("validators = %+v, want g1a and g1b", rep.Validators)
			}
			body, err := json.Marshal(rep)
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Validators []map[string]any `json:"validators"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatal(err)
			}
			for _, v := range payload.Validators {
				if spof, ok := v["spof"]; ok {
					t.Errorf("%s: spof = %v, want it omitted while a voting power is unknown", v["address"], spof)
				}
			}
		})
	}
}

func snapAt(blockTime string, catchingUp bool) *node.Snapshot {
	return &node.Snapshot{
		Status: &node.Status{
			NodeInfo: node.NodeInfo{Network: "test-13"},
			SyncInfo: node.SyncInfo{
				LatestBlockHeight: "100",
				LatestBlockTime:   blockTime,
				CatchingUp:        catchingUp,
			},
		},
	}
}

func TestDeriveStatus(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	recent := now.Add(-3 * time.Second).Format(time.RFC3339Nano)
	old := now.Add(-5 * time.Minute).Format(time.RFC3339Nano)

	if got := deriveStatus(nil, 30, now); got.Status != "down" {
		t.Errorf("nil snapshot = %q, want down", got.Status)
	}
	if got := deriveStatus(&node.Snapshot{Error: "boom"}, 30, now); got.Status != "down" {
		t.Errorf("error snapshot = %q, want down", got.Status)
	}
	if got := deriveStatus(snapAt(old, false), 30, now); got.Status != "down" {
		t.Errorf("stuck chain = %q, want down", got.Status)
	}
	if got := deriveStatus(snapAt(recent, true), 30, now); got.Status != "degraded" {
		t.Errorf("catching up = %q, want degraded", got.Status)
	}
	// Reachable node but garbage block time → degraded (don't claim operational).
	if got := deriveStatus(snapAt("not-a-time", false), 30, now); got.Status != "degraded" {
		t.Errorf("unparseable block time = %q, want degraded", got.Status)
	}
	got := deriveStatus(snapAt(recent, false), 30, now)
	if got.Status != "operational" {
		t.Errorf("healthy = %q, want operational", got.Status)
	}
	if got.Chain != "test-13" || got.Height != "100" {
		t.Errorf("healthy meta = %+v", got)
	}
}

func TestStatusBadgeSVG(t *testing.T) {
	svg := statusBadgeSVG("test-13", "operational")
	if !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, "</svg>") {
		t.Error("not an svg document")
	}
	if !strings.Contains(svg, "#3fb950") { // green
		t.Error("operational badge should be green")
	}
	if !strings.Contains(svg, "test-13") || !strings.Contains(svg, "operational") {
		t.Error("badge missing label/status text")
	}
	if !strings.Contains(statusBadgeSVG("x", "down"), "#f85149") { // red
		t.Error("down badge should be red")
	}
}

func TestHandleStatusAndBadge(t *testing.T) {
	srv := &Server{ChainStuckSecs: 30}
	srv.setSnapshot(snapAt(time.Now().Format(time.RFC3339Nano), false))

	w := httptest.NewRecorder()
	srv.handleStatus(w, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d", w.Code)
	}
	if ct := w.Header().Get("Access-Control-Allow-Origin"); ct != "*" {
		t.Errorf("status endpoint should be CORS-open, got %q", ct)
	}
	if !strings.Contains(w.Body.String(), `"operational"`) {
		t.Errorf("status body = %s", w.Body.String())
	}

	w = httptest.NewRecorder()
	srv.handleBadge(w, httptest.NewRequest(http.MethodGet, "/badge.svg", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("badge: got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "image/svg") {
		t.Errorf("badge content-type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), "#3fb950") {
		t.Error("badge should be green for operational")
	}
}

func TestStatusValidatorFields(t *testing.T) {
	s := statusTestServer(t)
	s.proposals24h = map[string][2]int{"g1a": {14, 1}}
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	snap := &node.Snapshot{
		Status: &node.Status{SyncInfo: node.SyncInfo{LatestBlockHeight: "100", LatestBlockTime: now.Format(time.RFC3339Nano)}},
		Consensus: &node.ConsensusState{
			Height: "100", Round: "0", Step: "1",
			Votes: []node.VoteInfo{{Address: "g1a"}, {Address: "g1b"}},
		},
		Validators: []node.Validator{{Address: "g1a", VotingPower: "1"}, {Address: "g1b", VotingPower: "1"}},
		Signing: &node.SigningStats{WindowSize: 100, AvgBlockMs: 3500, ValidatorSigning: map[string]node.ValSigning{
			"g1a": {Eligible: 100, Signed: 98, Missed: 2, Nil: 1, OtherBlock: 1, Votes: 99, VoteDelayMs: 210},
			"g1b": {Eligible: 100},
		}},
	}
	body, err := json.Marshal(s.buildStatusReport(snap, now))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Validators []map[string]any `json:"validators"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	rows := map[string]map[string]any{}
	for _, r := range payload.Validators {
		rows[r["address"].(string)] = r
	}
	a, b := rows["g1a"], rows["g1b"]
	for k, want := range map[string]any{"vote_delay_ms": 210.0, "nil_100": 1.0, "other_block_100": 1.0, "missed_100": 2.0, "missed_proposals_24h": 1.0, "expected_proposals_24h": 14.0} {
		if a[k] != want {
			t.Errorf("g1a %s = %v, want %v", k, a[k], want)
		}
	}
	if v, ok := b["vote_delay_ms"]; !ok || v != nil {
		t.Errorf("g1b vote_delay_ms = %v (present %v), want null: no precommit in the window", v, ok)
	}
	for _, r := range rows {
		if _, ok := r["avg_block_ms"]; ok {
			t.Errorf("%v has avg_block_ms, want it gone", r["address"])
		}
	}
}

func TestStatusPeersCarryConnectionAndPresence(t *testing.T) {
	s := statusTestServer(t)
	now := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	snap := &node.Snapshot{
		Status: &node.Status{NodeInfo: node.NodeInfo{Network: "test-x"}},
		Peers: []node.Peer{
			{
				NodeID: "n1", ConnectedMs: 90000,
				Presence: &node.Presence{Listed: 2, Polls: 3, FirstSeen: "2026-11-17T09:30:00Z"},
			},
			{NodeID: "n2"},
		},
	}

	raw, err := json.Marshal(s.buildStatusReport(snap, now))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Peers []json.RawMessage `json:"peers"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Peers) != 2 {
		t.Fatalf("peers = %d, want 2: %s", len(body.Peers), raw)
	}
	for _, want := range []string{
		`"connected_ms":90000`,
		`"presence":{"listed":2,"polls":3,"first_seen":"2026-11-17T09:30:00Z","from_start":false}`,
	} {
		if !strings.Contains(string(body.Peers[0]), want) {
			t.Errorf("peer with figures = %s, want it to contain %s", body.Peers[0], want)
		}
	}
	for _, key := range []string{`"connected_ms"`, `"presence"`} {
		if strings.Contains(string(body.Peers[1]), key) {
			t.Errorf("peer without figures = %s, want no %s", body.Peers[1], key)
		}
	}
}

// TestStatusUsesDeclaredLocation checks that a validator with a shown
// declaration takes its location, marked declared, in place of its matched
// peer's, its provider the declared one or none, and that a validator
// located by its IP keeps its peer's, its operator's stored declaration
// aside.
func TestStatusUsesDeclaredLocation(t *testing.T) {
	k1, k2 := locationAddr("K1"), locationAddr("K2")
	k2Location := map[string]any{"country": "DE", "lat": 50.1, "lon": 8.7, "provider": "Hetzner Online GmbH"}
	for _, c := range []struct {
		name     string
		provider string         // the provider A declares
		want     map[string]any // K1's location fields, location_declared included
	}{
		{"a provider", "Equinix", map[string]any{"country": "SG", "lat": 1.3, "lon": 103.8, "provider": "Equinix", "location_declared": true}},
		{"no provider", "", map[string]any{"country": "SG", "lat": 1.3, "lon": 103.8, "location_declared": true}},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := locationServer(t)
			declareOK(t, srv, signedLocation(t, "A", locationWith("A", c.provider), location.Legacy))
			storeDeclaration(t, srv, locationAt("B", locationT))
			snap := *srv.getSnapshot()
			snap.Consensus = &node.ConsensusState{Votes: []node.VoteInfo{{Address: k1}, {Address: k2}}}

			body, err := json.Marshal(srv.buildStatusReport(&snap, seamNow))
			if err != nil {
				t.Fatal(err)
			}
			var payload struct {
				Validators []map[string]any `json:"validators"`
			}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatal(err)
			}
			rows := map[string]map[string]any{}
			for _, r := range payload.Validators {
				rows[r["address"].(string)] = r
			}
			for key, want := range map[string]map[string]any{k1: c.want, k2: k2Location} {
				got := map[string]any{}
				for _, k := range []string{"country", "city", "lat", "lon", "provider", "location_declared"} {
					if v, ok := rows[key][k]; ok {
						got[k] = v
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s's location = %v, want %v", key, got, want)
				}
			}
		})
	}
}

// TestStatusPrefersALocatedPeer checks the location of a validator whose key
// two peers carry, one located by its IP and one not: the located one's,
// whichever comes last, as /api/validator takes it; its operator's stored
// declaration gives way to it.
func TestStatusPrefersALocatedPeer(t *testing.T) {
	k1, k2 := locationAddr("K1"), locationAddr("K2")
	lat, lon := 10.0, 20.0
	located := node.Peer{NodeID: "k1-node", ValAddress: k1, Lat: &lat, Lon: &lon, Country: "FR", Provider: "Scaleway"}
	unlocated := node.Peer{NodeID: "k1-namesake", ValAddress: k1, Provider: "OVH SAS"}
	for _, c := range []struct {
		name  string
		peers []node.Peer
	}{
		{"the located peer first", []node.Peer{located, unlocated}},
		{"the located peer last", []node.Peer{unlocated, located}},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := locationServer(t)
			storeDeclaration(t, srv, locationAt("A", locationT))
			snap := *srv.getSnapshot()
			snap.Peers = append(c.peers, snap.Peers[1]) // then K2's peer
			snap.Consensus = &node.ConsensusState{Votes: []node.VoteInfo{{Address: k1}, {Address: k2}}}

			var got *StatusValidator
			rep := srv.buildStatusReport(&snap, seamNow)
			for i := range rep.Validators {
				if rep.Validators[i].Address == k1 {
					got = &rep.Validators[i]
				}
			}
			if got == nil {
				t.Fatalf("/api/status lists no K1: %+v", rep.Validators)
			}
			if got.Country != "FR" || got.Lat == nil || *got.Lat != lat || got.Lon == nil || *got.Lon != lon ||
				got.Provider != "Scaleway" || got.LocationDeclared {
				t.Errorf("/api/status K1's location = %+v, declared %t; want FR (10, 20) at Scaleway, not declared",
					got.nodeLocation, got.LocationDeclared)
			}
		})
	}
}

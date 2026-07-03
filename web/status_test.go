package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/node"
)

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
	s := &Server{Client: node.NewClient("http://localhost:1", time.Second)}
	s.Client.Names = node.NewNameRegistryWithPersist(filepath.Join(t.TempDir(), "names.json"))
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
		Peers:   []node.Peer{{ValAddress: "g1a", Country: "US", Provider: "AWS", RPCURL: "http://x"}},
		Signing: &node.SigningStats{WindowSize: 100, ValidatorSigns: map[string]int{"g1a": 100, "g1b": 100}},
	}
	rep := s.buildStatusReport(snap, now)

	// Retrocompat: status/chain/height/time must serialize at the top level via
	// the embedded StatusInfo, alongside the new sections.
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
	if !a.SPOF {
		t.Error("g1a should be SPOF (10 of 11 total VP)")
	}
	if a.SignRate != 100 {
		t.Errorf("g1a sign_rate = %d, want 100", a.SignRate)
	}
	// g1b has no matched peer and only 1/11 VP.
	if b.Country != "" || b.Provider != "" {
		t.Errorf("g1b country/provider = %q/%q, want empty", b.Country, b.Provider)
	}
	if b.SPOF {
		t.Error("g1b should not be SPOF (1 of 11 total VP)")
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

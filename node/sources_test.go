package node

import "testing"

func TestMergePeers(t *testing.T) {
	src1 := []Peer{
		{NodeID: "A", Moniker: "alpha", RemoteIP: "203.0.113.1"},                              // public remote
		{NodeID: "B", Moniker: "beta", RemoteIP: "10.0.0.2", ExternalAddress: "198.51.100.2"}, // private remote, public external
		{NodeID: "C", Moniker: "gamma", RemoteIP: "10.0.0.3", ExternalAddress: "192.168.0.3"}, // private only in src1
	}
	src2 := []Peer{
		{NodeID: "A", Moniker: "alpha2", RemoteIP: "203.0.113.99"}, // dup of A: first source wins
		{NodeID: "C", Moniker: "gamma", RemoteIP: "203.0.113.3"},   // C public here
		{NodeID: "D", Moniker: "delta", RemoteIP: "203.0.113.4"},   // only in src2
	}
	merged := MergePeers([][]Peer{src1, src2}, map[string]bool{"A": true})

	if len(merged) != 4 {
		t.Fatalf("union size = %d, want 4 (A,B,C,D)", len(merged))
	}
	byID := map[string]Peer{}
	for _, p := range merged {
		byID[p.NodeID] = p
	}
	if byID["A"].Moniker != "alpha" || byID["A"].RemoteIP != "203.0.113.1" {
		t.Errorf("A = %+v, want moniker alpha / remote 203.0.113.1 (first source)", byID["A"])
	}
	if !byID["A"].Source {
		t.Error("A should be flagged Source (it's a configured endpoint)")
	}
	if byID["B"].RemoteIP != "198.51.100.2" {
		t.Errorf("B RemoteIP = %q, want 198.51.100.2 (private remote → public external)", byID["B"].RemoteIP)
	}
	if byID["C"].RemoteIP != "203.0.113.3" {
		t.Errorf("C RemoteIP = %q, want 203.0.113.3 (public only in src2)", byID["C"].RemoteIP)
	}
	if byID["D"].RemoteIP != "203.0.113.4" || byID["D"].Source {
		t.Errorf("D = %+v, want remote 203.0.113.4 / Source false", byID["D"])
	}
	// ExternalAddress is sanitized to public-only, mirroring RemoteIP: B's public
	// advertised host is kept; C's only advertised host is private, so it's dropped.
	if byID["B"].ExternalAddress != "198.51.100.2" {
		t.Errorf("B ExternalAddress = %q, want 198.51.100.2 (public advertised host kept)", byID["B"].ExternalAddress)
	}
	if byID["C"].ExternalAddress != "" {
		t.Errorf("C ExternalAddress = %q, want empty (private advertised host dropped)", byID["C"].ExternalAddress)
	}
}

func TestBestStatusIndex(t *testing.T) {
	st := func(height string, catchingUp bool) *Status {
		s := &Status{}
		s.SyncInfo.LatestBlockHeight = height
		s.SyncInfo.CatchingUp = catchingUp
		return s
	}
	// synced beats catching-up even at lower height
	if got := bestStatusIndex([]*Status{st("100", true), st("90", false)}); got != 1 {
		t.Errorf("synced-preference: got %d, want 1", got)
	}
	// among synced, highest height wins
	if got := bestStatusIndex([]*Status{st("100", false), st("120", false), st("110", false)}); got != 1 {
		t.Errorf("height-tiebreak: got %d, want 1", got)
	}
	// nil entries skipped
	if got := bestStatusIndex([]*Status{nil, st("50", false), nil}); got != 1 {
		t.Errorf("nil-skip: got %d, want 1", got)
	}
	// all nil -> -1
	if got := bestStatusIndex([]*Status{nil, nil}); got != -1 {
		t.Errorf("all-nil: got %d, want -1", got)
	}
}

func TestFillSourceEgressIP(t *testing.T) {
	s := &Sources{egressIP: "203.0.113.50"}
	peers := []Peer{
		{NodeID: "src-empty", Source: true, RemoteIP: ""},           // filled from egress
		{NodeID: "src-has", Source: true, RemoteIP: "198.51.100.7"}, // kept (observed IP wins)
		{NodeID: "peer", Source: false, RemoteIP: ""},               // untouched (not a source)
	}
	s.FillSourceEgressIP(peers)
	if peers[0].RemoteIP != "203.0.113.50" {
		t.Errorf("src-empty RemoteIP = %q, want 203.0.113.50 (egress fill)", peers[0].RemoteIP)
	}
	if peers[1].RemoteIP != "198.51.100.7" {
		t.Errorf("src-has RemoteIP = %q, want 198.51.100.7 (observed IP kept)", peers[1].RemoteIP)
	}
	if peers[2].RemoteIP != "" {
		t.Errorf("peer RemoteIP = %q, want empty (non-source untouched)", peers[2].RemoteIP)
	}
}

func TestFillSourceEgressIPNoEgress(t *testing.T) {
	s := &Sources{} // egressIP unknown
	peers := []Peer{{NodeID: "src", Source: true, RemoteIP: ""}}
	s.FillSourceEgressIP(peers)
	if peers[0].RemoteIP != "" {
		t.Errorf("RemoteIP = %q, want empty (no egress IP to fill with)", peers[0].RemoteIP)
	}
}

func TestMergePeersNoPublicIP(t *testing.T) {
	src := []Peer{{NodeID: "X", RemoteIP: "10.0.0.1", ExternalAddress: "192.168.1.1"}}
	merged := MergePeers([][]Peer{src}, nil)
	if len(merged) != 1 || merged[0].RemoteIP != "" {
		t.Errorf("merged = %+v, want one peer with empty RemoteIP (no public IP)", merged)
	}
}

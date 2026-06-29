package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGetStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","result":{
			"node_info":{"moniker":"test-node","network":"testchain","id":"abc123"},
			"sync_info":{"latest_block_height":"42","latest_block_time":"2025-01-01T00:00:00Z","catching_up":false},
			"validator_info":{"address":"g1test","pub_key":{"type":"ed25519","value":"AAAA"}}
		}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)
	status, err := c.GetStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.NodeInfo.Moniker != "test-node" {
		t.Errorf("moniker = %q, want test-node", status.NodeInfo.Moniker)
	}
	if status.SyncInfo.LatestBlockHeight != "42" {
		t.Errorf("height = %q, want 42", status.SyncInfo.LatestBlockHeight)
	}
	if status.SyncInfo.CatchingUp {
		t.Error("catching_up should be false")
	}
	if status.ValidatorInfo.Address != "g1test" {
		t.Errorf("address = %q, want g1test", status.ValidatorInfo.Address)
	}
}

func TestGetNetInfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","result":{
			"n_peers":"2",
			"peers":[
				{"node_info":{"moniker":"peer-a","id":"id1"},"remote_ip":"1.2.3.4"},
				{"node_info":{"moniker":"peer-b","id":"id2"},"remote_ip":"5.6.7.8"}
			]
		}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)
	peers, err := c.GetNetInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 2 {
		t.Fatalf("got %d peers, want 2", len(peers))
	}
	if peers[0].Moniker != "peer-a" {
		t.Errorf("peer[0].Moniker = %q", peers[0].Moniker)
	}
	if peers[1].RemoteIP != "5.6.7.8" {
		t.Errorf("peer[1].RemoteIP = %q", peers[1].RemoteIP)
	}
	// When node_info.id is present, it is used as the NodeID.
	if peers[0].NodeID != "id1" {
		t.Errorf("peer[0].NodeID = %q, want id1", peers[0].NodeID)
	}
}

func TestGetNetInfoNodeIDFromNetAddress(t *testing.T) {
	// gno's /net_info leaves node_info.id empty; the node ID is present only
	// embedded in net_address ("nodeID@host:port").
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","result":{
			"n_peers":"1",
			"peers":[
				{"node_info":{"moniker":"peer-a","net_address":"g1abcdef@1.2.3.4:26656"},"remote_ip":"1.2.3.4"}
			]
		}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)
	peers, err := c.GetNetInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 {
		t.Fatalf("got %d peers, want 1", len(peers))
	}
	if peers[0].NodeID != "g1abcdef" {
		t.Errorf("peer[0].NodeID = %q, want g1abcdef (extracted from net_address)", peers[0].NodeID)
	}
	if peers[0].ExternalAddress != "1.2.3.4" {
		t.Errorf("peer[0].ExternalAddress = %q, want 1.2.3.4", peers[0].ExternalAddress)
	}
}

func TestGetValidators(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","result":{
			"validators":[
				{"address":"g1uhv7wr7nku89se3t7v8fpquc7n5sf8rfkywxpc","pub_key":{"type":"ed25519","value":"AA"},"voting_power":"1"},
				{"address":"g1vta7dwp4guuhkfzksenfcheky4xf9hue8mgne4","pub_key":{"type":"ed25519","value":"BB"},"voting_power":"1"}
			]
		}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)
	// Pre-register names to simulate discovery
	c.Names.SetOurs("g1uhv7wr7nku89se3t7v8fpquc7n5sf8rfkywxpc", "moul-val-01")
	c.Names.Register("g1vta7dwp4guuhkfzksenfcheky4xf9hue8mgne4", "gnocore-val-01")
	vals, err := c.GetValidators(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 2 {
		t.Fatalf("got %d validators, want 2", len(vals))
	}
	if vals[0].Name != "moul-val-01 (us)" {
		t.Errorf("vals[0].Name = %q, want moul-val-01 (us)", vals[0].Name)
	}
	if vals[1].Name != "gnocore-val-01" {
		t.Errorf("vals[1].Name = %q, want gnocore-val-01", vals[1].Name)
	}
}

func TestGetConsensusState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"result": map[string]interface{}{
				"round_state": map[string]interface{}{
					"height": "100",
					"round":  "0",
					"step":   4, // int, not string
					"validators": map[string]interface{}{
						"proposer": map[string]interface{}{
							"address": "g1uhv7wr7nku89se3t7v8fpquc7n5sf8rfkywxpc",
						},
						"validators": []map[string]interface{}{
							{"address": "g1uhv7wr7nku89se3t7v8fpquc7n5sf8rfkywxpc"},
							{"address": "g1vta7dwp4guuhkfzksenfcheky4xf9hue8mgne4"},
						},
					},
					"votes": map[string]interface{}{
						"0": map[string]interface{}{
							"prevotes":   []string{"Vote{0:AABB 100/00/1}", "nil-Vote"},
							"precommits": []string{"Vote{0:AABB 100/00/2}", "Vote{1:CCDD 100/00/2}"},
						},
					},
				},
				"peers": []interface{}{},
			},
		}
		data, _ := json.Marshal(resp)
		w.Write(data)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)
	cs, _, err := c.GetConsensusState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if cs.Height != "100" {
		t.Errorf("height = %q, want 100", cs.Height)
	}
	if cs.Round != "0" {
		t.Errorf("round = %q, want 0", cs.Round)
	}
	if cs.Step != "4" {
		t.Errorf("step = %q, want 4", cs.Step)
	}
	if cs.Proposer != "g1uhv7wr7nku89se3t7v8fpquc7n5sf8rfkywxpc" {
		t.Errorf("proposer = %q", cs.Proposer)
	}
	if len(cs.Votes) != 2 {
		t.Fatalf("got %d votes, want 2", len(cs.Votes))
	}
	if !cs.Votes[0].Prevoted {
		t.Error("votes[0] should have prevoted")
	}
	if !cs.Votes[0].Precommit {
		t.Error("votes[0] should have precommit")
	}
	if cs.Votes[1].Prevoted {
		t.Error("votes[1] should not have prevoted")
	}
	if !cs.Votes[1].Precommit {
		t.Error("votes[1] should have precommit")
	}
}

func TestGetConsensusStateEmptyVotes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"jsonrpc": "2.0",
			"result": map[string]interface{}{
				"round_state": map[string]interface{}{
					"height": "1",
					"round":  "0",
					"step":   4,
					"validators": map[string]interface{}{
						"proposer": map[string]interface{}{
							"address": "g1vta7dwp4guuhkfzksenfcheky4xf9hue8mgne4",
						},
						"validators": []map[string]interface{}{
							{"address": "g1vta7dwp4guuhkfzksenfcheky4xf9hue8mgne4"},
							{"address": "g1uhv7wr7nku89se3t7v8fpquc7n5sf8rfkywxpc"},
						},
					},
					"votes": map[string]interface{}{},
				},
				"peers": []interface{}{},
			},
		}
		data, _ := json.Marshal(resp)
		w.Write(data)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)
	cs, _, err := c.GetConsensusState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cs.Votes) != 2 {
		t.Fatalf("got %d votes, want 2", len(cs.Votes))
	}
	for i, v := range cs.Votes {
		if v.Prevoted {
			t.Errorf("votes[%d] should not have prevoted", i)
		}
		if v.Precommit {
			t.Errorf("votes[%d] should not have precommit", i)
		}
	}
}

func TestGetBlockAppHash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","result":{
			"block":{"header":{"app_hash":"deadbeef1234"}}
		}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)
	hash, err := c.GetBlockAppHash(context.Background(), 2)
	if err != nil {
		t.Fatal(err)
	}
	if hash != "deadbeef1234" {
		t.Errorf("app_hash = %q, want deadbeef1234", hash)
	}
}

func TestGetCommit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","result":{"signed_header":{
			"header":{"height":"100","time":"2026-01-01T00:00:00Z","proposer_address":"g1prop","app_hash":"DEADBEEF","num_txs":"6"},
			"commit":{"precommits":[{"validator_address":"g1a"},null,{"validator_address":"g1c"}]}
		},"canonical":true}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)
	ci, err := c.GetCommit(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if ci.Height != "100" || ci.ProposerAddress != "g1prop" || ci.AppHash != "DEADBEEF" {
		t.Errorf("header parse: %+v", ci)
	}
	if ci.NumTxs != 6 {
		t.Errorf("num_txs = %d, want 6", ci.NumTxs)
	}
	if ci.Time != "2026-01-01T00:00:00Z" {
		t.Errorf("time = %q", ci.Time)
	}
	if len(ci.Precommits) != 3 {
		t.Fatalf("precommits = %d, want 3", len(ci.Precommits))
	}
	// The middle slot is null — an absent signer.
	if string(ci.Precommits[1]) != "null" {
		t.Errorf("precommits[1] = %s, want null", ci.Precommits[1])
	}
}

func TestGetSigningStatsMissingValidators(t *testing.T) {
	const (
		addr1 = "g1uhv7wr7nku89se3t7v8fpquc7n5sf8rfkywxpc"  // known name
		addr2 = "g1vta7dwp4guuhkfzksenfcheky4xf9hue8mgne4"  // unknown
		addr3 = "g1manfred0000000000000000000000000000000a" // known name, will sign
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/validators":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"validators":[
				{"address":%q,"pub_key":{"type":"ed25519","value":"AA"},"voting_power":"1"},
				{"address":%q,"pub_key":{"type":"ed25519","value":"BB"},"voting_power":"1"},
				{"address":%q,"pub_key":{"type":"ed25519","value":"CC"},"voting_power":"1"}
			]}}`, addr1, addr2, addr3)
		default: // /commit?height=2
			fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"signed_header":{
				"header":{"height":"2","time":"2026-03-20T14:32:01.123456789Z","proposer_address":%q},
				"commit":{"precommits":[
					{"validator_address":%q},
					null,
					null
				]}
			}}}`, addr3, addr3)
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)
	c.Names.Register(addr1, "validator-one")
	c.Names.Register(addr3, "validator-three")

	stats, err := c.GetSigningStats(context.Background(), 2, 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.RecentBlocks) != 1 {
		t.Fatalf("got %d blocks, want 1", len(stats.RecentBlocks))
	}
	b := stats.RecentBlocks[0]
	if b.Signers != 1 {
		t.Errorf("signers = %d, want 1", b.Signers)
	}
	if len(b.Missing) != 2 {
		t.Fatalf("len(missing) = %d, want 2", len(b.Missing))
	}

	// Build a map for order-independent assertions
	byAddr := map[string]MissingValidator{}
	for _, m := range b.Missing {
		byAddr[m.Address] = m
	}

	m1, ok := byAddr[addr1]
	if !ok {
		t.Fatalf("addr1 not in missing")
	}
	if m1.Name != "validator-one" {
		t.Errorf("m1.Name = %q, want validator-one", m1.Name)
	}
	if m1.Address != addr1 {
		t.Errorf("m1.Address = %q, want %s", m1.Address, addr1)
	}

	m2, ok := byAddr[addr2]
	if !ok {
		t.Fatalf("addr2 not in missing")
	}
	if m2.Name != "" {
		t.Errorf("m2.Name = %q, want empty (unknown validator)", m2.Name)
	}
	if m2.Address != addr2 {
		t.Errorf("m2.Address = %q, want %s", m2.Address, addr2)
	}
}

func TestActiveCountWithMissedBlocksThreshold(t *testing.T) {
	const (
		addrA = "g1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		addrB = "g1bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/validators":
			fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"validators":[
				{"address":%q,"pub_key":{"type":"ed25519","value":"AA"},"voting_power":"1"},
				{"address":%q,"pub_key":{"type":"ed25519","value":"BB"},"voting_power":"1"}
			]}}`, addrA, addrB)
		default:
			h := r.URL.Query().Get("height")
			if h == "2" {
				// Both validators sign
				fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"signed_header":{
					"header":{"height":"2","time":"2026-01-01T00:00:00Z","proposer_address":%q},
					"commit":{"precommits":[
						{"validator_address":%q},
						{"validator_address":%q}
					]}
				}}}`, addrA, addrA, addrB)
			} else {
				// Only A signs
				fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"signed_header":{
					"header":{"height":"3","time":"2026-01-01T00:00:01Z","proposer_address":%q},
					"commit":{"precommits":[
						{"validator_address":%q},
						null
					]}
				}}}`, addrA, addrA)
			}
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)

	// B signed 1/2 blocks → 50% missed.
	// With threshold 51%: 50 < 51 → B is active → ActiveCount=2
	stats, err := c.GetSigningStats(context.Background(), 3, 2, 51)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ActiveCount != 2 {
		t.Errorf("threshold 51%%: ActiveCount = %d, want 2", stats.ActiveCount)
	}

	// With threshold 50%: 50 >= 50 → B is inactive → ActiveCount=1
	stats, err = c.GetSigningStats(context.Background(), 3, 2, 50)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ActiveCount != 1 {
		t.Errorf("threshold 50%%: ActiveCount = %d, want 1", stats.ActiveCount)
	}

	// With threshold 100%: even 50% missed < 100% → both active
	stats, err = c.GetSigningStats(context.Background(), 3, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ActiveCount != 2 {
		t.Errorf("threshold 100%%: ActiveCount = %d, want 2", stats.ActiveCount)
	}
}

func TestVerboseLogging(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","result":{"node_info":{},"sync_info":{},"validator_info":{}}}`)
	}))
	defer srv.Close()

	var logged bool
	c := NewClient(srv.URL, 5*time.Second)
	c.LogFn = func(method, url string, status int, dur time.Duration, err error) {
		logged = true
		if method != "GET" {
			t.Errorf("method = %q, want GET", method)
		}
		if status != 200 {
			t.Errorf("status = %d, want 200", status)
		}
	}

	c.GetStatus(context.Background())
	if !logged {
		t.Error("LogFn was not called")
	}
}

func TestGetNPeers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","result":{
			"n_peers":"5",
			"peers":[
				{"node_info":{"moniker":"a","id":"1"},"remote_ip":"1.1.1.1"},
				{"node_info":{"moniker":"b","id":"2"},"remote_ip":"2.2.2.2"},
				{"node_info":{"moniker":"c","id":"3"},"remote_ip":"3.3.3.3"},
				{"node_info":{"moniker":"d","id":"4"},"remote_ip":"4.4.4.4"},
				{"node_info":{"moniker":"e","id":"5"},"remote_ip":"5.5.5.5"}
			]
		}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)
	n, err := c.GetNPeers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 {
		t.Errorf("got %d peers, want 5", n)
	}
}

func TestSeedNamesFromGenesis(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/genesis" {
			http.NotFound(w, r)
			return
		}
		// genesis_time and validators precede app_state. The app_state here is
		// deliberately invalid JSON standing in for the real multi-hundred-MB
		// blob: the fetch must stream the head, take what it needs, and abort
		// before ever reading this far.
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":-1,"result":{"genesis":{`+
			`"genesis_time":"2026-03-16T09:00:00Z",`+
			`"chain_id":"test-13",`+
			`"validators":[`+
			`{"address":"g1abc","name":"alice"},`+
			`{"address":"g1def","name":"bob"}`+
			`],`+
			`"app_hash":"",`+
			`"app_state": @@@ deliberately invalid, stands in for hundreds of MB @@@ }}}`)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)
	if err := c.SeedNamesFromGenesis(context.Background()); err != nil {
		t.Fatalf("SeedNamesFromGenesis: %v", err)
	}

	if c.Names.GenesisTime != "2026-03-16T09:00:00Z" {
		t.Errorf("GenesisTime = %q, want 2026-03-16T09:00:00Z", c.Names.GenesisTime)
	}
	if got := c.Names.Name("g1abc"); got != "alice" {
		t.Errorf("Name(g1abc) = %q, want alice", got)
	}
	if got := c.Names.Name("g1def"); got != "bob" {
		t.Errorf("Name(g1def) = %q, want bob", got)
	}
}

func TestRPCCandidates(t *testing.T) {
	got := rpcCandidates("1.2.3.4", "host.example", "26657")
	want := []string{
		"http://1.2.3.4:26657",
		"http://host.example:26657",
		"https://host.example:443",
		"https://1.2.3.4:443",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("distinct host:\n got %v\nwant %v", got, want)
	}

	got = rpcCandidates("5.6.7.8", "", "26657")
	want = []string{"http://5.6.7.8:26657", "https://5.6.7.8:443"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("no external host:\n got %v\nwant %v", got, want)
	}

	got = rpcCandidates("1.2.3.4", "1.2.3.4", "26657")
	want = []string{"http://1.2.3.4:26657", "https://1.2.3.4:443"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("external == observed (deduped):\n got %v\nwant %v", got, want)
	}
}

func TestFetchValoperNames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// abci_query data is base64("gno.land/r/gnops/valopers:<renderPath>").
		raw, _ := base64.StdEncoding.DecodeString(strings.Trim(r.URL.Query().Get("data"), `"`))
		arg := string(raw)
		var md string
		switch {
		case strings.HasSuffix(arg, "valopers:"): // home page 1
			md = "Welcome\n" +
				" * [Alice](/r/gnops/valopers:g1opa) - [profile](/r/demo/profile:u/g1opa)\n" +
				" * [Bob](/r/gnops/valopers:g1opb) - [profile](/r/demo/profile:u/g1opb)\n" +
				"**1** | [2](?page=2)"
		case strings.HasSuffix(arg, "?page=2"): // beyond the end
			md = "no valopers"
		case strings.HasSuffix(arg, ":g1opa"):
			md = "## Alice\n- Operator Address: g1opa\n- Signing Address: g1signa\n"
		case strings.HasSuffix(arg, ":g1opb"):
			md = "## Bob\n- Operator Address: g1opb\n- Signing Address: g1signb\n"
		}
		enc := base64.StdEncoding.EncodeToString([]byte(md))
		fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"response":{"ResponseBase":{"Error":null,"Data":%q}}}}`, enc)
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)
	got, err := c.FetchValoperNames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"g1signa": "Alice", "g1signb": "Bob"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FetchValoperNames = %v, want %v", got, want)
	}
}

func TestMatchDumpPeer(t *testing.T) {
	dpA := &DumpPeer{NodeID: "idA", RemoteIP: "1.1.1.1"}
	dpB := &DumpPeer{NodeID: "idB", RemoteIP: "1.1.1.1"} // shares the IP with A
	byNodeID := map[string]*DumpPeer{"idA": dpA, "idB": dpB}
	byIP := map[string]*DumpPeer{"1.1.1.1": dpB} // IP map keeps only the last writer

	// On a shared IP the node ID must win, so peer A resolves to A, not B.
	if got := MatchDumpPeer("idA", "1.1.1.1", byNodeID, byIP); got != dpA {
		t.Errorf("shared IP: got %v, want dpA (node id must win over IP)", got)
	}
	// Fall back to IP only when the node ID is unknown.
	if got := MatchDumpPeer("idX", "1.1.1.1", byNodeID, byIP); got != dpB {
		t.Errorf("ip fallback: got %v, want dpB", got)
	}
	// No match either way.
	if got := MatchDumpPeer("idX", "9.9.9.9", byNodeID, byIP); got != nil {
		t.Errorf("no match: got %v, want nil", got)
	}
}

func TestPubKeyBech32(t *testing.T) {
	want := "gpub1pggj7ard9eg82cjtv4u52epjx56nzwgjyg9zqqqpqgpsgpgxquyqjzstpsxsurcszyfpx9q4zct3sxg6rvwp68sluq9csv"
	b64 := "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="

	// "type" field (legacy format)
	pk := PubKey{Type: "ed25519", Value: b64}
	if got := pk.Bech32(); got != want {
		t.Errorf("PubKey{Type}.Bech32() = %q, want %q", got, want)
	}

	// "@type" field (gno RPC amino format)
	pk2 := PubKey{AminoType: "/tm.PubKeyEd25519", Value: b64}
	if got := pk2.Bech32(); got != want {
		t.Errorf("PubKey{AminoType}.Bech32() = %q, want %q", got, want)
	}
}

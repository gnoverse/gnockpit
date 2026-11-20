package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
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

// precommitJSON is a precommit as /commit gives it: for block, or for nil when
// block is empty.
func precommitJSON(addr string, index int, block string) string {
	return fmt.Sprintf(`{"type":2,"height":"0","round":"0","block_id":{"hash":%q},"timestamp":"2026-01-01T00:00:00Z","validator_address":%q,"validator_index":"%d"}`,
		block, addr, index)
}

// commitJSONAt is a /commit answer for height, committing block, with the given
// precommits ("null" for an empty slot).
func commitJSONAt(height, proposer, block string, canonical bool, precommits ...string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","result":{"canonical":%t,"signed_header":{
		"header":{"height":%q,"time":"2026-01-01T00:00:%02sZ","proposer_address":%q,"app_hash":"AH%s","num_txs":"1"},
		"commit":{"block_id":{"hash":%q},"precommits":[%s]}
	}}}`, canonical, height, height, proposer, height, block, strings.Join(precommits, ","))
}

func TestCommit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, commitJSONAt("10", "g1prop", "B10", true, precommitJSON("g1a", 0, "B10"), "null", precommitJSON("g1c", 2, "")))
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, 5*time.Second).Commit(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if got.Height != 10 || got.Proposer != "g1prop" || got.AppHash != "AH10" || got.NumTxs != 1 || got.BlockHash != "B10" || !got.Canonical {
		t.Errorf("header parse: %+v", got)
	}
	// The middle slot is empty: an absent signer; the last is a nil vote.
	if len(got.Precommits) != 3 || got.Precommits[1] != nil || got.Precommits[2].BlockHash != "" || got.Precommits[2].Index != 2 {
		t.Errorf("precommits = %+v", got.Precommits)
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
				{"address":%q,"pub_key":{"type":"ed25519","value":"AA"},"voting_power":"1","proposer_priority":"0"},
				{"address":%q,"pub_key":{"type":"ed25519","value":"BB"},"voting_power":"1","proposer_priority":"0"},
				{"address":%q,"pub_key":{"type":"ed25519","value":"CC"},"voting_power":"1","proposer_priority":"0"}
			]}}`, addr1, addr2, addr3)
		default: // /commit?height=2
			fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"signed_header":{
				"header":{"height":"2","time":"2026-03-20T14:32:01.123456789Z","proposer_address":%q},
				"commit":{"precommits":[
					%s,
					null,
					null
				]}
			}}}`, addr3, precommitJSON(addr3, 0, ""))
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)
	c.Names.Register(addr1, "validator-one")
	c.Names.Register(addr3, "validator-three")

	stats, err := c.GetSigningStats(context.Background(), 2, 1)
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

func TestGetSigningStatsPerValidator(t *testing.T) {
	const (
		addrA = "g1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		addrB = "g1bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/validators": // current set and any ?height= query
			fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"validators":[
				{"address":%q,"pub_key":{"type":"ed25519","value":"AA"},"voting_power":"1","proposer_priority":"0"},
				{"address":%q,"pub_key":{"type":"ed25519","value":"BB"},"voting_power":"1","proposer_priority":"0"}
			]}}`, addrA, addrB)
		default:
			if r.URL.Query().Get("height") == "2" {
				// Both validators sign height 2.
				fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"signed_header":{
					"header":{"height":"2","time":"2026-01-01T00:00:00Z","proposer_address":%q},
					"commit":{"precommits":[%s,%s]}
				}}}`, addrA, precommitJSON(addrA, 0, ""), precommitJSON(addrB, 1, ""))
			} else {
				// Only A signs height 3 — B misses the most recent block.
				fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"signed_header":{
					"header":{"height":"3","time":"2026-01-01T00:00:01Z","proposer_address":%q},
					"commit":{"precommits":[%s,null]}
				}}}`, addrA, precommitJSON(addrA, 0, ""))
			}
		}
	}))
	defer srv.Close()

	c := NewClient(srv.URL, 5*time.Second)

	// Window of 2 blocks (heights 2,3). A signs both; B signs 2, misses 3.
	stats, err := c.GetSigningStats(context.Background(), 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	a, b := stats.ValidatorSigning[addrA], stats.ValidatorSigning[addrB]
	if a.Signed != 2 || a.Missed != 0 || a.SignedInARow != 2 {
		t.Errorf("A = %+v, want signed 2 / missed 0 / signed-streak 2", a)
	}
	if b.Signed != 1 || b.Missed != 1 || b.MissedInARow != 1 || b.SignedInARow != 0 {
		t.Errorf("B = %+v, want signed 1 / missed 1 / missed-streak 1", b)
	}
	// Active/inactive is derived server-side from the detector, not here.
	if stats.ActiveCount != 0 {
		t.Errorf("ActiveCount = %d, want 0 (set by the server, not GetSigningStats)", stats.ActiveCount)
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

// onyxValoperRender is gno.land/r/gnops/valopers:g1078xslm886lv9ad4w5zxxfg8nt07gprk5kwcak
// on onyx, read 2026-11-19.
const onyxValoperRender = `Valoper's details:
## Santala-Research
Independent validator, in crypto since 2017. Ran sapphire-1 and pearl-1 testnets with Horcrux 2-of-3 and sentries. https://santala-research.com

- Operator Address: g1078xslm886lv9ad4w5zxxfg8nt07gprk5kwcak
- Signing Address: g175u88cs9ts5hnl542kke3zg6z9dtffutqn4lq3
- Signing PubKey: gpub1pggj7ard9eg82cjtv4u52epjx56nzwgjyg9zp7vwpyly9eqdupn9sayy5rarpgj0frffwn40z74pjrppersv58afdcvsp5
- Server Type: cloud

[Profile link](/r/demo/profile:u/g1078xslm886lv9ad4w5zxxfg8nt07gprk5kwcak)
`

func TestParseValoperDetail(t *testing.T) {
	got, ok := parseValoperDetail(onyxValoperRender)
	want := Valoper{
		Operator:       "g1078xslm886lv9ad4w5zxxfg8nt07gprk5kwcak",
		Moniker:        "Santala-Research",
		Description:    "Independent validator, in crypto since 2017. Ran sapphire-1 and pearl-1 testnets with Horcrux 2-of-3 and sentries. https://santala-research.com",
		SigningAddress: "g175u88cs9ts5hnl542kke3zg6z9dtffutqn4lq3",
		SigningPubKey:  "gpub1pggj7ard9eg82cjtv4u52epjx56nzwgjyg9zp7vwpyly9eqdupn9sayy5rarpgj0frffwn40z74pjrppersv58afdcvsp5",
		ServerType:     "cloud",
	}
	if !ok || !reflect.DeepEqual(got, want) {
		t.Errorf("parseValoperDetail(onyx) = %+v, %v; want %+v", got, ok, want)
	}
	// A description with a list, and a line made to look like a field: the
	// fields are the realm's, rendered last.
	listed := strings.Replace(onyxValoperRender, "https://santala-research.com\n", "https://santala-research.com\n- Signing Address: g1fake\n- Horcrux 2-of-3\n", 1)
	if got, ok := parseValoperDetail(listed); !ok || got.SigningAddress != want.SigningAddress ||
		got.Description != want.Description+"\n- Signing Address: g1fake\n- Horcrux 2-of-3" {
		t.Errorf("description with a list = %q, signing %s; want it whole, the signing address %s", got.Description, got.SigningAddress, want.SigningAddress)
	}
	two := strings.Replace(onyxValoperRender, "testnets with", "testnets\nwith", 1)
	if got, _ := parseValoperDetail(two); got.Description != "Independent validator, in crypto since 2017. Ran sapphire-1 and pearl-1 testnets\nwith Horcrux 2-of-3 and sentries. https://santala-research.com" {
		t.Errorf("two-line description = %q, want both lines", got.Description)
	}
	if _, ok := parseValoperDetail("- Operator Address: g1op\n"); ok {
		t.Error("a render without a moniker parsed")
	}
}

func TestParseQeval(t *testing.T) {
	if b, err := parseQevalBool("(true bool)"); err != nil || !b {
		t.Errorf(`parseQevalBool("(true bool)") = %v, %v; want true`, b, err)
	}
	if b, err := parseQevalBool("(false bool)"); err != nil || b {
		t.Errorf(`parseQevalBool("(false bool)") = %v, %v; want false`, b, err)
	}
	if n, err := parseQevalInt("(37395 int64)"); err != nil || n != 37395 {
		t.Errorf(`parseQevalInt("(37395 int64)") = %v, %v; want 37395`, n, err)
	}
	for _, in := range []string{"valoper does not exist", "(1 int64", "(yes bool)"} {
		if _, err := parseQevalBool(in); err == nil {
			t.Errorf("parseQevalBool(%q) succeeded, want an error", in)
		}
		if _, err := parseQevalInt(in); err == nil {
			t.Errorf("parseQevalInt(%q) succeeded, want an error", in)
		}
	}
}

// valopersRPC answers the valopers realm's home page, two profiles and their
// state; g1opb's state query is refused.
func valopersRPC(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := base64.StdEncoding.DecodeString(strings.Trim(r.URL.Query().Get("data"), `"`))
		arg := string(raw)
		if strings.Contains(r.URL.Query().Get("path"), "qeval") {
			switch {
			case strings.Contains(arg, "g1opb"):
				fmt.Fprint(w, `{"jsonrpc":"2.0","result":{"response":{"ResponseBase":{"Error":{"@type":"/abci.StringError","value":"valoper does not exist"},"Data":null}}}}`)
				return
			case strings.HasSuffix(arg, ".KeepRunning"):
				arg = "(true bool)"
			case strings.HasSuffix(arg, ".LastRotationHeight"):
				arg = "(37395 int64)"
			}
			fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"response":{"ResponseBase":{"Error":null,"Data":%q}}}}`, base64.StdEncoding.EncodeToString([]byte(arg)))
			return
		}
		var md string
		switch {
		case strings.HasSuffix(arg, "valopers:"): // home page 1
			md = "Welcome\n" +
				" * [Alice](/r/gnops/valopers:g1opa) - [profile](/r/demo/profile:u/g1opa)\n" +
				" * [Bob](/r/gnops/valopers:g1opb) - [profile](/r/demo/profile:u/g1opb)\n" +
				" * [Carol](/r/gnops/valopers:g1opc) - [profile](/r/demo/profile:u/g1opc)\n" +
				" * [Dan](/r/gnops/valopers:g1opd) - [profile](/r/demo/profile:u/g1opd)\n" +
				" * [Eve](/r/gnops/valopers:g1ope) - [profile](/r/demo/profile:u/g1ope)\n" +
				"**1** | [2](?page=2)"
		case strings.HasSuffix(arg, "?page=2"): // beyond the end
			md = "no valopers"
		case strings.HasSuffix(arg, ":g1opa"):
			md = "## Alice\nAlice's node.\n\n- Operator Address: g1opa\n- Signing Address: g1signa\n- Server Type: cloud\n"
		case strings.HasSuffix(arg, ":g1opb"):
			md = "## Bob\n- Operator Address: g1opb\n- Signing Address: g1signb\n"
		case strings.HasSuffix(arg, ":g1opc"): // the node fails the render
			fmt.Fprint(w, `{"jsonrpc":"2.0","result":{"response":{"ResponseBase":{"Error":{"@type":"/abci.StringError","value":"render failed"},"Data":null}}}}`)
			return
		case strings.HasSuffix(arg, ":g1opd"): // no moniker
			md = "nothing here"
		case strings.HasSuffix(arg, ":g1ope"): // a render naming another operator
			md = "## Eve\n- Operator Address: g1opa\n- Signing Address: g1signe\n"
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"response":{"ResponseBase":{"Error":null,"Data":%q}}}}`, base64.StdEncoding.EncodeToString([]byte(md)))
	}))
}

func TestFetchValopers(t *testing.T) {
	srv := valopersRPC(t)
	defer srv.Close()
	read, err := NewClient(srv.URL, 5*time.Second).FetchValopers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := read.Profiles
	slices.SortFunc(got, func(a, b Valoper) int { return strings.Compare(a.Operator, b.Operator) })
	if len(got) != 3 {
		t.Fatalf("valopers = %+v, want Alice, Bob and Eve", got)
	}
	alice, bob, eve := got[0], got[1], got[2]
	if eve.Operator != "g1ope" || eve.Moniker != "Eve" {
		t.Errorf("Eve = %+v, want her profile under the operator the registry lists, g1ope", eve)
	}
	if len(read.Unread) != 2 || !strings.Contains(read.Unread["g1opc"], "render failed") || read.Unread["g1opd"] == "" {
		t.Errorf("unread = %v, want g1opc (the node's reason) and g1opd", read.Unread)
	}
	if alice.Moniker != "Alice" || alice.SigningAddress != "g1signa" || alice.Description != "Alice's node." || alice.ServerType != "cloud" {
		t.Errorf("Alice = %+v", alice)
	}
	if alice.State == nil || !alice.State.KeepRunning || alice.State.LastRotationHeight != 37395 {
		t.Errorf("Alice state = %+v, want keep running, last rotation 37395", alice.State)
	}
	if bob.State != nil || !strings.Contains(bob.StateError, "valoper does not exist") {
		t.Errorf("Bob state = %+v, error %q; want none and the node's reason", bob.State, bob.StateError)
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

// ---- RPC errors

func TestRPCErrorsSurface(t *testing.T) {
	// The body gno writes for a JSON-RPC error, whatever the HTTP status.
	rpcErrorBody := func(data string) string {
		d, _ := json.Marshal(data)
		return `{"jsonrpc":"2.0","id":"","error":{"code":-32603,"message":"Internal error","data":` + string(d) + `}}`
	}
	t.Run("rate limited", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "236")
			w.WriteHeader(http.StatusTooManyRequests)
		}))
		defer srv.Close()

		_, err := NewClient(srv.URL, 5*time.Second).GetStatus(context.Background())
		var se *HTTPStatusError
		if !errors.As(err, &se) {
			t.Fatalf("GetStatus() error = %v, want an *HTTPStatusError", err)
		}
		if se.StatusCode != http.StatusTooManyRequests || se.RetryAfter != 236*time.Second {
			t.Errorf("HTTPStatusError = {%d, %v}, want {429, 236s}", se.StatusCode, se.RetryAfter)
		}
	})
	t.Run("error envelope with HTTP 200", func(t *testing.T) {
		const data = "min height 99999999 can't be greater than max height 1628915"
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, rpcErrorBody(data))
		}))
		defer srv.Close()

		_, err := NewClient(srv.URL, 5*time.Second).GetStatus(context.Background())
		var re *RPCError
		if !errors.As(err, &re) {
			t.Fatalf("GetStatus() error = %v, want an *RPCError", err)
		}
		if re.Code != -32603 || re.Data != data {
			t.Errorf("RPCError = {%d, %q}, want {-32603, %q}", re.Code, re.Data, data)
		}
		if !strings.Contains(err.Error(), data) {
			t.Errorf("error text %q does not carry the node's reason %q", err, data)
		}
	})
	t.Run("code-running query refused", func(t *testing.T) {
		// gno with rpc.allow_code_execution = false: HTTP 403 plus a JSON-RPC error.
		const data = `query "vm/qrender" runs code, which this node does not allow over RPC (rpc.allow_code_execution = false)`
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, rpcErrorBody(data))
		}))
		defer srv.Close()

		_, err := NewClient(srv.URL, 5*time.Second).FetchValopers(context.Background())
		if err == nil || !strings.Contains(err.Error(), "rpc.allow_code_execution = false") {
			t.Errorf("FetchValopers() error = %v, want the node's refusal", err)
		}
	})
}

// ---- Block times and halt params

func TestBlockTime(t *testing.T) {
	// Recorded from gnoland-1's /blockchain?minHeight=1600000&maxHeight=1600000.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.RawQuery; got != "minHeight=1600000&maxHeight=1600000" {
			t.Errorf("query = %q, want the single height 1600000", got)
		}
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"","result":{"last_height":"1628985","block_metas":[{
			"block_id":{"hash":"x"},
			"header":{"chain_id":"gnoland-1","height":"1600000","time":"2026-11-16T10:51:11.208138936Z"}}]}}`)
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, 5*time.Second).BlockTime(context.Background(), 1600000)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 16, 10, 51, 11, 208138936, time.UTC); !got.Equal(want) {
		t.Errorf("BlockTime(1600000) = %v, want %v", got, want)
	}
}

func TestBlockTimeNoBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":"","result":{"last_height":"5","block_metas":[]}}`)
	}))
	defer srv.Close()

	if _, err := NewClient(srv.URL, 5*time.Second).BlockTime(context.Background(), 9); err == nil {
		t.Error("BlockTime(9) with no block returned = nil error, want one")
	}
}

func TestHaltParams(t *testing.T) {
	// abci_query answers recorded from gnoland-1: the param value is amino JSON
	// (an int64 as a quoted string), base64-encoded in Data; an unset param has
	// a null Data.
	abci := func(data string) string {
		return `{"jsonrpc":"2.0","id":"","result":{"response":{"ResponseBase":{"Error":null,"Data":` + data +
			`,"Events":null,"Log":"","Info":""},"Key":null,"Value":null,"Proof":null,"Height":"0"}}}`
	}
	tests := []struct {
		name    string
		answers map[string]string // abci path -> Data JSON
		want    Halt
	}{
		{
			name: "set",
			answers: map[string]string{
				"params/node:p:halt_height":      `"IjExODMwMDAi"`, // "1183000"
				"params/node:p:halt_min_version": `"InYxLjcuMCI="`, // "v1.7.0"
			},
			want: Halt{Height: 1183000, MinVersion: "v1.7.0"},
		},
		{
			name: "unset",
			answers: map[string]string{
				"params/node:p:halt_height":      `null`,
				"params/node:p:halt_min_version": `null`,
			},
			want: Halt{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := strings.Trim(r.URL.Query().Get("path"), `"`)
				data, ok := tc.answers[path]
				if !ok {
					t.Errorf("unexpected abci path %q", path)
				}
				fmt.Fprint(w, abci(data))
			}))
			defer srv.Close()

			got, err := NewClient(srv.URL, 5*time.Second).HaltParams(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("HaltParams() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestChainHead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","result":{
			"node_info":{"moniker":"m","network":"gnoland-1","id":"abc"},
			"sync_info":{"latest_block_height":"1627838","latest_block_time":"2026-11-17T14:06:59.418337027Z","catching_up":false}
		}}`)
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL, 5*time.Second).ChainHead(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := ChainHead{ChainID: "gnoland-1", Height: 1627838, Time: time.Date(2026, 11, 17, 14, 6, 59, 418337027, time.UTC)}
	if got.ChainID != want.ChainID || got.Height != want.Height || !got.Time.Equal(want.Time) {
		t.Errorf("ChainHead() = %+v, want %+v", got, want)
	}
}

func TestStatusHeadUnparsable(t *testing.T) {
	for _, si := range []SyncInfo{
		{LatestBlockHeight: "", LatestBlockTime: "2026-11-17T14:06:59Z"},
		{LatestBlockHeight: "12", LatestBlockTime: "not a time"},
	} {
		if _, err := (&Status{SyncInfo: si}).Head(); err == nil {
			t.Errorf("Head() of %+v = nil error, want one", si)
		}
	}
}

func TestGetSigningStatsKeepsFinalCommits(t *testing.T) {
	const addrA = "g1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	var mu sync.Mutex
	head := "4"
	asked := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/validators" {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"validators":[{"address":%q,"pub_key":{"type":"ed25519","value":"AA"},"voting_power":"1","proposer_priority":"0"}]}}`, addrA)
			return
		}
		h := r.URL.Query().Get("height")
		mu.Lock()
		asked[h]++
		final := h != head // the head's commit is not final yet
		mu.Unlock()
		fmt.Fprint(w, commitJSONAt(h, addrA, "B"+h, final, precommitJSON(addrA, 0, "B"+h)))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, 5*time.Second)

	for range 2 {
		if _, err := c.GetSigningStats(context.Background(), 4, 3); err != nil {
			t.Fatal(err)
		}
	}
	if want := map[string]int{"2": 1, "3": 1, "4": 2}; !maps.Equal(asked, want) {
		t.Errorf("commits asked per height = %v, want %v (final ones once, the head each time)", asked, want)
	}

	// The window moving up drops the commits it left behind.
	mu.Lock()
	head = "6"
	mu.Unlock()
	if _, err := c.GetSigningStats(context.Background(), 6, 2); err != nil {
		t.Fatal(err)
	}
	if kept := slices.Sorted(maps.Keys(c.commits)); !slices.Equal(kept, []int64{5}) {
		t.Errorf("kept commits = %v, want [5]", kept)
	}
}

func TestGetSigningStatsCountsAVoteForAnotherBlockAsMissed(t *testing.T) {
	const (
		addrA = "g1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		addrB = "g1bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		addrC = "g1cccccccccccccccccccccccccccccccccccccccc"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/validators" {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"validators":[
				{"address":%q,"pub_key":{"type":"ed25519","value":"AA"},"voting_power":"1","proposer_priority":"0"},
				{"address":%q,"pub_key":{"type":"ed25519","value":"BB"},"voting_power":"1","proposer_priority":"0"},
				{"address":%q,"pub_key":{"type":"ed25519","value":"CC"},"voting_power":"1","proposer_priority":"0"}
			]}}`, addrA, addrB, addrC)
			return
		}
		// A votes for the block, B for another one, C for nil.
		fmt.Fprint(w, commitJSONAt("2", addrA, "B2", true,
			precommitJSON(addrA, 0, "B2"), precommitJSON(addrB, 1, "B1"), precommitJSON(addrC, 2, "")))
	}))
	defer srv.Close()

	stats, err := NewClient(srv.URL, 5*time.Second).GetSigningStats(context.Background(), 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	// The block history's rule: a vote for another block is missed, a nil vote signed.
	b := stats.RecentBlocks[0]
	if b.Signers != 2 || len(b.Missing) != 1 || b.Missing[0].Address != addrB {
		t.Errorf("block = %+v, want 2 signers and %s missing", b, addrB)
	}
}

func TestGetSigningStatsForgetsCommitsOfAnEarlierChain(t *testing.T) {
	const addrA = "g1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	var mu sync.Mutex
	head := "9"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/validators" {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"validators":[{"address":%q,"pub_key":{"type":"ed25519","value":"AA"},"voting_power":"1","proposer_priority":"0"}]}}`, addrA)
			return
		}
		h := r.URL.Query().Get("height")
		mu.Lock()
		final := h != head
		mu.Unlock()
		fmt.Fprint(w, commitJSONAt(h, addrA, "B"+h, final, precommitJSON(addrA, 0, "B"+h)))
	}))
	defer srv.Close()
	c := NewClient(srv.URL, 5*time.Second)
	if _, err := c.GetSigningStats(context.Background(), 9, 3); err != nil {
		t.Fatal(err)
	}
	// The RPC's chain restarts from genesis: the heights above its head are another chain's.
	mu.Lock()
	head = "3"
	mu.Unlock()
	if _, err := c.GetSigningStats(context.Background(), 3, 2); err != nil {
		t.Fatal(err)
	}
	if kept := slices.Sorted(maps.Keys(c.commits)); !slices.Equal(kept, []int64{2}) {
		t.Errorf("kept commits = %v, want [2]", kept)
	}
}

func TestGetSigningStatsVoteDelay(t *testing.T) {
	const (
		addrA = "g1aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		addrB = "g1bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	at := func(ms int) string {
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(ms) * time.Millisecond).Format(time.RFC3339Nano)
	}
	vote := func(addr string, index int, ms int) string {
		return fmt.Sprintf(`{"type":2,"height":"0","round":"0","block_id":{"hash":"B"},"timestamp":%q,"validator_address":%q,"validator_index":"%d"}`, at(ms), addr, index)
	}
	// B holds 20 of 30 power: each commit's weighted median is B's time, so
	// A's offsets are -300, -100 and +300 ms, B's 0.
	commits := map[string][2]int{"2": {100, 400}, "3": {0, 100}, "4": {500, 200}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/validators" {
			fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"validators":[
				{"address":%q,"pub_key":{"type":"ed25519","value":"AA"},"voting_power":"10","proposer_priority":"0"},
				{"address":%q,"pub_key":{"type":"ed25519","value":"BB"},"voting_power":"20","proposer_priority":"0"}
			]}}`, addrA, addrB)
			return
		}
		h := r.URL.Query().Get("height")
		c := commits[h]
		fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"canonical":true,"signed_header":{
			"header":{"height":%q,"time":%q,"proposer_address":%q},
			"commit":{"block_id":{"hash":"B"},"precommits":[%s,%s]}
		}}}`, h, at(0), addrA, vote(addrA, 0, c[0]), vote(addrB, 1, c[1]))
	}))
	defer srv.Close()

	stats, err := NewClient(srv.URL, 5*time.Second).GetSigningStats(context.Background(), 4, 3)
	if err != nil {
		t.Fatal(err)
	}
	if a := stats.ValidatorSigning[addrA]; a.Votes != 3 || a.VoteDelayMs != -100 {
		t.Errorf("A = %+v, want 3 votes, a -100 ms median delay", a)
	}
	if b := stats.ValidatorSigning[addrB]; b.Votes != 3 || b.VoteDelayMs != 0 {
		t.Errorf("B = %+v, want 3 votes, a 0 ms median delay", b)
	}
}

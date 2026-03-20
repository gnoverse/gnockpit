package node

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// LogFunc is called for verbose HTTP logging.
type LogFunc func(method, url string, status int, duration time.Duration, err error)

// Client talks to the gnoland RPC endpoint.
type Client struct {
	RPCURL  string
	HTTP    *http.Client
	Timeout time.Duration
	LogFn   LogFunc
	Names   *NameRegistry
}

// NewClient creates a new RPC client.
func NewClient(rpcURL string, timeout time.Duration) *Client {
	return &Client{
		RPCURL:  rpcURL,
		HTTP:    &http.Client{Timeout: timeout},
		Timeout: timeout,
		Names:   NewNameRegistry(),
	}
}

// rpcGet performs a GET request and returns the raw body.
func (c *Client) rpcGet(ctx context.Context, path string) ([]byte, error) {
	url := c.RPCURL + path
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		if c.LogFn != nil {
			c.LogFn("GET", url, 0, time.Since(start), err)
		}
		return nil, err
	}

	resp, err := c.HTTP.Do(req)
	dur := time.Since(start)
	if err != nil {
		if c.LogFn != nil {
			c.LogFn("GET", url, 0, dur, err)
		}
		return nil, err
	}
	defer resp.Body.Close()

	if c.LogFn != nil {
		c.LogFn("GET", url, resp.StatusCode, dur, nil)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// rpcResult wraps the JSON-RPC result envelope.
type rpcResult struct {
	Result json.RawMessage `json:"result"`
}

// rpcGetJSON fetches a path and unmarshals .result into dest.
func (c *Client) rpcGetJSON(ctx context.Context, path string, dest interface{}) error {
	body, err := c.rpcGet(ctx, path)
	if err != nil {
		return err
	}
	var envelope rpcResult
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("unmarshal envelope: %w (body: %s)", err, truncate(string(body), 200))
	}
	return json.Unmarshal(envelope.Result, dest)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// GetStatus fetches /status.
func (c *Client) GetStatus(ctx context.Context) (*Status, error) {
	var result struct {
		NodeInfo      NodeInfo `json:"node_info"`
		SyncInfo      SyncInfo `json:"sync_info"`
		ValidatorInfo ValInfo  `json:"validator_info"`
	}
	if err := c.rpcGetJSON(ctx, "/status", &result); err != nil {
		return nil, err
	}
	return &Status{
		NodeInfo:      result.NodeInfo,
		SyncInfo:      result.SyncInfo,
		ValidatorInfo: result.ValidatorInfo,
	}, nil
}

// GetNetInfo fetches /net_info and returns the peer list.
func (c *Client) GetNetInfo(ctx context.Context) ([]Peer, error) {
	var result struct {
		NPeers string `json:"n_peers"`
		Peers  []struct {
			NodeInfo struct {
				Moniker    string `json:"moniker"`
				ID         string `json:"id"`
				Version    string `json:"version"`
				Software   string `json:"software"`
				NetAddress string `json:"net_address"`
			} `json:"node_info"`
			RemoteIP string `json:"remote_ip"`
		} `json:"peers"`
	}
	if err := c.rpcGetJSON(ctx, "/net_info", &result); err != nil {
		return nil, err
	}
	peers := make([]Peer, len(result.Peers))
	for i, p := range result.Peers {
		peers[i] = Peer{
			Moniker:  p.NodeInfo.Moniker,
			RemoteIP: p.RemoteIP,
			NodeID:   p.NodeInfo.ID,
			Version:  p.NodeInfo.Version,
		}
	}
	return peers, nil
}

// GetValidators fetches /validators and returns the validator set.
func (c *Client) GetValidators(ctx context.Context) ([]Validator, error) {
	var result struct {
		Validators []struct {
			Address     string `json:"address"`
			PubKey      PubKey `json:"pub_key"`
			VotingPower string `json:"voting_power"`
		} `json:"validators"`
	}
	if err := c.rpcGetJSON(ctx, "/validators", &result); err != nil {
		return nil, err
	}
	vals := make([]Validator, len(result.Validators))
	for i, v := range result.Validators {
		vals[i] = Validator{
			Address:     v.Address,
			PubKey:      v.PubKey,
			VotingPower: v.VotingPower,
			Name:        c.Names.NameWithUs(v.Address),
		}
	}
	return vals, nil
}

// GetBlock fetches /block at a given height.
func (c *Client) GetBlock(ctx context.Context, height int) (json.RawMessage, error) {
	path := fmt.Sprintf("/block?height=%d", height)
	var result json.RawMessage
	if err := c.rpcGetJSON(ctx, path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetBlockAppHash returns the app_hash from a block at given height.
func (c *Client) GetBlockAppHash(ctx context.Context, height int) (string, error) {
	raw, err := c.GetBlock(ctx, height)
	if err != nil {
		return "", err
	}
	var block struct {
		Block struct {
			Header struct {
				AppHash string `json:"app_hash"`
			} `json:"header"`
		} `json:"block"`
	}
	if err := json.Unmarshal(raw, &block); err != nil {
		return "", err
	}
	return block.Block.Header.AppHash, nil
}

// GetSigningStats fetches the last N blocks and computes validator signing stats.
func (c *Client) GetSigningStats(ctx context.Context, currentHeight int, window int) (*SigningStats, error) {
	if currentHeight < 2 || window < 1 {
		return nil, fmt.Errorf("need height >= 2 and window >= 1")
	}
	if window > currentHeight-1 {
		window = currentHeight - 1
	}
	if window > 100 {
		window = 100
	}

	// Get validator set for address->name mapping
	valAddrs := map[string]bool{}
	if vals, err := c.GetValidators(ctx); err == nil {
		for _, v := range vals {
			valAddrs[v.Address] = true
		}
	}

	stats := &SigningStats{
		WindowSize:     window,
		TotalCount:     len(valAddrs),
		ValidatorSigns: make(map[string]int),
	}

	startHeight := currentHeight - window + 1
	for h := startHeight; h <= currentHeight; h++ {
		raw, err := c.GetBlock(ctx, h)
		if err != nil {
			continue
		}
		var block struct {
			BlockMeta struct {
				Header struct {
					Height          string `json:"height"`
					Time            string `json:"time"`
					ProposerAddress string `json:"proposer_address"`
				} `json:"header"`
			} `json:"block_meta"`
			Block struct {
				LastCommit struct {
					Precommits []json.RawMessage `json:"precommits"`
				} `json:"last_commit"`
			} `json:"block"`
		}
		if err := json.Unmarshal(raw, &block); err != nil {
			continue
		}

		// Total = precommit slots in this block (actual valset at that height)
		blockTotal := len(block.Block.LastCommit.Precommits)
		if blockTotal == 0 {
			blockTotal = len(valAddrs) // fallback for genesis block
		}
		proposerAddr := block.BlockMeta.Header.ProposerAddress
		proposerName := proposerAddr
		if c.Names != nil {
			if n := c.Names.Name(proposerAddr); n != proposerAddr {
				proposerName = n
			}
		}
		bi := BlockInfo{
			Height:   block.BlockMeta.Header.Height,
			Time:     block.BlockMeta.Header.Time,
			Total:    blockTotal,
			Proposer: proposerName,
		}

		signed := map[string]bool{}
		for _, pc := range block.Block.LastCommit.Precommits {
			if string(pc) == "null" {
				continue
			}
			var vote struct {
				ValidatorAddress string `json:"validator_address"`
			}
			if json.Unmarshal(pc, &vote) == nil && vote.ValidatorAddress != "" {
				signed[vote.ValidatorAddress] = true
				stats.ValidatorSigns[vote.ValidatorAddress]++
			}
		}
		bi.Signers = len(signed)
		for addr := range valAddrs {
			if !signed[addr] {
				name := addr[:12] + "..."
				if c.Names != nil {
					if n := c.Names.Name(addr); n != "" {
						name = n
					}
				}
				bi.Missing = append(bi.Missing, name)
			}
		}
		stats.RecentBlocks = append(stats.RecentBlocks, bi)
	}

	// Compute block times and per-proposer perf
	stats.ValidatorPerf = make(map[string]*ValidatorPerf)
	var totalBlockMs int64
	var blockCount int
	for i := 1; i < len(stats.RecentBlocks); i++ {
		t1, err1 := time.Parse(time.RFC3339Nano, stats.RecentBlocks[i-1].Time)
		t2, err2 := time.Parse(time.RFC3339Nano, stats.RecentBlocks[i].Time)
		if err1 != nil || err2 != nil {
			continue
		}
		ms := int(t2.Sub(t1).Milliseconds())
		if ms < 0 {
			ms = 0
		}
		stats.RecentBlocks[i].BlockMs = ms
		totalBlockMs += int64(ms)
		blockCount++
		// Track proposer perf — use the proposer address from the block header
		pAddr := ""
		for _, blk := range stats.RecentBlocks[i:i+1] {
			// Find the proposer address (not name)
			for addr := range valAddrs {
				if c.Names != nil {
					n := c.Names.Name(addr)
					if n == blk.Proposer || addr == blk.Proposer {
						pAddr = addr
						break
					}
				}
				if addr == blk.Proposer {
					pAddr = addr
					break
				}
			}
		}
		if pAddr == "" {
			pAddr = stats.RecentBlocks[i].Proposer
		}
		if _, ok := stats.ValidatorPerf[pAddr]; !ok {
			stats.ValidatorPerf[pAddr] = &ValidatorPerf{}
		}
		stats.ValidatorPerf[pAddr].Proposed++
		stats.ValidatorPerf[pAddr].AvgBlockMs += ms
	}
	// Finalize averages
	if blockCount > 0 {
		stats.AvgBlockMs = int(totalBlockMs / int64(blockCount))
	}
	for addr, perf := range stats.ValidatorPerf {
		if perf.Proposed > 0 {
			perf.AvgBlockMs = perf.AvgBlockMs / perf.Proposed
		}
		perf.Signed = stats.ValidatorSigns[addr]
	}

	// Count active (signed ALL blocks in window)
	for range valAddrs {
		stats.ActiveCount = 0
	}
	for addr := range valAddrs {
		if stats.ValidatorSigns[addr] >= window {
			stats.ActiveCount++
		}
	}

	stats.BFTThreshold = (stats.TotalCount*2)/3 + 1
	stats.Margin = stats.ActiveCount - stats.BFTThreshold
	// Can add one if: with total+1 validators, active count still >= new threshold
	newThreshold := ((stats.TotalCount + 1) * 2 / 3) + 1
	stats.CanAddOne = stats.ActiveCount >= newThreshold

	return stats, nil
}

// DumpConsensusState fetches /dump_consensus_state.
func (c *Client) DumpConsensusState(ctx context.Context) (json.RawMessage, error) {
	body, err := c.rpcGet(ctx, "/dump_consensus_state")
	if err != nil {
		return nil, err
	}
	var envelope rpcResult
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	return envelope.Result, nil
}

// GetConsensusState parses the dump_consensus_state into structured data.
// Also returns parsed per-peer consensus data as DumpPeer slice.
func (c *Client) GetConsensusState(ctx context.Context) (*ConsensusState, []DumpPeer, error) {
	raw, err := c.DumpConsensusState(ctx)
	if err != nil {
		return nil, nil, err
	}

	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawMap); err != nil {
		return nil, nil, fmt.Errorf("parse consensus state: %w", err)
	}

	var roundState struct {
		Height     string      `json:"height"`
		Round      string      `json:"round"`
		Step       json.Number `json:"step"`
		StartTime  string      `json:"start_time"`
		Validators struct {
			Proposer struct {
				Address string `json:"address"`
			} `json:"proposer"`
			Validators []struct {
				Address string `json:"address"`
			} `json:"validators"`
		} `json:"validators"`
		Votes json.RawMessage `json:"votes"`
	}

	if rsRaw, ok := rawMap["round_state"]; ok {
		if err := json.Unmarshal(rsRaw, &roundState); err != nil {
			return nil, nil, fmt.Errorf("parse round_state: %w", err)
		}
	}

	cs := &ConsensusState{
		Height:         roundState.Height,
		Round:          roundState.Round,
		Step:           roundState.Step.String(),
		Proposer:       roundState.Validators.Proposer.Address,
		RoundStartTime: roundState.StartTime,
	}

	// Parse consensus config
	if cfgRaw, ok := rawMap["config"]; ok {
		var cfg ConsensusConfig
		if json.Unmarshal(cfgRaw, &cfg) == nil {
			cs.Config = &cfg
		}
	}

	valCount := len(roundState.Validators.Validators)

	// Parse peers from dump_consensus_state
	var rawPeers []struct {
		NodeAddress string `json:"node_address"`
		PeerState   string `json:"peer_state"`
	}
	if peersRaw, ok := rawMap["peers"]; ok {
		json.Unmarshal(peersRaw, &rawPeers)
	}

	// Parse each peer's state
	var dumpPeers []DumpPeer
	for _, rp := range rawPeers {
		dp := DumpPeer{}
		// Parse node_address: "nodeID@ip:port"
		if atIdx := strings.Index(rp.NodeAddress, "@"); atIdx > 0 {
			dp.NodeID = rp.NodeAddress[:atIdx]
			hostPort := rp.NodeAddress[atIdx+1:]
			if colonIdx := strings.LastIndex(hostPort, ":"); colonIdx > 0 {
				dp.RemoteIP = hostPort[:colonIdx]
				dp.P2PPort = hostPort[colonIdx+1:]
			} else {
				dp.RemoteIP = hostPort
			}
		}

		// Decode base64 peer_state
		decoded, err := base64.StdEncoding.DecodeString(rp.PeerState)
		if err != nil {
			dumpPeers = append(dumpPeers, dp)
			continue
		}
		var ps struct {
			RoundState struct {
				Height     string   `json:"height"`
				Round      string   `json:"round"`
				Step       json.Number `json:"step"`
				Proposal   bool     `json:"proposal"`
				Prevotes   bitArray `json:"prevotes"`
				Precommits bitArray `json:"precommits"`
			} `json:"round_state"`
		}
		if err := json.Unmarshal(decoded, &ps); err == nil {
			dp.Height = ps.RoundState.Height
			dp.Round = ps.RoundState.Round
			dp.Step = ps.RoundState.Step.String()
			dp.Proposal = ps.RoundState.Proposal

			// Format prevotes/precommits as "count/total"
			pvBits := ps.RoundState.Prevotes.toBitmask()
			pcBits := ps.RoundState.Precommits.toBitmask()
			bits, _ := strconv.Atoi(ps.RoundState.Prevotes.Bits)
			if bits == 0 {
				bits = valCount
			}
			dp.Prevotes = fmt.Sprintf("%d/%d", popcount(pvBits), bits)
			dp.Precommits = fmt.Sprintf("%d/%d", popcount(pcBits), bits)
		}

		dumpPeers = append(dumpPeers, dp)
	}

	// First try to get votes from the votes field (works when blocks are being produced)
	var votesFromField bool
	var votes map[string]json.RawMessage
	if roundState.Votes != nil {
		json.Unmarshal(roundState.Votes, &votes)
	}

	// Build VoteInfo for each validator
	for i, v := range roundState.Validators.Validators {
		vi := VoteInfo{
			Index:   i,
			Address: v.Address,
			Name:    c.Names.NameWithUs(v.Address),
		}
		if roundVotes, ok := votes[roundState.Round]; ok {
			var rv struct {
				Prevotes   []string `json:"prevotes"`
				Precommits []string `json:"precommits"`
			}
			if err := json.Unmarshal(roundVotes, &rv); err == nil {
				if i < len(rv.Prevotes) {
					vi.Prevoted = isVotePresent(rv.Prevotes[i])
					votesFromField = true
				}
				if i < len(rv.Precommits) {
					vi.Precommit = isVotePresent(rv.Precommits[i])
					votesFromField = true
				}
			}
		}
		cs.Votes = append(cs.Votes, vi)
	}

	// If votes field was empty (common at genesis/stuck consensus),
	// extract vote info from peer bitmasks
	if !votesFromField {
		// Find best bitmask (highest prevote count) across all peers
		var bestPrevote, bestPrecommit uint64
		for _, rp := range rawPeers {
			decoded, err := base64.StdEncoding.DecodeString(rp.PeerState)
			if err != nil {
				continue
			}
			var ps struct {
				RoundState struct {
					Prevotes   bitArray `json:"prevotes"`
					Precommits bitArray `json:"precommits"`
				} `json:"round_state"`
			}
			if err := json.Unmarshal(decoded, &ps); err != nil {
				continue
			}
			pv := ps.RoundState.Prevotes.toBitmask()
			pc := ps.RoundState.Precommits.toBitmask()
			if popcount(pv) > popcount(bestPrevote) {
				bestPrevote = pv
			}
			if popcount(pc) > popcount(bestPrecommit) {
				bestPrecommit = pc
			}
		}

		// Apply bitmask to validators
		for i := range cs.Votes {
			if i < valCount {
				cs.Votes[i].Prevoted = (bestPrevote & (1 << uint(i))) != 0
				cs.Votes[i].Precommit = (bestPrecommit & (1 << uint(i))) != 0
			}
		}
	}

	return cs, dumpPeers, nil
}

type bitArray struct {
	Bits  string   `json:"bits"`
	Elems []string `json:"elems"`
}

func (ba bitArray) toBitmask() uint64 {
	if len(ba.Elems) == 0 {
		return 0
	}
	n, _ := strconv.ParseUint(ba.Elems[0], 10, 64)
	return n
}

func popcount(n uint64) int {
	count := 0
	for n != 0 {
		count += int(n & 1)
		n >>= 1
	}
	return count
}

// isVotePresent checks if a vote string represents an actual vote (not "nil-Vote").
func isVotePresent(vote string) bool {
	return vote != "" && vote != "nil-Vote"
}

// PeerClient creates a temporary client for querying a peer's RPC.
func PeerClient(ip string, port string, timeout time.Duration) *Client {
	return &Client{
		RPCURL:  fmt.Sprintf("http://%s:%s", ip, port),
		HTTP:    &http.Client{Timeout: timeout},
		Timeout: timeout,
	}
}

// QueryPeerStatus queries a peer's RPC for its status. Returns nil on error.
func QueryPeerStatus(ctx context.Context, ip, port string, timeout time.Duration, logFn LogFunc) (*Status, error) {
	pc := PeerClient(ip, port, timeout)
	pc.LogFn = logFn
	return pc.GetStatus(ctx)
}

// QueryPeerConsensus queries a peer's RPC for consensus state. Returns nil on error.
func QueryPeerConsensus(ctx context.Context, ip, port string, timeout time.Duration, logFn LogFunc) (*ConsensusState, error) {
	pc := PeerClient(ip, port, timeout)
	pc.LogFn = logFn
	cs, _, err := pc.GetConsensusState(ctx)
	return cs, err
}

// QueryAllPeers queries all peers in parallel for their status and optionally consensus state.
// Validator detection uses the /validators endpoint: "val" (confirmed via RPC + in valset),
// "val" (timeout but moniker matches a validator in the set), "full" (not in valset).
func QueryAllPeers(ctx context.Context, peers []Peer, rpcPort string, timeout time.Duration, withConsensus bool, validators []Validator, logFn LogFunc, names *NameRegistry) []Peer {
	valAddrs := make(map[string]bool)
	for _, v := range validators {
		valAddrs[v.Address] = true
	}

	// Build moniker→isValidator lookup from the validator set using the registry
	monikerIsVal := make(map[string]bool)
	for _, v := range validators {
		name := names.Name(v.Address)
		if name != v.Address { // known name
			monikerIsVal[name] = true
		}
	}

	results := make([]Peer, len(peers))
	var wg sync.WaitGroup

	for i, p := range peers {
		results[i] = p
		results[i].P2PAddress = p.NodeID + "@" + p.RemoteIP + ":" + rpcPort
		wg.Add(1)
		go func(idx int, peer Peer) {
			defer wg.Done()

			status, err := QueryPeerStatus(ctx, peer.RemoteIP, rpcPort, timeout, logFn)
			if err != nil {
				results[idx].Height = "timeout"
				results[idx].Error = err.Error()
				// Check if moniker matches a validator in the active set
				if monikerIsVal[peer.Moniker] {
					results[idx].Role = "val"
					// Try to resolve the address from known mapping
					if addr, ok := names.AddrByMoniker(peer.Moniker); ok {
						results[idx].ValAddress = addr
					}
				} else {
					results[idx].Role = "full"
				}
				return
			}

			results[idx].Height = status.SyncInfo.LatestBlockHeight

			// CRITICAL: verify the RPC response belongs to THIS peer.
			// Extract node-id from the RPC response and compare to the peer's node-id.
			// On shared IPs (e.g., gnocore), the RPC may return a DIFFERENT node's info.
			rpcNodeID := ""
			if na := status.NodeInfo.NetAddress; na != "" {
				if atIdx := strings.Index(na, "@"); atIdx > 0 {
					rpcNodeID = na[:atIdx]
				}
			}
			if status.NodeInfo.ID != "" {
				rpcNodeID = status.NodeInfo.ID
			}

			rpcMatchesPeer := rpcNodeID == "" || rpcNodeID == peer.NodeID
			if rpcMatchesPeer {
				// RPC confirmed to be this peer — safe to register
				results[idx].ValAddress = status.ValidatorInfo.Address
				results[idx].ValPubKey = names.PubKey(status.ValidatorInfo.Address)
				if status.ValidatorInfo.Address != "" && peer.Moniker != "" {
					names.Register(status.ValidatorInfo.Address, peer.Moniker)
				}
			} else {
				// RPC belongs to a different node on the same IP — register THAT node's mapping
				// but don't assign it to THIS peer
				rpcMoniker := status.NodeInfo.Moniker
				if status.ValidatorInfo.Address != "" && rpcMoniker != "" {
					names.Register(status.ValidatorInfo.Address, rpcMoniker)
				}
				// Try to resolve this peer from known mappings instead
				if addr, ok := names.AddrByMoniker(peer.Moniker); ok {
					results[idx].ValAddress = addr
					results[idx].ValPubKey = names.PubKey(addr)
				}
			}

			if valAddrs[results[idx].ValAddress] {
				results[idx].Role = "val"
			} else if monikerIsVal[peer.Moniker] {
				results[idx].Role = "val"
				if addr, ok := names.AddrByMoniker(peer.Moniker); ok {
					results[idx].ValAddress = addr
					results[idx].ValPubKey = names.PubKey(addr)
				}
			} else {
				results[idx].Role = "full"
			}

			if withConsensus {
				cs, err := QueryPeerConsensus(ctx, peer.RemoteIP, rpcPort, timeout, logFn)
				if err != nil {
					results[idx].Round = "-"
					results[idx].Step = "-"
					return
				}
				results[idx].Round = cs.Round
				results[idx].Step = cs.Step
			}
		}(i, p)
	}

	wg.Wait()
	return results
}

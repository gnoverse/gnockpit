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

// SeedNamesFromGenesis streams the node's /genesis endpoint to seed the name
// registry with validator names and the genesis time. Only the head of the
// (very large) genesis document is read; the download is aborted as soon as the
// validators array has been parsed, well before the trailing app_state.
func (c *Client) SeedNamesFromGenesis(ctx context.Context) error {
	url := c.RPCURL + "/genesis"
	start := time.Now()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	dur := time.Since(start)
	if err != nil {
		if c.LogFn != nil {
			c.LogFn("GET", url, 0, dur, err)
		}
		return err
	}
	defer resp.Body.Close()
	if c.LogFn != nil {
		c.LogFn("GET", url, resp.StatusCode, dur, nil)
	}
	return c.Names.SeedFromGenesisStream(resp.Body)
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
		// gno's /net_info leaves node_info.id empty; the node ID is present
		// only embedded in net_address ("nodeID@host:port").
		nodeID := p.NodeInfo.ID
		if nodeID == "" {
			nodeID = nodeIDFromNetAddress(p.NodeInfo.NetAddress)
		}
		peers[i] = Peer{
			Moniker:         p.NodeInfo.Moniker,
			RemoteIP:        p.RemoteIP,
			NodeID:          nodeID,
			Version:         p.NodeInfo.Version,
			ExternalAddress: hostFromNetAddress(p.NodeInfo.NetAddress),
		}
	}
	return peers, nil
}

// hostFromNetAddress extracts the host from a "nodeID@host:port" net address.
func hostFromNetAddress(na string) string {
	if at := strings.Index(na, "@"); at >= 0 {
		na = na[at+1:]
	}
	if c := strings.LastIndex(na, ":"); c > 0 {
		na = na[:c]
	}
	return na
}

// nodeIDFromNetAddress extracts the node ID from a "nodeID@host:port" net
// address, or "" if no node ID is present.
func nodeIDFromNetAddress(na string) string {
	if at := strings.Index(na, "@"); at > 0 {
		return na[:at]
	}
	return ""
}

// GetNPeers fetches /net_info and returns just the peer count.
func (c *Client) GetNPeers(ctx context.Context) (int, error) {
	var result struct {
		NPeers string `json:"n_peers"`
	}
	if err := c.rpcGetJSON(ctx, "/net_info", &result); err != nil {
		return 0, err
	}
	var n int
	fmt.Sscanf(result.NPeers, "%d", &n)
	return n, nil
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

// GetValidatorSetAt returns the set of validator addresses at a given height,
// used to tell an established-but-down validator (in the set from the window's
// start) from one that was only added mid-window.
func (c *Client) GetValidatorSetAt(ctx context.Context, height int) (map[string]bool, error) {
	var result struct {
		Validators []struct {
			Address string `json:"address"`
		} `json:"validators"`
	}
	if err := c.rpcGetJSON(ctx, fmt.Sprintf("/validators?height=%d", height), &result); err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(result.Validators))
	for _, v := range result.Validators {
		set[v.Address] = true
	}
	return set, nil
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

// CommitInfo holds the header and precommit signatures for a single height,
// fetched via /commit (lighter than /block — no transaction body).
type CommitInfo struct {
	Height          string
	Time            string
	ProposerAddress string
	AppHash         string
	NumTxs          int
	// Precommits has one entry per validator slot; "null" means that validator
	// did not sign. These are the signatures that finalized THIS height.
	Precommits []json.RawMessage
}

// GetCommit fetches /commit at a height: the block header plus the precommits
// that finalized that height. Unlike a block's last_commit (which proves the
// previous height), /commit?height=H is the signing record for H itself.
func (c *Client) GetCommit(ctx context.Context, height int) (*CommitInfo, error) {
	path := fmt.Sprintf("/commit?height=%d", height)
	var result struct {
		SignedHeader struct {
			Header struct {
				Height          string `json:"height"`
				Time            string `json:"time"`
				ProposerAddress string `json:"proposer_address"`
				AppHash         string `json:"app_hash"`
				NumTxs          string `json:"num_txs"`
			} `json:"header"`
			Commit struct {
				Precommits []json.RawMessage `json:"precommits"`
			} `json:"commit"`
		} `json:"signed_header"`
	}
	if err := c.rpcGetJSON(ctx, path, &result); err != nil {
		return nil, err
	}
	h := result.SignedHeader.Header
	numTxs, _ := strconv.Atoi(h.NumTxs)
	return &CommitInfo{
		Height:          h.Height,
		Time:            h.Time,
		ProposerAddress: h.ProposerAddress,
		AppHash:         h.AppHash,
		NumTxs:          numTxs,
		Precommits:      result.SignedHeader.Commit.Precommits,
	}, nil
}

// GetSigningStats fetches the last `window` blocks (capped at 100) and returns
// per-validator signing activity, scoped to the blocks each validator was
// actually in the set for — a newly added validator is not charged for blocks
// before it joined. Active/inactive state and the BFT margin are derived by the
// caller from the streaks, since they need cross-cycle hysteresis.
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

	vals, err := c.GetValidators(ctx)
	if err != nil {
		return nil, fmt.Errorf("validators: %w", err)
	}
	valAddrs := make(map[string]bool, len(vals))
	for _, v := range vals {
		valAddrs[v.Address] = true
	}

	stats := &SigningStats{
		WindowSize:   window,
		TotalCount:   len(valAddrs),
		BFTThreshold: (len(valAddrs)*2)/3 + 1,
	}

	startHeight := currentHeight - window + 1
	// Valset at the window's first block (best-effort): lets computeSigning tell
	// an established-but-down validator from one only added mid-window. If this
	// fails (nil), a validator down for the whole window looks never-eligible and
	// won't be flagged that cycle — it recovers once the lookup succeeds.
	atStart, _ := c.GetValidatorSetAt(ctx, startHeight)

	// Pass 1: fetch each commit, collecting the ordered per-block signer sets
	// (oldest first) and block metadata.
	var signers []map[string]bool
	var proposers []string
	for h := startHeight; h <= currentHeight; h++ {
		commit, err := c.GetCommit(ctx, h)
		if err != nil {
			continue
		}
		// Total = precommit slots for this height (the valset at that height).
		blockTotal := len(commit.Precommits)
		if blockTotal == 0 {
			blockTotal = len(valAddrs) // fallback for genesis block
		}
		signed := map[string]bool{}
		for _, pc := range commit.Precommits {
			if string(pc) == "null" {
				continue
			}
			var vote struct {
				ValidatorAddress string `json:"validator_address"`
			}
			if json.Unmarshal(pc, &vote) == nil && vote.ValidatorAddress != "" {
				signed[vote.ValidatorAddress] = true
			}
		}
		proposerName := commit.ProposerAddress
		if c.Names != nil {
			if n := c.Names.Name(commit.ProposerAddress); n != commit.ProposerAddress {
				proposerName = n
			}
		}
		stats.RecentBlocks = append(stats.RecentBlocks, BlockInfo{
			Height:   commit.Height,
			Time:     commit.Time,
			Total:    blockTotal,
			Signers:  len(signed),
			Proposer: proposerName,
			AppHash:  commit.AppHash,
			NumTxs:   commit.NumTxs,
		})
		signers = append(signers, signed)
		proposers = append(proposers, commit.ProposerAddress)
	}

	stats.ValidatorSigning = computeSigning(signers, atStart, valAddrs)

	// Pass 2: per-block missing set — count a validator only for blocks where it
	// was in the set (From <= block index).
	for i := range stats.RecentBlocks {
		for addr := range valAddrs {
			if stats.ValidatorSigning[addr].From <= i && !signers[i][addr] {
				mv := MissingValidator{Address: addr}
				if c.Names != nil {
					if n := c.Names.Name(addr); n != addr {
						mv.Name = n
					}
				}
				stats.RecentBlocks[i].Missing = append(stats.RecentBlocks[i].Missing, mv)
			}
		}
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
		pAddr := proposers[i] // proposer_address from the block header
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
		perf.Signed = stats.ValidatorSigning[addr].Signed
	}

	// ActiveCount / Margin / CanAddOne depend on the hysteretic up/down state and
	// are set by the caller after the alert detector runs.
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
				Height     string      `json:"height"`
				Round      string      `json:"round"`
				Step       json.Number `json:"step"`
				Proposal   bool        `json:"proposal"`
				Prevotes   bitArray    `json:"prevotes"`
				Precommits bitArray    `json:"precommits"`
			} `json:"round_state"`
		}
		if err := json.Unmarshal(decoded, &ps); err == nil {
			dp.Height = ps.RoundState.Height
			dp.Round = ps.RoundState.Round
			dp.Step = ps.RoundState.Step.String()
			dp.Proposal = ps.RoundState.Proposal

			// Format prevotes/precommits as "count/total"
			bits, _ := strconv.Atoi(ps.RoundState.Prevotes.Bits)
			if bits == 0 {
				bits = valCount
			}
			dp.Prevotes = fmt.Sprintf("%d/%d", ps.RoundState.Prevotes.count(), bits)
			dp.Precommits = fmt.Sprintf("%d/%d", ps.RoundState.Precommits.count(), bits)
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
		// Find the fullest bit-array (most bits set) across all peers.
		var bestPrevote, bestPrecommit bitArray
		bestPVCount, bestPCCount := -1, -1
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
			if n := ps.RoundState.Prevotes.count(); n > bestPVCount {
				bestPVCount, bestPrevote = n, ps.RoundState.Prevotes
			}
			if n := ps.RoundState.Precommits.count(); n > bestPCCount {
				bestPCCount, bestPrecommit = n, ps.RoundState.Precommits
			}
		}

		// Apply to validators by index (handles sets larger than 64).
		for i := range cs.Votes {
			if i < valCount {
				cs.Votes[i].Prevoted = bestPrevote.bit(i)
				cs.Votes[i].Precommit = bestPrecommit.bit(i)
			}
		}
	}

	return cs, dumpPeers, nil
}

type bitArray struct {
	Bits  string   `json:"bits"`
	Elems []string `json:"elems"`
}

// bit reports whether validator index i has its bit set. The bit-array packs
// bits little-endian across 64-bit words: index i is bit (i%64) of word (i/64).
func (ba bitArray) bit(i int) bool {
	w := i / 64
	if w < 0 || w >= len(ba.Elems) {
		return false
	}
	n, _ := strconv.ParseUint(ba.Elems[w], 10, 64)
	return n&(1<<uint(i%64)) != 0
}

// count returns the number of set bits across all words.
func (ba bitArray) count() int {
	total := 0
	for _, e := range ba.Elems {
		n, _ := strconv.ParseUint(e, 10, 64)
		total += popcount(n)
	}
	return total
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

// MatchDumpPeer resolves the dump_consensus_state peer entry for a connected
// peer, preferring the unique, cryptographically-verified node ID over the
// remote IP. IPs can be shared (multiple sentries behind one host) or ephemeral
// for inbound peers, so matching on IP first misattributes consensus state.
func MatchDumpPeer(nodeID, ip string, byNodeID, byIP map[string]*DumpPeer) *DumpPeer {
	if nodeID != "" {
		if d, ok := byNodeID[nodeID]; ok {
			return d
		}
	}
	if ip != "" {
		if d, ok := byIP[ip]; ok {
			return d
		}
	}
	return nil
}

// rpcCandidates returns the ordered RPC base URLs to probe for a peer: the
// default RPC port on the observed IP and on the advertised external host, then
// HTTPS on 443. Empty and duplicate hosts are skipped.
func rpcCandidates(remoteIP, externalHost, rpcPort string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(u string) {
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	if remoteIP != "" {
		add("http://" + remoteIP + ":" + rpcPort)
	}
	if externalHost != "" {
		add("http://" + externalHost + ":" + rpcPort)
	}
	if externalHost != "" {
		add("https://" + externalHost + ":443")
	}
	if remoteIP != "" {
		add("https://" + remoteIP + ":443")
	}
	return out
}

// probePeerStatus tries each candidate RPC URL until one answers /status with a
// node ID matching expectNodeID (when both are known). Returns the working
// client and status, or nil if none qualify. Bounded to roughly timeout total.
func probePeerStatus(ctx context.Context, candidates []string, expectNodeID string, timeout time.Duration, logFn LogFunc) (*Client, *Status) {
	for _, url := range candidates {
		if ctx.Err() != nil {
			break
		}
		pc := &Client{RPCURL: url, HTTP: &http.Client{Timeout: timeout}, Timeout: timeout, LogFn: logFn}
		st, err := pc.GetStatus(ctx)
		if err != nil {
			continue
		}
		rpcNodeID := st.NodeInfo.ID
		if rpcNodeID == "" && st.NodeInfo.NetAddress != "" {
			if at := strings.Index(st.NodeInfo.NetAddress, "@"); at > 0 {
				rpcNodeID = st.NodeInfo.NetAddress[:at]
			}
		}
		// Reject a response from a different node sharing the IP.
		if expectNodeID != "" && rpcNodeID != "" && rpcNodeID != expectNodeID {
			continue
		}
		return pc, st
	}
	return nil, nil
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

			// Bound all per-peer RPC work (probe + peer count + consensus) to one
			// timeout budget so a single slow peer can't stall the snapshot.
			pctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()

			candidates := rpcCandidates(peer.RemoteIP, peer.ExternalAddress, rpcPort)
			pc, status := probePeerStatus(pctx, candidates, peer.NodeID, timeout, logFn)
			if pc == nil {
				// Unreachable on every candidate RPC. Not an error condition —
				// most peers don't expose a reachable RPC. Best-effort role from
				// the moniker; the RPC column shows nothing available.
				results[idx].Height = "timeout"
				if monikerIsVal[peer.Moniker] {
					results[idx].Role = "val"
					if addr, ok := names.AddrByMoniker(peer.Moniker); ok {
						results[idx].ValAddress = addr
					}
				} else {
					results[idx].Role = "full"
				}
				return
			}

			// Reachable: node ID was verified inside probePeerStatus.
			results[idx].RPCURL = pc.RPCURL
			results[idx].Height = status.SyncInfo.LatestBlockHeight
			results[idx].CatchingUp = status.SyncInfo.CatchingUp
			results[idx].ValAddress = status.ValidatorInfo.Address
			results[idx].ValPubKey = names.PubKey(status.ValidatorInfo.Address)
			if status.ValidatorInfo.Address != "" && peer.Moniker != "" {
				names.Register(status.ValidatorInfo.Address, peer.Moniker)
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

			// Reuse the working client for the peer's own peer count.
			if n, err := pc.GetNPeers(pctx); err == nil {
				results[idx].NPeers = n
			}

			if withConsensus {
				if cs, _, err := pc.GetConsensusState(pctx); err == nil {
					results[idx].Round = cs.Round
					results[idx].Step = cs.Step
				} else {
					results[idx].Round = "-"
					results[idx].Step = "-"
				}
			}
		}(i, p)
	}

	wg.Wait()
	return results
}

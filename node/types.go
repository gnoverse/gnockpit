package node

import (
	"encoding/base64"
	"time"

	"github.com/gnolang/gno/tm2/pkg/crypto"
	"github.com/gnolang/gno/tm2/pkg/crypto/ed25519"
	"github.com/gnolang/gno/tm2/pkg/crypto/secp256k1"
)

// Status represents the node's current status.
type Status struct {
	NodeInfo      NodeInfo `json:"node_info"`
	SyncInfo      SyncInfo `json:"sync_info"`
	ValidatorInfo ValInfo  `json:"validator_info"`
}

type NodeInfo struct {
	Moniker    string `json:"moniker"`
	Network    string `json:"network"`
	ID         string `json:"id"`
	Version    string `json:"version"`
	NetAddress string `json:"net_address"`
}

type SyncInfo struct {
	LatestBlockHeight string `json:"latest_block_height"`
	LatestBlockTime   string `json:"latest_block_time"`
	CatchingUp        bool   `json:"catching_up"`
}

type ValInfo struct {
	Address string `json:"address"`
	PubKey  PubKey `json:"pub_key"`
}

type PubKey struct {
	Type     string `json:"type"`
	AminoType string `json:"@type"` // gno RPC uses "@type" with "/tm.PubKeyEd25519" format
	Value    string `json:"value"`
}

// keyType returns the normalized key type string.
func (pk PubKey) keyType() string {
	if pk.Type != "" {
		return pk.Type
	}
	switch pk.AminoType {
	case "/tm.PubKeyEd25519":
		return "ed25519"
	case "/tm.PubKeySecp256k1":
		return "secp256k1"
	}
	return pk.AminoType
}

// Bech32 returns the bech32-encoded public key (gpub1...).
// Returns empty string if the key type is unknown or the value is invalid.
func (pk PubKey) Bech32() string {
	if pk.Value == "" {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(pk.Value)
	if err != nil {
		return ""
	}
	var cpk crypto.PubKey
	switch pk.keyType() {
	case "ed25519":
		if len(raw) != ed25519.PubKeyEd25519Size {
			return ""
		}
		var key ed25519.PubKeyEd25519
		copy(key[:], raw)
		cpk = key
	case "secp256k1":
		if len(raw) != secp256k1.PubKeySecp256k1Size {
			return ""
		}
		var key secp256k1.PubKeySecp256k1
		copy(key[:], raw)
		cpk = key
	default:
		return ""
	}
	return crypto.PubKeyToBech32(cpk)
}

// Peer represents a connected peer.
type Peer struct {
	Moniker    string `json:"moniker"`
	RemoteIP   string `json:"remote_ip"`
	NodeID     string `json:"node_id"`
	Version    string `json:"version,omitempty"`     // software version from net_info
	P2PAddress string `json:"p2p_address,omitempty"` // nodeID@ip:port
	// Populated by network/consensus queries
	CatchingUp     bool   `json:"catching_up,omitempty"`
	ValAddress     string `json:"val_address,omitempty"` // validator address if known
	ValPubKey      string `json:"val_pubkey,omitempty"`  // base64 pubkey if known
	Role           string `json:"role,omitempty"`        // "val", "val?", "full"
	Height         string `json:"height,omitempty"`
	Round          string `json:"round,omitempty"`
	Step           string `json:"step,omitempty"`
	Error          string `json:"error,omitempty"`
	FirstSeen      string `json:"first_seen,omitempty"`
	LastSeen       string `json:"last_seen,omitempty"`
	NPeers         int    `json:"n_peers,omitempty"`
	// From dump_consensus_state peer data
	PeerHeight     string `json:"peer_height,omitempty"`
	PeerRound      string `json:"peer_round,omitempty"`
	PeerStep       string `json:"peer_step,omitempty"`
	HasProposal    bool   `json:"has_proposal,omitempty"`
	PeerPrevotes   string `json:"peer_prevotes,omitempty"`
	PeerPrecommits string `json:"peer_precommits,omitempty"`
}

// Validator represents a validator from the validator set.
type Validator struct {
	Address     string `json:"address"`
	PubKey      PubKey `json:"pub_key"`
	VotingPower string `json:"voting_power"`
	Name        string `json:"name"`
}

// ConsensusState represents the current consensus state.
type ConsensusState struct {
	Height         string          `json:"height"`
	Round          string          `json:"round"`
	Step           string          `json:"step"`
	Proposer       string          `json:"proposer"`
	RoundStartTime string          `json:"round_start_time,omitempty"`
	Config         *ConsensusConfig `json:"config,omitempty"`
	Votes          []VoteInfo      `json:"votes,omitempty"`
	Peers          []PeerState     `json:"peers,omitempty"`
}

// ConsensusConfig holds consensus timeout configuration.
type ConsensusConfig struct {
	TimeoutPropose   string `json:"timeout_propose"`
	TimeoutPrevote   string `json:"timeout_prevote"`
	TimeoutPrecommit string `json:"timeout_precommit"`
	TimeoutCommit    string `json:"timeout_commit"`
	EmptyBlocks      bool   `json:"create_empty_blocks"`
}

// DumpPeer holds parsed per-peer consensus data from dump_consensus_state.
type DumpPeer struct {
	NodeID     string `json:"node_id"`
	RemoteIP   string `json:"remote_ip"`
	P2PPort    string `json:"p2p_port"`
	Height     string `json:"height"`
	Round      string `json:"round"`
	Step       string `json:"step"`
	Proposal   bool   `json:"proposal"`
	Prevotes   string `json:"prevotes"`   // e.g. "4/6"
	Precommits string `json:"precommits"` // e.g. "3/6"
}

// VoteInfo holds decoded vote information per validator.
type VoteInfo struct {
	Index       int    `json:"index"`
	Address     string `json:"address"`
	Name        string `json:"name"`
	PubKey      string `json:"pub_key,omitempty"`
	VotingPower string `json:"voting_power,omitempty"`
	Prevoted    bool   `json:"prevoted"`
	Precommit   bool   `json:"precommit"`
	SignRate    int    `json:"sign_rate"`    // signed blocks / window (0-100%)
	AvgBlockMs  int    `json:"avg_block_ms"` // avg block time when proposing (ms)
}

// PeerState from dump_consensus_state peer_state (base64-encoded).
type PeerState struct {
	Prevotes   string `json:"prevotes"`
	Precommits string `json:"precommits"`
}

// CheckResult represents a verification check.
type CheckResult struct {
	Name     string `json:"name"`
	Status   string `json:"status"` // "ok", "mismatch", "missing", "n/a", "error"
	Got      string `json:"got,omitempty"`
	Expected string `json:"expected,omitempty"`
	Message  string `json:"message,omitempty"`
}

// SystemInfo holds system resource information.
type SystemInfo struct {
	DiskUsed       string `json:"disk_used"`
	DiskTotal      string `json:"disk_total"`
	DiskPercent    int    `json:"disk_percent"`
	LoadAvg        string `json:"load_avg"`
	NumCPU         int    `json:"num_cpu"`
	MemUsed        string `json:"mem_used"`
	MemTotal       string `json:"mem_total"`
	MemPercent     int    `json:"mem_percent"`
	GnolandUptime  string `json:"gnoland_uptime"`
	GnolandMem     string `json:"gnoland_mem"`
	ChainDataSize  string `json:"chain_data_size"`
	NodeTime       string `json:"node_time"`
	GenesisTime    string `json:"genesis_time,omitempty"`
	GitBranch      string `json:"git_branch,omitempty"`
	GitSHA         string `json:"git_sha,omitempty"`
	BinaryHash     string `json:"binary_hash,omitempty"`
	Seeds          string `json:"seeds,omitempty"`
}

// MissingValidator identifies a validator that did not sign a block.
type MissingValidator struct {
	Name    string `json:"name"`    // moniker, or empty if unknown
	Address string `json:"address"` // full bech32 address
}

// BlockInfo holds signing info for a recent block.
type BlockInfo struct {
	Height   string             `json:"height"`
	Time     string             `json:"time"`
	Signers  int                `json:"signers"`
	Total    int                `json:"total"`
	Missing  []MissingValidator `json:"missing,omitempty"`
	Proposer string             `json:"proposer"`          // proposer name
	BlockMs  int                `json:"block_ms"`          // time since previous block in ms; 0 for oldest block in window
	AppHash  string             `json:"app_hash"`
}

// ValidatorPerf tracks per-validator performance metrics.
type ValidatorPerf struct {
	AvgBlockMs int `json:"avg_block_ms"` // average block time when proposing
	Proposed   int `json:"proposed"`     // blocks proposed in window
	Signed     int `json:"signed"`       // blocks signed in window
}

// SigningStats summarizes validator signing activity over recent blocks.
type SigningStats struct {
	WindowSize     int                       `json:"window_size"`
	ActiveCount    int                       `json:"active_count"`    // signed all blocks in window
	TotalCount     int                       `json:"total_count"`     // validators in set
	BFTThreshold   int                       `json:"bft_threshold"`   // minimum needed for consensus
	Margin         int                       `json:"margin"`          // active - threshold (how many can go down)
	CanAddOne      bool                      `json:"can_add_one"`     // safe to add a validator?
	AvgBlockMs     int                       `json:"avg_block_ms"`    // average block time across window
	RecentBlocks   []BlockInfo               `json:"recent_blocks"`
	ValidatorSigns map[string]int            `json:"validator_signs"` // addr -> sign count in window
	ValidatorPerf  map[string]*ValidatorPerf `json:"validator_perf"`  // addr -> perf stats
}

// Snapshot holds all data fetched in one cycle by the background fetcher.
type Snapshot struct {
	Status         *Status          `json:"status,omitempty"`
	Consensus      *ConsensusState  `json:"consensus,omitempty"`
	Peers          []Peer           `json:"peers,omitempty"`
	Validators     []Validator      `json:"validators,omitempty"`
	GenesisSHA     string           `json:"genesis_sha256"`
	AppHashLast    string           `json:"apphash_last"`
	Uptime         string           `json:"uptime,omitempty"`
	RoundStartTime string           `json:"round_start_time,omitempty"`
	System         *SystemInfo      `json:"system,omitempty"`
	Signing        *SigningStats    `json:"signing,omitempty"`
	Timestamp      time.Time        `json:"timestamp"`
	Error          string           `json:"error,omitempty"`
}

// VotesReport shows the prevote/precommit state.
type VotesReport struct {
	Height         string           `json:"height"`
	Round          string           `json:"round"`
	Step           string           `json:"step"`
	Proposer       string           `json:"proposer"`
	RoundStartTime string           `json:"round_start_time,omitempty"`
	Config         *ConsensusConfig `json:"config,omitempty"`
	Validators     []VoteInfo       `json:"validators"`
	Timestamp      time.Time        `json:"timestamp"`
}

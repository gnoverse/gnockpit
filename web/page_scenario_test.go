//go:build page

package web

import (
	"cmp"
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gnolang/gno/tm2/pkg/crypto"
	"github.com/gnolang/gno/tm2/pkg/crypto/ed25519"

	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/ranking"
)

// scenario is the state the server under test starts from. The chain is the
// fake: a snapshot and a block history built in Go; everything else is the
// real server.
type scenario struct {
	now  time.Time      // the server's and the browser's clock; pageNow when zero
	snap *node.Snapshot // the published snapshot; nil while the server has none
	// names is the validator-name registry, address to moniker.
	names map[string]string
	// seed writes the block history; nil leaves it disabled.
	seed        func(t *testing.T, s *blocks.Store)
	fill        blocks.Progress         // how far the history is filled, read with seed
	fillFrom    BlockFill               // reports the fill in fill's stead, for a test to change it
	valopers    map[string]node.Valoper // valopers profiles by operator address
	eta         ETAView                 // the block-time model; nil disables /api/eta
	links       []Link
	statusLinks []*StatusLink
	rpcURL      string // the node the server reads blocks from; none when empty
	chainName   string // --chain-name
	hideSources bool   // --hide-sources
	push        bool   // a push manager, the push subscription stub and service workers
	// ranking turns the daily ranking on over the block history; nil leaves
	// it off.
	ranking *pageRanking
	// chainStuckSecs and maxMissedInARow are --chain-stuck-secs and
	// --max-missed-in-a-row; main.go's defaults when zero.
	chainStuckSecs, maxMissedInARow int
}

// pageRanking is the ranking's configuration: --ranking-exclude and
// --ranking-hide-excluded.
type pageRanking struct {
	exclude      []string
	hideExcluded bool
}

// clock returns the scenario's instant.
func (sc *scenario) clock() time.Time {
	if sc.now.IsZero() {
		return pageNow
	}
	return sc.now
}

// serverClock is the clock of the server under test: the scenario's instant,
// moved on as far as the test moves the browser's clock, so that the server
// and the browser read the same now, unless a test moves the server's alone,
// as a clock correction on its host does.
type serverClock struct {
	at      time.Time
	elapsed atomic.Int64 // nanoseconds
}

func (c *serverClock) now() time.Time { return c.at.Add(time.Duration(c.elapsed.Load())) }

// advance moves the clock by d: forward, or back for a negative d.
func (c *serverClock) advance(d time.Duration) { c.elapsed.Add(int64(d)) }

// newPageServer builds the server under test on sc, configured as main.go
// configures it, reading clock.
func newPageServer(t *testing.T, sc *scenario, clock *serverClock) *Server {
	t.Helper()
	srv := newSeamServer(t)
	srv.clock = clock.now
	srv.ChainStuckSecs = cmp.Or(sc.chainStuckSecs, pageChainStuckSecs)
	srv.MaxMissedInARow = cmp.Or(sc.maxMissedInARow, pageMaxMissedInARow)
	if sc.rpcURL != "" {
		srv.Client.RPCURL = sc.rpcURL
	}
	srv.ChainName = sc.chainName
	srv.HideSources = sc.hideSources
	srv.Links = sc.links
	srv.StatusLinks = sc.statusLinks
	srv.ETA = sc.eta
	srv.valopers = sc.valopers
	for addr, moniker := range sc.names {
		srv.Client.Names.Register(addr, moniker)
	}
	if sc.seed != nil {
		store := openBlocksStore(t)
		sc.seed(t, store)
		srv.Blocks = store
		srv.BlockFill = fakeFill(sc.fill)
		if sc.fillFrom != nil {
			srv.BlockFill = sc.fillFrom
		}
	}
	if sc.ranking != nil {
		srv.Ranking = newPagePublisher(t, sc, srv, clock)
		srv.RankingHideExcluded = sc.ranking.hideExcluded
	}
	if sc.push {
		srv.PushManager = newPushManager(t, srv.ChainStuckSecs, srv.MaxMissedInARow)
	}
	if sc.snap != nil {
		publishSnapshot(t.Context(), srv, sc.snap)
	}
	return srv
}

// newPagePublisher builds the ranking's publisher as main.go does, on the
// scenario's block history and clock. Its profiles are the scenario's
// valopers, read from the start: the server under test runs no valoper
// refresh. It runs one pass, as the publisher's loop does at once; a test
// runs another with Refresh.
func newPagePublisher(t *testing.T, sc *scenario, srv *Server, clock *serverClock) *ranking.Publisher {
	t.Helper()
	if srv.Blocks == nil {
		t.Fatal("newPagePublisher: the ranking reads the block history, which the scenario leaves disabled: set seed")
	}
	days, err := ranking.NewDayStore(t.Context(), openRankingDB(t))
	if err != nil {
		t.Fatal(err)
	}
	pub := &ranking.Publisher{
		Blocks:   srv.Blocks,
		Fill:     srv.BlockFill,
		Days:     days,
		Profiles: func() (ranking.Profiles, bool) { return ranking.Profiles(sc.valopers), true },
		Names:    srv.Moniker,
		Exclude:  sc.ranking.exclude,
		Now:      clock.now,
	}
	if err := pub.Refresh(t.Context()); err != nil {
		t.Fatalf("the ranking's first pass: %v", err)
	}
	return pub
}

// publishSnapshot publishes snap as the publish loop does after a fetch: the
// source's validator is ours, every validator address gets its registry name,
// then the snapshot is published.
func publishSnapshot(ctx context.Context, srv *Server, snap *node.Snapshot) {
	if snap.Status != nil {
		srv.Client.Names.SetOurs(snap.Status.ValidatorInfo.Address, snap.Status.NodeInfo.Moniker)
	}
	srv.ensureValidatorNames(snap)
	srv.publish(ctx, snap)
}

// ---- Builders

// bootScenario is a server that has just started: no chain data yet.
func bootScenario() *scenario {
	return &scenario{now: pageNow}
}

// sourcesDown is the snapshot the publish loop makes while every source is
// unreachable.
func sourcesDown() *node.Snapshot {
	return &node.Snapshot{
		Error:     "all sources unreachable",
		System:    &node.SystemInfo{NodeTime: pageNow.Format(nodeTimeLayout)},
		Timestamp: pageNow,
	}
}

const (
	// pageChainID is the chain the healthy network runs.
	pageChainID = "test-13"
	// pageHeight is the healthy network's head: three days of one-minute
	// blocks.
	pageHeight = 3 * 24 * 60
	// nodeTimeLayout is the layout of node.SystemInfo's times.
	nodeTimeLayout = "2006-01-02T15:04:05Z"
)

// pageValidator is a validator of the healthy network, its keys derived from
// its seed.
type pageValidator struct {
	moniker string // its registry name; empty names it by its address
	power   int64
	key     ed25519.PubKeyEd25519
}

func newPageValidator(seed, moniker string, power int64) pageValidator {
	key, ok := ed25519.GenPrivKeyFromSecret([]byte(seed)).PubKey().(ed25519.PubKeyEd25519)
	if !ok {
		panic("ed25519 private key without an ed25519 public key")
	}
	return pageValidator{moniker: moniker, power: power, key: key}
}

func (v pageValidator) address() string { return v.key.Address().String() }

func (v pageValidator) pubKey() node.PubKey {
	return node.PubKey{AminoType: "/tm.PubKeyEd25519", Value: base64.StdEncoding.EncodeToString(v.key[:])}
}

// member is v as a member of a validator set, at its own power.
func (v pageValidator) member() blocks.Member { return memberAt(v, v.power) }

// members is the validator set of vs, each at its own power.
func members(vs ...pageValidator) []blocks.Member {
	out := make([]blocks.Member, len(vs))
	for i, v := range vs {
		out[i] = v.member()
	}
	return out
}

// name is its registry name: the moniker, or the address.
func (v pageValidator) name() string {
	if v.moniker == "" {
		return v.address()
	}
	return v.moniker
}

// The healthy network's validators: alpha is the source's own, delta is
// named by its address.
var (
	valAlpha   = newPageValidator("alpha", "alpha", 10)
	valBravo   = newPageValidator("bravo", "bravo", 10)
	valCharlie = newPageValidator("charlie", "charlie", 10)
	valDelta   = newPageValidator("delta", "", 5)
)

// healthyValidators is the healthy network's set, in set order.
func healthyValidators() []pageValidator {
	return []pageValidator{valAlpha, valBravo, valCharlie, valDelta}
}

// healthyBlockTime is the time of height h of the healthy network: one block
// a minute, the head 2 s before pageNow.
func healthyBlockTime(h int64) time.Time {
	return pageNow.Add(-2*time.Second - time.Duration(pageHeight-h)*time.Minute)
}

// healthyAbsences are the blocks of the live window a validator misses.
func healthyAbsences() map[int64][]string {
	return map[int64][]string{
		pageHeight - 50: {valCharlie.address()},
		pageHeight - 10: {valDelta.address()},
	}
}

// healthyNetwork is a chain running well: four validators, one of them the
// source's own and one named by its address, voting powers 10/10/10/5; six
// peers; a round started 4 s ago; a live window of 100 blocks, two of them
// missing a validator and three with transactions; and three days of block
// history.
func healthyNetwork() *scenario {
	vals := healthyValidators()
	names := make(map[string]string, len(vals))
	for _, v := range vals {
		names[v.address()] = v.name()
	}
	set := members(vals...)
	return &scenario{
		now:   pageNow,
		snap:  healthySnapshot(),
		names: names,
		seed: func(t *testing.T, s *blocks.Store) {
			putBlocks(t, s, healthyBlockTime, "HEALTHY", set, 1, pageHeight, healthyAbsences())
		},
		fill: blocks.Progress{ChainID: pageChainID, From: 1, To: pageHeight, FromTime: healthyBlockTime(1), Complete: true},
	}
}

// healthySnapshot is the healthy network's live state.
func healthySnapshot() *node.Snapshot {
	vals := healthyValidators()
	head := healthyBlockTime(pageHeight)
	snap := &node.Snapshot{
		Status: &node.Status{
			NodeInfo: node.NodeInfo{
				Moniker:    valAlpha.moniker,
				Network:    pageChainID,
				ID:         nodeID("alpha"),
				NetAddress: nodeID("alpha") + "@203.0.113.10:26656",
			},
			SyncInfo:      node.SyncInfo{LatestBlockHeight: strconv.Itoa(pageHeight), LatestBlockTime: head.Format(time.RFC3339Nano)},
			ValidatorInfo: node.ValInfo{Address: valAlpha.address(), PubKey: valAlpha.pubKey()},
		},
		Consensus:      healthyConsensus(),
		Peers:          healthyPeers(),
		AppHashLast:    appHash(pageHeight),
		RoundStartTime: pageNow.Add(-4 * time.Second).Format(time.RFC3339Nano),
		System: &node.SystemInfo{
			NodeTime:    pageNow.Format(nodeTimeLayout),
			GenesisTime: healthyBlockTime(0).Format(nodeTimeLayout),
		},
		Signing:   healthySigning(),
		Timestamp: pageNow,
	}
	for _, v := range vals {
		snap.Validators = append(snap.Validators, node.Validator{
			Address: v.address(), PubKey: v.pubKey(), VotingPower: strconv.FormatInt(v.power, 10),
		})
	}
	return snap
}

// healthyConsensus is the round in progress above the head: alpha, bravo
// and charlie have prevoted, delta not yet.
func healthyConsensus() *node.ConsensusState {
	cs := &node.ConsensusState{
		Height:         strconv.Itoa(pageHeight + 1),
		Round:          "0",
		Step:           "4",
		Proposer:       valBravo.address(),
		RoundStartTime: pageNow.Add(-4 * time.Second).Format(time.RFC3339Nano),
		Config: &node.ConsensusConfig{
			TimeoutPropose: "3s", TimeoutPrevote: "1s", TimeoutPrecommit: "1s", TimeoutCommit: "1s", EmptyBlocks: true,
		},
	}
	for i, v := range healthyValidators() {
		cs.Votes = append(cs.Votes, node.VoteInfo{Index: i, Address: v.address(), Prevoted: v != valDelta})
	}
	return cs
}

// healthyPeers are the source's peers, sorted by moniker as fetchSnapshot
// sorts them: bravo and charlie are validators, sentry-1 is a source,
// full-norpc answers no RPC, eu-node and us-node are located.
func healthyPeers() []node.Peer {
	seen := pageNow.Format(time.RFC3339)
	peer := func(moniker, ip string) node.Peer {
		return node.Peer{
			Moniker: moniker, RemoteIP: ip, NodeID: nodeID(moniker), P2PAddress: nodeID(moniker) + "@" + ip + ":26656",
			RPCURL: "http://" + ip + ":26657", Role: "full", Height: strconv.Itoa(pageHeight), Round: "0", Step: "4",
			FirstSeen: seen, LastSeen: seen, NPeers: 5,
		}
	}
	bravo := peer("bravo", "203.0.113.11")
	bravo.Role, bravo.ValAddress, bravo.ValPubKey = "val", valBravo.address(), valBravo.pubKey().Value
	charlie := peer("charlie", "203.0.113.12")
	charlie.Role, charlie.ValAddress, charlie.ValPubKey = "val", valCharlie.address(), valCharlie.pubKey().Value
	eu := peer("eu-node", "198.51.100.21")
	eu.Lat, eu.Lon, eu.City, eu.Country = float64Ptr(50.11), float64Ptr(8.68), "Frankfurt am Main", "DE"
	eu.ASN, eu.ASOrg = 24940, "Hetzner Online GmbH"
	eu.Provider = node.Provider(eu.ASOrg)
	noRPC := peer("full-norpc", "203.0.113.14")
	noRPC.RPCURL = ""
	sentry := peer("sentry-1", "203.0.113.13")
	sentry.Source = true
	us := peer("us-node", "198.51.100.22")
	us.Lat, us.Lon, us.City, us.Country = float64Ptr(39.04), float64Ptr(-77.49), "Ashburn", "US"
	us.ASN, us.ASOrg = 16509, "AMAZON-02"
	us.Provider = node.Provider(us.ASOrg)
	return []node.Peer{bravo, charlie, eu, noRPC, sentry, us}
}

// healthySigning is the live window: the last 100 blocks, all proposed by
// alpha, charlie missing one 50 blocks ago and delta one 10 blocks ago,
// three blocks with transactions.
func healthySigning() *node.SigningStats {
	const window = 100
	absences := healthyAbsences()
	txs := map[int64]int{pageHeight - 80: 2, pageHeight - 30: 1, pageHeight - 5: 5}
	vals := healthyValidators()
	stats := &node.SigningStats{
		WindowSize:      window,
		TotalCount:      len(vals),
		MaxMissedInARow: pageMaxMissedInARow,
		BFTThreshold:    len(vals)*2/3 + 1,
		AvgBlockMs:      int(time.Minute.Milliseconds()),
	}
	for h := int64(pageHeight - window + 1); h <= pageHeight; h++ {
		b := node.BlockInfo{
			Height: strconv.FormatInt(h, 10), Time: healthyBlockTime(h).Format(time.RFC3339Nano),
			Signers: len(vals), Total: len(vals), Proposer: valAlpha.moniker, AppHash: appHash(h), NumTxs: txs[h],
		}
		if h > pageHeight-window+1 {
			b.BlockMs = int(time.Minute.Milliseconds())
		}
		for _, addr := range absences[h] {
			b.Signers--
			b.Missing = append(b.Missing, node.MissingValidator{Address: addr})
		}
		stats.RecentBlocks = append(stats.RecentBlocks, b)
	}
	signed := func(delayMs int) node.ValSigning {
		return node.ValSigning{Signed: window, Eligible: window, SignedInARow: window, Votes: window, VoteDelayMs: delayMs}
	}
	missedOnce := func(signedSince, delayMs int) node.ValSigning {
		return node.ValSigning{
			Signed: window - 1, Eligible: window, Missed: 1, SignedInARow: signedSince, Votes: window - 1, VoteDelayMs: delayMs,
		}
	}
	stats.ValidatorSigning = map[string]node.ValSigning{
		valAlpha.address():   signed(12),
		valBravo.address():   signed(35),
		valCharlie.address(): missedOnce(50, 80),
		valDelta.address():   missedOnce(10, 240),
	}
	return stats
}

// pageLandscapeIPhone is a touch screen wider than 768 px, a large iPhone
// held sideways: the desktop layout, without hover.
const pageLandscapeIPhone = "iPhone 17 Pro Max landscape"

// shortAddr is an address as the page shortens it: "g1", its characters 2
// to 8, an ellipsis and its last 6.
func shortAddr(addr string) string {
	return addr[:8] + "…" + addr[len(addr)-6:]
}

// nodeID is the node ID of the node named name.
func nodeID(name string) string {
	return crypto.AddressFromPreimage([]byte("node " + name)).String()
}

// appHash is the app hash of height h.
func appHash(h int64) string {
	return fmt.Sprintf("%064X", h)
}

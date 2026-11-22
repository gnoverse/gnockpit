//go:build page

package web

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/eta"
	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/web/push"
)

// ---- Fakes

// proxyTimeout is how long the proxy in front of the server waits for an
// answer it may give up.
const proxyTimeout = pageTimeout / 5

// behindProxy serves the server under test through a reverse proxy, as
// Caddy, nginx or a load balancer in front of gnockpit do, and returns the
// proxy's URL. The proxy gives up a request for path the server does not
// answer within proxyTimeout, and answers it status with its plain-text
// name, as nginx answers 502 or HAProxy 503; it waits for any other answer.
// Giving up cancels the request to the server, which ends the gate's hold on
// it: closed first at the test's end, the proxy has no HTTP request in
// flight. The page's WebSocket goes through it too, hijacked from the
// proxy's server, so closing the proxy does not wait for it.
func (e *pageEnv) behindProxy(path string, status int) string {
	e.T.Helper()
	target, err := url.Parse(e.ts.URL)
	if err != nil {
		e.T.Fatalf("parse the server URL: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, http.StatusText(status), status)
	}
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == path {
			ctx, cancel := context.WithTimeout(r.Context(), proxyTimeout)
			defer cancel()
			r = r.WithContext(ctx)
		}
		proxy.ServeHTTP(w, r)
	}))
	e.T.Cleanup(front.Close)
	return front.URL
}

// settableFill stands in for the tracker as the server's BlockFill
// (scenario.fillFrom): the fill progress a test moves while the page reads
// it.
type settableFill struct {
	mu sync.Mutex
	p  blocks.Progress
}

var _ BlockFill = (*settableFill)(nil)

// Progress returns the progress the test last set.
func (f *settableFill) Progress() blocks.Progress {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.p
}

// set makes p the progress the server reports.
func (f *settableFill) set(p blocks.Progress) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.p = p
}

// serverDevices returns the devices the server holds.
func serverDevices(t *testing.T, srv *Server) []push.Subscription {
	t.Helper()
	subs, err := srv.PushManager.DB().AllSubscriptions()
	if err != nil {
		t.Fatalf("read the devices: %v", err)
	}
	return subs
}

// expectDevice fails t unless the server holds exactly want for
// device id.
func expectDevice(t *testing.T, srv *Server, id string, want []push.SubscriptionAlert) {
	t.Helper()
	for i := range want {
		want[i].SubscriptionID = id
	}
	if got := deviceAlerts(t, srv, id); !reflect.DeepEqual(got, want) {
		t.Errorf("alerts of device %s = %+v, want %+v", id, got, want)
	}
}

// deviceAlerts returns the alerts the server holds for device id, by type
// then entity.
func deviceAlerts(t *testing.T, srv *Server, id string) []push.SubscriptionAlert {
	t.Helper()
	rows, err := srv.PushManager.DB().SQL().Query(
		`SELECT alert_type, entity_id, recovery_notif, lead FROM subscription_alerts
		WHERE subscription_id = ? ORDER BY alert_type, entity_id`, id)
	if err != nil {
		t.Fatalf("read the alerts of %s: %v", id, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Errorf("read the alerts of %s: %v", id, err)
		}
	}()
	var out []push.SubscriptionAlert
	for rows.Next() {
		a := push.SubscriptionAlert{SubscriptionID: id}
		if err := rows.Scan(&a.AlertType, &a.EntityID, &a.RecoveryNotif, &a.Lead); err != nil {
			t.Fatalf("read the alerts of %s: %v", id, err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read the alerts of %s: %v", id, err)
	}
	return out
}

// ---- Builders

// memberAt is v as a member of a validator set, at power.
func memberAt(v pageValidator, power int64) blocks.Member {
	return blocks.Member{Address: v.address(), PubKey: v.pubKey().Value, Power: power}
}

// healthyAt is the healthy network with its head at height h, the round in
// progress above it.
func healthyAt(h int) *node.Snapshot {
	snap := healthySnapshot()
	snap.Status.SyncInfo.LatestBlockHeight = strconv.Itoa(h)
	snap.Consensus.Height = strconv.Itoa(h + 1)
	return snap
}

// fullPeer is a full node named moniker that no database has located.
func fullPeer(moniker string) node.Peer {
	seen := pageNow.Format(time.RFC3339)
	return node.Peer{
		Moniker: moniker, NodeID: nodeID(moniker), Role: "full", Height: strconv.Itoa(pageHeight),
		FirstSeen: seen, LastSeen: seen,
	}
}

// withRecentBlocks is the healthy snapshot with blks, oldest first, as its
// recent blocks.
func withRecentBlocks(blks ...node.BlockInfo) *node.Snapshot {
	snap := healthySnapshot()
	snap.Signing.RecentBlocks = blks
	return snap
}

// consecutiveBlocks is n blocks without transactions, oldest first, the
// newest at height newest.
func consecutiveBlocks(newest int64, n int) []node.BlockInfo {
	blks := make([]node.BlockInfo, n)
	for i := range blks {
		blks[i] = blockAt(newest-int64(n-1-i), 0)
	}
	return blks
}

// blockAt is a block of height h that the whole set of four signed, with
// txs transactions.
func blockAt(h int64, txs int) node.BlockInfo {
	return node.BlockInfo{
		Height: strconv.FormatInt(h, 10), Time: healthyBlockTime(h).Format(time.RFC3339Nano),
		Signers: 4, Total: 4, BlockMs: int(time.Minute.Milliseconds()), NumTxs: txs,
	}
}

// sortedPeers sorts peers by moniker, keeping their order among equal
// monikers: one order the server can send, as fetchSnapshot sorts by
// moniker alone.
func sortedPeers(peers []node.Peer) []node.Peer {
	slices.SortStableFunc(peers, func(a, b node.Peer) int { return cmp.Compare(a.Moniker, b.Moniker) })
	return peers
}

// valSpec is a validator of a test network: its keys, name and power,
// its node among the source's peers, its live window, and its last day in
// the block history.
type valSpec struct {
	val pageValidator
	// node is its node among the source's peers, named like it and reporting
	// its address; nil for none.
	node    *node.Peer
	signing node.ValSigning
	// absent is how many blocks of the last day it missed; proposals and
	// missedProposals how many of the last day's it was expected to make,
	// and missed.
	absent, proposals, missedProposals int
}

// validatorNode is the node of validator v, at ip, located in country and
// hosted by provider; empty values leave them unknown.
func validatorNode(v pageValidator, ip, country, provider string) *node.Peer {
	p := fullPeer(v.moniker)
	p.Role, p.ValAddress, p.ValPubKey = "val", v.address(), v.pubKey().Value
	p.RemoteIP, p.Country, p.Provider = ip, country, provider
	return &p
}

// specNetwork is the healthy network with specs as its validators, in
// set order: alpha, when among them, is the source's own. The source sees
// the healthy network's peers that are no validator's node, and the specs'
// nodes. The block history holds the specs' last day.
func specNetwork(specs ...valSpec) *scenario {
	sc := healthyNetwork()
	sc.names = map[string]string{}
	snap := healthySnapshot()
	snap.Validators, snap.Consensus.Votes = nil, nil
	snap.Signing.ValidatorSigning = map[string]node.ValSigning{}
	snap.Signing.TotalCount = len(specs)
	snap.Signing.BFTThreshold = len(specs)*2/3 + 1
	peers := slices.DeleteFunc(healthyPeers(), func(p node.Peer) bool { return p.ValAddress != "" })
	for i, s := range specs {
		addr := s.val.address()
		sc.names[addr] = s.val.name()
		snap.Validators = append(snap.Validators, node.Validator{
			Address: addr, PubKey: s.val.pubKey(), VotingPower: strconv.FormatInt(s.val.power, 10),
		})
		snap.Consensus.Votes = append(snap.Consensus.Votes, node.VoteInfo{Index: i, Address: addr, Prevoted: true})
		snap.Signing.ValidatorSigning[addr] = s.signing
		if s.node != nil {
			peers = append(peers, *s.node)
		}
	}
	// Sorted by moniker, as fetchSnapshot sorts them.
	slices.SortStableFunc(peers, func(a, b node.Peer) int { return cmp.Compare(a.Moniker, b.Moniker) })
	snap.Peers = peers
	sc.snap = snap

	recs := specHistory(specs)
	set := make([]blocks.Member, len(specs))
	for i, s := range specs {
		set[i] = s.val.member()
	}
	sc.seed = func(t *testing.T, s *blocks.Store) {
		t.Helper()
		if err := s.PutValset(t.Context(), specValset, set); err != nil {
			t.Fatal(err)
		}
		if err := s.Write(t.Context(), recs); err != nil {
			t.Fatal(err)
		}
	}
	sc.fill = blocks.Progress{ChainID: pageChainID, From: recs[0].Height, To: pageHeight, FromTime: recs[0].Time, Complete: true}
	return sc
}

// appHashHex is the app hash of height h as the page writes it: the hex of
// the bytes its base64 stands for.
func appHashHex(h int64) string {
	raw, err := base64.StdEncoding.DecodeString(appHash(h))
	if err != nil {
		panic(err)
	}
	return strings.ToUpper(hex.EncodeToString(raw))
}

// loneCountry is the country of worldPeers' one peer alone in its
// country, far from the others on the map.
const loneCountry = "NZ"

// worldPeers are peers in 40 countries, two in each but
// loneCountry's one: the map's side list is longer than the map.
func worldPeers() []node.Peer {
	capitals := []struct {
		cc       string
		lat, lon float64
	}{
		{"US", 38.9, -77.0},
		{"CA", 45.4, -75.7},
		{"MX", 19.4, -99.1},
		{"BR", -15.8, -47.9},
		{"AR", -34.6, -58.4},
		{"CL", -33.4, -70.6},
		{"PE", -12.0, -77.0},
		{"CO", 4.7, -74.1},
		{"GB", 51.5, -0.1},
		{"FR", 48.9, 2.35},
		{"DE", 52.5, 13.4},
		{"ES", 40.4, -3.7},
		{"IT", 41.9, 12.5},
		{"NL", 52.4, 4.9},
		{"BE", 50.85, 4.35},
		{"CH", 46.9, 7.4},
		{"AT", 48.2, 16.4},
		{"PL", 52.2, 21.0},
		{"SE", 59.3, 18.1},
		{"NO", 59.9, 10.7},
		{"FI", 60.2, 24.9},
		{"DK", 55.7, 12.6},
		{"IE", 53.3, -6.3},
		{"PT", 38.7, -9.1},
		{"GR", 37.98, 23.7},
		{"TR", 39.9, 32.9},
		{"RU", 55.8, 37.6},
		{"UA", 50.45, 30.5},
		{"IN", 28.6, 77.2},
		{"CN", 39.9, 116.4},
		{"JP", 35.7, 139.7},
		{"KR", 37.6, 127.0},
		{"SG", 1.35, 103.8},
		{"ID", -6.2, 106.8},
		{"AU", -35.3, 149.1},
		{"ZA", -25.7, 28.2},
		{"EG", 30.0, 31.2},
		{"NG", 9.1, 7.5},
		{"KE", -1.3, 36.8},
		{loneCountry, -41.3, 174.8},
	}
	seen := pageNow.Format(time.RFC3339)
	var peers []node.Peer
	for i, c := range capitals {
		n := 2
		if c.cc == loneCountry {
			n = 1
		}
		for j := range n {
			moniker := fmt.Sprintf("node-%s-%d", strings.ToLower(c.cc), j)
			ip := fmt.Sprintf("198.51.%d.%d", 100+i, 10+j)
			peers = append(peers, node.Peer{
				Moniker: moniker, RemoteIP: ip, NodeID: nodeID(moniker), P2PAddress: nodeID(moniker) + "@" + ip + ":26656",
				Role: "full", Height: strconv.Itoa(pageHeight), FirstSeen: seen, LastSeen: seen,
				Lat: &c.lat, Lon: &c.lon, Country: c.cc,
			})
		}
	}
	return peers
}

// nextBlock is the healthy network one block later, which moves bravo's
// mean vote delay: the page draws its tables and cards again.
func nextBlock() *node.Snapshot {
	snap := healthySnapshot()
	snap.Status.SyncInfo.LatestBlockHeight = strconv.Itoa(pageHeight + 1)
	snap.Consensus.Height = strconv.Itoa(pageHeight + 2)
	sig := snap.Signing.ValidatorSigning[valBravo.address()]
	sig.VoteDelayMs++
	snap.Signing.ValidatorSigning[valBravo.address()] = sig
	return snap
}

// ---- Block ETA chain

// fullHistory is 40 days of blocks: more than the 30-day scenario and
// the margin's look-back need.
const fullHistory int64 = 40 * 24 * 3600 / 5

// haltHeight is where the scenarios schedule a halt: about 8 hours
// ahead.
const haltHeight int64 = 1_250_000

// reachedHeight is a block the chain produced at 06:01:19.
const reachedHeight int64 = 1_240_000

// soonHeight is about 25 minutes ahead, before the halt.
const soonHeight int64 = 1_244_542

// pastHaltHeight is about 11 hours ahead, after the halt.
const pastHaltHeight int64 = 1_252_000

// timedChain is the chain of the ETA cards: blocks of 5 s on average, the
// newest, chainTip, produced at tip.
type timedChain struct{ tip time.Time }

// runningChain produced its newest block 2 s before pageNow.
var runningChain = timedChain{tip: pageNow.Add(-2 * time.Second)}

// at is the time of block h.
func (c timedChain) at(h int64) time.Time {
	swing := func(h int64) float64 {
		return chainSwing.Seconds() * math.Sin(2*math.Pi*float64(h)/chainSwingBlocks)
	}
	secs := float64(h-chainTip)*chainBlockTime.Seconds() + swing(h) - swing(chainTip)
	return c.tip.Add(time.Duration(secs * float64(time.Second))).Round(time.Millisecond)
}

// view is what the tracker knows with the last blocks of history and halt
// scheduled (nil for none); complete tells whether the backfill is done.
func (c timedChain) view(blocks int64, halt *eta.ScheduledHalt, complete bool) eta.View {
	var samples []eta.Sample
	first := (chainTip - blocks + eta.Stride - 1) / eta.Stride * eta.Stride
	for h := first; h <= chainTip; h += eta.Stride {
		samples = append(samples, eta.Sample{Height: h, Time: c.at(h)})
	}
	return eta.View{Model: eta.NewModel(samples), Halt: halt, Complete: complete}
}

// snapshot is the chain's live state: its newest block, the round above it
// and the node's time.
func (c timedChain) snapshot() *node.Snapshot {
	return &node.Snapshot{
		Status: &node.Status{
			NodeInfo: node.NodeInfo{Moniker: valAlpha.moniker, Network: pageChainID},
			SyncInfo: node.SyncInfo{
				LatestBlockHeight: strconv.FormatInt(chainTip, 10),
				LatestBlockTime:   c.tip.Format(time.RFC3339Nano),
			},
		},
		// The round above the newest block: a readable chain has one.
		Consensus: &node.ConsensusState{Height: strconv.FormatInt(chainTip+1, 10), Round: "0", Step: "1"},
		System:    &node.SystemInfo{NodeTime: pageNow.Format(nodeTimeLayout)},
		Timestamp: pageNow,
	}
}

// haltedView is the full history with a halt at haltHeight requiring
// v1.8.0.
func haltedView() eta.View { return runningChain.view(fullHistory, scheduledHalt("v1.8.0"), true) }

// trackerStub stands in for eta.Tracker: the view it gives can change while
// the page polls.
type trackerStub struct {
	mu sync.Mutex
	v  eta.View
}

// View returns the view the page's next poll reads.
func (t *trackerStub) View() eta.View {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.v
}

// set makes v the view the next polls read.
func (t *trackerStub) set(v eta.View) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.v = v
}

// chainScenario is a server on chain c whose tracker starts with view v.
func chainScenario(c timedChain, v eta.View) (*scenario, *trackerStub) {
	tr := &trackerStub{v: v}
	return &scenario{now: pageNow, snap: c.snapshot(), eta: tr}, tr
}

// serveBlockTimes points sc's server's RPC at a node answering
// /blockchain with chain c's block times: the times of the targets already
// reached.
func serveBlockTimes(t *testing.T, sc *scenario, c timedChain) {
	rpc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, err := strconv.ParseInt(r.URL.Query().Get("minHeight"), 10, 64)
		if err != nil {
			http.Error(w, "bad minHeight", http.StatusBadRequest)
			return
		}
		fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"block_metas":[{"header":{"time":%q}}]}}`, c.at(h).Format(time.RFC3339Nano))
	}))
	t.Cleanup(rpc.Close)
	sc.rpcURL = rpc.URL
}

// openChainPage opens path on a server of chain c whose tracker starts with
// view v and whose RPC gives the times of the blocks c produced.
func openChainPage(t *testing.T, p profile, c timedChain, v eta.View, path string, opts ...pageOption) (*pageEnv, *trackerStub) {
	t.Helper()
	sc, tr := chainScenario(c, v)
	serveBlockTimes(t, sc, c)
	return openPage(t, p, sc, append(opts, withURL(path))...), tr
}

// The ETA chain's block times.
const (
	// chainBlockTime is its mean block time.
	chainBlockTime = 5 * time.Second
	// chainSwing is how far its block times drift from the mean, back and
	// forth over chainSwingBlocks (about a day): past estimates miss by
	// minutes, so the ranges have widths and the paces of the scenarios
	// differ.
	chainSwing       = 5 * time.Minute
	chainSwingBlocks = 17_280
)

// chainTip is the ETA chain's newest block.
const chainTip int64 = 1_244_242

// scheduledHalt is the halt the scenarios schedule, first seen two days before
// pageNow.
func scheduledHalt(minVersion string) *eta.ScheduledHalt {
	return &eta.ScheduledHalt{Height: haltHeight, MinVersion: minVersion, FirstSeen: pageNow.Add(-48 * time.Hour)}
}

// farHeight is about 11 hours ahead of the live chain: no lead
// a test sets is met.
const farHeight = pastHaltHeight

// fullView is the live chain's full history, no halt scheduled.
func fullView() eta.View { return runningChain.view(fullHistory, nil, true) }

// openPushChainPage opens path on a server of the live ETA chain whose
// tracker starts with view v, with push on.
func openPushChainPage(t *testing.T, p profile, v eta.View, path string, opts ...pageOption) (*pageEnv, *trackerStub) {
	t.Helper()
	sc, tr := chainScenario(runningChain, v)
	sc.push = true
	serveBlockTimes(t, sc, runningChain)
	return openPage(t, p, sc, append(opts, withURL(path))...), tr
}

//go:build page

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/presence"
)

// Columns of the Peers table, from 1.
const (
	peersColName = iota + 1
	peersColNodeID
	peersColCountry
	peersColProvider
	peersColConnected
	peersColOnline
	peersColRPC
)

// Colours of the peer cards: their status dot's for a peer whose RPC answered
// (one whose RPC did not takes fg2Color), and their border's.
const (
	peersGreen  = "rgb(63, 185, 80)" // --green
	peersBorder = "rgb(48, 54, 61)"  // --border
)

// peersCardKeys are the labels of a peer card's values, in order.
var peersCardKeys = []string{"node id", "country", "provider", "connected", "online 30d", "first seen"}

// peersFormer is a validator out of the set, whose address a peer still
// reports.
var peersFormer = newPageValidator("former", "former", 0)

// peersBravoSentry is a second node reporting bravo's address and key, its
// RPC answering while it catches up, with an advertised host, connected for
// 3 d 4 h.
func peersBravoSentry() node.Peer {
	p := fullPeer("bravo-sentry")
	p.RemoteIP, p.ExternalAddress = "203.0.113.41", "sentry.bravo.example"
	p.RPCURL, p.CatchingUp = "http://203.0.113.41:26657", true
	p.ValAddress, p.ValPubKey = valBravo.address(), valBravo.pubKey().Value
	p.Country, p.Provider = "FR", "OVH"
	p.ConnectedMs = 277_140_000
	return p
}

// peersBareNode is a node known by its name alone: no node id, IP, place,
// RPC or connection time.
func peersBareNode() node.Peer {
	return node.Peer{Moniker: "bare-node", Role: "full", Height: "timeout"}
}

// peersNameless is a node without a name reporting the former validator's
// address, without its key, at an IP whose provider is unknown, connected
// for 2 d; its RPC does not answer.
func peersNameless() node.Peer {
	p := fullPeer("")
	p.NodeID, p.RemoteIP, p.ValAddress = nodeID("nameless"), "203.0.113.42", peersFormer.address()
	p.ConnectedMs = 172_800_000
	return p
}

// peersNetwork is the healthy network whose source also sees bravo's
// sentry, the bare node and the nameless node: seven peers listed, bravo's
// and charlie's nodes going to their validators' rows. sentry-1's RPC
// answers its status without a height. Its presence store holds
// peersPresence's polls.
func peersNetwork() *scenario {
	sc := healthyNetwork()
	for i := range sc.snap.Peers {
		if sc.snap.Peers[i].Moniker == "sentry-1" {
			sc.snap.Peers[i].Height = ""
		}
	}
	sc.snap.Peers = sortedPeers(append(sc.snap.Peers, peersBravoSentry(), peersBareNode(), peersNameless()))
	sc.presence = peersPresence
	return sc
}

// peersPresence stores three polls: the chain's first, on 2026-11-16 at
// 12:00 UTC, listing sentry-1, eu-node, full-norpc and bravo's sentry; then
// two from 2026-11-17 at 09:30 UTC listing sentry-1, eu-node, us-node and the
// nameless node.
func peersPresence(t *testing.T, s *presence.Store) {
	t.Helper()
	peersPolls(t, s, time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC), 1, "sentry-1", "eu-node", "full-norpc", "bravo-sentry")
	peersPolls(t, s, time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC), 2, "sentry-1", "eu-node", "us-node", "nameless")
}

// peersPolls stores n polls of the healthy network's chain, 1 s apart from
// at, each listing the node id of each of names.
func peersPolls(t *testing.T, s *presence.Store, at time.Time, n int, names ...string) {
	t.Helper()
	ids := make([]string, len(names))
	for i, name := range names {
		ids[i] = nodeID(name)
	}
	for i := range n {
		when := at.Add(time.Duration(i) * time.Second)
		if err := s.Record(t.Context(), pageChainID, when, ids); err != nil {
			t.Fatalf("record a poll at %s listing %v: %v", when, names, err)
		}
	}
}

// peersAt is the peer of snap named moniker, for a test to change.
func peersAt(t *testing.T, snap *node.Snapshot, moniker string) *node.Peer {
	t.Helper()
	i := slices.IndexFunc(snap.Peers, func(p node.Peer) bool { return p.Moniker == moniker })
	if i < 0 {
		t.Fatalf("the snapshot has no peer named %q", moniker)
	}
	return &snap.Peers[i]
}

// peersRPCServer serves a peer's RPC and returns its URL. At every path it
// answers a page whose title tells whether that page can reach the window
// that opened it.
func peersRPCServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><title>RPC</title><script>document.title = window.opener ? "opener" : "no opener"</script>`)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// peersCell is the cell of column col of the Peers table's row holding a
// cell copying key: a peer's moniker, or its node id.
func peersCell(key string, col int) string {
	return fmt.Sprintf("%s > td:nth-child(%d)", peerEntry(desktop, key), col)
}

// peersCard is the peer card named name, its source tag aside.
func peersCard(name string) string {
	return peerEntry(phone, name)
}

// peersCardValue is the value of key on the peer card named name.
func peersCardValue(name, key string) string {
	return fmt.Sprintf(`%s .peer-card-k:text-is(%q) + .peer-card-v`, peersCard(name), key)
}

// peersHeight is how the RPC popover writes the healthy network's head.
var peersHeight = strconv.Itoa(pageHeight)

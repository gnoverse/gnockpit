//go:build page

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"

	"github.com/gnoverse/gnockpit/node"
)

// Columns of the Peers table, from 1.
const (
	peersColName = iota + 1
	peersColNodeID
	peersColCountry
	peersColProvider
	peersColPeers
	peersColRPC
)

// Colours of the peer cards: their status dot's, green for a peer whose RPC
// answered, yellow for one whose RPC did not, and their border's.
const (
	peersGreen  = "rgb(63, 185, 80)"  // --green
	peersYellow = "rgb(210, 153, 34)" // --yellow
	peersBorder = "rgb(48, 54, 61)"   // --border
)

// peersCardKeys are the labels of a peer card's values, in order.
var peersCardKeys = []string{"node id", "country", "provider", "peers"}

// peersFormer is a validator out of the set, whose address a peer still
// reports.
var peersFormer = newPageValidator("former", "former", 0)

// peersBravoSentry is a second node reporting bravo's address and key, its
// RPC answering while it catches up, with 12 peers and an advertised host.
func peersBravoSentry() node.Peer {
	p := fullPeer("bravo-sentry")
	p.RemoteIP, p.ExternalAddress = "203.0.113.41", "sentry.bravo.example"
	p.RPCURL, p.CatchingUp, p.NPeers = "http://203.0.113.41:26657", true, 12
	p.ValAddress, p.ValPubKey = valBravo.address(), valBravo.pubKey().Value
	p.Country, p.Provider = "FR", "OVH"
	return p
}

// peersBareNode is a node known by its name alone: no node id, IP, place,
// RPC or peer count.
func peersBareNode() node.Peer {
	return node.Peer{Moniker: "bare-node", Role: "full", Height: "timeout"}
}

// peersNameless is a node without a name reporting the former validator's
// address, without its key, at an IP whose provider is unknown; its RPC
// does not answer.
func peersNameless() node.Peer {
	p := fullPeer("")
	p.NodeID, p.RemoteIP, p.ValAddress = nodeID("nameless"), "203.0.113.42", peersFormer.address()
	return p
}

// peersNetwork is the healthy network whose source also sees bravo's
// sentry, the bare node and the nameless node: seven peers listed, bravo's
// and charlie's nodes going to their validators' rows. sentry-1's RPC
// answers its status without a height.
func peersNetwork() *scenario {
	sc := healthyNetwork()
	for i := range sc.snap.Peers {
		if sc.snap.Peers[i].Moniker == "sentry-1" {
			sc.snap.Peers[i].Height = ""
		}
	}
	sc.snap.Peers = sortedPeers(append(sc.snap.Peers, peersBravoSentry(), peersBareNode(), peersNameless()))
	return sc
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

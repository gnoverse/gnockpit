//go:build page

package web

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/gnoverse/gnockpit/node"
)

// ---- Table

// peersKeys are the sort keys of the Peers table's columns, in order.
var peersKeys = []string{"name", "nodeid", "country", "provider", "peers", "rpc"}

// TestPagePeersTableHead checks the Peers table's header on a desktop: its
// columns, in order, their labels and widths, the name marked as the order.
// Before any data the table says it is loading.
func TestPagePeersTableHead(t *testing.T) {
	sc := healthyNetwork()
	sc.snap = nil
	e := openPage(t, desktop, sc)
	expectTableHead(e, "#peers-card", peersKeys, []string{"Name\u00a0▲", "Node ID", "Country", "Provider", "Peers", "RPC"})
	expectColumnWidths(e, "#peers-card", peersKeys, []float64{22, 25, 15, 18, 10, 10})
	e.must(e.expect("#peers-header").ToHaveText("Peers"))
	e.must(e.expect("#peers-body tr").ToHaveCount(1))
	e.must(e.expect("#peers-body td").ToHaveText("loading..."))
	e.must(e.expect("#peers-body td").ToHaveAttribute("colspan", "6"))
}

// peersRowWant is what a peer's row of the Peers table shows.
type peersRowWant struct {
	key       string // the copy text of a cell of the row: its moniker, or its node id
	name      string // the name cell's text
	highlight string // the name the row highlights; none when empty
	nodeID    string // none when empty: "-"
	// idRows are the node id's popover rows, the validator address and key
	// the peer reports; no popover without them.
	idRows   [][2]string
	country  string // its ISO code; none when empty
	provider string // the provider cell's text
	ip       string // the IP the provider is guessed from; none when empty
	peers    string
	rpc      string // the RPC URL; none when empty
	height   string // the RPC popover's height row
}

// peersNetworkRows are the rows of the Peers table of peersNetwork.
func peersNetworkRows() []peersRowWant {
	none := func(moniker string) peersRowWant {
		return peersRowWant{key: moniker, name: moniker, highlight: moniker, nodeID: nodeID(moniker), peers: "5"}
	}
	eu := none("eu-node")
	eu.country, eu.provider, eu.ip = "DE", node.Provider("Hetzner Online GmbH")+" ⓘ", "198.51.100.21"
	eu.rpc, eu.height = "http://198.51.100.21:26657", peersHeight+" (synced)"
	noRPC := none("full-norpc")
	noRPC.provider, noRPC.ip = "unknown ⓘ", "203.0.113.14"
	sentry := none("sentry-1")
	sentry.name, sentry.provider, sentry.ip = "sentry-1 (source)", "unknown ⓘ", "203.0.113.13"
	sentry.rpc, sentry.height = "http://203.0.113.13:26657", "? (synced)"
	us := none("us-node")
	us.country, us.provider, us.ip = "US", node.Provider("AMAZON-02")+" ⓘ", "198.51.100.22"
	us.rpc, us.height = "http://198.51.100.22:26657", peersHeight+" (synced)"
	return []peersRowWant{
		{key: "bare-node", name: "bare-node", highlight: "bare-node", provider: "—", peers: "-"},
		{
			key: "bravo-sentry", name: "bravo-sentry", highlight: "bravo", nodeID: nodeID("bravo-sentry"),
			idRows:  [][2]string{{"address", valBravo.address()}, {"pubkey", valBravo.pubKey().Value}},
			country: "FR", provider: "OVH ⓘ", ip: "203.0.113.41", peers: "12",
			rpc: "http://203.0.113.41:26657", height: peersHeight + " (syncing)",
		},
		eu, noRPC, sentry, us,
		{
			key: nodeID("nameless"), nodeID: nodeID("nameless"), idRows: [][2]string{{"address", peersFormer.address()}},
			provider: "unknown ⓘ", ip: "203.0.113.42", peers: "-",
		},
	}
}

// TestPagePeersTableRows checks the Peers table on a desktop: the peers no
// validator's row took, in name order, the nameless last, the source's own
// tagged; per peer each cell's text, colour and data. The name highlights
// the validator whose address the peer reports, else the peer's own name;
// the node id keeps its end in view, with a popover of the validator
// address and key the peer reports; the country and provider, the
// provider's IP in a popover, a dash without either; the peer's own peers;
// a ✓ linking its RPC when its RPC answered, its peers and height in a
// popover, "?" for a height its status left out, else a dash.
func TestPagePeersTableRows(t *testing.T) {
	e := openPage(t, desktop, peersNetwork())
	e.must(e.expect("#peers-header").ToHaveText("Peers (7)"))
	e.must(e.expect(peerNameCells).ToHaveText([]string{
		"bare-node", "bravo-sentry", "eu-node", "full-norpc", "sentry-1 (source)", "us-node", "",
	}))
	// bravo's and charlie's nodes show in their validators' rows.
	e.must(e.expect(peerEntry(desktop, "bravo")).ToHaveCount(0))
	e.must(e.expect(peerEntry(desktop, "charlie")).ToHaveCount(0))
	e.must(e.expect("#peers-body tr > td:nth-child(6)").ToHaveCount(7))
	e.must(e.expect("#peers-body tr > td:nth-child(7)").ToHaveCount(0))

	for _, w := range peersNetworkRows() {
		cell := func(col int) playwright.LocatorAssertions { return e.expect(peersCell(w.key, col)) }

		name := cell(peersColName)
		e.must(name.ToHaveText(w.name))
		if w.highlight == "" {
			e.must(name.Not().ToHaveAttribute("data-highlight", regexp.MustCompile(`.*`)))
			e.must(name.Not().ToHaveAttribute("data-copy", regexp.MustCompile(`.*`)))
		} else {
			e.must(name.ToHaveAttribute("data-highlight", w.highlight))
			e.must(name.ToHaveAttribute("data-copy", strings.TrimSuffix(w.name, " (source)")))
		}
		if strings.HasSuffix(w.name, " (source)") {
			e.must(e.expect(peersCell(w.key, peersColName)+" > .source-tag").ToHaveCSS("color", blueColor))
		}

		id := cell(peersColNodeID)
		e.must(id.ToHaveCSS("font-size", "11px"))
		e.must(id.ToHaveCSS("color", fg2Color))
		if w.highlight == "" {
			e.must(id.Not().ToHaveAttribute("data-highlight", regexp.MustCompile(`.*`)))
		} else {
			e.must(id.ToHaveAttribute("data-highlight", w.highlight))
		}
		switch {
		case w.nodeID == "":
			e.must(id.ToHaveText("-"))
			e.must(id.Not().ToHaveAttribute("data-copy", regexp.MustCompile(`.*`)))
		default:
			idCell := peersCell(w.key, peersColNodeID)
			expectMidTrunc(e, idCell, w.nodeID)
			e.must(id.ToHaveAttribute("data-copy", w.nodeID))
			if w.idRows == nil {
				e.must(id.Not().ToHaveAttribute("data-pop-rows", regexp.MustCompile(`.*`)))
			} else if title, rows := popRows(e, idCell); title != "" || !slices.Equal(rows, w.idRows) {
				t.Errorf("%s: node id popover = %q %q, want no title, %q", w.key, title, rows, w.idRows)
			}
		}

		country := cell(peersColCountry)
		if w.country == "" {
			e.must(country.ToHaveText("—"))
			e.must(country.Not().ToHaveAttribute("data-cc", regexp.MustCompile(`.*`)))
		} else {
			e.must(country.ToHaveAttribute("data-cc", w.country))
		}

		provider := cell(peersColProvider)
		e.must(provider.ToHaveText(w.provider))
		if w.ip != "" {
			info := peersCell(w.key, peersColProvider) + " > .info-btn"
			if title, rows := popRows(e, info); title != "" || !slices.Equal(rows, [][2]string{{"guessed from IP", w.ip}}) {
				t.Errorf("%s: provider popover = %q %q, want no title and its IP %s", w.key, title, rows, w.ip)
			}
			e.must(provider.ToHaveCSS("font-size", "11px"))
			e.must(provider.ToHaveCSS("color", fg2Color))
		}
		switch w.provider {
		case "—":
			e.must(provider.Not().ToHaveAttribute("data-copy", regexp.MustCompile(`.*`)))
		case "unknown ⓘ":
			e.must(provider.ToHaveAttribute("data-copy", w.ip))
			e.must(provider.Not().ToHaveAttribute("data-prov", regexp.MustCompile(`.*`)))
		default:
			p := strings.TrimSuffix(w.provider, " ⓘ")
			e.must(provider.ToHaveAttribute("data-copy", p))
			e.must(provider.ToHaveAttribute("data-prov", p))
		}

		peers := cell(peersColPeers)
		e.must(peers.ToHaveText(w.peers))
		e.must(peers.ToHaveClass("full-role"))
		e.must(peers.ToHaveCSS("color", fg2Color))

		rpc := cell(peersColRPC)
		check := e.expect(peersCell(w.key, peersColRPC) + " > .info-btn")
		if w.rpc == "" {
			e.must(rpc.ToHaveText("-"))
			e.must(rpc.ToHaveClass("full-role"))
			e.must(check.ToHaveCount(0))
			continue
		}
		// A link to its RPC's status, in a new tab that cannot reach the
		// dashboard.
		e.must(rpc.ToHaveText("✓"))
		e.must(check.ToHaveJSProperty("tagName", "A"))
		e.must(check.ToHaveClass("ok info-btn"))
		e.must(check.ToHaveAttribute("href", w.rpc+"/status"))
		e.must(check.ToHaveAttribute("target", "_blank"))
		e.must(check.ToHaveAttribute("rel", "noopener"))
		e.must(check.ToHaveCSS("text-decoration-line", "none"))
		e.must(check.ToHaveCSS("cursor", "pointer"))
		title, rows := popRows(e, peersCell(w.key, peersColRPC)+" > .info-btn")
		if want := [][2]string{{"peers", w.peers}, {"height", w.height}}; title != w.rpc || !slices.Equal(rows, want) {
			t.Errorf("%s: RPC popover = %q %q, want %q %q", w.key, title, rows, w.rpc, want)
		}
	}
}

// TestPagePeersPopovers checks the popovers of a peer's row as the pointer
// shows them: the node id's, with the validator address and key the peer
// reports; the provider's, with the IP it is guessed from; the RPC ✓'s,
// titled by the RPC's URL, with the peer's peers and height.
func TestPagePeersPopovers(t *testing.T) {
	e := openPage(t, desktop, peersNetwork())
	sentry := peersBravoSentry()
	for _, tc := range []struct {
		target, title string
		rows          [][2]string
	}{
		{peersCell("bravo-sentry", peersColNodeID), "", [][2]string{{"address", valBravo.address()}, {"pubkey", valBravo.pubKey().Value}}},
		{peersCell("bravo-sentry", peersColProvider) + " > .info-btn", "", [][2]string{{"guessed from IP", sentry.RemoteIP}}},
		{peersCell("bravo-sentry", peersColRPC) + " > .info-btn", sentry.RPCURL, [][2]string{{"peers", "12"}, {"height", peersHeight + " (syncing)"}}},
	} {
		e.showPopover(e.Page.Locator(tc.target))
		e.must(e.expect(pagePopover).ToHaveClass(regexp.MustCompile(`\brows\b`)))
		if tc.title == "" {
			e.must(e.expect(pagePopover + " .t").ToHaveCount(0))
		} else {
			e.must(e.expect(pagePopover + " .t").ToHaveText(tc.title))
		}
		labels, values := make([]string, len(tc.rows)), make([]string, len(tc.rows))
		for i, r := range tc.rows {
			labels[i], values[i] = r[0], r[1]
		}
		e.must(e.expect(pagePopover + " td.l").ToHaveText(labels))
		e.must(e.expect(pagePopover + " td.v").ToHaveText(values))
	}
}

// TestPagePeersUnknownPeerCount checks that a peer whose RPC answered
// without its peer count shows "-" for it in its row and in its RPC
// popover alike: the server leaves out a count it did not read, and a count
// of 0.
func TestPagePeersUnknownPeerCount(t *testing.T) {
	sc := healthyNetwork()
	peersAt(t, sc.snap, "eu-node").NPeers = 0
	e := openPage(t, desktop, sc)
	e.must(e.expect(peersCell("eu-node", peersColPeers)).ToHaveText("-"))
	if _, rows := popRows(e, peersCell("eu-node", peersColRPC)+" > .info-btn"); !slices.Equal(rows, [][2]string{{"peers", "-"}, {"height", peersHeight + " (synced)"}}) {
		t.Errorf("RPC popover rows = %q, want peers - and the height", rows)
	}
}

// TestPagePeersOpenRPC checks that a peer's RPC ✓ is a link to its RPC's
// status, as its card's RPC line: a click, Enter on it focused from the
// keyboard, which shows its popover, and a middle click each open the
// status in a new tab, which cannot reach the dashboard.
func TestPagePeersOpenRPC(t *testing.T) {
	rpc := peersRPCServer(t)
	sc := healthyNetwork()
	peersAt(t, sc.snap, "eu-node").RPCURL = rpc
	e := openPage(t, desktop, sc)
	check := e.Page.Locator(peersCell("eu-node", peersColRPC) + " > .info-btn")
	for _, tc := range []struct {
		name string
		open func() error
	}{
		{"click", func() error { return check.Click() }},
		{"Enter", func() error {
			focusByKeyboard(e, peersCell("eu-node", peersColRPC)+" > .info-btn")
			e.must(e.expect(pagePopover + " .t").ToHaveText(rpc))
			return e.Page.Keyboard().Press("Enter")
		}},
		{"middle click", func() error {
			return check.Click(playwright.LocatorClickOptions{Button: playwright.MouseButtonMiddle})
		}},
	} {
		opened, err := e.Ctx.ExpectPage(tc.open)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		e.must(e.Expect.Page(opened).ToHaveURL(rpc + "/status"))
		e.must(e.Expect.Page(opened).ToHaveTitle("no opener"))
	}
}

// TestPagePeersOpenRPCOnATouchScreen checks a tap on a peer's RPC ✓ on a
// touch screen wide enough for the Peers table, a large iPhone held
// sideways: it opens the RPC's status in a new tab, which cannot reach the
// dashboard, and shows the ✓'s popover.
func TestPagePeersOpenRPCOnATouchScreen(t *testing.T) {
	rpc := peersRPCServer(t)
	sc := healthyNetwork()
	peersAt(t, sc.snap, "eu-node").RPCURL = rpc
	e := openPage(t, profile{name: "landscape", browser: webkit, device: pageLandscapeIPhone}, sc)
	check := e.Page.Locator(peersCell("eu-node", peersColRPC) + " > .info-btn")
	e.must(check.ScrollIntoViewIfNeeded())
	e.must(e.Expect.Locator(check).ToBeVisible())
	opened, err := e.Ctx.ExpectPage(func() error { return check.Tap() })
	e.must(err)
	e.must(e.Expect.Page(opened).ToHaveURL(rpc + "/status"))
	e.must(e.Expect.Page(opened).ToHaveTitle("no opener"))
	e.must(e.expect(pagePopover).ToBeVisible())
	e.must(e.expect(pagePopover + " .t").ToHaveText(rpc))
}

// TestPagePeersRPCCheckColour checks that a peer's RPC ✓ shows in the green
// its card's RPC line shows, at full strength; the pointer on it turns it
// blue, as on any ⓘ.
func TestPagePeersRPCCheckColour(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	check := peersCell("eu-node", peersColRPC) + " > .info-btn"
	e.must(e.expect(check).ToHaveCSS("color", peersGreen))
	e.must(e.expect(check).ToHaveCSS("opacity", "1"))
	e.must(e.Page.Locator(check).Hover())
	e.must(e.expect(check).ToHaveCSS("color", blueColor))
}

// ---- Cards

// peersCardWant is what a peer's card shows.
type peersCardWant struct {
	name      string // its name, as the card shows it
	reachable bool   // its RPC answered
	values    []string
	rpc       string // the RPC URL; none when empty
}

// peersNetworkCards are the cards of the peers of peersNetwork.
func peersNetworkCards() []peersCardWant {
	short := func(name string) string { return shortAddr(nodeID(name)) }
	return []peersCardWant{
		{name: "bare-node", values: []string{"-", "-", "-", "-"}},
		{name: "bravo-sentry", reachable: true, values: []string{short("bravo-sentry"), "🇫🇷 France", "OVH", "12"}, rpc: "http://203.0.113.41:26657"},
		{name: "eu-node", reachable: true, values: []string{short("eu-node"), "🇩🇪 Germany", node.Provider("Hetzner Online GmbH"), "5"}, rpc: "http://198.51.100.21:26657"},
		{name: "full-norpc", values: []string{short("full-norpc"), "-", "-", "5"}},
		{name: "sentry-1", reachable: true, values: []string{short("sentry-1"), "-", "-", "5"}, rpc: "http://203.0.113.13:26657"},
		{name: "us-node", reachable: true, values: []string{short("us-node"), "🇺🇸 United States", node.Provider("AMAZON-02"), "5"}, rpc: "http://198.51.100.22:26657"},
		// Without a name, the address it reports, shortened.
		{name: shortAddr(peersFormer.address()), values: []string{short("nameless"), "-", "-", "-"}},
	}
}

// TestPagePeersCards checks the peer cards on a phone, per peer: a dot,
// green when its RPC answered, yellow when not, before its name, the
// source's own tagged; reachable or unreachable beside it; then, label
// beside value in grey, its node id shortened, its country, provider and
// peers, a dash for each it lacks; under them its RPC, its URL in green
// with a blue arrow, or a grey dash with a faint arrow.
func TestPagePeersCards(t *testing.T) {
	e := openPage(t, phone, peersNetwork())
	e.must(e.expect("#peers-cards .peer-card").ToHaveCount(7))
	for _, w := range peersNetworkCards() {
		card := peersCard(w.name)
		e.must(e.expect(card).ToHaveCount(1))
		e.must(e.expect(card + " .peer-card-k").ToHaveText(peersCardKeys))
		for i, key := range peersCardKeys {
			e.must(e.expect(peersCardValue(w.name, key)).ToHaveText(w.values[i]))
			e.must(e.expect(peersCardValue(w.name, key)).ToHaveCSS("color", fg2Color))
		}

		dot := e.expect(card + " .peer-card-name > .dot:first-child")
		e.must(dot.ToHaveCSS("width", "7px"))
		e.must(dot.ToHaveCSS("height", "7px"))
		e.must(dot.ToHaveCSS("border-radius", "50%"))
		health := e.expect(card + " .peer-card-top > span:not(.peer-card-name)")
		e.must(health.ToHaveCSS("font-size", "11px"))
		rpc, hint := e.expect(card+" .peer-card-rpc-val"), e.expect(card+" .peer-card-hint")
		e.must(e.expect(card + " .peer-card-rpc-label").ToHaveText("rpc"))
		e.must(hint.ToHaveText("↗"))
		if w.reachable {
			e.must(e.expect(card).ToHaveClass("peer-card has-rpc"))
			e.must(dot.ToHaveCSS("background-color", peersGreen))
			e.must(health.ToHaveText("reachable"))
			e.must(health.ToHaveClass("ok"))
			e.must(health.ToHaveCSS("color", peersGreen))
			e.must(rpc.ToHaveText("✓ " + w.rpc))
			e.must(rpc.ToHaveCSS("color", peersGreen))
			e.must(hint.ToHaveCSS("color", blueColor))
			e.must(hint.ToHaveCSS("opacity", "1"))
			continue
		}
		e.must(e.expect(card).ToHaveClass("peer-card no-rpc"))
		e.must(dot.ToHaveCSS("background-color", peersYellow))
		e.must(health.ToHaveText("unreachable"))
		e.must(health.Not().ToHaveAttribute("class", regexp.MustCompile(`.*`)))
		e.must(health.ToHaveCSS("color", fgColor))
		e.must(rpc.ToHaveText("—"))
		e.must(rpc.ToHaveCSS("color", fg2Color))
		e.must(hint.ToHaveCSS("color", fg2Color))
		e.must(hint.ToHaveCSS("opacity", "0.3"))
	}
	e.must(e.expect(peersCard("sentry-1") + " .peer-card-name").ToHaveText("sentry-1 (source)"))
	e.must(e.expect(peersCard("sentry-1")+" .source-tag").ToHaveCSS("color", blueColor))
	e.must(e.expect("#peers-cards .source-tag").ToHaveCount(1))
}

// TestPagePeersCardHover checks that the pointer on a peer's card whose RPC
// answered draws its border in blue, and on another's leaves it grey.
func TestPagePeersCardHover(t *testing.T) {
	e := openPage(t, tablet, healthyNetwork())
	rpc := e.Page.Locator(peersCard("eu-node"))
	noRPC := e.Page.Locator(peersCard("full-norpc"))
	e.must(e.Expect.Locator(rpc).ToHaveCSS("border-top-color", peersBorder))
	e.must(rpc.Hover())
	e.must(e.Expect.Locator(rpc).ToHaveCSS("border-top-color", blueColor))
	e.must(noRPC.Hover())
	// The border eases over 0.15 s: both cards' transitions start on the one
	// pointer move, so once the first card is grey again the second's has
	// ended too.
	e.must(e.Expect.Locator(rpc).ToHaveCSS("border-top-color", peersBorder))
	e.must(e.Expect.Locator(noRPC).ToHaveCSS("border-top-color", peersBorder))
}

// TestPagePeersCardCopy checks that a click on a peer's card copies the
// peer: its name, the validator address and key it reports, its IP, which
// the card does not show, its node id and its RPC, a line each; a peer
// without some of them, the lines it has. On a phone the toast says so.
func TestPagePeersCardCopy(t *testing.T) {
	onProfiles(t, []profile{tablet, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, peersNetwork(), withInstalledClock())
		e.runFor(time.Second)
		for _, tc := range []struct {
			name  string
			lines []string
		}{
			{"bravo-sentry", []string{
				"bravo-sentry",
				"address: " + valBravo.address(),
				"pubkey: " + valBravo.pubKey().Value,
				"ip: sentry.bravo.example",
				"node id: " + nodeID("bravo-sentry"),
				"rpc: http://203.0.113.41:26657",
			}},
			{"full-norpc", []string{"full-norpc", "ip: 203.0.113.14", "node id: " + nodeID("full-norpc")}},
		} {
			card := e.Page.Locator(peersCard(tc.name))
			e.must(card.ScrollIntoViewIfNeeded())
			e.press(card.Locator(".peer-card-k").First())
			expectToast(e, "Copied!")
			if p == tablet {
				if got, want := e.clipboard(), strings.Join(tc.lines, "\n"); got != want {
					t.Errorf("%s: clipboard = %q, want %q", tc.name, got, want)
				}
			}
			fadeToast(e)
		}
	})
}

// TestPagePeersNamelessCardCopy checks that a click on the card of a peer
// without a name, or a validator address to name it by, copies its lines
// without an empty one for the name.
func TestPagePeersNamelessCardCopy(t *testing.T) {
	sc := healthyNetwork()
	nameless := fullPeer("")
	nameless.NodeID, nameless.RemoteIP = nodeID("nameless"), "203.0.113.42"
	sc.snap.Peers = sortedPeers(append(sc.snap.Peers, nameless))
	e := openPage(t, tablet, sc, withInstalledClock())
	e.runFor(time.Second)
	card := fmt.Sprintf(`#peers-cards .peer-card:has(.peer-card-v:text-is(%q))`, shortAddr(nameless.NodeID))
	e.must(e.expect(card + " .peer-card-name").ToHaveText(""))
	e.must(e.Page.Locator(card + " .peer-card-k").First().Click())
	expectToast(e, "Copied!")
	if got, want := e.clipboard(), "ip: 203.0.113.42\nnode id: "+nameless.NodeID; got != want {
		t.Errorf("clipboard = %q, want %q", got, want)
	}
}

// TestPagePeersCardOpensRPC checks the RPC line of a peer's card: a click on
// it, when the peer's RPC answered, opens the RPC's status in a new tab,
// which cannot reach the dashboard, and copies nothing; when the RPC did
// not answer, the line does nothing.
func TestPagePeersCardOpensRPC(t *testing.T) {
	onProfiles(t, []profile{tablet, phone}, func(t *testing.T, p profile) {
		rpc := peersRPCServer(t)
		sc := healthyNetwork()
		peersAt(t, sc.snap, "eu-node").RPCURL = rpc
		e := openPage(t, p, sc, withInstalledClock())
		e.runFor(time.Second)
		// A copy, for the clicks below to leave as it is.
		e.press(e.Page.Locator(peersCard("us-node") + " .peer-card-k").First())
		expectToast(e, "Copied!")
		copied := ""
		if p == tablet {
			copied = e.clipboard()
		}
		fadeToast(e)

		noRPC := e.Page.Locator(peersCard("full-norpc") + " .peer-card-footer")
		e.must(noRPC.ScrollIntoViewIfNeeded())
		e.press(noRPC)
		footer := e.Page.Locator(peersCard("eu-node") + " .peer-card-footer")
		e.must(e.Expect.Locator(footer).ToHaveCSS("cursor", "pointer"))
		e.must(footer.ScrollIntoViewIfNeeded())
		opened, err := e.Ctx.ExpectPage(func() error {
			e.press(footer)
			return nil
		})
		e.must(err)
		e.must(e.Expect.Page(opened).ToHaveURL(rpc + "/status"))
		e.must(e.Expect.Page(opened).ToHaveTitle("no opener"))
		// The dashboard and the one tab: the line without an RPC opened none.
		if n := len(e.Ctx.Pages()); n != 2 {
			t.Errorf("%d pages open, want the dashboard and the RPC's status", n)
		}
		e.must(e.expect(".copy-toast").ToHaveAttribute("style", toastClear))
		if p == tablet {
			if got := e.clipboard(); got != copied {
				t.Errorf("after the clicks on the RPC lines: clipboard = %q, want it unchanged (%q)", got, copied)
			}
		}
	})
}

// TestPagePeersCardRPCFromTheKeyboard checks that a peer card's RPC line is
// a link the keyboard reaches: Enter on it opens the RPC's status in a new
// tab, which cannot reach the dashboard. The line of a peer whose RPC did
// not answer is no link.
func TestPagePeersCardRPCFromTheKeyboard(t *testing.T) {
	rpc := peersRPCServer(t)
	sc := healthyNetwork()
	peersAt(t, sc.snap, "eu-node").RPCURL = rpc
	e := openPage(t, tablet, sc)
	footer := peersCard("eu-node") + " .peer-card-footer"
	focusByKeyboard(e, footer)
	opened, err := e.Ctx.ExpectPage(func() error { return e.Page.Keyboard().Press("Enter") })
	e.must(err)
	e.must(e.Expect.Page(opened).ToHaveURL(rpc + "/status"))
	e.must(e.Expect.Page(opened).ToHaveTitle("no opener"))
	e.must(e.expect(footer).ToHaveCSS("text-decoration-line", "none"))
	e.must(e.expect(peersCard("full-norpc")+" .peer-card-footer").ToHaveJSProperty("tagName", "DIV"))
}

// ---- Markup

// TestPagePeersMarkupStaysText checks that a peer's name, node id, provider
// and RPC URL carrying markup show as text, in the table, its popover and
// on the cards: nothing of it becomes an element.
func TestPagePeersMarkupStaysText(t *testing.T) {
	const (
		name     = `<i>mal</i><img src="/x">`
		id       = `<s>id</s><img src="/z">`
		provider = `<b>ISP</b><img src="/y">`
		rpc      = `http://<u>rpc</u><img src="/w">`
	)
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := healthyNetwork()
		mal := fullPeer(name)
		mal.NodeID, mal.RemoteIP, mal.Provider, mal.RPCURL = id, "203.0.113.50", provider, rpc
		sc.snap.Peers = sortedPeers(append(sc.snap.Peers, mal))
		e := openPage(t, p, sc)
		if p == phone {
			e.must(e.expect(peersCard(name)).ToHaveCount(1))
			e.must(e.expect(peersCardValue(name, "provider")).ToHaveText(provider))
			e.must(e.expect(peersCard(name) + " .peer-card-rpc-val").ToHaveText("✓ " + rpc))
			e.must(e.expect("#peers-card i, #peers-card s, #peers-card b, #peers-card u, #peers-card img").ToHaveCount(0))
			return
		}
		e.must(e.expect(peersCell(name, peersColName)).ToHaveText(name))
		e.must(e.expect(peersCell(name, peersColName)).ToHaveAttribute("data-highlight", name))
		e.must(e.expect(peersCell(name, peersColNodeID)).ToHaveText(id))
		e.must(e.expect(peersCell(name, peersColProvider)).ToHaveText(provider + " ⓘ"))
		e.must(e.expect(peersCell(name, peersColProvider)).ToHaveAttribute("data-prov", provider))
		e.showPopover(e.Page.Locator(peersCell(name, peersColRPC) + " > .info-btn"))
		e.must(e.expect(pagePopover + " .t").ToHaveText(rpc))
		e.must(e.expect("#peers-card i, #peers-card s, #peers-card b, #peers-card u, #peers-card img").ToHaveCount(0))
		e.must(e.expect(pagePopover + " u, " + pagePopover + " img").ToHaveCount(0))
	})
}

// TestPagePeersReportedAddressIsData checks a peer reporting a validator
// address that names a property of a plain object, "constructor" or
// "__proto__": no validator has it, and the peer's row highlights by its own
// name.
func TestPagePeersReportedAddressIsData(t *testing.T) {
	for _, addr := range []string{"constructor", "__proto__"} {
		t.Run(addr, func(t *testing.T) {
			sc := healthyNetwork()
			odd := fullPeer("odd")
			odd.ValAddress = addr
			sc.snap.Peers = sortedPeers(append(sc.snap.Peers, odd))
			e := openPage(t, desktop, sc)
			e.must(e.expect(peersCell("odd", peersColName)).ToHaveAttribute("data-highlight", "odd"))
		})
	}
}

// ---- Which peers

// TestPagePeersHiddenSources checks the Peers list with --hide-sources: the
// source's own nodes leave the table, the cards and the count; with only
// sources left, the list says there are no peers. Without the flag they are
// listed, tagged.
func TestPagePeersHiddenSources(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		t.Run("shown", func(t *testing.T) {
			e := openPage(t, p, healthyNetwork())
			e.must(e.expect("#peers-header").ToHaveText("Peers (4)"))
			expectPeerNames(e, p, "eu-node", "full-norpc", "sentry-1 (source)", "us-node")
		})
		t.Run("hidden", func(t *testing.T) {
			sc := healthyNetwork()
			sc.hideSources = true
			e := openPage(t, p, sc)
			e.must(e.expect("#peers-header").ToHaveText("Peers (3)"))
			expectPeerNames(e, p, "eu-node", "full-norpc", "us-node")
		})
		t.Run("only sources", func(t *testing.T) {
			sc := healthyNetwork()
			sc.hideSources = true
			sc.snap.Peers = slices.DeleteFunc(sc.snap.Peers, func(peer node.Peer) bool { return peer.ValAddress == "" && !peer.Source })
			e := openPage(t, p, sc)
			e.must(e.expect("#peers-header").ToHaveText("Peers (0)"))
			if p == phone {
				e.must(e.expect("#peers-cards").ToHaveText("no peers"))
				e.must(e.expect("#peers-cards .peer-card").ToHaveCount(0))
				return
			}
			e.must(e.expect("#peers-body tr").ToHaveCount(1))
			e.must(e.expect("#peers-body").ToHaveText("no peers"))
		})
	})
}

// TestPagePeersKeepNamesakesAndNameless checks the Peers list beside a
// validator whose node is named like it, and one whose node has no name: a
// full node named like the first validator, and the nameless peers, each
// keep a row and a card of their own, with their own data; the validators'
// nodes alone leave the list.
func TestPagePeersKeepNamesakesAndNameless(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		charlie := validatorNode(valCharlie, "203.0.113.12", "JP", "")
		charlie.Moniker = ""
		sc := specNetwork(
			valSpec{val: valAlpha},
			valSpec{val: valBravo, node: validatorNode(valBravo, "203.0.113.11", "DE", "")},
			valSpec{val: valCharlie, node: charlie},
		)
		namesake := fullPeer("bravo")
		namesake.NodeID, namesake.Country, namesake.NPeers = nodeID("bravo-namesake"), "FR", 8
		nameless := []node.Peer{fullPeer(""), fullPeer("")}
		for i := range nameless {
			nameless[i].NodeID = nodeID(fmt.Sprintf("nameless-%d", i))
			nameless[i].NPeers = 3 + i
		}
		// One order the server can send: by name, the namesake after
		// bravo's node.
		sc.snap.Peers = sortedPeers(append(append(sc.snap.Peers, namesake), nameless...))
		e := openPage(t, p, sc)
		e.must(e.expect("#peers-header").ToHaveText("Peers (7)"))
		if p == phone {
			e.must(e.expect("#peers-cards .peer-card").ToHaveCount(7))
			e.must(e.expect(peersCardValue("bravo", "node id")).ToHaveText(shortAddr(namesake.NodeID)))
			e.must(e.expect(peersCardValue("bravo", "country")).ToHaveText("🇫🇷 France"))
			e.must(e.expect(peersCardValue("bravo", "peers")).ToHaveText("8"))
			for _, n := range nameless {
				card := fmt.Sprintf(`#peers-cards .peer-card:has(.peer-card-v:text-is(%q))`, shortAddr(n.NodeID))
				e.must(e.expect(card).ToHaveCount(1))
				e.must(e.expect(card + ` .peer-card-k:text-is("peers") + .peer-card-v`).ToHaveText(fmt.Sprint(n.NPeers)))
			}
			return
		}
		e.must(e.expect("#peers-body tr").ToHaveCount(7))
		e.must(e.expect(peersCell("bravo", peersColNodeID)).ToHaveAttribute("data-copy", namesake.NodeID))
		e.must(e.expect(peersCell("bravo", peersColCountry)).ToHaveAttribute("data-cc", "FR"))
		e.must(e.expect(peersCell("bravo", peersColPeers)).ToHaveText("8"))
		// bravo's node and charlie's show in their validators' rows only.
		for _, id := range []string{nodeID("bravo"), nodeID("charlie")} {
			e.must(e.expect(peerEntry(desktop, id)).ToHaveCount(0))
		}
		for _, n := range nameless {
			e.must(e.expect(peersCell(n.NodeID, peersColName)).ToHaveText(""))
			e.must(e.expect(peersCell(n.NodeID, peersColPeers)).ToHaveText(fmt.Sprint(n.NPeers)))
		}
	})
}

// ---- Sorting

// peersSortNetwork is the healthy network whose four listed peers each
// column orders differently:
//
//	            node id  country         provider  peers  RPC
//	eu-node     3rd      Germany         Hetzner   9      yes
//	full-norpc  1st      Switzerland     OVH       3      no
//	sentry-1    4th      Australia       Contabo   7      yes
//	us-node     2nd      United States   AWS       12     yes
//
// By ISO code the countries order AU, CH, DE, US, unlike their names.
func peersSortNetwork() *scenario {
	sc := healthyNetwork()
	ids := []string{nodeID("sort-0"), nodeID("sort-1"), nodeID("sort-2"), nodeID("sort-3")}
	slices.Sort(ids)
	for i, p := range sc.snap.Peers {
		peer := &sc.snap.Peers[i]
		switch p.Moniker {
		case "eu-node":
			peer.NodeID, peer.NPeers = ids[2], 9
		case "full-norpc":
			peer.NodeID, peer.Country, peer.Provider, peer.NPeers = ids[0], "CH", "OVH", 3
		case "sentry-1":
			peer.NodeID, peer.Country, peer.Provider, peer.NPeers = ids[3], "AU", "Contabo", 7
		case "us-node":
			peer.NodeID, peer.NPeers = ids[1], 12
		}
	}
	return sc
}

// TestPagePeersSort checks that a click on a header of the Peers table sorts
// its rows by that column, ascending, and a second click descending, its
// header marked; the Validators table keeps its own order. Peers alike in a
// column keep their name order. The Country column sorts by what it shows:
// names at 1280 px, ISO codes in a tablet's narrower table. The cards follow
// the table's order.
func TestPagePeersSort(t *testing.T) {
	const source = "sentry-1 (source)"
	t.Run("table", func(t *testing.T) {
		e := openPage(t, desktop, peersSortNetwork())
		e.must(e.expect(peerNameCells).ToHaveText([]string{"eu-node", "full-norpc", source, "us-node"}))
		e.must(e.expect(euNodeCountry + " .cc-name").ToBeVisible())
		for _, tc := range []struct {
			key       string
			asc, desc []string
		}{
			{"nodeid", []string{"full-norpc", "us-node", "eu-node", source}, []string{source, "eu-node", "us-node", "full-norpc"}},
			{"country", []string{source, "eu-node", "full-norpc", "us-node"}, []string{"us-node", "full-norpc", "eu-node", source}},
			{"provider", []string{"us-node", source, "eu-node", "full-norpc"}, []string{"full-norpc", "eu-node", source, "us-node"}},
			{"peers", []string{"full-norpc", source, "eu-node", "us-node"}, []string{"us-node", "eu-node", source, "full-norpc"}},
			// The three whose RPC answered keep their name order, both ways.
			{"rpc", []string{"full-norpc", "eu-node", source, "us-node"}, []string{"eu-node", source, "us-node", "full-norpc"}},
			{"name", []string{"eu-node", "full-norpc", source, "us-node"}, []string{"us-node", source, "full-norpc", "eu-node"}},
		} {
			sortTable(e, "#peers-card", tc.key, true)
			e.must(e.expect(peerNameCells).ToHaveText(tc.asc))
			sortTable(e, "#peers-card", tc.key, false)
			e.must(e.expect(peerNameCells).ToHaveText(tc.desc))
			expectSorted(e, "#validators-card", "name", true)
		}
	})
	t.Run("codes", func(t *testing.T) {
		e := openPage(t, tablet, peersSortNetwork())
		e.must(e.Page.Locator("#peers-compact").Check())
		e.must(e.expect(euNodeCountry + " .cc-code").ToBeVisible())
		e.must(e.expect(euNodeCountry + " .cc-name").ToBeHidden())
		// AU, CH, DE, US: CH before DE, unlike Switzerland after Germany.
		sortTable(e, "#peers-card", "country", true)
		e.must(e.expect(peerNameCells).ToHaveText([]string{source, "full-norpc", "eu-node", "us-node"}))
		sortTable(e, "#peers-card", "country", false)
		e.must(e.expect(peerNameCells).ToHaveText([]string{"us-node", "eu-node", "full-norpc", source}))
	})
	t.Run("cards", func(t *testing.T) {
		e := openPage(t, tablet, peersSortNetwork())
		e.must(e.expect("#peers-cards .peer-card-name").ToHaveText([]string{"eu-node", "full-norpc", source, "us-node"}))
		e.must(e.Page.Locator("#peers-compact").Check())
		sortTable(e, "#peers-card", "peers", true)
		e.must(e.Page.Locator("#peers-compact").Uncheck())
		e.must(e.expect("#peers-cards").ToBeVisible())
		e.must(e.expect("#peers-cards .peer-card-name").ToHaveText([]string{"full-norpc", source, "eu-node", "us-node"}))
	})
}

// TestPagePeersSortMissingLast checks that a value the Peers table shows as
// missing sorts last, both ways: a peer without a peer count, a node id or
// a name. Peers alike in a column keep their name order, nameless ones
// last.
func TestPagePeersSortMissingLast(t *testing.T) {
	const source = "sentry-1 (source)"
	sc := peersSortNetwork()
	// full-norpc reads no peer count; a nameless peer, with 4 peers and no
	// RPC, has the node id us-node lacks.
	nameless := fullPeer("")
	nameless.NPeers = 4
	for i, p := range sc.snap.Peers {
		switch p.Moniker {
		case "full-norpc":
			sc.snap.Peers[i].NPeers = 0
		case "us-node":
			nameless.NodeID, sc.snap.Peers[i].NodeID = p.NodeID, ""
		}
	}
	sc.snap.Peers = sortedPeers(append(sc.snap.Peers, nameless))
	e := openPage(t, desktop, sc)
	e.must(e.expect(peerNameCells).ToHaveText([]string{"eu-node", "full-norpc", source, "us-node", ""}))
	for _, tc := range []struct {
		key       string
		asc, desc []string
	}{
		{"peers", []string{"", source, "eu-node", "us-node", "full-norpc"}, []string{"us-node", "eu-node", source, "", "full-norpc"}},
		{"nodeid", []string{"full-norpc", "", "eu-node", source, "us-node"}, []string{source, "eu-node", "", "full-norpc", "us-node"}},
		{"rpc", []string{"full-norpc", "", "eu-node", source, "us-node"}, []string{"eu-node", source, "us-node", "full-norpc", ""}},
		{"name", []string{"eu-node", "full-norpc", source, "us-node", ""}, []string{"us-node", source, "full-norpc", "eu-node", ""}},
	} {
		sortTable(e, "#peers-card", tc.key, true)
		e.must(e.expect(peerNameCells).ToHaveText(tc.asc))
		sortTable(e, "#peers-card", tc.key, false)
		e.must(e.expect(peerNameCells).ToHaveText(tc.desc))
	}
}

// ---- Layout

// TestPagePeersCardColumns checks a peer card's grid on a phone: labels
// upper case, with a colon, in two narrow columns, the two value columns
// sharing the rest evenly.
func TestPagePeersCardColumns(t *testing.T) {
	e := openPage(t, phone, healthyNetwork())
	e.must(e.expect("#peers-cards .peer-card-k >> nth=0").ToHaveCSS("text-transform", "uppercase"))
	if got := pseudoContent(e, "#peers-cards .peer-card-k", "::after"); got != `":"` {
		t.Errorf("a card label ends with %s, want a colon", got)
	}
	cols := evalFloats(e, `() => getComputedStyle(document.querySelector('#peers-cards .peer-card-kv')).gridTemplateColumns.split(' ').map(parseFloat)`, nil)
	if len(cols) != 4 || cols[0] != 56 || cols[2] != 56 || math.Abs(cols[1]-cols[3]) > 1 {
		t.Errorf("a card's columns are %v px wide, want four: 56, an even share, 56, an even share", cols)
	}
}

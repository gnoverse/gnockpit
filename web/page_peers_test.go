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
	"github.com/gnoverse/gnockpit/presence"
)

// ---- Table

// peersKeys are the sort keys of the Peers table's columns, in order.
var peersKeys = []string{"name", "nodeid", "country", "provider", "connected", "online", "rpc"}

// TestPagePeersTableHead checks the Peers table's header on a desktop: its
// columns, in order, their labels and widths, the name marked as the order;
// the Connected, Online and RPC headers with the narrower padding of their
// figures. Before any data the table says it is loading.
func TestPagePeersTableHead(t *testing.T) {
	sc := healthyNetwork()
	sc.snap = nil
	e := openPage(t, desktop, sc)
	// A no-break space keeps "(30 d)" whole: "d)" never wraps alone.
	expectTableHead(e, "#peers-card", peersKeys, []string{"Name\u00a0▲", "Node ID", "Country", "Provider", "Connected", "Online (30\u00a0d)", "RPC"})
	expectColumnWidths(e, "#peers-card", peersKeys, []float64{20, 15, 13, 15, 16, 11, 10})
	for _, key := range []string{"connected", "online", "rpc"} {
		expectFigurePadding(e, fmt.Sprintf(`#peers-card th[data-sort=%q]`, key))
	}
	e.must(e.expect("#peers-header").ToHaveText("Peers"))
	e.must(e.expect("#peers-body tr").ToHaveCount(1))
	e.must(e.expect("#peers-body td").ToHaveText("loading..."))
	e.must(e.expect("#peers-body td").ToHaveAttribute("colspan", "7"))
}

// expectFigurePadding checks that each element the selector matches has
// 8 px padding on both sides, the narrower padding of the Connected, Online
// and RPC columns' short figures.
func expectFigurePadding(e *pageEnv, selector string) {
	e.T.Helper()
	e.must(e.expect(selector).ToHaveCSS("padding-left", "8px"))
	e.must(e.expect(selector).ToHaveCSS("padding-right", "8px"))
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
	// connected and online are the Connected and Online cells' texts;
	// firstSeen is the Online popover's first seen row, no popover when
	// empty.
	connected, online, firstSeen string
	rpc                          string // the RPC URL; none when empty: closed
	height                       string // the RPC popover's height row
}

// peersNetworkRows are the rows of the Peers table of peersNetwork.
func peersNetworkRows() []peersRowWant {
	none := func(moniker string) peersRowWant {
		return peersRowWant{key: moniker, name: moniker, highlight: moniker, nodeID: nodeID(moniker)}
	}
	// The chain's first stored poll listed them: they may have been there
	// before.
	const fromStart = "≤ 2026-11-16"
	eu := none("eu-node")
	eu.country, eu.provider, eu.ip = "DE", node.Provider("Hetzner Online GmbH")+" ⓘ", "198.51.100.21"
	eu.connected, eu.online, eu.firstSeen = "5 h 12 min", "100.0 %", fromStart
	eu.rpc, eu.height = "http://198.51.100.21:26657", peersHeight+" (synced)"
	noRPC := none("full-norpc")
	noRPC.provider, noRPC.ip = "unknown ⓘ", "203.0.113.14"
	noRPC.connected, noRPC.online, noRPC.firstSeen = "12 min", "33.3 %", fromStart
	sentry := none("sentry-1")
	sentry.name, sentry.provider, sentry.ip = "sentry-1 (source)", "unknown ⓘ", "203.0.113.13"
	sentry.connected, sentry.online, sentry.firstSeen = "-", "100.0 %", fromStart
	sentry.rpc, sentry.height = "http://203.0.113.13:26657", "? (synced)"
	us := none("us-node")
	us.country, us.provider, us.ip = "US", node.Provider("AMAZON-02")+" ⓘ", "198.51.100.22"
	us.connected, us.online, us.firstSeen = "< 1 min", "66.6 %", "2026-11-17 09:30 UTC"
	us.rpc, us.height = "http://198.51.100.22:26657", peersHeight+" (synced)"
	return []peersRowWant{
		{key: "bare-node", name: "bare-node", highlight: "bare-node", provider: "—", connected: "-", online: "-"},
		{
			key: "bravo-sentry", name: "bravo-sentry", highlight: "bravo", nodeID: nodeID("bravo-sentry"),
			idRows:  [][2]string{{"address", valBravo.address()}, {"pubkey", valBravo.pubKey().Value}},
			country: "FR", provider: "OVH ⓘ", ip: "203.0.113.41",
			connected: "3 d 4 h", online: "33.3 %", firstSeen: fromStart,
			rpc: "http://203.0.113.41:26657", height: peersHeight + " (syncing)",
		},
		eu, noRPC, sentry, us,
		{
			key: nodeID("nameless"), nodeID: nodeID("nameless"), idRows: [][2]string{{"address", peersFormer.address()}},
			provider: "unknown ⓘ", ip: "203.0.113.42",
			connected: "2 d 0 h", online: "66.6 %", firstSeen: "2026-11-17 09:30 UTC",
		},
	}
}

// TestPagePeersTableRows checks the Peers table on a desktop: the peers no
// validator's row took, in name order, the nameless last, the source's own
// tagged; per peer each cell's text, colour and data. The name highlights
// the validator whose address the peer reports, else the peer's own name;
// the node id keeps its end in view, with a popover of the validator
// address and key the peer reports; the country and provider, the
// provider's IP in a popover, a dash without either; how long the peer has
// been connected; its share of the polls, when it was first seen in a
// popover; "open" linking its RPC when its RPC answered, its height in a
// popover, "?" for a height its status left out, else "closed", both at the
// cell's size.
func TestPagePeersTableRows(t *testing.T) {
	e := openPage(t, desktop, peersNetwork())
	e.must(e.expect("#peers-header").ToHaveText("Peers (7)"))
	e.must(e.expect(peerNameCells).ToHaveText([]string{
		"bare-node", "bravo-sentry", "eu-node", "full-norpc", "sentry-1 (source)", "us-node", "",
	}))
	// bravo's and charlie's nodes show in their validators' rows.
	e.must(e.expect(peerEntry(desktop, "bravo")).ToHaveCount(0))
	e.must(e.expect(peerEntry(desktop, "charlie")).ToHaveCount(0))
	e.must(e.expect("#peers-body tr > td:nth-child(7)").ToHaveCount(7))
	e.must(e.expect("#peers-body tr > td:nth-child(8)").ToHaveCount(0))

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

		for _, col := range []int{peersColConnected, peersColOnline, peersColRPC} {
			expectFigurePadding(e, peersCell(w.key, col))
		}

		connected := cell(peersColConnected)
		e.must(connected.ToHaveText(w.connected))
		e.must(connected.ToHaveClass(regexp.MustCompile(`\bfull-role\b`)))
		e.must(connected.ToHaveCSS("color", fg2Color))

		online := cell(peersColOnline)
		e.must(online.ToHaveText(w.online))
		e.must(online.ToHaveClass(regexp.MustCompile(`\bfull-role\b`)))
		e.must(online.ToHaveCSS("color", fg2Color))
		if w.firstSeen == "" {
			e.must(online.Not().ToHaveAttribute("data-pop-rows", regexp.MustCompile(`.*`)))
		} else if title, rows := popRows(e, peersCell(w.key, peersColOnline)); title != "" || !slices.Equal(rows, [][2]string{{"first seen", w.firstSeen}}) {
			t.Errorf("%s: Online popover = %q %q, want no title and first seen %s", w.key, title, rows, w.firstSeen)
		}

		rpc := cell(peersColRPC)
		open := e.expect(peersCell(w.key, peersColRPC) + " > .info-btn")
		if w.rpc == "" {
			e.must(rpc.ToHaveText("closed"))
			e.must(rpc.ToHaveClass(regexp.MustCompile(`\bfull-role\b`)))
			e.must(rpc.ToHaveCSS("font-size", "13px"))
			e.must(open.ToHaveCount(0))
			continue
		}
		// A link to its RPC's status, in a new tab that cannot reach the
		// dashboard, the cell's text as "closed" is.
		e.must(rpc.ToHaveText("open"))
		e.must(open.ToHaveCSS("font-size", "13px"))
		e.must(open.ToHaveCSS("margin-left", "0px"))
		e.must(open.ToHaveJSProperty("tagName", "A"))
		e.must(open.ToHaveClass("ok info-btn"))
		e.must(open.ToHaveAttribute("href", w.rpc+"/status"))
		e.must(open.ToHaveAttribute("target", "_blank"))
		e.must(open.ToHaveAttribute("rel", "noopener"))
		e.must(open.ToHaveCSS("text-decoration-line", "none"))
		e.must(open.ToHaveCSS("cursor", "pointer"))
		title, rows := popRows(e, peersCell(w.key, peersColRPC)+" > .info-btn")
		if want := [][2]string{{"height", w.height}}; title != w.rpc || !slices.Equal(rows, want) {
			t.Errorf("%s: RPC popover = %q %q, want %q %q", w.key, title, rows, w.rpc, want)
		}
	}
}

// TestPagePeersFiguresKeepTheirUnits checks that Connected and Online join
// each number to its unit with a no-break space, in the table and on the
// cards: a wrapping value never leaves a unit alone. The space between two
// units' groups breaks.
func TestPagePeersFiguresKeepTheirUnits(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, peersNetwork())
		for _, tc := range []struct{ peer, connected, online string }{
			{"bravo-sentry", "3\u00a0d 4\u00a0h", "33.3\u00a0%"},
			{"eu-node", "5\u00a0h 12\u00a0min", "100.0\u00a0%"},
			{"full-norpc", "12\u00a0min", "33.3\u00a0%"},
			{"us-node", "<\u00a01\u00a0min", "66.6\u00a0%"},
		} {
			connected, online := peersCell(tc.peer, peersColConnected), peersCell(tc.peer, peersColOnline)
			if p == phone {
				connected, online = peersCardValue(tc.peer, "connected"), peersCardValue(tc.peer, "online 30d")
			}
			e.must(e.expect(connected).ToHaveJSProperty("textContent", tc.connected))
			e.must(e.expect(online).ToHaveJSProperty("textContent", tc.online))
		}
	})
}

// TestPagePeersPopovers checks the popovers of a peer's row as the pointer
// shows them: the node id's, with the validator address and key the peer
// reports; the provider's, with the IP it is guessed from; the Online
// cell's, with when the peer was first seen; the RPC link's, titled by the
// RPC's URL, with the peer's height.
func TestPagePeersPopovers(t *testing.T) {
	e := openPage(t, desktop, peersNetwork())
	sentry := peersBravoSentry()
	for _, tc := range []struct {
		target, title string
		rows          [][2]string
	}{
		{peersCell("bravo-sentry", peersColNodeID), "", [][2]string{{"address", valBravo.address()}, {"pubkey", valBravo.pubKey().Value}}},
		{peersCell("bravo-sentry", peersColProvider) + " > .info-btn", "", [][2]string{{"guessed from IP", sentry.RemoteIP}}},
		{peersCell("us-node", peersColOnline), "", [][2]string{{"first seen", "2026-11-17 09:30 UTC"}}},
		{peersCell("bravo-sentry", peersColRPC) + " > .info-btn", sentry.RPCURL, [][2]string{{"height", peersHeight + " (syncing)"}}},
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

// TestPagePeersOnlineWithoutTheStore checks the Online column while the
// presence store is off: the server sends no peer its presence, and every
// Online cell shows "-", without a popover.
func TestPagePeersOnlineWithoutTheStore(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	online := fmt.Sprintf("#peers-body tr > td:nth-child(%d)", peersColOnline)
	e.must(e.expect(online).ToHaveText([]string{"-", "-", "-", "-"}))
	e.must(e.expect(online + "[data-pop-rows]").ToHaveCount(0))
}

// TestPagePeersOnlineFollowsItsPolls checks that the Online column follows
// the polls the server records: a poll that read the peers counts for each
// peer it listed, and first sees a peer no poll listed before. A poll that
// leaves what the column shows as it is leaves the table as it is, an open
// Online popover on it open.
func TestPagePeersOnlineFollowsItsPolls(t *testing.T) {
	t.Run("records", func(t *testing.T) {
		sc := healthyNetwork()
		sc.presence = func(t *testing.T, s *presence.Store) {
			at := time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)
			peersPolls(t, s, at, 1, "eu-node", "us-node")
			peersPolls(t, s, at.Add(time.Second), 1, "us-node")
		}
		e := openPage(t, desktop, sc)
		e.must(e.expect(peersCell("eu-node", peersColOnline)).ToHaveText("50.0 %"))

		next := healthyAt(pageHeight + 1)
		next.PeersRead = true
		e.publish(next)
		e.must(e.expect(peersCell("eu-node", peersColOnline)).ToHaveText("66.6 %"))
		e.must(e.expect(peersCell("full-norpc", peersColOnline)).ToHaveText("33.3 %"))
		if title, rows := popRows(e, peersCell("full-norpc", peersColOnline)); title != "" || !slices.Equal(rows, [][2]string{{"first seen", "2026-11-18 12:00 UTC"}}) {
			t.Errorf("full-norpc's Online popover = %q %q, want no title and first seen 2026-11-18 12:00 UTC", title, rows)
		}
	})
	t.Run("keeps its drawing", func(t *testing.T) {
		sc := healthyNetwork()
		sc.presence = func(t *testing.T, s *presence.Store) {
			at := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
			peersPolls(t, s, at, 99, "bravo", "charlie", "eu-node", "full-norpc", "sentry-1", "us-node")
			peersPolls(t, s, at.Add(99*time.Second), 1)
		}
		e := openPage(t, desktop, sc)
		online := fmt.Sprintf("#peers-body tr > td:nth-child(%d)", peersColOnline)
		e.must(e.expect(online).ToHaveText([]string{"99.0 %", "99.0 %", "99.0 %", "99.0 %"}))
		target := peersCell("eu-node", peersColOnline)
		e.showPopover(e.Page.Locator(target))
		old, err := e.Page.Locator(target).ElementHandle()
		e.must(err)

		// 100 of 101 polls: 99.0 % still.
		next := healthyAt(pageHeight + 1)
		next.PeersRead = true
		e.publish(next)
		eu := nodeID("eu-node")
		figures, err := e.Srv.Presence.Figures(t.Context(), pageChainID, pageNow, []string{eu})
		e.must(err)
		if f := figures[eu]; f.Listed != 100 || f.Polls != 101 {
			t.Fatalf("eu-node listed in %d of %d polls after the publish, want 100 of 101: the publish recorded no poll", f.Listed, f.Polls)
		}
		e.must(e.expect("#ns-consensus").ToContainText(fmt.Sprintf("h=%d ", pageHeight+2)))
		if v, err := old.Evaluate(`(el) => el.isConnected`); err != nil || v != true {
			t.Fatalf("the Online cell is out of the page (%v, %v): the table was drawn again", v, err)
		}
		e.must(e.expect(pagePopover).ToBeVisible())
		e.must(e.expect(target).ToHaveText("99.0 %"))
	})
}

// TestPagePeersOpenRPC checks that a peer's RPC "open" is a link to its
// RPC's status, as its card's RPC line: a click, Enter on it focused from
// the keyboard, which shows its popover, and a middle click each open the
// status in a new tab, which cannot reach the dashboard.
func TestPagePeersOpenRPC(t *testing.T) {
	rpc := peersRPCServer(t)
	sc := healthyNetwork()
	peersAt(t, sc.snap, "eu-node").RPCURL = rpc
	e := openPage(t, desktop, sc)
	open := e.Page.Locator(peersCell("eu-node", peersColRPC) + " > .info-btn")
	for _, tc := range []struct {
		name string
		open func() error
	}{
		{"click", func() error { return open.Click() }},
		{"Enter", func() error {
			focusByKeyboard(e, peersCell("eu-node", peersColRPC)+" > .info-btn")
			e.must(e.expect(pagePopover + " .t").ToHaveText(rpc))
			return e.Page.Keyboard().Press("Enter")
		}},
		{"middle click", func() error {
			return open.Click(playwright.LocatorClickOptions{Button: playwright.MouseButtonMiddle})
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

// TestPagePeersOpenRPCOnATouchScreen checks a tap on a peer's RPC "open" on
// a touch screen wide enough for the Peers table, a large iPhone held
// sideways: it opens the RPC's status in a new tab, which cannot reach the
// dashboard, and shows the link's popover.
func TestPagePeersOpenRPCOnATouchScreen(t *testing.T) {
	rpc := peersRPCServer(t)
	sc := healthyNetwork()
	peersAt(t, sc.snap, "eu-node").RPCURL = rpc
	e := openPage(t, profile{name: "landscape", browser: webkit, device: pageLandscapeIPhone}, sc)
	open := e.Page.Locator(peersCell("eu-node", peersColRPC) + " > .info-btn")
	e.must(open.ScrollIntoViewIfNeeded())
	e.must(e.Expect.Locator(open).ToBeVisible())
	opened, err := e.Ctx.ExpectPage(func() error { return open.Tap() })
	e.must(err)
	e.must(e.Expect.Page(opened).ToHaveURL(rpc + "/status"))
	e.must(e.Expect.Page(opened).ToHaveTitle("no opener"))
	e.must(e.expect(pagePopover).ToBeVisible())
	e.must(e.expect(pagePopover + " .t").ToHaveText(rpc))
}

// TestPagePeersOpenRPCColour checks that a peer's RPC "open" shows in the
// green its card's RPC line shows, at full strength; the pointer on it turns
// it blue, as on any ⓘ.
func TestPagePeersOpenRPCColour(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	open := peersCell("eu-node", peersColRPC) + " > .info-btn"
	e.must(e.expect(open).ToHaveCSS("color", peersGreen))
	e.must(e.expect(open).ToHaveCSS("opacity", "1"))
	e.must(e.Page.Locator(open).Hover())
	e.must(e.expect(open).ToHaveCSS("color", blueColor))
}

// ---- Cards

// peersCardWant is what a peer's card shows.
type peersCardWant struct {
	name   string   // its name, as the card shows it
	values []string // in peersCardKeys' order
	rpc    string   // the RPC URL; none when empty: closed
}

// peersNetworkCards are the cards of the peers of peersNetwork.
func peersNetworkCards() []peersCardWant {
	short := func(name string) string { return shortAddr(nodeID(name)) }
	const fromStart = "≤ 2026-11-16"
	return []peersCardWant{
		{name: "bare-node", values: []string{"-", "-", "-", "-", "-", "-"}},
		{
			name:   "bravo-sentry",
			values: []string{short("bravo-sentry"), "🇫🇷 France", "OVH", "3 d 4 h", "33.3 %", fromStart},
			rpc:    "http://203.0.113.41:26657",
		},
		{
			name:   "eu-node",
			values: []string{short("eu-node"), "🇩🇪 Germany", node.Provider("Hetzner Online GmbH"), "5 h 12 min", "100.0 %", fromStart},
			rpc:    "http://198.51.100.21:26657",
		},
		{name: "full-norpc", values: []string{short("full-norpc"), "-", "-", "12 min", "33.3 %", fromStart}},
		{name: "sentry-1", values: []string{short("sentry-1"), "-", "-", "-", "100.0 %", fromStart}, rpc: "http://203.0.113.13:26657"},
		{
			name:   "us-node",
			values: []string{short("us-node"), "🇺🇸 United States", node.Provider("AMAZON-02"), "< 1 min", "66.6 %", "2026-11-17 09:30 UTC"},
			rpc:    "http://198.51.100.22:26657",
		},
		// Without a name, the address it reports, shortened.
		{name: shortAddr(peersFormer.address()), values: []string{short("nameless"), "-", "-", "2 d 0 h", "66.6 %", "2026-11-17 09:30 UTC"}},
	}
}

// peersExpectCardRows checks the grid of the peer card named name, label
// beside value: node id and country, provider and connected, then online 30d
// alone, nothing else on its row; first seen on a row of its own, its value
// from the first value's column to the end of the row.
func peersExpectCardRows(e *pageEnv, name string) {
	e.T.Helper()
	// The row checks assume a row's grid items share its top: the card grid aligns them at the start.
	key := func(k string) *playwright.Rect {
		return boxOf(e, fmt.Sprintf(`%s .peer-card-k:text-is(%q)`, peersCard(name), k))
	}
	first := key("node id")
	above := math.Inf(-1)
	for _, row := range [][]string{{"node id", "country"}, {"provider", "connected"}, {"online 30d"}, {"first seen"}} {
		lead := key(row[0])
		if math.Abs(lead.X-first.X) > 1 || lead.Y <= above {
			e.T.Errorf("%s: %q at (%.1f, %.1f), want a row of its own below y %.1f, in the first column (x %.1f)", name, row[0], lead.X, lead.Y, above, first.X)
		}
		for _, k := range row[1:] {
			if b := key(k); math.Abs(b.Y-lead.Y) > 1 || b.X <= lead.X {
				e.T.Errorf("%s: %q at (%.1f, %.1f), want it beside %q at (%.1f, %.1f)", name, k, b.X, b.Y, row[0], lead.X, lead.Y)
			}
		}
		above = lead.Y
	}
	// The labels and values on online 30d's row, in order.
	v, err := e.Page.Locator(peersCard(name)+" .peer-card-kv").Evaluate(`(kv) => {
		const top = (el) => el.getBoundingClientRect().top
		const label = [...kv.children].find((el) => el.matches('.peer-card-k') && el.textContent === 'online 30d')
		return [...kv.children].filter((el) => Math.abs(top(el) - top(label)) <= 1).map((el) => el.textContent).join(' | ')
	}`, nil)
	e.must(err)
	if row, ok := v.(string); !ok || len(strings.Split(row, " | ")) != 2 || !strings.HasPrefix(row, "online 30d | ") {
		e.T.Errorf("%s: online 30d's row holds %v, want its label and value alone", name, v)
	}
	kv := boxOf(e, peersCard(name)+" .peer-card-kv")
	firstValue := boxOf(e, peersCardValue(name, "node id"))
	if v := boxOf(e, peersCardValue(name, "first seen")); math.Abs(v.X-firstValue.X) > 1 || math.Abs(v.X+v.Width-(kv.X+kv.Width)) > 1 {
		e.T.Errorf("%s: first seen's value spans x %.1f to %.1f, want %.1f to the card's %.1f", name, v.X, v.X+v.Width, firstValue.X, kv.X+kv.Width)
	}
}

// TestPagePeersCards checks the peer cards on a phone, per peer: a dot,
// green when its RPC answered, grey when not, before its name, the
// source's own tagged, nothing beside it; then, label beside value in
// grey, its node id shortened, its country, provider, how long it has been
// connected, its share of the polls in pairs, and when it was first seen on
// a row of its own, a dash for each it lacks; under them its RPC, its URL in
// green with a blue arrow, or "closed" in grey with a faint arrow.
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
		peersExpectCardRows(e, w.name)

		dot := e.expect(card + " .peer-card-name > .dot:first-child")
		e.must(dot.ToHaveCSS("width", "7px"))
		e.must(dot.ToHaveCSS("height", "7px"))
		e.must(dot.ToHaveCSS("border-radius", "50%"))
		e.must(e.expect(card + " .peer-card-top > :not(.peer-card-name)").ToHaveCount(0))
		rpc, hint := e.expect(card+" .peer-card-rpc-val"), e.expect(card+" .peer-card-hint")
		e.must(e.expect(card + " .peer-card-rpc-label").ToHaveText("rpc"))
		e.must(hint.ToHaveText("↗"))
		if w.rpc != "" {
			e.must(e.expect(card).ToHaveClass("peer-card has-rpc"))
			e.must(dot.ToHaveCSS("background-color", peersGreen))
			e.must(rpc.ToHaveText(w.rpc))
			e.must(rpc.ToHaveCSS("color", peersGreen))
			e.must(hint.ToHaveCSS("color", blueColor))
			e.must(hint.ToHaveCSS("opacity", "1"))
			continue
		}
		e.must(e.expect(card).ToHaveClass("peer-card no-rpc"))
		e.must(dot.ToHaveCSS("background-color", fg2Color))
		e.must(rpc.ToHaveText("closed"))
		e.must(rpc.ToHaveCSS("color", fg2Color))
		e.must(hint.ToHaveCSS("color", fg2Color))
		e.must(hint.ToHaveCSS("opacity", "0.3"))
	}
	e.must(e.expect(peersCard("sentry-1") + " .peer-card-name").ToHaveText("sentry-1 (source)"))
	e.must(e.expect(peersCard("sentry-1")+" .source-tag").ToHaveCSS("color", blueColor))
	e.must(e.expect("#peers-cards .source-tag").ToHaveCount(1))
}

// TestPagePeersCardValuesWrap checks the peer cards on a tablet, where the
// cards sit two abreast: connected and online 30d wrap rather than clip;
// node id, country, provider and first seen keep to one line.
func TestPagePeersCardValuesWrap(t *testing.T) {
	e := openPage(t, tablet, healthyNetwork())
	first, second := boxOf(e, peersCard("eu-node")), boxOf(e, peersCard("full-norpc"))
	if math.Abs(first.Y-second.Y) > 1 || second.X <= first.X {
		t.Fatalf("eu-node's card at (%.1f, %.1f), full-norpc's at (%.1f, %.1f): want them side by side", first.X, first.Y, second.X, second.Y)
	}
	for _, key := range peersCardKeys {
		want := "nowrap"
		if key == "connected" || key == "online 30d" {
			want = "normal"
		}
		e.must(e.expect(peersCardValue("eu-node", key)).ToHaveCSS("white-space", want))
	}
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
			e.must(e.expect(peersCard(name) + " .peer-card-rpc-val").ToHaveText(rpc))
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
		namesake.NodeID, namesake.Country, namesake.ConnectedMs = nodeID("bravo-namesake"), "FR", (8 * time.Minute).Milliseconds()
		nameless := []node.Peer{fullPeer(""), fullPeer("")}
		for i := range nameless {
			nameless[i].NodeID = nodeID(fmt.Sprintf("nameless-%d", i))
		}
		nameless[0].ConnectedMs, nameless[1].ConnectedMs = 180_000, 240_000
		// How the page writes the nameless peers' connection times.
		namelessConnected := []string{"3 min", "4 min"}
		// One order the server can send: by name, the namesake after
		// bravo's node.
		sc.snap.Peers = sortedPeers(append(append(sc.snap.Peers, namesake), nameless...))
		e := openPage(t, p, sc)
		e.must(e.expect("#peers-header").ToHaveText("Peers (7)"))
		if p == phone {
			e.must(e.expect("#peers-cards .peer-card").ToHaveCount(7))
			e.must(e.expect(peersCardValue("bravo", "node id")).ToHaveText(shortAddr(namesake.NodeID)))
			e.must(e.expect(peersCardValue("bravo", "country")).ToHaveText("🇫🇷 France"))
			e.must(e.expect(peersCardValue("bravo", "connected")).ToHaveText("8 min"))
			for i, n := range nameless {
				card := fmt.Sprintf(`#peers-cards .peer-card:has(.peer-card-v:text-is(%q))`, shortAddr(n.NodeID))
				e.must(e.expect(card).ToHaveCount(1))
				e.must(e.expect(card + ` .peer-card-k:text-is("connected") + .peer-card-v`).ToHaveText(namelessConnected[i]))
			}
			return
		}
		e.must(e.expect("#peers-body tr").ToHaveCount(7))
		e.must(e.expect(peersCell("bravo", peersColNodeID)).ToHaveAttribute("data-copy", namesake.NodeID))
		e.must(e.expect(peersCell("bravo", peersColCountry)).ToHaveAttribute("data-cc", "FR"))
		e.must(e.expect(peersCell("bravo", peersColConnected)).ToHaveText("8 min"))
		// bravo's node and charlie's show in their validators' rows only.
		for _, id := range []string{nodeID("bravo"), nodeID("charlie")} {
			e.must(e.expect(peerEntry(desktop, id)).ToHaveCount(0))
		}
		for i, n := range nameless {
			e.must(e.expect(peersCell(n.NodeID, peersColName)).ToHaveText(""))
			e.must(e.expect(peersCell(n.NodeID, peersColConnected)).ToHaveText(namelessConnected[i]))
		}
	})
}

// ---- Sorting

// peersSortNetwork is the healthy network whose four listed peers each
// column orders differently:
//
//	            node id  country         provider  connected                    online   RPC
//	eu-node     3rd      Germany         Hetzner   5 h 12 min (18 750 000 ms)   50.0 %   yes
//	full-norpc  1st      Switzerland     OVH       3 d 4 h (277 140 000 ms)     75.0 %   no
//	sentry-1    4th      Australia       Contabo   < 1 min (30 000 ms)          100.0 %  yes
//	us-node     2nd      United States   AWS       12 min (779 000 ms)          90.0 %   yes
//
// By ISO code the countries order AU, CH, DE, US, unlike their names. Its
// presence store holds 20 polls: 10 listing all four, 5 full-norpc, sentry-1
// and us-node, 3 sentry-1 and us-node, 2 sentry-1 alone.
func peersSortNetwork() *scenario {
	sc := healthyNetwork()
	// The node ids' names, in the node ids' order.
	names := []string{"sort-0", "sort-1", "sort-2", "sort-3"}
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(nodeID(a), nodeID(b)) })
	eu, noRPC, sentry, us := names[2], names[0], names[3], names[1]
	for i, p := range sc.snap.Peers {
		peer := &sc.snap.Peers[i]
		switch p.Moniker {
		case "eu-node":
			peer.NodeID, peer.ConnectedMs = nodeID(eu), 18_750_000
		case "full-norpc":
			peer.NodeID, peer.Country, peer.Provider, peer.ConnectedMs = nodeID(noRPC), "CH", "OVH", 277_140_000
		case "sentry-1":
			peer.NodeID, peer.Country, peer.Provider, peer.ConnectedMs = nodeID(sentry), "AU", "Contabo", 30_000
		case "us-node":
			peer.NodeID, peer.ConnectedMs = nodeID(us), 779_000
		}
	}
	sc.presence = func(t *testing.T, s *presence.Store) {
		at := time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)
		peersPolls(t, s, at, 10, eu, noRPC, sentry, us)
		peersPolls(t, s, at.Add(10*time.Second), 5, noRPC, sentry, us)
		peersPolls(t, s, at.Add(15*time.Second), 3, sentry, us)
		peersPolls(t, s, at.Add(18*time.Second), 2, sentry)
	}
	return sc
}

// TestPagePeersSortArrowJoinsItsLabel checks that a sorted header's arrow
// follows its label's last character, joined by its no-break space alone:
// white space at the label's end would let the arrow wrap onto a line of its
// own. Both tables, every column, ascending.
func TestPagePeersSortArrowJoinsItsLabel(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	for _, table := range []struct {
		card string
		keys []string
	}{{"#peers-card", peersKeys}, {"#validators-card", validatorsKeys}} {
		for _, key := range table.keys {
			// Both tables are sorted by name from the start.
			if key != "name" {
				sortTable(e, table.card, key, true)
			}
			v, err := e.Page.Locator(fmt.Sprintf(`%s th[data-sort=%q] > button`, table.card, key)).Evaluate(`(b) => b.textContent`, nil)
			e.must(err)
			text, ok := v.(string)
			if !ok {
				t.Fatalf("%s %s: header text is a %T, want a string", table.card, key, v)
			}
			label, joined := strings.CutSuffix(text, "\u00a0▲")
			if !joined || label == "" || strings.TrimRight(label, " \t\n\r\u00a0") != label {
				t.Errorf("%s %s: header text %q, want its label, ending without white space, then \"\\u00a0▲\"", table.card, key, text)
			}
		}
	}
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
			{"connected", []string{source, "us-node", "eu-node", "full-norpc"}, []string{"full-norpc", "eu-node", "us-node", source}},
			{"online", []string{"eu-node", "full-norpc", "us-node", source}, []string{source, "us-node", "full-norpc", "eu-node"}},
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
		sortTable(e, "#peers-card", "connected", true)
		e.must(e.Page.Locator("#peers-compact").Uncheck())
		e.must(e.expect("#peers-cards").ToBeVisible())
		e.must(e.expect("#peers-cards .peer-card-name").ToHaveText([]string{source, "us-node", "eu-node", "full-norpc"}))
	})
}

// TestPagePeersSortByWhatItShows checks that Connected and Online sort by
// what they show. Each pair below differs in exact value but reads the
// same, so its two peers tie and keep their name order, both ways.
// Connected reads whole minutes below a day and whole hours from a day up;
// Online a share to a tenth of a percent, rounded down.
func TestPagePeersSortByWhatItShows(t *testing.T) {
	const source = "sentry-1 (source)"
	sc := healthyNetwork()
	// In each pair the first by name has the longer time and the larger
	// share.
	for _, p := range []struct {
		moniker string
		ms      int64
	}{
		{"eu-node", 18_790_000},    // 313 min 10 s: 5 h 13 min
		{"full-norpc", 18_780_000}, // 313 min: 5 h 13 min
		{"sentry-1", 95_400_000},   // 26 h 30 min: 1 d 2 h
		{"us-node", 93_960_000},    // 26 h 6 min: 1 d 2 h
	} {
		peersAt(t, sc.snap, p.moniker).ConnectedMs = p.ms
	}
	// 2000 polls: eu-node listed in 1001 and full-norpc in 1000, both
	// 50.0 %; sentry-1 in 1601 and us-node in 1600, both 80.0 %.
	sc.presence = func(t *testing.T, s *presence.Store) {
		at := time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)
		peersPolls(t, s, at, 1000, "eu-node", "full-norpc", "sentry-1", "us-node")
		peersPolls(t, s, at.Add(1000*time.Second), 1, "eu-node", "sentry-1")
		peersPolls(t, s, at.Add(1001*time.Second), 600, "sentry-1", "us-node")
		peersPolls(t, s, at.Add(1601*time.Second), 399)
	}
	e := openPage(t, desktop, sc)
	connected := fmt.Sprintf("#peers-body tr > td:nth-child(%d)", peersColConnected)
	online := fmt.Sprintf("#peers-body tr > td:nth-child(%d)", peersColOnline)
	e.must(e.expect(connected).ToHaveText([]string{"5 h 13 min", "5 h 13 min", "1 d 2 h", "1 d 2 h"}))
	e.must(e.expect(online).ToHaveText([]string{"50.0 %", "50.0 %", "80.0 %", "80.0 %"}))
	for _, key := range []string{"connected", "online"} {
		sortTable(e, "#peers-card", key, true)
		e.must(e.expect(peerNameCells).ToHaveText([]string{"eu-node", "full-norpc", source, "us-node"}))
		sortTable(e, "#peers-card", key, false)
		e.must(e.expect(peerNameCells).ToHaveText([]string{source, "us-node", "eu-node", "full-norpc"}))
	}
}

// TestPagePeersSortMissingLast checks that a value the Peers table shows as
// missing sorts last, both ways: a peer without a connection time, a share
// of the polls, a node id or a name. Peers alike in a column keep their
// name order, nameless ones last.
func TestPagePeersSortMissingLast(t *testing.T) {
	const source = "sentry-1 (source)"
	sc := peersSortNetwork()
	// No source reports how long it has been connected to full-norpc; a
	// nameless peer, connected for 4 min, without an RPC, has the node id
	// us-node lacks, and the polls that listed it.
	nameless := fullPeer("")
	nameless.ConnectedMs = 240_000
	for i, p := range sc.snap.Peers {
		switch p.Moniker {
		case "full-norpc":
			sc.snap.Peers[i].ConnectedMs = 0
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
		{"connected", []string{source, "", "us-node", "eu-node", "full-norpc"}, []string{"eu-node", "us-node", "", source, "full-norpc"}},
		{"online", []string{"eu-node", "full-norpc", "", source, "us-node"}, []string{source, "", "full-norpc", "eu-node", "us-node"}},
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

//go:build page

package web

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/node"
)

// ---- Popovers

// TestPagePopoverOnHover checks the popovers of a pointer that hovers: each
// kind of target shows its popover while hovered, the pointer moving into
// the target's own children keeps it, a click on the target leaves it, and
// leaving the target hides it.
func TestPagePopoverOnHover(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	e.must(e.expect("#validators-body tr").ToHaveCount(4))

	for _, tc := range []struct{ name, target, text string }{
		{".info-btn", `#validators-card th[data-sort="address"] > button`, "Validator's consensus (signing) address, derived from its consensus public key"},
		{"[data-popover]", euNodeCountry, "Germany"},
		{"[data-pop-rows]", charlieMissed100, "last 100 blocks"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e.showPopover(e.Page.Locator(tc.target))
			e.must(e.expect(pagePopover).ToContainText(tc.text))
			e.must(e.Page.Mouse().Move(0, 0))
			e.must(e.expect(pagePopover).ToBeHidden())
		})
	}

	t.Run("children", func(t *testing.T) {
		cell := e.Page.Locator(euNodeCountry)
		e.showPopover(cell.Locator(".cc-flag"))
		e.must(e.expect(pagePopover).ToHaveText("Germany"))
		box := boxOf(e, euNodeCountry)
		// The cell's own padding, left of its children.
		e.must(e.Page.Mouse().Move(box.X+2, box.Y+box.Height/2))
		e.must(e.expect(pagePopover).ToBeVisible())
		e.must(cell.Click())
		e.must(e.expect(pagePopover).ToBeVisible())
		e.must(e.Page.Mouse().Move(box.X+box.Width/2, box.Y-40))
		e.must(e.expect(pagePopover).ToBeHidden())
	})
}

// TestPagePopoverOnTap checks the popovers of a touch screen: a tap on a
// target shows its popover, a tap on another target moves it there, a tap on
// the same target or anywhere else hides it.
func TestPagePopoverOnTap(t *testing.T) {
	sc := healthyNetwork()
	sc.snap.Consensus.Proposer = valDelta.address()
	e := openPage(t, phone, sc)
	blockBox := "#recent-blocks-body .info-btn"
	proposer := "#ns-proposer .tap-popover"
	missed := charlieCardMissed100

	e.showPopover(e.Page.Locator(blockBox).Last())
	e.must(e.expect(pagePopover + " .t").ToHaveText(mechBlockTitle))
	e.must(e.Page.Locator(blockBox).Last().Tap())
	e.must(e.expect(pagePopover).ToBeHidden())

	e.showPopover(e.Page.Locator(proposer))
	e.must(e.expect(pagePopover).ToHaveText(valDelta.address()))
	e.showPopover(e.Page.Locator(missed))
	e.must(e.expect(pagePopover + " .t").ToHaveText("last 100 blocks"))
	e.must(e.Page.Locator("h1").Tap())
	e.must(e.expect(pagePopover).ToBeHidden())
}

// TestPagePopoverDescribesItsTarget checks that the popover is a tooltip
// that describes the element it is open for, and only while it is open.
func TestPagePopoverDescribesItsTarget(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		e.must(e.expect(pagePopover).ToHaveAttribute("role", "tooltip"))
		first := e.Page.Locator("#recent-blocks-body .info-btn").First()
		second := e.Page.Locator("#recent-blocks-body .info-btn").Nth(1)
		e.showPopover(first)
		e.must(e.Expect.Locator(first).ToHaveAttribute("aria-describedby", "popover"))
		e.showPopover(second)
		e.must(e.Expect.Locator(second).ToHaveAttribute("aria-describedby", "popover"))
		e.must(e.Expect.Locator(first).Not().ToHaveAttribute("aria-describedby", "popover"))
		e.press(e.Page.Locator("h1"))
		e.must(e.expect(pagePopover).ToBeHidden())
		e.must(e.expect(`[aria-describedby="popover"]`).ToHaveCount(0))
	})
}

// TestPagePopoverContent checks the popover's two forms: the text form keeps
// the target's line breaks within 350 px; the rows form has a title, then
// label and value rows, a label led by its colour swatch when it has one,
// within the screen's width.
func TestPagePopoverContent(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		show := func(target string) {
			t.Helper()
			e.showPopover(e.Page.Locator(target).First())
		}

		blockBox := "#recent-blocks-body .info-btn"
		show(blockBox)
		e.must(e.expect(pagePopover).ToHaveClass(regexp.MustCompile(`\brows\b`)))
		e.must(e.expect(pagePopover).ToHaveCSS("white-space", "normal"))
		e.must(e.expect(pagePopover + " .t").ToHaveText(mechBlockTitle))
		e.must(e.Expect.Locator(e.Page.Locator(pagePopover + " td.l").First()).ToBeVisible())
		vw := mechViewportWidth(e)
		e.must(e.expect(pagePopover).ToHaveCSS("max-width", fmt.Sprintf("%dpx", vw-16)))
		e.press(e.Page.Locator("h1"))
		e.must(e.expect(pagePopover).ToBeHidden())

		// The validator page's info and uptime cells.
		e.press(e.Page.Locator("a.val-link:visible").First())
		e.must(e.expect("#val-modal").ToHaveClass(regexp.MustCompile(`\bopen\b`)))
		info := `#val-body .val-sec[data-section="profile"] h3 .info-btn`
		show(info)
		e.must(e.expect(pagePopover).Not().ToHaveClass(regexp.MustCompile(`\brows\b`)))
		e.must(e.expect(pagePopover).ToHaveText("What: what the operator wrote in its valopers profile, and the realm's state for it\n" +
			"Source: the valopers realm (gno.land/r/gnops/valopers), read every 15 min\n" +
			"Scored: no, shown for monitoring"))
		e.must(e.expect(pagePopover).ToHaveCSS("white-space", "pre-wrap"))
		e.must(e.expect(pagePopover).ToHaveCSS("max-width", "350px"))

		// Today's cell: a day alpha signed throughout.
		day := `#val-body .val-sec[data-section="uptime"] rect[data-pop-rows] >> nth=-1`
		show(day)
		e.must(e.expect(pagePopover + " .t").ToHaveText(regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)))
		swatch := e.Page.Locator(pagePopover + " tr").First().Locator("td.l .sw")
		e.must(e.Expect.Locator(swatch).ToHaveCount(1))
		e.must(e.Expect.Locator(swatch).ToHaveCSS("background-color", "rgb(63, 185, 80)"))
		e.must(e.Expect.Locator(e.Page.Locator(pagePopover + " tr").First().Locator("td.l")).ToHaveText("signed"))
		e.must(e.Expect.Locator(e.Page.Locator(pagePopover + " tr").First().Locator("td.v")).ToHaveText(regexp.MustCompile(` of `)))
		e.must(e.Expect.Locator(e.Page.Locator(pagePopover + " tr").Nth(2).Locator("td.l .sw")).ToHaveCount(0))
	})
}

// TestPagePopoverPlacement checks where a popover opens: 4 px below its
// target, or 4 px above it when there is no room below, its left edge at the
// target's but no closer than 8 px to the screen's right.
func TestPagePopoverPlacement(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	e.must(e.expect("#peers-body tr").ToHaveCount(4))
	hover := func(target string) {
		t.Helper()
		e.showPopover(e.Page.Locator(target))
	}

	apphash := "#d-apphash-last"
	hover(apphash)
	mechExpectPopoverAt(e, apphash, false)

	// The RPC column's popover, wider than the room right of its target.
	first := "#peers-body tr:first-child td:last-child .info-btn"
	e.must(e.Page.Locator(first).ScrollIntoViewIfNeeded())
	hover(first)
	mechExpectPopoverAt(e, first, false)
	if tb, pb := boxOf(e, first), boxOf(e, pagePopover); tb.X+pb.Width+8 <= float64(mechViewportWidth(e)) {
		t.Fatalf("%s: the popover fits right of its target, want a case it does not", first)
	}

	// The same popover, its target scrolled to the screen's bottom edge: no
	// room below, however short the popover.
	if _, err := e.Page.Locator(first).Evaluate(`(el) => el.scrollIntoView({ block: 'end' })`, nil); err != nil {
		t.Fatalf("scroll %s to the screen's bottom edge: %v", first, err)
	}
	hover(first)
	mechExpectPopoverAt(e, first, true)
}

// mechExpectPopoverAt checks that the popover sits where it should for target:
// above it when above is true, below otherwise, in both cases where the
// rule says.
func mechExpectPopoverAt(e *pageEnv, target string, above bool) {
	e.T.Helper()
	tb, pb := boxOf(e, target), boxOf(e, pagePopover)
	vh := float64(mechViewportHeight(e))
	if fits := tb.Y+tb.Height+4+pb.Height <= vh; fits == above {
		e.T.Fatalf("%s: room below = %v, want the case where it is %v", target, fits, !above)
	}
	mechExpectPopoverPlaced(e, target, above)
}

// mechExpectPopoverBeside checks that the popover sits where the rule puts
// it for target, below or above, whichever the room decides.
func mechExpectPopoverBeside(e *pageEnv, target string) {
	e.T.Helper()
	tb, pb := boxOf(e, target), boxOf(e, pagePopover)
	mechExpectPopoverPlaced(e, target, tb.Y+tb.Height+4+pb.Height > float64(mechViewportHeight(e)))
}

// mechExpectPopoverPlaced checks the popover's place for target, above it or
// below it.
func mechExpectPopoverPlaced(e *pageEnv, target string, above bool) {
	e.T.Helper()
	tb, pb := boxOf(e, target), boxOf(e, pagePopover)
	vw := float64(mechViewportWidth(e))
	wantY := tb.Y + tb.Height + 4
	if above {
		wantY = math.Max(4, tb.Y-4-pb.Height)
	}
	wantX := math.Max(4, math.Min(tb.X, vw-pb.Width-8))
	if math.Abs(pb.Y-wantY) > 1 || math.Abs(pb.X-wantX) > 1 {
		e.T.Errorf("popover of %s at (%.1f, %.1f), want (%.1f, %.1f)", target, pb.X, pb.Y, wantX, wantY)
	}
}

// TestPageTableHeaderPopovers checks the explanation each table header's
// popover gives; the name headers have none.
func TestPageTableHeaderPopovers(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	for _, tc := range []struct{ header, text string }{
		{`#validators-card th[data-sort="address"] > button`, "Validator's consensus (signing) address, derived from its consensus public key"},
		{`#validators-card th[data-sort="country"] > button`, "Country of the validator's IP, via its connected peer (DB-IP geolocation). Hover to cross-highlight the network map."},
		{`#validators-card th[data-sort="provider"] > button`, "Cloud provider guessed from the validator's IP, via its connected peer (DB-IP ASN). Hover the ⓘ on a row to see the IP."},
		{`#validators-card th[data-sort="vp"] > button`, "Voting power (stake weight in the validator set)"},
		{`#validators-card th[data-sort="spof"] > button`, "Single point of failure: holds ≥1/3 of total voting power, so losing it drops the rest below the 2/3+1 quorum and would halt consensus"},
		{`#validators-card th[data-sort="missed"] > button`, "Blocks missed in the last 100: absent from the commit, or a vote for another block (a nil vote counts as signed); only the blocks the validator was in the set for count"},
		{`#validators-card th[data-sort="missed24h"] > button`, "Blocks this validator missed in the last 24h: absent from the commit, or a vote for another block (a nil vote counts as signed)"},
		{`#validators-card th[data-sort="delay"] > button`, "Median delay of its precommits from each commit's voting-power-weighted median time, over the last 100 blocks; negative is early. Not scored."},
		{`#validators-card th[data-sort="props"] > button`, "Proposals it missed in the last 24 h, of those it was expected to make: the rounds it was the proposer of until the block was made. Not scored: the ranking scores missed proposals over 30 days (10 %)."},
		{`#peers-card th[data-sort="nodeid"] > button`, "The peer's P2P node ID (bech32). Hover or click to copy the full ID."},
		{`#peers-card th[data-sort="country"] > button`, "Country of the peer's IP (DB-IP geolocation). Hover to cross-highlight the network map."},
		{`#peers-card th[data-sort="provider"] > button`, "Cloud provider guessed from the peer's IP (DB-IP ASN). Hover the ⓘ on a row to see the IP."},
		{`#peers-card th[data-sort="connected"] > button`, "How long the peer has been connected: its longest current connection to one of gnockpit's sources (their /net_info), which gnockpit's own restarts do not reset"},
		{`#peers-card th[data-sort="online"] > button`, "Share of gnockpit's polls over the last 30 days (today and the 29 UTC days before, or since gnockpit began recording) in which a source listed the peer; hover a share for when gnockpit first saw the peer"},
		{`#peers-card th[data-sort="rpc"] > button`, "Whether one of the peer's RPC endpoints gnockpit probes answered: open links its /status (hover for the endpoint and its height), closed otherwise"},
	} {
		e.showPopover(e.Page.Locator(tc.header))
		e.must(e.expect(pagePopover).ToHaveText(tc.text))
		e.must(e.expect(tc.header).ToHaveClass(regexp.MustCompile(`\binfo-btn\b`)))
	}
	for _, name := range []string{`#validators-card th[data-sort="name"]`, `#peers-card th[data-sort="name"]`} {
		e.point(e.Page.Locator(name))
		e.must(e.expect(pagePopover).ToBeHidden())
	}
}

// TestPagePopoverOverDialog checks that a popover opened in the validator
// dialog shows above it, and that closing the dialog hides the popover.
func TestPagePopoverOverDialog(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		e.press(e.Page.Locator("a.val-link:visible").First())
		e.must(e.expect("#val-modal").ToHaveClass(regexp.MustCompile(`\bopen\b`)))
		e.showPopover(e.Page.Locator(`#val-body .val-sec[data-section="uptime"] h3 .info-btn`))
		pb := boxOf(e, pagePopover)
		v, err := e.Page.Evaluate(`([x, y]) => document.getElementById('popover').contains(document.elementFromPoint(x, y))`,
			[]float64{pb.X + pb.Width/2, pb.Y + pb.Height/2})
		e.must(err)
		if v != true {
			t.Errorf("the element at the popover's centre is not the popover: the dialog covers it")
		}

		_, err = e.Page.GoBack()
		e.must(err)
		e.must(e.expect("#val-modal").Not().ToHaveClass(regexp.MustCompile(`\bopen\b`)))
		e.must(e.expect(pagePopover).ToBeHidden())
	})
}

// TestPageInfoButtonsFromKeyboard checks that every ⓘ takes the keyboard
// focus and shows its popover while it holds it: Tab moves it on, leaving
// hides it, and Escape hides it without closing the dialog it sits in.
func TestPageInfoButtonsFromKeyboard(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	e.must(e.Expect.Locator(e.Page.Locator("#recent-blocks-body .info-btn").First()).ToBeAttached())
	// Every ⓘ takes the focus, a button's or a link's by itself; the recent
	// blocks' one by one through their single tab stop.
	e.must(e.expect(".info-btn:not(button):not(a[href]):not([tabindex])").ToHaveCount(0))

	address := `#validators-card th[data-sort="address"] > button`
	e.must(e.expect(address).ToHaveCSS("opacity", "0.5"))
	focusByKeyboard(e, address)
	// Focused, it is drawn at full strength, as hovered.
	e.must(e.expect(address).ToHaveCSS("opacity", "1"))
	e.must(e.expect(pagePopover).ToBeVisible())
	e.must(e.expect(pagePopover).ToHaveText("Validator's consensus (signing) address, derived from its consensus public key"))
	e.must(e.Page.Keyboard().Press("Tab"))
	e.must(e.expect(`#validators-card th[data-sort="country"] > button`).ToBeFocused())
	e.must(e.expect(pagePopover).ToContainText("Country of the validator's IP"))
	e.must(e.Page.Keyboard().Press("Escape"))
	e.must(e.expect(pagePopover).ToBeHidden())
	e.must(e.expect(`#validators-card th[data-sort="country"] > button`).ToBeFocused())
	e.must(e.Page.Keyboard().Press("Tab"))
	e.must(e.expect(pagePopover).ToContainText("Cloud provider guessed"))
	e.must(e.Page.Locator(`#validators-view button[data-view="live"]`).Focus())
	e.must(e.expect(pagePopover).ToBeHidden())

	e.must(e.Page.Locator(`a.val-link:visible:has-text("alpha")`).Click())
	e.must(e.expect("#val-close").ToBeFocused())
	e.must(e.Expect.Locator(e.Page.Locator("#val-body .val-sec").First()).ToBeVisible())
	e.must(e.Page.Keyboard().Press("Tab"))
	e.must(e.Expect.Locator(e.Page.Locator("#val-body .info-btn").First()).ToBeFocused())
	e.must(e.expect(pagePopover).ToBeVisible())
	e.must(e.Page.Keyboard().Press("Escape"))
	e.must(e.expect(pagePopover).ToBeHidden())
	e.must(e.expect("#val-modal").ToHaveClass(regexp.MustCompile(`\bopen\b`)))
	e.must(e.Page.Keyboard().Press("Escape"))
	e.must(e.expect("#val-modal").Not().ToHaveClass(regexp.MustCompile(`\bopen\b`)))
}

// TestPageEscapeUnderAHoveredPopover checks that Escape closes the
// validator dialog at once while the pointer holds a popover open in it, and
// hides that popover.
func TestPageEscapeUnderAHoveredPopover(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	e.must(e.Page.Locator(`a.val-link:visible:has-text("alpha")`).Click())
	e.must(e.Expect.Locator(e.Page.Locator(`#val-body .val-sec[data-section="timeline"]`)).ToBeVisible())
	e.showPopover(e.Page.Locator(`#val-body .val-sec[data-section="uptime"] h3 .info-btn`))
	e.must(e.Page.Keyboard().Press("Escape"))
	e.must(e.expect("#val-modal").Not().ToHaveClass(regexp.MustCompile(`\bopen\b`)))
	e.must(e.expect(pagePopover).ToBeHidden())
}

// TestPageFocusStaysThroughATableRedraw checks that an ⓘ the keyboard
// focused in a table keeps the focus, and its popover, when the page draws
// the table again: the same cell's ⓘ in the new table takes both.
func TestPageFocusStaysThroughATableRedraw(t *testing.T) {
	for _, tc := range []struct{ name, info string }{
		{"validators", `#validators-body tr:has(td[data-copy="bravo"]) td:nth-child(4) .info-btn`},
		{"peers", `#peers-body tr:has(td[data-copy="eu-node"]) td:nth-child(4) .info-btn`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := openPage(t, desktop, healthyNetwork())
			focusByKeyboard(e, tc.info)
			e.must(e.expect(pagePopover + " td.l").ToHaveText("guessed from IP"))
			old, err := e.Page.Locator(tc.info).ElementHandle()
			e.must(err)
			e.publish(nextBlock())
			e.must(e.expect("#ns-consensus").ToContainText(fmt.Sprintf("h=%d ", pageHeight+2)))
			if v, err := old.Evaluate(`(el) => el.isConnected`); err != nil || v != false {
				t.Fatalf("the ⓘ focused before is still in the page (%v, %v): the table was not drawn again", v, err)
			}
			e.must(e.expect(tc.info).ToBeFocused())
			e.must(e.expect(pagePopover).ToBeVisible())
			e.must(e.expect(pagePopover + " td.l").ToHaveText("guessed from IP"))
		})
	}
}

// TestPageFocusStaysThroughACardRedraw checks that a link the keyboard
// focused on a card keeps the focus when the page draws the cards again with
// that card moved in its list: the same card's link in the new list takes
// it, not the link at the card's former place. A validator card's name, and
// a peer card's RPC line.
func TestPageFocusStaysThroughACardRedraw(t *testing.T) {
	for _, tc := range []struct{ name, link string }{
		{"validators", validatorEntryAt(phone, valBravo.address()) + " a.val-link"},
		{"peers", peerEntry(phone, "eu-node") + " a.peer-card-footer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := openPage(t, tablet, healthyNetwork())
			expectValidatorCards(e, "alpha", "bravo", "charlie", shortAddr(valDelta.address()))
			expectPeerNames(e, phone, "eu-node", "full-norpc", "sentry-1 (source)", "us-node")
			focusByKeyboard(e, tc.link)
			old, err := e.Page.Locator(tc.link).ElementHandle()
			e.must(err)
			e.publish(mechCardsMoved())
			expectValidatorCards(e, "bravo", "charlie", shortAddr(valDelta.address()))
			expectPeerNames(e, phone, "ap-node", "eu-node", "full-norpc", "sentry-1 (source)", "us-node")
			if v, err := old.Evaluate(`(el) => el.isConnected`); err != nil || v != false {
				t.Fatalf("the link focused before is still in the page (%v, %v): the cards were not drawn again", v, err)
			}
			e.must(e.expect(tc.link).ToBeFocused())
		})
	}
}

// mechCardsMoved is the healthy network one block later, alpha out of the
// set and a new peer, ap-node, whose RPC answers: bravo's card moves up from
// second to first, eu-node's down from first to second.
func mechCardsMoved() *node.Snapshot {
	snap := nextBlock()
	isAlpha := func(addr string) bool { return addr == valAlpha.address() }
	snap.Validators = slices.DeleteFunc(snap.Validators, func(v node.Validator) bool { return isAlpha(v.Address) })
	snap.Consensus.Votes = slices.DeleteFunc(snap.Consensus.Votes, func(v node.VoteInfo) bool { return isAlpha(v.Address) })
	// The live window's set counts as the source counts it; alpha's signing
	// stays, as for a validator that left within the window.
	snap.Signing.TotalCount = len(snap.Validators)
	snap.Signing.BFTThreshold = len(snap.Validators)*2/3 + 1
	ap := fullPeer("ap-node")
	ap.RemoteIP, ap.RPCURL = "198.51.100.30", "http://198.51.100.30:26657"
	snap.Peers = sortedPeers(append(snap.Peers, ap))
	return snap
}

// TestPageFocusStaysOnItsNamelessPeer checks that a redraw gives the focus
// back to the same row among peers with neither a node id nor a moniker:
// known by their IPs, whatever their order in the new message; sharing an
// IP, or with none, by their order.
func TestPageFocusStaysOnItsNamelessPeer(t *testing.T) {
	for _, tc := range []struct {
		name     string
		ips      [2]string
		reversed bool // the new message lists them the other way round
	}{
		{"by IP", [2]string{"203.0.113.31", "203.0.113.32"}, true},
		{"sharing an IP", [2]string{"203.0.113.31", "203.0.113.31"}, false},
		{"by order", [2]string{"", ""}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Their RPC ports tell them apart.
			nameless := []node.Peer{mechNamelessPeer("203.0.113.31"), mechNamelessPeer("203.0.113.31")}
			for i := range nameless {
				nameless[i].RemoteIP = tc.ips[i]
				nameless[i].RPCURL = fmt.Sprintf("http://203.0.113.31:%d", 26657+i)
			}
			sc := healthyNetwork()
			sc.snap.Peers = append(slices.Clone(nameless), sc.snap.Peers...)
			e := openPage(t, desktop, sc)
			second := `#peers-body .info-btn[href="` + nameless[1].RPCURL + `/status"]`
			focusByKeyboard(e, second)
			e.must(e.expect(pagePopover + " .t").ToHaveText(nameless[1].RPCURL))
			old, err := e.Page.Locator(second).ElementHandle()
			e.must(err)

			next := slices.Clone(nameless)
			if tc.reversed {
				slices.Reverse(next)
			}
			snap := nextBlock()
			snap.Peers = append(next, snap.Peers...)
			e.publish(snap)
			if _, err := e.Page.WaitForFunction(`(el) => !el.isConnected`, old); err != nil {
				t.Fatalf("the RPC link focused before is still in the page: the table was not drawn again: %v", err)
			}
			e.must(e.expect(second).ToBeFocused())
			e.must(e.expect(pagePopover + " .t").ToHaveText(nameless[1].RPCURL))
		})
	}
}

// mechNamelessPeer is a peer known by its IP alone, with an RPC: no node id,
// no moniker.
func mechNamelessPeer(ip string) node.Peer {
	return node.Peer{RemoteIP: ip, RPCURL: "http://" + ip + ":26657", Role: "full", Height: strconv.Itoa(pageHeight)}
}

// TestPageFocusStaysInItsColumn checks that a redraw gives the focus back to
// the control in the same cell when the row gains a control before it: the
// RPC link of a peer whose provider cell gets an ⓘ, as its IP becomes known.
func TestPageFocusStaysInItsColumn(t *testing.T) {
	sc := healthyNetwork()
	eu := slices.IndexFunc(sc.snap.Peers, func(p node.Peer) bool { return p.Moniker == "eu-node" })
	ip := sc.snap.Peers[eu].RemoteIP
	sc.snap.Peers[eu].RemoteIP = ""
	e := openPage(t, desktop, sc)
	row := `#peers-body tr:has(td[data-copy="eu-node"])`
	rpc := row + " td:nth-child(7) .info-btn"
	focusByKeyboard(e, rpc)
	e.must(e.expect(row + " td:nth-child(4) .info-btn").ToHaveCount(0))
	e.must(e.expect(pagePopover + " .t").ToHaveText("http://" + ip + ":26657"))

	snap := nextBlock()
	e.publish(snap)
	e.must(e.expect(row + " td:nth-child(4) .info-btn").ToHaveCount(1))
	e.must(e.expect(rpc).ToBeFocused())
	e.must(e.expect(pagePopover + " .t").ToHaveText("http://" + ip + ":26657"))
}

// TestPageRecentBlocksOneTabStop checks that the recent blocks are one tab
// stop: Tab reaches the newest block; the arrow keys, Home and End move the
// focus among the blocks, each showing its popover; Tab leaves them and
// Shift+Tab comes back to the block left; a redraw keeps the focus on that
// block.
func TestPageRecentBlocksOneTabStop(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	boxes := e.Page.Locator("#recent-blocks-body .info-btn")
	e.must(e.Expect.Locator(boxes).ToHaveCount(100))
	e.must(e.expect(`#recent-blocks-body [tabindex="0"]`).ToHaveCount(1))
	block := func(h int) {
		t.Helper()
		e.must(e.Expect.Locator(e.Page.Locator(fmt.Sprintf(`#recent-blocks-body [data-key="%d"]`, h))).ToBeFocused())
		e.must(e.expect(pagePopover + " .t").ToHaveText(fmt.Sprintf("#%d", h)))
		e.must(e.expect(pagePopover).ToBeVisible())
	}

	e.must(e.Page.Locator("#rb-compact").Focus())
	e.must(e.Page.Keyboard().Press("Tab"))
	block(pageHeight)
	e.must(e.Page.Keyboard().Press("ArrowRight"))
	block(pageHeight - 1)
	e.must(e.Page.Keyboard().Press("End"))
	block(pageHeight - 99)
	e.must(e.Page.Keyboard().Press("ArrowRight"))
	block(pageHeight - 99)
	e.must(e.Page.Keyboard().Press("Home"))
	block(pageHeight)
	e.must(e.Page.Keyboard().Press("ArrowLeft"))
	block(pageHeight)
	e.must(e.Page.Keyboard().Press("ArrowRight"))
	e.must(e.Page.Keyboard().Press("ArrowRight"))
	block(pageHeight - 2)

	e.must(e.Page.Keyboard().Press("Tab"))
	if mechFocusInside(e, "#recent-blocks-body") {
		t.Fatalf("Tab from a block focused %s, want the focus out of the blocks", mechFocusedName(e))
	}
	e.must(e.Page.Keyboard().Press("Shift+Tab"))
	block(pageHeight - 2)

	e.publish(healthySnapshot())
	block(pageHeight - 2)
	e.must(e.expect(`#recent-blocks-body [tabindex="0"]`).ToHaveCount(1))
}

// TestPageRecentBlocksLeaveModifiedKeysToTheBrowser checks that an arrow,
// Home or End with Alt, Control or Meta held does not move the focus among
// the recent blocks: the browser has its own use for them.
func TestPageRecentBlocksLeaveModifiedKeysToTheBrowser(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	newest := fmt.Sprintf(`#recent-blocks-body [data-key="%d"]`, pageHeight)
	e.must(e.Page.Locator("#rb-compact").Focus())
	e.must(e.Page.Keyboard().Press("Tab"))
	e.must(e.expect(newest).ToBeFocused())
	for _, key := range []string{"Alt+ArrowRight", "Control+ArrowRight", "Meta+ArrowRight", "Control+End"} {
		e.must(e.Page.Keyboard().Press(key))
		e.must(e.expect(newest).ToBeFocused())
	}
	e.must(e.Page.Keyboard().Press("ArrowRight"))
	e.must(e.expect(fmt.Sprintf(`#recent-blocks-body [data-key="%d"]`, pageHeight-1)).ToBeFocused())
}

// TestPageRecentBlocksFocusOutlivesItsBlock checks that a keyboard user on
// the oldest block keeps the focus among the blocks when that block leaves
// the window: it goes to the nearest block left, the new oldest, which
// shows its popover.
func TestPageRecentBlocksFocusOutlivesItsBlock(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	block := func(h int) string { return fmt.Sprintf(`#recent-blocks-body [data-key="%d"]`, h) }
	e.must(e.Page.Locator("#rb-compact").Focus())
	e.must(e.Page.Keyboard().Press("Tab"))
	e.must(e.Page.Keyboard().Press("End"))
	e.must(e.expect(block(pageHeight - 99)).ToBeFocused())
	e.publish(withRecentBlocks(consecutiveBlocks(pageHeight+1, 100)...))
	e.must(e.expect(block(pageHeight + 1)).ToBeAttached())
	e.must(e.expect(block(pageHeight - 98)).ToBeFocused())
	e.must(e.expect(pagePopover + " .t").ToHaveText(fmt.Sprintf("#%d", pageHeight-98)))
}

// ---- Popover lifecycle

// TestPagePopoverFollowsItsTarget checks that an open popover never outlives
// its target: it hides when the page draws the target again, when the target
// stops showing, when a dialog opens over it and when what is under it
// scrolls; it shows the target's new content when the page updates the
// target in place; a tap on the target drawn again opens it again. With a
// pointer that hovers, the pointer may open the new target's popover where
// the old one stood: what such a check asserts is that no popover shows the
// old target's content.
func TestPagePopoverFollowsItsTarget(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		t.Run("drawn again", func(t *testing.T) {
			e := openPage(t, p, healthyNetwork())
			target := e.Page.Locator(charlieMissed100)
			if p == phone {
				target = e.Page.Locator(charlieCardMissed100)
			}
			e.showPopover(target)
			e.must(e.Expect.Locator(e.Page.Locator(pagePopover + " td.v").First()).ToHaveText("1"))
			snap := nextBlock()
			missed := snap.Signing.ValidatorSigning[valCharlie.address()]
			missed.Missed, missed.Signed = 2, missed.Signed-1
			snap.Signing.ValidatorSigning[valCharlie.address()] = missed
			e.publish(snap)
			e.must(e.Expect.Locator(target).ToHaveText("2"))
			mechExpectPopoverGone(e, p, ` tr:first-child td.v:text-is("1")`)

			e.showPopover(target)
			e.must(e.Expect.Locator(e.Page.Locator(pagePopover + " td.v").First()).ToHaveText("2"))
			if p.touch() {
				e.must(target.Tap())
				e.must(e.expect(pagePopover).ToBeHidden())
			}
		})

		if p == desktop {
			// The keyboard holds the popover open where a pointer would not.
			t.Run("keyboard, drawn again", func(t *testing.T) {
				e := openPage(t, p, healthyNetwork())
				info := `#validators-body tr:has(td[data-copy="bravo"]) td:nth-child(4) .info-btn`
				focusByKeyboard(e, info)
				e.must(e.expect(pagePopover + " td.v").ToHaveText("203.0.113.11"))
				snap := nextBlock()
				snap.Peers[0].RemoteIP = "203.0.113.111" // bravo's
				e.publish(snap)
				e.must(e.expect(pagePopover + " td.v").ToHaveText("203.0.113.111"))
				e.must(e.expect(pagePopover).ToBeVisible())
				mechExpectPopoverBeside(e, info)
			})
		}

		t.Run("updated in place", func(t *testing.T) {
			e := openPage(t, p, healthyNetwork())
			if p == phone {
				// A phone's Network State opens compact, without the app hash.
				e.must(e.Page.Locator("#ns-compact-cb").Uncheck())
			}
			e.showPopover(e.Page.Locator("#d-apphash-last"))
			e.must(e.Expect.Locator(e.Page.Locator(pagePopover + " td.v").First()).ToHaveText(appHashHex(pageHeight)))
			snap := nextBlock()
			snap.AppHashLast = appHash(pageHeight + 1)
			e.publish(snap)
			e.must(e.Expect.Locator(e.Page.Locator(pagePopover + " td.v").First()).ToHaveText(appHashHex(pageHeight + 1)))
			e.must(e.expect(pagePopover).ToBeVisible())
		})

		t.Run("stops showing", func(t *testing.T) {
			fill := &settableFill{p: blocks.Progress{
				ChainID: pageChainID, From: 4000, To: pageHeight, FromTime: healthyBlockTime(4000), Floor: 1,
			}}
			sc := healthyNetwork()
			sc.fillFrom = fill
			e := openPage(t, p, sc, withInstalledClock())
			e.showPopover(e.Page.Locator("#ns-sync"))
			e.must(e.expect(pagePopover + " .t").ToHaveText("Block history"))
			fill.set(blocks.Progress{
				ChainID: pageChainID, From: 1, To: pageHeight, FromTime: healthyBlockTime(1), Complete: true,
			})
			e.runFor(syncPoll)
			e.must(e.expect("#ns-sync").ToBeHidden())
			mechExpectPopoverGone(e, p, ` .t:text-is("Block history")`)
		})

		t.Run("dialog opens", func(t *testing.T) {
			e := openPage(t, p, healthyNetwork())
			target := e.Page.Locator(charlieMissed100)
			if p == phone {
				target = e.Page.Locator(charlieCardMissed100)
			}
			name := e.Page.Locator(`a.val-link:visible:has-text("alpha")`)
			e.press(name)
			e.must(e.expect("#val-modal").ToHaveClass(regexp.MustCompile(`\bopen\b`)))
			_, err := e.Page.GoBack()
			e.must(err)
			e.must(e.expect("#val-modal").Not().ToHaveClass(regexp.MustCompile(`\bopen\b`)))

			e.showPopover(target)
			_, err = e.Page.GoForward()
			e.must(err)
			e.must(e.expect("#val-modal").ToHaveClass(regexp.MustCompile(`\bopen\b`)))
			mechExpectPopoverGone(e, p, ` .t:text-is("last 100 blocks")`)

			if p == phone {
				_, err = e.Page.GoBack()
				e.must(err)
				e.showPopover(target)
				e.must(name.Tap())
				e.must(e.expect("#val-modal").ToHaveClass(regexp.MustCompile(`\bopen\b`)))
				e.must(e.expect(pagePopover).ToBeHidden())
			}
		})

		t.Run("scrolls", func(t *testing.T) {
			e := openPage(t, p, healthyNetwork())
			if p == phone {
				e.must(e.Page.Locator("#ns-compact-cb").Uncheck())
			}
			e.showPopover(e.Page.Locator("#d-apphash-last"))
			e.must(e.Page.Locator("#peers-card").ScrollIntoViewIfNeeded())
			mechExpectPopoverGone(e, p, ` td.l:text-is("base64")`)

			e.press(e.Page.Locator(`a.val-link:visible:has-text("alpha")`))
			timeline := e.Page.Locator(`#val-body .val-sec[data-section="timeline"]`)
			e.must(e.Expect.Locator(timeline).ToBeAttached())
			e.showPopover(e.Page.Locator(`#val-body .val-sec[data-section="uptime"] h3 .info-btn`))
			e.must(e.expect(pagePopover).ToContainText("What: "))
			e.must(timeline.ScrollIntoViewIfNeeded())
			mechExpectPopoverGone(e, p, `:has-text("What: ")`)
		})
	})
}

// TestPagePopoverStaysWhileItsTargetStays checks that a scroll event that
// leaves the popover's target where it was leaves the popover open, such as
// the one a browser sends a frame after a scroll the pointer then opened the
// popover on; a scroll that moves the target hides it.
func TestPagePopoverStaysWhileItsTargetStays(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		if p == phone {
			e.must(e.Page.Locator("#ns-compact-cb").Uncheck())
		}
		e.showPopover(e.Page.Locator("#d-apphash-last"))
		e.must(e.expect(pagePopover + ` td.l:text-is("base64")`).ToHaveCount(1))
		// A scroll event of the page whose position has not changed since
		// the popover opened.
		_, err := e.Page.Evaluate(`() => document.dispatchEvent(new Event('scroll'))`)
		e.must(err)
		e.must(e.expect(pagePopover).ToBeVisible())
		e.must(e.expect(pagePopover + ` td.l:text-is("base64")`).ToHaveCount(1))

		e.must(e.Page.Locator("#peers-card").ScrollIntoViewIfNeeded())
		mechExpectPopoverGone(e, p, ` td.l:text-is("base64")`)
	})
}

// TestPagePopoverFollowsAPageScrollUnderItsTarget checks a page scroll that
// leaves the popover's target where it sits on screen, which would leave
// the popover, placed on the page, behind: a pointer's popover hides as the
// lost-connection banner shows above its target and the browser scrolls the
// page by the banner's height to keep the view in place; a keyboard-focused
// one in the validator dialog, which stays put over the page, moves with
// the page's scroll to stay beside its target.
func TestPagePopoverFollowsAPageScrollUnderItsTarget(t *testing.T) {
	t.Run("pointer", func(t *testing.T) {
		e := openPage(t, desktop, healthyNetwork(), withInstalledClock())
		e.runFor(time.Second)
		e.showPopover(e.Page.Locator(euNodeCountry))
		before, y := boxOf(e, euNodeCountry), evalInt(e, `() => scrollY`)
		e.closeWebSockets()
		e.must(e.expect("#error-banner").ToBeVisible())
		if _, err := e.Page.WaitForFunction(`(y) => scrollY !== y`, y); err != nil {
			t.Fatalf("the page stayed at scrollY %d as the banner showed: %v", y, err)
		}
		if after := boxOf(e, euNodeCountry); math.Abs(after.Y-before.Y) > 1 {
			t.Fatalf("the target moved on screen from y %.1f to %.1f, want it kept in place", before.Y, after.Y)
		}
		e.must(e.expect(pagePopover).ToBeHidden())
	})
	t.Run("keyboard", func(t *testing.T) {
		e := openPage(t, desktop, healthyNetwork())
		e.press(e.Page.Locator(`a.val-link:visible:has-text("alpha")`))
		info := `#val-body .val-sec[data-section="uptime"] h3 .info-btn`
		focusByKeyboard(e, info)
		e.must(e.expect(pagePopover).ToContainText("What: "))
		y := evalInt(e, `() => scrollY`)
		if y < 100 {
			t.Fatalf("the page behind the dialog is at scrollY %d, want room to scroll 100 up", y)
		}
		// The wheel over the dialog's backdrop scrolls the page behind it.
		e.must(e.Page.Mouse().Move(4, 4))
		e.must(e.Page.Mouse().Wheel(0, -100))
		if _, err := e.Page.WaitForFunction(`(y) => scrollY === y`, y-100); err != nil {
			t.Fatalf("the wheel left the page at scrollY %d, want %d: %v", evalInt(e, `() => scrollY`), y-100, err)
		}
		e.must(e.expect(pagePopover).ToBeVisible())
		mechExpectPopoverBeside(e, info)
	})
}

// TestPagePopoverStaysOverAnotherBoxScroll checks that a scroll elsewhere
// leaves a popover open: hovering a map dot scrolls the side list to the
// dot's row, and the dot's popover stays.
func TestPagePopoverStaysOverAnotherBoxScroll(t *testing.T) {
	sc := healthyNetwork()
	sc.snap.Peers = worldPeers()
	e := openPage(t, desktop, sc)
	// The last row: the one country with one peer, the others have two.
	row := e.Page.Locator("#map-country-list .country-row").Last()
	e.must(e.Expect.Locator(row).ToHaveAttribute("data-cc", loneCountry))
	e.must(e.Expect.Locator(row).Not().ToBeInViewport())
	dot := e.Page.Locator(fmt.Sprintf(`#map-points [data-cc="%s"]`, loneCountry))
	e.showPopover(dot)
	e.must(e.expect(pagePopover + " .t").ToContainText("New Zealand"))
	e.must(e.Expect.Locator(row).ToBeInViewport())
	e.must(e.expect(pagePopover).ToBeVisible())
	e.must(e.expect(pagePopover + " .t").ToContainText("New Zealand"))
}

// TestPageMapListShowsAHoveredRowWhole checks that hovering a map dot
// scrolls its row clear of the side list's border: a row whose last pixel
// lies under the list's bottom border is clipped, and the list scrolls it
// into the part inside its borders.
func TestPageMapListShowsAHoveredRowWhole(t *testing.T) {
	sc := healthyNetwork()
	sc.snap.Peers = worldPeers()
	e := openPage(t, desktop, sc)
	const list = "#map-country-list"
	row := list + fmt.Sprintf(` .country-row[data-cc="%s"]`, loneCountry)
	e.must(e.expect(row).ToHaveCount(1))

	// Scroll the list until the row's bottom edge lies under the border:
	// below the scrollport by at most a pixel.
	c := mechMeasureListRow(e, list, row)
	to := math.Ceil(c.ScrollTop + c.RowBottom - c.PortBottom - 1)
	b := boxOf(e, list)
	e.must(e.Page.Mouse().Move(b.X+b.Width/2, b.Y+b.Height/2))
	e.must(e.Page.Mouse().Wheel(0, to-c.ScrollTop))
	if _, err := e.Page.WaitForFunction(`([sel, to]) => document.querySelector(sel).scrollTop === to`,
		[]any{list, to}); err != nil {
		t.Fatalf("the wheel left the list at scrollTop %.1f, want %.1f: %v", mechMeasureListRow(e, list, row).ScrollTop, to, err)
	}
	c = mechMeasureListRow(e, list, row)
	if c.RowBottom <= c.PortBottom || c.RowBottom > c.BorderBottom {
		t.Fatalf("row bottom %.1f, want under the border, in (%.1f, %.1f]", c.RowBottom, c.PortBottom, c.BorderBottom)
	}

	e.must(e.Page.Locator(fmt.Sprintf(`#map-points [data-cc="%s"]`, loneCountry)).Hover())
	shown := `([list, row]) => {
		const l = document.querySelector(list)
		const bottom = l.getBoundingClientRect().top + l.clientTop + l.clientHeight
		return document.querySelector(row).getBoundingClientRect().bottom <= bottom
	}`
	if _, err := e.Page.WaitForFunction(shown, []string{list, row}); err != nil {
		c = mechMeasureListRow(e, list, row)
		t.Fatalf("hovered row bottom %.1f, scrollport bottom %.1f: want the row inside the scrollport: %v", c.RowBottom, c.PortBottom, err)
	}
}

// mechListRow is a row of a scrolling list and the list, measured in the
// viewport's CSS pixels: the row's bottom edge, the bottom of the list's
// scrollport (inside its borders) and of its border box, and how far the
// list is scrolled.
type mechListRow struct{ RowBottom, PortBottom, BorderBottom, ScrollTop float64 }

func mechMeasureListRow(e *pageEnv, list, row string) mechListRow {
	e.T.Helper()
	v, err := e.Page.Evaluate(`([list, row]) => {
		const l = document.querySelector(list)
		const b = l.getBoundingClientRect()
		return [
			document.querySelector(row).getBoundingClientRect().bottom,
			b.top + l.clientTop + l.clientHeight,
			b.bottom,
			l.scrollTop,
		]
	}`, []string{list, row})
	e.must(err)
	n, ok := v.([]any)
	if !ok || len(n) != 4 {
		e.T.Fatalf("measure %s in %s: got %v, want four numbers", row, list, v)
	}
	f := make([]float64, len(n))
	for i, x := range n {
		switch x := x.(type) {
		case float64:
			f[i] = x
		case int:
			f[i] = float64(x)
		default:
			e.T.Fatalf("measure %s in %s: got %v, want four numbers", row, list, v)
		}
	}
	return mechListRow{RowBottom: f[0], PortBottom: f[1], BorderBottom: f[2], ScrollTop: f[3]}
}

// TestPageFocusedInfoPopoverFollowsAScroll checks that the popover of an ⓘ
// the keyboard focuses stays open when the page scrolls, still beside it.
func TestPageFocusedInfoPopoverFollowsAScroll(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	address := `#validators-card th[data-sort="address"] > button`
	focusByKeyboard(e, address)
	e.must(e.expect(pagePopover).ToBeVisible())
	before := evalInt(e, `() => scrollY`)
	// Up: the focus scrolled the page down to the header, to its end.
	e.must(e.Page.Mouse().Wheel(0, -300))
	// The wheel returns before the page scrolls, and the scroll's event
	// comes a frame after: the intersection observer of ToBeInViewport
	// reports after that frame.
	if _, err := e.Page.WaitForFunction(`(y) => scrollY !== y`, before); err != nil {
		t.Fatalf("the wheel left the page at scrollY %d: %v", before, err)
	}
	e.must(e.expect("body").ToBeInViewport())
	e.must(e.expect(pagePopover).ToBeVisible())
	e.must(e.Page.Locator(address).ScrollIntoViewIfNeeded())
	e.must(e.Expect.Locator(e.Page.Locator(address)).ToBeInViewport())
	e.must(e.expect(pagePopover).ToBeVisible())
	mechExpectPopoverBeside(e, address)
}

// mechExpectPopoverGone checks that the popover does not show its former
// target, whose content stale selects within the shown popover: on a touch
// screen the popover is hidden; with a pointer that hovers, it may show what
// the pointer is on.
func mechExpectPopoverGone(e *pageEnv, p profile, stale string) {
	e.T.Helper()
	if p.touch() {
		e.must(e.expect(pagePopover).ToBeHidden())
		return
	}
	e.must(e.expect(pagePopover + ".visible" + stale).ToHaveCount(0))
}

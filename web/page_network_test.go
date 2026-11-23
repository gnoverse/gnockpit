//go:build page

package web

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/node"
)

// ---- Network State rows

// networkShown says in which of the Network State's modes a row shows.
type networkShown int

const (
	networkAlways      networkShown = iota // full and compact
	networkFullOnly                        // full only
	networkCompactOnly                     // compact only
)

// shows tells whether a row shown so shows in compact mode or not.
func (s networkShown) shows(compact bool) bool {
	switch s {
	case networkFullOnly:
		return !compact
	case networkCompactOnly:
		return compact
	}
	return true
}

// networkRows are the Network State's rows, in page order: the left grid's
// first, then the right one's.
var networkRows = []struct {
	label, value string
	shown        networkShown
}{
	{"chainid", "#d-network", networkFullOnly},
	{"genesis time", "#c-gentime", networkFullOnly},
	{"block height", "#d-block", networkFullOnly},
	{"block time", "#d-time", networkAlways},
	{"avg block time", "#ns-avgblock", networkFullOnly},
	{"apphash", "#d-apphash-last", networkFullOnly},
	{"consensus", "#ns-consensus", networkAlways},
	{"proposer", "#ns-proposer", networkAlways},
	{"votes (pv/pc)", "#ns-votes", networkAlways},
	{"round age", "#ns-roundage", networkAlways},
	{"active vals", "#ns-active", networkFullOnly},
	{"active vp", "#ns-active-vp", networkFullOnly},
	{"active vals", "#ns-active-compact", networkCompactOnly},
}

// networkLeftRows is how many of networkRows the left grid holds.
const networkLeftRows = 6

// networkCard is the Network State card, which has no id.
const networkCard = ".card:has(> #ns-header-row)"

// ---- Compact mode

// networkExpectCompact checks the Network State's rows in compact mode or
// not: every row's label, and which rows show, labels and values alike.
func networkExpectCompact(e *pageEnv, compact bool) {
	e.T.Helper()
	var left, right []string
	for i, r := range networkRows {
		if i < networkLeftRows {
			left = append(left, r.label)
		} else {
			right = append(right, r.label)
		}
	}
	e.must(e.expect(networkCard + " .kv:nth-child(1) > .k").ToHaveText(left))
	e.must(e.expect(networkCard + " .kv:nth-child(2) > .k").ToHaveText(right))
	for _, r := range networkRows {
		label, value := e.expect(".kv > .k:has(+ "+r.value+")"), e.expect(r.value)
		if r.shown.shows(compact) {
			e.must(label.ToBeVisible())
			e.must(value.ToBeVisible())
		} else {
			e.must(label.ToBeHidden())
			e.must(value.ToBeHidden())
		}
	}
}

// TestPageNetworkCompact checks the Network State's compact mode: on by
// default up to 768 px wide, where its checkbox shows, and off on wider
// screens, which have no checkbox and leave a stored choice aside. Compact
// hides the rows it leaves out, values and labels alike, and shows the
// compact line of the active validators; the checkbox's choice is stored
// and holds across loads.
func TestPageNetworkCompact(t *testing.T) {
	t.Run("desktop", func(t *testing.T) {
		e := openPage(t, desktop, healthyNetwork(), withStorage(map[string]string{"ns-compact": "1"}))
		e.must(e.expect("#ns-consensus").ToContainText("h="))
		e.must(e.expect("#ns-compact-label").ToBeHidden())
		networkExpectCompact(e, false)
	})

	onProfiles(t, []profile{tablet, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		e.must(e.expect("#ns-consensus").ToContainText("h="))
		cb := e.Page.Locator("#ns-compact-cb")
		e.must(e.expect("#ns-compact-label").ToBeVisible())
		e.must(e.Expect.Locator(cb).ToBeChecked())
		networkExpectCompact(e, true)

		e.must(cb.Uncheck())
		networkExpectCompact(e, false)
		expectStored(e, "ns-compact", "0")
		e.navigate("/")
		e.must(e.Expect.Locator(cb).Not().ToBeChecked())
		networkExpectCompact(e, false)

		e.must(cb.Check())
		networkExpectCompact(e, true)
		expectStored(e, "ns-compact", "1")
		e.navigate("/")
		e.must(e.Expect.Locator(cb).ToBeChecked())
		networkExpectCompact(e, true)
	})
}

// ---- Network State values

// networkExpectBand checks that the element selector matches is classed
// in band alone and coloured as the band.
func networkExpectBand(e *pageEnv, selector, band string) {
	e.T.Helper()
	e.must(e.expect(selector).ToHaveClass(band))
	e.must(e.expect(selector).ToHaveCSS("color", bandColor[band]))
}

// networkWithPowers sets the voting powers of snap's validators to powers,
// in set order: alpha, bravo, charlie, delta. It panics when one of them is
// not in snap's set.
func networkWithPowers(snap *node.Snapshot, powers [4]int64) *node.Snapshot {
	for i, v := range healthyValidators() {
		j := slices.IndexFunc(snap.Validators, func(sv node.Validator) bool { return sv.Address == v.address() })
		if j < 0 {
			panic(fmt.Sprintf("networkWithPowers: %s not in the set", v.name()))
		}
		snap.Validators[j].VotingPower = strconv.FormatInt(powers[i], 10)
	}
	return snap
}

// networkWithVotes sets which validators of snap prevoted and which
// precommitted.
func networkWithVotes(snap *node.Snapshot, prevoted, precommitted []pageValidator) *node.Snapshot {
	among := func(vs []pageValidator, addr string) bool {
		return slices.ContainsFunc(vs, func(v pageValidator) bool { return v.address() == addr })
	}
	for i := range snap.Consensus.Votes {
		addr := snap.Consensus.Votes[i].Address
		snap.Consensus.Votes[i].Prevoted = among(prevoted, addr)
		snap.Consensus.Votes[i].Precommit = among(precommitted, addr)
	}
	return snap
}

// TestPageNetworkGridsStack checks that the Network State's two grids of
// rows sit side by side on a screen wider than 768 px, and one under the
// other at 768 px and less, a phone's included.
func TestPageNetworkGridsStack(t *testing.T) {
	beside, under := regexp.MustCompile(`^[\d.]+px [\d.]+px$`), regexp.MustCompile(`^[\d.]+px$`)
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		grids := e.expect(networkCard + " .ns-grid")
		if p == phone {
			e.must(grids.ToHaveCSS("grid-template-columns", under))
			return
		}
		e.must(grids.ToHaveCSS("grid-template-columns", beside))
		e.must(e.Page.SetViewportSize(769, 1000))
		e.must(grids.ToHaveCSS("grid-template-columns", beside))
		e.must(e.Page.SetViewportSize(768, 1000))
		e.must(grids.ToHaveCSS("grid-template-columns", under))
	})
}

// TestPageNetworkBeforeData checks that every Network State value reads "-"
// until the server sends any data.
func TestPageNetworkBeforeData(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, bootScenario())
		e.must(e.expect("#boot-panel").ToBeVisible())
		e.must(e.expect("#ns-header-row h2").ToHaveText("Network State"))
		for _, r := range networkRows {
			e.must(e.expect(r.value).ToHaveText("-"))
		}
	})
}

// TestPageNetworkVotes checks the votes line: the voting power that
// prevoted and that precommitted, of the total, each green from the BFT
// quorum (⅔ of the total, plus one), yellow from 80 % of it, red below.
func TestPageNetworkVotes(t *testing.T) {
	// A total of 30 makes a quorum of 21; a total of 29 a quorum of 20, its
	// 80 % exactly 16.
	for _, tc := range []struct {
		name                   string
		powers                 [4]int64
		prevoted, precommitted []pageValidator
		text, pv, pc           string
	}{
		{"healthy", [4]int64{10, 10, 10, 5}, []pageValidator{valAlpha, valBravo, valCharlie}, nil, "30/0 of 35", "ok", "err"},
		{"at the quorum", [4]int64{20, 1, 4, 5}, []pageValidator{valAlpha, valBravo}, []pageValidator{valAlpha}, "21/20 of 30", "ok", "warn"},
		{"at 80 % of it", [4]int64{15, 1, 4, 9}, []pageValidator{valAlpha, valBravo}, []pageValidator{valAlpha}, "16/15 of 29", "warn", "err"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				sc := healthyNetwork()
				networkWithVotes(networkWithPowers(sc.snap, tc.powers), tc.prevoted, tc.precommitted)
				e := openPage(t, p, sc)
				e.must(e.expect("#ns-votes").ToHaveText(tc.text))
				networkExpectBand(e, "#ns-votes > span:nth-of-type(1)", tc.pv)
				networkExpectBand(e, "#ns-votes > span:nth-of-type(2)", tc.pc)
			})
		})
	}
}

// networkWithDown is the healthy network with its voting powers set to
// powers, whose validators down have missed the blocks in a row that flag
// them inactive. It has a push manager: its alert detector flags them.
func networkWithDown(powers [4]int64, down ...pageValidator) *scenario {
	sc := healthyNetwork()
	sc.push = true
	networkWithPowers(sc.snap, powers)
	for _, v := range down {
		sig := sc.snap.Signing.ValidatorSigning[v.address()]
		sig.MissedInARow, sig.SignedInARow = pageMaxMissedInARow, 0
		sc.snap.Signing.ValidatorSigning[v.address()] = sig
	}
	return sc
}

// networkWithout leaves validator v out of the set of sc's snapshot.
func networkWithout(sc *scenario, v pageValidator) *scenario {
	snap, addr := sc.snap, v.address()
	snap.Validators = slices.DeleteFunc(snap.Validators, func(sv node.Validator) bool { return sv.Address == addr })
	snap.Consensus.Votes = slices.DeleteFunc(snap.Consensus.Votes, func(vi node.VoteInfo) bool { return vi.Address == addr })
	delete(snap.Signing.ValidatorSigning, addr)
	snap.Signing.TotalCount--
	return sc
}

// TestPageNetworkActivePower checks the active validators and their voting
// power. The count is ok when all are active, warn above ⅔ of them, err
// otherwise, in the full row and in the compact line alike. The power reads
// active/total and its margin over the BFT quorum (⅔ of the total, plus one),
// signed: ok from a margin of 10 % of the total, warn above 0, err otherwise.
func TestPageNetworkActivePower(t *testing.T) {
	// Powers 17/4/10/9 make a total of 40, a quorum of 27 and an ok margin
	// from 4.
	powers := [4]int64{17, 4, 10, 9}
	for _, tc := range []struct {
		name             string
		sc               func() *scenario
		count, countBand string
		power, powerBand string
	}{
		{"all active", func() *scenario { return networkWithDown(powers) }, "4/4", "ok", "40/40 (margin: +13)", "ok"},
		{"margin of 10 %", func() *scenario { return networkWithDown(powers, valDelta) }, "3/4", "warn", "31/40 (margin: +4)", "ok"},
		{"margin under 10 %", func() *scenario { return networkWithDown(powers, valCharlie) }, "3/4", "warn", "30/40 (margin: +3)", "warn"},
		{"no margin", func() *scenario { return networkWithDown(powers, valBravo, valDelta) }, "2/4", "err", "27/40 (margin: +0)", "err"},
		{"short of the quorum", func() *scenario { return networkWithDown(powers, valBravo, valCharlie) }, "2/4", "err", "26/40 (margin: -1)", "err"},
		{
			"⅔ of the validators", func() *scenario { return networkWithout(networkWithDown(powers, valCharlie), valDelta) },
			"2/3", "err", "21/31 (margin: +0)", "err",
		},
		{
			"far short, in thousands",
			func() *scenario {
				return networkWithDown([4]int64{17_000, 4_000, 10_000, 9_000}, valBravo, valCharlie, valDelta)
			},
			"1/4", "err", "17.0K/40.0K (margin: -9.7K)", "err",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				e := openPage(t, p, tc.sc())
				expectValue(e, "#ns-active", tc.count, "v "+tc.countBand)
				expectValue(e, "#ns-active-vp", tc.power, "v "+tc.powerBand)
				e.must(e.expect("#ns-active-compact").ToHaveText(tc.count + " · 60.0s avg"))
				networkExpectBand(e, "#ns-active-compact > span", tc.countBand)
				e.must(e.expect("#ns-active-compact > span").ToHaveText(tc.count))
			})
		})
	}
}

// TestPageNetworkAverageBlockTime checks the average block time, in seconds
// to a tenth, and the compact line of the active validators and that time;
// a message without an average keeps the one shown, which the compact line
// reads "-".
func TestPageNetworkAverageBlockTime(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		e.must(e.expect("#ns-avgblock").ToHaveText("60.0s"))
		e.must(e.expect("#ns-active-compact").ToHaveText("4/4 · 60.0s avg"))

		snap := healthySnapshot()
		snap.Signing.AvgBlockMs = 1234
		e.publish(snap)
		e.must(e.expect("#ns-avgblock").ToHaveText("1.2s"))
		e.must(e.expect("#ns-active-compact").ToHaveText("4/4 · 1.2s avg"))

		snap = healthySnapshot()
		snap.Signing.AvgBlockMs = 0
		e.publish(snap)
		e.must(e.expect("#ns-active-compact").ToHaveText("4/4 · - avg"))
		e.must(e.expect("#ns-avgblock").ToHaveText("1.2s"))
	})
}

// TestPageNetworkCompactBeforeTheScripts checks the first paint of a
// visitor with no stored choice: the page shows the Network State in its
// default mode, compact up to 768 px wide with its checkbox ticked, before
// the Network State's module runs, and keeps it once the module ran.
func TestPageNetworkCompactBeforeTheScripts(t *testing.T) {
	onProfiles(t, []profile{desktop, tablet, phone}, func(t *testing.T, p profile) {
		compact := p.name != desktop.name
		e := openPage(t, p, healthyNetwork(), noNavigate())
		networkState := e.hold("/js/network-state.js")
		awaitLoad := e.navigateCommitted("/")
		expectDefault := func() {
			t.Helper()
			networkExpectCompact(e, compact)
			if compact {
				e.must(e.expect("#ns-compact-cb").ToBeChecked())
			}
		}

		// The markup is parsed to its end, and the module has not run: it
		// marks the card with the viewer's choice.
		e.must(e.expect("#val-modal").ToBeAttached())
		e.must(e.expect(networkCard).Not().ToHaveClass(regexp.MustCompile(`\bcompact-`)))
		expectDefault()

		networkState.release()
		awaitLoad()
		e.must(e.expect("#ns-consensus").ToContainText("h="))
		expectDefault()
	})
}

// ---- App hash

// TestPageNetworkAppHash checks the app hash: its hex in green, then the
// height of the head in grey; its popover lists the hex and the base64,
// with no title and no native tooltip. A base64 the page cannot read shows
// as it came, and a message without an app hash keeps the one shown.
func TestPageNetworkAppHash(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork(), withStorage(map[string]string{"ns-compact": "0"}))
		const hash = "#d-apphash-last"
		hex := appHashHex(pageHeight)
		e.must(e.expect(hash + " .hash-hex").ToHaveText(hex))
		e.must(e.expect(hash + " .hash-h").ToHaveText(fmt.Sprintf("(%d)", pageHeight)))
		e.must(e.expect(hash).ToHaveClass("v hash ok"))
		e.must(e.expect(hash+" .hash-hex").ToHaveCSS("color", bandColor["ok"]))
		e.must(e.expect(hash+" .hash-h").ToHaveCSS("color", fg2Color))
		e.must(e.expect(hash).Not().ToHaveAttribute("title", regexp.MustCompile(`.*`)))

		e.showPopover(e.Page.Locator(hash))
		e.must(e.expect(pagePopover + " .t").ToHaveCount(0))
		e.must(e.expect(pagePopover + " td.l").ToHaveText([]string{"hex", "base64"}))
		e.must(e.expect(pagePopover + " td.v").ToHaveText([]string{hex, appHash(pageHeight)}))
		e.press(e.Page.Locator("h1"))
		e.must(e.expect(pagePopover).ToBeHidden())

		snap := healthyAt(pageHeight + 1)
		snap.AppHashLast = "not base64!"
		e.publish(snap)
		e.must(e.expect(hash + " .hash-hex").ToHaveText("not base64!"))
		e.must(e.expect(hash + " .hash-h").ToHaveText(fmt.Sprintf("(%d)", pageHeight+1)))

		snap = healthyAt(pageHeight + 2)
		snap.AppHashLast = ""
		e.publish(snap)
		e.must(e.expect("#d-block").ToHaveText(strconv.Itoa(pageHeight + 2)))
		e.must(e.expect(hash + " .hash-hex").ToHaveText("not base64!"))
		e.must(e.expect(hash + " .hash-h").ToHaveText(fmt.Sprintf("(%d)", pageHeight+1)))
	})
}

// ---- Voting power units

// networkVotingPower is the voting power cell (desktop) or card value
// (phone) of the validator at address.
func networkVotingPower(p profile, address string) string {
	if p == phone {
		return validatorEntryAt(p, address) + ` .peer-card-k:text-is("vp") + .peer-card-v`
	}
	return validatorEntryAt(p, address) + " td:nth-child(5)"
}

// TestPageNetworkVotingPowerUnits checks that a voting power is written in
// the unit it reaches once rounded to a tenth: 999,949 is 999.9K and 999,950
// is 1.0M, 999,949,999 is 999.9M and 999,950,000 is 1.0B.
func TestPageNetworkVotingPowerUnits(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := healthyNetwork()
		networkWithPowers(sc.snap, [4]int64{999_949, 999_950, 999_949_999, 999_950_000})
		networkWithVotes(sc.snap, []pageValidator{valBravo}, []pageValidator{valDelta})
		e := openPage(t, p, sc)
		e.must(e.expect("#ns-votes").ToHaveText("1.0M/1.0B of 2.0B"))
		e.must(e.expect("#ns-active-vp").ToHaveText("2.0B/2.0B (margin: +667.3M)"))
		for _, tc := range []struct {
			v    pageValidator
			text string
		}{
			{valAlpha, "999.9K (0%)"},
			{valBravo, "1.0M (0%)"},
			{valCharlie, "999.9M (50%)"},
			{valDelta, "1.0B (50%)"},
		} {
			e.must(e.expect(networkVotingPower(p, tc.v.address())).ToHaveText(tc.text))
		}
	})
}

// ---- Proposer

// networkProposing is the healthy network with v proposing the round.
func networkProposing(v pageValidator) *scenario {
	sc := healthyNetwork()
	sc.snap.Consensus.Proposer = v.address()
	return sc
}

// networkProposer is the proposer's name, as the Network State shows it.
const networkProposer = "#ns-proposer .node-ref"

// TestPageNetworkProposerAsTheTables checks that the proposer shows as the
// tables show a validator, our own tagged "(source)", and that pointing at
// it highlights its row, as pointing at its row highlights it.
func TestPageNetworkProposerAsTheTables(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, networkProposing(valAlpha))
		e.must(e.expect("#ns-proposer").ToHaveText("alpha (source)"))
		e.must(e.expect(networkProposer).ToHaveText("alpha"))
		e.must(e.expect("#ns-proposer .source-tag").ToHaveText(" (source)"))
		e.must(e.expect(validatorEntryAt(p, valAlpha.address()) + " .source-tag").ToHaveText(" (source)"))
		if p == phone {
			// The cards carry no data-highlight: only the table and the
			// proposer highlight each other.
			return
		}
		highlighted := regexp.MustCompile(`\bhl\b`)
		e.point(e.Page.Locator(networkProposer))
		e.must(e.expect(validatorEntryAt(p, valAlpha.address())).ToHaveClass(highlighted))
		e.point(e.Page.Locator(validatorEntryAt(p, valAlpha.address()) + " td:nth-child(1)"))
		e.must(e.expect(networkProposer).ToHaveClass(highlighted))
	})
}

// TestPageNetworkProposerCopiesItsName checks that a click on the proposer
// copies its full name, without its tag, or its full address when it is
// named by its address, and nothing once it reads "-". A phone's clipboard
// cannot be read: there the test checks the toast.
func TestPageNetworkProposerCopiesItsName(t *testing.T) {
	for _, tc := range []struct {
		name     string
		proposer pageValidator
		copied   string
	}{
		{"ours", valAlpha, "alpha"},
		{"named by its address", valDelta, valDelta.address()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				e := openPage(t, p, networkProposing(tc.proposer))
				e.must(e.expect(networkProposer).ToBeVisible())
				e.press(e.Page.Locator("#ns-proposer"))
				expectToast(e, "Copied!")
				if p.browser == chromium {
					if got := e.clipboard(); got != tc.copied {
						t.Errorf("clipboard = %q, want %q", got, tc.copied)
					}
				}
			})
		})
	}

	t.Run("sources down", func(t *testing.T) {
		e := openPage(t, desktop, networkProposing(valAlpha), withInstalledClock())
		e.runFor(time.Second)
		e.must(e.expect(networkProposer).ToHaveText("alpha"))
		e.publish(sourcesDown())
		e.runFor(time.Second)
		e.must(e.expect("#ns-proposer").ToHaveText("-"))
		e.must(e.Page.Locator("#d-network").Click())
		expectToast(e, "Copied!")
		fadeToast(e)
		e.must(e.Page.Locator("#ns-proposer").Click())
		if got := e.clipboard(); got != pageChainID {
			t.Errorf("clipboard after a click on \"-\" = %q, want the text copied before, %q", got, pageChainID)
		}
		e.must(e.expect(".copy-toast").ToHaveAttribute("style", toastClear))
	})
}

// TestPageNetworkNoProposer checks that the proposer reads "-", as other
// values without data do, while the round names none.
func TestPageNetworkNoProposer(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := healthyNetwork()
		sc.snap.Consensus.Proposer = ""
		e := openPage(t, p, sc)
		e.must(e.expect("#ns-consensus").ToHaveText(fmt.Sprintf("h=%d r=0 s=4", pageHeight+1)))
		e.must(e.expect("#ns-proposer").ToHaveText("-"))
	})
}

// ---- Validator names

// networkAddressLikeName is a moniker that reads like an address: it starts
// with g1 and is longer than 20 characters.
const networkAddressLikeName = "g1-charlie-validator-node"

// TestPageNetworkNamesShortenOnlyAddresses checks that a validator's name is
// shortened only when the validator is named by its address, the full
// address then in a popover, not a native tooltip, in the proposer, the
// table and the cards: a moniker that reads like an address shows whole.
func TestPageNetworkNamesShortenOnlyAddresses(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := networkProposing(valCharlie)
		sc.names[valCharlie.address()] = networkAddressLikeName
		e := openPage(t, p, sc)
		delta := valDelta.address()

		e.must(e.expect(networkProposer).ToHaveText(networkAddressLikeName))
		e.must(e.expect(networkProposer).Not().ToHaveAttribute("data-popover", regexp.MustCompile(`.*`)))
		for _, tc := range []struct{ address, shown string }{
			{valCharlie.address(), networkAddressLikeName},
			{delta, shortAddr(delta)},
		} {
			e.must(e.expect(validatorEntryAt(p, tc.address) + " a.val-link").ToHaveText(tc.shown))
		}
		if p == desktop {
			name := func(address string) string { return validatorEntryAt(p, address) + " td:nth-child(1)" }
			e.must(e.expect(name(valCharlie.address())).Not().ToHaveAttribute("data-popover", regexp.MustCompile(`.*`)))
			e.must(e.expect(name(delta)).ToHaveAttribute("data-popover", delta))
		}

		snap := healthySnapshot()
		snap.Consensus.Proposer = delta
		e.publish(snap)
		e.must(e.expect(networkProposer).ToHaveText(shortAddr(delta)))
		e.must(e.expect("#ns-proposer[title], #ns-proposer [title]").ToHaveCount(0))
		e.showPopover(e.Page.Locator(networkProposer))
		e.must(e.expect(pagePopover).ToHaveText(delta))
	})
}

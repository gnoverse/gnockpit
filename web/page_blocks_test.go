//go:build page

package web

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/node"
)

const (
	blocksCard  = "#recent-blocks"
	blocksBoxes = "#recent-blocks-body .info-btn"
)

// Colours of the Recent Blocks boxes: green when every validator signed,
// yellow when one is missing; strong with transactions, faint without.
const (
	blocksGreenTxs     = "rgba(63, 185, 80, 0.45)"
	blocksGreenEmpty   = "rgba(63, 185, 80, 0.13)"
	blocksYellowTxs    = "rgba(210, 153, 34, 0.45)"
	blocksYellowEmpty  = "rgba(210, 153, 34, 0.13)"
	blocksFlashColour  = "rgba(88, 166, 255, 0.5)"
	blocksGreenBorder  = "rgb(63, 185, 80)"
	blocksGreenFaint   = "rgba(63, 185, 80, 0.4)"
	blocksYellowBorder = "rgb(210, 153, 34)"
	blocksYellowFaint  = "rgba(210, 153, 34, 0.4)"
	blocksFlashBorder  = "rgb(88, 166, 255)"
)

// blocksScenario is a server with a snapshot of no recent blocks: a page
// shows the chain's status as soon as its WebSocket is connected, which is
// when an update reaches it.
func blocksScenario() *scenario {
	sc := bootScenario()
	sc.snap = withRecentBlocks()
	return sc
}

// blocksDetailed is a block with every field the popover and the copied text
// show: three of four signed, two transactions, an app hash, and a missing
// validator named by its address and two named in the registry, listed out of
// order.
func blocksDetailed() (blk node.BlockInfo, hash string) {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	hash = strings.ToUpper(hex.EncodeToString(raw))
	blk = blockAt(pageHeight, 2)
	blk.Signers = 1
	blk.AppHash = base64.StdEncoding.EncodeToString(raw)
	blk.Missing = []node.MissingValidator{
		{Address: valDelta.address()},
		{Address: valCharlie.address()},
		{Address: valAlpha.address()},
	}
	return blk, hash
}

// TestPageRecentBlocksColours checks that Recent Blocks stays hidden until
// the first update and then shows one box per block, newest first, green
// when every validator signed and yellow when one is missing, strong with
// transactions and faint without.
func TestPageRecentBlocksColours(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, blocksScenario(), withInstalledClock())
		e.must(e.expect("#d-block").ToHaveText("4320"))
		e.must(e.expect(blocksCard).ToBeHidden())

		missing := func(blk node.BlockInfo) node.BlockInfo {
			blk.Signers = 3
			return blk
		}
		top := int64(pageHeight)
		e.publish(withRecentBlocks(
			missing(blockAt(top-4, 0)),
			missing(blockAt(top-3, 3)),
			blockAt(top-2, 0),
			blockAt(top-1, 3),
			blockAt(top, 0),
		))
		e.must(e.expect(blocksCard).ToBeVisible())
		e.must(e.expect(blocksBoxes).ToHaveCount(5))

		// The newest box is the one flashing: its own colours show once the
		// flash has faded.
		for i, want := range []struct{ bg, border string }{
			{blocksGreenTxs, blocksGreenBorder},
			{blocksGreenEmpty, blocksGreenFaint},
			{blocksYellowTxs, blocksYellowBorder},
			{blocksYellowEmpty, blocksYellowFaint},
		} {
			box := e.Expect.Locator(e.Page.Locator(blocksBoxes).Nth(i + 1))
			e.must(box.ToHaveCSS("background-color", want.bg))
			e.must(box.ToHaveCSS("border-top-color", want.border))
		}
		e.runFor(time.Second)
		first := e.Expect.Locator(e.Page.Locator(blocksBoxes).First())
		e.must(first.ToHaveCSS("background-color", blocksGreenEmpty))
		e.must(first.ToHaveCSS("border-top-color", blocksGreenFaint))

		// An update without blocks leaves the card shown.
		later := withRecentBlocks()
		later.Status.SyncInfo.LatestBlockHeight = strconv.Itoa(pageHeight + 1)
		e.publish(later)
		e.must(e.expect("#d-block").ToHaveText(strconv.Itoa(pageHeight + 1)))
		e.must(e.expect(blocksCard).ToBeVisible())
		e.must(e.expect(blocksBoxes).ToHaveCount(5))
	})
}

// TestPageRecentBlocksCompact checks the compact toggle: on by default, a
// row of text-free 28 px boxes with a 1 px gap on a phone and 2 px
// elsewhere; unchecked, each box shows its height and signers over total,
// newest first, and the row scrolls. The choice is kept across a reload, and
// toggling does not flash the newest block.
func TestPageRecentBlocksCompact(t *testing.T) {
	onProfiles(t, []profile{desktop, tablet, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, blocksScenario(), withInstalledClock())
		e.must(e.expect("#d-block").ToHaveText("4320"))
		e.publish(withRecentBlocks(consecutiveBlocks(pageHeight, 3)...))

		check := e.expect("#rb-compact")
		row := e.expect("#recent-blocks-body")
		box := e.Expect.Locator(e.Page.Locator(blocksBoxes).Nth(1))
		newest := e.Expect.Locator(e.Page.Locator(blocksBoxes).First())
		gap := "2px"
		if p.name == "phone" {
			gap = "1px"
		}
		compact := func() {
			e.must(e.expect(blocksBoxes).ToHaveCount(3))
			e.must(row.ToHaveCSS("column-gap", gap))
			e.must(row.ToHaveCSS("overflow-x", "hidden"))
			e.must(box.ToHaveCSS("height", "28px"))
			e.must(box.ToHaveCSS("flex-grow", "1"))
			e.must(box.ToHaveCSS("min-width", "3px"))
			e.must(box.ToHaveCSS("cursor", "pointer"))
			e.must(e.expect(blocksBoxes).ToHaveText([]string{"", "", ""}))
		}
		full := func() {
			e.must(e.expect(blocksBoxes).ToHaveCount(3))
			e.must(row.ToHaveCSS("column-gap", "2px"))
			e.must(row.ToHaveCSS("overflow-x", "auto"))
			e.must(box.ToHaveCSS("cursor", "pointer"))
			e.must(e.expect(blocksBoxes + " > div").ToHaveText([]string{"4320", "4/4", "4319", "4/4", "4318", "4/4"}))
		}

		e.must(e.expect(blocksBoxes).ToHaveCount(3))
		e.must(check.ToBeChecked())
		compact()
		// The first render flashed the newest block; the clock is paused, so
		// it stays blue.
		e.must(newest.ToHaveCSS("background-color", blocksFlashColour))

		e.must(e.Page.Locator("#rb-compact").Uncheck())
		full()
		e.must(newest.ToHaveCSS("background-color", blocksGreenEmpty))
		if v, _ := e.storage("rb-compact"); v != "false" {
			t.Errorf("localStorage rb-compact = %q, want false", v)
		}

		// The blocks arrive with an update: publish them again to the
		// reloaded page.
		e.navigate("/")
		e.must(e.expect("#d-block").ToHaveText("4320"))
		e.publish(withRecentBlocks(consecutiveBlocks(pageHeight, 3)...))
		e.must(check.Not().ToBeChecked())
		full()

		e.must(e.Page.Locator("#rb-compact").Check())
		compact()
		e.must(newest.ToHaveCSS("background-color", blocksGreenEmpty))
		if v, _ := e.storage("rb-compact"); v != "true" {
			t.Errorf("localStorage rb-compact = %q, want true", v)
		}
	})
}

// TestPageRecentBlocksStoredFull checks that a stored "false" starts Recent
// Blocks in full mode.
func TestPageRecentBlocksStoredFull(t *testing.T) {
	e := openPage(t, desktop, blocksScenario(), withStorage(map[string]string{"rb-compact": "false"}))
	e.must(e.expect("#d-block").ToHaveText("4320"))
	e.publish(withRecentBlocks(consecutiveBlocks(pageHeight, 2)...))
	e.must(e.expect("#rb-compact").Not().ToBeChecked())
	e.must(e.expect(blocksBoxes + " > div").ToHaveText([]string{"4320", "4/4", "4319", "4/4"}))
}

// TestPageRecentBlocksCompactBeforeTheScripts checks the first paint of a
// visitor with no stored choice: the compact box of Recent Blocks is ticked
// before the module that reads the choice runs, and stays ticked once it
// ran, so the box does not flip as the page loads.
func TestPageRecentBlocksCompactBeforeTheScripts(t *testing.T) {
	e := openPage(t, desktop, blocksScenario(), noNavigate())
	recentBlocks := e.hold("/js/recent-blocks.js")
	awaitLoad := e.navigateCommitted("/")

	// The markup is parsed to its end, and no module has run: main.js
	// imports recent-blocks.js.
	e.must(e.expect("#val-modal").ToBeAttached())
	e.must(e.expect("#rb-compact").ToBeChecked())

	recentBlocks.release()
	awaitLoad()
	e.must(e.expect("#d-block").ToHaveText("4320"))
	e.must(e.expect("#rb-compact").ToBeChecked())
}

// blocksNamedScenario registers names for the validators blocksDetailed
// misses: "Charlie" in capitals sorts before "alpha" only in a case-sensitive
// order. Every missing validator is registered: the server names an
// anonymous one "unknown-val-N", so the page's address-only fallback cannot
// be reached through the server.
func blocksNamedScenario() *scenario {
	sc := blocksScenario()
	sc.names = map[string]string{
		valAlpha.address():   "alpha",
		valCharlie.address(): "Charlie",
		valDelta.address():   valDelta.address(),
	}
	return sc
}

// TestPageRecentBlocksPopover checks a block's popover: its height as the
// title and a row per fact the block carries, the missing validators sorted
// without regard to case and listed by name, else by address; a block
// without them shows its signers and transactions only.
func TestPageRecentBlocksPopover(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		detailed, hash := blocksDetailed()
		oldest := blockAt(pageHeight-1, 0)
		oldest.Time, oldest.BlockMs = "", 0
		e := openPage(t, p, blocksNamedScenario())
		e.must(e.expect("#d-block").ToHaveText("4320"))
		e.publish(withRecentBlocks(oldest, detailed))

		pop := e.expect("#popover")
		e.point(e.Page.Locator(blocksBoxes).First())
		e.must(pop.ToBeVisible())
		e.must(e.expect("#popover .t").ToHaveText("#4320"))
		e.must(e.expect("#popover td.l").ToHaveText([]string{"signed", "time", "block time", "txs", "app hash", "missing", "", ""}))
		e.must(e.expect("#popover td.v").ToHaveText([]string{
			"1/4",
			healthyBlockTime(pageHeight).Format("2006-01-02 15:04:05") + " UTC",
			"60000 ms",
			"2",
			hash[:8] + "\u2026" + hash[len(hash)-8:],
			"alpha", "Charlie", valDelta.address(),
		}))

		e.point(e.Page.Locator(blocksBoxes).Last())
		e.must(e.expect("#popover .t").ToHaveText("#4319"))
		e.must(e.expect("#popover td.l").ToHaveText([]string{"signed", "txs"}))
		e.must(e.expect("#popover td.v").ToHaveText([]string{"4/4", "0"}))
	})
}

// TestPageRecentBlocksMissingInEnglishOrder checks that a block's popover
// lists the validators missing from it in English order, an accented name
// among the others, not by code point.
func TestPageRecentBlocksMissingInEnglishOrder(t *testing.T) {
	sc := blocksScenario()
	sc.names = map[string]string{
		valBravo.address():   "Émile",
		valCharlie.address(): "zed",
		valDelta.address():   "bob",
	}
	e := openPage(t, desktop, sc)
	e.must(e.expect("#d-block").ToHaveText("4320"))
	blk := blockAt(pageHeight, 0)
	blk.Signers = 1
	blk.Missing = []node.MissingValidator{{Address: valCharlie.address()}, {Address: valBravo.address()}, {Address: valDelta.address()}}
	e.publish(withRecentBlocks(blk))
	e.point(e.Page.Locator(blocksBoxes).First())
	e.must(e.expect("#popover td.l").ToHaveText([]string{"signed", "time", "block time", "txs", "missing", "", ""}))
	e.must(e.expect("#popover td.v").ToHaveText([]string{
		"1/4", healthyBlockTime(pageHeight).Format("2006-01-02 15:04:05") + " UTC", "60000 ms", "0", "bob", "Émile", "zed",
	}))
}

// TestPageRecentBlocksNamed checks that each compact block box, which shows
// no text, is named after its block.
func TestPageRecentBlocksNamed(t *testing.T) {
	e := openPage(t, desktop, blocksScenario())
	e.must(e.expect("#d-block").ToHaveText("4320"))
	e.publish(withRecentBlocks(consecutiveBlocks(pageHeight, 3)...))
	e.must(e.expect(blocksBoxes).ToHaveCount(3))
	for i, h := range []int{pageHeight, pageHeight - 1, pageHeight - 2} {
		e.must(e.Expect.Locator(e.Page.Locator(blocksBoxes).Nth(i)).ToHaveAccessibleName(fmt.Sprintf("block #%d", h)))
	}
}

// TestPageRecentBlocksCopy checks that a click on a block copies it as text,
// with the full app hash and the missing validators by name and address, and
// shows the Copied! toast; on a touch screen the same tap opens the popover.
func TestPageRecentBlocksCopy(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		detailed, hash := blocksDetailed()
		e := openPage(t, p, blocksNamedScenario())
		e.must(e.expect("#d-block").ToHaveText("4320"))
		e.publish(withRecentBlocks(blockAt(pageHeight-1, 0), detailed))

		box := e.Page.Locator(blocksBoxes).First()
		e.must(e.expect(blocksBoxes).ToHaveCount(2))
		if p.touch() {
			e.must(box.Tap())
			e.must(e.expect("#popover").ToBeVisible())
		} else {
			e.must(box.Click())
		}
		e.must(e.expect(".copy-toast").ToHaveText("Copied!"))
		if p.name == "phone" {
			return
		}
		want := "#4320 \u2014 1/4 signed\n" +
			"Time: " + healthyBlockTime(pageHeight).Format("2006-01-02 15:04:05") + " UTC\n" +
			"Block time: 60000ms\n" +
			"Txs: 2\n" +
			"AppHash: " + hash + "\n" +
			"Missing:\n  alpha (" + valAlpha.address() + ")\n  Charlie (" + valCharlie.address() + ")\n  " + valDelta.address() + " (" + valDelta.address() + ")"
		if got := e.clipboard(); got != want {
			t.Errorf("clipboard = %q, want %q", got, want)
		}
	})
}

// TestPageRecentBlocksFlash checks that the newest block flashes blue and
// fades to its own colours when its height is greater than the last one
// shown, including when the height gains a digit, and that the same height
// shown again does not flash.
func TestPageRecentBlocksFlash(t *testing.T) {
	for _, tc := range []struct {
		name        string
		first, next int64
	}{
		{"same digits", 998, 999},
		{"gains a digit", 999, 1000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				e := openPage(t, p, blocksScenario(), withInstalledClock())
				newest := e.Expect.Locator(e.Page.Locator(blocksBoxes).First())
				e.must(e.expect("#d-block").ToHaveText("4320"))

				e.publish(withRecentBlocks(consecutiveBlocks(tc.first, 2)...))
				e.must(e.expect(blocksBoxes).ToHaveCount(2))
				e.must(newest.ToHaveCSS("background-color", blocksFlashColour))
				e.must(newest.ToHaveCSS("border-top-color", blocksFlashBorder))

				e.publish(withRecentBlocks(consecutiveBlocks(tc.next, 2)...))
				e.must(newest.ToHaveCSS("background-color", blocksFlashColour))
				e.must(newest.ToHaveCSS("border-top-color", blocksFlashBorder))
				e.runFor(time.Second)
				e.must(newest.ToHaveCSS("background-color", blocksGreenEmpty))
				e.must(newest.ToHaveCSS("border-top-color", blocksGreenFaint))

				// The same newest height again, now with transactions: shown
				// at once in its own colours.
				again := consecutiveBlocks(tc.next, 2)
				again[1].NumTxs = 1
				e.publish(withRecentBlocks(again...))
				e.must(newest.ToHaveCSS("background-color", blocksGreenTxs))
			})
		})
	}
}

// TestPageRecentBlocksFlashAfterBadHeight checks that a newest block whose
// height is not a number does not stop later blocks from flashing.
func TestPageRecentBlocksFlashAfterBadHeight(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, blocksScenario(), withInstalledClock())
		e.must(e.expect("#d-block").ToHaveText("4320"))

		bad := consecutiveBlocks(998, 2)
		bad[1].Height = "unknown"
		e.publish(withRecentBlocks(bad...))
		e.must(e.expect(blocksBoxes).ToHaveCount(2))

		e.publish(withRecentBlocks(consecutiveBlocks(999, 2)...))
		newest := e.Expect.Locator(e.Page.Locator(blocksBoxes).First())
		e.must(newest.ToHaveCSS("background-color", blocksFlashColour))
	})
}

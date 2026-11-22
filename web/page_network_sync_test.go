//go:build page

package web

import (
	"math"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
)

// networkFilling is the healthy network whose block history fills, as fill
// reports; the test changes it as the page reads it.
func networkFilling(p blocks.Progress) (*scenario, *settableFill) {
	fill := &settableFill{p: p}
	sc := healthyNetwork()
	sc.fillFrom = fill
	return sc, fill
}

// networkFilled is the healthy network's history filled from its head down
// to from, filling back to floor at rate heights a second.
func networkFilled(from, floor int64, rate float64) blocks.Progress {
	return blocks.Progress{
		ChainID: pageChainID, From: from, To: pageHeight, FromTime: healthyBlockTime(from), Floor: floor, Rate: rate,
	}
}

// networkLeftIn is the fill rate that leaves the history from 4000 down to
// height 1 filled in d.
func networkLeftIn(d time.Duration) float64 {
	return 3999 / d.Seconds()
}

// networkFillWidth returns the width the page gives the sync line's fill, in
// percent of its track.
func networkFillWidth(e *pageEnv) float64 {
	e.T.Helper()
	v, err := e.Page.Locator("#ns-sync .fill").Evaluate(`(el) => parseFloat(el.style.width)`, nil)
	e.must(err)
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	}
	e.T.Fatalf("width of the sync line's fill = %T, want a number", v)
	return 0
}

const (
	// networkDesktopTrack and networkPhoneTrack are the widths of the sync
	// line's track, in CSS pixels.
	networkDesktopTrack = 120.0
	networkPhoneTrack   = 56.0
	// networkBoxSlack is how far, in CSS pixels, a box may sit from where
	// the layout rule puts it: layout works in fractions of a pixel.
	networkBoxSlack = 0.5
	// networkShareSlack is how far, in percent, the fill's width may read
	// from its share: the browser keeps six significant digits of it.
	networkShareSlack = 1e-3
)

// TestPageNetworkSyncLine checks the Network State header's block history
// line while the history fills, read every 10 s: its label, the share filled
// as text, rounded down (to a tenth below 10 %), and as the fill's width (at
// least 0.5 %), the time left at the fill's rate once known, and the popover
// of where the fill stands.
func TestPageNetworkSyncLine(t *testing.T) {
	// From 4000 of 4320 down to 1: 321 of 4320 heights, 7.43 %.
	share := 321.0 / 4320 * 100
	for _, tc := range []struct {
		fill  blocks.Progress
		text  string
		width float64
	}{
		{networkFilled(4000, 0, 0), "7.4%", share},
		{networkFilled(pageHeight, 0, 0), "0.0%", 0.5},
		{networkFilled(3890, 0, 0), "9.9%", 431.0 / 4320 * 100},
		{networkFilled(3889, 0, 0), "10%", 10},
		{networkFilled(1000, 0, 0), "76%", 3321.0 / 4320 * 100},
		{networkFilled(4000, 3000, 0), "24%", 321.0 / 1321 * 100},
		{networkFilled(4000, 0, networkLeftIn(29*time.Second)), "7.4% · < 1 min left", share},
		{networkFilled(4000, 0, networkLeftIn(31*time.Second)), "7.4% · ≈ 1 min left", share},
		{networkFilled(4000, 0, networkLeftIn(59*time.Minute+29*time.Second)), "7.4% · ≈ 59 min left", share},
		{networkFilled(4000, 0, networkLeftIn(59*time.Minute+31*time.Second)), "7.4% · ≈ 1 h 00 min left", share},
		{networkFilled(4000, 0, networkLeftIn(time.Hour+5*time.Minute)), "7.4% · ≈ 1 h 05 min left", share},
		{networkFilled(4000, 0, networkLeftIn(9*time.Hour+59*time.Minute)), "7.4% · ≈ 9 h 59 min left", share},
		{networkFilled(4000, 0, networkLeftIn(10*time.Hour)), "7.4% · ≈ 10 h left", share},
		{networkFilled(4000, 0, networkLeftIn(47*time.Hour+59*time.Minute)), "7.4% · ≈ 47 h left", share},
		{networkFilled(4000, 0, networkLeftIn(48*time.Hour)), "7.4% · ≈ 2 d left", share},
		{networkFilled(4000, 0, networkLeftIn(60*time.Hour)), "7.4% · ≈ 3 d left", share},
	} {
		t.Run(tc.text, func(t *testing.T) {
			sc, _ := networkFilling(tc.fill)
			e := openPage(t, desktop, sc)
			e.must(e.expect("#ns-sync .text").ToHaveText(tc.text))
			if got := networkFillWidth(e); math.Abs(got-tc.width) > networkShareSlack {
				t.Errorf("fill width = %v%%, want %v%%", got, tc.width)
			}
		})
	}

	t.Run("read every 10 s and its popover", func(t *testing.T) {
		onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
			first := networkFilled(4000, 0, 0)
			first.FromTime = time.Date(2026, 11, 5, 9, 3, 0, 0, time.UTC)
			sc, fill := networkFilling(first)
			e := openPage(t, p, sc, withInstalledClock())
			e.must(e.expect("#ns-sync").ToBeVisible())
			e.must(e.expect("#ns-sync .label").ToHaveText("syncing block history"))
			e.must(e.expect("#ns-sync .text").ToHaveText("7.4%"))
			e.showPopover(e.Page.Locator("#ns-sync"))
			e.must(e.expect(pagePopover + " .t").ToHaveText("Block history"))
			e.must(e.expect(pagePopover + " td.l").ToHaveText([]string{"filled from", "to", "filling back to"}))
			e.must(e.expect(pagePopover + " td.v").ToHaveText([]string{"#4,000 (5 Nov 2026 09:03 UTC)", "#4,320", "#1"}))
			e.press(e.Page.Locator("h1"))
			e.must(e.expect(pagePopover).ToBeHidden())

			fill.set(networkFilled(3000, 1500, 1))
			e.runFor(syncPoll - time.Second)
			e.must(e.expect("#ns-sync .text").ToHaveText("7.4%"))
			e.runFor(time.Second)
			e.must(e.expect("#ns-sync .text").ToHaveText("46% · ≈ 25 min left"))
			e.showPopover(e.Page.Locator("#ns-sync"))
			e.must(e.expect(pagePopover + " td.v").ToHaveText([]string{"#3,000 (17 Nov 2026 13:59 UTC)", "#4,320", "#1,500"}))
		})
	})
}

// networkAwaitSyncRead waits until the page has handled its last read of the
// block history: it reads again, 10 s later, only once it has.
func networkAwaitSyncRead(e *pageEnv) {
	e.T.Helper()
	next := e.hold("/api/history")
	e.runFor(syncPoll)
	next.release()
}

// TestPageNetworkSyncLineHidden checks that the sync line stays hidden on a
// server without a block history, and hides at the next read once the
// history has nothing stored or is filled to the end.
func TestPageNetworkSyncLineHidden(t *testing.T) {
	t.Run("no history", func(t *testing.T) {
		sc := healthyNetwork()
		sc.seed = nil
		e := openPage(t, desktop, sc, withInstalledClock())
		networkAwaitSyncRead(e)
		e.must(e.expect("#ns-sync").ToBeHidden())
	})

	for _, tc := range []struct {
		name string
		fill blocks.Progress
	}{
		{"nothing stored", blocks.Progress{ChainID: pageChainID}},
		{"filled to the end", blocks.Progress{ChainID: pageChainID, From: 1, To: pageHeight, FromTime: healthyBlockTime(1), Complete: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				sc, fill := networkFilling(networkFilled(2, 0, 0))
				e := openPage(t, p, sc, withInstalledClock())
				e.must(e.expect("#ns-sync .text").ToHaveText("99%"))
				fill.set(tc.fill)
				e.runFor(syncPoll)
				e.must(e.expect("#ns-sync").ToBeHidden())
			})
		})
	}
}

// TestPageNetworkSyncLineKeptOnAFailedRead checks that a read of the block
// history that fails leaves the sync line as it shows, and that the next
// read that answers updates it.
func TestPageNetworkSyncLineKeptOnAFailedRead(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc, fill := networkFilling(networkFilled(4000, 0, 0))
		e := openPage(t, p, sc, withInstalledClock())
		e.must(e.expect("#ns-sync .text").ToHaveText("7.4%"))
		failed := e.hold("/api/history")
		failed.drop()
		e.runFor(syncPoll)
		failed.awaitFailure()

		// The page reads again only once it has handled the failure.
		next := e.hold("/api/history")
		fill.set(networkFilled(3000, 0, 0))
		e.runFor(syncPoll)
		e.must(e.expect("#ns-sync").ToBeVisible())
		e.must(e.expect("#ns-sync .text").ToHaveText("7.4%"))
		next.release()
		e.must(e.expect("#ns-sync .text").ToHaveText("30%"))
	})
}

// networkSyncReadBound is how long the page waits for an answer to a read
// of the block history before it gives the read up.
const networkSyncReadBound = 10 * time.Second

// TestPageNetworkSyncLineGivesUpAnUnansweredRead checks that a read of the
// block history without an answer is given up after 10 s, the line kept as it
// shows, and that the reads go on: the next one updates the line.
func TestPageNetworkSyncLineGivesUpAnUnansweredRead(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc, fill := networkFilling(networkFilled(4000, 0, 0))
		e := openPage(t, p, sc, withInstalledClock())
		e.must(e.expect("#ns-sync .text").ToHaveText("7.4%"))
		slow := e.hold("/api/history")
		e.runFor(syncPoll)
		fill.set(networkFilled(3000, 0, 0))
		e.runFor(networkSyncReadBound)
		slow.awaitAbandoned()
		e.must(e.expect("#ns-sync .text").ToHaveText("7.4%"))
		e.runFor(syncPoll)
		e.must(e.expect("#ns-sync .text").ToHaveText("30%"))
	})
}

// TestPageNetworkSyncLineLayout checks where the sync line sits: on wide
// screens on the title's line, its end at the header's, its track 120 px;
// on phones on its own line under the title, from the header's start, its
// track 56 px.
func TestPageNetworkSyncLineLayout(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc, _ := networkFilling(networkFilled(4000, 0, 0))
		e := openPage(t, p, sc)
		e.must(e.expect("#ns-sync .text").ToHaveText("7.4%"))
		row, title, line := boxOf(e, "#ns-header-row"), boxOf(e, "#ns-header-row h2"), boxOf(e, "#ns-sync")
		track := boxOf(e, "#ns-sync .track")
		if p == phone {
			if line.Y < title.Y+title.Height {
				t.Errorf("sync line top %.1f, want under the title, from %.1f", line.Y, title.Y+title.Height)
			}
			if start := boxOf(e, "#ns-sync .label").X; math.Abs(start-row.X) > networkBoxSlack {
				t.Errorf("sync line starts at %.1f, want the header's start, %.1f", start, row.X)
			}
			if math.Abs(track.Width-networkPhoneTrack) > networkBoxSlack {
				t.Errorf("track width %.1f, want %v", track.Width, networkPhoneTrack)
			}
			return
		}
		if line.Y >= title.Y+title.Height {
			t.Errorf("sync line top %.1f, want on the title's line, above %.1f", line.Y, title.Y+title.Height)
		}
		if end, want := textRight(e, "#ns-sync .text"), row.X+row.Width; math.Abs(end-want) > networkBoxSlack {
			t.Errorf("sync line text ends at %.1f, want the header's end, %.1f", end, want)
		}
		if math.Abs(track.Width-networkDesktopTrack) > networkBoxSlack {
			t.Errorf("track width %.1f, want %v", track.Width, networkDesktopTrack)
		}
	})
}

// TestPageNetworkSyncLineInAFoldedSection checks that the sync line stays in
// sight while the Network State is folded, and that pressing it neither
// folds nor unfolds the section.
func TestPageNetworkSyncLineInAFoldedSection(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc, _ := networkFilling(networkFilled(4000, 0, 0))
		e := openPage(t, p, sc)
		e.must(e.expect("#ns-sync").ToBeVisible())
		e.press(e.Page.Locator(sectionToggle("network-state")))
		expectSectionFolded(e, "network-state", true)
		e.must(e.expect("#ns-sync").ToBeVisible())
		e.press(e.Page.Locator("#ns-sync"))
		expectSectionFolded(e, "network-state", true)
		e.press(e.Page.Locator(sectionToggle("network-state")))
		expectSectionFolded(e, "network-state", false)
		e.press(e.Page.Locator("#ns-sync"))
		expectSectionFolded(e, "network-state", false)
	})
}

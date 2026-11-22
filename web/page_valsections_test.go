//go:build page

package web

import (
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// ---- Stats lines

// TestPageValidatorStats checks the stats lines of four validators: each
// figure as the report has it, counts with thousands separators, and the
// scored ones banded: uptime from 95 % (red) to 99.9 % (green), outages
// from one (yellow) to ten (red), missed proposals, the median recovery; no
// band without blocks in the set; a dash for a figure without data.
func TestPageValidatorStats(t *testing.T) {
	type stats struct {
		uptime, proposals, recovery, delay []statWindow
	}
	last30 := func(s ...statItem) statWindow { return statWindow{name: "last 30 days", stats: s} }
	type validatorStats struct {
		name string
		val  string
		want stats
	}
	cases := []validatorStats{
		{"alpha", valAlpha.address(), stats{
			uptime: []statWindow{last30(
				statItem{label: "signed", value: "100.00 %", band: "ok"}, statItem{label: "outages", value: "0", band: "ok"},
				statItem{label: "nil votes", value: "0", band: ""}, statItem{label: "other block", value: "0", band: ""},
			)},
			proposals: []statWindow{
				last30(statItem{label: "missed", value: "0 of 19,272 (0.0 %)", band: "ok"}),
				{name: "last 24 h", stats: []statItem{{label: "missed", value: "0 of 681", band: ""}}},
			},
			recovery: []statWindow{{name: "last 180 days", stats: []statItem{
				{label: "median of its last 3", value: "0 s", band: "ok"}, {label: "samples", value: "4", band: ""}, {label: "slowest", value: "0 s", band: ""},
			}}},
			delay: []statWindow{
				{name: "last 100 blocks", stats: []statItem{{label: "median", value: "+0.01 s", band: ""}}},
				last30(statItem{label: "mean", value: "0.00 s", band: ""}),
			},
		}},
		{"bravo", valBravo.address(), stats{
			uptime: []statWindow{last30(
				statItem{label: "signed", value: "95.72 %", band: "warn"}, statItem{label: "outages", value: "2", band: "warn"},
				statItem{label: "nil votes", value: "2", band: ""}, statItem{label: "other block", value: "1", band: ""},
			)},
			proposals: []statWindow{
				last30(statItem{label: "missed", value: "2 of 100 (2.0 %)", band: "warn"}),
				{name: "last 24 h", stats: []statItem{{label: "missed", value: "none expected", band: ""}}},
			},
			recovery: []statWindow{{name: "last 180 days", stats: []statItem{
				{label: "median of its last 3", value: "12 min 00 s", band: "warn"}, {label: "samples", value: "4", band: ""}, {label: "slowest", value: "24 h 00 min", band: ""},
			}}},
			// Its delays average 1 ms early: no sign once rounded.
			delay: []statWindow{
				{name: "last 100 blocks", stats: []statItem{{label: "median", value: "+0.04 s", band: ""}}},
				last30(statItem{label: "mean", value: "0.00 s", band: ""}),
			},
		}},
		{"charlie", valCharlie.address(), stats{
			uptime: []statWindow{last30(
				statItem{label: "signed", value: "94.84 %", band: "err"}, statItem{label: "outages", value: "10", band: "err"},
				statItem{label: "nil votes", value: "0", band: ""}, statItem{label: "other block", value: "0", band: ""},
			)},
			proposals: []statWindow{
				last30(statItem{label: "missed", value: "1 of 10 (10.0 %)", band: "err"}),
				{name: "last 24 h", stats: []statItem{{label: "missed", value: "1 of 10", band: ""}}},
			},
			recovery: []statWindow{{name: "last 180 days", stats: []statItem{
				{label: "median of its last 3", value: "0 s", band: "ok"}, {label: "samples", value: "4", band: ""}, {label: "slowest", value: "0 s", band: ""},
			}}},
			delay: []statWindow{
				{name: "last 100 blocks", stats: []statItem{{label: "median", value: "+0.08 s", band: ""}}},
				last30(statItem{label: "mean", value: "0.00 s", band: ""}),
			},
		}},
		// In the current set, in no stored one, out of the live window.
		{"delta", valDelta.address(), stats{
			uptime: []statWindow{last30(
				statItem{label: "signed", value: "—", band: ""}, statItem{label: "outages", value: "0", band: ""},
				statItem{label: "nil votes", value: "0", band: ""}, statItem{label: "other block", value: "0", band: ""},
			)},
			proposals: []statWindow{
				last30(statItem{label: "missed", value: "none expected", band: ""}),
				{name: "last 24 h", stats: []statItem{{label: "missed", value: "none expected", band: ""}}},
			},
			recovery: []statWindow{{name: "last 180 days", stats: []statItem{
				{label: "median of its last 3", value: "—", band: ""}, {label: "samples", value: "0", band: ""}, {label: "slowest", value: "—", band: ""},
			}}},
			delay: []statWindow{
				{name: "last 100 blocks", stats: []statItem{{label: "median", value: "—", band: ""}}},
				last30(statItem{label: "mean", value: "—", band: ""}),
			},
		}},
	}
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, monthNetwork(t), noNavigate())
		for _, tc := range cases {
			e.navigate("/?val=" + tc.val)
			e.must(e.expect("#val-body [data-section=timeline]").ToBeVisible())
			e.expectStats(tc.name, "uptime", tc.want.uptime...)
			e.expectStats(tc.name, "proposals", tc.want.proposals...)
			e.expectStats(tc.name, "recovery", tc.want.recovery...)
			e.expectStats(tc.name, "delay", tc.want.delay...)
		}
	})
}

// TestPageValidatorStatsLine checks a stats line's form: its text runs its
// parts together, a window's name is small and upper case, a divider sits
// before every window but the first, and the bands are the theme's colours.
func TestPageValidatorStatsLine(t *testing.T) {
	e := openPage(t, desktop, monthNetwork(t), withURL("/?val="+valCharlie.address()))
	e.must(e.expect("#val-body [data-section=delay] > .val-stats").ToHaveText("last 100 blocksmedian+0.08 slast 30 daysmean0.00 s"))
	wins := e.Page.Locator("#val-body [data-section=proposals] > .val-stats > .val-win")
	e.must(e.Expect.Locator(wins.Nth(0)).ToHaveCSS("border-left-width", "0px"))
	e.must(e.Expect.Locator(wins.Nth(1)).ToHaveCSS("border-left-width", "1px"))
	e.must(e.Expect.Locator(wins.Nth(1)).ToHaveCSS("border-left-color", borderColor))
	e.must(e.Expect.Locator(wins.Nth(0).Locator(".w")).ToHaveCSS("text-transform", "uppercase"))
	e.must(e.Expect.Locator(wins.Nth(0).Locator(".w")).ToHaveCSS("font-size", "11px"))
	e.must(e.Expect.Locator(wins.Nth(0).Locator(".l")).ToHaveCSS("color", fg2Color))
	for selector, colour := range map[string]string{"b.ok": greenColor, "b.err": redColor, "b:not([class])": fgColor} {
		e.must(e.Expect.Locator(e.Page.Locator("#val-body .val-stats "+selector).First()).ToHaveCSS("color", colour))
	}
	e.navigate("/?val=" + valBravo.address())
	e.must(e.Expect.Locator(e.Page.Locator("#val-body .val-stats b.warn").First()).ToHaveCSS("color", yellowColor))
}

// ---- Timeline

// valChartsRow is a Timeline row as shown: its height and age, its kind's
// mark and the mark's class, and what it says.
type valChartsRow struct{ h, k, class, w string }

// valChartsExpectTimeline waits until the Timeline shows rows, newest
// first.
func (e *pageEnv) valChartsExpectTimeline(rows ...valChartsRow) {
	e.T.Helper()
	sel := "#val-body [data-section=timeline] > .vt-row"
	var hs, ks, ws []string
	for _, r := range rows {
		hs, ks, ws = append(hs, r.h), append(ks, r.k), append(ws, r.w)
	}
	e.must(e.expect(sel + " > .h").ToHaveText(hs))
	e.must(e.expect(sel + " > .k").ToHaveText(ks))
	e.must(e.expect(sel + " > .w").ToHaveText(ws))
	for i, r := range rows {
		e.must(e.Expect.Locator(e.Page.Locator(sel + " > .k").Nth(i)).ToHaveClass("k " + r.class))
	}
}

// TestPageValidatorTimeline checks the Timeline: a row per change of the
// report, newest first, each kind with its mark, colour and words, its age
// as of the report, and its time to the minute in a popover; no link.
func TestPageValidatorTimeline(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, monthNetwork(t), withURL("/?val="+valBravo.address()))
		e.expectValidatorReport("bravo")
		e.valChartsExpectTimeline(
			valChartsRow{h: "#19,679 · 1 h ago", k: "↻", class: "vt-out", w: "not signed since the restart restart at #19,679"},
			valChartsRow{h: "#14,195 · 8 d ago", k: "↻", class: "vt-rec", w: "recovered in 12 min 00 s restart at #14,189"},
			valChartsRow{h: "#13,860 · 8 d ago", k: "↻", class: "vt-rec", w: "recovered in 2 min 00 s restart at #13,859"},
			valChartsRow{h: "#10,080 · 14 d ago", k: "↻", class: "vt-rec", w: "recovered in 24 h 00 min restart at #9,360"},
			valChartsRow{h: "#2,885 · 24 d ago", k: "↻", class: "vt-rec", w: "recovered in 10 min 00 s restart at #2,880"},
			valChartsRow{h: "#2,461 · 26 d ago", k: "■", class: "vt-out", w: "outage of 15 blocks 30 min 00 s"},
			valChartsRow{h: "#2,131 · 27 d ago", k: "■", class: "vt-out", w: "outage of 60 blocks 2 h 00 min"},
			valChartsRow{h: "#1 · 30 d ago", k: "◆", class: "vt-gen", w: "in the genesis set power 10"},
		)
		rows := e.Page.Locator("#val-body [data-section=timeline] > .vt-row")
		// A recovery's time to sign again is banded.
		for i, band := range []string{"warn", "ok", "err", "ok"} {
			e.must(e.Expect.Locator(rows.Nth(i + 1).Locator(".w > b")).ToHaveClass(band))
		}
		e.must(e.Expect.Locator(rows.Nth(0).Locator(".k")).ToHaveCSS("color", redColor))
		e.must(e.Expect.Locator(rows.Nth(1).Locator(".k")).ToHaveCSS("color", greenColor))
		e.must(e.Expect.Locator(rows.Nth(1).Locator(".w > b")).ToHaveCSS("color", yellowColor))
		e.must(e.Expect.Locator(rows.Nth(7).Locator(".k")).ToHaveCSS("color", fg2Color))
		e.must(e.Expect.Locator(rows.Nth(5).Locator(".w > .p")).ToHaveText("30 min 00 s"))

		e.point(rows.Nth(5).Locator(".h"))
		e.must(e.expect("#popover").ToBeVisible())
		e.must(e.expect("#popover").ToHaveText("2026-10-23 10:00 UTC"))
		e.must(e.expect("#val-body a").ToHaveCount(0))

		e.navigate("/?val=" + valCharlie.address())
		e.must(e.expect("#val-title").ToHaveText("charlie"))
		// Every row, however many: 10 outages, 5 recoveries, the genesis set.
		e.must(e.Expect.Locator(rows).ToHaveCount(16))
	})
}

// TestPageValidatorOngoingOutage checks an outage still going on: its
// Timeline row and its popover rows say so.
func TestPageValidatorOngoingOutage(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, ongoingOutageNetwork(t), withURL("/?val="+valBravo.address()))
		e.expectValidatorReport("bravo")
		row := e.Page.Locator("#val-body [data-section=timeline] > .vt-row > .w").First()
		e.must(e.Expect.Locator(row).ToHaveText("outage of 31 blocks 1 h 00 min, ongoing"))
		// From 2026-11-15: 2026-11-17 is the third day.
		e.point(e.chartBox("uptime").Locator("svg > rect").Nth(2))
		e.expectPopover(rowsPopover{
			title:    "2026-11-17",
			labels:   []string{"signed", "", "absent", "other block", "nil votes", "● outage", ""},
			values:   []string{"690 of 720", "95.83 %", "30", "0", "0 (signed)", "31 blocks, 1 h 00 min, ongoing", "from 23:00 UTC, #2,131"},
			swatches: []string{yellowColor},
		})
	})
}

// TestPageValidatorTimelineSetChanges checks the Timeline rows of set
// changes: a key rotation, a join, a leave, a power change and the genesis
// set, powers with thousands separators, addresses short and plain.
func TestPageValidatorTimelineSetChanges(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, foxtrotNetwork(t), withURL("/?val="+opFoxtrot))
		e.expectValidatorReport("foxtrot")
		e.valChartsExpectTimeline(
			valChartsRow{h: "#3,000 · 22 h ago", k: "⇄", class: "vt-rot", w: "rotated its key " +
				shortAddr(foxtrotOld.address()) + " → " + shortAddr(foxtrotNew.address())},
			valChartsRow{h: "#2,500 · 30 h ago", k: "+", class: "vt-add", w: "joined power 5"},
			valChartsRow{h: "#2,000 · 39 h ago", k: "−", class: "vt-rm", w: "left power 20"},
			valChartsRow{h: "#1,000 · 2 d ago", k: "↕", class: "vt-pow", w: "power 10 → 20"},
			valChartsRow{h: "#1 · 3 d ago", k: "◆", class: "vt-gen", w: "in the genesis set power 10"},
		)
		rows := e.Page.Locator("#val-body [data-section=timeline] > .vt-row")
		for i, colour := range []string{blueColor, greenColor, redColor, yellowColor, fg2Color} {
			e.must(e.Expect.Locator(rows.Nth(i).Locator(".k")).ToHaveCSS("color", colour))
		}
		e.must(e.expect("#val-body a").ToHaveCount(0))
	})
}

// TestPageValidatorTimelineLayout checks a Timeline row's layout: four
// columns on a wide screen; on a phone, the mark and what it says on one
// line, its height and age below; what it says wraps.
func TestPageValidatorTimelineLayout(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, foxtrotNetwork(t), withURL("/?val="+opFoxtrot))
		e.expectValidatorReport("foxtrot")
		row := e.Page.Locator("#val-body [data-section=timeline] > .vt-row").First()
		w := row.Locator(".w")
		// What a row says wraps, a long name broken anywhere.
		e.must(e.Expect.Locator(w).ToHaveCSS("white-space", "normal"))
		e.must(e.Expect.Locator(w).ToHaveCSS("overflow-wrap", "anywhere"))
		if p.device == "" {
			e.must(e.Expect.Locator(row).ToHaveCSS("grid-template-columns", regexp.MustCompile(`^[\d.]+px 18px [\d.]+px [\d.]+px$`)))
			return
		}
		e.must(e.Expect.Locator(row).ToHaveCSS("grid-template-columns", regexp.MustCompile(`^18px [\d.]+px$`)))
		e.must(e.Expect.Locator(row.Locator(".h")).ToHaveCSS("grid-row-start", "2"))
		e.must(e.Expect.Locator(row.Locator(".h")).ToHaveCSS("grid-column-start", "2"))
		e.must(e.Expect.Locator(row.Locator(".k")).ToHaveCSS("grid-row-start", "1"))
	})
}

// TestPageValidatorDurations checks how a duration is written, at each
// boundary: seconds under a minute, minutes and seconds under an hour, hours
// and minutes under 48 h, days from there, rounded.
func TestPageValidatorDurations(t *testing.T) {
	e := openPage(t, desktop, timedOutagesNetwork(t), withURL("/?val="+valBravo.address()))
	e.expectValidatorReport("bravo")
	// Newest first: 60 h, 48 h, 47 h 59 min, 1 h, 3599 s, 59.5 s, 59 s.
	e.must(e.expect("#val-body [data-section=timeline] > .vt-row:has(> .k.vt-out) > .w > .p").ToHaveText([]string{
		"3 d", "2 d", "47 h 59 min", "1 h 00 min", "59 min 59 s", "1 min 00 s", "59 s",
	}))
}

// ---- Sections

// TestPageValidatorScoredGroup checks the group of the sections the ranking
// scores: a blue line down its left, its label upper case with its ⓘ, its
// first section close under the label.
func TestPageValidatorScoredGroup(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1), withURL("/?val="+opBravo))
		e.expectValidatorReport("bravo")
		group := e.Page.Locator("#val-body > .val-scored")
		e.must(e.Expect.Locator(group).ToHaveCSS("box-shadow", blueColor+" 3px 0px 0px 0px inset"))
		e.must(e.Expect.Locator(group).ToHaveCSS("padding-left", "12px"))
		label := group.Locator(":scope > .val-glabel")
		e.must(e.Expect.Locator(label).ToHaveCSS("text-transform", "uppercase"))
		e.must(e.Expect.Locator(label).ToHaveCSS("color", blueColor))
		e.must(e.Expect.Locator(label.Locator(".info-btn")).ToHaveCSS("text-transform", "none"))
		e.must(e.Expect.Locator(group.Locator(":scope > .val-sec").First()).ToHaveCSS("margin-top", "8px"))
		e.must(e.Expect.Locator(group.Locator(":scope > .val-sec").Nth(1)).ToHaveCSS("margin-top", "16px"))
	})
}

// TestPageValidatorInfoPopovers checks each ⓘ of the page: what the datum
// is, where it comes from, whether the ranking scores it, shown on three
// lines.
func TestPageValidatorInfoPopovers(t *testing.T) {
	const no = "no, shown for monitoring"
	infos := []struct{ what, source, scored string }{
		{
			"whether it is in the validator set, since when without a break (key rotations included), its voting power and share of the set",
			"the node's current validator set; the block history's set changes for since, a bound (since before) when its join is older than the history",
			no,
		},
		{"where its node is hosted", "its matched peer's IP, through the GeoIP and ASN databases", no},
		{
			"what the operator wrote in its valopers profile, and the realm's state for it",
			"the valopers realm (gno.land/r/gnops/valopers), read every 15 min", no,
		},
		{
			"the sections the validator ranking scores; its score combines uptime (50 %), outages (20 %), recovery (20 %) and proposals (10 %)",
			"the block history", "yes, all three, with the weights above",
		},
		{
			"per UTC day, of the blocks it was in the set for: signed (a nil vote counts as signed), absent from the commit, or voted for another block",
			"the block history: every block's commit",
			"yes, signed ÷ blocks in the set (50 %), and outages, runs of 10 or more absences (20 %), over the 30 complete UTC days before today; outages leave recovery runs out, as the ranking does; signed counts every height, where the ranking leaves recovery runs out",
		},
		{
			"per UTC day: the rounds it was the expected proposer of until the block was made, and those it missed",
			"the block history: block headers and each round's expected proposer",
			"yes, missed ÷ expected (10 %) over the 30 complete UTC days before today; the ranking counts only heights it signed and leaves restart heights out, the figures here count every height",
		},
		{
			"after a restart (two blocks more than 2 min apart), how long it took to sign again; it gets a sample only if it was signing before the restart",
			"the block history: restart gaps and absences",
			"yes, the median of its last 3 samples within the 180 complete UTC days before today (20 %)",
		},
		{
			"how far its precommits' times are from the commit's voting-power-weighted median time; negative is early",
			"the block history: daily vote-delay histograms; the live window for the last 100 blocks", no,
		},
		{
			"its changes: joins, leaves, power, key rotations, outages, recoveries after a restart",
			"the block history: validator sets, set-change events, absences, restart gaps",
			"yes, its outages and recoveries (see Uptime and Recovery); the rest is not",
		},
	}
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1), withURL("/?val="+opBravo))
		e.expectValidatorReport("bravo")
		buttons := e.Page.Locator("#val-body .info-btn")
		e.must(e.Expect.Locator(buttons).ToHaveCount(len(infos)))
		for i, in := range infos {
			e.must(e.Expect.Locator(buttons.Nth(i)).ToHaveAttribute("data-popover", "What: "+in.what+"\nSource: "+in.source+"\nScored: "+in.scored))
		}
		e.point(buttons.Nth(4))
		e.must(e.expect("#popover").ToBeVisible())
		e.must(e.expect("#popover").ToHaveCSS("white-space", "pre-wrap"))
		e.must(e.expect("#popover").ToHaveJSProperty("innerText", "What: "+infos[4].what+"\nSource: "+infos[4].source+"\nScored: "+infos[4].scored))
	})
}

// ---- Popovers and layout

// TestPageValidatorPopoversOnHover checks the popovers of a pointer device:
// a hover shows one in rows, never hidden while the pointer moves within its
// target or clicks it, below its target, or above it without room below,
// kept 8 px inside the window's right edge and 4 px from its left, over the
// page; leaving the target hides it.
func TestPageValidatorPopoversOnHover(t *testing.T) {
	e := openPage(t, desktop, monthNetwork(t), withURL("/?val="+valBravo.address()))
	e.expectValidatorReport("bravo")
	pop := e.Page.Locator("#popover")
	day := e.chartBox("delay").Locator(".val-axis > svg > g").First() // 2026-10-20
	parts := day.Locator("rect")
	e.point(parts.Nth(2)) // ±0.25 s
	e.must(e.Expect.Locator(pop).ToBeVisible())
	e.must(e.Expect.Locator(pop).ToHaveClass("popover rows visible"))
	e.must(e.Expect.Locator(pop.Locator(":scope > div.t")).ToHaveText("2026-10-20"))
	e.must(e.Expect.Locator(pop.Locator(":scope > table td.v").First()).ToHaveCSS("text-align", "right"))
	e.must(e.Expect.Locator(pop).ToHaveCSS("z-index", "2000"))
	e.must(e.expect("#val-modal").ToHaveCSS("z-index", "1000"))
	below := e.popoverPlacement(day)
	if !nearPx(below.top, below.targetBottom+4) {
		t.Errorf("popover top at %v, want 4 px under its target's bottom %v", below.top, below.targetBottom)
	}
	if !nearPx(below.left, below.targetLeft) {
		t.Errorf("popover left at %v, want its target's left %v", below.left, below.targetLeft)
	}
	hidings := e.watchPopover()
	e.must(parts.Nth(3).Hover()) // 0.25 to 1 s: the same day
	e.must(parts.Nth(3).Click())
	e.must(e.Expect.Locator(pop).ToBeVisible())
	if n := hidings(); n != 0 {
		t.Errorf("the popover hid %d times as the pointer moved within its target and clicked it, want never", n)
	}
	// Leaving to the page's corner, a move that scrolls nothing.
	e.must(e.Page.Mouse().Move(0, 0))
	e.must(e.Expect.Locator(pop).ToBeHidden())

	// The newest uptime bar, at the right: kept 8 px inside the window.
	newest := e.chartBox("uptime").Locator(".val-axis > svg > rect").Last()
	e.point(newest)
	e.must(e.Expect.Locator(pop.Locator(":scope > div.t")).ToHaveText("2026-11-18"))
	right := e.popoverPlacement(newest)
	if !nearPx(right.left, right.innerWidth-right.width-8) {
		t.Errorf("popover left at %v, want %v: 8 px inside the window", right.left, right.innerWidth-right.width-8)
	}

	// A short window: no room under the bar, the popover sits above it.
	e.must(e.Page.SetViewportSize(1280, 400))
	e.must(e.Page.Mouse().Move(0, 0))
	e.must(e.Expect.Locator(pop).ToBeHidden())
	bar := e.chartBox("uptime").Locator(".val-axis > svg > rect").First()
	e.point(bar)
	above := e.popoverPlacement(bar)
	if above.targetBottom+4+above.height <= above.innerHeight {
		t.Fatalf("the bar's bottom at %v leaves room for the popover's %v px in %v: the test needs a lower bar", above.targetBottom, above.height, above.innerHeight)
	}
	if want := max(4, above.targetTop-4-above.height); !nearPx(above.top, want) {
		t.Errorf("popover top at %v, want %v: 4 px over its target's top %v", above.top, want, above.targetTop)
	}

	// A window narrower than an ⓘ's popover: it starts 4 px from the left.
	// The charts are drawn again as calendars first, which moves the page
	// under the pointer.
	e.must(e.Page.SetViewportSize(300, 800))
	e.must(e.expect("#val-body .val-cal").ToHaveCount(2))
	info := e.Page.Locator(sectionInfo("uptime"))
	e.point(info)
	narrow := e.popoverPlacement(info)
	if narrow.innerWidth-narrow.width-8 >= 4 {
		t.Fatalf("a popover %v px wide fits a %v px window: the test needs a wider one", narrow.width, narrow.innerWidth)
	}
	if !nearPx(narrow.left, 4) {
		t.Errorf("popover left at %v, want 4", narrow.left)
	}
}

// TestPageValidatorPopoversOnTap checks the popovers of a touch screen: a
// tap on a target shows its popover, a second hides it, a tap elsewhere
// hides it.
func TestPageValidatorPopoversOnTap(t *testing.T) {
	e := openPage(t, phone, monthNetwork(t), withURL("/?val="+valBravo.address()))
	e.expectValidatorReport("bravo")
	pop := e.Page.Locator("#popover")
	cell := e.chartBox("uptime").Locator(".val-cal-scroll > svg > rect").First()
	e.point(cell)
	e.must(e.Expect.Locator(pop.Locator(":scope > div.t")).ToHaveText("2026-10-20"))
	e.must(cell.Tap())
	e.must(e.Expect.Locator(pop).ToBeHidden())
	e.must(cell.Tap())
	e.must(e.Expect.Locator(pop).ToBeVisible())
	e.must(e.Page.Locator("#val-title").Tap())
	e.must(e.Expect.Locator(pop).ToBeHidden())

	age := e.Page.Locator("#val-body [data-section=timeline] > .vt-row > .h").First()
	e.point(age)
	e.must(e.Expect.Locator(pop).ToHaveText("2026-11-18 11:00 UTC"))
	e.point(e.Page.Locator(sectionInfo("uptime")))
	e.must(e.Expect.Locator(pop).ToHaveText(regexp.MustCompile(`^What: per UTC day, of the blocks`)))
}

// TestPageValidatorPageSize checks the page's size: on a wide screen at
// most 1100 px wide and 90 % of the window's height, scrolling within; on a
// phone the whole screen, without a frame. The dashboard behind still
// scrolls.
func TestPageValidatorPageSize(t *testing.T) {
	onProfiles(t, []profile{desktop, tablet, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1), withURL("/?val="+opBravo))
		e.expectValidatorReport("bravo")
		page := e.Page.Locator("#val-page")
		e.must(e.Expect.Locator(page).ToHaveCSS("overflow-y", "auto"))
		e.must(e.Expect.Locator(e.Page.Locator("body")).ToHaveCSS("overflow", "visible"))
		switch p.name {
		case "desktop":
			e.must(e.Expect.Locator(page).ToHaveCSS("width", "1100px"))
			e.must(e.Expect.Locator(page).ToHaveCSS("max-height", "720px"))
			// A wheel over the backdrop scrolls the dashboard, the page open.
			e.must(e.Page.Locator("#val-modal").Hover(playwright.LocatorHoverOptions{Position: &playwright.Position{X: 20, Y: 20}}))
			e.must(e.Page.Mouse().Wheel(0, 300))
			e.must(e.Expect.Locator(e.Page.Locator("html")).ToHaveJSProperty("scrollTop", 300))
			e.must(e.expect("#val-modal").ToHaveClass(modalOpenClass))
		case "tablet":
			e.must(e.Expect.Locator(page).ToHaveCSS("width", "658px")) // 94 % of 700
		case "phone":
			width := e.numberOf(page, "() => innerWidth")
			height := e.numberOf(page, "() => innerHeight")
			e.must(e.Expect.Locator(page).ToHaveCSS("width", fmt.Sprintf("%gpx", width)))
			e.must(e.Expect.Locator(page).ToHaveCSS("height", fmt.Sprintf("%gpx", height)))
			e.must(e.Expect.Locator(page).ToHaveCSS("border-top-width", "0px"))
			e.must(e.Expect.Locator(page).ToHaveCSS("border-radius", "0px"))
			e.must(e.Expect.Locator(page).ToHaveCSS("padding", "16px"))
		}
	})
}

// TestPageValidatorStaysAsLoaded checks that an open page keeps its report
// as loaded: no read and no redraw as time passes, until it opens again.
func TestPageValidatorStaysAsLoaded(t *testing.T) {
	e := openPage(t, desktop, setChangesNetwork(t, 1), withInstalledClock())
	// The handler runs on playwright-go's one dispatch goroutine, which a
	// blocked send would stall for the whole suite: a read past the buffer
	// is counted instead, and fails the test.
	const kept = 16
	reads := make(chan string, kept)
	var overflow atomic.Int32
	e.Page.OnRequest(func(r playwright.Request) {
		if !strings.Contains(r.URL(), "/api/validator") {
			return
		}
		select {
		case reads <- r.URL():
		default:
			overflow.Add(1)
		}
	})
	t.Cleanup(func() {
		if n := overflow.Load(); n > 0 {
			t.Errorf("%d reads of a validator's report past the %d kept, want one per opening", n, kept)
		}
	})
	nextRead := func() string {
		t.Helper()
		select {
		case u := <-reads:
			return u
		case <-time.After(pageTimeout):
			t.Fatal("no read of a validator's report")
			return ""
		}
	}
	// The dashboard renders in animation frames, which wait on the clock.
	e.runFor(time.Second)
	e.press(e.validatorNameLink("bravo"))
	e.expectValidatorReport("bravo")
	if got := nextRead(); !strings.Contains(got, valBravo.address()) {
		t.Fatalf("first read %s, want bravo's", got)
	}
	row, err := e.Page.Locator("#val-body [data-section=timeline] > .vt-row").First().ElementHandle()
	e.must(err)
	e.runFor(3 * time.Minute)
	if connected, err := row.Evaluate("el => el.isConnected"); err != nil || connected != true {
		t.Errorf("the Timeline's first row is connected: %v (%v), want it kept 3 min on", connected, err)
	}

	// The next read is charlie's: none came for bravo in between.
	e.press(e.Page.Locator("#val-close"))
	e.expectValidatorPageClosed()
	e.press(e.validatorNameLink("charlie"))
	e.expectValidatorReport("charlie")
	if got := nextRead(); !strings.Contains(got, valCharlie.address()) {
		t.Errorf("read %s while bravo's page was open, want none before charlie's", got)
	}
}

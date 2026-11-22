//go:build page

package web

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/gnoverse/gnockpit/eta"
)

// ---- Halt card

// TestPageETAHaltCard checks the card of a halt scheduled on chain: its
// countdown, the halt and its minimum version, the estimate with its ranges,
// the estimate when the halt was first seen and the drift since, the pace,
// the scenarios and the chart; no link card, no reminder on it.
func TestPageETAHaltCard(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, haltedView(), "/")
		rep := e.etaAnswer("")
		const card = "#eta-halt-card"
		e.must(e.expect(card + ` h2[data-section="eta-halt"]`).ToHaveText("Next halt"))
		e.etaAssertCard(card, etaCard(t, rep, etaCardSpec{kind: "halt"}))
		e.etaAssertChart(card, rep)
		e.must(e.expect(card + " .eta-scen-title").ToHaveText("If the chain keeps the pace of…"))
		e.must(e.expect(card + " .eta-remind").ToHaveCount(0))
		e.must(e.expect("#eta-link-card").ToBeHidden())

		// The halt comes first, above the link card and Network State.
		e.must(e.expect("#eta-halt-card:first-child + #eta-link-card + .card:has(> #ns-header-row)").ToHaveCount(1))
	})
}

// TestPageETAHaltCardFollowsTheTracker checks that the halt card appears
// when a halt is scheduled and goes once the tracker drops it, at the next
// poll.
func TestPageETAHaltCardFollowsTheTracker(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, tr := openChainPage(t, p, runningChain, runningChain.view(fullHistory, nil, true), "/", withInstalledClock())
		e.must(e.expect("#eta-link-card").ToBeHidden())
		e.must(e.expect("#eta-halt-card").ToBeHidden())

		tr.set(haltedView())
		e.runFor(cardPoll)
		e.must(e.expect("#eta-halt-card .eta-target").ToHaveText("until halt #1,250,000 · min v1.8.0 (5,758 blocks to go)"))
		e.must(e.expect("#eta-halt-card").ToBeVisible())

		tr.set(runningChain.view(fullHistory, nil, true))
		e.runFor(cardPoll)
		e.must(e.expect("#eta-halt-card").ToBeHidden())
	})
}

// TestPageETAHaltReached checks the halt card once the chain reached the
// halt, until the tracker drops it: when it came, and how far that was from
// the estimate when the halt was first seen.
func TestPageETAHaltReached(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		halt := &eta.ScheduledHalt{Height: reachedHeight, MinVersion: "v1.8.0", FirstSeen: pageNow.Add(-48 * time.Hour)}
		e, _ := openChainPage(t, p, runningChain, runningChain.view(fullHistory, halt, true), "/")
		rep := e.etaAnswer("")
		const card = "#eta-halt-card"
		v := etaCard(t, rep, etaCardSpec{kind: "halt"})
		if v.inline != "reached · halt #1,240,000 · min v1.8.0 · Wed 18 Nov 06:01:19 UTC" || v.keys[1] != "original" {
			t.Fatalf("expected compact line %q and rows %q: the chain's block times moved", v.inline, v.keys)
		}
		e.etaAssertCard(card, v)
		e.must(e.expect(card).ToHaveClass(regexp.MustCompile(`\beta-reached\b`)))
		e.etaAssertChart(card, rep)
	})
}

// TestPageETAHaltWithoutEstimate checks a halt whose estimate fails: the
// card names the halt with the error in its body and on its compact line,
// still names it when a later answer is lost, and the planner says the halt
// is scheduled.
func TestPageETAHaltWithoutEstimate(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		v := runningChain.view(etaPacelessHistory, scheduledHalt("v1.8.0"), true)
		e, _ := openChainPage(t, p, runningChain, v, "/", withInstalledClock(), expectStatus("/api/eta", http.StatusServiceUnavailable))
		const card = "#eta-halt-card"
		named := func(msg string) {
			t.Helper()
			e.must(e.expect(card).ToBeVisible())
			e.must(e.expect(card).ToHaveClass(regexp.MustCompile(`\beta-failed\b`)))
			e.must(e.expect(card + " .eta-target").ToHaveText("halt #1,250,000 · min v1.8.0"))
			e.must(e.expect(card + " .eta-target").ToBeVisible())
			e.must(e.expect(card + " .kv .k").ToHaveText([]string{"error"}))
			e.must(e.expect(card + " .kv .v").ToHaveText([]string{msg}))
			e.must(e.expect(card + " .eta-count").ToBeHidden())
			e.must(e.expect(card + " .eta-inline").ToHaveText("halt #1,250,000 · min v1.8.0 · " + msg))
		}
		named("the block-time history is too short to measure the pace yet")
		e.openPlanner()
		e.must(e.expect("#eta-halt-row").ToHaveText(
			"On chain: halt scheduled at #1,250,000 (min v1.8.0). Its countdown shows for everyone, no link needed."))
		e.must(e.Page.Keyboard().Press("Escape"))

		lost := e.hold("/api/eta")
		lost.drop()
		e.runFor(cardPoll)
		lost.awaitFailure()
		named("gnockpit is unreachable")
		// Off the planner's button, whose tooltip covers the checkbox.
		e.must(e.Page.Locator("#page-title").Hover())
		e.must(e.Page.Locator(card + " .eta-compact-cb").Check())
		e.must(e.expect(card + " .eta-inline").ToBeVisible())
	})
}

// TestPageETAMovedHaltWithoutEstimate checks a halt that moves to a height
// whose estimate fails: the card drops the old halt's countdown and names
// the new halt with the error.
func TestPageETAMovedHaltWithoutEstimate(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, tr := openChainPage(t, p, runningChain, haltedView(), "/", withInstalledClock(), expectStatus("/api/eta", http.StatusServiceUnavailable))
		const card = "#eta-halt-card"
		e.must(e.expect(card + " .eta-target").ToHaveText("until halt #1,250,000 · min v1.8.0 (5,758 blocks to go)"))

		moved := &eta.ScheduledHalt{Height: 1_260_000, MinVersion: "v1.9.0", FirstSeen: pageNow.Add(-time.Hour)}
		tr.set(runningChain.view(etaPacelessHistory, moved, true))
		e.runFor(cardPoll)
		e.must(e.expect(card + " .eta-target").ToHaveText("halt #1,260,000 · min v1.9.0"))
		e.must(e.expect(card).ToHaveClass(regexp.MustCompile(`\beta-failed\b`)))
		e.must(e.expect(card + " .kv .v").ToHaveText([]string{"the block-time history is too short to measure the pace yet"}))
		e.must(e.expect(card + " .eta-count").ToBeHidden())
	})
}

// TestPageETASameHaltWithoutEstimate checks a halt whose estimate fails
// while the card shows an earlier one: the card keeps that answer, counting
// down, marked with the error.
func TestPageETASameHaltWithoutEstimate(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, tr := openChainPage(t, p, runningChain, haltedView(), "/", withInstalledClock(), expectStatus("/api/eta", http.StatusServiceUnavailable))
		const card = "#eta-halt-card"
		halt := e.etaAnswer("")
		e.etaAssertCard(card, etaCard(t, halt, etaCardSpec{kind: "halt"}))

		tr.set(runningChain.view(etaPacelessHistory, scheduledHalt("v1.8.0"), true))
		e.runFor(cardPoll)
		e.etaAssertCard(card, etaCard(t, halt, etaCardSpec{
			kind: "halt", elapsed: 10, stale: "the block-time history is too short to measure the pace yet",
		}))
		e.must(e.expect(card+" .eta-inline .warn.tap-popover").ToHaveAttribute("data-popover",
			"the block-time history is too short to measure the pace yet; showing the last answer"))
	})
}

// TestPageETARescheduledHaltWithoutEstimate checks a halt set again at the
// same height with a new minimum version, whose estimate fails: the card
// keeps its countdown but names the new version, as the planner does.
func TestPageETARescheduledHaltWithoutEstimate(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, tr := openChainPage(t, p, runningChain, haltedView(), "/", withInstalledClock(), expectStatus("/api/eta", http.StatusServiceUnavailable))
		const card = "#eta-halt-card"
		e.must(e.expect(card + " .eta-target").ToHaveText("until halt #1,250,000 · min v1.8.0 (5,758 blocks to go)"))

		tr.set(runningChain.view(etaPacelessHistory, scheduledHalt("v1.9.0"), true))
		e.runFor(cardPoll)
		e.must(e.expect(card + " .eta-target").ToHaveText("until halt #1,250,000 · min v1.9.0 (5,758 blocks to go)"))
		e.must(e.expect(card + " .eta-inline").ToContainText("halt #1,250,000 · min v1.9.0"))
		e.openPlanner()
		e.must(e.expect("#eta-halt-row").ToHaveText(
			"On chain: halt scheduled at #1,250,000 (min v1.9.0). Its countdown shows for everyone, no link needed."))
	})
}

// ---- Link card

// TestPageETALinkCard checks the card of the target in the page link: the
// height, the label capped at 40 characters and shown as text, the estimate
// as it stood at since, the warning of a halt before the target, and its
// copy and close buttons, named for what they do.
func TestPageETALinkCard(t *testing.T) {
	// 29 characters of markup, then 20 four-byte ones: the cap counts
	// characters, not UTF-16 units.
	label := "<img src=x onerror=alert(1)> " + strings.Repeat("🚀", 20)
	since := pageNow.Add(-6 * time.Hour)
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, haltedView(), cardLink(pastHaltHeight, since, label))
		rep := e.etaAnswer(etaLinkQuery(pastHaltHeight, since))
		if !strings.Contains(rep.Warning, "#1,250,000") && !strings.Contains(rep.Warning, "#1250000") {
			t.Fatalf("warning = %q, want the halt named", rep.Warning)
		}
		const card = "#eta-link-card"
		e.must(e.expect(card + ` h2[data-section="eta-link"]`).ToHaveText("Block ETA"))
		e.etaAssertCard(card, etaCard(t, rep, etaCardSpec{kind: "link", label: string([]rune(label)[:40])}))
		e.etaAssertChart(card, rep)
		e.must(e.expect(card + " img").ToHaveCount(0))
		e.must(e.expect(card+" .eta-copy").ToHaveAttribute("data-popover", "Copy the link to this countdown"))
		e.must(e.expect(card + " .eta-copy").ToHaveAccessibleName("Copy the link to this countdown"))
		e.must(e.expect(card+" .eta-close").ToHaveAttribute("data-popover", "Remove this countdown"))
		e.must(e.expect(card + " .eta-close").ToHaveAccessibleName("Remove this countdown"))
		e.must(e.expect("#eta-halt-card").ToBeVisible())
	})
}

// TestPageETALinkTargets checks what the link brings: a bare height shows
// its card without original or label, and an empty one no card.
func TestPageETALinkTargets(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, runningChain.view(fullHistory, nil, true), cardLink(soonHeight, time.Time{}, ""))
		rep := e.etaAnswer(etaLinkQuery(soonHeight, time.Time{}))
		e.etaAssertCard("#eta-link-card", etaCard(t, rep, etaCardSpec{kind: "link"}))
		e.etaAssertChart("#eta-link-card", rep)

		e.navigate("/?eta=")
		e.must(e.expect("#eta-halt-card").ToBeHidden())
		e.must(e.expect("#eta-link-card").ToBeHidden())
	})
}

// TestPageETALinkEqualsHalt checks that a link to the halt's height shows
// the halt card only.
func TestPageETALinkEqualsHalt(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, haltedView(), cardLink(haltHeight, pageNow.Add(-time.Hour), "upgrade"))
		e.must(e.expect("#eta-halt-card .eta-target").ToHaveText("until halt #1,250,000 · min v1.8.0 (5,758 blocks to go)"))
		e.must(e.expect("#eta-link-card").ToBeHidden())
	})
}

// TestPageETALinkErrors checks a link card whose first answer is an error:
// a height the server refuses, a produced block the RPC cannot date, and a
// server out of reach. The card shows the error alone, also on its compact
// line, and the countdown's tick leaves it so.
func TestPageETALinkErrors(t *testing.T) {
	for _, tc := range []struct {
		name, path, msg string
		status          int // the answer's code; 0 for none
		rpc             bool
	}{
		{
			name: "not a height", path: "/?eta=1,250,000", status: http.StatusBadRequest, rpc: true,
			msg: `height must be a positive block height, got "1,250,000"`,
		},
		{
			name: "undated block", path: cardLink(reachedHeight, time.Time{}, ""), status: http.StatusBadGateway,
			msg: "the RPC could not give the time of block #1240000",
		},
		{
			name: "unreachable", path: cardLink(soonHeight, time.Time{}, ""), rpc: true,
			msg: "gnockpit is unreachable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				sc, _ := chainScenario(runningChain, runningChain.view(fullHistory, nil, true))
				opts := []pageOption{noNavigate(), withInstalledClock()}
				if tc.status != 0 {
					opts = append(opts, expectStatus("/api/eta", tc.status))
				}
				if tc.rpc {
					serveBlockTimes(t, sc, runningChain)
				}
				e := openPage(t, p, sc, opts...)
				var dropped *heldRequest
				if tc.status == 0 {
					dropped = e.hold("/api/eta?height=")
					dropped.drop()
				}
				e.navigate(tc.path)
				if dropped != nil {
					dropped.awaitFailure()
				}
				const card = "#eta-link-card"
				e.expectFailedCard(card, tc.msg)
				e.runFor(2 * time.Second)
				e.expectFailedCard(card, tc.msg)

				e.must(e.Page.Locator(card + " .eta-compact-cb").Check())
				e.must(e.expect(card + " .eta-inline").ToBeVisible())
				e.must(e.expect(card + " .eta-inline").ToHaveText(tc.msg))
				e.must(e.expect(card + " .eta-bar").ToBeHidden())
			})
		})
	}
}

// TestPageETAUnreadableAnswer checks an answer that is not gnockpit's JSON,
// a proxy's 502 when gnockpit does not answer in time: the card says which
// status it got.
func TestPageETAUnreadableAnswer(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc, _ := chainScenario(runningChain, runningChain.view(fullHistory, nil, true))
		e := openPage(t, p, sc, noNavigate(), withInstalledClock(), expectStatus("/api/eta", http.StatusBadGateway))
		front := e.behindProxy("/api/eta", http.StatusBadGateway)
		e.hold("/api/eta?height=")
		e.navigateTo(front + cardLink(soonHeight, time.Time{}, ""))
		e.expectFailedCard("#eta-link-card", "unreadable answer (HTTP 502)")
	})
}

// TestPageETALinkReached checks the card of a produced block: when it came,
// how far that was from the estimate at since, no pace, range or
// scenarios, and the chart ending on it.
func TestPageETALinkReached(t *testing.T) {
	since := pageNow.Add(-7 * time.Hour)
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, runningChain.view(fullHistory, nil, true), cardLink(reachedHeight, since, "v1.7"))
		rep := e.etaAnswer(etaLinkQuery(reachedHeight, since))
		const card = "#eta-link-card"
		v := etaCard(t, rep, etaCardSpec{kind: "link", label: "v1.7"})
		if v.inline != "reached · #1,240,000 · v1.7 · Wed 18 Nov 06:01:19 UTC" {
			t.Fatalf("expected compact line %q: the chain's block time moved", v.inline)
		}
		e.etaAssertCard(card, v)
		e.must(e.expect(card).ToHaveClass(regexp.MustCompile(`\beta-reached\b`)))
		e.must(e.expect(card + " .eta-scen-title").ToBeHidden())
		e.etaAssertChart(card, rep)

		e.must(e.Page.Locator(card + " .eta-compact-cb").Check())
		e.must(e.expect(card + " .eta-inline").ToBeVisible())
		e.must(e.expect(card + " .eta-bar").ToBeHidden())

		// A block produced two days ago: the chart's end names its day.
		old := chainTip - 40_000
		e.must(e.Page.Locator(card + " .eta-compact-cb").Uncheck())
		e.navigate(cardLink(old, pageNow.Add(-3*24*time.Hour), ""))
		rep = e.etaAnswer(etaLinkQuery(old, pageNow.Add(-3*24*time.Hour)))
		e.etaAssertCard(card, etaCard(t, rep, etaCardSpec{kind: "link"}))
		e.etaAssertChart(card, rep)
		e.must(e.Expect.Locator(e.Page.Locator(card + " .eta-chart svg text").Last()).ToHaveText(regexp.MustCompile(`^reached Mon 16 Nov \d\d:\d\d$`)))
	})
}

// TestPageETAStalledChain checks a target whose ETA passed while the chain
// produced nothing: the count says due and the card waits for the block.
func TestPageETAStalledChain(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, etaStalledChain, etaStalledChain.view(fullHistory, nil, true), cardLink(chainTip+1, time.Time{}, ""))
		rep := e.etaAnswer(etaLinkQuery(chainTip+1, time.Time{}))
		if *rep.SecondsLeft != 0 || rep.ReachedAt != "" {
			t.Fatalf("seconds_left %d, reached_at %q: want a passed ETA not reached", *rep.SecondsLeft, rep.ReachedAt)
		}
		v := etaCard(t, rep, etaCardSpec{kind: "link"})
		if v.count != "due" || v.target != "#1,244,243 · ETA passed, waiting for the block" {
			t.Fatalf("expected count %q, target %q", v.count, v.target)
		}
		e.etaAssertCard("#eta-link-card", v)
	})
}

// TestPageETAPendingRange checks a card whose history is too short for a
// margin and still loading: the likely row says the range is pending, the
// history row how far back it reaches, and neither the compact bar nor the
// chart's cone is drawn.
func TestPageETAPendingRange(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		v := runningChain.view(etaShortHistory, nil, false)
		e, _ := openChainPage(t, p, runningChain, v, cardLink(soonHeight, time.Time{}, ""))
		rep := e.etaAnswer(etaLinkQuery(soonHeight, time.Time{}))
		if rep.Range80 != nil {
			t.Fatalf("range_80 = %+v, want none from a short history", rep.Range80)
		}
		const card = "#eta-link-card"
		e.etaAssertCard(card, etaCard(t, rep, etaCardSpec{kind: "link"}))
		e.must(e.expect(card).ToHaveClass(regexp.MustCompile(`\beta-norange\b`)))
		e.etaAssertChart(card, rep)

		e.must(e.Page.Locator(card + " .eta-compact-cb").Check())
		e.must(e.expect(card + " .eta-inline").ToBeVisible())
		e.must(e.expect(card + " .eta-bar").ToBeHidden())
		e.must(e.expect(card + " .eta-range").ToBeEmpty())
	})
}

// TestPageETATimezones checks that a viewer off UTC also reads the times on
// their own clock, with the day when it differs from the UTC one; ranges,
// originals and the compact line stay in UTC.
func TestPageETATimezones(t *testing.T) {
	paris, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Fatal(err)
	}
	// Its ETA is 23:57 UTC, 00:57 the next day in Paris; its 80% range
	// spans midnight.
	const late int64 = 1_252_700
	since := pageNow.Add(-6 * time.Hour)
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, haltedView(), cardLink(late, since, ""), withTimezone("Europe/Paris"))
		halt := etaCard(t, e.etaAnswer(""), etaCardSpec{kind: "halt", loc: paris})
		link := etaCard(t, e.etaAnswer(etaLinkQuery(late, since)), etaCardSpec{kind: "link", loc: paris})
		if halt.values[0] != "Wed 18 Nov 20:08:24 UTC (21:08 your time)" || link.values[0] != "Wed 18 Nov 23:57:27 UTC (00:57 Thu your time)" ||
			link.values[1] != "Wed 18 Nov 23:37–Thu 19 Nov 00:17 UTC (80%) · Wed 18 Nov 23:36–Thu 19 Nov 00:18 (95%)" {
			t.Fatalf("expected rows %q and %q: the chain's block times moved", halt.values, link.values)
		}
		e.etaAssertCard("#eta-halt-card", halt)
		e.etaAssertCard("#eta-link-card", link)
	})
}

// TestPageETACountdown checks that the count ticks down each second from
// the server's seconds left, by days past one, and turns to due with the
// card waiting for the block once they run out.
func TestPageETACountdown(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		next := chainTip + 1
		e, _ := openChainPage(t, p, runningChain, haltedView(), cardLink(next, time.Time{}, ""), withInstalledClock())
		halt := e.etaAnswer("")
		link := e.etaAnswer(etaLinkQuery(next, time.Time{}))
		if left := *link.SecondsLeft; left != 3 {
			t.Fatalf("seconds_left = %d to the next block, want 3", left)
		}
		e.etaAssertCard("#eta-halt-card", etaCard(t, halt, etaCardSpec{kind: "halt"}))
		v := etaCard(t, link, etaCardSpec{kind: "link"})
		if v.target != "until #1,244,243 (1 block to go)" {
			t.Fatalf("expected target %q", v.target)
		}
		e.etaAssertCard("#eta-link-card", v)

		for elapsed := int64(1); elapsed <= 3; elapsed++ {
			e.runFor(time.Second)
			e.must(e.expect("#eta-halt-card .eta-count").ToHaveText(etaCountdown(*halt.SecondsLeft - elapsed)))
			e.etaAssertCard("#eta-link-card", etaCard(t, link, etaCardSpec{kind: "link", elapsed: elapsed}))
		}
		e.must(e.expect("#eta-link-card .eta-count").ToHaveText("due"))
		e.must(e.expect("#eta-link-card .eta-target").ToHaveText("#1,244,243 · ETA passed, waiting for the block"))

		far := chainTip + 60_000
		e.navigate(cardLink(far, time.Time{}, ""))
		left := *e.etaAnswer(etaLinkQuery(far, time.Time{})).SecondsLeft
		if left < 2*86400 {
			t.Fatalf("seconds_left = %d, want days", left)
		}
		e.must(e.expect("#eta-link-card .eta-count").ToHaveText(etaCountdown(left)))
		e.must(e.expect("#eta-link-card .eta-count").ToHaveText(regexp.MustCompile(`^\d+d \d\d:\d\d:\d\d$`)))
	})
}

// TestPageETAScenarioChips checks the scenario chips: the current pace
// first, then each look-back span's pace with its ETA and the offset from
// the current one, grey within a minute, yellow later, blue earlier; two to
// a row on a narrow screen.
func TestPageETAScenarioChips(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		drifts := map[string]bool{}
		// The next block's scenarios round to its ETA; 300 blocks ahead
		// they differ by seconds, the halt's by minutes.
		e, _ := openChainPage(t, p, runningChain, haltedView(), "/")
		for _, h := range []int64{chainTip + 1, chainTip + 300} {
			e.navigate(cardLink(h, time.Time{}, ""))
			for _, card := range []struct {
				sel   string
				query string
				kind  string
			}{{"#eta-halt-card", "", "halt"}, {"#eta-link-card", etaLinkQuery(h, time.Time{}), "link"}} {
				v := etaCard(t, e.etaAnswer(card.query), etaCardSpec{kind: card.kind})
				e.etaAssertCard(card.sel, v)
				for _, c := range v.chips[1:] {
					drifts[regexp.MustCompile(`\d+`).ReplaceAllString(c.d, "N")+" "+c.color] = true
				}
			}
			display := "flex"
			if e.narrow() {
				display = "grid"
			}
			e.must(e.expect("#eta-link-card .eta-scen").ToHaveCSS("display", display))
		}
		for _, want := range []string{"±Ns fg2", "−Ns fg2", "+Nm Ns yellow", "−Nm Ns blue"} {
			if !drifts[want] {
				t.Errorf("no chip offset of the form %q among %v", want, drifts)
			}
		}
	})
}

// ---- Polling

// TestPageETAPollingSurvivesABadAnswer checks that the cards keep polling
// through failed answers: a card with an answer keeps it, counting down,
// marked with the error until a poll brings a good answer again.
func TestPageETAPollingSurvivesABadAnswer(t *testing.T) {
	since := pageNow.Add(-time.Hour)
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, haltedView(), cardLink(soonHeight, since, ""),
			withInstalledClock(), expectStatus("/api/eta", http.StatusServiceUnavailable))
		halt := e.etaAnswer("")
		link := e.etaAnswer(etaLinkQuery(soonHeight, since))
		const haltCard = "#eta-halt-card"
		e.etaAssertCard(haltCard, etaCard(t, halt, etaCardSpec{kind: "halt"}))
		e.etaAssertCard(linkCard, etaCard(t, link, etaCardSpec{kind: "link"}))

		// Every source down: both answers fail, the cards keep theirs.
		e.publish(sourcesDown())
		e.runFor(cardPoll)
		const down = "no chain status yet"
		e.etaAssertCard(haltCard, etaCard(t, halt, etaCardSpec{kind: "halt", elapsed: 10, stale: down}))
		e.etaAssertCard(linkCard, etaCard(t, link, etaCardSpec{kind: "link", elapsed: 10, stale: down}))
		e.must(e.expect(linkCard+" .eta-inline .warn.tap-popover").ToHaveAttribute("data-popover", down+"; showing the last answer"))

		// Back up, but the halt's answer is lost on the way: the link card
		// recovers, the halt card stays marked. The server's clock runs with
		// the page's: the answers a poll gets are the server's as of then.
		e.publish(runningChain.snapshot())
		lost := e.hold("/api/eta")
		lost.drop()
		e.runFor(cardPoll)
		lost.awaitFailure()
		e.etaAssertCard(linkCard, etaCard(t, e.etaAnswer(etaLinkQuery(soonHeight, since)), etaCardSpec{kind: "link"}))
		e.must(e.expect(linkCard + " .eta-inline .warn").ToHaveCount(0))
		e.etaAssertCard(haltCard, etaCard(t, halt, etaCardSpec{kind: "halt", elapsed: 20, stale: "gnockpit is unreachable"}))

		e.runFor(cardPoll)
		e.etaAssertCard(haltCard, etaCard(t, e.etaAnswer(""), etaCardSpec{kind: "halt"}))
		e.etaAssertCard(linkCard, etaCard(t, e.etaAnswer(etaLinkQuery(soonHeight, since)), etaCardSpec{kind: "link"}))
	})
}

// TestPageETAPollingSurvivesAnUnansweredRead checks that a read of the ETA
// gnockpit never answers is given up after 10 s: the card keeps its answer,
// marked "no answer in 10 s", and the polls go on: the next one brings the
// server's answer again.
func TestPageETAPollingSurvivesAnUnansweredRead(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, haltedView(), "/", withInstalledClock())
		const haltCard = "#eta-halt-card"
		halt := e.etaAnswer("")
		e.etaAssertCard(haltCard, etaCard(t, halt, etaCardSpec{kind: "halt"}))

		stuck := e.hold("/api/eta")
		e.runFor(cardPoll)
		e.must(e.gate.awaitHeld("/api/eta", 1))
		e.runFor(etaReadBound)
		stuck.awaitAbandoned()
		e.etaAssertCard(haltCard, etaCard(t, halt, etaCardSpec{kind: "halt", elapsed: 20, stale: "no answer in 10 s"}))

		e.runFor(cardPoll)
		e.etaAssertCard(haltCard, etaCard(t, e.etaAnswer(""), etaCardSpec{kind: "halt"}))
	})
}

// TestPageETANewTargetStartsAfresh checks that a new link target drops the
// old answer: its first error shows alone, not as a stale mark on the old
// target's card.
func TestPageETANewTargetStartsAfresh(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, haltedView(), cardLink(soonHeight, time.Time{}, ""),
			expectStatus("/api/eta", http.StatusServiceUnavailable))
		e.must(e.expect("#eta-link-card .eta-target").ToHaveText("until #1,244,542 (300 blocks to go)"))
		e.openPlanner()
		e.plan("1252000")
		e.must(e.expect("#eta-go").ToBeEnabled())

		e.publish(sourcesDown())
		e.must(e.Page.Locator("#eta-go").Click())
		e.expectFailedCard("#eta-link-card", "no chain status yet")
	})
}

// TestPageETATargetChangeDropsTheOldAnswer checks that an answer for the
// link's previous target, still in flight when the target changes, does not
// replace the new target's card.
func TestPageETATargetChangeDropsTheOldAnswer(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, runningChain.view(fullHistory, nil, true), cardLink(soonHeight, time.Time{}, ""), withInstalledClock())
		e.must(e.expect("#eta-link-card .eta-target").ToHaveText("until #1,244,542 (300 blocks to go)"))
		old := e.hold("/api/eta?height=1244542")
		e.runFor(cardPoll)

		e.openPlanner()
		e.plan("1252000")
		e.runFor(etaDebounce)
		e.must(e.expect("#eta-go").ToBeEnabled())
		e.must(e.Page.Locator("#eta-go").Click())
		target := "until #1,252,000 (7,758 blocks to go)"
		e.must(e.expect("#eta-link-card .eta-target").ToHaveText(target))

		if _, err := e.Page.ExpectRequestFinished(func() error { old.release(); return nil }, playwright.PageExpectRequestFinishedOptions{
			Predicate: func(r playwright.Request) bool { return strings.Contains(r.URL(), "height=1244542") },
		}); err != nil {
			t.Fatalf("release the old target's answer: %v", err)
		}
		e.must(e.expect("#eta-link-card .eta-target").ToHaveText(target))
	})
}

// ---- Card controls

// TestPageETACompact checks the compact toggle: the card shrinks to its
// line and its range bar, the choice is kept per card across loads, and
// the bar's marks name their times.
func TestPageETACompact(t *testing.T) {
	since := pageNow.Add(-time.Hour)
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, runningChain.view(fullHistory, nil, true), cardLink(soonHeight, since, ""))
		rep := e.etaAnswer(etaLinkQuery(soonHeight, since))
		const card = "#eta-link-card"
		cb := e.Page.Locator(card + " .eta-compact-cb")
		e.must(e.expect(card + " .eta-actions label").ToHaveText("compact"))
		e.must(e.expect(card + " .section-body").ToBeVisible())
		e.must(e.expect(card + " .eta-inline").ToBeHidden())
		e.must(e.expect(card + " .eta-bar").ToBeHidden())
		if v, ok := e.storage("eta-compact-link"); !ok || v != "0" {
			t.Errorf("eta-compact-link = %q (set %t) after the first load, want 0", v, ok)
		}

		e.must(cb.Check())
		e.must(e.expect(card).ToHaveClass(regexp.MustCompile(`\bcompact\b`)))
		e.must(e.expect(card + " .section-body").ToBeHidden())
		e.must(e.expect(card + " .eta-inline").ToBeVisible())
		e.must(e.expect(card + " .eta-inline").ToHaveText(etaCard(t, rep, etaCardSpec{kind: "link"}).inline))
		e.must(e.expect(card + " .eta-bar").ToBeVisible())
		if bar := e.etaAssertBar(card, rep); len(bar.labels) != 3 {
			t.Fatalf("bar labels %q: want both range ends labelled", bar.labels)
		}
		if v, _ := e.storage("eta-compact-link"); v != "1" {
			t.Errorf("eta-compact-link = %q after checking compact, want 1", v)
		}
		wrap := "nowrap"
		if e.narrow() {
			wrap = "normal"
		}
		e.must(e.expect(card+" .eta-inline").ToHaveCSS("white-space", wrap))
		if p.name == desktop.name {
			e.must(e.Page.Locator(card + " .eta-range .now").Hover())
			e.must(e.expect("#popover").ToHaveText("ETA " + etaHMS(etaTime(t, rep.ETA)) + " UTC"))
			e.must(e.Page.Locator(card + " .eta-range .orig").Hover())
			e.must(e.expect("#popover").ToHaveText("original ETA " + etaHMS(etaTime(t, rep.Original.ETA)) + " UTC"))
		}

		// Kept across loads, for this card only; a link without since has
		// no original on its bar.
		e.navigate(cardLink(soonHeight, time.Time{}, ""))
		e.must(e.expect(card).ToHaveClass(regexp.MustCompile(`\bcompact\b`)))
		e.must(e.expect(card + " .eta-compact-cb").ToBeChecked())
		e.etaAssertBar(card, e.etaAnswer(etaLinkQuery(soonHeight, time.Time{})))
		if v, _ := e.storage("eta-compact-halt"); v != "0" {
			t.Errorf("eta-compact-halt = %q, want 0", v)
		}

		// Estimated 6 hours ago, the original lies 10 minutes off: the
		// range ends sit too close to the ETA for their labels.
		long := pageNow.Add(-6 * time.Hour)
		e.navigate(cardLink(soonHeight, long, ""))
		if bar := e.etaAssertBar(card, e.etaAnswer(etaLinkQuery(soonHeight, long))); len(bar.labels) != 1 {
			t.Fatalf("bar labels %q: want the ETA's alone", bar.labels)
		}

		e.must(cb.Uncheck())
		e.must(e.expect(card).Not().ToHaveClass(regexp.MustCompile(`\bcompact\b`)))
		e.must(e.expect(card + " .section-body").ToBeVisible())
		e.must(e.expect(card + " .eta-bar").ToBeHidden())
		e.must(e.expect(card + " .eta-chart svg polyline.line").ToHaveCount(1))
		if v, _ := e.storage("eta-compact-link"); v != "0" {
			t.Errorf("eta-compact-link = %q after unchecking compact, want 0", v)
		}
	})
}

// TestPageETACollapse checks that a click on the card's title folds its
// body and bar, not its compact line, keeps the choice across loads, and
// that unfolding draws what a folded card skipped.
func TestPageETACollapse(t *testing.T) {
	since := pageNow.Add(-time.Hour)
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		path := cardLink(soonHeight, since, "")
		e, _ := openChainPage(t, p, runningChain, runningChain.view(fullHistory, nil, true), path)
		rep := e.etaAnswer(etaLinkQuery(soonHeight, since))
		const card = "#eta-link-card"
		h2 := e.Page.Locator(card + " h2")
		key := e.etaCollapseKey()
		e.must(e.expect(card + " .eta-chart svg polyline.line").ToHaveCount(1))

		e.must(h2.Click())
		e.must(e.expect(card + " h2").ToHaveClass(regexp.MustCompile(`\bcollapsed\b`)))
		e.must(e.expect(card + " .section-body").ToBeHidden())
		if v, _ := e.storage(key); v != `{"eta-link":true}` {
			t.Errorf("%s = %q, want the link card collapsed", key, v)
		}

		// Folded at load, the chart is not drawn; unfolding draws it.
		e.navigate(path)
		e.must(e.expect(card + " h2").ToHaveClass(regexp.MustCompile(`\bcollapsed\b`)))
		e.must(e.expect(card + " .eta-target").ToHaveText(etaCard(t, rep, etaCardSpec{kind: "link"}).target))
		e.must(e.expect(card + " .eta-chart svg polyline.line").ToHaveCount(0))
		e.must(h2.Click())
		e.must(e.expect(card + " .section-body").ToBeVisible())
		e.must(e.expect(card + " .eta-chart svg polyline.line").ToHaveCount(1))
		if v, _ := e.storage(key); v != "{}" {
			t.Errorf("%s = %q, want nothing collapsed", key, v)
		}

		// Folded and compact: the line stays, the bar goes, and a bar
		// drawn folded at load gets its labels on unfolding.
		e.must(e.Page.Locator(card + " .eta-compact-cb").Check())
		e.must(h2.Click())
		e.must(e.expect(card + " .eta-inline").ToBeVisible())
		e.must(e.expect(card + " .eta-bar").ToBeHidden())
		e.navigate(path)
		e.must(e.expect(card + " .eta-range .lbl").ToHaveText([]string{etaHMS(etaTime(t, rep.ETA))}))
		e.must(h2.Click())
		e.must(e.expect(card + " .eta-bar").ToBeVisible())
		e.etaAssertBar(card, rep)
	})
}

// TestPageETACopyLink checks that the copy button copies the page link,
// card target included, and says so until its toast fades.
func TestPageETACopyLink(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		path := cardLink(soonHeight, pageNow.Add(-time.Hour), "v1.8")
		e, _ := openChainPage(t, p, runningChain, runningChain.view(fullHistory, nil, true), path, withInstalledClock())
		e.must(e.expect("#eta-link-card .eta-copy").ToHaveText("⧉ link"))
		e.must(e.Page.Locator("#eta-link-card .eta-copy").Click())
		e.must(e.expect(".copy-toast").ToHaveText("Copied!"))
		// The page's opacity, not the computed one: the toast fades in and
		// out through a transition, which the timer can cut before 1.
		e.must(e.expect(".copy-toast").ToHaveAttribute("style", toastOpaque))
		// Only Chromium is granted reading the clipboard.
		if p.browser == chromium {
			if got := e.clipboard(); got != e.ts.URL+path {
				t.Errorf("clipboard = %q, want %q", got, e.ts.URL+path)
			}
		}
		fadeToast(e)
		e.must(e.expect(".copy-toast").ToHaveCSS("opacity", "0"))
	})
}

// TestPageETAClose checks that closing the link card removes its target
// from the link, keeping the rest of it, without a history entry.
func TestPageETAClose(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, runningChain.view(fullHistory, nil, true),
			"/?foo=1&eta=1244542&since=2026-11-18T11%3A00%3A00Z&label=v1.8#top")
		e.must(e.expect("#eta-link-card").ToBeVisible())
		before := e.historyLength()
		e.must(e.Page.Locator("#eta-link-card .eta-close").Click())
		e.must(e.expect("#eta-link-card").ToBeHidden())
		e.must(e.Expect.Page(e.Page).ToHaveURL(e.ts.URL + "/?foo=1#top"))
		if after := e.historyLength(); after != before {
			t.Errorf("history.length = %d after closing, want %d", after, before)
		}
	})
}

// TestPageETAResizeRedraws checks that the chart is drawn again at the new
// width when the window is resized.
func TestPageETAResizeRedraws(t *testing.T) {
	e, _ := openChainPage(t, desktop, runningChain, haltedView(), "/")
	svg := e.Page.Locator("#eta-halt-card .eta-chart svg")
	viewBox := func() string {
		w, err := svg.Evaluate(`el => el.getBoundingClientRect().width`, nil)
		if err != nil {
			t.Fatalf("read the chart's width: %v", err)
		}
		return fmt.Sprintf("0 0 %v 170", w)
	}
	e.must(e.Expect.Locator(svg).ToHaveAttribute("viewBox", viewBox()))
	wide := viewBox()
	e.must(e.Page.SetViewportSize(1000, 800))
	narrow := viewBox()
	if narrow == wide {
		t.Fatalf("chart width unchanged by the resize: %s", wide)
	}
	e.must(e.Expect.Locator(svg).ToHaveAttribute("viewBox", narrow))
}

// TestPageETAChartUnderTheFiguresOnNarrowScreens checks where a card's chart
// sits: beside its figures on a screen wider than 860 px, under them at
// 860 px and less, widths between the tablet's and the desktop's.
func TestPageETAChartUnderTheFiguresOnNarrowScreens(t *testing.T) {
	e, _ := openChainPage(t, desktop, runningChain, haltedView(), "/")
	grid := e.expect("#eta-halt-card .eta-grid")
	beside, under := regexp.MustCompile(`^[\d.]+px [\d.]+px$`), regexp.MustCompile(`^[\d.]+px$`)
	e.must(grid.ToHaveCSS("grid-template-columns", beside))
	e.must(e.Page.SetViewportSize(861, 800))
	e.must(grid.ToHaveCSS("grid-template-columns", beside))
	e.must(e.Page.SetViewportSize(860, 800))
	e.must(grid.ToHaveCSS("grid-template-columns", under))
}

// TestPageETACountShowsDueAtZero checks that the count says due as soon as
// it would read 00:00:00: an answer landing between two ticks leaves a last
// fraction of a second that rounds to zero.
func TestPageETACountShowsDueAtZero(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		next := chainTip + 1
		sc, _ := chainScenario(runningChain, runningChain.view(fullHistory, nil, true))
		serveBlockTimes(t, sc, runningChain)
		e := openPage(t, p, sc, noNavigate(), withInstalledClock())
		first := e.hold("/api/eta?height=")
		e.navigate(cardLink(next, time.Time{}, ""))
		// The ticks run on whole seconds from the load; the answer lands
		// 300 ms after one.
		e.runFor(300 * time.Millisecond)
		first.release()
		const card = "#eta-link-card"
		if left := *e.etaAnswer(etaLinkQuery(next, time.Time{})).SecondsLeft; left != 3 {
			t.Fatalf("seconds_left = %d to the next block, want 3", left)
		}
		e.must(e.expect(card + " .eta-count").ToHaveText("00:00:03"))
		e.runFor(700 * time.Millisecond)
		e.must(e.expect(card + " .eta-count").ToHaveText("00:00:02"))
		e.runFor(time.Second)
		e.must(e.expect(card + " .eta-count").ToHaveText("00:00:01"))
		e.runFor(time.Second)
		e.must(e.expect(card + " .eta-count").ToHaveText("due"))
		e.must(e.expect(card + " .eta-target").ToHaveText("#1,244,243 · ETA passed, waiting for the block"))
		e.must(e.expect(card + " .eta-inline").ToHaveText("due · #1,244,243 · Wed 18 Nov 12:00 UTC"))
	})
}

// TestPageETACompactWarning checks that a compact card carries the
// server's warning as its ⚠ mark, the warning in its popover, joined by the
// error of a failed poll.
func TestPageETACompactWarning(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, haltedView(), cardLink(pastHaltHeight, time.Time{}, ""),
			withInstalledClock(), expectStatus("/api/eta", http.StatusServiceUnavailable))
		rep := e.etaAnswer(etaLinkQuery(pastHaltHeight, time.Time{}))
		const card = "#eta-link-card"
		mark := card + " .eta-inline .warn.tap-popover"
		e.must(e.Page.Locator(card + " .eta-compact-cb").Check())
		e.must(e.expect(mark).ToBeVisible())
		e.must(e.expect(mark).ToHaveText("⚠"))
		e.must(e.expect(mark).ToHaveAttribute("data-popover", rep.Warning))
		e.etaAssertCard(card, etaCard(t, rep, etaCardSpec{kind: "link"}))
		e.point(e.Page.Locator(mark))
		e.must(e.expect("#popover").ToBeVisible())
		e.must(e.expect("#popover").ToHaveText(rep.Warning))

		e.publish(sourcesDown())
		e.runFor(cardPoll)
		e.must(e.expect(mark).ToHaveAttribute("data-popover", rep.Warning+" · no chain status yet; showing the last answer"))
	})
}

// TestPageETAChipsNameTheDay checks an ETA days away: the scenario chips
// and the chart's end name its day, and the offsets run in hours.
func TestPageETAChipsNameTheDay(t *testing.T) {
	far := chainTip + 60_000
	hours := regexp.MustCompile(`^[+−]\d+h \d\dm$`)
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, runningChain.view(fullHistory, nil, true), cardLink(far, time.Time{}, ""))
		rep := e.etaAnswer(etaLinkQuery(far, time.Time{}))
		v := etaCard(t, rep, etaCardSpec{kind: "link"})
		if v.chips[0].v != "Sun 22 Nov 00:49:42" || !slices.ContainsFunc(v.chips, func(c etaChip) bool { return hours.MatchString(c.d) }) {
			t.Fatalf("expected chips %+v: want Sun 22 Nov first and an offset in hours", v.chips)
		}
		const card = "#eta-link-card"
		e.etaAssertCard(card, v)
		e.etaAssertChart(card, rep)
		e.must(e.Expect.Locator(e.Page.Locator(card + " .eta-chart svg text").Last()).ToHaveText("ETA Sun 22 Nov 00:49"))
	})
}

// TestPageETAChipsTodayIsTheServers checks that today is the server's day:
// with the browser's clock already on the next UTC day, an ETA on the
// server's day shows without its day.
func TestPageETAChipsTodayIsTheServers(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc, _ := chainScenario(runningChain, runningChain.view(fullHistory, nil, true))
		e := openPage(t, p, sc, noNavigate(), withBrowserOffset(13*time.Hour))
		// The card waits for the server's time, which the WebSocket brings.
		answer := e.hold("/api/eta?height=")
		e.navigate(cardLink(pastHaltHeight, time.Time{}, ""))
		e.must(e.expect("#gc-drift").ToContainText("46800s"))
		answer.release()
		rep := e.etaAnswer(etaLinkQuery(pastHaltHeight, time.Time{}))
		v := etaCard(t, rep, etaCardSpec{kind: "link"})
		if v.chips[0].v != "22:58:04" {
			t.Fatalf("expected chips %+v: want the ETA at 22:58:04 on the server's day", v.chips)
		}
		e.etaAssertCard("#eta-link-card", v)
		e.etaAssertChart("#eta-link-card", rep)
	})
}

// TestPageETAStaleMarkKeepsItsPopover checks that the ticks leave the
// compact line's ⚠ in place: its popover, opened by a tap, stays open below
// it, and a second tap closes it.
func TestPageETAStaleMarkKeepsItsPopover(t *testing.T) {
	e, _ := openChainPage(t, phone, runningChain, runningChain.view(fullHistory, nil, true), cardLink(soonHeight, time.Time{}, ""),
		withInstalledClock(), expectStatus("/api/eta", http.StatusServiceUnavailable))
	const card = "#eta-link-card"
	mark := e.Page.Locator(card + " .eta-inline .warn.tap-popover")
	e.must(e.Page.Locator(card + " .eta-compact-cb").Check())
	e.publish(sourcesDown())
	e.runFor(cardPoll)
	e.must(e.Expect.Locator(mark).ToBeVisible())

	e.must(mark.Tap())
	e.must(e.expect("#popover").ToHaveText("no chain status yet; showing the last answer"))
	e.runFor(3 * time.Second)
	e.must(e.expect("#popover").ToBeVisible())
	e.etaAssertBelow("#popover", mark)
	e.must(mark.Tap())
	e.must(e.expect("#popover").ToBeHidden())
}

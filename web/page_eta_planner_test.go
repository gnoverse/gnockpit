//go:build page

package web

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/gnoverse/gnockpit/eta"
	"github.com/gnoverse/gnockpit/node"
)

// TestPagePlannerOpenClose checks the header's planner button, the planner
// it opens, a modal dialog named by its title with its note on the link, and
// the ways to close it, each giving the focus back to the button.
func TestPagePlannerOpenClose(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, haltedView(), "/")
		btn := e.expect("#eta-btn")
		// The text as shown: the phone hides the button's first word.
		shown := playwright.LocatorAssertionsToHaveTextOptions{UseInnerText: playwright.Bool(true)}
		e.must(btn.ToHaveAttribute("data-popover", "When will a block be produced, or which block at a given time"))
		e.must(btn.ToHaveAttribute("aria-label", "Block ETA"))
		if p.name == phone.name {
			e.must(e.expect("#eta-btn .long").ToBeHidden())
			e.must(btn.ToHaveText("ETA", shown))
		} else {
			e.must(btn.ToHaveText("Block ETA", shown))
		}
		e.must(e.expect("#eta-modal").ToBeHidden())

		closed := func() {
			t.Helper()
			e.must(e.expect("#eta-modal").ToBeHidden())
			e.must(e.expect("#eta-modal").Not().ToHaveClass(regexp.MustCompile(`\bopen\b`)))
			e.must(btn.ToBeFocused())
		}
		e.openPlanner()
		dialog := e.Page.GetByRole("dialog", playwright.PageGetByRoleOptions{Name: "Block ETA", Exact: playwright.Bool(true)})
		e.must(e.Expect.Locator(dialog).ToHaveAttribute("aria-modal", "true"))
		e.must(e.expect("#eta-modal-title").ToHaveText("Block ETA"))
		e.must(e.Expect.Locator(dialog.Locator(".eta-note")).ToHaveText("The countdown lives in the page link: share the link and " +
			"everyone opening it sees the same card, with the original estimate kept for comparison."))
		e.must(e.expect("#eta-in").ToBeFocused())
		e.must(e.expect("#eta-result").ToHaveText(etaPlanHeightEmpty))
		e.must(e.expect("#eta-go").ToBeDisabled())
		e.must(dialog.GetByRole("button", playwright.LocatorGetByRoleOptions{Name: "Close", Exact: playwright.Bool(true)}).Click())
		closed()

		e.openPlanner()
		e.must(e.Page.Locator("#eta-modal").Click(playwright.LocatorClickOptions{Position: &playwright.Position{X: 5, Y: 5}}))
		closed()

		e.openPlanner()
		e.must(e.Page.Locator("#eta-modal .eta-modal-inner").Click())
		e.must(e.expect("#eta-modal").ToHaveClass(regexp.MustCompile(`\bopen\b`)))
		e.must(e.Page.Keyboard().Press("Escape"))
		closed()
	})
}

// TestPagePlannerFocusTrap checks that Tab and Shift+Tab stay in the
// planner, skipping Show countdown while it is disabled.
func TestPagePlannerFocusTrap(t *testing.T) {
	e, _ := openChainPage(t, desktop, runningChain, haltedView(), "/")
	e.openPlanner()
	closeBtn := e.Page.Locator("#eta-modal .eta-modal-head button")
	kb := e.Page.Keyboard()

	e.must(e.Page.Locator("#eta-label-in").Focus())
	e.must(kb.Press("Tab"))
	e.must(e.Expect.Locator(closeBtn).ToBeFocused())
	e.must(kb.Press("Shift+Tab"))
	e.must(e.expect("#eta-label-in").ToBeFocused())

	e.plan("1252000")
	e.must(e.expect("#eta-go").ToBeEnabled())
	e.must(e.Page.Locator("#eta-label-in").Focus())
	e.must(kb.Press("Tab"))
	e.must(e.expect("#eta-go").ToBeFocused())
	e.must(kb.Press("Tab"))
	e.must(e.Expect.Locator(closeBtn).ToBeFocused())
	e.must(kb.Press("Shift+Tab"))
	e.must(e.expect("#eta-go").ToBeFocused())

	// From outside the planner, Tab comes back to its first control.
	e.must(e.Page.Locator("#eta-go").Blur())
	e.must(kb.Press("Tab"))
	e.must(e.Expect.Locator(closeBtn).ToBeFocused())
}

// TestPagePlannerModes checks the switch between asking when a height comes
// and which height a time reaches.
func TestPagePlannerModes(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, haltedView(), "/")
		e.openPlanner()
		height := e.Page.Locator(`.eta-seg button[data-mode="height"]`)
		tm := e.Page.Locator(`.eta-seg button[data-mode="time"]`)
		e.must(e.Expect.Locator(height).ToHaveText("height → time"))
		e.must(e.Expect.Locator(tm).ToHaveText("time → height"))
		on := regexp.MustCompile(`\bon\b`)
		e.plan("1252000")
		e.must(e.expect("#eta-go").ToBeEnabled())

		e.etaPlanMode("time")
		e.must(e.Expect.Locator(tm).ToHaveClass(on))
		e.must(e.Expect.Locator(tm).ToHaveAttribute("aria-pressed", "true"))
		e.must(e.Expect.Locator(height).Not().ToHaveClass(on))
		e.must(e.Expect.Locator(height).ToHaveAttribute("aria-pressed", "false"))
		e.must(e.expect("#eta-in-label").ToHaveText("date and time (UTC)"))
		e.must(e.expect("#eta-in").ToHaveAttribute("placeholder", "e.g. 2026-11-18 13:00"))
		e.must(e.expect("#eta-in").ToHaveValue(""))
		e.must(e.expect("#eta-result").ToHaveText(etaPlanTimeEmpty))
		e.must(e.expect("#eta-go").ToBeDisabled())

		e.etaPlanMode("height")
		e.must(e.Expect.Locator(height).ToHaveClass(on))
		e.must(e.Expect.Locator(height).ToHaveAttribute("aria-pressed", "true"))
		e.must(e.Expect.Locator(tm).ToHaveAttribute("aria-pressed", "false"))
		e.must(e.expect("#eta-in-label").ToHaveText("block height"))
		e.must(e.expect("#eta-in").ToHaveAttribute("placeholder", "e.g. 1651200"))
		e.must(e.expect("#eta-result").ToHaveText(etaPlanHeightEmpty))
	})
}

// TestPagePlannerHeight checks the answers to a height: a whole number,
// written with or without separators, its ETA, how soon, its ranges and a
// halt's warning; a produced height's time; the server's refusal.
func TestPagePlannerHeight(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, haltedView(), "/", expectStatus("/api/eta", http.StatusBadRequest))
		e.openPlanner()
		for _, tc := range []struct {
			input, query string
			go_          bool
		}{
			{"#1,250,000", "height=1250000", true},
			{"1 252_000", "height=1252000", true},
			{"1240000", "height=1240000", false},
		} {
			e.plan(tc.input)
			big, all := etaPlanned(t, e.etaAnswer(tc.query))
			e.must(e.expect("#eta-result .big").ToHaveText(big))
			e.must(e.expect("#eta-result").ToHaveText(all))
			if tc.go_ {
				e.must(e.expect("#eta-go").ToBeEnabled())
			} else {
				e.must(e.expect("#eta-go").ToBeDisabled())
			}
		}
		e.must(e.expect("#eta-result .eta-warn").ToHaveCount(0))
		e.plan("1252000")
		e.must(e.expect("#eta-result .eta-warn").ToHaveText(
			"a halt is scheduled at #1250000, before this height; the estimate does not include the upgrade downtime"))

		for _, tc := range []struct{ input, msg string }{
			{"abc", "A block height is a whole number."},
			{"12.5", "A block height is a whole number."},
			{"0", `height must be a positive block height, got "0"`},
		} {
			e.etaPlanAfresh(tc.input, etaPlanHeightEmpty)
			e.must(e.expect("#eta-result").ToHaveText(tc.msg))
			e.must(e.expect("#eta-go").ToBeDisabled())
		}
	})
}

// TestPagePlannerTime checks the answers to a UTC time: the height
// expected, its ranges and a halt's warning, or the margin pending on a
// short history; malformed times and times already past.
func TestPagePlannerTime(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, tr := openChainPage(t, p, runningChain, haltedView(), "/", expectStatus("/api/eta", http.StatusBadRequest))
		e.openPlanner()
		e.etaPlanMode("time")
		for _, tc := range []struct{ input, at string }{
			{"2026-11-18 20:00", "2026-11-18T20:00:00Z"},
			{"2026-11-18T21:00:30 utc", "2026-11-18T21:00:30Z"},
		} {
			e.plan(tc.input)
			big, all := etaPlannedHeight(t, e.etaAnswer("time="+url.QueryEscape(tc.at)))
			e.must(e.expect("#eta-result .big").ToHaveText(big))
			e.must(e.expect("#eta-result").ToHaveText(all))
			e.must(e.expect("#eta-go").ToBeEnabled())
		}
		e.must(e.expect("#eta-result .eta-warn").ToHaveText(
			"a halt is scheduled at #1250000, before this time; the estimate does not include the upgrade downtime"))

		for _, tc := range []struct{ input, msg string }{
			{"tomorrow", "Use YYYY-MM-DD HH:MM, in UTC."},
			{"2026-11-18 9:00", "Use YYYY-MM-DD HH:MM, in UTC."},
			{"2026-11-18 11:00", "time 2026-11-18T11:00:00Z is not after the newest block (2026-11-18T11:59:58Z)"},
		} {
			e.etaPlanAfresh(tc.input, etaPlanTimeEmpty)
			e.must(e.expect("#eta-result").ToHaveText(tc.msg))
			e.must(e.expect("#eta-go").ToBeDisabled())
		}

		tr.set(runningChain.view(etaShortHistory, nil, false))
		e.plan("2026-11-18 20:00")
		_, all := etaPlannedHeight(t, e.etaAnswer("time=2026-11-18T20%3A00%3A00Z"))
		if !strings.Contains(all, "margin pending") {
			t.Fatalf("expected %q to say the margin is pending", all)
		}
		e.must(e.expect("#eta-result").ToHaveText(all))
	})
}

// TestPagePlannerDebounce checks that the planner asks the server 300 ms
// after the last keystroke, disables Show countdown meanwhile, and shows
// only the answer to the latest input.
func TestPagePlannerDebounce(t *testing.T) {
	e, _ := openChainPage(t, desktop, runningChain, haltedView(), "/", withInstalledClock())
	var mu sync.Mutex
	var asked []string
	e.Page.OnRequest(func(r playwright.Request) {
		// The planner asks for a height alone; the link card adds since.
		if u, err := url.Parse(r.URL()); err == nil && u.Path == "/api/eta" && u.Query().Has("height") && !u.Query().Has("since") {
			mu.Lock()
			asked = append(asked, u.Query().Get("height"))
			mu.Unlock()
		}
	})
	e.openPlanner()
	e.plan("1250000")
	e.runFor(etaDebounce)
	first, _ := etaPlanned(t, e.etaAnswer("height=1250000"))
	e.must(e.expect("#eta-result .big").ToHaveText(first))
	e.must(e.expect("#eta-go").ToBeEnabled())

	// Each keystroke disables Show countdown and restarts the wait.
	e.plan("125100")
	e.must(e.expect("#eta-go").ToBeDisabled())
	e.runFor(etaDebounce - 100*time.Millisecond)
	e.plan("1251000")
	e.runFor(etaDebounce - 100*time.Millisecond)
	e.must(e.expect("#eta-result .big").ToHaveText(first))
	e.runFor(100 * time.Millisecond)
	second, _ := etaPlanned(t, e.etaAnswer("height=1251000"))
	e.must(e.expect("#eta-result .big").ToHaveText(second))

	// An answer overtaken by a later input is dropped.
	slow := e.hold("/api/eta?height=1252000")
	e.plan("1252000")
	e.runFor(etaDebounce)
	e.plan("1249000")
	e.runFor(etaDebounce)
	latest, _ := etaPlanned(t, e.etaAnswer("height=1249000"))
	e.must(e.expect("#eta-result .big").ToHaveText(latest))
	if _, err := e.Page.ExpectRequestFinished(func() error { slow.release(); return nil }, playwright.PageExpectRequestFinishedOptions{
		Predicate: func(r playwright.Request) bool { return strings.Contains(r.URL(), "height=1252000") },
	}); err != nil {
		t.Fatalf("release the slow answer: %v", err)
	}
	e.must(e.expect("#eta-result .big").ToHaveText(latest))
	e.must(e.Page.Locator("#eta-go").Click())
	e.must(e.Expect.Page(e.Page).ToHaveURL(regexp.MustCompile(`[?&]eta=1249000&`)))

	mu.Lock()
	defer mu.Unlock()
	if want := []string{"1250000", "1251000", "1252000", "1249000"}; !slices.Equal(asked, want) {
		t.Errorf("heights asked = %v, want %v: none for an input replaced within 300 ms", asked, want)
	}
}

// TestPagePlannerHaltRow checks the planner's line on the halt: scheduled
// with its minimum version, none, or unknown while the server cannot say;
// the halt card shows only for a scheduled halt.
func TestPagePlannerHaltRow(t *testing.T) {
	for _, tc := range []struct {
		name string
		snap *node.Snapshot
		view eta.View
		row  string
	}{
		{
			"scheduled", runningChain.snapshot(), haltedView(),
			"On chain: halt scheduled at #1,250,000 (min v1.8.0). Its countdown shows for everyone, no link needed.",
		},
		{
			"none", runningChain.snapshot(), runningChain.view(fullHistory, nil, true),
			"On chain: no halt scheduled.",
		},
		{
			"unknown", sourcesDown(), haltedView(),
			"On chain: halt status unavailable (no chain status yet).",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				sc, _ := chainScenario(runningChain, tc.view)
				sc.snap = tc.snap
				e := openPage(t, p, sc)
				e.openPlanner()
				e.must(e.expect("#eta-halt-row").ToHaveText(tc.row))
				if tc.name == "scheduled" {
					e.must(e.expect("#eta-halt-row b").ToHaveText("#1,250,000"))
					e.must(e.expect("#eta-halt-card").ToBeVisible())
				} else {
					e.must(e.expect("#eta-halt-card").ToBeHidden())
				}
			})
		})
	}
}

// TestPagePlannerHaltRowWithoutMinVersion checks a halt that sets no
// minimum version: the card and the planner name its height alone.
func TestPagePlannerHaltRowWithoutMinVersion(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, runningChain.view(fullHistory, scheduledHalt(""), true), "/")
		e.must(e.expect("#eta-halt-card .eta-target").ToHaveText("until halt #1,250,000 (5,758 blocks to go)"))
		e.etaAssertCard("#eta-halt-card", etaCard(t, e.etaAnswer(""), etaCardSpec{kind: "halt"}))
		e.openPlanner()
		e.must(e.expect("#eta-halt-row").ToHaveText(
			"On chain: halt scheduled at #1,250,000. Its countdown shows for everyone, no link needed."))
		e.must(e.expect("#eta-halt-row b").ToHaveText("#1,250,000"))
	})
}

// TestPagePlannerHaltRowFollowsPolls checks that an open planner's halt row
// follows the halt as each poll finds it scheduled or dropped.
func TestPagePlannerHaltRowFollowsPolls(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		none := runningChain.view(fullHistory, nil, true)
		e, tr := openChainPage(t, p, runningChain, none, "/", withInstalledClock())
		e.openPlanner()
		e.must(e.expect("#eta-halt-row").ToHaveText("On chain: no halt scheduled."))

		tr.set(haltedView())
		e.runFor(cardPoll)
		e.must(e.expect("#eta-halt-card").ToBeVisible())
		e.must(e.expect("#eta-halt-row").ToHaveText(
			"On chain: halt scheduled at #1,250,000 (min v1.8.0). Its countdown shows for everyone, no link needed."))

		tr.set(none)
		e.runFor(cardPoll)
		e.must(e.expect("#eta-halt-card").ToBeHidden())
		e.must(e.expect("#eta-halt-row").ToHaveText("On chain: no halt scheduled."))
	})
}

// TestPagePlannerShowCountdown checks Show countdown: the link gets the
// height, the server's time as since and the label, keeping its other
// parameters and without a history entry; the planner closes and the card
// shows; the inputs stay for the next opening.
func TestPagePlannerShowCountdown(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		// The browser's clock runs 10 minutes behind the server's: since
		// is the server's time.
		e, _ := openChainPage(t, p, runningChain, haltedView(), "/?foo=1#top", withBrowserOffset(-10*time.Minute))
		e.must(e.expect("#gc-drift").ToContainText("600s"))
		before := e.historyLength()
		e.openPlanner()
		e.plan("1252000")
		e.must(e.Page.Locator("#eta-label-in").Fill("  v1.9 <i>  "))
		e.must(e.expect("#eta-label-in").ToHaveAttribute("maxlength", "40"))
		e.must(e.expect("#eta-go").ToBeEnabled())
		e.must(e.expect("#eta-go").ToHaveText("Show countdown"))
		e.must(e.Page.Locator("#eta-go").Click())

		e.must(e.expect("#eta-modal").ToBeHidden())
		e.must(e.expect("#eta-btn").ToBeFocused())
		e.must(e.Expect.Page(e.Page).ToHaveURL(e.ts.URL + "/?foo=1&eta=1252000&since=2026-11-18T12%3A00%3A00Z&label=v1.9+%3Ci%3E#top"))
		if after := e.historyLength(); after != before {
			t.Errorf("history.length = %d after Show countdown, want %d", after, before)
		}
		rep := e.etaAnswer(etaLinkQuery(pastHaltHeight, pageNow))
		e.etaAssertCard("#eta-link-card", etaCard(t, rep, etaCardSpec{kind: "link", label: "v1.9 <i>"}))

		e.openPlanner()
		e.must(e.expect("#eta-in").ToHaveValue("1252000"))
		e.must(e.expect("#eta-label-in").ToHaveValue("  v1.9 <i>  "))
	})
}

// TestPagePlannerHeightRanges checks the ranges of a height: with their
// days when they span midnight, and the margin pending while the history
// is too short for one.
func TestPagePlannerHeightRanges(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, tr := openChainPage(t, p, runningChain, runningChain.view(fullHistory, nil, true), "/")
		e.openPlanner()
		e.plan("1252700")
		big, all := etaPlanned(t, e.etaAnswer("height=1252700"))
		if want := big + "in 11h 57m · 80% Wed 18 Nov 23:37–Thu 19 Nov 00:17 · 95% Wed 18 Nov 23:36–Thu 19 Nov 00:18"; all != want {
			t.Fatalf("expected %q, want %q: the chain's block times moved", all, want)
		}
		e.must(e.expect("#eta-result").ToHaveText(all))

		tr.set(runningChain.view(etaShortHistory, nil, false))
		e.plan("1250500")
		_, all = etaPlanned(t, e.etaAnswer("height=1250500"))
		if !strings.HasSuffix(all, "m · margin pending: the block-time history is still too short") {
			t.Fatalf("expected %q to say the margin is pending", all)
		}
		e.must(e.expect("#eta-result").ToHaveText(all))
		e.must(e.expect("#eta-go").ToBeEnabled())
	})
}

// TestPagePlannerPassedETA checks a height whose ETA passed while the
// chain stalled: the planner says the block is awaited.
func TestPagePlannerPassedETA(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, etaStalledChain, etaStalledChain.view(fullHistory, nil, true), "/")
		e.openPlanner()
		e.plan("1244243")
		big, all := etaPlanned(t, e.etaAnswer("height=1244243"))
		if !strings.HasPrefix(all, big+"ETA passed, waiting for the block · 80% ") {
			t.Fatalf("expected %q to say the ETA passed", all)
		}
		e.must(e.expect("#eta-result").ToHaveText(all))
		e.must(e.expect("#eta-go").ToBeEnabled())
	})
}

// TestPagePlannerTimeWithOffset checks that the planner reads a pasted RFC
// 3339 time: a trailing Z, with or without seconds or their fraction, or an
// offset of whole minutes under a day; it refuses a malformed offset, and
// UTC after an offset.
func TestPagePlannerTimeWithOffset(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, haltedView(), "/")
		e.openPlanner()
		e.etaPlanMode("time")
		big, all := etaPlannedHeight(t, e.etaAnswer("time=2026-11-18T20%3A00%3A00Z"))
		for _, input := range []string{
			"2026-11-18T20:00:00Z",
			"2026-11-18 20:00Z",
			"2026-11-18T20:00:00.000Z",
			"2026-11-18T21:00+01:00",
			"2026-11-18 15:00:00-05:00",
			"2026-11-19T01:30+05:30",
			"2026-11-18T20:00-00:00",
		} {
			e.etaPlanAfresh(input, etaPlanTimeEmpty)
			e.must(e.expect("#eta-result").ToHaveText(all))
			e.must(e.expect("#eta-result .big").ToHaveText(big))
			e.must(e.expect("#eta-go").ToBeEnabled())
		}
		for _, input := range []string{
			"2026-11-18T21:00+0100",
			"2026-11-18T21:00+1:00",
			"2026-11-18T20:00+24:00",
			"2026-11-18T21:00+01:00 UTC",
		} {
			e.etaPlanAfresh(input, etaPlanTimeEmpty)
			e.must(e.expect("#eta-result").ToHaveText("Use YYYY-MM-DD HH:MM, in UTC."))
			e.must(e.expect("#eta-go").ToBeDisabled())
		}
	})
}

// TestPagePlannerHeightUnderAMinute checks a height due in seconds: the
// planner says under a minute, not in 0 minutes.
func TestPagePlannerHeightUnderAMinute(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e, _ := openChainPage(t, p, runningChain, runningChain.view(fullHistory, nil, true), "/")
		e.openPlanner()
		e.plan("1244243")
		big, all := etaPlanned(t, e.etaAnswer("height=1244243"))
		if !strings.HasPrefix(all, big+"in <1m · 80% ") {
			t.Fatalf("expected %q to say under a minute", all)
		}
		e.must(e.expect("#eta-result").ToHaveText(all))
	})
}

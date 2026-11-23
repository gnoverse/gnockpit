//go:build page

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// ---- Opening

// TestPageValidatorOpensFromAName checks that a plain click on a validator's
// name in the dashboard opens its page in the modal, in a pushed history
// entry the URL of which follows to the operator the key resolves to, with
// the focus on × and no other handler of the click run.
func TestPageValidatorOpensFromAName(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1))
		link := e.validatorNameLink("bravo")
		e.must(e.Expect.Locator(link).ToHaveAttribute("href", "?val="+valBravo.address()))
		entries := e.valPageHistoryLength()
		loads := e.valPageDocumentLoads()

		e.press(link)
		e.expectValidatorReport("bravo")
		e.valPageExpectURL("/?val=" + opBravo)
		if got := e.valPageHistoryLength(); got != entries+1 {
			t.Errorf("history.length = %d, want %d: one entry pushed", got, entries+1)
		}
		if n := loads(); n != 0 {
			t.Errorf("%d documents loaded, want the page opened in place", n)
		}
		e.must(e.expect("#val-close").ToBeFocused())
		e.must(e.expect(".copy-toast").ToHaveText(""))
	})
}

// TestPageValidatorOpensFromTheTimelineView checks that a name in the
// Validators card's Timeline view opens the validator's page.
func TestPageValidatorOpensFromTheTimelineView(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1))
		e.press(e.valPageTimelineButton())
		link := e.Page.Locator("#validators-timeline").GetByRole(*playwright.AriaRoleLink, playwright.LocatorGetByRoleOptions{
			Name: "bravo", Exact: playwright.Bool(true),
		}).First()
		e.must(e.Expect.Locator(link).ToHaveAttribute("href", "?val="+valBravo.address()))
		loads := e.valPageDocumentLoads()

		e.press(link)
		e.expectValidatorReport("bravo")
		e.valPageExpectURL("/?val=" + opBravo)
		if n := loads(); n != 0 {
			t.Errorf("%d documents loaded, want the page opened in place", n)
		}
	})
}

// TestPageValidatorModifiedClickFollowsTheLink checks that a click with a
// modifier key or another button leaves the page as it is and lets the
// browser follow the link: a new tab or window opens the validator's page.
func TestPageValidatorModifiedClickFollowsTheLink(t *testing.T) {
	for _, tc := range []struct {
		name    string
		click   playwright.LocatorClickOptions
		newPage bool // the browser opens the link in a new tab or window on every platform
	}{
		{name: "meta", click: playwright.LocatorClickOptions{Modifiers: []playwright.KeyboardModifier{*playwright.KeyboardModifierMeta}}},
		{name: "alt", click: playwright.LocatorClickOptions{Modifiers: []playwright.KeyboardModifier{*playwright.KeyboardModifierAlt}}},
		// Control on Linux and Windows, Meta on macOS, where a Control click
		// is a right click.
		{name: "new tab", click: playwright.LocatorClickOptions{Modifiers: []playwright.KeyboardModifier{*playwright.KeyboardModifierControlOrMeta}}, newPage: true},
		{name: "shift", click: playwright.LocatorClickOptions{Modifiers: []playwright.KeyboardModifier{*playwright.KeyboardModifierShift}}, newPage: true},
		{name: "middle button", click: playwright.LocatorClickOptions{Button: playwright.MouseButtonMiddle}, newPage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := openPage(t, desktop, setChangesNetwork(t, 1))
			link := e.validatorNameLink("bravo")
			e.must(e.Expect.Locator(link).ToBeVisible())
			click := func() error { return link.Click(tc.click) }
			if tc.newPage {
				opened, err := e.Ctx.ExpectPage(click)
				if err != nil {
					t.Fatalf("click with %s: %v", tc.name, err)
				}
				e.must(e.Expect.Page(opened).ToHaveURL(e.ts.URL + "/?val=" + opBravo))
				e.must(e.Expect.Locator(opened.Locator("#val-title")).ToHaveText("bravo"))
			} else {
				e.must(click())
			}
			e.expectValidatorPageClosed()
			e.valPageExpectURL("/")
		})
	}
}

// TestPageValidatorOpensFromTheURL checks ?val= on load: the page opens
// without a new entry, its URL follows to the operator in place keeping the
// other parameters and the hash, and closing it removes ?val= in place too.
// An empty ?val= opens nothing.
func TestPageValidatorOpensFromTheURL(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1), withURL("/?ref=x&val="+valBravo.address()+"#v"))
		e.expectValidatorReport("bravo")
		e.valPageExpectURL("/?ref=x&val=" + opBravo + "#v")
		entries := e.valPageHistoryLength()

		e.press(e.Page.Locator("#val-close"))
		e.expectValidatorPageClosed()
		e.valPageExpectURL("/?ref=x#v")
		if got := e.valPageHistoryLength(); got != entries {
			t.Errorf("history.length = %d after closing, want %d: the entry replaced", got, entries)
		}

		e.navigate("/?val=")
		e.must(e.expect("#val-modal").Not().ToHaveClass(modalOpenClass))
	})
}

// ---- Closing

// TestPageValidatorCloses checks every way to close the page: it closes,
// goes back to the entry it was opened from, URL parameters and hash
// included, and gives the focus back to the name it was opened from; Forward
// opens it again.
func TestPageValidatorCloses(t *testing.T) {
	for _, tc := range []struct {
		name     string
		profiles []profile
		close    func(e *pageEnv)
	}{
		{"×", []profile{desktop, phone}, func(e *pageEnv) { e.press(e.Page.Locator("#val-close")) }},
		{"Escape", []profile{desktop}, func(e *pageEnv) { e.must(e.Page.Keyboard().Press("Escape")) }},
		{"backdrop", []profile{desktop}, func(e *pageEnv) {
			e.must(e.Page.Locator("#val-modal").Click(playwright.LocatorClickOptions{Position: &playwright.Position{X: 5, Y: 5}}))
		}},
		{"Back", []profile{desktop, phone}, func(e *pageEnv) {
			if _, err := e.Page.GoBack(); err != nil {
				e.T.Fatalf("go back: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, tc.profiles, func(t *testing.T, p profile) {
				e := openPage(t, p, setChangesNetwork(t, 1), withURL("/?ref=x#v"))
				link := e.validatorNameLink("bravo")
				e.press(link)
				e.expectValidatorReport("bravo")
				e.valPageExpectURL("/?ref=x&val=" + opBravo + "#v")
				entries := e.valPageHistoryLength()

				tc.close(e)
				e.expectValidatorPageClosed()
				e.valPageExpectURL("/?ref=x#v")
				if got := e.valPageHistoryLength(); got != entries {
					t.Errorf("history.length = %d after closing, want %d", got, entries)
				}
				e.must(e.Expect.Locator(link).ToBeFocused())

				if _, err := e.Page.GoForward(); err != nil {
					t.Fatalf("go forward: %v", err)
				}
				e.expectValidatorReport("bravo")
				e.valPageExpectURL("/?ref=x&val=" + opBravo + "#v")
			})
		})
	}
}

// TestPageValidatorBackdropOnlyClosesOutside checks that a click inside the
// page does not close it.
func TestPageValidatorBackdropOnlyClosesOutside(t *testing.T) {
	e := openPage(t, desktop, setChangesNetwork(t, 1), withURL("/?val="+opBravo))
	e.expectValidatorReport("bravo")
	e.must(e.Page.Locator("#val-title").Click())
	e.must(e.expect("#val-modal").ToHaveClass(modalOpenClass))
	e.valPageExpectURL("/?val=" + opBravo)
}

// TestPageValidatorSameEntryKeepsThePage checks that a history entry naming
// the validator shown (a fragment navigation) neither reopens nor reloads
// the page.
func TestPageValidatorSameEntryKeepsThePage(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1), withURL("/?val="+opBravo))
		e.expectValidatorReport("bravo")
		// The page's next read is held: had the fragment started one, its
		// "loading…" would replace the report.
		read := e.hold("/api/validator")
		if _, err := e.Page.Goto(e.ts.URL + "/?val=" + opBravo + "#x"); err != nil {
			t.Fatalf("go to the fragment: %v", err)
		}
		e.valPageExpectURL("/?val=" + opBravo + "#x")
		e.expectValidatorReport("bravo")

		// The held read is a reload's.
		e.navigate("/?val=" + opBravo)
		e.must(e.expect("#val-body").ToHaveText("loading…"))
		read.release()
		e.expectValidatorReport("bravo")
	})
}

// TestPageValidatorReloadKeepsTheEntry checks that reloading an open page
// keeps the entry its opening pushed: no entry is added, and closing goes
// back to the dashboard's entry.
func TestPageValidatorReloadKeepsTheEntry(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1))
		e.press(e.validatorNameLink("bravo"))
		e.expectValidatorReport("bravo")
		entries := e.valPageHistoryLength()

		if _, err := e.Page.Reload(); err != nil {
			t.Fatalf("reload: %v", err)
		}
		e.expectValidatorReport("bravo")
		e.valPageExpectURL("/?val=" + opBravo)
		e.press(e.Page.Locator("#val-close"))
		e.expectValidatorPageClosed()
		e.valPageExpectURL("/")
		if got := e.valPageHistoryLength(); got != entries {
			t.Errorf("history.length = %d, want %d: no entry added", got, entries)
		}
		if _, err := e.Page.GoForward(); err != nil {
			t.Fatalf("go forward: %v", err)
		}
		e.expectValidatorReport("bravo")
	})
}

// TestPageValidatorFocusReturnsToItsName checks that closing the page gives
// the focus back to the name it was opened from, or, when the dashboard
// re-rendered the name while the page was open, to the name in its place.
func TestPageValidatorFocusReturnsToItsName(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rerender bool
	}{{"kept", false}, {"re-rendered", true}} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				e := openPage(t, p, setChangesNetwork(t, 1))
				link := e.validatorNameLink("bravo")
				e.press(link)
				e.expectValidatorReport("bravo")
				if tc.rerender {
					opener, err := link.ElementHandle()
					if err != nil {
						t.Fatalf("the name opened from: %v", err)
					}
					e.publish(valPageNextHead(t))
					if _, err := e.Page.WaitForFunction(`a => !a.isConnected`, opener); err != nil {
						t.Fatalf("the dashboard did not re-render the name: %v", err)
					}
				}
				e.press(e.Page.Locator("#val-close"))
				e.expectValidatorPageClosed()
				e.must(e.Expect.Locator(link).ToBeFocused())
			})
		})
	}
}

// TestPageValidatorFocusReturnsToItsTimelineRow checks that closing a page
// opened from the Timeline view gives the focus back to the row it was
// opened from, though the view re-rendered every row meanwhile and shows
// other links to the same validator.
func TestPageValidatorFocusReturnsToItsTimelineRow(t *testing.T) {
	e := openPage(t, desktop, setChangesNetwork(t, 1), withInstalledClock())
	// The dashboard renders in animation frames, which wait on the clock.
	e.runFor(time.Second)
	e.press(e.valPageTimelineButton())
	links := e.Page.Locator("#validators-timeline").GetByRole(*playwright.AriaRoleLink, playwright.LocatorGetByRoleOptions{
		Name: "bravo", Exact: playwright.Bool(true),
	})
	e.must(e.Expect.Locator(links).ToHaveCount(2))
	opener, err := links.Nth(1).ElementHandle()
	if err != nil {
		t.Fatalf("the name opened from: %v", err)
	}
	e.press(links.Nth(1))
	e.expectValidatorReport("bravo")
	// The Timeline view reads its changes again every 30 s and rebuilds its
	// rows.
	e.runFor(30 * time.Second)
	if _, err := e.Page.WaitForFunction(`a => !a.isConnected`, opener); err != nil {
		t.Fatalf("the Timeline view did not re-render its rows: %v", err)
	}
	e.press(e.Page.Locator("#val-close"))
	e.expectValidatorPageClosed()
	e.must(e.Expect.Locator(links.Nth(1)).ToBeFocused())
}

// ---- Loading and errors

// TestPageValidatorLoading checks the page while its report loads: the
// address asked, shortened, as the title, "loading…", the focus on × (named
// Close), and the URL naming the address asked until the report names its
// operator. The page is a modal dialog, named by its title.
func TestPageValidatorLoading(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1))
		read := e.hold("/api/validator?val=" + valBravo.address())
		e.press(e.validatorNameLink("bravo"))

		e.must(e.expect("#val-page").ToBeVisible())
		e.must(e.expect("#val-title").ToHaveText(shortAddr(valBravo.address())))
		e.must(e.expect("#val-body").ToHaveText("loading…"))
		e.must(e.expect("#val-body .val-msg").ToHaveCount(1))
		e.must(e.expect("#val-close").ToBeFocused())
		e.must(e.expect("#val-close").ToHaveAccessibleName("Close"))
		e.valPageExpectURL("/?val=" + valBravo.address())

		read.release()
		e.expectValidatorReport("bravo")
		e.valPageExpectURL("/?val=" + opBravo)
		dialog := e.Page.GetByRole("dialog", playwright.PageGetByRoleOptions{Name: "bravo", Exact: playwright.Bool(true)})
		e.must(e.Expect.Locator(dialog).ToHaveAttribute("aria-modal", "true"))
	})
}

// TestPageValidatorErrorAnswers checks that the page shows the error a
// failed read answers, under the address asked.
func TestPageValidatorErrorAnswers(t *testing.T) {
	noHistory := func(t *testing.T) *scenario {
		sc := setChangesNetwork(t, 1)
		sc.seed = nil
		return sc
	}
	for _, tc := range []struct {
		name, val, title, msg string
		sc                    func(t *testing.T) *scenario
		code                  int
	}{
		{"malformed", "g1nope", "g1nope", "val must be a g1 address", valPageNetworkOf(1), http.StatusBadRequest},
		{"unknown", valPageNobody, shortAddr(valPageNobody), "unknown validator", valPageNetworkOf(1), http.StatusNotFound},
		{"no block history", valBravo.address(), shortAddr(valBravo.address()), "block history disabled", noHistory, http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				e := openPage(t, p, tc.sc(t), withURL("/?val="+tc.val), expectStatus("/api/validator", tc.code))
				e.must(e.expect("#val-page").ToBeVisible())
				e.must(e.expect("#val-body .val-msg").ToHaveText(tc.msg))
				e.must(e.expect("#val-title").ToHaveText(tc.title))
			})
		})
	}
}

// TestPageValidatorErrorPageBehindAProxy checks that an error answer that is
// not gnockpit's JSON, a proxy's 502 when gnockpit does not answer, says
// which status it got.
func TestPageValidatorErrorPageBehindAProxy(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1), noNavigate(), withInstalledClock(), expectStatus("/api/validator", http.StatusBadGateway))
		front := e.behindProxy("/api/validator", http.StatusBadGateway)
		e.hold("/api/validator")
		e.navigateTo(front + "/?val=" + valBravo.address())
		e.must(e.expect("#val-body .val-msg").ToHaveText("could not load: HTTP 502"))
		e.must(e.expect("#val-title").ToHaveText(shortAddr(valBravo.address())))
	})
}

// TestPageValidatorReadWithoutAnswer checks a report gnockpit never sends:
// the page gives the read up after 10 s and says so.
func TestPageValidatorReadWithoutAnswer(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1), noNavigate(), withInstalledClock())
		stuck := e.hold("/api/validator")
		e.navigate("/?val=" + valBravo.address())
		e.must(e.gate.awaitHeld("/api/validator?val="+valBravo.address(), 1))
		e.must(e.expect("#val-body .val-msg").ToHaveText("loading…"))
		e.runFor(valPageReadBound)
		stuck.awaitAbandoned()
		e.must(e.expect("#val-body .val-msg").ToHaveText("could not load: no answer in 10 s"))
	})
}

// TestPageValidatorNetworkFailure checks that a read the network fails says
// so with the browser's reason, and that a resize afterwards leaves the
// message as it is, though the page drew a report before.
func TestPageValidatorNetworkFailure(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1), withInstalledClock())
		// The dashboard renders in animation frames, which wait on the clock.
		e.runFor(time.Second)
		e.press(e.validatorNameLink("bravo"))
		e.expectValidatorReport("bravo")
		e.press(e.Page.Locator("#val-close"))
		e.expectValidatorPageClosed()
		e.valPageExpectURL("/")

		read := e.hold("/api/validator")
		read.drop()
		if _, err := e.Page.GoForward(); err != nil {
			t.Fatalf("go forward: %v", err)
		}
		read.awaitFailure()
		reason := map[string]string{chromium: "Failed to fetch", webkit: "Load failed"}[p.browser]
		msg := e.expect("#val-body .val-msg")
		e.must(msg.ToHaveText("could not load: " + reason))
		e.must(e.expect("#val-title").ToHaveText(shortAddr(opBravo)))

		e.resize(500, 700)
		e.runFor(time.Second)
		e.must(msg.ToHaveText("could not load: " + reason))
	})
}

// TestPageValidatorStaleAnswerIsDropped checks that the answer of a read the
// page no longer waits for (it closed, then opened another validator) is
// dropped: it neither shows nor moves the URL to its operator.
func TestPageValidatorStaleAnswerIsDropped(t *testing.T) {
	e := openPage(t, desktop, setChangesNetwork(t, 1))
	stale := e.hold("/api/validator?val=" + valBravo.address())
	e.press(e.validatorNameLink("bravo"))
	e.must(e.expect("#val-body").ToHaveText("loading…"))
	e.must(e.Page.Keyboard().Press("Escape"))
	e.expectValidatorPageClosed()
	e.valPageExpectURL("/")

	current := e.hold("/api/validator?val=" + valAlpha.address())
	e.press(e.validatorNameLink("alpha"))
	e.must(e.expect("#val-body").ToHaveText("loading…"))
	answer, err := e.Page.ExpectResponse(e.ts.URL+"/api/validator?val="+valBravo.address(), func() error {
		stale.release()
		return nil
	})
	if err != nil {
		t.Fatalf("the stale answer: %v", err)
	}
	e.must(answer.Finished())
	current.release()

	e.expectValidatorReport("alpha")
	e.valPageExpectURL("/?val=" + valAlpha.address())
	e.must(e.Expect.Locator(e.valPageStatusSpans().First()).ToHaveText("in the set since 2026-11-15 (2 d) · power 10 (28.6 %)ⓘ"))
}

// ---- Status line

// TestPageValidatorStatusLine checks the status line: in or out of the set,
// since when, the power and share in the set (no share while a member's
// power is unknown, "-" for its own unknown power), and where the node is
// hosted.
func TestPageValidatorStatusLine(t *testing.T) {
	sinceBefore := func(t *testing.T) *scenario {
		sc := setChangesNetwork(t, 1)
		sc.seed = setChangesHistory(bravoJoins, 1)
		sc.fill.From, sc.fill.FromTime = bravoJoins, healthyBlockTime(bravoJoins)
		return sc
	}
	unreadable := func(t *testing.T) *scenario {
		sc := setChangesNetwork(t, 1)
		for i, v := range sc.snap.Validators {
			if v.Address == valCharlie.address() {
				sc.snap.Validators[i].VotingPower = "lots"
			}
		}
		return sc
	}
	type line struct {
		val, title, dot, dotColor string
		spans                     []string
	}
	for _, tc := range []struct {
		name  string
		sc    func(t *testing.T) *scenario
		lines []line
	}{
		{"history", valPageNetworkOf(1), []line{
			{valBravo.address(), "bravo", "dot green", "rgb(63, 185, 80)", []string{
				"in the set since 2026-11-16 (1 d) · power 10 (28.6 %)ⓘ", "🇩🇪 Germany · Hetznerⓘ",
			}},
			{valCharlie.address(), "charlie", "dot green", "rgb(63, 185, 80)", []string{
				"in the set since 2026-11-15 (2 d) · power 10 (28.6 %)ⓘ", "— · OVHⓘ",
			}},
			{valDelta.address(), shortAddr(opDelta), "dot green", "rgb(63, 185, 80)", []string{
				"in the set since 2026-11-16 (2 d) · power 5 (14.3 %)ⓘ", "🇺🇸 United States · —ⓘ",
			}},
			{valEcho.address(), "echo", "dot out", "rgb(139, 148, 158)", []string{
				"not in the set since 2026-11-17 (0 d)ⓘ",
			}},
		}},
		// The set's total is unknown: no share; charlie's own power is
		// unknown too.
		{"a power that reads as no number", unreadable, []line{
			{valBravo.address(), "bravo", "dot green", "rgb(63, 185, 80)", []string{
				"in the set since 2026-11-16 (1 d) · power 10ⓘ", "🇩🇪 Germany · Hetznerⓘ",
			}},
			{valCharlie.address(), "charlie", "dot green", "rgb(63, 185, 80)", []string{
				"in the set since 2026-11-15 (2 d) · power -ⓘ", "— · OVHⓘ",
			}},
		}},
		{"since before the history", sinceBefore, []line{
			{valAlpha.address(), "alpha", "dot green", "rgb(63, 185, 80)", []string{
				"in the set since before 2026-11-16 (over 1 d) · power 10 (28.6 %)ⓘ",
			}},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				e := openPage(t, p, tc.sc(t))
				for _, l := range tc.lines {
					e.navigate("/?val=" + l.val)
					e.must(e.expect("#val-body [data-section=profile]").ToBeVisible())
					e.must(e.expect("#val-title").ToHaveText(l.title))
					e.must(e.Expect.Locator(e.valPageStatusSpans()).ToHaveText(l.spans))
					dot := e.expect("#val-body .val-status .dot")
					e.must(dot.ToHaveClass(l.dot))
					e.must(dot.ToHaveCSS("background-color", l.dotColor))
				}
			})
		})
	}
}

// TestPageValidatorStatusLineWithoutTheSet checks that the status line is
// empty while the node's set is unknown and no peer matches.
func TestPageValidatorStatusLineWithoutTheSet(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := setChangesNetwork(t, 1)
		sc.snap = nil
		e := openPage(t, p, sc, withURL("/?val="+opBravo))
		e.expectValidatorReport("bravo")
		e.must(e.expect("#val-body .val-status").ToBeEmpty())
	})
}

// TestPageValidatorTitleNamesTheOperator checks that a report on a key its
// operator has no moniker for is titled by the operator, as its URL is.
func TestPageValidatorTitleNamesTheOperator(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1), withURL("/?val="+valDelta.address()))
		e.expectValidatorReport(shortAddr(opDelta))
		e.valPageExpectURL("/?val=" + opDelta)
	})
}

// TestPageValidatorPowersUseThousandsSeparators checks that every power the
// page shows is written with thousands separators.
func TestPageValidatorPowersUseThousandsSeparators(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 100_000), withURL("/?val="+opBravo))
		e.expectValidatorReport("bravo")
		e.must(e.Expect.Locator(e.valPageStatusSpans().First()).ToHaveText("in the set since 2026-11-16 (1 d) · power 1,000,000 (28.6 %)ⓘ"))
		e.must(e.expect("#val-body .vt-row .w").ToHaveText([]string{"power 800,000 → 1,000,000", "joined power 800,000"}))
	})
}

// TestPageValidatorAgesReadServerTime checks that a browser clock two days
// behind the server's changes no age the page shows: the status line's
// days, the timeline's ages, and the Timeline view's.
func TestPageValidatorAgesReadServerTime(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1), withBrowserOffset(-48*time.Hour))
		// The name comes with the first snapshot, which carries the server's
		// time.
		e.press(e.validatorNameLink("bravo"))
		e.expectValidatorReport("bravo")
		e.must(e.Expect.Locator(e.valPageStatusSpans().First()).ToHaveText("in the set since 2026-11-16 (1 d) · power 10 (28.6 %)ⓘ"))
		e.must(e.expect("#val-body .vt-row .h").ToHaveText([]string{"#3,001 · 22 h ago", "#2,001 · 39 h ago"}))

		e.press(e.Page.Locator("#val-close"))
		e.expectValidatorPageClosed()
		e.press(e.valPageTimelineButton())
		e.must(e.Expect.Locator(e.Page.Locator("#validators-timeline .vt-row .h").First()).ToHaveText("#3,001 · 22 h ago"))
	})
}

// TestPageValidatorAgesReadTheReportTime checks that a page opened from its
// URL counts its ages from its report's time, before the dashboard hears the
// server's: a browser clock two days behind changes none of them.
func TestPageValidatorAgesReadTheReportTime(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1), noNavigate(), withBrowserOffset(-48*time.Hour))
		// The WebSocket, which brings the server's time to the dashboard,
		// opens once the report is shown.
		ws := e.hold("/ws")
		e.navigate("/?val=" + valBravo.address())
		e.expectValidatorReport("bravo")
		e.must(e.Expect.Locator(e.valPageStatusSpans().First()).ToHaveText("in the set since 2026-11-16 (1 d) · power 10 (28.6 %)ⓘ"))
		e.must(e.expect("#val-body .vt-row .h").ToHaveText([]string{"#3,001 · 22 h ago", "#2,001 · 39 h ago"}))
		ws.release()
	})
}

// ---- Profile

// TestPageValidatorProfile checks the profile's rows: the valopers profile's
// when there is one, the operator and signing address otherwise; and the
// rows' columns on each screen width.
func TestPageValidatorProfile(t *testing.T) {
	type kv struct{ keys, values []string }
	cases := []struct {
		val string
		kv  kv
	}{
		{opBravo, kv{
			[]string{"description", "server type", "operator", "signing address", "signing key", "keep running", "last rotation"},
			[]string{"Runs bravo on bare metal.", "bare metal", opBravo, valBravo.address(), "gpub1bravo", "yes", "#2,001"},
		}},
		{opCharlie, kv{
			[]string{"description", "server type", "operator", "signing address", "signing key"},
			[]string{"—", "—", opCharlie, valCharlie.address(), "gpub1charlie"},
		}},
		{valDelta.address(), kv{
			[]string{"operator", "signing address", "profile"},
			[]string{opDelta, valDelta.address(), "none in the valopers realm"},
		}},
		{valEcho.address(), kv{
			[]string{"operator", "signing address", "profile"},
			[]string{"—", valEcho.address(), "none in the valopers realm"},
		}},
	}
	columns := map[string]*regexp.Regexp{
		desktop.name: regexp.MustCompile(`^140px [\d.]+px$`),
		tablet.name:  regexp.MustCompile(`^110px [\d.]+px$`),
		phone.name:   regexp.MustCompile(`^[\d.]+px$`),
	}
	onProfiles(t, []profile{desktop, tablet, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1))
		for _, c := range cases {
			e.navigate("/?val=" + c.val)
			e.must(e.expect("#val-body [data-section=profile]").ToBeVisible())
			e.must(e.expect("#val-body .kv .k").ToHaveText(c.kv.keys))
			e.must(e.expect("#val-body .kv .v").ToHaveText(c.kv.values))
			if c.kv.keys[0] == "description" {
				e.must(e.Expect.Locator(e.valPageValue(0)).ToHaveClass("v val-text"))
			}
			e.must(e.expect("#val-body .kv").ToHaveCSS("grid-template-columns", columns[p.name]))
			e.must(e.Expect.Locator(e.valPageValue(0)).ToHaveCSS("cursor", "pointer"))
		}
	})
}

// TestPageValidatorBlankDescription checks that a description of blanks
// shows as none.
func TestPageValidatorBlankDescription(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, valPageWithProfile(setChangesNetwork(t, 1), " \n\t\n "), withURL("/?val="+opBravo))
		e.expectValidatorReport("bravo")
		description := e.Expect.Locator(e.valPageValue(0))
		e.must(description.ToHaveText("—"))
		e.must(description.ToHaveClass("v val-text"))
	})
}

// TestPageValidatorProfileCopy checks that a click on a profile value copies
// it, a click on a value of — copies nothing, and a click on a link in a
// value opens the link without copying.
func TestPageValidatorProfileCopy(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, "<!doctype html><title>Linked</title>")
		}))
		t.Cleanup(target.Close)
		linked := target.URL + "/linked"
		sc := valPageWithProfile(setChangesNetwork(t, 1), "See [the linked page]("+linked+").")
		e := openPage(t, p, sc, withURL("/?val="+opCharlie))
		var clip string
		if p.browser == chromium {
			clip = e.clipboard()
		}
		e.must(e.Expect.Locator(e.valPageValue(1)).ToHaveText("—"))
		e.press(e.valPageValue(1))
		e.must(e.expect(".copy-toast").ToHaveText(""))

		e.navigate("/?val=" + opBravo)
		popup, err := e.Page.ExpectPopup(func() error {
			e.press(e.valPageValue(0).GetByRole(*playwright.AriaRoleLink))
			return nil
		})
		if err != nil {
			t.Fatalf("follow the description's link: %v", err)
		}
		e.must(e.Expect.Page(popup).ToHaveURL(linked))
		e.must(e.expect(".copy-toast").ToHaveText(""))
		if p.browser == chromium && e.clipboard() != clip {
			t.Errorf("clipboard = %q after a click on a link, want it unchanged (%q)", e.clipboard(), clip)
		}

		e.press(e.valPageValue(2))
		e.must(e.expect(".copy-toast").ToHaveText("Copied!"))
		if p.browser == chromium {
			if got := e.clipboard(); got != opBravo {
				t.Errorf("clipboard = %q, want the operator %s", got, opBravo)
			}
		}
	})
}

// TestPageValidatorDescriptionCopiesItsSource checks that a click on the
// description copies the markdown its operator wrote.
func TestPageValidatorDescriptionCopiesItsSource(t *testing.T) {
	const source = "Runs **bravo**: [site](https://example.org/b)."
	e := openPage(t, desktop, valPageWithProfile(setChangesNetwork(t, 1), source), withURL("/?val="+opBravo))
	e.expectValidatorReport("bravo")
	e.press(e.Page.Locator("#val-body .val-text strong"))
	e.must(e.expect(".copy-toast").ToHaveText("Copied!"))
	if got := e.clipboard(); got != source {
		t.Errorf("clipboard = %q, want the description's source %q", got, source)
	}
}

// ---- Profile markdown

// valPageMarkdown covers each form a profile's markdown takes, and what
// stays text.
var valPageMarkdown = strings.Join([]string{
	"Intro with **bold**, *italic*, _under_ and `code *as is*`.",
	"A single newline is a space.",
	"### Heading with `code` ###",
	"  # Indented heading",
	"Right after a heading.",
	"####### Seven marks stay text",
	"#no space stays text",
	"",
	"- dash",
	"* star",
	"+ plus",
	"  - flat, no nesting",
	"1. one",
	"2) two",
	"not an item",
	"",
	"A [link](https://example.org/a), https://bare.example.org/x. and (https://paren.example.org/p)",
	"",
	"**bold [link](https://example.org/b) and _em_** snake_case_name __double__",
	"",
	"[js](javascript:void%200) [data](data:text/html,x) [mail](mailto:a@example.org) [relative](/relative)",
	"",
	"![image](https://img.example.org/i.png) [**not bold**](https://example.org/c)",
	"",
	"`[not](https://a.link)` **`code in bold`**",
	"",
	"first\r\nsecond\rthird",
	"",
	"> not a quote",
	"| a | b |",
	"---",
	"```",
}, "\n")

// valPageMarkdownRendered is valPageMarkdown as rendered, in the form
// valPageMarkdownTree reads.
var valPageMarkdownRendered = []string{
	`p("Intro with " strong("bold") ", " em("italic") ", " em("under") " and " code("code *as is*") ". A single newline is a space.")`,
	`div.val-md-h("Heading with " code("code"))`,
	`div.val-md-h("Indented heading")`,
	`p("Right after a heading. ####### Seven marks stay text #no space stays text")`,
	`ul(li("dash") li("star") li("plus") li("flat, no nesting"))`,
	`ol(li("one") li("two"))`,
	`p("not an item")`,
	`p("A " a[https://example.org/a]("link") ", " a[https://bare.example.org/x]("https://bare.example.org/x") ". and (" a[https://paren.example.org/p]("https://paren.example.org/p") ")")`,
	`p(strong("bold " a[https://example.org/b]("link") " and " em("em")) " snake_case_name __double__")`,
	`p("js data mail relative")`,
	`p("!" a[https://img.example.org/i.png]("image") " " a[https://example.org/c]("**not bold**"))`,
	`p(code("[not](https://a.link)") " " strong(code("code in bold")))`,
	`p("first second third")`,
	"p(\"> not a quote | a | b | --- ```\")",
}

// TestPageValidatorProfileMarkdown checks how a profile's description
// renders: paragraphs, headings, lists, inline forms, links to http(s) URLs
// only, opening in a new tab, in the theme's colours.
func TestPageValidatorProfileMarkdown(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, valPageWithProfile(setChangesNetwork(t, 1), valPageMarkdown), withURL("/?val="+opBravo))
		e.expectValidatorReport("bravo")
		if got := e.valPageMarkdownTree(); !slices.Equal(got, valPageMarkdownRendered) {
			t.Errorf("description =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(valPageMarkdownRendered, "\n"))
		}

		links := e.Page.Locator("#val-body .val-text a")
		hrefs := []string{
			"https://example.org/a", "https://bare.example.org/x", "https://paren.example.org/p",
			"https://example.org/b", "https://img.example.org/i.png", "https://example.org/c",
		}
		e.must(e.Expect.Locator(links).ToHaveCount(len(hrefs)))
		for i, href := range hrefs {
			a := e.Expect.Locator(links.Nth(i))
			e.must(a.ToHaveAttribute("href", href))
			e.must(a.ToHaveAttribute("target", "_blank"))
			e.must(a.ToHaveAttribute("rel", "noopener"))
			e.must(e.Expect.Locator(links.Nth(i).Locator("span.ext-arrow")).ToHaveText(" ↗"))
			e.must(a.ToHaveCSS("color", "rgb(88, 166, 255)"))
			e.must(a.ToHaveCSS("text-decoration-line", "none"))
		}
		code := e.Expect.Locator(e.Page.Locator("#val-body .val-text code").First())
		e.must(code.ToHaveCSS("background-color", "rgb(13, 17, 23)"))
		e.must(code.ToHaveCSS("padding", "0px 3px"))
		e.must(e.Expect.Locator(e.Page.Locator("#val-body .val-text .val-md-h").First()).ToHaveCSS("font-weight", "600"))

		if p == desktop {
			e.must(links.First().Hover())
			e.must(e.Expect.Locator(links.First()).ToHaveCSS("text-decoration-line", "underline"))
		}
	})
}

// TestPageValidatorProfileListKeepsItsNumbers checks that an ordered list
// counts from its first item's number.
func TestPageValidatorProfileListKeepsItsNumbers(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := valPageWithProfile(setChangesNetwork(t, 1), "3. third\n4. fourth\n\n1. one")
		e := openPage(t, p, sc, withURL("/?val="+opBravo))
		e.expectValidatorReport("bravo")
		want := []string{`ol[start=3](li("third") li("fourth"))`, `ol(li("one"))`}
		if got := e.valPageMarkdownTree(); !slices.Equal(got, want) {
			t.Errorf("description = %q, want %q", got, want)
		}
	})
}

// TestPageValidatorProfileUnderscoreItalic checks where an _italic_ opens:
// at the start or after any non-word character, the closing mark of a code
// or an *italic* included, never after a word character, so the underscores
// in one_two_ stay text.
func TestPageValidatorProfileUnderscoreItalic(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := valPageWithProfile(setChangesNetwork(t, 1), "_start_ one_two_ three, *x*_after_ `y`_code_ and (_paren_)")
		e := openPage(t, p, sc, withURL("/?val="+opBravo))
		e.expectValidatorReport("bravo")
		want := []string{`p(em("start") " one_two_ three, " em("x") em("after") " " code("y") em("code") " and (" em("paren") ")")`}
		if got := e.valPageMarkdownTree(); !slices.Equal(got, want) {
			t.Errorf("description = %q, want %q", got, want)
		}
	})
}

// TestPageValidatorProfileURLs checks that a URL links in any case of its
// scheme and keeps balanced parentheses, a link's and a bare one's alike; a
// bare one leaves out trailing punctuation and the closing parentheses it
// has more of than opening ones.
func TestPageValidatorProfileURLs(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := valPageWithProfile(setChangesNetwork(t, 1), strings.Join([]string{
			"HTTPS://CAPS.example.org/c and Http://mixed.example.org/m",
			"[wiki](https://en.wikipedia.org/wiki/Foo_(x)) and https://en.wikipedia.org/wiki/Bar_(y), (see https://paren.example.org/p)",
			"[js](javascript:alert(1)) stays text",
			"",
			"https://x.example.org/a)b and https://x.example.org/((a)) and [x](https://a.example.org/(b(c))) and (https://x.example.org/q.)",
		}, "\n"))
		e := openPage(t, p, sc, withURL("/?val="+opBravo))
		e.expectValidatorReport("bravo")
		want := []string{
			`p(` +
				`a[HTTPS://CAPS.example.org/c]("HTTPS://CAPS.example.org/c") " and " a[Http://mixed.example.org/m]("Http://mixed.example.org/m") " " ` +
				`a[https://en.wikipedia.org/wiki/Foo_(x)]("wiki") " and " ` +
				`a[https://en.wikipedia.org/wiki/Bar_(y)]("https://en.wikipedia.org/wiki/Bar_(y)") ", (see " ` +
				`a[https://paren.example.org/p]("https://paren.example.org/p") ") js stays text")`,
			`p(a[https://x.example.org/a)b]("https://x.example.org/a)b") " and " ` +
				`a[https://x.example.org/((a))]("https://x.example.org/((a))") " and " ` +
				`a[https://a.example.org/(b(c))]("x") " and (" ` +
				`a[https://x.example.org/q]("https://x.example.org/q") ".)")`,
		}
		if got := e.valPageMarkdownTree(); !slices.Equal(got, want) {
			t.Errorf("description =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
}

// TestPageValidatorProfileBracketRun checks that a description of a long
// run of [ renders within the assertion timeout once its report is read:
// matching its link texts takes linear time. A page busy rendering answers
// no assertion, so the time from the report's release to its rendering is
// measured, the page's load left out.
func TestPageValidatorProfileBracketRun(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		run := strings.Repeat("[", 100_000)
		e := openPage(t, p, valPageWithProfile(setChangesNetwork(t, 1), run), noNavigate())
		read := e.hold("/api/validator")
		e.navigate("/?val=" + opBravo)
		e.must(e.expect("#val-body").ToHaveText("loading…"))
		start := time.Now()
		read.release()
		e.expectValidatorReport("bravo")
		if took := time.Since(start); took > pageTimeout {
			t.Errorf("the profile rendered %s after its report's release, want within %s", took.Round(time.Second), pageTimeout)
		}
		e.must(e.Expect.Locator(e.valPageValue(0)).ToHaveText(run))
	})
}

// TestPageValidatorMarkupStaysText checks that chain-supplied text with HTML
// in it shows as text in the title, the profile and the location.
func TestPageValidatorMarkupStaysText(t *testing.T) {
	const markup = `<img src=x onerror=alert(1)>`
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := valPageWithProfile(setChangesNetwork(t, 1), markup)
		bravo := sc.valopers[opBravo]
		bravo.Moniker, bravo.ServerType = markup, markup
		sc.valopers[opBravo] = bravo
		for i := range sc.snap.Peers {
			if sc.snap.Peers[i].ValAddress == valBravo.address() {
				sc.snap.Peers[i].Provider = markup
			}
		}
		e := openPage(t, p, sc, withURL("/?val="+opBravo))
		e.Page.OnDialog(func(d playwright.Dialog) {
			t.Errorf("dialog %q opened", d.Message())
			if err := d.Dismiss(); err != nil {
				t.Errorf("dismiss the dialog: %v", err)
			}
		})
		e.expectValidatorReport(markup)
		e.must(e.Expect.Locator(e.valPageValue(0)).ToHaveText(markup))
		e.must(e.Expect.Locator(e.valPageValue(1)).ToHaveText(markup))
		e.must(e.Expect.Locator(e.valPageStatusSpans().Last()).ToHaveText("🇩🇪 Germany · " + markup + "ⓘ"))
		e.must(e.expect("#val-page img").ToHaveCount(0))
		e.must(e.expect("#val-title *").ToHaveCount(0))
	})
}

// ---- Actions

// TestPageValidatorLinkButton checks that ⧉ link copies the page's own link:
// the address asked while the report loads, its operator once it names one,
// and none of the dashboard's other parameters.
func TestPageValidatorLinkButton(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1), noNavigate(), withInstalledClock())
		read := e.hold("/api/validator")
		e.navigate("/?ref=x&val=" + valBravo.address() + "#v")
		button := e.Page.Locator("#val-link-btn")
		e.must(e.Expect.Locator(button).ToHaveText("⧉ link"))
		toast := e.expect(".copy-toast")

		e.must(e.expect("#val-body").ToHaveText("loading…"))
		e.press(button)
		e.must(toast.ToHaveText("Copied!"))
		e.must(toast.ToHaveCSS("opacity", "1"))
		if p.browser == chromium {
			if got, want := e.clipboard(), e.ts.URL+"/?val="+valBravo.address(); got != want {
				t.Errorf("clipboard while loading = %q, want %q", got, want)
			}
		}

		read.release()
		e.expectValidatorReport("bravo")
		e.press(button)
		if p.browser == chromium {
			if got, want := e.clipboard(), e.ts.URL+"/?val="+opBravo; got != want {
				t.Errorf("clipboard = %q, want %q", got, want)
			}
		}
	})
}

// TestPageValidatorFocusTrap checks that Tab and Shift+Tab cycle through the
// page's controls (its two buttons, then its body's ⓘ, links and charts, the
// description's two links and each chart's newest day among them), and bring
// a focus that left the page back to it.
func TestPageValidatorFocusTrap(t *testing.T) {
	sc := valPageWithProfile(setChangesNetwork(t, 1), "[one](https://example.org/1) and [two](https://example.org/2)")
	e := openPage(t, desktop, sc, withURL("/?val="+opBravo))
	e.expectValidatorReport("bravo")
	button, closer := e.Page.Locator("#val-link-btn"), e.Page.Locator("#val-close")
	one := e.Page.Locator("#val-body .val-text a").First()
	two := e.Page.Locator("#val-body .val-text a").Last()
	stops := e.Page.Locator(`#val-modal :is(a[href], button:not(:disabled), input, [tabindex="0"])`)
	e.must(e.Expect.Locator(stops.And(one)).ToHaveCount(1))
	e.must(e.Expect.Locator(stops.And(two)).ToHaveCount(1))
	// Each chart with days is one stop, on its newest; Recovery has none.
	for _, newest := range []string{"[data-chart=uptime] svg > rect", "[data-chart=proposals] svg > rect", "[data-chart=delay] svg > g"} {
		e.must(e.Expect.Locator(stops.And(e.Page.Locator("#val-body " + newest).Last())).ToHaveCount(1))
	}
	e.must(e.Expect.Locator(stops.And(e.Page.Locator("#val-body [data-chart] [data-pop-rows]"))).ToHaveCount(3))
	e.must(e.Expect.Locator(stops.First()).ToHaveId("val-link-btn"))
	e.must(e.Expect.Locator(stops.Nth(1)).ToHaveId("val-close"))
	n, err := stops.Count()
	e.must(err)
	last := stops.Last()
	e.must(e.Expect.Locator(closer).ToBeFocused())

	keyboard := e.Page.Keyboard()
	for i := 2; i < n; i++ {
		e.must(keyboard.Press("Tab"))
		e.must(e.Expect.Locator(stops.Nth(i)).ToBeFocused())
	}
	for _, step := range []struct {
		key     string
		focused playwright.Locator
	}{
		{"Tab", button},
		{"Tab", closer},
		{"Shift+Tab", button},
		{"Shift+Tab", last},
		{"Shift+Tab", stops.Nth(n - 2)},
	} {
		e.must(keyboard.Press(step.key))
		e.must(e.Expect.Locator(step.focused).ToBeFocused())
	}

	e.must(e.Page.Locator("#val-body .kv .k").First().Click())
	e.must(e.expect("body").ToBeFocused())
	e.must(keyboard.Press("Tab"))
	e.must(e.Expect.Locator(button).ToBeFocused())
	e.must(e.Page.Locator("#val-body .kv .k").First().Click())
	e.must(keyboard.Press("Shift+Tab"))
	e.must(e.Expect.Locator(last).ToBeFocused())
}

// ---- Sections

// TestPageValidatorSections checks the page's sections: their order, the
// group the ranking scores, every ⓘ's three lines, the charts' boxes and
// the uptime stats line.
func TestPageValidatorSections(t *testing.T) {
	info := regexp.MustCompile(`^What: .+\nSource: .+\nScored: (yes, .+|no, shown for monitoring)$`)
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, setChangesNetwork(t, 1), withURL("/?val="+opBravo))
		e.expectValidatorReport("bravo")
		e.must(e.expect("#val-body h3").ToHaveText([]string{"Profileⓘ", "Uptimeⓘ", "Proposalsⓘ", "Recoveryⓘ", "Vote delayⓘ", "Timelineⓘ"}))
		sections := []string{"profile", "uptime", "proposals", "recovery", "delay", "timeline"}
		for i, key := range sections {
			e.must(e.Expect.Locator(e.Page.Locator("#val-body [data-section]").Nth(i)).ToHaveAttribute("data-section", key))
		}
		e.must(e.expect("#val-body .val-scored h3").ToHaveText([]string{"Uptimeⓘ", "Proposalsⓘ", "Recoveryⓘ"}))
		e.must(e.expect("#val-body .val-scored > .val-glabel").ToHaveText("scored by the ranking" + "ⓘ"))

		infos := e.Page.Locator("#val-body .info-btn")
		e.must(e.Expect.Locator(infos).ToHaveCount(9))
		for i := range 9 {
			e.must(e.Expect.Locator(infos.Nth(i)).ToHaveAttribute("data-popover", info))
		}
		charts := []string{"uptime", "proposals", "recovery", "delay"}
		e.must(e.expect("#val-body [data-chart]").ToHaveCount(len(charts)))
		for i, key := range charts {
			e.must(e.Expect.Locator(e.Page.Locator("#val-body [data-chart]").Nth(i)).ToHaveAttribute("data-chart", key))
		}
		uptime := "#val-body [data-section=uptime] .val-stats"
		e.must(e.expect(uptime).ToHaveText("last 30 dayssigned100.00 %outages0nil votes0other block0"))
		e.must(e.Expect.Locator(e.Page.Locator(uptime + " b").First()).ToHaveClass("ok"))
	})
}

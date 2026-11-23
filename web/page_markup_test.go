//go:build page

package web

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

// expectNoInlineHandlers fails the test when an element of the page's DOM,
// as rendered, carries an attribute named on*, listing each as
// tag#id[attribute]: where names the state of the page it reads.
func (e *pageEnv) expectNoInlineHandlers(where string) {
	e.T.Helper()
	v, err := e.Page.Evaluate(`() => [...document.querySelectorAll('*')].flatMap((el) =>
		[...el.attributes]
			.filter((a) => a.name.startsWith('on'))
			.map((a) => el.tagName.toLowerCase() + (el.id ? '#' + el.id : '') + '[' + a.name + ']'))`)
	if err != nil {
		e.T.Fatalf("%s: read the inline handlers: %v", where, err)
	}
	found, ok := v.([]any)
	if !ok {
		e.T.Fatalf("%s: read the inline handlers: got %T, want a list", where, v)
	}
	if len(found) > 0 {
		e.T.Errorf("%s: inline handlers %v", where, found)
	}
}

// TestPageRendersNoInlineHandlers checks the markup the page's modules
// build from strings where it renders, as index.html is checked as written
// (TestIndexHasNoInlineHandlers): the ETA cards, with an answer and with an
// error, the planner's three answers, the dashboard with its data and a
// validator's page carry no attribute named on*.
func TestPageRendersNoInlineHandlers(t *testing.T) {
	t.Run("ETA cards and planner", func(t *testing.T) {
		e, _ := openChainPage(t, desktop, runningChain, haltedView(), cardLink(soonHeight, time.Time{}, ""),
			expectStatus("/api/eta", http.StatusBadRequest))
		for _, card := range []string{"#eta-halt-card", linkCard} {
			e.must(e.expect(card + " .eta-scen .s").Not().ToHaveCount(0))
			e.must(e.expect(card + " .eta-range .b80").ToHaveCount(1))
			e.must(e.expect(card + " .eta-chart svg polyline").ToHaveCount(1))
		}
		e.expectNoInlineHandlers("ETA cards")

		e.openPlanner()
		e.plan(strconv.FormatInt(soonHeight, 10))
		e.must(e.expect("#eta-result .big").ToContainText(" UTC"))
		e.expectNoInlineHandlers("planner, a height ahead")
		e.plan(strconv.FormatInt(reachedHeight, 10))
		e.must(e.expect("#eta-result .big").ToContainText("Reached "))
		e.expectNoInlineHandlers("planner, a height reached")
		e.etaPlanMode("time")
		e.plan("2026-11-18 20:00")
		e.must(e.expect("#eta-result .big").ToContainText("≈ #"))
		e.expectNoInlineHandlers("planner, a time")

		e.navigate("/?eta=1,250,000")
		e.must(e.expect(linkCard + " .kv .k").ToHaveText("error"))
		e.expectNoInlineHandlers("ETA card with an error")
	})

	t.Run("dashboard and validator page", func(t *testing.T) {
		e := openPage(t, desktop, setChangesNetwork(t, 1))
		for _, rendered := range []string{
			"#validators-body td:has(> .mid-trunc)",
			"#peers-body td:has(> .mid-trunc)",
			"#recent-blocks-body .info-btn",
			".map-dot",
		} {
			e.must(e.expect(rendered).Not().ToHaveCount(0))
		}
		e.expectNoInlineHandlers("dashboard")

		e.navigate("/?val=" + valBravo.address())
		e.expectValidatorReport("bravo")
		e.expectNoInlineHandlers("validator page")
	})
}

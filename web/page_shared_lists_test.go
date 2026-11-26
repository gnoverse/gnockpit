//go:build page

package web

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"

	"github.com/mxschmitt/playwright-go"
)

// ---- The validators card's views

const (
	// validatorsViewKey is the localStorage key of the validators card's view.
	validatorsViewKey = "gnockpit-validators-view"
	// validatorsLiveToggle and validatorsTimelineToggle are the view switch's
	// Live and Timeline buttons.
	validatorsLiveToggle     = `#validators-view button[data-view="live"]`
	validatorsTimelineToggle = `#validators-view button[data-view="timeline"]`
)

// ---- Rows and cards

// peerEntry is the row of the Peers table (desktop, tablet) or the card
// (phone) of the peer named moniker.
func peerEntry(p profile, moniker string) string {
	if p == phone {
		return fmt.Sprintf(`#peers-cards .peer-card:has(.peer-card-name:text-is(%q))`, moniker)
	}
	return fmt.Sprintf(`#peers-body tr:has(td[data-copy=%q])`, moniker)
}

// validatorEntryAt is the row of the Validators table (desktop, tablet) or
// the card (phone) of the validator at address, found by the link to its
// page: its name may show shortened.
func validatorEntryAt(p profile, address string) string {
	if p == phone {
		return fmt.Sprintf(`#validators-cards .peer-card:has(a.val-link[href="?val=%s"])`, address)
	}
	return fmt.Sprintf(`#validators-body tr:has(a.val-link[href="?val=%s"])`, address)
}

// Targets of the healthy network.
const (
	// euNodeCountry is the country cell of the peer eu-node.
	euNodeCountry = `#peers-body tr:has(td[data-copy="eu-node"]) td.country-cell`
	// charlieMissed100 is charlie's count of blocks missed of the last 100.
	charlieMissed100 = `#validators-body tr:has(td[data-copy="charlie"]) td:nth-child(7)`
	// charlieCardMissed100 is the same count on charlie's card.
	charlieCardMissed100 = `#validators-cards .peer-card:has(a.val-link:text-is("charlie")) .peer-card-v[data-pop-rows]`
)

// validatorNameLink is the link of the validator named name in the
// dashboard's list.
func (e *pageEnv) validatorNameLink(name string) playwright.Locator {
	return e.Page.Locator(validatorList(e.profile)).GetByRole(*playwright.AriaRoleLink, playwright.LocatorGetByRoleOptions{
		Name: name, Exact: playwright.Bool(true),
	})
}

// cardsUpTo is the widest window, in CSS pixels, the dashboard lists
// its validators in as cards rather than a table.
const cardsUpTo = 768

// validatorList is where the dashboard lists the validators on p: the Live
// table on a wide screen, the cards up to cardsUpTo.
func validatorList(p profile) string {
	if p.device == "" && p.viewport.Width > cardsUpTo {
		return "#validators-body"
	}
	return "#validators-cards"
}

// ---- Tables

// sortTable clicks the header of key of the table of card, and checks the
// header then marks it, alone, by its arrow.
func sortTable(e *pageEnv, card, key string, asc bool) {
	e.T.Helper()
	e.must(e.Page.Locator(fmt.Sprintf(`%s th[data-sort=%q]`, card, key)).Click())
	expectSorted(e, card, key, asc)
}

// expectSorted checks that the header of key, alone in the table of card,
// is marked sorted, in blue, with its arrow: ▲ ascending, ▼ descending.
func expectSorted(e *pageEnv, card, key string, asc bool) {
	e.T.Helper()
	th := fmt.Sprintf(`%s th[data-sort=%q]`, card, key)
	// A no-break space joins the arrow to the label.
	arrow := "\u00a0▲"
	if !asc {
		arrow = "\u00a0▼"
	}
	e.must(e.expect(card + " th.sorted-col").ToHaveCount(1))
	e.must(e.expect(th).ToHaveClass(regexp.MustCompile(`\bsorted-col\b`)))
	e.must(e.expect(th+" > button").ToHaveCSS("color", blueColor))
	e.must(e.expect(th+" > button").ToHaveCSS("opacity", "1"))
	e.must(e.expect(card + " .sort-arrow").ToHaveCount(1))
	e.must(e.expect(th+" > button > .sort-arrow").ToHaveJSProperty("textContent", arrow))
}

// peerNameCells are the names in the Peers table's rows, in order.
const peerNameCells = "#peers-body tr > td:first-child"

// expectPeerNames checks the names of the peers listed, in order: the
// table's on a desktop, the cards' on a phone.
func expectPeerNames(e *pageEnv, p profile, names ...string) {
	e.T.Helper()
	if p == phone {
		e.must(e.expect("#peers-cards .peer-card-name").ToHaveText(names))
		return
	}
	e.must(e.expect(peerNameCells).ToHaveText(names))
}

// Columns of the Validators table, from 1.
const (
	valColName = iota + 1
	valColAddress
	valColCountry
	valColProvider
	valColVP
	valColSPOF
	valColMissed
	valColMissed24h
	valColDelay
	valColProps
)

// expectValidatorCards checks the names of the validator cards, in order.
func expectValidatorCards(e *pageEnv, names ...string) {
	e.T.Helper()
	e.must(e.expect("#validators-cards .peer-card-name a.val-link").ToHaveText(names))
}

// popRows reads the rows of the popover of the element selector
// matches, as label and value pairs, and its title.
func popRows(e *pageEnv, selector string) (title string, rows [][2]string) {
	e.T.Helper()
	raw, err := e.Page.Locator(selector).GetAttribute("data-pop-rows")
	e.must(err)
	var pop struct {
		T string
		R [][3]any
	}
	if err := json.Unmarshal([]byte(raw), &pop); err != nil {
		e.T.Fatalf("data-pop-rows of %s = %q: %v", selector, raw, err)
	}
	for _, r := range pop.R {
		rows = append(rows, [2]string{fmt.Sprint(r[1]), fmt.Sprint(r[2])})
	}
	return pop.T, rows
}

// expectMidTrunc checks that cell shows text truncated in the
// middle: its start, then its last midTruncTail characters.
func expectMidTrunc(e *pageEnv, cell, text string) {
	e.T.Helper()
	cut := len(text) - midTruncTail
	e.must(e.expect(cell + " > .mid-trunc > .mt-start").ToHaveText(text[:cut]))
	e.must(e.expect(cell + " > .mid-trunc > .mt-end").ToHaveText(text[cut:]))
}

// expectTableHead checks the headers of the table of card, in order:
// each column's sort key, and its label as the page writes it, the sort
// arrow and the no-break space joining it included.
func expectTableHead(e *pageEnv, card string, keys, labels []string) {
	e.T.Helper()
	headers := card + " thead th"
	e.must(e.expect(headers).ToHaveCount(len(keys)))
	for i, key := range keys {
		th := fmt.Sprintf("%s:nth-child(%d)", headers, i+1)
		e.must(e.expect(th).ToHaveAttribute("data-sort", key))
		e.must(e.expect(th+" > button").ToHaveJSProperty("textContent", labels[i]))
	}
}

// expectColumnWidths checks that the columns of the table of card,
// named by keys, take their share of its width, in percent.
func expectColumnWidths(e *pageEnv, card string, keys []string, widths []float64) {
	e.T.Helper()
	table := boxOf(e, card+" table")
	got := evalFloats(e, `(sel) => [...document.querySelectorAll(sel)].map((th) => th.getBoundingClientRect().width)`, card+" thead th")
	for i, w := range widths {
		if want := table.Width * w / 100; math.Abs(got[i]-want) > 1 {
			e.T.Errorf("column %s is %.1f px wide, want %.1f (%g %% of %.1f)", keys[i], got[i], want, w, table.Width)
		}
	}
}

// midTruncTail is how many characters a cell truncated in the
// middle keeps apart at its end, in view however narrow its column.
const midTruncTail = 7

// ---- Map

// expectMapHighlighted asserts that the elements of the country or provider attr=val
// are highlighted, and no other.
func expectMapHighlighted(e *pageEnv, attr, val string) {
	e.T.Helper()
	sel := "[" + attr + `="` + val + `"]`
	e.must(e.expect(sel + ".cc-hl").Not().ToHaveCount(0))
	e.must(e.expect(sel + ":not(.cc-hl)").ToHaveCount(0))
	e.must(e.expect(".cc-hl:not(" + sel + ")").ToHaveCount(0))
}

// expectMapDimmed asserts whether the map dims what is not highlighted.
func expectMapDimmed(e *pageEnv, dimmed bool) {
	e.T.Helper()
	n := 0
	if dimmed {
		n = 1
	}
	e.must(e.expect("#map-points.dim-others").ToHaveCount(n))
}

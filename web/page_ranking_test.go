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

	"github.com/mxschmitt/playwright-go"

	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/ranking"
)

const (
	// rankingPoll is how often the Ranking view reads the ranking again.
	rankingPoll = time.Minute
	// rankingReadBound is how long it waits for an answer to a read before
	// it gives the read up.
	rankingReadBound = 10 * time.Second
	rankingRead      = "/api/ranking"

	rankingToggle = `#validators-view button[data-view="ranking"]`
	rankingPanel  = "#validators-ranking"
	rankingMeta   = rankingPanel + " .rk-meta"
	rankingTable  = rankingPanel + " .rk-wrap"
	rankingRows   = rankingTable + " tbody tr"
	rankingCards  = rankingPanel + " .rk-cards"
	// rankingState is the line on a ranking not ready: filling, unavailable,
	// or ready with no day published yet.
	rankingState = rankingPanel + " .rk-state"
	// rankingError is the line on a failed read.
	rankingError = rankingPanel + " .rk-err"
	// rankingTitle is the page's title once /api/boot has answered.
	rankingTitle = "Gnockpit " + pageChainID

	// The separators' text, above the excluded and the not-ranked-yet rows.
	rankingExcludedSep = "Not ranked: run by the gno.land team or close partners (this dashboard's exclusion list). Their values are shown, scored the same way."
	rankingWaitingSep  = "Not ranked yet: less than 168 h (7 days) in the set within the window"
	// rankingFillingLead starts the line of a ranking filling.
	rankingFillingLead = "The ranking needs the blocks of the last 180 days (back to genesis on a younger chain): recovery after restarts looks back that far. Stored so far: "

	// rankingModal is the "How is this ranked?" dialog's backdrop, and
	// rankingModalInner the dialog in it.
	rankingModal      = "#ranking-modal"
	rankingModalInner = rankingModal + " .eta-modal-inner"

	// The labels of the recovery and proposals popovers' sub-scores, on
	// their mappings.
	rankingRecoverySub  = "Sub-score (10 min → 1, 1 h → 0.66, 6 h → 0.33, 24 h → 0)"
	rankingProposalsSub = "Sub-score (0 % → 1, 5 % → 0)"
)

// rankingProposalsLabels are the counts a proposals popover starts with.
var rankingProposalsLabels = []string{"Due to propose, on heights it signed", "Proposed", "Missed"}

// rankingShown opens the validators card on its Ranking view.
func rankingShown() pageOption {
	return withStorage(map[string]string{validatorsViewKey: "ranking"})
}

// rankingPublished returns the day the server under test serves.
func rankingPublished(e *pageEnv) ranking.Published {
	e.T.Helper()
	return rankingPublishedBy(e.T, e.Srv)
}

// rankingPublishedBy returns the day srv serves, failing t when it serves
// none.
func rankingPublishedBy(t *testing.T, srv *Server) ranking.Published {
	t.Helper()
	pub := srv.Ranking.Snapshot().Published
	if pub == nil {
		t.Fatal("the server under test publishes no ranking")
	}
	return *pub
}

// rankingTableCells returns the text of each cell of the Ranking table's
// rows, row by row.
func (e *pageEnv) rankingTableCells() [][]string {
	e.T.Helper()
	v, err := e.Page.Locator(rankingRows).EvaluateAll(`rows => rows.map(r => [...r.cells].map(c => c.textContent))`)
	if err != nil {
		e.T.Fatalf("read the Ranking table: %v", err)
	}
	return decodeValue[[][]string](e.T, v)
}

// rankingNetworkRows are rankingNetwork's rows, as the Ranking table reads
// them: alpha and charlie share the first rank, alpha's operator first in
// byte order. The uptime is rounded down to 2 decimals, so alpha's and
// charlie's, 6,447 of 6,451 blocks (99.938 %), reads 99.93 %.
func rankingNetworkRows() [][]string {
	return [][]string{
		{"1", "alpha", "100.0", "99.93 % (50.0)", "0 (20.0)", "0 s (20.0)", "0 / 60 (10.0)"},
		{"1", "charlie", "100.0", "99.93 % (50.0)", "0 (20.0)", "0 s (20.0)", "0 / 50 (10.0)"},
		{"3", "bravo", "96.9", "100.00 % (55.6)", "0 (22.2)", "30 min 00 s (19.2)", "—"},
		{"4", shortAddr(valDelta.address()), "51.6", "98.26 % (41.6)", "10 (0.0)", "—", "1 / 100 (10.0)"},
		{rankingExcludedSep},
		{"—", "echo", "100.0", "100.00 % (50.0)", "0 (20.0)", "0 s (20.0)", "0 / 6,241 (10.0)"},
		{rankingWaitingSep},
		{"—", "newcomer", "95 h of 168 h in the set"},
	}
}

// rankingExpectView checks that the validators card shows view, and its
// switch says so.
func rankingExpectView(e *pageEnv, view string) {
	e.T.Helper()
	for _, v := range []string{"live", "timeline", "ranking"} {
		e.must(e.expect(`#validators-view button[data-view="`+v+`"]`).ToHaveAttribute("aria-pressed", strconv.FormatBool(v == view)))
	}
	if view == "ranking" {
		e.must(e.expect(rankingPanel).ToBeVisible())
	} else {
		e.must(e.expect(rankingPanel).ToBeHidden())
	}
}

// ---- The view

// TestPageRankingOfferedOnlyWhenOn checks that the validators card offers
// its Ranking view only on a server with the ranking on, as /api/boot says.
func TestPageRankingOfferedOnlyWhenOn(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		t.Run("off", func(t *testing.T) {
			e := openPage(t, p, healthyNetwork())
			e.must(e.expect("#page-title").ToHaveText(rankingTitle))
			e.must(e.expect(rankingToggle).ToBeHidden())
			rankingExpectView(e, "live")
		})
		t.Run("on", func(t *testing.T) {
			e := openPage(t, p, rankingHealthy())
			toggle := e.expect(rankingToggle)
			e.must(toggle.ToBeVisible())
			e.must(toggle.ToHaveText("Ranking"))
			rankingExpectView(e, "live")
		})
	})
}

// TestPageRankingView checks the Ranking view: it shows the ranking in
// place of the Live table and cards, hides the compact switch and is saved;
// Timeline and Live take its place again.
func TestPageRankingView(t *testing.T) {
	onProfiles(t, []profile{desktop, tablet, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, rankingHealthy())
		liveList := "#validators-card .section-body > .table-wrap"
		if p != desktop {
			liveList = "#validators-cards"
		}
		e.must(e.expect(liveList).ToBeVisible())

		e.press(e.Page.Locator(rankingToggle))
		rankingExpectView(e, "ranking")
		e.must(e.expect("#validators-card").ToContainClass("view-ranking"))
		e.must(e.expect(rankingMeta).ToBeVisible())
		e.must(e.expect(liveList).ToBeHidden())
		e.must(e.expect("#validators-compact-label").ToHaveCSS("visibility", "hidden"))
		expectStored(e, validatorsViewKey, "ranking")

		e.press(e.Page.Locator(validatorsTimelineToggle))
		rankingExpectView(e, "timeline")
		e.must(e.expect("#validators-card").Not().ToContainClass("view-ranking"))
		e.must(e.expect("#validators-timeline").ToBeVisible())

		e.press(e.Page.Locator(rankingToggle))
		rankingExpectView(e, "ranking")
		e.must(e.expect("#validators-timeline").ToBeHidden())

		e.press(e.Page.Locator(validatorsLiveToggle))
		rankingExpectView(e, "live")
		e.must(e.expect("#validators-card").Not().ToContainClass("view-ranking"))
		e.must(e.expect(liveList).ToBeVisible())
		expectStored(e, validatorsViewKey, "live")
	})
}

// TestPageRankingFromTheURL checks ?view=: it opens the card on the view it
// names, over the one saved, and saves nothing, so a link never changes the
// reader's choice; ranking waits for the server's word that the ranking is
// on, and Live shows while it is off. An unknown view leaves the saved one.
func TestPageRankingFromTheURL(t *testing.T) {
	for _, tc := range []struct {
		name, url, stored string // stored "" leaves the key unset
		off               bool   // the server has the ranking off
		want              string
	}{
		{name: "ranking", url: "/?view=ranking", want: "ranking"},
		{name: "ranking over the saved timeline", url: "/?view=ranking", stored: "timeline", want: "ranking"},
		{name: "live over the saved ranking", url: "/?view=live", stored: "ranking", want: "live"},
		{name: "timeline over the saved ranking", url: "/?view=timeline", stored: "ranking", want: "timeline"},
		{name: "ranking while off", url: "/?view=ranking", off: true, want: "live"},
		{name: "ranking while off, timeline saved", url: "/?view=ranking", stored: "timeline", off: true, want: "live"},
		{name: "unknown view", url: "/?view=chart", stored: "timeline", want: "timeline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				sc := rankingHealthy()
				if tc.off {
					sc.ranking = nil
				}
				opts := []pageOption{withURL(tc.url)}
				if tc.stored != "" {
					opts = append(opts, withStorage(map[string]string{validatorsViewKey: tc.stored}))
				}
				e := openPage(t, p, sc, opts...)
				e.must(e.expect("#page-title").ToHaveText(rankingTitle))
				rankingExpectView(e, tc.want)
				if tc.want == "ranking" {
					e.must(e.expect(rankingMeta).ToBeVisible())
				}
				got, set := e.storage(validatorsViewKey)
				if got != tc.stored || set != (tc.stored != "") {
					t.Errorf("localStorage %s = %q (set %v), want %q, as stored", validatorsViewKey, got, set, tc.stored)
				}
			})
		})
	}
}

// TestPageRankingClickDropsTheURLView checks that a click on the switch
// takes ?view= out of the page's address, the entry replaced, its state and
// the address's other parameters kept: a reload opens the view clicked,
// which the click saved, not the link's.
func TestPageRankingClickDropsTheURLView(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, rankingHealthy(), withURL("/?view=ranking&foo=1"))
		rankingExpectView(e, "ranking")
		before, err := e.Page.Evaluate(`() => { history.replaceState({ kept: 1 }, ''); return history.length }`)
		if err != nil {
			t.Fatalf("mark the entry's state: %v", err)
		}
		e.press(e.Page.Locator(`#validators-view button[data-view="live"]`))
		rankingExpectView(e, "live")
		e.must(e.Expect.Page(e.Page).ToHaveURL(e.ts.URL + "/?foo=1"))
		after, err := e.Page.Evaluate(`() => [history.length, history.state && history.state.kept]`)
		if err != nil {
			t.Fatalf("read the history: %v", err)
		}
		if got := decodeValue[[]int](t, after); !slices.Equal(got, []int{decodeValue[int](t, before), 1}) {
			t.Errorf("history.length and the entry's state's kept = %v, want %v, 1: the entry replaced, its state kept", got, before)
		}
		if _, err := e.Page.Reload(); err != nil {
			t.Fatalf("reload: %v", err)
		}
		rankingExpectView(e, "live")
		expectStored(e, validatorsViewKey, "live")
	})
}

// TestPageRankingSavedView checks the saved view: Ranking, chosen, opens
// again on a reload; on a server with the ranking off, the card opens on
// Live, without the Ranking button, and keeps the choice for a server with
// it on. A server's ranking is set at its start, so the second case starts
// a server with it off on the key the first saves.
func TestPageRankingSavedView(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		t.Run("on", func(t *testing.T) {
			e := openPage(t, p, rankingHealthy())
			e.press(e.Page.Locator(rankingToggle))
			rankingExpectView(e, "ranking")
			expectStored(e, validatorsViewKey, "ranking")
			e.navigate("/")
			rankingExpectView(e, "ranking")
			e.must(e.expect(rankingMeta).ToBeVisible())
		})
		t.Run("off", func(t *testing.T) {
			e := openPage(t, p, healthyNetwork(), rankingShown())
			e.must(e.expect("#page-title").ToHaveText(rankingTitle))
			e.must(e.expect(rankingToggle).ToBeHidden())
			rankingExpectView(e, "live")
			expectStored(e, validatorsViewKey, "ranking")
		})
	})
}

// ---- The ranking

// TestPageRankingTable checks the table on a wide screen: the meta line,
// the headers, the ranked rows in rank order, a shared rank repeated; each
// value coloured by its sub-score with its points; a value without a
// sub-score a dash; the excluded and those not ranked yet under their
// separators.
func TestPageRankingTable(t *testing.T) {
	e := openPage(t, desktop, rankingNetwork(t), rankingShown())
	e.must(e.expect(rankingMeta + " > *").ToHaveText([]string{
		rankingNetworkWindow,
		"formula v1 · updated daily after 00:00 UTC",
		"How is this ranked?",
	}))
	e.must(e.expect(rankingMeta+" button.rk-how").ToHaveAttribute("type", "button"))
	e.must(e.expect(rankingTable + " thead th").ToHaveText([]string{
		"#", "Name", "Score", "Uptime (/50)", "Outages (/20)", "Recovery (/20)", "Missed props (/10)",
	}))
	e.must(e.expect(rankingRows).ToHaveCount(len(rankingNetworkRows())))
	if got, want := e.rankingTableCells(), rankingNetworkRows(); !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("rows =\n%q\nwant\n%q", got, want)
	}
	e.must(e.expect(rankingCards).ToBeHidden())

	// A value is green at full marks, orange between, red at zero; its
	// points are grey; a dash has no colour of its own.
	value := func(row, col int) string {
		return fmt.Sprintf("%s:nth-child(%d) td:nth-child(%d) .rk-val", rankingRows, row, col)
	}
	for _, tc := range []struct {
		sel, color string
	}{
		{value(1, 4), greenColor},
		{value(3, 6), yellowColor},
		{value(4, 4), yellowColor},
		{value(4, 5), redColor},
		{value(4, 6), fgColor},
		{value(3, 7), fgColor},
	} {
		e.must(e.expect(tc.sel).ToHaveCSS("color", tc.color))
	}
	e.must(e.expect(rankingRows+":nth-child(1) td:nth-child(4) .rk-pts").ToHaveCSS("color", fg2Color))
	e.must(e.expect(value(4, 6) + " + .rk-pts").ToHaveCount(0))
	e.must(e.expect(rankingRows+":nth-child(5) td").ToHaveAttribute("colspan", "7"))
	e.must(e.expect(rankingRows+":nth-child(8) td.rk-wait").ToHaveAttribute("colspan", "5"))
}

// TestPageRankingCards checks the cards up to 768 px wide, one per entry in
// the table's order, each with its rank and name, and its score or its
// hours in the set, on top, and its values below; the separators come
// between them.
func TestPageRankingCards(t *testing.T) {
	onProfiles(t, []profile{tablet, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, rankingNetwork(t), rankingShown())
		cards := rankingCards + " > .peer-card"
		e.must(e.expect(cards).ToHaveCount(6))
		e.must(e.expect(rankingTable).ToBeHidden())
		v, err := e.Page.Locator(rankingCards + " > *").EvaluateAll(`els => els.map(el => el.className)`)
		if err != nil {
			t.Fatalf("read the cards' classes: %v", err)
		}
		if got, want := decodeValue[[]string](t, v), []string{
			"peer-card", "peer-card", "peer-card", "peer-card", "rk-sep", "peer-card rk-excluded", "rk-sep", "peer-card",
		}; !slices.Equal(got, want) {
			t.Errorf("the cards' list = %q, want %q", got, want)
		}
		e.must(e.expect(rankingCards + " > .rk-sep").ToHaveText([]string{rankingExcludedSep, rankingWaitingSep}))
		e.must(e.expect(cards + " .peer-card-top .rk-rank").ToHaveText([]string{"#1", "#1", "#3", "#4", "—", "—"}))
		e.must(e.expect(cards + " .peer-card-name a.val-link").ToHaveText([]string{
			"alpha", "charlie", "bravo", shortAddr(valDelta.address()), "echo", "newcomer",
		}))
		e.must(e.expect(cards + " .peer-card-top .rk-score").ToHaveText([]string{"100.0", "100.0", "96.9", "51.6", "100.0"}))
		e.must(e.expect(cards + " .peer-card-top .rk-wait").ToHaveText([]string{"95 h of 168 h in the set"}))
		for i, values := range [][]string{
			{"99.93 % (50.0)", "0 (20.0)", "0 s (20.0)", "0 / 60 (10.0)"},
			{"99.93 % (50.0)", "0 (20.0)", "0 s (20.0)", "0 / 50 (10.0)"},
			{"100.00 % (55.6)", "0 (22.2)", "30 min 00 s (19.2)", "—"},
			{"98.26 % (41.6)", "10 (0.0)", "—", "1 / 100 (10.0)"},
			{"100.00 % (50.0)", "0 (20.0)", "0 s (20.0)", "0 / 6,241 (10.0)"},
		} {
			card := e.Page.Locator(cards).Nth(i)
			e.must(e.Expect.Locator(card.Locator(".peer-card-k")).ToHaveText([]string{"uptime", "outages", "recovery", "missed props"}))
			e.must(e.Expect.Locator(card.Locator(".peer-card-v")).ToHaveText(values))
		}
		e.must(e.expect(cards + ":last-child .peer-card-kv").ToHaveCount(0))
		e.must(e.Expect.Locator(e.Page.Locator(cards+":nth-child(1) .peer-card-kv .rk-val").First()).ToHaveCSS("color", greenColor))
		// Delta's outages, its card's second value.
		e.must(e.expect(cards+":nth-child(4) .peer-card-kv > :nth-child(4) .rk-val").ToHaveCSS("color", redColor))
	})
}

// TestPageRankingExcluded checks the operators the exclusion list names:
// listed with their values, no rank, under their separator; hidden with
// --ranking-hide-excluded, the meta line then counting them.
func TestPageRankingExcluded(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		// entries is where the page lists the ranking on p.
		entries := rankingRows
		if p == phone {
			entries = rankingCards + " > *"
		}
		t.Run("listed", func(t *testing.T) {
			e := openPage(t, p, rankingNetwork(t), rankingShown())
			sep := e.Page.Locator(entries).Filter(playwright.LocatorFilterOptions{HasText: rankingExcludedSep})
			e.must(e.Expect.Locator(sep).ToHaveCount(1))
			e.must(e.Expect.Locator(sep.Locator("xpath=following-sibling::*[1]")).ToContainText("echo100.0"))
			e.must(e.expect(rankingMeta).Not().ToContainText("not ranked"))
		})
		for _, tc := range []struct {
			name    string
			exclude []string
			count   string
		}{
			{"one hidden", []string{opRankingEcho}, "1 team and partner validator not ranked"},
			{"two hidden", []string{opRankingEcho, opBravo}, "2 team and partner validators not ranked"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				sc := rankingNetwork(t)
				sc.ranking = &pageRanking{exclude: tc.exclude, hideExcluded: true}
				e := openPage(t, p, sc, rankingShown())
				e.must(e.expect(rankingMeta + " > *").ToHaveText([]string{
					rankingNetworkWindow,
					"formula v1 · updated daily after 00:00 UTC",
					tc.count,
					"How is this ranked?",
				}))
				e.must(e.Expect.Locator(e.Page.Locator(entries).Filter(playwright.LocatorFilterOptions{HasText: rankingExcludedSep})).ToHaveCount(0))
				// Neither the table nor the cards hold them, shown or not.
				for _, op := range tc.exclude {
					e.must(e.expect(rankingPanel + ` a.val-link[href="?val=` + op + `"]`).ToHaveCount(0))
				}
				e.must(e.expect(rankingPanel + ` a.val-link[href="?val=` + valAlpha.address() + `"]`).ToHaveCount(2))
			})
		}
	})
}

// TestPageRankingNoneListed checks a ranking that lists no operator: every
// operator in the set excluded and hidden, which the meta line counts, or
// no block in the window. A line says so, in place of an empty table.
func TestPageRankingNoneListed(t *testing.T) {
	allHidden := rankingHealthy()
	for _, v := range healthyValidators() {
		allHidden.ranking.exclude = append(allHidden.ranking.exclude, v.address())
	}
	allHidden.ranking.hideExcluded = true
	for _, tc := range []struct {
		name string
		sc   *scenario
		meta []string
		line string
	}{
		{
			name: "all hidden",
			sc:   allHidden,
			meta: []string{
				rankingWindowDays + " · heights 1 – 3,600",
				"formula v1 · updated daily after 00:00 UTC",
				"4 team and partner validators not ranked",
				"How is this ranked?",
			},
			line: "No community validator is in the set.",
		},
		{
			name: "no block in the window",
			sc:   rankingTodayOnly(),
			meta: []string{rankingWindowDays, "formula v1 · updated daily after 00:00 UTC", "How is this ranked?"},
			line: "No validator to rank: the window holds no block.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				e := openPage(t, p, tc.sc, rankingShown())
				e.must(e.expect(rankingMeta + " > *").ToHaveText(tc.meta))
				e.must(e.expect(rankingState).ToHaveText(tc.line))
				e.must(e.expect(rankingTable).ToHaveCount(0))
				e.must(e.expect(rankingCards).ToHaveCount(0))
			})
		})
	}
}

// TestPageRankingNotRankedYet checks an operator short of 168 h in the set:
// listed last, under its separator, with its hours in the set rounded
// down, which the published entry gives.
func TestPageRankingNotRankedYet(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, rankingNetwork(t), rankingShown())
		pub := rankingPublished(e)
		last := pub.Entries[len(pub.Entries)-1]
		if last.Status != ranking.NotRankedYet || last.Name != "newcomer" {
			t.Fatalf("last entry = %s, %s; want the newcomer, not ranked yet", last.Name, last.Status)
		}
		hours := fmt.Sprintf("%d h of 168 h in the set", int(math.Floor(last.HoursInSet)))
		if hours != "95 h of 168 h in the set" {
			t.Fatalf("the newcomer's hours read %q, want 95 h: the scenario's 95.5 h rounded down", hours)
		}
		entries := rankingRows
		if p == phone {
			entries = rankingCards + " > *"
		}
		all := e.Page.Locator(entries)
		e.must(e.Expect.Locator(all.Nth(-2)).ToHaveText(rankingWaitingSep))
		e.must(e.Expect.Locator(all.Last()).ToHaveText("—newcomer" + hours))
		e.must(e.Expect.Locator(all.Last().Locator(".rk-wait")).ToHaveCSS("color", fg2Color))
		e.must(e.Expect.Locator(all.Last().Locator(".rk-wait")).ToHaveCSS("font-style", "italic"))
	})
}

// TestPageRankingNameOpensTheValidatorPage checks that a name in the
// ranking opens its operator's validator page.
func TestPageRankingNameOpensTheValidatorPage(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, rankingNetwork(t), rankingShown())
		name := e.Page.Locator(rankingPanel).GetByRole("link", playwright.LocatorGetByRoleOptions{Name: "bravo", Exact: playwright.Bool(true)})
		e.must(e.Expect.Locator(name).ToHaveAttribute("href", "?val="+opBravo))
		e.press(name)
		e.must(e.expect("#val-modal").ToContainClass("open"))
		e.must(e.expect("#val-title").ToHaveText("bravo"))
		e.must(e.Expect.Page(e.Page).ToHaveURL(regexp.MustCompile(`\?val=` + opBravo + `$`)))
	})
}

// ---- The ranking's states

// TestPageRankingFilling checks a ranking filling the history it needs: the
// share stored, and the time left at the fill's rate, an upper bound, when
// measured; once the blocks are stored, the set changes' events still read.
// No ranking shows.
func TestPageRankingFilling(t *testing.T) {
	pending := healthyNetwork()
	pending.ranking = &pageRanking{}
	healthySeed := pending.seed
	pending.seed = func(t *testing.T, s *blocks.Store) {
		healthySeed(t, s)
		if err := s.AddEventGap(t.Context(), pageHeight); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		sc   *scenario
		want string
	}{
		{"time left", rankingFromHeight(rankingFillingProgress(2.5)), "76 % · up to 6 min 40 s left"},
		{"rate unmeasured", rankingFromHeight(rankingFillingProgress(0)), "76 %"},
		{"events pending", pending, "100 % · reading the validator set's changes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				e := openPage(t, p, tc.sc, rankingShown())
				e.must(e.expect(rankingState).ToHaveText(rankingFillingLead + tc.want))
				e.must(e.expect(rankingState + " .rk-bar").ToBeVisible())
				e.must(e.expect(rankingMeta).ToHaveCount(0))
				e.must(e.expect(rankingTable).ToHaveCount(0))
			})
		})
	}
}

// TestPageRankingUnavailable checks a node that keeps no block back to the
// span the ranking needs: the page says from which height it keeps them.
func TestPageRankingUnavailable(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := rankingFromHeight(blocks.Progress{
			ChainID: pageChainID, From: 1000, To: pageHeight, FromTime: healthyBlockTime(1000), Floor: 1000, Complete: true,
		})
		e := openPage(t, p, sc, rankingShown())
		e.must(e.expect(rankingState).ToHaveText("This node keeps no blocks below #1,000, and the ranking needs the last 180 days of blocks."))
		e.must(e.expect(rankingState + " .rk-bar").ToHaveCount(0))
		e.must(e.expect(rankingMeta).ToHaveCount(0))
	})
}

// TestPageRankingWaiting checks a ranking ready with no day published yet,
// here with the chain id not read: a line says so, and no table shows.
func TestPageRankingWaiting(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := rankingHealthy()
		sc.fill.ChainID = ""
		e := openPage(t, p, sc, rankingShown())
		e.must(e.expect(rankingState).ToHaveText("The ranking is being computed for today."))
		e.must(e.expect(rankingMeta).ToHaveCount(0))
		e.must(e.expect(rankingTable).ToHaveCount(0))
	})
}

// ---- Reads

// TestPageRankingPolls checks the view's reads: every 60 s while it is
// shown, none while it is not, whose last answer shows again until the
// read that showing the view starts answers. A state not ready shows above
// the ranking still served.
func TestPageRankingPolls(t *testing.T) {
	sc := rankingHealthy()
	unread := sc.fill
	unread.ChainID = ""
	fill := &settableFill{p: unread}
	sc.fillFrom = fill
	e := openPage(t, desktop, sc, rankingShown(), withInstalledClock())
	e.must(e.expect(rankingState).ToHaveText("The ranking is being computed for today."))

	fill.set(rankingHealthy().fill)
	if err := e.Srv.Ranking.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	e.runFor(rankingPoll)
	e.must(e.expect(rankingMeta).ToBeVisible())
	e.must(e.expect(rankingState).ToHaveCount(0))
	// Every operator is short of 168 h: one row each under the
	// not-ranked-yet separator.
	pub := rankingPublished(e)
	for _, en := range pub.Entries {
		if en.Status != ranking.NotRankedYet {
			t.Fatalf("entry %s is %s, want every entry of rankingHealthy not ranked yet", en.Name, en.Status)
		}
	}
	e.must(e.expect(rankingRows).ToHaveCount(len(pub.Entries) + 1))
	e.must(e.Expect.Locator(e.Page.Locator(rankingRows).First()).ToHaveText(rankingWaitingSep))

	e.press(e.Page.Locator(validatorsLiveToggle))
	rankingExpectView(e, "live")
	fill.set(rankingFillingProgress(0))
	if err := e.Srv.Ranking.Refresh(t.Context()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	for range 3 {
		e.runFor(rankingPoll)
	}
	read := e.hold(rankingRead)
	e.press(e.Page.Locator(rankingToggle))
	rankingExpectView(e, "ranking")
	e.must(e.expect(rankingMeta).ToBeVisible())
	e.must(e.expect(rankingState).ToHaveCount(0))
	read.release()
	e.must(e.expect(rankingState).ToHaveText(rankingFillingLead + "76 %"))
	e.must(e.expect(rankingMeta).ToBeVisible())
}

// TestPageRankingPollKeepsTheFocus checks that a read whose answer is the
// one shown leaves the view as it is: a keyboard user on a name stays on
// it. The next read is asked only once the one before is handled.
func TestPageRankingPollKeepsTheFocus(t *testing.T) {
	e := openPage(t, desktop, rankingNetwork(t), rankingShown(), withInstalledClock())
	name := e.Page.Locator(rankingPanel).GetByRole("link", playwright.LocatorGetByRoleOptions{Name: "bravo", Exact: playwright.Bool(true)})
	e.must(e.Expect.Locator(name).ToBeVisible())
	e.must(name.Focus())
	e.runFor(rankingPoll)
	next := e.hold(rankingRead)
	e.runFor(rankingPoll)
	e.must(e.gate.awaitHeld(rankingRead, 1))
	e.must(e.Expect.Locator(name).ToBeFocused())
	next.release()
}

// TestPageRankingRedrawKeepsTheFocus checks that a read that changes the
// view, a read that fails and the next that answers, leaves the keyboard's
// focus on the control it was on: a header, a name, "How is this ranked?",
// a card's rank, name or value label.
func TestPageRankingRedrawKeepsTheFocus(t *testing.T) {
	inPanel := func(e *pageEnv, sel string) playwright.Locator { return e.Page.Locator(rankingPanel + " " + sel) }
	bravoCard := func(e *pageEnv) playwright.Locator {
		link := e.Page.GetByRole("link", playwright.PageGetByRoleOptions{Name: "bravo", Exact: playwright.Bool(true)})
		return e.Page.Locator(rankingCards + " > .peer-card").Filter(playwright.LocatorFilterOptions{Has: link})
	}
	for _, tc := range []struct {
		name    string
		p       profile
		control func(e *pageEnv) playwright.Locator
	}{
		{"a header", desktop, func(e *pageEnv) playwright.Locator { return inPanel(e, "thead th > button").Nth(2) }},
		{"a name", desktop, func(e *pageEnv) playwright.Locator { return inPanel(e, ".rk-wrap a.val-link").Nth(2) }},
		{"how", desktop, func(e *pageEnv) playwright.Locator { return inPanel(e, ".rk-meta button.rk-how") }},
		{"a card's rank", phone, func(e *pageEnv) playwright.Locator { return bravoCard(e).Locator(".rk-rank") }},
		{"a card's name", phone, func(e *pageEnv) playwright.Locator { return bravoCard(e).Locator("a.val-link") }},
		{"a card's label", phone, func(e *pageEnv) playwright.Locator { return bravoCard(e).Locator(".peer-card-k").Nth(2) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := openPage(t, tc.p, rankingNetwork(t), rankingShown(), withInstalledClock())
			control := tc.control(e)
			e.must(e.Expect.Locator(control).ToBeVisible())
			e.must(control.Focus())
			stuck := e.hold(rankingRead)
			e.runFor(rankingPoll)
			e.runFor(rankingReadBound)
			stuck.awaitAbandoned()
			e.must(e.expect(rankingError).ToBeVisible())
			e.must(e.Expect.Locator(control).ToBeFocused())
			e.runFor(rankingPoll)
			e.must(e.expect(rankingError).ToHaveCount(0))
			e.must(e.Expect.Locator(control).ToBeFocused())
		})
	}
}

// TestPageRankingGivesUpTheReadsItDrops checks that the view gives up a
// read in flight whose answer it would drop: one a newer read replaces, as
// Ranking pressed again starts, and one in flight as the view hides. Each
// case gives up one read: a give-up is declared as the first failure after
// its hold was asked.
func TestPageRankingGivesUpTheReadsItDrops(t *testing.T) {
	t.Run("read anew", func(t *testing.T) {
		e := openPage(t, desktop, rankingHealthy())
		first := e.hold(rankingRead)
		e.press(e.Page.Locator(rankingToggle))
		e.must(e.gate.awaitHeld(rankingRead, 1))
		second := e.hold(rankingRead)
		e.press(e.Page.Locator(rankingToggle))
		first.awaitAbandoned()
		e.must(e.gate.awaitHeld(rankingRead, 1))
		second.release()
		e.must(e.expect(rankingMeta).ToBeVisible())
	})
	t.Run("hidden", func(t *testing.T) {
		e := openPage(t, desktop, rankingHealthy())
		read := e.hold(rankingRead)
		e.press(e.Page.Locator(rankingToggle))
		e.must(e.gate.awaitHeld(rankingRead, 1))
		e.press(e.Page.Locator(validatorsLiveToggle))
		read.awaitAbandoned()
		rankingExpectView(e, "live")
	})
}

// TestPageRankingReadWithoutAnswer checks a read gnockpit never answers:
// the view gives it up after 10 s and says so above the ranking it shows,
// and the next read that answers clears it.
func TestPageRankingReadWithoutAnswer(t *testing.T) {
	e := openPage(t, desktop, rankingHealthy(), rankingShown(), withInstalledClock())
	e.must(e.expect(rankingMeta).ToBeVisible())

	stuck := e.hold(rankingRead)
	e.runFor(rankingPoll)
	e.runFor(rankingReadBound)
	stuck.awaitAbandoned()
	e.must(e.expect(rankingError).ToHaveText("Could not read the ranking (no answer in 10 s): trying again shortly."))
	e.must(e.expect(rankingError).ToHaveCSS("color", redColor))
	e.must(e.expect(rankingMeta).ToBeVisible())

	e.runFor(rankingPoll)
	e.must(e.expect(rankingError).ToHaveCount(0))
	e.must(e.expect(rankingMeta).ToBeVisible())
}

// ---- Explanations

// rankingHeaderHelp are the Ranking table's header popovers, in the
// headers' order: what each column counts, its mapping and its weight.
var rankingHeaderHelp = []string{
	"Order: the higher score, then the higher uptime, then the shorter longest absence (its longest run of missed blocks in the window, recovery runs left out), " +
		"then the faster recovery (no restart sample last), then the lower missed-proposal share (skipped while proposals are disabled), " +
		"each compared unrounded. Operators equal on all five share a rank (1, 1, 3): neither an address nor a name decides between them. " +
		"Two gnockpits reading the same chain, with the same exclusion list, print the same order, unless a valoper profile or a key link changed between their computations.",
	"The operator, ranked across its signing keys: a key rotation keeps its record. Keys are linked to operators by chain records only " +
		"(the operator's valoper profile and the validator set's events), never by name; a key no record links is ranked on its own. " +
		"Only the operators in the validator set at the window's last height are listed; one that left keeps its record on its validator page.",
	"0 to 100, shown to one decimal, rounded down: 50 % uptime, 20 % outages, 20 % recovery, 10 % proposals, each scored 0 to 1. " +
		"A metric without data (such as no restart sample, no proposal due, or proposals disabled) passes its weight to the others in proportion to theirs.",
	"Blocks the validator signed, of the blocks it was in the set for, in the window. A nil vote counts as signed; " +
		"a missing vote or a vote for another block does not. The blocks it missed around a chain restart (its run of misses through or just before it, within 24 h of it) " +
		"are left out: Recovery times the restart instead. 95 % or less scores 0, 99.9 % or more scores 1, linear between. 50 % of the score.",
	"Times the validator missed 10 or more blocks in a row in the window, a run counting once 10 of its blocks fall inside it; " +
		"blocks missed around a chain restart, within 24 h of it, are left out. Twenty short outages and one long one can miss the same blocks; " +
		"the first is a setup to fix. None scores 1, 10 or more score 0, linear between. 20 % of the score.",
	"After a chain restart (two consecutive blocks more than 2 minutes apart: a halt, an upgrade or a stall), the time until the validator signs again; " +
		"one added at the restart, or already in an outage before it, gets no sample. The median of its last 3 samples within the " +
		"180 complete UTC days before today, so one bad restart does not decide alone. 10 min or less scores 1, 1 h 0.66, 6 h 0.33, " +
		"24 h or more 0, linear between. 20 % of the score.",
	"Rounds the validator was due to propose and did not, of the rounds it was due to propose, counted on the heights it signed, " +
		"restart heights left out: a validator that is down already loses those blocks through Uptime. Who was due is recomputed from " +
		"the validator sets' proposer priorities; a block whose header names none of its rounds' recomputed proposers disables this metric " +
		"for every validator, in every window holding that block, and /api/ranking lists those heights. 0 % scores 1, 5 % or more scores 0, " +
		"linear between. 10 % of the score.",
}

// rankingValueParts are the scored values of an entry, in the order its
// row and its card show them.
var rankingValueParts = []string{"uptime", "outages", "recovery", "proposals"}

// rankingValue locates what the Ranking view shows of the entry named name
// on p: its score, one of rankingValueParts, or, not ranked yet, its hours
// in the set (wait); a cell of its row on a wide screen, a part of its card
// on a phone.
func rankingValue(e *pageEnv, p profile, name, part string) playwright.Locator {
	e.T.Helper()
	link := e.Page.GetByRole("link", playwright.PageGetByRoleOptions{Name: name, Exact: playwright.Bool(true)})
	entry := e.Page.Locator(rankingRows).Filter(playwright.LocatorFilterOptions{Has: link})
	score, wait, values := "td.score", "td.rk-wait", "td.num:not(.score)"
	if p == phone {
		entry = e.Page.Locator(rankingCards + " > .peer-card").Filter(playwright.LocatorFilterOptions{Has: link})
		score, wait, values = ".rk-score", ".rk-wait", ".peer-card-v"
	}
	switch part {
	case "score":
		return entry.Locator(score)
	case "wait":
		return entry.Locator(wait)
	}
	i := slices.Index(rankingValueParts, part)
	if i < 0 {
		e.T.Fatalf("rankingValue: no value %q, want score, wait or one of %q", part, rankingValueParts)
	}
	return entry.Locator(values).Nth(i)
}

// rankingPartPopover is a popover of rankingNetwork's ranking: the entry it
// belongs to, its value, and what it shows.
type rankingPartPopover struct {
	entry, part string
	want        rowsPopover
}

// rankingNetworkPopovers are rankingNetwork's value popovers: each value's
// counts, sub-score and points, a value without data and where its weight
// goes; the score's parts, ranked and excluded; the hours of one not ranked
// yet and the day it would be ranked from, 4 days after the published day,
// 2026-11-18, as 95.5 h plus 24 h a day first reach 168 h on the fourth.
func rankingNetworkPopovers() []rankingPartPopover {
	delta := shortAddr(valDelta.address())
	uptimeLabels := func(leftOut bool) []string {
		l := []string{"Blocks in the set", "Signed", "of which nil votes", "Missing from the commit", "Voted for another block"}
		if leftOut {
			l = append(l, "Left out: missed around restarts")
		}
		return append(l, "Counted", "Uptime", "Sub-score (95 % → 0, 99.9 % → 1)", "Points")
	}
	// outageStarts are delta's outages' first blocks: the first n hours
	// of 2026-11-12.
	outageStarts := func(n int) []string {
		var l []string
		for hour := 1; hour <= n; hour++ {
			l = append(l, fmt.Sprintf("Nov 12, %02d:00 UTC", hour))
		}
		return l
	}
	repeat := func(s string, n int) []string { return slices.Repeat([]string{s}, n) }
	uptimeTitle := "Uptime, " + rankingWindowSpan
	outagesTitle := "Outages, " + rankingWindowSpan
	proposalsTitle := "Proposals, " + rankingWindowSpan
	recoveryLabels := []string{"Nov 9, restart after block 361", "Median", rankingRecoverySub, "Points"}
	parts := []string{"Uptime", "Outages", "Recovery", "Proposals", "Score"}
	return []rankingPartPopover{
		{"bravo", "uptime", rowsPopover{
			title:    uptimeTitle,
			labels:   uptimeLabels(true),
			values:   []string{"6,451", "6,436", "0", "15", "0", "−15", "6,436", "100.000 %", "1.000", "55.6 of 55.6"},
			swatches: []string{greenColor, redColor, redColor},
		}},
		{delta, "uptime", rowsPopover{
			title:    uptimeTitle,
			labels:   uptimeLabels(false),
			values:   []string{"5,760", "5,660", "0", "100", "0", "5,760", "98.263 %", "0.666", "41.6 of 62.5"},
			swatches: []string{greenColor, redColor, redColor},
		}},
		{"alpha", "outages", rowsPopover{
			title:  outagesTitle,
			labels: []string{"Runs of 10 or more missed blocks", "Longest absence", "Sub-score (0 → 1, 10 → 0)", "Points"},
			values: []string{"none", "1 block", "1.000", "20.0 of 20.0"},
		}},
		{delta, "outages", rowsPopover{
			title:    outagesTitle,
			labels:   append(outageStarts(10), "Longest absence", "Sub-score (0 → 1, 10 → 0)", "Points"),
			values:   append(repeat("10 blocks · 20 min 00 s", 10), "10 blocks", "0.000", "0.0 of 25.0"),
			swatches: repeat(redColor, 10),
		}},
		{"alpha", "recovery", rowsPopover{
			title:  "Recovery, its last restart within 180 days",
			labels: recoveryLabels,
			values: []string{"0 s", "0 s", "1.000", "20.0 of 20.0"},
		}},
		{"bravo", "recovery", rowsPopover{
			title:  "Recovery, its last restart within 180 days",
			labels: recoveryLabels,
			values: []string{"30 min 00 s", "30 min 00 s", "0.864", "19.2 of 22.2"},
		}},
		{delta, "recovery", rowsPopover{
			title:  "Recovery: no sample yet",
			labels: []string{"Samples within 180 days", "Sub-score", "Its 20 %"},
			values: []string{"none: no restart it was signing before and signed again after", "none without a sample", "goes to the other metrics in proportion"},
		}},
		{delta, "proposals", rowsPopover{
			title:    proposalsTitle,
			labels:   slices.Concat(rankingProposalsLabels, []string{"Missed share", rankingProposalsSub, "Points"}),
			values:   []string{"100", "99", "1", "1.00 %", "0.800", "10.0 of 12.5"},
			swatches: []string{greenColor, redColor},
		}},
		{"bravo", "proposals", rowsPopover{
			title:    proposalsTitle,
			labels:   slices.Concat(rankingProposalsLabels, []string{"Sub-score", "Its 10 %"}),
			values:   []string{"0", "0", "0", "none without a proposal due", "goes to the other metrics in proportion"},
			swatches: []string{greenColor, redColor},
		}},
		{"bravo", "score", rowsPopover{
			title:  "Rank 3 of 4 ranked operators",
			labels: parts,
			values: []string{"55.6 of 55.6", "22.2 of 22.2", "19.2 of 22.2", "no data: its weight spread over the others", "96.9 of 100"},
		}},
		{"echo", "score", rowsPopover{
			title:  "Not ranked: on this dashboard's exclusion list",
			labels: parts,
			values: []string{"50.0 of 50.0", "20.0 of 20.0", "20.0 of 20.0", "10.0 of 10.0", "100.0 of 100"},
		}},
		{"newcomer", "wait", rowsPopover{
			title:  "Not ranked yet",
			labels: []string{"In the set within the window", "Needed to be ranked", "Ranked from about"},
			values: []string{"95 h", "168 h (7 days)", "Nov 22 (if it stays in the set)"},
		}},
	}
}

// TestPageRankingHeaderPopovers checks the Ranking table's headers: each a
// button whose popover says what its column counts, its mapping and its
// weight, the rank's the order and its ties. On a phone, a card's rank and
// its values' labels, each focusable, carry their columns' popovers: the
// rank's, then the four values'. The dash of a card without a rank, excluded
// or not ranked yet, carries none and takes no focus.
func TestPageRankingHeaderPopovers(t *testing.T) {
	t.Run("desktop", func(t *testing.T) {
		e := openPage(t, desktop, rankingNetwork(t), rankingShown())
		heads := e.Page.Locator(rankingTable + " thead th > button.info-btn")
		e.must(e.Expect.Locator(heads).ToHaveCount(len(rankingHeaderHelp)))
		for i, want := range rankingHeaderHelp {
			head := heads.Nth(i)
			e.must(e.Expect.Locator(head).ToHaveAttribute("type", "button"))
			e.showPopover(head)
			e.must(e.expect(pagePopover).ToHaveText(want))
		}
	})
	t.Run("phone", func(t *testing.T) {
		e := openPage(t, phone, rankingNetwork(t), rankingShown())
		card := e.Page.Locator(rankingCards + " > .peer-card").First()
		heads := card.Locator(".info-btn")
		wants := []string{rankingHeaderHelp[0], rankingHeaderHelp[3], rankingHeaderHelp[4], rankingHeaderHelp[5], rankingHeaderHelp[6]}
		e.must(e.Expect.Locator(heads).ToHaveText([]string{"#1", "uptime", "outages", "recovery", "missed props"}))
		e.must(e.Expect.Locator(card.Locator("button.info-btn")).ToHaveCount(5))
		for i, want := range wants {
			head := heads.Nth(i)
			e.must(e.Expect.Locator(head).ToHaveAttribute("type", "button"))
			e.showPopover(head)
			e.must(e.expect(pagePopover).ToHaveText(want))
		}
		someValue := regexp.MustCompile(`.*`)
		for _, name := range []string{"echo", "newcomer"} {
			link := e.Page.GetByRole("link", playwright.PageGetByRoleOptions{Name: name, Exact: playwright.Bool(true)})
			rank := e.Page.Locator(rankingCards + " > .peer-card").Filter(playwright.LocatorFilterOptions{Has: link}).Locator(".rk-rank")
			e.must(e.Expect.Locator(rank).ToHaveText("—"))
			e.must(e.Expect.Locator(rank).Not().ToHaveClass(regexp.MustCompile(`\binfo-btn\b`)))
			e.must(e.Expect.Locator(rank).Not().ToHaveAttribute("tabindex", someValue))
			e.must(e.Expect.Locator(rank).Not().ToHaveAttribute("data-popover", someValue))
		}
	})
}

// TestPageRankingValuePopovers checks the popover of each kind of value, on
// a row and on a card: from the chain's counts, which the published inputs
// give, to the sub-score and the points; a value without data and where
// its weight goes; the score's four parts; the hours of an operator not
// ranked yet.
func TestPageRankingValuePopovers(t *testing.T) {
	// The uptime popovers' counts are the published inputs: bravo's and
	// delta's, whose operator is its signing address.
	sc := rankingNetwork(t)
	entries := rankingPublishedBy(t, newPageServer(t, sc, &serverClock{at: sc.clock()})).Entries
	for _, want := range []struct {
		operator string
		inputs   ranking.Inputs
	}{
		{opBravo, ranking.Inputs{InSet: 6451, Recovering: 15, Signed: 6436, Absent: 15}},
		{valDelta.address(), ranking.Inputs{InSet: 5760, Signed: 5660, Absent: 100}},
	} {
		i := slices.IndexFunc(entries, func(en ranking.Entry) bool { return en.Operator == want.operator })
		if i < 0 {
			t.Fatalf("no published entry of operator %s", want.operator)
		}
		got := entries[i].Inputs
		counts := [6]int64{got.InSet, got.Recovering, got.Signed, got.Nil, got.Absent, got.OtherBlock}
		wantCounts := [6]int64{want.inputs.InSet, want.inputs.Recovering, want.inputs.Signed, want.inputs.Nil, want.inputs.Absent, want.inputs.OtherBlock}
		if counts != wantCounts {
			t.Fatalf("operator %s's published in set, recovering, signed, nil, absent, other block = %v, want %v", want.operator, counts, wantCounts)
		}
	}
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, rankingNetwork(t), rankingShown())
		for _, tc := range rankingNetworkPopovers() {
			e.showPopover(rankingValue(e, p, tc.entry, tc.part))
			e.expectPopover(tc.want)
		}
	})
}

// TestPageRankingProposalsDisabledPopover checks the proposals popover
// while a block of the window disables the proposals sub-score: it says
// so, and where its weight goes.
func TestPageRankingProposalsDisabledPopover(t *testing.T) {
	e := openPage(t, desktop, rankingProposalsDisabled(t), rankingShown())
	if got := rankingPublished(e).ProposalsDisabled; !slices.Equal(got, []int64{100}) {
		t.Fatalf("published proposals_disabled = %v, want [100]", got)
	}
	e.showPopover(rankingValue(e, desktop, shortAddr(valDelta.address()), "proposals"))
	e.expectPopover(rowsPopover{
		title:    "Proposals, " + rankingWindowSpan,
		labels:   []string{"Due to propose, on heights it signed", "Proposed", "Missed", "Missed share", "Sub-score", "Its 10 %"},
		values:   []string{"100", "99", "1", "1.00 %", "disabled: 1 height whose proposer could not be checked", "goes to the other metrics in proportion"},
		swatches: []string{greenColor, redColor},
	})
}

// TestPageRankingRestartsPopovers checks rankingRestartsNetwork's
// popovers: bravo's recovery lists its three samples, by restart, and their
// median; delta's proposals write a missed share rounded up, a sub-score and
// points rounded down, none better than it is: 1 of 150 (0.666… %) reading
// 0.67 %, 0.8666… reading 0.866 and its 8.666… points 8.6; alpha's score
// names the two operators sharing its rank, in the listing's order.
func TestPageRankingRestartsPopovers(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, rankingRestartsNetwork(), rankingShown())
		for _, tc := range []rankingPartPopover{
			{"bravo", "recovery", rowsPopover{
				title: "Recovery, its last 3 restarts within 180 days",
				labels: []string{
					"Nov 10, restart after block 1,081", "Nov 12, restart after block 2,492", "Nov 14, restart after block 3,903",
					"Median", rankingRecoverySub, "Points",
				},
				values: []string{"10 min 00 s", "30 min 00 s", "20 min 00 s", "20 min 00 s", "0.932", "20.7 of 22.2"},
			}},
			{shortAddr(valDelta.address()), "proposals", rowsPopover{
				title:    "Proposals, " + rankingWindowSpan,
				labels:   slices.Concat(rankingProposalsLabels, []string{"Missed share", rankingProposalsSub, "Points"}),
				values:   []string{"150", "149", "1", "0.67 %", "0.866", "8.6 of 10.0"},
				swatches: []string{greenColor, redColor},
			}},
			{"alpha", "score", rowsPopover{
				title:  "Rank 1 of 5 ranked operators",
				labels: []string{"Uptime", "Outages", "Recovery", "Proposals", "Score", "Rank shared with"},
				values: []string{
					"55.6 of 55.6", "22.2 of 22.2", "22.2 of 22.2", "no data: its weight spread over the others", "100.0 of 100",
					"echo, charlie: equal on the score and every tie-break",
				},
			}},
		} {
			e.showPopover(rankingValue(e, p, tc.entry, tc.part))
			e.expectPopover(tc.want)
		}
	})
}

// TestPageRankingNeverReadsBetterThanItIs checks the figures of an entry
// just short of full marks, echo's with one proposal missed of 6,241: its
// proposals' points are not the column's maximum, nor its score 100.0, and
// its missed share is not 0.00 %, each rounded the way that never reads
// better than the value, which the orange colour gives.
func TestPageRankingNeverReadsBetterThanItIs(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, rankingEchoMisses(t), rankingShown())
		if p == desktop {
			e.must(e.expect(rankingRows + ":nth-child(6) td").ToHaveText([]string{
				"—", "echo", "99.9", "100.00 % (50.0)", "0 (20.0)", "0 s (20.0)", "1 / 6,241 (9.9)",
			}))
		}
		e.must(e.Expect.Locator(rankingValue(e, p, "echo", "proposals").Locator(".rk-val")).ToHaveCSS("color", yellowColor))
		for _, tc := range []rankingPartPopover{
			{"echo", "proposals", rowsPopover{
				title:    "Proposals, " + rankingWindowSpan,
				labels:   slices.Concat(rankingProposalsLabels, []string{"Missed share", rankingProposalsSub, "Points"}),
				values:   []string{"6,241", "6,240", "1", "0.02 %", "0.996", "9.9 of 10.0"},
				swatches: []string{greenColor, redColor},
			}},
			{"echo", "score", rowsPopover{
				title:  "Not ranked: on this dashboard's exclusion list",
				labels: []string{"Uptime", "Outages", "Recovery", "Proposals", "Score"},
				values: []string{"50.0 of 50.0", "20.0 of 20.0", "20.0 of 20.0", "9.9 of 10.0", "99.9 of 100"},
			}},
		} {
			e.showPopover(rankingValue(e, p, tc.entry, tc.part))
			e.expectPopover(tc.want)
		}
	})
}

// TestPageRankingSharedRankPopover checks the score popover of a shared
// rank: it names the operators sharing it.
func TestPageRankingSharedRankPopover(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, rankingNetwork(t), rankingShown())
		for _, tc := range [][2]string{{"alpha", "charlie"}, {"charlie", "alpha"}} {
			e.showPopover(rankingValue(e, p, tc[0], "score"))
			e.expectPopover(rowsPopover{
				title:  "Rank 1 of 4 ranked operators",
				labels: []string{"Uptime", "Outages", "Recovery", "Proposals", "Score", "Rank shared with"},
				values: []string{"50.0 of 50.0", "20.0 of 20.0", "20.0 of 20.0", "10.0 of 10.0", "100.0 of 100", tc[1] + ": equal on the score and every tie-break"},
			})
		}
	})
}

// TestPageRankingHiddenPopover checks the meta line's count of the
// excluded operators hidden: its popover names them, as the excluded are
// listed, by score, each with its short address; delta, without a moniker,
// by its short address alone. The keyboard reaches it too.
func TestPageRankingHiddenPopover(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := rankingNetwork(t)
		sc.ranking = &pageRanking{exclude: []string{opRankingEcho, opBravo, valDelta.address()}, hideExcluded: true}
		e := openPage(t, p, sc, rankingShown())
		count := rankingMeta + " > button.info-btn"
		e.must(e.expect(count).ToHaveText("3 team and partner validators not ranked"))
		e.must(e.expect(count).ToHaveAttribute("type", "button"))
		hidden := rowsPopover{
			title:  "Not ranked: this dashboard's exclusion list",
			labels: []string{"echo", "bravo", shortAddr(valDelta.address())},
			values: []string{shortAddr(opRankingEcho), shortAddr(opBravo), ""},
		}
		e.showPopover(e.Page.Locator(count))
		e.expectPopover(hidden)
		if p == desktop {
			e.must(e.Page.Mouse().Move(0, 0))
			e.must(e.expect(pagePopover).ToBeHidden())
			focusByKeyboard(e, count)
			e.expectPopover(hidden)
		}
	})
}

// TestPageRankingExplainer checks "How is this ranked?": a modal dialog
// named by its title, the focus on its Close button, its window, heights
// and formula from the ranking shown and its curl line on the page's own
// origin; ×, Escape and the backdrop close it, giving the focus back to the
// button, and a click inside it leaves it open. Tab and Shift+Tab stay in
// it, and Tab from outside comes back to it. A window without a block has
// no heights.
func TestPageRankingExplainer(t *testing.T) {
	const (
		dialogName = "How the ranking works"
		formula    = "A fixed formula. Every weight and threshold is fixed in gnockpit's code, never a setting. This is formula v%d; any change raises the version."
		window     = "A fixed window. The 30 complete UTC days before today; for this ranking, " + rankingWindowSpan + "%s. " +
			"A block counts on the UTC day of its header time. The ranking is recomputed daily, as soon as the first block after 00:00 UTC is stored."
	)
	// open opens the dialog from the meta line's button.
	open := func(e *pageEnv) (dialog, closeBtn, how playwright.Locator) {
		e.T.Helper()
		how = e.Page.Locator(rankingMeta + " button.rk-how")
		dialog = e.Page.GetByRole("dialog", playwright.PageGetByRoleOptions{Name: dialogName, Exact: playwright.Bool(true)})
		closeBtn = dialog.GetByRole("button", playwright.LocatorGetByRoleOptions{Name: "Close", Exact: playwright.Bool(true)})
		e.press(how)
		e.must(e.Expect.Locator(dialog).ToBeVisible())
		e.must(e.Expect.Locator(closeBtn).ToBeFocused())
		return dialog, closeBtn, how
	}
	sentence := func(dialog playwright.Locator, lead string) playwright.Locator {
		return dialog.Locator("li").Filter(playwright.LocatorFilterOptions{HasText: lead})
	}
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, rankingNetwork(t), rankingShown())
		pub := rankingPublished(e)
		e.must(e.expect(rankingModal).ToBeHidden())
		dialog, closeBtn, how := open(e)
		e.must(e.Expect.Locator(dialog).ToHaveAttribute("aria-modal", "true"))
		e.must(e.expect("#ranking-title").ToHaveText(dialogName))
		heights := ", heights " + thousands(pub.FirstHeight) + " to " + thousands(pub.LastHeight)
		e.must(e.Expect.Locator(sentence(dialog, "A fixed window.")).ToHaveText(fmt.Sprintf(window, heights)))
		e.must(e.Expect.Locator(sentence(dialog, "A fixed formula.")).ToHaveText(fmt.Sprintf(formula, pub.Formula)))
		e.must(e.Expect.Locator(dialog.Locator("pre")).ToHaveText("curl -s " + e.ts.URL + "/api/ranking"))

		closed := func(how playwright.Locator, by string) {
			e.T.Helper()
			e.must(e.expect(rankingModal).ToBeHidden())
			if err := e.Expect.Locator(how).ToBeFocused(); err != nil {
				t.Errorf("closed by %s: %v", by, err)
			}
		}
		e.press(closeBtn)
		closed(how, "×")

		open(e)
		e.must(e.Page.Keyboard().Press("Escape"))
		closed(how, "Escape")

		open(e)
		e.must(e.Page.Locator(rankingModalInner).Click())
		e.must(e.expect(rankingModal).ToBeVisible())
		e.must(e.Page.Locator(rankingModal).Click(playwright.LocatorClickOptions{Position: &playwright.Position{X: 5, Y: 5}}))
		closed(how, "the backdrop")
	})
	t.Run("Tab", func(t *testing.T) {
		e := openPage(t, desktop, rankingNetwork(t), rankingShown())
		_, closeBtn, _ := open(e)
		kb := e.Page.Keyboard()
		e.must(kb.Press("Tab"))
		e.must(e.Expect.Locator(closeBtn).ToBeFocused())
		e.must(kb.Press("Shift+Tab"))
		e.must(e.Expect.Locator(closeBtn).ToBeFocused())
		e.must(closeBtn.Blur())
		e.must(kb.Press("Tab"))
		e.must(e.Expect.Locator(closeBtn).ToBeFocused())
	})
	t.Run("no block in the window", func(t *testing.T) {
		e := openPage(t, desktop, rankingTodayOnly(), rankingShown())
		dialog, _, _ := open(e)
		e.must(e.Expect.Locator(sentence(dialog, "A fixed window.")).ToHaveText(fmt.Sprintf(window, ", which holds no block")))
	})
}

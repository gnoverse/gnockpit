//go:build page

package web

import (
	"cmp"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/gnoverse/gnockpit/node"
)

// ---- Table

// validatorsHeaders are the Validators table's headers.
const validatorsHeaders = "#validators-card thead th"

// validatorsKeys are the sort keys of the Validators table's columns, in
// order.
var validatorsKeys = []string{"name", "address", "country", "provider", "vp", "spof", "missed", "missed24h", "delay", "props"}

// TestPageValidatorsTableHead checks the Validators table's header on a
// desktop: its columns, in order, their labels and widths, the name marked
// as the order; the cells are clipped to their columns. Before any data the
// table says it is loading.
func TestPageValidatorsTableHead(t *testing.T) {
	sc := healthyNetwork()
	sc.snap = nil
	e := openPage(t, desktop, sc)
	expectTableHead(e, "#validators-card", validatorsKeys, []string{
		"Name\u00a0▲", "Signing address", "Country", "Provider", "VP", "SPOF",
		"Missed last 100", "Missed 24h", "Vote delay", "Missed props 24h",
	})
	e.must(e.expect("#validators-header").ToHaveText("Validators"))
	e.must(e.expect("#validators-body tr").ToHaveCount(1))
	e.must(e.expect("#validators-body td").ToHaveText("loading..."))
	e.must(e.expect("#validators-body td").ToHaveAttribute("colspan", "10"))

	expectColumnWidths(e, "#validators-card", validatorsKeys, []float64{13, 13, 9, 13, 8, 6, 10, 9, 8, 11})
	for _, cell := range []string{validatorsHeaders + ":first-child", "#validators-body td"} {
		l := e.expect(cell)
		e.must(l.ToHaveCSS("padding-left", "14px"))
		e.must(l.ToHaveCSS("padding-right", "14px"))
		e.must(l.ToHaveCSS("text-overflow", "ellipsis"))
	}
	e.must(e.expect("#validators-body td").ToHaveCSS("white-space", "nowrap"))
	e.must(e.expect("#validators-card table").ToHaveCSS("table-layout", "fixed"))
}

// TestPageValidatorsHeaderLayout checks the Validators card's header: on a
// screen wider than 640 px the Live / Timeline toggle is centred in it; on a
// phone the header is a row.
func TestPageValidatorsHeaderLayout(t *testing.T) {
	onProfiles(t, []profile{desktop, tablet, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		const header = "#validators-card > .card-header-row"
		if p == phone {
			e.must(e.expect(header).ToHaveCSS("display", "flex"))
			return
		}
		e.must(e.expect(header).ToHaveCSS("display", "grid"))
		h, seg := boxOf(e, header), boxOf(e, "#validators-view")
		if d := seg.X + seg.Width/2 - (h.X + h.Width/2); math.Abs(d) > 2 {
			t.Errorf("the Live / Timeline toggle is %.1f px off the header's centre", d)
		}
	})
}

// TestPageValidatorsTableRows checks a row of the Validators table per
// validator, by name, each cell's text, colour and data: the name links the
// validator's page and marks the source's own; the signing address keeps its
// end in view and gives the address and key; the country and provider come
// from the validator's node; the voting power with its share; whether it is
// a single point of failure; its misses of the last 100 blocks with their
// split, and of the last day; its vote delay; its proposals.
func TestPageValidatorsTableRows(t *testing.T) {
	specs := validatorsSortSpecs()
	e := openPage(t, desktop, specNetwork(specs...))
	e.must(e.expect("#validators-header").ToHaveText("Validators (4)"))
	validatorsExpectRows(e, "alpha", "bravo", "charlie", "echo")
	e.must(e.expect("#validators-body tr > td:nth-child(10)").ToHaveCount(4))
	e.must(e.expect("#validators-body tr > td:nth-child(11)").ToHaveCount(0))
	since := validatorsHistorySince(specs)

	for _, s := range specs {
		name, addr := s.val.name(), s.val.address()
		cell := func(col int) playwright.LocatorAssertions { return e.expect(validatorsCell(s.val, col)) }

		nameCell := cell(valColName)
		e.must(nameCell.ToHaveAttribute("data-highlight", name))
		e.must(nameCell.ToHaveAttribute("data-copy", name))
		link := e.expect(validatorsCell(s.val, valColName) + " > a.val-link")
		e.must(link.ToHaveText(name))
		e.must(link.ToHaveAttribute("href", "?val="+addr))
		if name == "alpha" {
			e.must(nameCell.ToHaveText(name + " (source)"))
			e.must(e.expect(validatorsCell(s.val, valColName)+" > .source-tag").ToHaveCSS("color", blueColor))
		} else {
			e.must(nameCell.ToHaveText(name))
		}

		addrCell := validatorsCell(s.val, valColAddress)
		expectMidTrunc(e, addrCell, addr)
		pubKey := s.val.pubKey().Bech32()
		e.must(cell(valColAddress).ToHaveAttribute("data-copy", addr+" "+pubKey))
		e.must(cell(valColAddress).ToHaveAttribute("data-highlight", name))
		e.must(cell(valColAddress).ToHaveCSS("font-size", "11px"))
		e.must(cell(valColAddress).ToHaveCSS("color", fg2Color))
		if title, rows := popRows(e, addrCell); title != "" || !slices.Equal(rows, [][2]string{{"address", addr}, {"pubkey", pubKey}}) {
			t.Errorf("%s: address popover = %q %q, want no title, the address and the key", name, title, rows)
		}

		e.must(cell(valColCountry).ToHaveAttribute("data-cc", s.node.Country))
		e.must(e.expect(validatorsCell(s.val, valColProvider)).ToHaveText(s.node.Provider + " ⓘ"))

		// The powers sum to 100: each is its own share.
		e.must(cell(valColVP).ToHaveText(fmt.Sprintf("%d (%d%%)", s.val.power, s.val.power)))
		e.must(cell(valColVP).ToHaveAttribute("data-copy", strconv.FormatInt(s.val.power, 10)))
		e.must(cell(valColVP).ToHaveCSS("font-size", "12px"))
		e.must(e.expect(validatorsCell(s.val, valColVP)+" > span").ToHaveCSS("color", fg2Color))

		if name == "alpha" {
			e.must(cell(valColSPOF).ToHaveText("yes"))
			e.must(cell(valColSPOF).ToHaveClass("err"))
		} else {
			e.must(cell(valColSPOF).ToHaveText("no"))
			e.must(cell(valColSPOF).ToHaveCSS("color", fg2Color))
		}

		missed := strconv.Itoa(s.signing.Missed)
		e.must(cell(valColMissed).ToHaveText(missed))
		band := "warn"
		if s.signing.Missed == 0 {
			band = "ok"
		}
		e.must(cell(valColMissed).ToHaveClass(band))
		title, rows := popRows(e, validatorsCell(s.val, valColMissed))
		if want := validatorsMissedRows(s.signing.Missed, s.signing.OtherBlock, s.signing.Nil); title != "last 100 blocks" || !slices.Equal(rows, want) {
			t.Errorf("%s: missed popover = %q %q, want %q %q", name, title, rows, "last 100 blocks", want)
		}

		absent := strconv.Itoa(s.absent)
		e.must(cell(valColMissed24h).ToHaveText(absent))
		e.must(cell(valColMissed24h).ToHaveAttribute("data-copy", absent))
		e.must(cell(valColMissed24h).ToHaveAttribute("data-popover", "missed in last 24h · history since "+since))
		e.must(cell(valColMissed24h).ToHaveClass(regexp.MustCompile(`\btap-popover\b`)))

		delay := e.expect(validatorsCell(s.val, valColDelay) + " > span")
		e.must(delay.ToHaveText(validatorsDelayText(s.signing.VoteDelayMs)))
		e.must(delay.ToHaveClass(validatorsDelayClass(s.signing.VoteDelayMs)))

		e.must(cell(valColProps).ToHaveText(fmt.Sprintf("%d / %d", s.missedProposals, s.proposals)))
	}
}

// TestPageValidatorsCellsMatchTheAPI checks that each row of the Validators
// table shows what /api/status says of its validator: its vote delay, with
// its colour, its proposals, and the split of its misses.
func TestPageValidatorsCellsMatchTheAPI(t *testing.T) {
	e := openPage(t, desktop, specNetwork(validatorsSortSpecs()...))
	e.must(e.expect("#validators-body tr").ToHaveCount(4))
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, e.ts.URL+"/api/status", nil)
	e.must(err)
	res, err := http.DefaultClient.Do(req)
	e.must(err)
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	var rep StatusReport
	e.must(json.NewDecoder(res.Body).Decode(&rep))
	if len(rep.Validators) != 4 {
		t.Fatalf("/api/status lists %d validators, want 4", len(rep.Validators))
	}
	for _, v := range rep.Validators {
		if v.VoteDelayMs == nil {
			t.Fatalf("/api/status gives %s no vote delay: the test's network has one for each", v.Name)
		}
		row := fmt.Sprintf(`#validators-body tr[data-key=%q]`, v.Address)
		delay := e.expect(fmt.Sprintf("%s > td:nth-child(%d) > span", row, valColDelay))
		e.must(delay.ToHaveText(validatorsDelayText(*v.VoteDelayMs)))
		e.must(delay.ToHaveClass(validatorsDelayClass(*v.VoteDelayMs)))
		props := fmt.Sprintf("%d / %d", v.MissedProposals24h, v.ExpectedProposals24h)
		e.must(e.expect(fmt.Sprintf("%s > td:nth-child(%d)", row, valColProps)).ToHaveText(props))
		if _, rows := popRows(e, fmt.Sprintf("%s > td:nth-child(%d)", row, valColMissed)); !slices.Equal(rows, validatorsMissedRows(v.Missed100, v.OtherBlock100, v.Nil100)) {
			t.Errorf("%s: missed popover rows = %q, want those of /api/status", v.Name, rows)
		}
	}
}

// validatorsDelayText and validatorsDelayClass are how the page writes and
// colours a vote delay of ms.
func validatorsDelayText(ms int) string {
	s := strconv.FormatFloat(math.Abs(float64(ms))/1000, 'f', 2, 64)
	switch {
	case s == "0.00":
		return s + " s"
	case ms < 0:
		return "−" + s + " s"
	}
	return "+" + s + " s"
}

func validatorsDelayClass(ms int) string {
	switch a := max(ms, -ms); {
	case a <= 1000:
		return "ok"
	case a <= 2500:
		return "warn"
	}
	return "err"
}

// ---- Cards

// validatorsCardKeys are the labels of a validator card's values, in order.
var validatorsCardKeys = []string{"signing", "country", "provider", "vp", "spof", "miss 100", "delay", "miss 24h", "props 24h"}

// TestPageValidatorsCards checks a validator's card on a phone, per
// validator: its name links its page and marks the source's own, without a
// peer's dot, health or RPC; then, label beside value, its signing address
// shortened on a row of its own, its country, provider, voting power with
// its share, whether it is a single point of failure, its misses of the
// last 100 blocks, vote delay, misses of the last day and proposals,
// coloured as in the table, grey without a colour. The labels are upper
// case, with a colon, and the two value columns share the width evenly.
func TestPageValidatorsCards(t *testing.T) {
	specs := validatorsSortSpecs()
	e := openPage(t, phone, specNetwork(specs...))
	expectValidatorCards(e, "alpha", "bravo", "charlie", "echo")
	e.must(e.expect("#validators-cards .peer-card.no-rpc").ToHaveCount(4))
	e.must(e.expect("#validators-cards .peer-card-top > :not(.peer-card-name)").ToHaveCount(0))
	e.must(e.expect("#validators-cards .dot, #validators-cards .peer-card-footer").ToHaveCount(0))

	e.must(e.expect("#validators-cards .peer-card-k >> nth=0").ToHaveCSS("text-transform", "uppercase"))
	if got := pseudoContent(e, "#validators-cards .peer-card-k", "::after"); got != `":"` {
		t.Errorf("a card label ends with %s, want a colon", got)
	}
	cols := evalFloats(e, `() => getComputedStyle(document.querySelector('#validators-cards .peer-card-kv')).gridTemplateColumns.split(' ').map(parseFloat)`, nil)
	if len(cols) != 4 || math.Abs(cols[1]-cols[3]) > 1 {
		t.Errorf("a card's columns are %v px wide, want four, the two value columns even", cols)
	}

	for _, s := range specs {
		name, addr := s.val.name(), s.val.address()
		card := validatorEntryAt(phone, addr)
		e.must(e.expect(card + " .peer-card-k").ToHaveText(validatorsCardKeys))
		e.must(e.expect(card+" .peer-card-name a.val-link").ToHaveAttribute("href", "?val="+addr))
		value := func(key string) playwright.LocatorAssertions { return e.expect(validatorsCardValue(s.val, key)) }
		grey := func(key, text string) {
			t.Helper()
			e.must(value(key).ToHaveText(text))
			e.must(value(key).ToHaveCSS("color", fg2Color))
		}
		if name == "alpha" {
			e.must(e.expect(card + " .peer-card-name").ToHaveText("alpha (source)"))
		} else {
			e.must(e.expect(card + " .peer-card-name").ToHaveText(name))
		}

		grey("signing", shortAddr(addr))
		e.must(value("signing").ToHaveCSS("grid-column-start", "2"))
		e.must(value("signing").ToHaveCSS("grid-column-end", "-1"))
		grey("country", validatorsCountries[s.node.Country])
		grey("provider", s.node.Provider)
		// The powers sum to 100: each is its own share.
		grey("vp", fmt.Sprintf("%d (%d%%)", s.val.power, s.val.power))
		if name == "alpha" {
			e.must(value("spof").ToHaveText("yes"))
			e.must(value("spof").ToHaveClass("peer-card-v err"))
		} else {
			grey("spof", "no")
		}

		e.must(value("miss 100").ToHaveText(strconv.Itoa(s.signing.Missed)))
		band := "warn"
		if s.signing.Missed == 0 {
			band = "ok"
		}
		e.must(value("miss 100").ToHaveClass("peer-card-v " + band))
		title, rows := popRows(e, validatorsCardValue(s.val, "miss 100"))
		if want := validatorsMissedRows(s.signing.Missed, s.signing.OtherBlock, s.signing.Nil); title != "last 100 blocks" || !slices.Equal(rows, want) {
			t.Errorf("%s: miss 100 popover = %q %q, want %q %q", name, title, rows, "last 100 blocks", want)
		}

		delay := e.expect(validatorsCardValue(s.val, "delay") + " > span")
		e.must(delay.ToHaveText(validatorsDelayText(s.signing.VoteDelayMs)))
		e.must(delay.ToHaveClass(validatorsDelayClass(s.signing.VoteDelayMs)))

		e.must(value("miss 24h").ToHaveText(strconv.Itoa(s.absent)))
		e.must(value("miss 24h").ToHaveClass("peer-card-v " + validatorsMissed24hBand(s.absent)))

		e.must(value("props 24h").ToHaveText(fmt.Sprintf("%d / %d", s.missedProposals, s.proposals)))
		band = "err"
		if s.missedProposals == 0 {
			band = "ok"
		}
		e.must(e.expect(validatorsCardValue(s.val, "props 24h") + " > span > span").ToHaveClass(band))
	}
}

// validatorsCountries are the countries of the tests' validators, as a card
// writes them.
var validatorsCountries = map[string]string{"DE": "🇩🇪 Germany", "AU": "🇦🇺 Australia", "FR": "🇫🇷 France", "JP": "🇯🇵 Japan"}

// validatorsMissed24hBand is the colour of n blocks missed in a day: none
// ok, up to 5 a warning, more an error.
func validatorsMissed24hBand(n int) string {
	switch {
	case n == 0:
		return "ok"
	case n <= 5:
		return "warn"
	}
	return "err"
}

// TestPageValidatorsCardPopover checks that a tap on a card's misses of the
// last 100 blocks shows their split.
func TestPageValidatorsCardPopover(t *testing.T) {
	e := openPage(t, phone, specNetwork(validatorsSortSpecs()...))
	e.showPopover(e.Page.Locator(validatorsCardValue(valBravo, "miss 100")))
	e.must(e.expect(pagePopover + " .t").ToHaveText("last 100 blocks"))
	e.must(e.expect(pagePopover + " td.l").ToHaveText([]string{"missed", "absent", "other block", "nil votes"}))
	e.must(e.expect(pagePopover + " td.v").ToHaveText([]string{"5", "3", "2", "3 (counted as signed)"}))
}

// TestPageValidatorsCardCopy checks that a click on a validator's card
// copies the validator: its name, address, key and voting power, a line
// each; a click on its misses of the last 100 blocks shows their split, and
// one on its name opens its page, neither copying. On a phone the toast
// says so.
func TestPageValidatorsCardCopy(t *testing.T) {
	onProfiles(t, []profile{tablet, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, specNetwork(validatorsSortSpecs()...), withInstalledClock())
		e.runFor(time.Second)
		card := validatorEntryAt(phone, valBravo.address())
		e.must(e.expect(card).ToBeVisible())
		e.press(e.Page.Locator(card + " .peer-card-k").First())
		expectToast(e, "Copied!")
		if p == phone {
			return
		}
		want := strings.Join([]string{
			"bravo",
			"address: " + valBravo.address(),
			"pubkey: " + valBravo.pubKey().Bech32(),
			"vp: 30 (30%)",
		}, "\n")
		if got := e.clipboard(); got != want {
			t.Errorf("clipboard = %q, want %q", got, want)
		}
		fadeToast(e)

		// Another card's misses and name: a copy would take alpha's lines.
		e.must(e.Page.Locator(validatorsCardValue(valAlpha, "miss 100")).Click())
		e.must(e.expect(pagePopover + " .t").ToHaveText("last 100 blocks"))
		e.must(e.Page.Locator(validatorEntryAt(phone, valAlpha.address()) + " a.val-link").Click())
		e.must(e.expect("#val-modal").ToHaveClass(modalOpenClass))
		e.must(e.expect(".copy-toast").ToHaveAttribute("style", toastClear))
		if got := e.clipboard(); got != want {
			t.Errorf("after the clicks on alpha's misses and name: clipboard = %q, want bravo's, unchanged", got)
		}
	})
}

// TestPageValidatorsCompact checks the compact switches of the Validators
// and Peers cards: shown between 481 and 768 px, where a list shows as cards
// in two columns and, switched on, as its table, kept across loads; hidden
// from 480 px down, where the cards take one column, a phone's included, and
// on a desktop, which always shows the tables.
func TestPageValidatorsCompact(t *testing.T) {
	lists := []struct{ card, compact, label string }{
		{"#validators-card", "#validators-compact", "#validators-compact-label"},
		{"#peers-card", "#peers-compact", "#peers-compact-label"},
	}
	// shows checks that each list shows as cards or as its table.
	shows := func(e *pageEnv, table bool) {
		e.T.Helper()
		for _, l := range lists {
			cards, wrap := e.expect(l.card+" .node-cards"), e.expect(l.card+" .table-wrap")
			if table {
				e.must(wrap.ToBeVisible())
				e.must(cards.ToBeHidden())
			} else {
				e.must(cards.ToBeVisible())
				e.must(wrap.ToBeHidden())
			}
		}
	}
	t.Run("tablet", func(t *testing.T) {
		e := openPage(t, tablet, healthyNetwork())
		e.must(e.expect(validatorEntryAt(phone, valBravo.address())).ToHaveCount(1))
		for _, l := range lists {
			e.must(e.expect(l.label).ToBeVisible())
			e.must(e.expect(l.label).ToHaveText("compact"))
			e.must(e.expect(l.compact).Not().ToBeChecked())
		}
		shows(e, false)
		cols := evalFloats(e, `() => getComputedStyle(document.getElementById('validators-cards')).gridTemplateColumns.split(' ').map(parseFloat)`, nil)
		if len(cols) != 2 {
			t.Errorf("the validator cards sit in %d columns, want 2", len(cols))
		}

		for _, l := range lists {
			e.must(e.Page.Locator(l.compact).Check())
		}
		shows(e, true)
		e.must(e.expect("#validators-body tr").ToHaveCount(4))
		for _, key := range []string{"validators-compact", "peers-compact"} {
			expectStored(e, key, "true")
		}
		// The lists drawn again keep the tables.
		e.publish(nextBlock())
		e.must(e.expect("#ns-consensus").ToContainText(fmt.Sprintf("h=%d ", pageHeight+2)))
		shows(e, true)

		e.navigate("/")
		for _, l := range lists {
			e.must(e.expect(l.compact).ToBeChecked())
		}
		shows(e, true)
		for _, l := range lists {
			e.must(e.Page.Locator(l.compact).Uncheck())
		}
		shows(e, false)
		for _, key := range []string{"validators-compact", "peers-compact"} {
			expectStored(e, key, "false")
		}

		// The cards take one column from 480 px down.
		cards := e.expect("#validators-cards")
		e.must(e.Page.SetViewportSize(481, 1000))
		e.must(cards.ToHaveCSS("grid-template-columns", regexp.MustCompile(`^[\d.]+px [\d.]+px$`)))
		e.must(e.Page.SetViewportSize(480, 1000))
		e.must(cards.ToHaveCSS("grid-template-columns", regexp.MustCompile(`^[\d.]+px$`)))
	})
	onProfiles(t, []profile{phone, desktop}, func(t *testing.T, p profile) {
		for _, stored := range []string{"true", "false"} {
			t.Run(stored, func(t *testing.T) {
				e := openPage(t, p, healthyNetwork(), withStorage(map[string]string{"validators-compact": stored, "peers-compact": stored}))
				e.must(e.expect(validatorEntryAt(p, valBravo.address())).ToHaveCount(1))
				for _, l := range lists {
					e.must(e.expect(l.label).ToBeHidden())
				}
				shows(e, p == desktop)
				if p == phone {
					e.must(e.expect("#validators-cards").ToHaveCSS("grid-template-columns", regexp.MustCompile(`^[\d.]+px$`)))
				}
			})
		}
	})
}

// ---- Sorting

// validatorsSortBy clicks the Validators table's header of key, and checks
// the header then marks it, alone, by its arrow: ▲ ascending, ▼ descending.
func validatorsSortBy(e *pageEnv, key string, asc bool) {
	e.T.Helper()
	sortTable(e, "#validators-card", key, asc)
}

// validatorsSortAddresses are the names of specs in the order of their
// addresses.
func validatorsSortAddresses(specs []valSpec) []string {
	specs = slices.Clone(specs)
	slices.SortFunc(specs, func(a, b valSpec) int { return strings.Compare(a.val.address(), b.val.address()) })
	names := make([]string, len(specs))
	for i, s := range specs {
		names[i] = s.val.name()
	}
	return names
}

// TestPageValidatorsSort checks that a click on a header of the Validators
// table sorts its rows by that column, ascending, and a second click
// descending, its header marked; the Peers table keeps its own order. Rows
// start in name order. Rows alike in a column keep the set order; a
// validator without a vote delay, or expected to make no proposal, comes
// last either way. The Country column sorts by what it shows: its column,
// narrow on a page at most 1400 px wide, shows ISO codes.
func TestPageValidatorsSort(t *testing.T) {
	specs := validatorsSortSpecs()
	byAddress := validatorsSortAddresses(specs)
	byAddressDesc := slices.Clone(byAddress)
	slices.Reverse(byAddressDesc)
	t.Run("columns", func(t *testing.T) {
		e := openPage(t, desktop, specNetwork(specs...))
		validatorsExpectRows(e, "alpha", "bravo", "charlie", "echo")
		expectSorted(e, "#validators-card", "name", true)
		expectSorted(e, "#peers-card", "name", true)
		// The column shows ISO codes.
		e.must(e.expect(validatorsCell(valAlpha, valColCountry) + " .cc-name").ToBeHidden())
		for _, tc := range []struct {
			key       string
			asc, desc []string
		}{
			{"address", byAddress, byAddressDesc},
			{"country", []string{"bravo", "alpha", "charlie", "echo"}, []string{"echo", "charlie", "alpha", "bravo"}},
			{"provider", []string{"charlie", "alpha", "bravo", "echo"}, []string{"echo", "bravo", "alpha", "charlie"}},
			{"vp", []string{"echo", "charlie", "bravo", "alpha"}, []string{"alpha", "bravo", "charlie", "echo"}},
			// The three that are no single point of failure keep the set
			// order, both ways.
			{"spof", []string{"charlie", "echo", "bravo", "alpha"}, []string{"alpha", "charlie", "echo", "bravo"}},
			{"missed", []string{"alpha", "charlie", "echo", "bravo"}, []string{"bravo", "echo", "charlie", "alpha"}},
			{"missed24h", []string{"bravo", "echo", "alpha", "charlie"}, []string{"charlie", "alpha", "echo", "bravo"}},
			{"delay", []string{"charlie", "alpha", "echo", "bravo"}, []string{"bravo", "echo", "alpha", "charlie"}},
			{"props", []string{"echo", "bravo", "charlie", "alpha"}, []string{"alpha", "charlie", "bravo", "echo"}},
			{"name", []string{"alpha", "bravo", "charlie", "echo"}, []string{"echo", "charlie", "bravo", "alpha"}},
		} {
			validatorsSortBy(e, tc.key, true)
			validatorsExpectRows(e, tc.asc...)
			validatorsSortBy(e, tc.key, false)
			validatorsExpectRows(e, tc.desc...)
			expectSorted(e, "#peers-card", "name", true)
		}
	})
	t.Run("gaps", func(t *testing.T) {
		e := openPage(t, desktop, specNetwork(validatorsGapsSpecs()...))
		validatorsExpectRows(e, "alpha", "bravo", "charlie", "echo")
		validatorsSortBy(e, "delay", true)
		validatorsExpectRows(e, "alpha", "echo", "bravo", "charlie")
		validatorsSortBy(e, "delay", false)
		validatorsExpectRows(e, "bravo", "echo", "alpha", "charlie")
		validatorsSortBy(e, "props", true)
		validatorsExpectRows(e, "bravo", "charlie", "alpha", "echo")
		validatorsSortBy(e, "props", false)
		validatorsExpectRows(e, "alpha", "charlie", "bravo", "echo")
	})
}

// validatorsGapsSpecs are the sort network with gaps: bravo has no node, so
// no country or provider; charlie cast no vote in the live window, so it
// has no vote delay; echo was expected to make no proposal.
func validatorsGapsSpecs() []valSpec {
	specs := validatorsSortSpecs()
	for i := range specs {
		switch specs[i].val.name() {
		case "bravo":
			specs[i].node = nil
		case "charlie":
			specs[i].signing.Votes = 0
		case "echo":
			specs[i].proposals, specs[i].missedProposals = 0, 0
		}
	}
	return specs
}

// TestPageValidatorsCardsFollowTheSort checks that the validator cards
// follow the table's order: by name at first, then by the column the table
// was sorted by, shown as its table.
func TestPageValidatorsCardsFollowTheSort(t *testing.T) {
	t.Run("tablet", func(t *testing.T) {
		e := openPage(t, tablet, specNetwork(validatorsSortSpecs()...))
		expectValidatorCards(e, "alpha", "bravo", "charlie", "echo")
		e.must(e.Page.Locator("#validators-compact").Check())
		validatorsSortBy(e, "vp", true)
		e.must(e.Page.Locator("#validators-compact").Uncheck())
		e.must(e.expect("#validators-cards").ToBeVisible())
		expectValidatorCards(e, "echo", "charlie", "bravo", "alpha")
	})
	t.Run("phone", func(t *testing.T) {
		e := openPage(t, phone, specNetwork(validatorsSortSpecs()...))
		expectValidatorCards(e, "alpha", "bravo", "charlie", "echo")
	})
}

// ---- Cells from the validator's node

// validatorsCellSpecs are five validators whose nodes tell the country and
// provider cells' cases apart: alpha's node is located and hosted, with an
// IP; bravo's has a region code that names no country, and a provider but
// no IP; charlie's has an IP alone; echo's only advertises an address; foxtrot
// has no node.
func validatorsCellSpecs() []valSpec {
	echo := validatorNode(validatorsEcho, "", "", "")
	echo.ExternalAddress = "198.51.100.7"
	return []valSpec{
		{val: valAlpha, node: validatorNode(valAlpha, "203.0.113.10", "DE", node.Provider("Hetzner Online GmbH"))},
		{val: valBravo, node: validatorNode(valBravo, "", "150", "OVH")},
		{val: valCharlie, node: validatorNode(valCharlie, "203.0.113.12", "", "")},
		{val: validatorsEcho, node: echo},
		{val: validatorsFoxtrot},
	}
}

// TestPageValidatorsCountryCell checks the Country cell: the country of the
// validator's node, flag and name, or its ISO code in a narrow column, the
// name in a popover and copied, and linked to the map; a code that names no
// country shows as is, without a flag; without a country, a dash. A wide column, as the Peers
// table's, shows the name.
func TestPageValidatorsCountryCell(t *testing.T) {
	e := openPage(t, desktop, specNetwork(validatorsCellSpecs()...))
	de := validatorsCell(valAlpha, valColCountry)
	l := e.expect(de)
	e.must(l.ToHaveAttribute("data-cc", "DE"))
	e.must(l.ToHaveAttribute("data-copy", "Germany"))
	e.must(l.ToHaveAttribute("data-popover", "Germany"))
	e.must(l.ToHaveClass("country-cell tap-popover"))
	e.must(l.Not().ToHaveAttribute("title", regexp.MustCompile(`.*`)))
	e.must(e.expect(de + " > .cc-inner > span").ToHaveText([]string{"🇩🇪", "Germany", "DE"}))
	e.must(e.expect(de + " .cc-flag").ToHaveClass("cc-flag"))
	e.must(e.expect(de + " .cc-name").ToBeHidden())
	e.must(e.expect(de + " .cc-code").ToBeVisible())
	e.showPopover(e.Page.Locator(de))
	e.must(e.expect(pagePopover).ToHaveText("Germany"))

	region := validatorsCell(valBravo, valColCountry)
	e.must(e.expect(region).ToHaveAttribute("data-cc", "150"))
	e.must(e.expect(region + " > .cc-inner > span").ToHaveText([]string{"", "150", "150"}))

	for _, v := range []pageValidator{valCharlie, validatorsEcho, validatorsFoxtrot} {
		cell := e.expect(validatorsCell(v, valColCountry))
		e.must(cell.ToHaveText("—"))
		e.must(cell.ToHaveClass("country-cell"))
		e.must(cell.Not().ToHaveAttribute("data-cc", regexp.MustCompile(`.*`)))
	}

	peers := euNodeCountry
	e.must(e.expect(peers + " .cc-name").ToBeVisible())
	e.must(e.expect(peers + " .cc-code").ToBeHidden())
}

// TestPageValidatorsProviderCell checks the Provider cell: the provider of
// the validator's node, small and grey, copied and linked to the map, with
// an ⓘ giving the IP it was guessed from: the node's IP, else the address it
// advertises; with an IP alone, "unknown", the IP copied; with neither, a
// dash.
func TestPageValidatorsProviderCell(t *testing.T) {
	e := openPage(t, desktop, specNetwork(validatorsCellSpecs()...))
	hetzner := node.Provider("Hetzner Online GmbH")
	for _, tc := range []struct {
		val                    pageValidator
		text, copied, prov, ip string
	}{
		{valAlpha, hetzner + " ⓘ", hetzner, hetzner, "203.0.113.10"},
		{valBravo, "OVH", "OVH", "OVH", ""},
		{valCharlie, "unknown ⓘ", "203.0.113.12", "", "203.0.113.12"},
		{validatorsEcho, "unknown ⓘ", "198.51.100.7", "", "198.51.100.7"},
	} {
		cell := validatorsCell(tc.val, valColProvider)
		l := e.expect(cell)
		e.must(l.ToHaveText(tc.text))
		e.must(l.ToHaveAttribute("data-copy", tc.copied))
		e.must(l.ToHaveCSS("font-size", "11px"))
		e.must(l.ToHaveCSS("color", fg2Color))
		if tc.prov != "" {
			e.must(l.ToHaveAttribute("data-prov", tc.prov))
		} else {
			e.must(l.Not().ToHaveAttribute("data-prov", regexp.MustCompile(`.*`)))
		}
		if tc.ip == "" {
			e.must(e.expect(cell + " .info-btn").ToHaveCount(0))
			continue
		}
		if title, rows := popRows(e, cell+" .info-btn"); title != "" || !slices.Equal(rows, [][2]string{{"guessed from IP", tc.ip}}) {
			t.Errorf("%s: provider popover = %q %q, want no title and the IP %s", tc.val.name(), title, rows, tc.ip)
		}
	}
	e.must(e.expect(validatorsCell(validatorsFoxtrot, valColProvider)).ToHaveText("—"))
	e.must(e.expect(validatorsCell(validatorsFoxtrot, valColProvider)).Not().ToHaveAttribute("data-copy", regexp.MustCompile(`.*`)))
}

// TestPageValidatorsCountryHighlight checks that the pointer on a
// validator's country or provider highlights every element of that country
// or provider: on the map, in its list and in both tables; leaving clears
// it.
func TestPageValidatorsCountryHighlight(t *testing.T) {
	e := openPage(t, desktop, specNetwork(validatorsCellSpecs()...))
	away := e.Page.Locator("#validators-header")
	e.must(e.Page.Locator(validatorsCell(valAlpha, valColCountry)).Hover())
	expectMapHighlighted(e, "data-cc", "DE")
	expectMapDimmed(e, true)
	e.must(e.expect(`#map-points .cc-hl[data-cc="DE"]`).Not().ToHaveCount(0))
	e.must(e.expect(`#map-country-list .country-row.cc-hl[data-cc="DE"]`).ToHaveCount(1))
	e.must(e.expect(euNodeCountry).ToHaveClass(regexp.MustCompile(`\bcc-hl\b`)))
	e.must(e.expect(euNodeCountry).ToHaveCSS("background-color", validatorsHighlightSource))
	e.must(away.Hover())
	e.must(e.expect(".cc-hl").ToHaveCount(0))
	expectMapDimmed(e, false)

	hetzner := node.Provider("Hetzner Online GmbH")
	e.must(e.Page.Locator(validatorsCell(valAlpha, valColProvider)).Hover())
	expectMapHighlighted(e, "data-prov", hetzner)
	e.must(e.expect(peerEntry(desktop, "eu-node") + " td.cc-hl[data-prov]").ToHaveCount(1))
	e.must(away.Hover())
	e.must(e.expect(".cc-hl").ToHaveCount(0))
}

// ---- Cross-highlight

// validatorsHighlightNetwork is the healthy network in which bravo's node,
// renamed bravo-node, reports bravo's address, and a full node also named
// bravo, which reports none, is listed among the peers.
func validatorsHighlightNetwork() *scenario {
	sc := healthyNetwork()
	i := slices.IndexFunc(sc.snap.Peers, func(p node.Peer) bool { return p.Moniker == "bravo" })
	namesake := fullPeer("bravo")
	sc.snap.Peers[i].Moniker = "bravo-node"
	sc.snap.Peers = append(sc.snap.Peers, namesake)
	slices.SortStableFunc(sc.snap.Peers, func(a, b node.Peer) int { return cmp.Compare(a.Moniker, b.Moniker) })
	return sc
}

// validatorsHighlighted checks the highlight classes of the element sel
// matches.
func validatorsHighlighted(e *pageEnv, sel, class string) {
	e.T.Helper()
	e.must(e.expect(sel).ToHaveClass(regexp.MustCompile(`\b` + class + `\b`)))
}

// TestPageValidatorsHighlight checks the highlight of a validator: the
// pointer on its name or address marks its row and those cells, and
// highlights everything else of that name, the proposer and the peer named
// so; the pointer on the proposer highlights the validator's row and the
// peer. Leaving clears it all.
func TestPageValidatorsHighlight(t *testing.T) {
	onProfiles(t, []profile{desktop, desktopWebKit}, func(t *testing.T, p profile) {
		e := openPage(t, p, validatorsHighlightNetwork())
		row := validatorEntryAt(desktop, valBravo.address())
		peer := peerEntry(desktop, "bravo")
		proposer := `#ns-proposer [data-highlight="bravo"]`
		away := e.Page.Locator("#validators-header")
		e.must(e.expect(peer).ToHaveCount(1))
		for _, cell := range []string{validatorsCell(valBravo, valColName), validatorsCell(valBravo, valColAddress)} {
			e.must(e.Page.Locator(cell).Hover())
			validatorsHighlighted(e, row, "hl-source")
			for _, col := range []int{valColName, valColAddress} {
				validatorsHighlighted(e, validatorsCell(valBravo, col), "hl-source")
			}
			e.must(e.expect(validatorsCell(valBravo, valColName)).ToHaveCSS("background-color", validatorsHighlightSource))
			validatorsHighlighted(e, proposer, "hl")
			e.must(e.expect(proposer).ToHaveCSS("background-color", validatorsSpanHighlight))
			e.must(e.expect(proposer).ToHaveCSS("color", blueColor))
			validatorsHighlighted(e, peer, "hl")
			e.must(e.expect(peer + " td.hl[data-highlight]").ToHaveCount(2))
			e.must(e.expect(peer+" td:first-child").ToHaveCSS("background-color", validatorsHighlight))
			e.must(e.expect(".hl, .hl-source").ToHaveCount(7))
			e.must(away.Hover())
			e.must(e.expect(".hl, .hl-source").ToHaveCount(0))
		}

		e.must(e.Page.Locator(proposer).Hover())
		validatorsHighlighted(e, proposer, "hl-source")
		e.must(e.expect(proposer).ToHaveCSS("background-color", validatorsSpanSource))
		e.must(e.expect(proposer).ToHaveCSS("color", validatorsWhite))
		validatorsHighlighted(e, row, "hl")
		e.must(e.expect(row + " td.hl").ToHaveCount(2))
		validatorsHighlighted(e, peer, "hl")
		e.must(e.expect(".hl, .hl-source").ToHaveCount(7))
		e.must(away.Hover())
		e.must(e.expect(".hl, .hl-source").ToHaveCount(0))
	})
}

// ---- Markup

// TestPageValidatorsMarkupStaysText checks that a validator's name and its
// node's provider carrying markup show as text, in the table and on the
// cards: nothing of it becomes an element.
func TestPageValidatorsMarkupStaysText(t *testing.T) {
	const name, provider = `<i>mal</i><img src="/x">`, `<b>ISP</b><img src="/y">`
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		mal := newPageValidator("mal", name, 10)
		e := openPage(t, p, specNetwork(
			valSpec{val: valAlpha},
			valSpec{val: mal, node: validatorNode(mal, "203.0.113.20", "DE", provider)},
		))
		if p == phone {
			card := validatorEntryAt(phone, mal.address())
			e.must(e.expect(card + " .peer-card-name").ToHaveText(name))
			e.must(e.expect(validatorsCardValue(mal, "provider")).ToHaveText(provider))
		} else {
			e.must(e.expect(validatorsCell(mal, valColName) + " a.val-link").ToHaveText(name))
			e.must(e.expect(validatorsCell(mal, valColName)).ToHaveAttribute("data-highlight", name))
			e.must(e.expect(validatorsCell(mal, valColProvider)).ToHaveText(provider + " ⓘ"))
			e.must(e.expect(validatorsCell(mal, valColProvider)).ToHaveAttribute("data-prov", provider))
		}
		e.must(e.expect("#validators-card i, #validators-card b, #validators-card img").ToHaveCount(0))
	})
}

// ---- Bands

// validatorsBandSpecs are three validators of equal power, each a single
// point of failure: alpha signs everything; bravo missed 3 of the last 100
// and 5 blocks of the last day, and cast no vote in the window, so it has no
// vote delay; charlie missed the last 10 blocks in a row, which flags it
// down, and 6 of the last day, and was expected to make no proposal.
func validatorsBandSpecs() []valSpec {
	bravo := validatorsSigning(3, 0)
	bravo.Votes = 0
	charlie := validatorsSigning(10, 40)
	charlie.MissedInARow, charlie.SignedInARow = 10, 0
	return []valSpec{
		{val: valAlpha, signing: validatorsSigning(0, 12), proposals: 3},
		{val: valBravo, signing: bravo, absent: 5, proposals: 2},
		{val: valCharlie, signing: charlie, absent: 6},
	}
}

// TestPageValidatorsBands checks the colours of a validator's values at
// their thresholds, in the table and on the cards: no miss of the last 100
// is ok, one a warning, a validator flagged down an error; no miss of the
// last day is ok, up to 5 a warning, more an error; a validator holding a
// third of the power is a single point of failure. Without a vote delay, a
// dash; without an expected proposal, no colour; a card without the
// validator's node, a dash for its country and provider.
func TestPageValidatorsBands(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := specNetwork(validatorsBandSpecs()...)
		// The push manager flags a validator down.
		sc.push = true
		e := openPage(t, p, sc)
		for _, tc := range []struct {
			val                                                        pageValidator
			missed, missedBand, missed24h, missed24hBand, delay, props string
		}{
			{valAlpha, "0", "ok", "0", "ok", "+0.01 s", "0 / 3"},
			{valBravo, "3", "warn", "5", "warn", "-", "0 / 2"},
			{valCharlie, "10", "err", "6", "err", "+0.04 s", "0 / 0"},
		} {
			if p == desktop {
				cell := func(col int) playwright.LocatorAssertions { return e.expect(validatorsCell(tc.val, col)) }
				e.must(cell(valColSPOF).ToHaveText("yes"))
				e.must(cell(valColSPOF).ToHaveClass("err"))
				e.must(cell(valColSPOF).ToHaveCSS("color", validatorsRed))
				e.must(cell(valColMissed).ToHaveText(tc.missed))
				e.must(cell(valColMissed).ToHaveClass(tc.missedBand))
				e.must(cell(valColMissed24h).ToHaveText(tc.missed24h))
				e.must(cell(valColMissed24h).ToHaveClass(tc.missed24hBand + " tap-popover"))
				e.must(cell(valColDelay).ToHaveText(tc.delay))
				e.must(cell(valColProps).ToHaveText(tc.props))
				continue
			}
			value := func(key string) playwright.LocatorAssertions { return e.expect(validatorsCardValue(tc.val, key)) }
			// No validator here has a node among the peers.
			e.must(value("country").ToHaveText("-"))
			e.must(value("provider").ToHaveText("-"))
			e.must(value("spof").ToHaveText("yes"))
			e.must(value("spof").ToHaveClass("peer-card-v err"))
			e.must(value("miss 100").ToHaveText(tc.missed))
			e.must(value("miss 100").ToHaveClass("peer-card-v " + tc.missedBand))
			e.must(value("miss 24h").ToHaveText(tc.missed24h))
			e.must(value("miss 24h").ToHaveClass("peer-card-v " + tc.missed24hBand))
			e.must(value("delay").ToHaveText(tc.delay))
			e.must(value("props 24h").ToHaveText(tc.props))
		}
		props := validatorsCardValue(valCharlie, "props 24h") + " > span > span"
		if p == desktop {
			props = validatorsCell(valCharlie, valColProps) + " > span > span"
		}
		e.must(e.expect(props).Not().ToHaveAttribute("class", regexp.MustCompile(`.*`)))
		e.must(e.expect(props).ToHaveCSS("color", fgColor))
	})
}

// ---- Empty lists

// TestPageValidatorsEmpty checks the lists while every source is down, with
// neither validators nor peers: each table says so in a row spanning all
// its columns; on a phone or a tablet, each card list says so, in grey,
// across its columns.
func TestPageValidatorsEmpty(t *testing.T) {
	onProfiles(t, []profile{desktop, tablet, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, &scenario{now: pageNow, snap: sourcesDown()})
		for _, tc := range []struct {
			body, cards, text string
			columns           int
		}{
			{"#validators-body", "#validators-cards", "no validators", 10},
			{"#peers-body", "#peers-cards", "no peers", 7},
		} {
			if p != desktop {
				e.must(e.expect(tc.cards).ToHaveText(tc.text))
				e.must(e.expect(tc.cards+" > *").ToHaveCSS("color", fg2Color))
				cards, msg := boxOf(e, tc.cards), boxOf(e, tc.cards+" > *")
				if math.Abs(msg.Width-cards.Width) > 1 {
					t.Errorf("%s: the message is %.1f px wide, want the list's %.1f", tc.cards, msg.Width, cards.Width)
				}
				continue
			}
			e.must(e.expect(tc.body + " tr").ToHaveCount(1))
			e.must(e.expect(tc.body).ToHaveText(tc.text))
			e.must(e.expect(tc.body + " td:first-child").ToHaveClass("full-role"))
			spanned := evalInt(e, fmt.Sprintf(`() => [...document.querySelector('%s tr').cells].reduce((n, c) => n + c.colSpan, 0)`, tc.body))
			if spanned != tc.columns {
				t.Errorf("the %s row spans %d columns, want %d", tc.body, spanned, tc.columns)
			}
		}
	})
}

// TestPageValidatorsHighlightInsideTheCell checks that a highlight lasts
// while the pointer is anywhere on the cell or list row it started on: moved
// from a part of it, the name's link, the address's start, the country's
// code or name, onto the cell's or row's own space, the pointer keeps it;
// moved off, it clears it.
func TestPageValidatorsHighlightInsideTheCell(t *testing.T) {
	onProfiles(t, []profile{desktop, desktopWebKit}, func(t *testing.T, p profile) {
		e := openPage(t, p, specNetwork(validatorsCellSpecs()...))
		for _, tc := range []struct {
			cell, part, highlighted, class string
		}{
			{validatorsCell(valAlpha, valColName), " a.val-link", validatorEntryAt(desktop, valAlpha.address()), "hl-source"},
			{validatorsCell(valAlpha, valColAddress), " .mt-start", validatorEntryAt(desktop, valAlpha.address()), "hl-source"},
			{validatorsCell(valAlpha, valColCountry), " .cc-code", euNodeCountry, "cc-hl"},
			{`#map-country-list .country-row[data-cc="DE"]`, " .country-name", euNodeCountry, "cc-hl"},
		} {
			e.must(e.Page.Locator(tc.cell).ScrollIntoViewIfNeeded())
			e.must(e.expect(tc.cell).ToBeInViewport())
			part := boxOf(e, tc.cell+tc.part)
			e.must(e.Page.Mouse().Move(part.X+part.Width/2, part.Y+part.Height/2))
			validatorsHighlighted(e, tc.highlighted, tc.class)
			// The cell's own space: its padding, left of its content.
			cell := boxOf(e, tc.cell)
			e.must(e.Page.Mouse().Move(cell.X+2, part.Y+part.Height/2))
			validatorsHighlighted(e, tc.highlighted, tc.class)
			e.must(e.Page.Locator("#validators-header").Hover())
			e.must(e.expect(".hl, .hl-source, .cc-hl").ToHaveCount(0))
		}
	})
}

// ---- Validators' nodes

// TestPageValidatorsKeepOtherPeers checks that a validator's row and card
// show its own node, the peer reporting its address, and that this peer
// alone leaves the Peers list: a full node named like a validator, listed
// after its node, and nameless peers beside a validator's nameless node,
// all stay listed.
func TestPageValidatorsKeepOtherPeers(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		charlie := validatorNode(valCharlie, "203.0.113.12", "JP", "")
		charlie.Moniker = ""
		sc := specNetwork(
			valSpec{val: valAlpha},
			valSpec{val: valBravo, node: validatorNode(valBravo, "203.0.113.11", "DE", "")},
			valSpec{val: valCharlie, node: charlie},
		)
		namesake := fullPeer("bravo")
		namesake.Country = "FR"
		nameless := []node.Peer{fullPeer(""), fullPeer("")}
		for i := range nameless {
			nameless[i].NodeID = nodeID(fmt.Sprintf("nameless-%d", i))
			nameless[i].RemoteIP = fmt.Sprintf("203.0.113.3%d", i)
		}
		// One order the server can send: by moniker, the namesake after
		// bravo's node.
		sc.snap.Peers = append(append(sc.snap.Peers, namesake), nameless...)
		slices.SortStableFunc(sc.snap.Peers, func(a, b node.Peer) int { return cmp.Compare(a.Moniker, b.Moniker) })
		e := openPage(t, p, sc)

		// The four healthy peers, the namesake and the two nameless.
		e.must(e.expect("#peers-header").ToHaveText("Peers (7)"))
		if p == phone {
			e.must(e.expect("#peers-cards .peer-card").ToHaveCount(7))
			e.must(e.expect(validatorsCardValue(valBravo, "country")).ToHaveText("🇩🇪 Germany"))
			e.must(e.expect(validatorsCardValue(valCharlie, "country")).ToHaveText("🇯🇵 Japan"))
			e.must(e.expect(peerEntry(phone, "bravo") + ` .peer-card-k:text-is("country") + .peer-card-v`).ToHaveText("🇫🇷 France"))
			return
		}
		e.must(e.expect("#peers-body tr").ToHaveCount(7))
		e.must(e.expect(validatorsCell(valBravo, valColCountry)).ToHaveAttribute("data-cc", "DE"))
		e.must(e.expect(validatorsCell(valCharlie, valColCountry)).ToHaveAttribute("data-cc", "JP"))
		e.must(e.expect(peerEntry(desktop, "bravo")+" td.country-cell").ToHaveAttribute("data-cc", "FR"))
		for _, n := range nameless {
			e.must(e.expect(fmt.Sprintf(`#peers-body tr:has(td[data-copy=%q])`, n.NodeID)).ToHaveCount(1))
		}
	})
}

// ---- Voting power

// TestPageValidatorsUnreadablePower checks a voting power the page cannot
// know, one that reads as no number or none at all: the total is unknown
// then. That validator's row and card show "-" and it sorts last by voting
// power, either way; the others show their power with no share, every
// validator's SPOF reads a grey "-", unknown, and the Network State's votes
// and active power read "-". Nothing shows NaN.
func TestPageValidatorsUnreadablePower(t *testing.T) {
	for _, tc := range []struct {
		name string
		// unknown leaves one validator's voting power unknown: the one
		// whose power vps gives as "-".
		unknown   func(*node.Snapshot)
		vps       []string // alpha's, bravo's, charlie's, echo's
		asc, desc []string
	}{
		{
			name:    "reads as no number",
			unknown: func(snap *node.Snapshot) { snap.Validators[1].VotingPower = "lots" },
			vps:     []string{"30", "-", "10", "10"},
			asc:     []string{"charlie", "echo", "alpha", "bravo"},
			desc:    []string{"alpha", "charlie", "echo", "bravo"},
		},
		{
			// echo is out of the set the source read.
			name:    "none",
			unknown: func(snap *node.Snapshot) { snap.Validators = snap.Validators[:3] },
			vps:     []string{"30", "10", "10", "-"},
			asc:     []string{"bravo", "charlie", "alpha", "echo"},
			desc:    []string{"alpha", "bravo", "charlie", "echo"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				vals := []pageValidator{validatorsPowered(valAlpha, 30), valBravo, valCharlie, validatorsEcho}
				sc := specNetwork(
					valSpec{val: vals[0]}, valSpec{val: vals[1]},
					valSpec{val: vals[2]}, valSpec{val: vals[3]},
				)
				tc.unknown(sc.snap)
				e := openPage(t, p, sc)
				e.must(e.expect("#ns-votes").ToHaveText("-"))
				e.must(e.expect("#ns-active-vp").ToHaveText("-"))
				if p == phone {
					for i, v := range vals {
						e.must(e.expect(validatorsCardValue(v, "vp")).ToHaveText(tc.vps[i]))
						e.must(e.expect(validatorsCardValue(v, "spof")).ToHaveText("-"))
						e.must(e.expect(validatorsCardValue(v, "spof")).ToHaveCSS("color", fg2Color))
					}
					e.must(e.expect("#validators-cards").Not().ToContainText("NaN"))
					e.must(e.expect("#validators-cards").Not().ToContainText("%"))
					return
				}
				for i, v := range vals {
					e.must(e.expect(validatorsCell(v, valColVP)).ToHaveText(tc.vps[i]))
					e.must(e.expect(validatorsCell(v, valColSPOF)).ToHaveText("-"))
					e.must(e.expect(validatorsCell(v, valColSPOF)).ToHaveCSS("color", fg2Color))
					if tc.vps[i] == "-" {
						e.must(e.expect(validatorsCell(v, valColVP)).Not().ToHaveAttribute("data-copy", regexp.MustCompile(`.*`)))
					}
				}
				e.must(e.expect("#validators-body").Not().ToContainText("NaN"))
				e.must(e.expect("#validators-body").Not().ToContainText("%"))
				validatorsSortBy(e, "vp", true)
				validatorsExpectRows(e, tc.asc...)
				validatorsSortBy(e, "vp", false)
				validatorsExpectRows(e, tc.desc...)
			})
		})
	}
}

// TestPageValidatorsSortMissingLast checks that a value a column shows as
// missing sorts last, both ways: a validator without a node, so without a
// country or provider; in the Peers table, whose Country column shows
// countries by name and sorts by them, a peer without a country or provider.
func TestPageValidatorsSortMissingLast(t *testing.T) {
	sc := specNetwork(validatorsGapsSpecs()...)
	// By name Germany, Switzerland, Vietnam; by code CH, DE, VN.
	for i, p := range sc.snap.Peers {
		switch p.Moniker {
		case "us-node":
			sc.snap.Peers[i].Country = "VN"
		case "full-norpc":
			sc.snap.Peers[i].Country = "CH"
		}
	}
	e := openPage(t, desktop, sc)
	validatorsSortBy(e, "country", true)
	validatorsExpectRows(e, "alpha", "charlie", "echo", "bravo")
	validatorsSortBy(e, "country", false)
	validatorsExpectRows(e, "echo", "charlie", "alpha", "bravo")
	validatorsSortBy(e, "provider", true)
	validatorsExpectRows(e, "charlie", "alpha", "echo", "bravo")
	validatorsSortBy(e, "provider", false)
	validatorsExpectRows(e, "echo", "alpha", "charlie", "bravo")

	e.must(e.expect(euNodeCountry + " .cc-name").ToBeVisible())
	sortTable(e, "#peers-card", "country", true)
	e.must(e.expect(peerNameCells).ToHaveText([]string{"eu-node", "full-norpc", "us-node", "sentry-1 (source)"}))
	sortTable(e, "#peers-card", "country", false)
	e.must(e.expect(peerNameCells).ToHaveText([]string{"us-node", "full-norpc", "eu-node", "sentry-1 (source)"}))
	sortTable(e, "#peers-card", "provider", true)
	e.must(e.expect(peerNameCells).ToHaveText([]string{"us-node", "eu-node", "full-norpc", "sentry-1 (source)"}))
	sortTable(e, "#peers-card", "provider", false)
	e.must(e.expect(peerNameCells).ToHaveText([]string{"eu-node", "us-node", "full-norpc", "sentry-1 (source)"}))
}

// TestPageValidatorsNameOrder checks that names sort in English
// alphabetical order, upper and lower case alike, in both tables, both ways,
// and on the cards.
func TestPageValidatorsNameOrder(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		bravo := valBravo
		bravo.moniker = "Bravo"
		sc := specNetwork(valSpec{val: valCharlie}, valSpec{val: bravo}, valSpec{val: valAlpha})
		us := slices.IndexFunc(sc.snap.Peers, func(p node.Peer) bool { return p.Moniker == "us-node" })
		sc.snap.Peers[us].Moniker = "Us-node"
		e := openPage(t, p, sc)
		peers := []string{"eu-node", "full-norpc", "sentry-1 (source)", "Us-node"}
		if p == phone {
			expectValidatorCards(e, "alpha", "Bravo", "charlie")
			e.must(e.expect("#peers-cards .peer-card-name").ToHaveText(peers))
			return
		}
		validatorsExpectRows(e, "alpha", "Bravo", "charlie")
		e.must(e.expect(peerNameCells).ToHaveText(peers))
		validatorsSortBy(e, "name", false)
		validatorsExpectRows(e, "charlie", "Bravo", "alpha")
		sortTable(e, "#peers-card", "name", false)
		slices.Reverse(peers)
		e.must(e.expect(peerNameCells).ToHaveText(peers))
	})
}

// TestPageValidatorsProviderNameOrder checks that providers sort in English
// alphabetical order, upper and lower case alike, in both tables, both
// ways: netcup GmbH between Hetzner and Vultr.
func TestPageValidatorsProviderNameOrder(t *testing.T) {
	sc := specNetwork(
		valSpec{val: valAlpha, node: validatorNode(valAlpha, "203.0.113.10", "DE", "Vultr")},
		valSpec{val: valBravo, node: validatorNode(valBravo, "203.0.113.11", "DE", "netcup GmbH")},
		valSpec{val: valCharlie, node: validatorNode(valCharlie, "203.0.113.12", "DE", "Hetzner")},
	)
	for i, p := range sc.snap.Peers {
		switch p.Moniker {
		case "full-norpc":
			sc.snap.Peers[i].Provider = "netcup GmbH"
		case "sentry-1":
			sc.snap.Peers[i].Provider = "Vultr"
		}
	}
	e := openPage(t, desktop, sc)
	validatorsSortBy(e, "provider", true)
	validatorsExpectRows(e, "charlie", "bravo", "alpha")
	validatorsSortBy(e, "provider", false)
	validatorsExpectRows(e, "alpha", "bravo", "charlie")

	// us-node is hosted by AWS, eu-node by Hetzner.
	peers := []string{"us-node", "eu-node", "full-norpc", "sentry-1 (source)"}
	sortTable(e, "#peers-card", "provider", true)
	e.must(e.expect(peerNameCells).ToHaveText(peers))
	sortTable(e, "#peers-card", "provider", false)
	slices.Reverse(peers)
	e.must(e.expect(peerNameCells).ToHaveText(peers))
}

// TestPageValidatorsNamesInEnglishOrder checks that names sort in English
// order whatever the browser's language: in a Swedish browser, which puts Ä
// and Ö after Z, the Validators and Peers tables and the peers' own order
// (the Peers table sorted by a column that ties them all) still sort them
// with A and O.
func TestPageValidatorsNamesInEnglishOrder(t *testing.T) {
	arla := valBravo
	arla.moniker = "Ärla"
	sc := specNetwork(valSpec{val: valAlpha}, valSpec{val: arla}, valSpec{val: valCharlie})
	us := slices.IndexFunc(sc.snap.Peers, func(p node.Peer) bool { return p.Moniker == "us-node" })
	sc.snap.Peers[us].Moniker = "Öland-node"
	e := openPage(t, desktop, sc, withLocale("sv-SE"))
	validatorsExpectRows(e, "alpha", "Ärla", "charlie")
	english := []string{"eu-node", "full-norpc", "Öland-node", "sentry-1 (source)"}
	e.must(e.expect(peerNameCells).ToHaveText(english))
	// The presence store is off, so no peer has a share of the polls: they
	// keep their own order.
	sortTable(e, "#peers-card", "online", true)
	e.must(e.expect(peerNameCells).ToHaveText(english))
}

// TestPageValidatorsSortBeforeData checks the sort marks before any data:
// both tables mark their name column from the start, and a click on a
// header marks it at once; the data, once it comes, is in that order.
func TestPageValidatorsSortBeforeData(t *testing.T) {
	sc := healthyNetwork()
	sc.snap = nil
	e := openPage(t, desktop, sc)
	expectSorted(e, "#validators-card", "name", true)
	expectSorted(e, "#peers-card", "name", true)
	validatorsSortBy(e, "vp", true)
	validatorsSortBy(e, "vp", false)
	validatorsSortBy(e, "vp", true)
	e.must(e.expect("#validators-body").ToHaveText("loading..."))

	// By voting power, ascending: delta, which holds the least, first.
	e.publish(healthySnapshot())
	validatorsExpectRows(e, shortAddr(valDelta.address()), "alpha", "bravo", "charlie")
	expectSorted(e, "#validators-card", "vp", true)
	expectSorted(e, "#peers-card", "name", true)
}

// validatorsExpectAriaSort checks that the header of key, alone in the
// table of card, tells assistive technology the table's order.
func validatorsExpectAriaSort(e *pageEnv, card, key, order string) {
	e.T.Helper()
	e.must(e.expect(card + " th[aria-sort]").ToHaveCount(1))
	e.must(e.expect(fmt.Sprintf(`%s th[data-sort=%q]`, card, key)).ToHaveAttribute("aria-sort", order))
}

// TestPageValidatorsSortFromKeyboard checks that every sortable header is
// a button, which sorts its table on Enter and on Space; the sorted header
// tells assistive technology the order.
func TestPageValidatorsSortFromKeyboard(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	delta := shortAddr(valDelta.address())
	validatorsExpectRows(e, "alpha", "bravo", "charlie", delta)
	validatorsExpectAriaSort(e, "#validators-card", "name", "ascending")
	validatorsExpectAriaSort(e, "#peers-card", "name", "ascending")
	e.must(e.expect("th[data-sort]:not(:has(> button))").ToHaveCount(0))

	focusByKeyboard(e, `#validators-card th[data-sort="name"] > button`)
	e.must(e.Page.Keyboard().Press("Enter"))
	validatorsExpectRows(e, delta, "charlie", "bravo", "alpha")
	expectSorted(e, "#validators-card", "name", false)
	validatorsExpectAriaSort(e, "#validators-card", "name", "descending")

	e.must(e.Page.Keyboard().Press("Tab"))
	e.must(e.expect(`#validators-card th[data-sort="address"] > button`).ToBeFocused())
	e.must(e.Page.Keyboard().Press(" "))
	expectSorted(e, "#validators-card", "address", true)
	validatorsExpectAriaSort(e, "#validators-card", "address", "ascending")

	focusByKeyboard(e, `#peers-card th[data-sort="name"] > button`)
	e.must(e.Page.Keyboard().Press("Enter"))
	expectSorted(e, "#peers-card", "name", false)
	validatorsExpectAriaSort(e, "#peers-card", "name", "descending")
	e.must(e.expect(peerNameCells).ToHaveText([]string{"us-node", "sentry-1 (source)", "full-norpc", "eu-node"}))
	validatorsExpectAriaSort(e, "#validators-card", "address", "ascending")
}

// TestPageValidatorsCountryHighlightSurvivesRedraw checks that a message
// drawing the tables again under a still pointer keeps the country it is on
// highlighted, from the redraw on: the redrawn cells of that country, in
// both tables, are highlighted like the ones they replace.
func TestPageValidatorsCountryHighlightSurvivesRedraw(t *testing.T) {
	onProfiles(t, []profile{desktop, desktopWebKit}, func(t *testing.T, p profile) {
		specs := validatorsCellSpecs()
		e := openPage(t, p, specNetwork(specs...))
		cell := validatorsCell(valAlpha, valColCountry)
		e.must(e.Page.Locator(cell).Hover())
		expectMapHighlighted(e, "data-cc", "DE")

		// charlie misses a block: the tables are drawn again, the map not.
		specs[2].signing = validatorsSigning(1, 0)
		e.publish(specNetwork(specs...).snap)
		e.must(e.expect(validatorsCell(valCharlie, valColMissed)).ToHaveText("1"))
		for _, sel := range []string{cell, euNodeCountry} {
			if !hasClassNow(e, sel, "cc-hl") {
				t.Errorf("right after the redraw, %s is not highlighted", sel)
			}
		}
		expectMapHighlighted(e, "data-cc", "DE")
		expectMapDimmed(e, true)
	})
}

// TestPageValidatorsHighlightSurvivesRedraw checks that a message drawing
// the tables again under a still pointer keeps the validator it is on
// highlighted, from the redraw on: its redrawn row and cells, and what else
// carries its name; the pointer leaving right after a redraw clears it all.
func TestPageValidatorsHighlightSurvivesRedraw(t *testing.T) {
	onProfiles(t, []profile{desktop, desktopWebKit}, func(t *testing.T, p profile) {
		e := openPage(t, p, validatorsHighlightNetwork())
		row := validatorEntryAt(desktop, valBravo.address())
		proposer := `#ns-proposer [data-highlight="bravo"]`
		e.must(e.Page.Locator(validatorsCell(valBravo, valColName)).Hover())
		validatorsHighlighted(e, row, "hl-source")

		// bravo's vote delay moves: the tables are drawn again, the rest of
		// the page as it was.
		redraw := func(delayMs int) {
			t.Helper()
			next := validatorsHighlightNetwork().snap
			sig := next.Signing.ValidatorSigning[valBravo.address()]
			sig.VoteDelayMs = delayMs
			next.Signing.ValidatorSigning[valBravo.address()] = sig
			e.publish(next)
			e.must(e.expect(validatorsCell(valBravo, valColDelay)).ToHaveText(validatorsDelayText(delayMs)))
		}
		redraw(1500)
		for _, sel := range []string{row, validatorsCell(valBravo, valColName)} {
			if !hasClassNow(e, sel, "hl-source") {
				t.Errorf("right after the redraw, %s is not marked", sel)
			}
		}
		validatorsHighlighted(e, row, "hl-source")
		validatorsHighlighted(e, proposer, "hl")
		validatorsHighlighted(e, peerEntry(desktop, "bravo"), "hl")
		e.must(e.expect(".hl, .hl-source").ToHaveCount(7))

		// The pointer moves off at once, before the browser's own hover
		// update, which comes a frame after a redraw on Chromium and
		// several on WebKit.
		away := boxOf(e, "#validators-header")
		redraw(2000)
		e.must(e.Page.Mouse().Move(away.X+away.Width/2, away.Y+away.Height/2))
		e.must(e.expect(".hl, .hl-source").ToHaveCount(0))
	})
}

// TestPageValidatorsCardLinkCopiesNothing checks that a click on a card's
// name with a modifier key, which opens the validator's page in a new tab,
// copies nothing, like a plain click on it.
func TestPageValidatorsCardLinkCopiesNothing(t *testing.T) {
	e := openPage(t, tablet, healthyNetwork(), withInstalledClock())
	e.runFor(time.Second)
	bravo := validatorEntryAt(phone, valBravo.address())
	e.must(e.Page.Locator(bravo + " .peer-card-k").First().Click())
	expectToast(e, "Copied!")
	copied := e.clipboard()
	fadeToast(e)

	link := e.Page.Locator(validatorEntryAt(phone, valAlpha.address()) + " a.val-link")
	opened, err := e.Ctx.ExpectPage(func() error {
		return link.Click(playwright.LocatorClickOptions{Modifiers: []playwright.KeyboardModifier{*playwright.KeyboardModifierControlOrMeta}})
	})
	e.must(err)
	e.must(e.Expect.Page(opened).ToHaveURL(e.ts.URL + "/?val=" + valAlpha.address()))
	e.must(e.Expect.Locator(opened.Locator("#val-title")).ToHaveText("alpha"))
	e.must(e.expect(".copy-toast").ToHaveAttribute("style", toastClear))
	if got := e.clipboard(); got != copied {
		t.Errorf("after a modified click on a card's name: clipboard = %q, want it unchanged (%q)", got, copied)
	}
}

// TestPageValidatorsSortArrowJoinsItsLabel checks the sort arrow of a header
// too narrow at 1280 px for its label and arrow on one line, Vote delay: the
// arrow stays on the line of the label's last word, and out of the header's
// name, which aria-sort tells the order of.
func TestPageValidatorsSortArrowJoinsItsLabel(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	const delay = `#validators-card th[data-sort="delay"] > button`
	validatorsSortBy(e, "delay", true)
	e.must(e.expect(delay).ToHaveAccessibleName("Vote delay"))
	e.must(e.expect(delay+" > .sort-arrow").ToHaveAttribute("aria-hidden", "true"))
	// The top of the label's last letter, of the arrow, and the heights of
	// a line and of the header's button.
	m := evalFloats(e, `(sel) => {
		const b = document.querySelector(sel)
		const text = b.firstChild
		const end = text.textContent.trimEnd().length
		const r = document.createRange()
		r.setStart(text, end - 1)
		r.setEnd(text, end)
		const last = r.getBoundingClientRect()
		return [last.top, b.querySelector('.sort-arrow').getBoundingClientRect().top, last.height, b.getBoundingClientRect().height]
	}`, delay)
	last, arrow, line, button := m[0], m[1], m[2], m[3]
	if button < 1.5*line {
		t.Fatalf("the Vote delay header and its arrow hold on one line (%.1f px high, a line %.1f): the test needs them wrapped", button, line)
	}
	if math.Abs(arrow-last) > 4 {
		t.Errorf("the sort arrow's top is at %.1f px, the label's last word's at %.1f: the arrow went on a line of its own", arrow, last)
	}
}

// TestPageValidatorsHeadersFit checks that on a desktop every header label
// of both tables shows whole: a label longer than its column goes on two
// lines rather than lose its end.
func TestPageValidatorsHeadersFit(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	e.must(e.expect("#validators-body tr").ToHaveCount(4))
	const headers = ".node-list-card thead th"
	labels, err := e.Page.Locator(headers).AllTextContents()
	e.must(err)
	overflow := evalFloats(e, `(sel) => [...document.querySelectorAll(sel)].map((th) => th.scrollWidth - th.clientWidth)`, headers)
	for i, o := range overflow {
		if o > 0 {
			t.Errorf("the label %q overflows its column by %g px", strings.TrimSpace(labels[i]), o)
		}
	}
}

// TestPageValidatorsMatchTheirNodeByAddress checks that a validator is
// matched to the peer reporting its address, whatever its name: a moniker
// starting with g1, and a validator named by its address. Each row and card
// shows its node's country, and the node leaves the Peers list.
func TestPageValidatorsMatchTheirNodeByAddress(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		gee := newPageValidator("gee", "g1-charlie-validator-node", 10)
		geeNode := validatorNode(gee, "203.0.113.31", "FR", "")
		geeNode.Moniker = "gee-host"
		deltaNode := validatorNode(valDelta, "203.0.113.32", "JP", "")
		deltaNode.Moniker = "delta-host"
		e := openPage(t, p, specNetwork(
			valSpec{val: valAlpha},
			valSpec{val: gee, node: geeNode},
			valSpec{val: valDelta, node: deltaNode},
		))
		// The four healthy peers that are no validator's node.
		e.must(e.expect("#peers-header").ToHaveText("Peers (4)"))
		if p == phone {
			e.must(e.expect(validatorsCardValue(gee, "country")).ToHaveText("🇫🇷 France"))
			e.must(e.expect(validatorsCardValue(valDelta, "country")).ToHaveText("🇯🇵 Japan"))
			e.must(e.expect("#peers-cards .peer-card").ToHaveCount(4))
			return
		}
		e.must(e.expect(validatorsCell(gee, valColCountry)).ToHaveAttribute("data-cc", "FR"))
		e.must(e.expect(validatorsCell(valDelta, valColCountry)).ToHaveAttribute("data-cc", "JP"))
		e.must(e.expect("#peers-body tr").ToHaveCount(4))
	})
}

// TestPageValidatorsPeersNamedByTheirValidator checks the name a listed peer
// reporting a validator's address carries for the highlight: the validator's
// name, a moniker starting with g1 included; for a validator named by its
// address, the peer's own moniker. Each validator's first such peer is its
// node and leaves the list; a second one stays listed.
func TestPageValidatorsPeersNamedByTheirValidator(t *testing.T) {
	gee := newPageValidator("gee", "g1-charlie-validator-node", 10)
	second := func(v pageValidator, moniker string) node.Peer {
		p := *validatorNode(v, "", "", "")
		p.Moniker = moniker
		return p
	}
	sc := specNetwork(
		valSpec{val: valAlpha, node: validatorNode(valAlpha, "203.0.113.10", "", "")},
		valSpec{val: gee, node: validatorNode(gee, "203.0.113.31", "", "")},
		valSpec{val: valDelta, node: validatorNode(valDelta, "203.0.113.32", "", "")},
	)
	for i := range sc.snap.Peers {
		if sc.snap.Peers[i].ValAddress == valDelta.address() {
			sc.snap.Peers[i].Moniker = "delta-host"
		}
	}
	sc.snap.Peers = append(sc.snap.Peers,
		second(valAlpha, "alpha-sentry"), second(gee, "gee-sentry"), second(valDelta, "delta-sentry"))
	slices.SortStableFunc(sc.snap.Peers, func(a, b node.Peer) int { return cmp.Compare(a.Moniker, b.Moniker) })
	e := openPage(t, desktop, sc)
	for _, tc := range []struct{ peer, name string }{
		{"alpha-sentry", "alpha"},
		{"gee-sentry", gee.name()},
		{"delta-sentry", "delta-sentry"},
	} {
		e.must(e.expect(peerEntry(desktop, tc.peer)+" > td:first-child").ToHaveAttribute("data-highlight", tc.name))
	}
	// The four healthy peers that are no validator's node, and the three
	// second peers.
	e.must(e.expect("#peers-body tr").ToHaveCount(7))
}

// TestPageValidatorsCountryNamesInEnglishOrder checks that countries shown
// by name sort as English names, accents aside: Åland Islands with the A's,
// Türkiye before Turkmenistan.
func TestPageValidatorsCountryNamesInEnglishOrder(t *testing.T) {
	sc := healthyNetwork()
	for i, p := range sc.snap.Peers {
		switch p.Moniker {
		case "sentry-1":
			sc.snap.Peers[i].Country = "AX"
		case "full-norpc":
			sc.snap.Peers[i].Country = "TM"
		case "us-node":
			sc.snap.Peers[i].Country = "TR"
		}
	}
	e := openPage(t, desktop, sc)
	for moniker, name := range map[string]string{"sentry-1": "Åland Islands", "full-norpc": "Turkmenistan", "us-node": "Türkiye"} {
		e.must(e.expect(peerEntry(desktop, moniker)+" td.country-cell").ToHaveAttribute("data-copy", name))
	}
	sortTable(e, "#peers-card", "country", true)
	e.must(e.expect(peerNameCells).ToHaveText([]string{"sentry-1 (source)", "eu-node", "us-node", "full-norpc"}))
	sortTable(e, "#peers-card", "country", false)
	e.must(e.expect(peerNameCells).ToHaveText([]string{"full-norpc", "us-node", "eu-node", "sentry-1 (source)"}))
}

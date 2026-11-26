//go:build page

package web

import (
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/gnoverse/gnockpit/blocks"
)

const (
	// timelinePoll is how often the Timeline reads its changes again.
	timelinePoll = 30 * time.Second
	// timelineReadBound is how long the Timeline waits for an answer to a
	// read before it gives the read up.
	timelineReadBound = 10 * time.Second
	// timelineRead starts the URI of each read of the Timeline: its first
	// page is that URI alone, an older page adds &before=.
	timelineRead = "/api/history?changes=50"
	// timelineOlderRead starts the URI of an older page's read.
	timelineOlderRead = timelineRead + "&before="

	timelineList = "#validators-timeline"
	timelineRows = timelineList + " .vt-row"
	timelineAges = timelineRows + " .h"
	// timelineFooter is the footer, after the rows or the empty-history line.
	timelineFooter = timelineList + " .vt-foot:last-child"
	timelineOlder  = timelineList + " .vt-older"
	// timelineSectionToggle collapses and expands the validators section.
	timelineSectionToggle = "#validators-header > button"
)

// The page's colours, as computed styles read them.
const (
	timelineColorFG     = "rgb(201, 209, 217)"
	timelineColorFG2    = "rgb(139, 148, 158)"
	timelineColorBG     = "rgb(13, 17, 23)"
	timelineColorGreen  = "rgb(63, 185, 80)"
	timelineColorRed    = "rgb(248, 81, 73)"
	timelineColorYellow = "rgb(210, 153, 34)"
	timelineColorBlue   = "rgb(88, 166, 255)"
)

// timelineShown opens the validators card on its Timeline view.
func timelineShown() pageOption {
	return withStorage(map[string]string{validatorsViewKey: "timeline"})
}

// ---- Helpers

// timelineActivate clicks the element selector matches, or taps it on a touch
// screen.
func timelineActivate(e *pageEnv, selector string) {
	e.T.Helper()
	e.press(e.Page.Locator(selector))
}

// timelineSlack is how far apart two box edges may be and count as one:
// layouts land on fractions of a pixel.
const timelineSlack = 0.5

// timelineNear tells whether two box edges are one, within timelineSlack.
func timelineNear(a, b float64) bool {
	return math.Abs(a-b) <= timelineSlack
}

// timelineTopAge returns the assertions on the age of the Timeline's first row.
func timelineTopAge(e *pageEnv) playwright.LocatorAssertions {
	return e.Expect.Locator(e.Page.Locator(timelineAges).First())
}

// timelineFetchFailure is the message of a fetch that got no answer in p's
// browser.
func timelineFetchFailure(p profile) string {
	if p.browser == webkit {
		return "Load failed"
	}
	return "Failed to fetch"
}

// timelineHeightRow matches the first row of the change at height h, whatever
// its age.
func timelineHeightRow(h int64) *regexp.Regexp {
	return regexp.MustCompile(`^#` + regexp.QuoteMeta(timelineFormatHeight(h)) + ` · `)
}

// timelineFormatHeight writes h as the page does, in en-US digits groups.
func timelineFormatHeight(h int64) string {
	s := strconv.FormatInt(h, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// timelineAttributes returns the attribute name of each element selector
// matches, "" for an element without it.
func (e *pageEnv) timelineAttributes(selector, name string) []string {
	e.T.Helper()
	v, err := e.Page.Locator(selector).EvaluateAll(`(els, name) => els.map(el => el.getAttribute(name))`, name)
	if err != nil {
		e.T.Fatalf("read %s of %s: %v", name, selector, err)
	}
	items, ok := v.([]any)
	if !ok {
		e.T.Fatalf("read %s of %s: got %T, want a list", name, selector, v)
	}
	out := make([]string, len(items))
	for i, it := range items {
		out[i], _ = it.(string)
	}
	return out
}

// timelineBox returns the bounding box of the element selector matches.
func (e *pageEnv) timelineBox(selector string) playwright.Rect {
	e.T.Helper()
	b, err := e.Page.Locator(selector).BoundingBox()
	if err != nil {
		e.T.Fatalf("box of %s: %v", selector, err)
	}
	if b == nil {
		e.T.Fatalf("box of %s: not displayed", selector)
	}
	return *b
}

// timelineCSS returns the computed style property of the element selector
// matches.
func (e *pageEnv) timelineCSS(selector, property string) string {
	e.T.Helper()
	v, err := e.Page.Locator(selector).Evaluate(`(el, p) => getComputedStyle(el).getPropertyValue(p)`, property)
	if err != nil {
		e.T.Fatalf("%s of %s: %v", property, selector, err)
	}
	s, ok := v.(string)
	if !ok {
		e.T.Fatalf("%s of %s: got %T, want a string", property, selector, v)
	}
	return s
}

// timelineOverflow returns how much wider than its box the content of the
// element selector matches is.
func (e *pageEnv) timelineOverflow(selector string) float64 {
	e.T.Helper()
	v, err := e.Page.Locator(selector).Evaluate(`el => el.scrollWidth - el.clientWidth`, nil)
	return e.timelineNumber("the overflow of "+selector, v, err)
}

// timelineNumber returns the number a page evaluation of what gave, failing
// the test on an error or another type: the browser sends whole numbers as
// ints.
func (e *pageEnv) timelineNumber(what string, v any, err error) float64 {
	e.T.Helper()
	if err != nil {
		e.T.Fatalf("read %s: %v", what, err)
	}
	switch n := v.(type) {
	case int:
		return float64(n)
	case float64:
		return n
	}
	e.T.Fatalf("read %s: got %T, want a number", what, v)
	return 0
}

// timelineScrollY returns how far down the page is scrolled.
func (e *pageEnv) timelineScrollY() float64 {
	e.T.Helper()
	v, err := e.Page.Evaluate(`() => window.scrollY`)
	return e.timelineNumber("the scroll position", v, err)
}

// timelineExpectPagedHeights checks that the Timeline shows each change of a
// timelinePaged whose head is top once, in order: top down to 4,200, then
// the genesis set.
func timelineExpectPagedHeights(e *pageEnv, top int64) {
	e.T.Helper()
	var want []int64
	for h := top; h >= 4200; h-- {
		want = append(want, h)
	}
	want = append(want, 1)
	if got := e.timelineHeights(); !slices.Equal(got, want) {
		e.T.Errorf("heights shown = %v, want %d down to 4,200 and the genesis set", got, top)
	}
}

// timelineHeights returns the heights of the changes the Timeline shows, in
// order.
func (e *pageEnv) timelineHeights() []int64 {
	e.T.Helper()
	ages, err := e.Page.Locator(timelineAges).AllTextContents()
	if err != nil {
		e.T.Fatalf("read the ages: %v", err)
	}
	var out []int64
	for _, a := range ages {
		if a == "" {
			continue
		}
		digits, _, _ := strings.Cut(strings.TrimPrefix(a, "#"), " ")
		h, err := strconv.ParseInt(strings.ReplaceAll(digits, ",", ""), 10, 64)
		if err != nil {
			e.T.Fatalf("age %q: %v", a, err)
		}
		out = append(out, h)
	}
	return out
}

// ---- View

// TestPageTimelineView checks the Live / Timeline toggle: Live is shown
// first; Timeline shows the set changes in place of the table and the cards,
// hides the compact switch and is saved; Live brings them back.
func TestPageTimelineView(t *testing.T) {
	onProfiles(t, []profile{desktop, tablet, phone}, func(t *testing.T, p profile) {
		e := timelineOpen(t, p, timelineRich())
		live, tl := e.expect(validatorsLiveToggle), e.expect(validatorsTimelineToggle)
		liveList := "#validators-card .table-wrap"
		if p != desktop {
			liveList = "#validators-cards"
		}
		e.must(live.ToHaveText("Live"))
		e.must(tl.ToHaveText("Timeline"))
		e.must(live.ToHaveAttribute("aria-pressed", "true"))
		e.must(tl.ToHaveAttribute("aria-pressed", "false"))
		e.must(e.expect(liveList).ToBeVisible())
		e.must(e.expect(timelineList).ToBeHidden())
		if p == tablet {
			e.must(e.expect("#validators-compact-label").ToBeVisible())
		}

		timelineActivate(e, validatorsTimelineToggle)
		e.must(tl.ToHaveAttribute("aria-pressed", "true"))
		e.must(live.ToHaveAttribute("aria-pressed", "false"))
		e.must(e.expect("#validators-card").ToContainClass("view-timeline"))
		e.must(timelineTopAge(e).ToHaveText("#4,320 · just now"))
		e.must(e.expect(timelineList).ToBeVisible())
		e.must(e.expect("#validators-card .table-wrap").ToBeHidden())
		e.must(e.expect("#validators-cards").ToBeHidden())
		e.must(e.expect("#validators-compact-label").ToHaveCSS("visibility", "hidden"))
		if got, _ := e.storage(validatorsViewKey); got != "timeline" {
			t.Errorf("localStorage %s = %q after Timeline, want timeline", validatorsViewKey, got)
		}

		timelineActivate(e, validatorsLiveToggle)
		e.must(live.ToHaveAttribute("aria-pressed", "true"))
		e.must(tl.ToHaveAttribute("aria-pressed", "false"))
		e.must(e.expect("#validators-card").Not().ToContainClass("view-timeline"))
		e.must(e.expect(liveList).ToBeVisible())
		e.must(e.expect(timelineList).ToBeHidden())
		if p == tablet {
			e.must(e.expect("#validators-compact-label").ToBeVisible())
		}
		if got, _ := e.storage(validatorsViewKey); got != "live" {
			t.Errorf("localStorage %s = %q after Live, want live", validatorsViewKey, got)
		}
	})
}

// TestPageTimelineViewPersisted checks that the view saved opens the card:
// "timeline" opens the Timeline, through a reload too; anything else opens
// Live. Opening saves nothing: only a click on the switch saves a view.
func TestPageTimelineViewPersisted(t *testing.T) {
	for _, tc := range []struct {
		name, stored string // stored "" leaves the key unset
		want         string
	}{
		{name: "nothing saved", want: "live"},
		{name: "timeline saved", stored: "timeline", want: "timeline"},
		{name: "unknown view saved", stored: "Timeline", want: "live"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				var opts []pageOption
				if tc.stored != "" {
					opts = append(opts, withStorage(map[string]string{validatorsViewKey: tc.stored}))
				}
				e := timelineOpen(t, p, timelineRich(), opts...)
				for load := range 2 {
					if load > 0 {
						e.navigate("/")
					}
					e.must(e.expect(`#validators-view button[data-view="`+tc.want+`"]`).ToHaveAttribute("aria-pressed", "true"))
					if tc.want == "timeline" {
						e.must(timelineTopAge(e).ToHaveText("#4,320 · just now"))
					} else {
						e.must(e.expect(timelineList).ToBeHidden())
					}
					if got, set := e.storage(validatorsViewKey); got != tc.stored || set != (tc.stored != "") {
						t.Errorf("load %d: localStorage %s = %q (set %v), want %q, as stored", load+1, validatorsViewKey, got, set, tc.stored)
					}
				}
			})
		})
	}
}

// TestPageTimelineViewWithoutStorage checks the toggle in a browser whose
// localStorage throws on every use (site data blocked): the card opens on
// Live, and the toggle still switches to the Timeline and back.
func TestPageTimelineViewWithoutStorage(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := timelineOpenUnloaded(t, p, timelineRich())
		e.must(e.Ctx.AddInitScript(playwright.Script{Content: playwright.String(storageBlocked)}))
		e.navigate("/")
		live, tl := e.expect(validatorsLiveToggle), e.expect(validatorsTimelineToggle)
		e.must(live.ToHaveAttribute("aria-pressed", "true"))
		e.must(e.expect(timelineList).ToBeHidden())

		timelineActivate(e, validatorsTimelineToggle)
		e.must(tl.ToHaveAttribute("aria-pressed", "true"))
		e.must(timelineTopAge(e).ToHaveText("#4,320 · just now"))

		timelineActivate(e, validatorsLiveToggle)
		e.must(live.ToHaveAttribute("aria-pressed", "true"))
		e.must(e.expect(timelineList).ToBeHidden())
	})
}

// TestPageTimelineToggleLayout checks where the toggle sits in the card's
// header: centred from 641 px on, after the title, which takes the room,
// below.
func TestPageTimelineToggleLayout(t *testing.T) {
	onProfiles(t, []profile{desktop, tablet, phone}, func(t *testing.T, p profile) {
		e := timelineOpen(t, p, timelineRich())
		const header = "#validators-card > .card-header-row"
		e.must(e.expect("#validators-view").ToBeVisible())
		row, title, toggle := e.timelineBox(header), e.timelineBox(header+" > h2"), e.timelineBox("#validators-view")
		if p != phone {
			e.must(e.expect(header).ToHaveCSS("display", "grid"))
			if c, want := toggle.X+toggle.Width/2, row.X+row.Width/2; !timelineNear(c, want) {
				t.Errorf("toggle centre at %.1f, header's at %.1f: want the toggle centred", c, want)
			}
			return
		}
		e.must(e.expect(header).ToHaveCSS("display", "flex"))
		e.must(e.expect("#validators-compact-label").ToBeHidden())
		if !timelineNear(title.X, row.X) || toggle.X < title.X+title.Width-timelineSlack {
			t.Errorf("title at %.1f, toggle at %.1f, header at %.1f: want the title first, then the toggle", title.X, toggle.X, row.X)
		}
		if end, want := toggle.X+toggle.Width, row.X+row.Width; !timelineNear(end, want) {
			t.Errorf("toggle ends at %.1f, the header at %.1f: want it at the header's end", end, want)
		}
	})
}

// TestPageTimelineToggleKeyboard checks the toggle from the keyboard: Enter
// and Space switch the view, the focused button is ringed, and the pressed
// one reads light on dark.
func TestPageTimelineToggleKeyboard(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := timelineOpen(t, p, timelineRich())
		live, tl := e.Page.Locator(validatorsLiveToggle), e.Page.Locator(validatorsTimelineToggle)

		e.must(tl.Press("Enter"))
		e.must(e.expect(validatorsTimelineToggle).ToHaveAttribute("aria-pressed", "true"))
		e.must(e.expect(timelineList).ToBeVisible())
		ring := e.expect(validatorsTimelineToggle)
		e.must(ring.ToBeFocused())
		e.must(ring.ToHaveCSS("outline-style", "solid"))
		e.must(ring.ToHaveCSS("outline-width", "2px"))
		e.must(ring.ToHaveCSS("outline-color", timelineColorBlue))
		e.must(ring.ToHaveCSS("color", timelineColorFG))
		e.must(ring.ToHaveCSS("background-color", timelineColorBG))
		e.must(e.expect(validatorsLiveToggle).ToHaveCSS("color", timelineColorFG2))
		e.must(e.expect(validatorsLiveToggle).ToHaveCSS("background-color", "rgba(0, 0, 0, 0)"))

		e.must(live.Press("Space"))
		e.must(e.expect(validatorsLiveToggle).ToHaveAttribute("aria-pressed", "true"))
		e.must(e.expect(timelineList).ToBeHidden())
		e.must(e.expect(validatorsLiveToggle).ToHaveCSS("color", timelineColorFG))
		e.must(e.expect(validatorsTimelineToggle).ToHaveCSS("color", timelineColorFG2))
	})
}

// ---- Rows

// TestPageTimelineChangeKinds checks the rows of each kind of change: newest
// first, a change's lines in the order joined, left, power, rotation; the
// height, age and size on its first line only; names linked to their
// validator's page; each kind's symbol and colour; events not read yet
// flagged.
func TestPageTimelineChangeKinds(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := timelineOpen(t, p, timelineRich(), timelineShown())
		want := timelineRichRows()
		var ages, ks, kinds, whats, sizes, pops []string
		for _, r := range want {
			ages = append(ages, r.age)
			ks = append(ks, r.k)
			kinds = append(kinds, "k "+r.kind)
			whats = append(whats, r.what)
			sizes = append(sizes, r.sz)
			pops = append(pops, timelineUTCMinute(r.at))
		}
		e.must(e.expect(timelineAges).ToHaveText(ages))
		e.must(e.expect(timelineRows + " .k").ToHaveText(ks))
		e.must(e.expect(timelineRows + " .k").ToHaveClass(kinds))
		e.must(e.expect(timelineRows + " .w").ToHaveText(whats))
		e.must(e.expect(timelineRows + " .n").ToHaveText(sizes))

		for kind, color := range map[string]string{
			"vt-add": timelineColorGreen, "vt-rm": timelineColorRed, "vt-pow": timelineColorYellow, "vt-rot": timelineColorBlue, "vt-gen": timelineColorFG2,
		} {
			e.must(e.Expect.Locator(e.Page.Locator(timelineRows+" .k."+kind).First()).ToHaveCSS("color", color))
		}
		pending := e.expect(timelineRows + " .w .vt-pend")
		e.must(pending.ToHaveCount(1))
		e.must(pending.ToHaveText("events pending"))
		e.must(pending.ToHaveCSS("color", timelineColorYellow))
		e.must(pending.ToHaveCSS("font-size", "11px"))
		e.must(pending.ToHaveCSS("margin-left", "6px"))

		if got := e.timelineAttributes(timelineAges, "data-popover"); !slices.Equal(got, pops) {
			t.Errorf("age popovers = %q, want %q", got, pops)
		}
		e.must(e.expect(timelineAges + ".tap-popover").ToHaveCount(len(want)))

		links := timelineRows + " .w a.val-link"
		linked := []pageValidator{
			timelineKilo, timelineKilo, timelineJuliet, timelineGolf, valAlpha, timelineDeltaNew, timelineHotelNew, valBravo, valCharlie,
			timelineEcho, timelineFoxtrot, timelineGolf, timelineHotel, timelineIndia, timelineEcho, timelineFoxtrot, valDelta,
		}
		var hrefs []string
		for _, v := range linked {
			hrefs = append(hrefs, "?val="+v.address())
		}
		if got := e.timelineAttributes(links, "href"); !slices.Equal(got, hrefs) {
			t.Errorf("name links = %q, want %q", got, hrefs)
		}
		e.must(e.expect(links + " > b").ToHaveCount(len(linked)))
	})
}

// TestPageTimelineShortAddressShownWhole checks that an unnamed member's
// address too short to shorten is shown whole.
func TestPageTimelineShortAddressShownWhole(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		const short = "g1shortkey" // under the 12 characters the page shortens
		tl := newTimelineScenario(timelineProgress(1, pageHeight, 0), func(t *testing.T, s *blocks.Store) {
			grown := append(timelineGenesisSet(), blocks.Member{Address: short, Power: 1})
			timelinePutEras(t, s, pageHeight, timelineEra{1, timelineGenesisSet()}, timelineEra{4000, grown})
		})
		e := timelineOpen(t, p, tl, timelineShown())
		first := timelineRows + ":first-child"
		e.must(e.expect(first + " .w").ToHaveText(short + " joined power 1"))
		e.must(e.expect(first+" .w a.val-link").ToHaveAttribute("href", "?val="+short))
	})
}

// TestPageTimelineNameOpensValidatorPage checks that a name in the Timeline
// opens its validator's page.
func TestPageTimelineNameOpensValidatorPage(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := timelineOpen(t, p, timelineRich(), timelineShown())
		name := e.Page.Locator(timelineList).GetByRole("link", playwright.LocatorGetByRoleOptions{Name: "juliet", Exact: playwright.Bool(true)})
		e.press(name)
		e.must(e.expect("#val-modal").ToContainClass("open"))
		e.must(e.expect("#val-title").ToHaveText("juliet"))
		e.must(e.Expect.Page(e.Page).ToHaveURL(regexp.MustCompile(`\?val=` + timelineJuliet.address() + `$`)))
	})
}

// TestPageTimelineAgesOnTheServersClock checks that the rows' ages read the
// server's time, which the Timeline's answers carry, on a direct load with
// the browser's clock 10 min behind and no WebSocket message yet to set the
// page's clock by.
func TestPageTimelineAgesOnTheServersClock(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := timelineOpenUnloaded(t, p, timelineRich(), timelineShown(), withBrowserOffset(-10*time.Minute))
		ws := e.hold("/ws")
		e.navigate("/")
		e.must(e.gate.awaitHeld("/ws", 1))
		ages := e.Page.Locator(timelineAges)
		e.must(e.Expect.Locator(ages.Nth(0)).ToHaveText("#4,320 · just now"))
		e.must(e.Expect.Locator(ages.Nth(1)).ToHaveText("#4,310 · 10 min ago"))
		ws.release()
		e.must(e.expect("#d-block").ToHaveText(strconv.Itoa(pageHeight)))
	})
}

// TestPageTimelineAgePopover checks that a change's age gives its time, UTC
// to the minute: on hover, or on a tap on a touch screen.
func TestPageTimelineAgePopover(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := timelineOpen(t, p, timelineRich(), timelineShown())
		age := e.Page.Locator(timelineAges).Nth(2)
		e.must(e.Expect.Locator(age).ToHaveText("#4,000 · 5 h ago"))
		// The age is scrolled into view before the pointer reaches it: a
		// scroll event arriving after the popover opens would hide it.
		e.showPopover(age)
		e.must(e.expect(pagePopover).ToHaveText(timelineUTCMinute(4000)))
	})
}

// TestPageTimelineEmptyHistory checks a history that holds no set change
// yet: a line says so, above the footer saying how far the filling history
// reaches; nothing older is offered.
func TestPageTimelineEmptyHistory(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		tl := newTimelineScenario(timelineProgress(4000, pageHeight, 0), func(t *testing.T, s *blocks.Store) {
			timelinePutEras(t, s, pageHeight, timelineEra{4000, timelineGenesisSet()})
		})
		e := timelineOpen(t, p, tl, timelineShown())
		e.must(e.expect(timelineList + " .vt-foot").ToHaveText([]string{
			"No validator set change in the history yet",
			"history reaches back to #4,000: older changes appear as it fills",
		}))
		e.must(e.expect(timelineRows).ToHaveCount(0))
		e.must(e.expect(timelineOlder).ToHaveCount(0))
	})
}

// TestPageTimelineLayout checks the rows' grid: on a wide screen one line of
// age, symbol, text and size, the age and size muted with aligned digits; on
// a phone the symbol beside the text, the age and the size below it.
func TestPageTimelineLayout(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := timelineOpen(t, p, timelineRich(), timelineShown())
		e.must(timelineTopAge(e).ToHaveText("#4,320 · just now"))
		row := timelineRows + ":nth-child(3)"
		columns := e.timelineCSS(row, "grid-template-columns")
		h, k, w, n := e.timelineBox(row+" .h"), e.timelineBox(row+" .k"), e.timelineBox(row+" .w"), e.timelineBox(row+" .n")
		if p == phone {
			if tracks := strings.Fields(columns); len(tracks) != 2 || tracks[0] != "18px" {
				t.Errorf("grid columns = %q, want 18px and the rest", columns)
			}
			if k.X+k.Width > w.X+timelineSlack || !timelineNear(h.X, w.X) || !timelineNear(n.X, w.X) {
				t.Errorf("symbol at %.1f+%.1f, text at %.1f, age at %.1f, size at %.1f: want the symbol, then text, age and size in one column",
					k.X, k.Width, w.X, h.X, n.X)
			}
			if h.Y < w.Y+w.Height-timelineSlack || n.Y < h.Y+h.Height-timelineSlack {
				t.Errorf("text at y %.1f+%.1f, age at %.1f+%.1f, size at %.1f: want text, age, size top to bottom", w.Y, w.Height, h.Y, h.Height, n.Y)
			}
			e.must(e.expect(row+" .h").ToHaveCSS("font-size", "12px"))
			e.must(e.expect(row+" .n").ToHaveCSS("font-size", "12px"))
			e.must(e.expect(row+" .w").ToHaveCSS("white-space", "normal"))
			// A change's later line keeps the age's and the size's grid rows,
			// empty: two 4 px row gaps under its text.
			next := timelineRows + ":nth-child(4)"
			e.must(e.expect(next + " .h").ToHaveText(""))
			e.must(e.expect(next + " .n").ToHaveText(""))
			e.must(e.expect(next+" .h").ToHaveCSS("grid-row-start", "2"))
			e.must(e.expect(next+" .n").ToHaveCSS("grid-row-start", "3"))
			line, text := e.timelineBox(next), e.timelineBox(next+" .w")
			const padding, border, gaps = 12, 1, 8
			if want := text.Height + padding + border + gaps; !timelineNear(line.Height, want) {
				t.Errorf("later line %.1f px high, text %.1f px: want %.1f, the text and two empty grid rows", line.Height, text.Height, want)
			}
			return
		}
		if tracks := strings.Fields(columns); len(tracks) != 4 || tracks[1] != "18px" {
			t.Errorf("grid columns = %q, want age, 18px, text and size", columns)
		}
		if !(h.X < k.X && k.X < w.X && w.X < n.X) {
			t.Errorf("age at %.1f, symbol at %.1f, text at %.1f, size at %.1f: want them left to right", h.X, k.X, w.X, n.X)
		}
		for _, cell := range []string{row + " .h", row + " .n", timelineRows + ":nth-child(1) .p"} {
			c := e.expect(cell)
			e.must(c.ToHaveCSS("color", timelineColorFG2))
			e.must(c.ToHaveCSS("font-variant-numeric", "tabular-nums"))
			e.must(c.ToHaveCSS("white-space", "nowrap"))
		}
	})
}

// TestPageTimelineRowTextWraps checks that a long line of names wraps rather
// than being cut: the whole line, its power last, stays in view.
func TestPageTimelineRowTextWraps(t *testing.T) {
	onProfiles(t, []profile{desktop, tablet, phone}, func(t *testing.T, p profile) {
		joiners := make([]pageValidator, 12)
		for i := range joiners {
			name := fmt.Sprintf("validator-number-%02d", i+1)
			joiners[i] = newPageValidator(name, name, 1)
		}
		tl := newTimelineScenario(timelineProgress(1, pageHeight, 0), func(t *testing.T, s *blocks.Store) {
			grown := append(timelineGenesisSet(), members(joiners...)...)
			timelinePutEras(t, s, pageHeight, timelineEra{1, timelineGenesisSet()}, timelineEra{4000, grown})
		})
		for _, v := range joiners {
			tl.sc.names[v.address()] = v.moniker
		}
		e := timelineOpen(t, p, tl, timelineShown())
		what := timelineRows + ":first-child .w"
		e.must(e.expect(what).ToContainText("validator-number-11 and validator-number-12 joined power 1 each"))
		if cut := e.timelineOverflow(what); cut > 0 {
			t.Errorf("the line overflows its column by %.0f px, want it wrapped", cut)
		}
		w, power := e.timelineBox(what), e.timelineBox(what+" .p")
		if power.X+power.Width > w.X+w.Width+timelineSlack || power.Y+power.Height > w.Y+w.Height+timelineSlack {
			t.Errorf("power at %.1f,%.1f+%.1fx%.1f, outside the line's box %.1f,%.1f+%.1fx%.1f",
				power.X, power.Y, power.Width, power.Height, w.X, w.Y, w.Width, w.Height)
		}
	})
}

// TestPageTimelineLongNameWraps checks that a name with no break in it
// wraps inside its column rather than spilling over the size beside it.
func TestPageTimelineLongNameWraps(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		// Wider than the text's column on any screen.
		long := newPageValidator("long", strings.Repeat("validator", 16), 1)
		tl := newTimelineScenario(timelineProgress(1, pageHeight, 0), func(t *testing.T, s *blocks.Store) {
			grown := append(timelineGenesisSet(), memberAt(long, 1))
			timelinePutEras(t, s, pageHeight, timelineEra{1, timelineGenesisSet()}, timelineEra{4000, grown})
		})
		tl.sc.names[long.address()] = long.moniker
		e := timelineOpen(t, p, tl, timelineShown())
		what := timelineRows + ":first-child .w"
		e.must(e.expect(what).ToHaveText(long.moniker + " joined power 1"))
		if cut := e.timelineOverflow(what); cut > 0 {
			t.Errorf("the name overflows its column by %.0f px, want it wrapped", cut)
		}
	})
}

// ---- Reads

// TestPageTimelinePolls checks the Timeline's reads: every 30 s while it is
// shown, the section collapsed too; none while Live is shown, whose earlier
// rows show again until the read that showing the Timeline starts answers;
// and at once on a click on Timeline while it is shown.
func TestPageTimelinePolls(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		tl := timelineRich()
		e := timelineOpen(t, p, tl, timelineShown(), withInstalledClock())
		top := timelineTopAge(e)
		e.must(top.ToHaveText("#4,320 · just now"))
		withKilo := timelineRichSets()[6].members
		withoutKilo := timelineRichSets()[5].members
		change := func(h int64, members []blocks.Member) {
			t.Helper()
			timelinePutEras(t, e.Srv.Blocks, h, timelineEra{h, members})
			tl.fill.set(timelineProgress(1, h, 0))
		}

		change(4321, withKilo)
		e.runFor(timelinePoll)
		e.must(top.ToHaveText(timelineHeightRow(4321)))

		timelineActivate(e, timelineSectionToggle)
		e.must(e.expect(timelineList).ToBeHidden())
		change(4322, withoutKilo)
		e.runFor(timelinePoll)
		timelineActivate(e, timelineSectionToggle)
		e.must(top.ToHaveText(timelineHeightRow(4322)))

		timelineActivate(e, validatorsLiveToggle)
		e.must(e.expect(timelineList).ToBeHidden())
		change(4323, withKilo)
		for range 3 {
			e.runFor(timelinePoll)
		}
		read := e.hold(timelineRead)
		timelineActivate(e, validatorsTimelineToggle)
		e.must(e.expect(timelineList).ToBeVisible())
		e.must(top.ToHaveText(timelineHeightRow(4322)))
		read.release()
		e.must(top.ToHaveText(timelineHeightRow(4323)))

		change(4324, withoutKilo)
		timelineActivate(e, validatorsTimelineToggle)
		e.must(top.ToHaveText(timelineHeightRow(4324)))
	})
}

// TestPageTimelineLiveEndsTheQueuedPoll checks that a poll waiting for an
// older read reads nothing once Live is shown.
func TestPageTimelineLiveEndsTheQueuedPoll(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := timelineOpen(t, p, timelinePaged(), timelineShown(), withInstalledClock())
		e.must(e.expect(timelineRows).ToHaveCount(50))

		// The older read is asked 5 s before the poll, and answers within
		// the 10 s bound.
		const ahead = 5 * time.Second
		e.runFor(timelinePoll - ahead)
		read := e.hold(timelineOlderRead + "4271")
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineOlder).ToBeDisabled())
		e.runFor(ahead)
		// The browser reports requests in the order it sends them. The first
		// pages read before the next read below 4,221, which only "older"
		// clicked once the Timeline is shown again asks, are the new
		// showing's and any the queued poll sent.
		var firsts atomic.Int32
		firstsBeforeOlder := make(chan int32, 1)
		e.Page.OnRequest(func(r playwright.Request) {
			switch {
			case strings.HasSuffix(r.URL(), timelineRead):
				firsts.Add(1)
			case strings.HasSuffix(r.URL(), timelineOlderRead+"4221"):
				select {
				case firstsBeforeOlder <- firsts.Load():
				default:
				}
			}
		})
		timelineActivate(e, validatorsLiveToggle)
		read.release()
		e.must(e.expect(timelineRows).ToHaveCount(100))
		timelineActivate(e, validatorsTimelineToggle)
		timelineActivate(e, timelineOlder)
		select {
		case n := <-firstsBeforeOlder:
			if n != 1 {
				t.Errorf("first page read %d times before the next older read, want 1: the new showing's, none for the poll Live ended", n)
			}
		case <-time.After(pageTimeout):
			t.Fatal("no older read below 4,221")
		}
		e.must(e.expect(timelineRows).ToHaveCount(122))
	})
}

// TestPageTimelineLiveStopsAPollMidway checks that a poll reading the pages
// shown reads no further page once Live is shown.
func TestPageTimelineLiveStopsAPollMidway(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := timelineOpen(t, p, timelinePaged(), timelineShown(), withInstalledClock())
		e.must(e.expect(timelineRows).ToHaveCount(50))
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineRows).ToHaveCount(100))
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineRows).ToHaveCount(122))

		// The poll reads three pages: the second is held, the third comes
		// below 4,221.
		second := e.hold(timelineOlderRead + "4271")
		e.runFor(timelinePoll)
		e.must(e.gate.awaitHeld(timelineOlderRead+"4271", 1))
		// The browser reports requests in the order it sends them, the
		// poll's first page before its held second: the third pages read
		// before the next first page are the poll's.
		var thirds atomic.Int32
		thirdsBeforeNext := make(chan int32, 1)
		e.Page.OnRequest(func(r playwright.Request) {
			switch {
			case strings.HasSuffix(r.URL(), timelineOlderRead+"4221"):
				thirds.Add(1)
			case strings.HasSuffix(r.URL(), timelineRead):
				select {
				case thirdsBeforeNext <- thirds.Load():
				default:
				}
			}
		})
		timelineActivate(e, validatorsLiveToggle)
		e.must(e.expect(timelineList).ToBeHidden())
		second.release()
		e.runFor(time.Second)
		timelineActivate(e, validatorsTimelineToggle)
		select {
		case n := <-thirdsBeforeNext:
			if n != 0 {
				t.Errorf("third page read %d times once Live was shown, want none", n)
			}
		case <-time.After(pageTimeout):
			t.Fatal("no first page read once the Timeline was shown again")
		}
	})
}

// TestPageTimelineOlder checks reading older changes: "older" adds the next
// 50 below the rows, and goes once the last page has come, the genesis set
// at its end.
func TestPageTimelineOlder(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := timelineOpen(t, p, timelinePaged(), timelineShown())
		ages := e.Page.Locator(timelineAges)
		e.must(e.expect(timelineRows).ToHaveCount(50))
		e.must(e.Expect.Locator(ages.Nth(49)).ToHaveText(timelineHeightRow(4271)))
		e.must(e.expect(timelineFooter).ToHaveText("older"))

		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineRows).ToHaveCount(100))
		e.must(e.Expect.Locator(ages.Nth(50)).ToHaveText(timelineHeightRow(4270)))
		e.must(e.Expect.Locator(ages.Nth(99)).ToHaveText(timelineHeightRow(4221)))
		e.must(e.expect(timelineOlder).ToBeVisible())

		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineRows).ToHaveCount(122))
		e.must(e.Expect.Locator(ages.Nth(100)).ToHaveText(timelineHeightRow(4220)))
		e.must(e.Expect.Locator(ages.Nth(120)).ToHaveText(timelineHeightRow(4200)))
		e.must(e.Expect.Locator(ages.Nth(121)).ToHaveText("#1 · 3 d ago"))
		e.must(e.expect(timelineOlder).ToHaveCount(0))
		e.must(e.expect(timelineFooter).ToHaveText(""))
	})
}

// TestPageTimelineTwoFullPagesEndPaging checks a history of exactly two
// pages of changes, complete from the source's lowest height: the second page
// ends the paging, with no empty page to read below it, and the next poll
// does not offer "older" again.
func TestPageTimelineTwoFullPagesEndPaging(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		tl := newTimelineScenario(timelineProgress(4220, pageHeight, 4220), func(t *testing.T, s *blocks.Store) {
			timelinePutFlips(t, s, 4220, pageHeight)
		})
		e := timelineOpen(t, p, tl, timelineShown(), withInstalledClock())
		e.must(e.expect(timelineRows).ToHaveCount(50))

		read := e.hold(timelineOlderRead + "4271")
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineOlder).ToBeDisabled())
		read.release()
		e.must(e.expect(timelineRows).ToHaveCount(100))
		e.must(e.Expect.Locator(e.Page.Locator(timelineAges).Last()).ToHaveText(timelineHeightRow(4221)))
		e.must(e.expect(timelineOlder).ToHaveCount(0))

		e.runFor(timelinePoll)
		e.must(timelineTopAge(e).ToHaveText("#4,320 · 1 min ago"))
		e.must(e.expect(timelineRows).ToHaveCount(100))
		e.must(e.expect(timelineOlder).ToHaveCount(0))
	})
}

// TestPageTimelineRecoversAfterError checks that the Timeline outlives
// failed reads: the rows stay with the footer saying the read failed, the
// reads go on every 30 s, and the first one that answers clears the error.
func TestPageTimelineRecoversAfterError(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		tl := timelineRich()
		e := timelineOpen(t, p, tl, timelineShown(), withInstalledClock())
		top := timelineTopAge(e)
		e.must(top.ToHaveText("#4,320 · just now"))
		rows := len(timelineRichRows())

		failure := "Could not read the set changes (" + timelineFetchFailure(p) + "): trying again shortly."
		for range 2 {
			failed := e.hold(timelineRead)
			failed.drop()
			e.runFor(timelinePoll)
			failed.awaitFailure()
			e.must(e.expect(timelineFooter).ToHaveText(failure))
			e.must(e.expect(timelineFooter+" .vt-err").ToHaveCSS("color", timelineColorRed))
			e.must(e.expect(timelineRows).ToHaveCount(rows))
			e.must(top.ToHaveText(timelineHeightRow(pageHeight)))
		}

		withKilo := timelineRichSets()[6].members
		timelinePutEras(t, e.Srv.Blocks, 4321, timelineEra{4321, withKilo})
		tl.fill.set(timelineProgress(1, 4321, 0))
		e.runFor(timelinePoll)
		e.must(top.ToHaveText(timelineHeightRow(4321)))
		e.must(e.expect(timelineFooter).ToHaveText(""))
		e.must(e.expect(timelineRows).ToHaveCount(rows + 1))

		timelinePutEras(t, e.Srv.Blocks, 4322, timelineEra{4322, timelineRichSets()[5].members})
		tl.fill.set(timelineProgress(1, 4322, 0))
		e.runFor(timelinePoll)
		e.must(top.ToHaveText(timelineHeightRow(4322)))
	})
}

// TestPageTimelineRecoversFromALaterPageError checks a poll whose read of a
// page after the first fails: the rows read before stay, the footer says the
// read failed, and the next poll reads them all again.
func TestPageTimelineRecoversFromALaterPageError(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := timelineOpen(t, p, timelinePaged(), timelineShown(), withInstalledClock())
		e.must(e.expect(timelineRows).ToHaveCount(50))
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineRows).ToHaveCount(100))
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineRows).ToHaveCount(122))

		failed := e.hold(timelineOlderRead + "4271")
		failed.drop()
		e.runFor(timelinePoll)
		failed.awaitFailure()
		e.must(e.expect(timelineFooter).ToHaveText("Could not read the set changes (" + timelineFetchFailure(p) + "): trying again shortly."))
		e.must(e.expect(timelineRows).ToHaveCount(122))

		e.runFor(timelinePoll)
		e.must(e.expect(timelineFooter + " .vt-err").ToHaveCount(0))
		e.must(e.expect(timelineRows).ToHaveCount(122))
		timelineExpectPagedHeights(e, pageHeight)
	})
}

// TestPageTimelineReadWithoutAnswer checks a read gnockpit never answers:
// the Timeline gives it up after 10 s and says so, and the next poll and
// "older" read again.
func TestPageTimelineReadWithoutAnswer(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := timelineOpen(t, p, timelinePaged(), timelineShown(), withInstalledClock())
		e.must(e.expect(timelineRows).ToHaveCount(50))

		stuck := e.hold(timelineRead)
		e.runFor(timelinePoll)
		e.runFor(timelineReadBound)
		stuck.awaitAbandoned()
		e.must(e.expect(timelineFooter + " .vt-err").ToHaveText("Could not read the set changes (no answer in 10 s): trying again shortly."))
		e.must(e.expect(timelineRows).ToHaveCount(50))

		e.runFor(timelinePoll)
		e.must(e.expect(timelineFooter + " .vt-err").ToHaveCount(0))
		e.must(e.expect(timelineRows).ToHaveCount(50))
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineRows).ToHaveCount(100))
	})
}

// TestPageTimelineOlderError checks a failed read of older changes: the
// rows and "older" stay, and the footer says the read failed until the next
// read that answers, a poll's or a new click's.
func TestPageTimelineOlderError(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := timelineOpen(t, p, timelinePaged(), timelineShown(), withInstalledClock())
		e.must(e.expect(timelineRows).ToHaveCount(50))
		failure := "Could not read older changes (" + timelineFetchFailure(p) + ")."

		failed := e.hold(timelineOlderRead + "4271")
		failed.drop()
		timelineActivate(e, timelineOlder)
		failed.awaitFailure()
		e.must(e.expect(timelineFooter + " .vt-err").ToHaveText(failure))
		e.must(e.expect(timelineRows).ToHaveCount(50))
		e.must(e.expect(timelineOlder).ToBeVisible())
		e.runFor(timelinePoll)
		e.must(e.expect(timelineFooter + " .vt-err").ToHaveCount(0))
		e.must(e.expect(timelineRows).ToHaveCount(50))

		again := e.hold(timelineOlderRead + "4271")
		again.drop()
		timelineActivate(e, timelineOlder)
		again.awaitFailure()
		e.must(e.expect(timelineFooter + " .vt-err").ToHaveText(failure))
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineRows).ToHaveCount(100))
		e.must(e.expect(timelineFooter + " .vt-err").ToHaveCount(0))
		e.must(e.expect(timelineFooter).ToHaveText("older"))
	})
}

// TestPageTimelineLoadingUntilFirstAnswer checks that the Timeline reads
// "loading…" until its first read answers, whether the page opens on it or
// switches to it from Live.
func TestPageTimelineLoadingUntilFirstAnswer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		saved bool // the page opens on the Timeline
	}{
		{name: "opened on the Timeline", saved: true},
		{name: "switched to the Timeline"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				var opts []pageOption
				if tc.saved {
					opts = append(opts, timelineShown())
				}
				e := timelineOpenUnloaded(t, p, timelineRich(), opts...)
				read := e.hold(timelineRead)
				e.navigate("/")
				if !tc.saved {
					timelineActivate(e, validatorsTimelineToggle)
				}
				e.must(e.expect(timelineList).ToBeVisible())
				e.must(e.expect(timelineList).ToHaveText("loading…"))
				read.release()
				e.must(timelineTopAge(e).ToHaveText("#4,320 · just now"))
				e.must(e.expect(timelineList).Not().ToContainText("loading…"))
			})
		})
	}
}

// TestPageTimelineShowsTheServersError checks that a read the server refuses
// shows the server's reason in the footer, alone: no empty-history line, no
// "older", and no promise of a retry a disabled history cannot keep.
func TestPageTimelineShowsTheServersError(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := healthyNetwork()
		sc.seed = nil
		e := openPage(t, p, sc, timelineShown())
		e.must(e.expect(timelineFooter).ToHaveText("Could not read the set changes (block history disabled)."))
		e.must(e.expect(timelineList + " .vt-foot").ToHaveCount(1))
		e.must(e.expect(timelineRows).ToHaveCount(0))
		e.must(e.expect(timelineOlder).ToHaveCount(0))
	})
}

// TestPageTimelineShowsTheStatusWithoutAReason checks a failed read whose
// answer gives no reason, as the proxy in front of gnockpit sends when
// gnockpit does not answer in time: the footer names the HTTP status, and
// promises the retry, a proxy's 503 included.
func TestPageTimelineShowsTheStatusWithoutAReason(t *testing.T) {
	for _, status := range []int{http.StatusBadGateway, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				e := timelineOpenUnloaded(t, p, timelineRich(), expectStatus("/api/history", status))
				front := e.behindProxy("/api/history", status)
				e.navigateTo(front + "/")
				e.hold(timelineRead)
				timelineActivate(e, validatorsTimelineToggle)
				e.must(e.expect(timelineFooter).ToHaveText(fmt.Sprintf("Could not read the set changes (HTTP %d): trying again shortly.", status)))
				e.must(e.expect(timelineRows).ToHaveCount(0))
			})
		})
	}
}

// TestPageTimelineKeepsFocusOnOlder checks that a keyboard user on "older"
// stays on it when the list redraws: after its own read, and after a poll.
func TestPageTimelineKeepsFocusOnOlder(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		tl := timelinePaged()
		e := timelineOpen(t, p, tl, timelineShown(), withInstalledClock())
		e.must(e.expect(timelineRows).ToHaveCount(50))

		e.must(e.Page.Locator(timelineOlder).Press("Enter"))
		e.must(e.expect(timelineRows).ToHaveCount(100))
		e.must(e.expect(timelineOlder).ToBeFocused())

		timelinePutFlips(t, e.Srv.Blocks, pageHeight+1, pageHeight+1)
		tl.fill.set(timelineProgress(1, pageHeight+1, 0))
		e.runFor(timelinePoll)
		e.must(timelineTopAge(e).ToHaveText(timelineHeightRow(pageHeight + 1)))
		e.must(e.expect(timelineOlder).ToBeFocused())
	})
}

// TestPageTimelineFocusAfterTheLastPage checks that the read of the last
// page, which removes "older", hands a keyboard user on it to the first name
// it added.
func TestPageTimelineFocusAfterTheLastPage(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := timelineOpen(t, p, timelinePaged(), timelineShown())
		rows := e.Page.Locator(timelineRows)
		e.must(e.expect(timelineRows).ToHaveCount(50))
		e.must(e.Page.Locator(timelineOlder).Press("Enter"))
		e.must(e.expect(timelineRows).ToHaveCount(100))
		e.must(e.Page.Locator(timelineOlder).Press("Enter"))
		e.must(e.expect(timelineRows).ToHaveCount(122))
		e.must(e.expect(timelineOlder).ToHaveCount(0))
		e.must(e.Expect.Locator(rows.Nth(100).Locator(".h")).ToHaveText(timelineHeightRow(4220)))
		e.must(e.Expect.Locator(rows.Nth(100).Locator("a.val-link")).ToBeFocused())
	})
}

// TestPageTimelineFocusAfterANamelessLastPage checks the read of the last
// page when it adds no name, an empty page or the genesis set alone: a
// keyboard user on "older" lands on the last name of the list.
func TestPageTimelineFocusAfterANamelessLastPage(t *testing.T) {
	for _, tc := range []struct {
		name string
		tl   func() *timelineScenario
		// reads are the rows shown after each "older" read, to the last
		// page; complete, when set, ends the history's fill before the last.
		reads    []int
		complete func(tl *timelineScenario)
		named    int   // the row of the last name
		lowest   int64 // its change's height
	}{
		{
			name: "empty page",
			// Two pages of changes while the history fills, "older" offered
			// below them; the fill then ends at the source's lowest height
			// with no change below: the last page is empty.
			tl: func() *timelineScenario {
				return newTimelineScenario(timelineProgress(4220, pageHeight, 0), func(t *testing.T, s *blocks.Store) {
					timelinePutFlips(t, s, 4220, pageHeight)
				})
			},
			reads: []int{100, 100},
			complete: func(tl *timelineScenario) {
				tl.fill.set(timelineProgress(4220, pageHeight, 4220))
			},
			named: 99, lowest: 4221,
		},
		{
			name: "genesis set alone",
			// One page of changes above the genesis set: the last page is
			// the genesis set.
			tl: func() *timelineScenario {
				return newTimelineScenario(timelineProgress(1, 4319, 0), func(t *testing.T, s *blocks.Store) {
					timelinePutEras(t, s, 4269, timelineEra{1, timelineGenesisSet()})
					timelinePutFlips(t, s, 4270, 4319)
				})
			},
			reads: []int{51}, named: 49, lowest: 4270,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				tl := tc.tl()
				e := timelineOpen(t, p, tl, timelineShown())
				e.must(e.expect(timelineRows).ToHaveCount(50))
				for i, rows := range tc.reads {
					if i == len(tc.reads)-1 && tc.complete != nil {
						tc.complete(tl)
					}
					e.must(e.Page.Locator(timelineOlder).Press("Enter"))
					e.must(e.expect(timelineRows).ToHaveCount(rows))
					if i < len(tc.reads)-1 {
						e.must(e.expect(timelineOlder).ToBeFocused())
					}
				}
				e.must(e.expect(timelineOlder).ToHaveCount(0))
				row := e.Page.Locator(timelineRows).Nth(tc.named)
				e.must(e.Expect.Locator(row.Locator(".h")).ToHaveText(timelineHeightRow(tc.lowest)))
				e.must(e.Expect.Locator(row.Locator("a.val-link")).ToBeFocused())
				e.must(e.Expect.Locator(e.Page.Locator(timelineRows + " a.val-link").Last()).ToBeFocused())
			})
		})
	}
}

// TestPageTimelineKeepsFocusOnAName checks that a keyboard user on a name
// stays on it when a poll redraws the list: on the same name of the same
// change, though the validator has other rows, and the second name of a
// line too. Focus outside the list stays where it is.
func TestPageTimelineKeepsFocusOnAName(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		tl := timelineRich()
		e := timelineOpen(t, p, tl, timelineShown(), withInstalledClock())
		rows := e.Page.Locator(timelineRows)
		withKilo, withoutKilo := timelineRichSets()[6].members, timelineRichSets()[5].members
		poll := func(h int64, members []blocks.Member) {
			t.Helper()
			timelinePutEras(t, e.Srv.Blocks, h, timelineEra{h, members})
			tl.fill.set(timelineProgress(1, h, 0))
			e.runFor(timelinePoll)
			e.must(timelineTopAge(e).ToHaveText(timelineHeightRow(h)))
		}

		// kilo joined at 4,310 and left at 4,320: the 4,310 name.
		e.must(e.Expect.Locator(rows.Nth(1).Locator(".h")).ToHaveText(timelineHeightRow(4310)))
		e.must(rows.Nth(1).Locator("a.val-link").Focus())
		poll(pageHeight+1, withKilo)
		e.must(e.Expect.Locator(rows.Nth(2).Locator("a.val-link")).ToBeFocused())

		// foxtrot, the second name of "echo and foxtrot left" at 3,000.
		e.must(e.Expect.Locator(rows.Nth(10).Locator(".h")).ToHaveText(timelineHeightRow(3000)))
		e.must(rows.Nth(10).Locator("a.val-link").Nth(1).Focus())
		poll(pageHeight+2, withoutKilo)
		e.must(e.Expect.Locator(rows.Nth(11).Locator("a.val-link").Nth(1)).ToBeFocused())

		e.must(e.Page.Locator(validatorsLiveToggle).Focus())
		poll(pageHeight+3, withKilo)
		e.must(e.expect(validatorsLiveToggle).ToBeFocused())
	})
}

// TestPageTimelineRefocusKeepsTheScroll checks that giving the focus back to
// "older" after its read leaves the page where it was: the new rows come in
// below, the page does not scroll to the moved button.
func TestPageTimelineRefocusKeepsTheScroll(t *testing.T) {
	e := timelineOpen(t, desktop, timelinePaged(), timelineShown())
	e.must(e.expect(timelineRows).ToHaveCount(50))

	read := e.hold(timelineOlderRead + "4271")
	e.must(e.Page.Locator(timelineOlder).Click())
	e.must(e.expect(timelineOlder).ToBeDisabled())
	before := e.timelineScrollY()
	read.release()
	e.must(e.expect(timelineRows).ToHaveCount(100))
	e.must(e.expect(timelineOlder).ToBeFocused())
	if after := e.timelineScrollY(); !timelineNear(after, before) {
		t.Errorf("page scrolled from %.0f to %.0f px when older took the focus back, want it left where it was", before, after)
	}
}

// TestPageTimelineOlderLeavesTheClickElsewhere checks that a user who
// clicks elsewhere while "older" reads, even on content that takes no focus,
// does not get the focus pulled back to "older".
func TestPageTimelineOlderLeavesTheClickElsewhere(t *testing.T) {
	e := timelineOpen(t, desktop, timelinePaged(), timelineShown())
	e.must(e.expect(timelineRows).ToHaveCount(50))

	read := e.hold(timelineOlderRead + "4271")
	e.must(e.Page.Locator(timelineOlder).Click())
	e.must(e.expect(timelineOlder).ToBeDisabled())
	e.must(e.Page.Locator("#page-title").Click())
	read.release()
	e.must(e.expect(timelineRows).ToHaveCount(100))
	e.must(e.expect(timelineOlder).Not().ToBeFocused())
}

// TestPageTimelineOlderLeavesTheFocusMoved checks that "older" takes back
// the focus its read's disabling dropped only while nothing else has it: a
// user who moved on stays where they went.
func TestPageTimelineOlderLeavesTheFocusMoved(t *testing.T) {
	e := timelineOpen(t, desktop, timelinePaged(), timelineShown())
	e.must(e.expect(timelineRows).ToHaveCount(50))

	read := e.hold(timelineOlderRead + "4271")
	e.must(e.Page.Locator(timelineOlder).Press("Enter"))
	e.must(e.expect(timelineOlder).ToBeDisabled())
	e.must(e.Page.Locator(validatorsLiveToggle).Focus())
	read.release()
	e.must(e.expect(timelineRows).ToHaveCount(100))
	e.must(e.expect(validatorsLiveToggle).ToBeFocused())
}

// TestPageTimelineOlderDisabledWhileLoading checks that "older" is disabled,
// and looks it, until its page answers.
func TestPageTimelineOlderDisabledWhileLoading(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := timelineOpen(t, p, timelinePaged(), timelineShown())
		e.must(e.expect(timelineRows).ToHaveCount(50))

		read := e.hold(timelineOlderRead + "4271")
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineOlder).ToBeDisabled())
		e.must(e.expect(timelineOlder).ToHaveCSS("opacity", "0.4"))
		read.release()
		e.must(e.expect(timelineRows).ToHaveCount(100))
		e.must(e.expect(timelineOlder).ToBeEnabled())
		e.must(e.expect(timelineOlder).ToHaveCSS("opacity", "1"))
	})
}

// TestPageTimelineRefreshRereadsOlderPages checks that a poll reads every
// page shown again: a change of an older page whose events were read since
// shows them read, and a change at the top shifts the pages, no change shown
// twice or missing.
func TestPageTimelineRefreshRereadsOlderPages(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		const pending = 4205 // a change of the third page
		tl := timelinePaged()
		seed := tl.sc.seed
		tl.sc.seed = func(t *testing.T, s *blocks.Store) {
			seed(t, s)
			if err := s.AddEventGap(t.Context(), pending); err != nil {
				t.Fatal(err)
			}
		}
		e := timelineOpen(t, p, tl, timelineShown(), withInstalledClock())
		e.must(e.expect(timelineRows).ToHaveCount(50))
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineRows).ToHaveCount(100))
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineRows).ToHaveCount(122))
		row := e.Page.Locator(timelineRows).Nth(pageHeight - pending)
		e.must(e.Expect.Locator(row.Locator(".h")).ToHaveText(timelineHeightRow(pending)))
		e.must(e.Expect.Locator(row.Locator(".vt-pend")).ToHaveCount(1))

		if err := e.Srv.Blocks.PutEvents(t.Context(), pending, pending-2, nil); err != nil {
			t.Fatal(err)
		}
		timelinePutFlips(t, e.Srv.Blocks, pageHeight+1, pageHeight+1)
		tl.fill.set(timelineProgress(1, pageHeight+1, 0))
		e.runFor(timelinePoll)
		e.must(timelineTopAge(e).ToHaveText(timelineHeightRow(pageHeight + 1)))
		e.must(e.expect(timelineRows).ToHaveCount(123))
		e.must(e.expect(timelineRows + " .vt-pend").ToHaveCount(0))

		timelineExpectPagedHeights(e, pageHeight+1)
	})
}

// TestPageTimelinePollKeepsThePagesRead checks that a poll keeps every row
// the user paged to, and only those: a change at the top pushes the last row
// read onto the page below, which the poll reads down to that row.
func TestPageTimelinePollKeepsThePagesRead(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		tl := timelinePaged()
		e := timelineOpen(t, p, tl, timelineShown(), withInstalledClock())
		last := e.Expect.Locator(e.Page.Locator(timelineAges).Last())
		e.must(e.expect(timelineRows).ToHaveCount(50))
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineRows).ToHaveCount(100))
		e.must(last.ToHaveText(timelineHeightRow(4221)))

		timelinePutFlips(t, e.Srv.Blocks, pageHeight+1, pageHeight+1)
		tl.fill.set(timelineProgress(1, pageHeight+1, 0))
		e.runFor(timelinePoll)
		e.must(timelineTopAge(e).ToHaveText(timelineHeightRow(pageHeight + 1)))
		e.must(e.expect(timelineRows).ToHaveCount(101))
		e.must(last.ToHaveText(timelineHeightRow(4221)))
		e.must(e.expect(timelineOlder).ToBeVisible())
	})
}

// TestPageTimelineNewChangesAddTheirRows checks that the changes polls find
// at the top add their own rows and no more: the rows below stay those the
// user paged to, "older" still offered.
func TestPageTimelineNewChangesAddTheirRows(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		tl := timelinePaged()
		e := timelineOpen(t, p, tl, timelineShown(), withInstalledClock())
		last := e.Expect.Locator(e.Page.Locator(timelineAges).Last())
		e.must(e.expect(timelineRows).ToHaveCount(50))
		for n := int64(1); n <= 3; n++ {
			timelinePutFlips(t, e.Srv.Blocks, pageHeight+n, pageHeight+n)
			tl.fill.set(timelineProgress(1, pageHeight+n, 0))
			e.runFor(timelinePoll)
			e.must(timelineTopAge(e).ToHaveText(timelineHeightRow(pageHeight + n)))
			e.must(e.expect(timelineRows).ToHaveCount(50 + int(n)))
			e.must(last.ToHaveText(timelineHeightRow(4271)))
			e.must(e.expect(timelineOlder).ToBeVisible())
		}
	})
}

// TestPageTimelineRefilledHistoryShows checks a history the server dropped
// and refilled between two polls, wholly below the rows shown: the poll shows
// the new history's first page.
func TestPageTimelineRefilledHistoryShows(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		tl := timelinePaged()
		e := timelineOpen(t, p, tl, timelineShown(), withInstalledClock())
		e.must(e.expect(timelineRows).ToHaveCount(50))

		const chain = pageChainID + "-next"
		if err := e.Srv.Blocks.Reset(t.Context(), chain); err != nil {
			t.Fatal(err)
		}
		timelinePutFlips(t, e.Srv.Blocks, 1000, 1100)
		refilled := timelineProgress(1000, 1100, 0)
		refilled.ChainID = chain
		tl.fill.set(refilled)
		e.runFor(timelinePoll)
		e.must(timelineTopAge(e).ToHaveText(timelineHeightRow(1100)))
		e.must(e.expect(timelineRows).ToHaveCount(50))
	})
}

// TestPageTimelineOlderWhileFilling checks "older" while the history fills
// down: it stays offered below the last change stored; the polls keep the
// rows shown and the footer's reach, and the changes the fill adds below come
// with "older"; it goes once the history is complete.
func TestPageTimelineOlderWhileFilling(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		tl := newTimelineScenario(timelineProgress(4200, pageHeight, 0), func(t *testing.T, s *blocks.Store) {
			timelinePutFlips(t, s, 4200, pageHeight)
		})
		e := timelineOpen(t, p, tl, timelineShown(), withInstalledClock())
		last := e.Expect.Locator(e.Page.Locator(timelineAges).Last())
		reach := e.expect(timelineFooter + " span")
		e.must(e.expect(timelineRows).ToHaveCount(50))
		e.must(reach.ToHaveText("history reaches back to #4,200: older changes appear as it fills"))
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineRows).ToHaveCount(100))
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineRows).ToHaveCount(120))
		e.must(last.ToHaveText(timelineHeightRow(4201)))
		e.must(e.expect(timelineOlder).ToBeVisible())

		read := e.hold(timelineOlderRead + "4201")
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineOlder).ToBeDisabled())
		read.release()
		e.must(e.expect(timelineOlder).ToBeEnabled())
		e.must(e.expect(timelineRows).ToHaveCount(120))

		timelinePutFlips(t, e.Srv.Blocks, 4150, 4199)
		tl.fill.set(timelineProgress(4150, pageHeight, 0))
		e.runFor(timelinePoll)
		e.must(reach.ToHaveText("history reaches back to #4,150: older changes appear as it fills"))
		e.must(e.expect(timelineRows).ToHaveCount(120))
		e.must(last.ToHaveText(timelineHeightRow(4201)))
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineRows).ToHaveCount(170))
		e.must(last.ToHaveText(timelineHeightRow(4151)))
		e.must(e.expect(timelineOlder).ToBeEnabled())

		timelinePutEras(t, e.Srv.Blocks, 4149, timelineEra{1, timelineGenesisSet()})
		tl.fill.set(timelineProgress(1, pageHeight, 0))
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineRows).ToHaveCount(172))
		e.must(last.ToHaveText("#1 · 3 d ago"))
		e.must(e.expect(timelineOlder).ToHaveCount(0))
		e.must(reach.ToHaveText(""))
	})
}

// TestPageTimelineOlderDuringRefresh checks "older" clicked while a poll
// reads the pages shown: its read waits for the poll's, then adds the page
// below the rows the poll shows, which no poll then drops.
func TestPageTimelineOlderDuringRefresh(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		tl := timelinePaged()
		e := timelineOpen(t, p, tl, timelineShown(), withInstalledClock())
		e.must(e.expect(timelineRows).ToHaveCount(50))
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineRows).ToHaveCount(100))

		timelinePutFlips(t, e.Srv.Blocks, pageHeight+1, pageHeight+1)
		tl.fill.set(timelineProgress(1, pageHeight+1, 0))
		// The poll's read of its second page, below its first page's last
		// change, 4,272.
		second := e.hold(timelineOlderRead + "4272")
		e.runFor(timelinePoll)
		timelineActivate(e, timelineOlder)
		e.must(e.expect(timelineOlder).ToBeDisabled())
		second.release()
		e.must(timelineTopAge(e).ToHaveText(timelineHeightRow(pageHeight + 1)))
		e.must(e.expect(timelineRows).ToHaveCount(123))
		e.must(e.expect(timelineOlder).ToHaveCount(0))
		timelineExpectPagedHeights(e, pageHeight+1)
	})
}

// TestPageTimelineEmptyFirstPageClearsTheList checks that the rows go once
// the history no longer holds them, as when the server drops it for another
// chain, and that the next history's changes show alone.
func TestPageTimelineEmptyFirstPageClearsTheList(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		tl := timelineRich()
		e := timelineOpen(t, p, tl, timelineShown(), withInstalledClock())
		e.must(e.expect(timelineRows).ToHaveCount(len(timelineRichRows())))

		const chain = pageChainID + "-next"
		if err := e.Srv.Blocks.Reset(t.Context(), chain); err != nil {
			t.Fatal(err)
		}
		tl.fill.set(blocks.Progress{ChainID: chain})
		e.runFor(timelinePoll)
		e.must(e.expect(timelineList + " .vt-foot").ToHaveText([]string{"No validator set change in the history yet", ""}))
		e.must(e.expect(timelineRows).ToHaveCount(0))

		timelinePutFlips(t, e.Srv.Blocks, 4310, pageHeight)
		refilled := timelineProgress(4310, pageHeight, 0)
		refilled.ChainID = chain
		tl.fill.set(refilled)
		e.runFor(timelinePoll)
		e.must(timelineTopAge(e).ToHaveText(timelineHeightRow(pageHeight)))
		e.must(e.expect(timelineRows).ToHaveCount(10))
	})
}

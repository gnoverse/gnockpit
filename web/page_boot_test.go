//go:build page

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/web/icon"
)

const (
	themeColorMeta = `meta[name="theme-color"]`
	// linksReadBound is how long a read of the header links may take.
	linksReadBound = 10 * time.Second
)

// TestPageHead checks the document head: language, viewport, the title and
// theme colour before any chain data, and the linked manifest and icons.
func TestPageHead(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, bootScenario())
		e.must(e.expect("#boot-panel").ToBeVisible())

		e.must(e.expect("html").ToHaveAttribute("lang", "en"))
		e.must(e.expect("meta[charset]").ToHaveAttribute("charset", "utf-8"))
		e.must(e.expect(`meta[name="viewport"]`).ToHaveAttribute("content", "width=device-width, initial-scale=1"))
		e.must(e.Expect.Page(e.Page).ToHaveTitle("gnockpit"))
		e.must(e.expect("#page-title").ToHaveText("Gnockpit"))
		e.must(e.expect(themeColorMeta).ToHaveAttribute("content", "#1a2332"))

		for _, link := range []struct{ selector, contentType string }{
			{`link[rel="manifest"]`, "application/json"},
			{`link[rel="icon"][type="image/svg+xml"]`, "image/svg+xml"},
			{`link[rel="icon"][type="image/png"]`, "image/png"},
			{`link[rel="apple-touch-icon"]`, "image/png"},
		} {
			href, err := e.Page.Locator(link.selector).GetAttribute("href")
			if err != nil {
				t.Fatalf("%s: %v", link.selector, err)
			}
			resp, err := e.Ctx.Request().Get(e.ts.URL + href)
			if err != nil {
				t.Fatalf("GET %s: %v", href, err)
			}
			if resp.Status() != http.StatusOK {
				t.Errorf("GET %s (%s) = %d, want %d", href, link.selector, resp.Status(), http.StatusOK)
			}
			if got := resp.Headers()["content-type"]; !strings.HasPrefix(got, link.contentType) {
				t.Errorf("GET %s content type = %q, want %s", href, got, link.contentType)
			}
			if err := resp.Dispose(); err != nil {
				t.Errorf("GET %s: %v", href, err)
			}
		}
	})
}

// TestPageChainTitle checks the chain's names: the chain-id row reads the
// chain id, the title and heading read the display name (--chain-name, else
// the chain id), and the theme colour is the one Go derives from it for the
// icons.
func TestPageChainTitle(t *testing.T) {
	for _, tc := range []struct{ name, chainName, display string }{
		{name: "chain id", display: pageChainID},
		{name: "chain name", chainName: "test13", display: "test13"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				sc := healthyNetwork()
				sc.chainName = tc.chainName
				e := openPage(t, p, sc)
				e.must(e.expect("#d-network").ToHaveText(pageChainID))
				e.must(e.Expect.Page(e.Page).ToHaveTitle("Gnockpit " + tc.display))
				e.must(e.expect("#page-title").ToHaveText("Gnockpit " + tc.display))
				e.must(e.expect(themeColorMeta).ToHaveAttribute("content", icon.ChainColor(tc.display).BG))
			})
		})
	}
}

// TestPageChainTitleWithoutChainID checks a status without a chain id: the
// chain-id row reads "-" and the title, heading and theme colour keep their
// defaults.
func TestPageChainTitleWithoutChainID(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, bootScenario())
		e.must(e.expect("#boot-panel").ToBeVisible())

		snap := healthySnapshot()
		snap.Status.NodeInfo.Network = ""
		e.publish(snap)
		e.must(e.expect("#d-block").ToHaveText(strconv.Itoa(pageHeight)))
		e.must(e.expect("#d-network").ToHaveText("-"))
		e.must(e.Expect.Page(e.Page).ToHaveTitle("gnockpit"))
		e.must(e.expect("#page-title").ToHaveText("Gnockpit"))
		e.must(e.expect(themeColorMeta).ToHaveAttribute("content", "#1a2332"))
	})
}

// TestPageTitleUsesChainNameFirst checks that with --chain-name set the page
// never titles itself with the chain id, even when the first snapshot comes
// before /api/boot's answer: the title waits for it.
func TestPageTitleUsesChainNameFirst(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := healthyNetwork()
		sc.chainName = "test13"
		e := openPage(t, p, sc, noNavigate(), withInstalledClock())
		boot := e.hold("/api/boot")
		e.navigate("/")

		e.must(e.expect("#d-network").ToHaveText(pageChainID))
		e.must(e.Expect.Page(e.Page).ToHaveTitle("gnockpit"))
		e.must(e.expect("#page-title").ToHaveText("Gnockpit"))
		e.must(e.expect(themeColorMeta).ToHaveAttribute("content", "#1a2332"))

		boot.release()
		e.must(e.Expect.Page(e.Page).ToHaveTitle("Gnockpit test13"))
		e.must(e.expect("#page-title").ToHaveText("Gnockpit test13"))
		e.must(e.expect(themeColorMeta).ToHaveAttribute("content", icon.ChainColor("test13").BG))
	})
}

// TestPageTitleUsesChainIDWhenBootFails checks that a failed /api/boot
// read does not leave the page untitled: the title falls back to the chain
// id, and takes the chain name once a read answers.
func TestPageTitleUsesChainIDWhenBootFails(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := healthyNetwork()
		sc.chainName = "test13"
		e := openPage(t, p, sc, noNavigate(), withInstalledClock())
		failed := e.hold("/api/boot")
		failed.drop()
		e.navigate("/")
		failed.awaitFailure()

		e.must(e.expect("#d-network").ToHaveText(pageChainID))
		e.must(e.Expect.Page(e.Page).ToHaveTitle("Gnockpit " + pageChainID))
		e.must(e.expect("#page-title").ToHaveText("Gnockpit " + pageChainID))
		e.must(e.expect(themeColorMeta).ToHaveAttribute("content", icon.ChainColor(pageChainID).BG))

		e.runFor(readBootEvery)
		e.must(e.Expect.Page(e.Page).ToHaveTitle("Gnockpit test13"))
		e.must(e.expect("#page-title").ToHaveText("Gnockpit test13"))
		e.must(e.expect(themeColorMeta).ToHaveAttribute("content", icon.ChainColor("test13").BG))
	})
}

// TestPageBootPollRecovers checks that the boot panel follows the sources
// for as long as the page is open: shown while the server has no chain data,
// hidden once it has, shown again when every source goes down, through a
// failed read, and hidden again when they come back.
func TestPageBootPollRecovers(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, bootScenario(), withInstalledClock())
		panel := e.expect("#boot-panel")
		e.must(panel.ToBeVisible())
		e.must(panel.ToContainText("Node booting — RPC not ready"))
		e.must(e.expect("#boot-info").ToHaveText("waiting..."))

		e.publish(healthySnapshot())
		e.runFor(readBootEvery)
		e.must(panel.ToBeHidden())

		e.publish(sourcesDown())
		e.runFor(readBootEvery)
		e.must(panel.ToBeVisible())
		e.must(e.expect("#boot-info").ToHaveText("waiting..."))

		failed := e.hold("/api/boot")
		failed.drop()
		e.runFor(readBootEvery)
		failed.awaitFailure()
		e.must(panel.ToBeVisible())

		e.publish(healthySnapshot())
		e.runFor(readBootEvery)
		e.must(panel.ToBeHidden())
	})
}

// TestPageBootReadIsBounded checks that a /api/boot read that does not
// answer is given up after 4 s, changing nothing, and that the next tick
// reads again.
func TestPageBootReadIsBounded(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork(), withInstalledClock())
		e.must(e.Expect.Page(e.Page).ToHaveTitle("Gnockpit " + pageChainID))
		panel := e.expect("#boot-panel")
		tick := func(d time.Duration) func() error {
			return func() error {
				e.runFor(d)
				return nil
			}
		}

		e.publish(sourcesDown())
		slow := e.hold("/api/boot")
		if _, err := e.Page.ExpectRequest("**/api/boot", tick(readBootEvery)); err != nil {
			t.Fatalf("the tick's read: %v", err)
		}
		e.runFor(readBootWithin)
		slow.awaitAbandoned()
		e.must(panel.ToBeHidden())

		if _, err := e.Page.ExpectRequest("**/api/boot", tick(readBootEvery-readBootWithin)); err != nil {
			t.Fatalf("the next tick's read: %v", err)
		}
		e.must(panel.ToBeVisible())
	})
}

// headerLinkState is a status link's state and the colour of its dot.
type headerLinkState struct{ title, state, color string }

// headerLinkStates covers every colour of a status dot: the page's --green,
// --red, --yellow and --fg2.
var headerLinkStates = []headerLinkState{
	{"Up", "operational", "rgb(63, 185, 80)"},
	{"Down", "downtime", "rgb(248, 81, 73)"},
	{"Slow", "degraded", "rgb(210, 153, 34)"},
	{"Works", "maintenance", "rgb(210, 153, 34)"},
	{"Unknown", "unknown", "rgb(139, 148, 158)"},
}

// headerLinksScenario serves two plain links and a status link per state of
// headerLinkStates.
func headerLinksScenario() *scenario {
	sc := bootScenario()
	sc.links = []Link{
		{Title: "Docs", URL: "https://docs.example.org/"},
		{Title: "Explorer", URL: "https://explorer.example.org/"},
	}
	for _, s := range headerLinkStates {
		sl := NewStatusLink(Link{Title: s.title, URL: "https://status.example.org/" + strings.ToLower(s.title)})
		sl.setState(s.state)
		sc.statusLinks = append(sc.statusLinks, sl)
	}
	return sc
}

// TestPageHeaderLinks checks the header links: plain links first, then the
// status links, each opening its URL in a new tab, a status link's dot
// coloured by its state and naming it.
func TestPageHeaderLinks(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := headerLinksScenario()
		e := openPage(t, p, sc)

		const links = "#header-links a.header-link"
		var titles, hrefs []string
		for _, l := range sc.links {
			titles, hrefs = append(titles, l.Title), append(hrefs, l.URL)
		}
		for _, sl := range sc.statusLinks {
			titles, hrefs = append(titles, sl.Title), append(hrefs, sl.URL)
		}
		e.must(e.expect(links).ToHaveText(titles))
		for i, href := range hrefs {
			a := e.Expect.Locator(e.Page.Locator(links).Nth(i))
			e.must(a.ToHaveAttribute("href", href))
			e.must(a.ToHaveAttribute("target", "_blank"))
			e.must(a.ToHaveAttribute("rel", "noopener"))
		}

		e.must(e.expect(links + " .dot").ToHaveCount(len(headerLinkStates)))
		for i, s := range headerLinkStates {
			dot := e.Expect.Locator(e.Page.Locator(links).Nth(len(sc.links) + i).Locator(".dot"))
			e.must(dot.ToHaveCSS("background-color", s.color))
			e.must(dot.ToHaveAttribute("data-popover", s.state))
		}
	})
}

// TestPageHeaderLinksRefresh checks that the header links are read again
// every 30 s, and that a failed read keeps the links shown and the reads
// going.
func TestPageHeaderLinksRefresh(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := headerLinksScenario()
		e := openPage(t, p, sc, withInstalledClock())
		up := sc.statusLinks[0]
		dot := e.Expect.Locator(e.Page.Locator("#header-links a.header-link").Nth(len(sc.links)).Locator(".dot"))
		e.must(dot.ToHaveAttribute("data-popover", "operational"))

		up.setState("downtime")
		e.runFor(readLinksEvery)
		e.must(dot.ToHaveAttribute("data-popover", "downtime"))
		e.must(dot.ToHaveCSS("background-color", headerLinkStates[1].color))

		up.setState("operational")
		failed := e.hold("/api/links")
		failed.drop()
		e.runFor(readLinksEvery)
		failed.awaitFailure()
		e.must(e.expect("#header-links a.header-link").ToHaveCount(len(sc.links) + len(sc.statusLinks)))
		e.must(dot.ToHaveAttribute("data-popover", "downtime"))

		e.runFor(readLinksEvery)
		e.must(dot.ToHaveAttribute("data-popover", "operational"))
		e.must(dot.ToHaveCSS("background-color", headerLinkStates[0].color))
	})
}

// TestPageHeaderLinksReadIsBounded checks that a read of the header links
// gnockpit does not answer is given up after 10 s, the links kept as they
// show, and that the next poll reads them again.
func TestPageHeaderLinksReadIsBounded(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := headerLinksScenario()
		e := openPage(t, p, sc, withInstalledClock())
		up := sc.statusLinks[0]
		dot := e.Expect.Locator(e.Page.Locator("#header-links a.header-link").Nth(len(sc.links)).Locator(".dot"))
		e.must(dot.ToHaveAttribute("data-popover", "operational"))

		up.setState("downtime")
		stuck := e.hold("/api/links")
		e.runFor(readLinksEvery)
		e.must(e.gate.awaitHeld("/api/links", 1))
		e.runFor(linksReadBound)
		stuck.awaitAbandoned()
		e.must(e.expect("#header-links a.header-link").ToHaveCount(len(sc.links) + len(sc.statusLinks)))
		e.must(dot.ToHaveAttribute("data-popover", "operational"))

		e.runFor(readLinksEvery - linksReadBound)
		e.must(dot.ToHaveAttribute("data-popover", "downtime"))
	})
}

// TestPageHeaderStatusDotPopover checks that hovering a status dot shows its
// state.
func TestPageHeaderStatusDotPopover(t *testing.T) {
	e := openPage(t, desktop, headerLinksScenario())
	e.must(e.Page.Locator("#header-links a.header-link .dot").First().Hover())
	e.must(e.expect("#popover").ToBeVisible())
	e.must(e.expect("#popover").ToHaveText("operational"))
}

// TestPageHeaderStatusDotPopoverFollowsARefresh checks that the read of the
// header links every 30 s, which draws them again, never leaves a status
// dot's popover showing a state the dot no longer has.
func TestPageHeaderStatusDotPopoverFollowsARefresh(t *testing.T) {
	onProfiles(t, []profile{desktop, desktopWebKit}, func(t *testing.T, p profile) {
		sc := headerLinksScenario()
		e := openPage(t, p, sc, withInstalledClock())
		dot := e.Page.Locator("#header-links a.header-link .dot").First()
		e.must(dot.Hover())
		e.must(e.expect("#popover").ToHaveText("operational"))

		// The popover as each redraw of the links leaves it, recorded before
		// the pointer resting on the new dot can open the new dot's popover,
		// which would replace a stale one before any assertion saw it. The
		// record is queued after the page's own handling of the same
		// mutations, which hides a popover whose target left.
		states, err := e.Page.EvaluateHandle(`() => {
			const states = []
			new MutationObserver(() => queueMicrotask(() => {
				const pop = document.getElementById('popover')
				states.push(pop.classList.contains('visible') ? pop.textContent : '')
			})).observe(document.getElementById('header-links'), { childList: true })
			return states
		}`)
		e.must(err)

		sc.statusLinks[0].setState("downtime")
		e.runFor(readLinksEvery)
		e.must(e.Expect.Locator(dot).ToHaveAttribute("data-popover", "downtime"))
		v, err := states.JSONValue()
		e.must(err)
		seen, _ := v.([]any)
		if len(seen) == 0 {
			t.Fatal("the links were not drawn again")
		}
		if slices.Contains(seen, any("operational")) {
			t.Errorf("popover at each redraw = %q: it shows the state the dot had before the links were read again", seen)
		}
	})
}

// TestPageHeaderStatusDotTapFollowsLink checks that on a touch screen the
// status dot is no popover target: a tap on it opens the status page.
func TestPageHeaderStatusDotTapFollowsLink(t *testing.T) {
	statusPage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!doctype html><title>Status</title>")
	}))
	t.Cleanup(statusPage.Close)
	sc := bootScenario()
	up := NewStatusLink(Link{Title: "Up", URL: statusPage.URL + "/up"})
	up.setState("operational")
	sc.statusLinks = []*StatusLink{up}
	e := openPage(t, phone, sc)

	popup, err := e.Page.ExpectPopup(func() error {
		return e.Page.Locator("#header-links a.header-link .dot").Tap()
	})
	if err != nil {
		t.Fatalf("tap the status dot: %v", err)
	}
	e.must(e.Expect.Page(popup).ToHaveURL(up.URL))
	e.must(e.expect("#popover").ToBeHidden())
}

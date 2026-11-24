//go:build page

package web

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mxschmitt/playwright-go"

	"github.com/gnoverse/gnockpit/node"
)

const (
	mapCard = "#map-card"
	mapDots = "#map-points .map-dot"
	mapRows = "#map-country-list .country-row"

	// Colours of the map: green peers, blue validators.
	mapPeerGreen = "rgb(63, 185, 80)"
	mapValBlue   = "rgb(88, 166, 255)"
)

// mapLocated is p placed at lat, lon in country, hosted by provider.
func mapLocated(p node.Peer, lat, lon float64, country, provider string) node.Peer {
	p.Lat, p.Lon, p.Country, p.Provider = float64Ptr(lat), float64Ptr(lon), country, provider
	return p
}

// mapScenario is a server whose snapshot has peers as the source's peers.
func mapScenario(peers ...node.Peer) *scenario {
	sc := bootScenario()
	sc.snap = mapSnapshot(peers...)
	return sc
}

// mapSnapshot is the healthy snapshot with peers as the source's peers.
func mapSnapshot(peers ...node.Peer) *node.Snapshot {
	snap := healthySnapshot()
	snap.Peers = peers
	return snap
}

// TestPageMapZeroCoordinates checks that a peer located on the equator or on
// the prime meridian is drawn where it is. The server sends a latitude only
// with a longitude, and never a peer at exactly (0,0), which it reads as
// DB-IP's missing location: the page's handling of either is left untested.
func TestPageMapZeroCoordinates(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, mapScenario(
			mapLocated(fullPeer("equator"), 0, -78.5, "EC", ""),
			mapLocated(fullPeer("meridian"), 51.48, 0, "GB", ""),
		))
		e.must(e.expect(mapCard).ToBeVisible())
		e.must(e.expect(mapDots).ToHaveCount(2))
		for _, d := range []struct{ sel, cx, cy string }{
			{mapDots + `[data-cc="EC"]`, "101.50", "90.00"},
			{mapDots + `[data-cc="GB"]`, "180.00", "38.52"},
		} {
			e.must(e.expect(d.sel).ToHaveAttribute("cx", d.cx))
			e.must(e.expect(d.sel).ToHaveAttribute("cy", d.cy))
		}
	})
}

// TestPageMapHiddenUntilLocated checks that the map card stays hidden while
// the peers it would show have no location, shows once one is located and
// hides again when none is.
func TestPageMapHiddenUntilLocated(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		unlocated := []node.Peer{fullPeer("eu-node"), fullPeer("us-node")}
		e := openPage(t, p, mapScenario(unlocated...))
		e.must(e.expect("#d-block").ToHaveText("4320"))
		e.must(e.expect(mapCard).ToBeHidden())

		e.publish(mapSnapshot(mapLocated(fullPeer("eu-node"), 50.11, 8.68, "DE", "Hetzner"), fullPeer("us-node")))
		e.must(e.expect(mapCard).ToBeVisible())
		e.must(e.expect(mapDots).ToHaveCount(1))

		e.publish(mapSnapshot(unlocated...))
		e.must(e.expect(mapCard).ToBeHidden())
	})
}

// mapWorldPeers are peers spread over the world. Located: three DE peers on
// Hetzner and OVH, in Frankfurt and Berlin, close enough to cluster, beside
// bravo, a DE validator in Frankfurt; charlie and jp-val, two JP validators
// in Tokyo, one known by its role only; syd in Sydney (AU, OVH), us-a in
// Ashburn (US, Vultr) and anon in São Paulo with no country or provider.
// Not located: full-norpc on Hetzner, and sentry-1, a source.
func mapWorldPeers() []node.Peer {
	validator := func(p node.Peer, v pageValidator) node.Peer {
		p.Role, p.ValAddress = "val", v.address()
		return p
	}
	jpVal := fullPeer("jp-val")
	jpVal.Role = "val?"
	noRPC := fullPeer("full-norpc")
	noRPC.Provider = "Hetzner"
	sentry := fullPeer("sentry-1")
	sentry.Source = true
	return []node.Peer{
		mapLocated(fullPeer("anon"), -23.55, -46.63, "", ""),
		mapLocated(fullPeer("ber-a"), 52.52, 13.40, "DE", "OVH"),
		mapLocated(validator(fullPeer("bravo"), valBravo), 50.11, 8.68, "DE", "Hetzner"),
		mapLocated(validator(fullPeer("charlie"), valCharlie), 35.68, 139.69, "JP", "AWS"),
		mapLocated(fullPeer("fra-a"), 50.11, 8.68, "DE", "Hetzner"),
		mapLocated(fullPeer("fra-b"), 50.11, 8.68, "DE", "Hetzner"),
		noRPC,
		mapLocated(jpVal, 35.68, 139.69, "JP", "AWS"),
		sentry,
		mapLocated(fullPeer("syd"), -33.87, 151.21, "AU", "OVH"),
		mapLocated(fullPeer("us-a"), 39.04, -77.49, "US", "Vultr"),
	}
}

// mapPressMode switches the side list and the map to mode, "Country" or
// "Provider": a click, or a tap on a touch screen.
func mapPressMode(e *pageEnv, mode string) {
	e.T.Helper()
	e.press(e.Page.Locator("#map-list-toggle").GetByRole("button", playwright.LocatorGetByRoleOptions{Name: mode}))
}

// mapAttrs asserts the value of attribute name on each element selector
// matches, in order.
func mapAttrs(e *pageEnv, selector, name string, want ...string) {
	e.T.Helper()
	e.must(e.expect(selector).ToHaveCount(len(want)))
	for i, v := range want {
		e.must(e.Expect.Locator(e.Page.Locator(selector).Nth(i)).ToHaveAttribute(name, v))
	}
}

// TestPageMapCard checks the map card: hidden until the peers arrive, then
// its title, the world map, the legend, the list and the DB-IP credit.
func TestPageMapCard(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, bootScenario())
		e.must(e.expect(mapCard).ToBeHidden())

		e.publish(mapSnapshot(mapWorldPeers()...))
		e.must(e.expect(mapCard).ToBeVisible())
		e.must(e.expect("#map-header").ToHaveText("Network Map"))
		svg := e.expect("svg.world-map")
		e.must(svg.ToHaveAttribute("viewBox", "0 0 360 180"))
		e.must(svg.ToHaveAttribute("aria-label", "World map of peer locations"))
		e.must(e.expect("svg.world-map path.world-land").ToHaveCount(1))
		e.must(e.expect(".map-legend .map-legend-row").ToHaveText([]string{"Validators", "Peers"}))
		e.must(e.expect(".map-legend .map-legend-val").ToHaveCSS("background-color", mapValBlue))
		e.must(e.expect(".map-legend .map-legend-peer").ToHaveCSS("background-color", mapPeerGreen))
		e.must(e.expect("#map-country-list").ToHaveAttribute("aria-label", "Peers by country or provider"))
		credit := e.Expect.Locator(e.Page.Locator(mapCard).GetByRole("link", playwright.LocatorGetByRoleOptions{Name: "IP Geolocation by DB-IP"}))
		e.must(credit.ToHaveAttribute("href", "https://db-ip.com"))
		e.must(credit.ToHaveAttribute("target", "_blank"))
		e.must(credit.ToHaveAttribute("rel", "noopener"))
	})
}

// TestPageMapClusters checks the clusters: one per city, type and country,
// at x = lon + 180 and y = 90 − lat, sized by their count and labelled with
// it above one; a group's clusters that touch merge across cities; clusters
// that still overlap are pushed apart, each tied to where it belongs by a
// leader line; a located peer with no country is drawn untagged. In
// Provider mode the clusters group by provider. Every update redraws them.
func TestPageMapClusters(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, mapScenario(mapWorldPeers()...))
		e.must(e.expect(mapDots).ToHaveCount(6))

		dot := func(sel, class, cx, cy, r string) {
			t.Helper()
			d := e.expect(sel)
			e.must(d.ToHaveAttribute("class", class))
			e.must(d.ToHaveAttribute("cx", cx))
			e.must(d.ToHaveAttribute("cy", cy))
			e.must(d.ToHaveAttribute("r", r))
		}
		dot(mapDots+`[data-cc="US"]`, "map-dot", "102.51", "50.96", "2.15")
		dot(mapDots+`[data-cc="AU"]`, "map-dot", "331.21", "123.87", "2.15")
		dot(mapDots+`:not([data-cc])`, "map-dot", "133.37", "113.55", "2.15")
		dot(mapDots+`[data-cc="JP"]`, "map-dot is-val", "319.69", "54.32", "2.46")
		e.must(e.expect(mapDots+`[data-cc="US"]`).ToHaveCSS("fill", mapPeerGreen))
		e.must(e.expect(mapDots+`[data-cc="JP"]`).ToHaveCSS("fill", mapValBlue))
		jpCount := e.expect(`#map-points text.map-count[data-cc="JP"]`)
		e.must(jpCount.ToHaveText("2"))
		e.must(jpCount.ToHaveAttribute("x", "319.69"))
		e.must(jpCount.ToHaveAttribute("y", "54.32"))
		e.must(jpCount.ToHaveAttribute("font-size", "2.34"))
		mapAttrs(e, "#map-points text.map-count", "data-cc", "DE", "JP")

		// Frankfurt's and Berlin's DE peers merge into one cluster of three;
		// bravo, a DE validator in Frankfurt, is a cluster of its own. The
		// two overlap, so each is pushed off its anchor, to which a line
		// leads back.
		dePeers := mapDots + `:not(.is-val)[data-cc="DE"]`
		deVal := mapDots + `.is-val[data-cc="DE"]`
		e.must(e.expect(dePeers).ToHaveAttribute("r", "2.70"))
		e.must(e.expect(`#map-points text.map-count[data-cc="DE"]`).ToHaveText("3"))
		e.must(e.expect(deVal).ToHaveAttribute("r", "2.15"))
		e.must(e.expect(`#map-points circle.map-anchor[data-cc="DE"]`).ToHaveCount(2))
		for _, l := range []struct{ line, dot, x1, y1 string }{
			{`#map-points line.map-leader-peer[data-cc="DE"]`, dePeers, "190.25", "39.09"},
			{`#map-points line.map-leader-val[data-cc="DE"]`, deVal, "188.68", "39.89"},
		} {
			line := e.expect(l.line)
			e.must(line.ToHaveAttribute("x1", l.x1))
			e.must(line.ToHaveAttribute("y1", l.y1))
			anchor := e.expect(`#map-points circle.map-anchor[data-cc="DE"][cx="` + l.x1 + `"][cy="` + l.y1 + `"]`)
			e.must(anchor.ToHaveAttribute("r", "0.5"))
			cx, err := e.Page.Locator(l.dot).GetAttribute("cx")
			e.must(err)
			cy, err := e.Page.Locator(l.dot).GetAttribute("cy")
			e.must(err)
			e.must(line.ToHaveAttribute("x2", cx))
			e.must(line.ToHaveAttribute("y2", cy))
			if cx == l.x1 && cy == l.y1 {
				t.Errorf("%s: at its anchor (%s, %s), want it pushed off", l.dot, cx, cy)
			}
		}
		e.must(e.expect("#map-points line").ToHaveCount(2))

		mapPressMode(e, "Provider")
		e.must(e.expect(`#map-points [data-cc]`).ToHaveCount(0))
		for prov, n := range map[string]int{"Hetzner": 2, "OVH": 2, "AWS": 1, "Vultr": 1} {
			e.must(e.expect(mapDots + `[data-prov="` + prov + `"]`).ToHaveCount(n))
		}
		e.must(e.expect(mapDots + `:not([data-prov])`).ToHaveCount(1))
		mapAttrs(e, "#map-points text.map-count", "data-prov", "AWS", "Hetzner")

		// An update redraws the map: us-a moves to Seattle.
		moved := mapWorldPeers()
		moved[len(moved)-1] = mapLocated(fullPeer("us-a"), 47.61, -122.33, "US", "Vultr")
		e.publish(mapSnapshot(moved...))
		us := e.expect(mapDots + `[data-prov="Vultr"]`)
		e.must(us.ToHaveAttribute("cx", "57.67"))
		e.must(us.ToHaveAttribute("cy", "42.39"))
	})
}

// TestPageMapPopover checks a cluster's popover: titled with its group and
// the group's located validators and peers, a row per branch: a country's
// providers, a provider's countries, most nodes first, then by name.
func TestPageMapPopover(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, mapScenario(mapWorldPeers()...))
		e.must(e.expect(mapDots).ToHaveCount(6))

		check := func(sel, title string, labels, values []string) {
			t.Helper()
			cluster := e.Page.Locator(sel)
			// A pointer opens the popover by hovering; a touch screen, by a
			// tap.
			if p.touch() {
				e.must(cluster.Tap())
			} else {
				e.must(cluster.Hover())
			}
			e.must(e.expect("#popover").ToBeVisible())
			e.must(e.expect("#popover .t").ToHaveText(title))
			e.must(e.expect("#popover td.l").ToHaveText(labels))
			e.must(e.expect("#popover td.v").ToHaveText(values))
			// On a touch screen the popover stays open, over the clusters
			// below it, until a second tap on its cluster closes it.
			if p.touch() {
				e.must(cluster.Tap())
				e.must(e.expect("#popover").ToBeHidden())
			}
		}
		de := []string{"☁️ Hetzner", "☁️ OVH"}
		deCounts := []string{"1 val · 2 peers", "1 peer"}
		check(mapDots+`:not(.is-val)[data-cc="DE"]`, "🇩🇪 Germany · 1 val · 3 peers", de, deCounts)
		check(mapDots+`.is-val[data-cc="DE"]`, "🇩🇪 Germany · 1 val · 3 peers", de, deCounts)
		check(mapDots+`[data-cc="JP"]`, "🇯🇵 Japan · 2 vals", []string{"☁️ AWS"}, []string{"2 vals"})
		check(mapDots+`[data-cc="US"]`, "🇺🇸 United States · 1 peer", []string{"☁️ Vultr"}, []string{"1 peer"})
		check(mapDots+`:not([data-cc])`, "Unknown · 1 peer", []string{"☁️ Unknown"}, []string{"1 peer"})

		mapPressMode(e, "Provider")
		e.must(e.expect(mapDots + `[data-prov]`).ToHaveCount(6))
		// full-norpc, on Hetzner but not located, is not counted.
		hetzner := []string{"🇩🇪 Germany"}
		check(mapDots+`:not(.is-val)[data-prov="Hetzner"]`, "☁️ Hetzner · 1 val · 2 peers", hetzner, []string{"1 val · 2 peers"})
		ovh := []string{"🇦🇺 Australia", "🇩🇪 Germany"}
		for i := range 2 {
			sel := mapDots + `[data-prov="OVH"] >> nth=` + strconv.Itoa(i)
			check(sel, "☁️ OVH · 2 peers", ovh, []string{"1 peer", "1 peer"})
		}
		check(mapDots+`:not([data-prov])`, "☁️ Unknown · 1 peer", []string{"Unknown"}, []string{"1 peer"})
	})
}

// TestPageMapSideList checks the list beside the map: in Country mode a row
// per country with its flag, name and number of peers, located or not,
// sources included; in Provider mode a row per provider with its name and
// number. Rows go by number, then by name, Unknown last. Assistive
// technology reads it as a list of those rows, by its name.
func TestPageMapSideList(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, mapScenario(mapWorldPeers()...))
		items := e.Page.GetByRole("list", playwright.PageGetByRoleOptions{Name: "Peers by country or provider"}).GetByRole("listitem")
		mapAttrs(e, mapRows, "data-cc", "DE", "JP", "AU", "US", "Unknown")
		e.must(e.Expect.Locator(items).ToHaveText([]string{"🇩🇪Germany4", "🇯🇵Japan2", "🇦🇺Australia1", "🇺🇸United States1", "Unknown3"}))
		e.must(e.expect(mapRows + " .country-flag").ToHaveText([]string{"🇩🇪", "🇯🇵", "🇦🇺", "🇺🇸", ""}))
		e.must(e.expect(mapRows + " .country-name").ToHaveText([]string{"Germany", "Japan", "Australia", "United States", "Unknown"}))
		e.must(e.expect(mapRows + " .country-count").ToHaveText([]string{"4", "2", "1", "1", "3"}))

		mapPressMode(e, "Provider")
		e.must(e.expect(mapRows + " .country-name").ToHaveText([]string{"Hetzner", "AWS", "OVH", "Vultr", "Unknown"}))
		e.must(e.expect(mapRows + " .country-count").ToHaveText([]string{"4", "2", "2", "1", "2"}))
		mapAttrs(e, mapRows+"[data-prov]", "data-prov", "Hetzner", "AWS", "OVH", "Vultr")
		e.must(e.expect(mapRows + ":not([data-prov]) .country-name").ToHaveText("Unknown"))
		e.must(e.expect(mapRows + " .country-flag").ToHaveCount(0))
		e.must(e.Expect.Locator(items).ToHaveText([]string{"Hetzner4", "AWS2", "OVH2", "Vultr1", "Unknown2"}))
	})
}

// TestPageMapProviderNameOrder checks that providers alike in number go
// in English alphabetical order, case aside, in the side list and in a
// country's popover: netcup GmbH between Hetzner and Vultr.
func TestPageMapProviderNameOrder(t *testing.T) {
	e := openPage(t, desktop, mapScenario(
		mapLocated(fullPeer("fra-v"), 50.11, 8.68, "DE", "Vultr"),
		mapLocated(fullPeer("fra-n"), 50.11, 8.68, "DE", "netcup GmbH"),
		mapLocated(fullPeer("fra-h"), 50.11, 8.68, "DE", "Hetzner"),
	))
	providers := []string{"Hetzner", "netcup GmbH", "Vultr"}
	e.must(e.expect(mapDots).ToHaveCount(1))
	e.must(e.Page.Locator(mapDots).Hover())
	e.must(e.expect("#popover td.l").ToHaveText([]string{"☁️ Hetzner", "☁️ netcup GmbH", "☁️ Vultr"}))
	mapPressMode(e, "Provider")
	e.must(e.expect(mapRows + " .country-name").ToHaveText(providers))
}

// TestPageMapProviderPopoverCountryOrder checks that a provider's popover
// lists its countries alike in number in the order of the names it shows:
// Germany before Switzerland, though CH comes before DE.
func TestPageMapProviderPopoverCountryOrder(t *testing.T) {
	e := openPage(t, desktop, mapScenario(
		mapLocated(fullPeer("zrh"), 47.37, 8.54, "CH", "OVH"),
		mapLocated(fullPeer("fra"), 50.11, 8.68, "DE", "OVH"),
	))
	mapPressMode(e, "Provider")
	e.must(e.Page.Locator(mapDots + `[data-prov="OVH"] >> nth=0`).Hover())
	e.must(e.expect("#popover td.l").ToHaveText([]string{"🇩🇪 Germany", "🇨🇭 Switzerland"}))
}

// TestPageMapCountryListNameOrder checks that the side list orders countries
// alike in number by the names it shows: Germany before Switzerland, though
// CH comes before DE.
func TestPageMapCountryListNameOrder(t *testing.T) {
	e := openPage(t, desktop, mapScenario(
		mapLocated(fullPeer("zrh"), 47.37, 8.54, "CH", "OVH"),
		mapLocated(fullPeer("fra"), 50.11, 8.68, "DE", "OVH"),
	))
	e.must(e.expect(mapRows + " .country-name").ToHaveText([]string{"Germany", "Switzerland"}))
}

// TestPageMapPopoverUnknownLast checks that a cluster's popover lists its
// unknown branch last, whatever its number, as the side list does: a
// country's peers of no known provider, a provider's peers of no known
// country.
func TestPageMapPopoverUnknownLast(t *testing.T) {
	e := openPage(t, desktop, mapScenario(
		mapLocated(fullPeer("fra-a"), 50.11, 8.68, "DE", "Hetzner"),
		mapLocated(fullPeer("fra-b"), 50.11, 8.68, "DE", ""),
		mapLocated(fullPeer("fra-c"), 50.11, 8.68, "DE", ""),
		mapLocated(fullPeer("ber"), 52.52, 13.40, "DE", "OVH"),
		mapLocated(fullPeer("sao-a"), -23.55, -46.63, "", "OVH"),
		mapLocated(fullPeer("sao-b"), -23.55, -46.63, "", "OVH"),
	))
	e.must(e.Page.Locator(mapDots + `[data-cc="DE"]`).Hover())
	e.must(e.expect("#popover td.l").ToHaveText([]string{"☁️ Hetzner", "☁️ OVH", "☁️ Unknown"}))
	e.must(e.expect("#popover td.v").ToHaveText([]string{"1 peer", "1 peer", "2 peers"}))
	mapPressMode(e, "Provider")
	e.must(e.Page.Mouse().Move(0, 0))
	e.must(e.Page.Locator(mapDots + `[data-prov="OVH"] >> nth=0`).Hover())
	e.must(e.expect("#popover td.l").ToHaveText([]string{"🇩🇪 Germany", "Unknown"}))
	e.must(e.expect("#popover td.v").ToHaveText([]string{"1 peer", "2 peers"}))
}

// TestPageMapCountryNames checks how the list names a country: its English
// name and flag, the code itself when the browser knows no name for it, and
// no flag for a code that is not two letters.
func TestPageMapCountryNames(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, mapScenario(
			mapLocated(fullPeer("de-1"), 50.11, 8.68, "DE", ""),
			mapLocated(fullPeer("abc-1"), 39.04, -77.49, "ABC", ""),
			mapLocated(fullPeer("xx-1"), 35.68, 139.69, "XX", ""),
		))
		mapAttrs(e, mapRows, "data-cc", "ABC", "DE", "XX")
		e.must(e.expect(mapRows + " .country-flag").ToHaveText([]string{"", "🇩🇪", "🇽🇽"}))
		e.must(e.expect(mapRows + " .country-name").ToHaveText([]string{"ABC", "Germany", "XX"}))
	})
}

// TestPageMapToggle checks the Country / Provider toggle: styled as the
// validators card's toggle, as wide as the list below it, beside the map on
// a wide screen and under it, capped in height, at 720 px and below. A
// press switches the list and the map; a reload starts in Country mode.
func TestPageMapToggle(t *testing.T) {
	onProfiles(t, []profile{desktop, tablet, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, mapScenario(mapWorldPeers()...))
		toggle := e.Page.Locator("#map-list-toggle")
		list := e.Page.Locator("#map-country-list")
		e.must(e.expect("#map-list-toggle").ToHaveClass(regexp.MustCompile(`(^|\s)view-seg(\s|$)`)))
		buttons := "#map-list-toggle button"
		e.must(e.expect(buttons).ToHaveText([]string{"Country", "Provider"}))
		mapAttrs(e, buttons, "type", "button", "button")
		mapAttrs(e, buttons, "data-mode", "country", "provider")

		pressed := func(country, provider string) {
			t.Helper()
			mapAttrs(e, buttons, "aria-pressed", country, provider)
		}
		pressed("true", "false")
		on := e.expect(`#map-list-toggle button[aria-pressed="true"]`)
		off := e.expect(`#map-list-toggle button[aria-pressed="false"]`)
		e.must(on.ToHaveCSS("color", "rgb(201, 209, 217)"))
		e.must(on.ToHaveCSS("background-color", "rgb(13, 17, 23)"))
		e.must(off.ToHaveCSS("color", "rgb(139, 148, 158)"))
		e.must(off.ToHaveCSS("background-color", "rgba(0, 0, 0, 0)"))

		e.must(e.expect(mapRows).ToHaveCount(5))
		tb, err := toggle.BoundingBox()
		e.must(err)
		lb, err := list.BoundingBox()
		e.must(err)
		mb, err := e.Page.Locator("svg.world-map").BoundingBox()
		e.must(err)
		if math.Abs(tb.Width-lb.Width) > 1 {
			t.Errorf("toggle width = %.1f, want the list's %.1f", tb.Width, lb.Width)
		}
		if p.name == "desktop" {
			if lb.X < mb.X+mb.Width {
				t.Errorf("list at x %.1f, want it right of the map, which ends at %.1f", lb.X, mb.X+mb.Width)
			}
			e.must(e.expect("#map-country-list").ToHaveCSS("max-height", "none"))
		} else {
			if tb.Y < mb.Y+mb.Height {
				t.Errorf("toggle at y %.1f, want it under the map, which ends at %.1f", tb.Y, mb.Y+mb.Height)
			}
			e.must(e.expect("#map-country-list").ToHaveCSS("max-height", "200px"))
		}

		mapPressMode(e, "Provider")
		pressed("false", "true")
		e.must(e.expect(mapRows + " .country-name").ToHaveText([]string{"Hetzner", "AWS", "OVH", "Vultr", "Unknown"}))
		e.must(e.expect(mapDots + "[data-prov]").ToHaveCount(6))
		mapPressMode(e, "Country")
		pressed("true", "false")
		e.must(e.expect(mapRows + " .country-name").ToHaveText([]string{"Germany", "Japan", "Australia", "United States", "Unknown"}))
		e.must(e.expect(mapDots + "[data-cc]").ToHaveCount(5))

		mapPressMode(e, "Provider")
		pressed("false", "true")
		e.navigate("/")
		pressed("true", "false")
		e.must(e.expect(mapRows + " .country-name").ToHaveText([]string{"Germany", "Japan", "Australia", "United States", "Unknown"}))
	})
}

// TestPageMapCrossHighlight checks that a pointer on a cluster, a list row
// or a table cell highlights every element of its country or provider, on
// the map, in the list and in the tables, and dims the rest of the map when
// the map shows it; leaving clears it all.
func TestPageMapCrossHighlight(t *testing.T) {
	e := openPage(t, desktop, mapScenario(mapWorldPeers()...))
	e.must(e.expect(mapDots).ToHaveCount(6))
	away := e.Page.Locator("#map-header")

	e.must(e.Page.Locator(mapDots + `:not(.is-val)[data-cc="DE"]`).Hover())
	expectMapHighlighted(e, "data-cc", "DE")
	expectMapDimmed(e, true)
	e.must(e.expect(`#map-points > .cc-hl`).ToHaveCount(7))
	e.must(e.expect(mapDots+`:not(.is-val)[data-cc="DE"]`).ToHaveCSS("opacity", "1"))
	e.must(e.expect(mapDots+`:not(.is-val)[data-cc="DE"]`).ToHaveCSS("stroke", "rgb(255, 255, 255)"))
	e.must(e.expect(mapDots+`[data-cc="US"]`).ToHaveCSS("opacity", "0.15"))
	e.must(e.expect(mapDots+`:not([data-cc])`).ToHaveCSS("opacity", "0.15"))
	e.must(e.expect(mapRows+`[data-cc="DE"]`).ToHaveCSS("background-color", "rgba(88, 166, 255, 0.25)"))
	// bravo, a validator, has its row in the validators table.
	e.must(e.expect(`#peers-body td.country-cell[data-cc="DE"]`).ToHaveCount(3))
	e.must(e.expect(`#validators-body td.country-cell[data-cc="DE"]`).ToHaveCount(1))
	e.must(e.expect(`#peers-body td.country-cell[data-cc="DE"] >> nth=0`).ToHaveCSS("background-color", "rgba(88, 166, 255, 0.25)"))

	e.must(away.Hover())
	e.must(e.expect(".cc-hl").ToHaveCount(0))
	expectMapDimmed(e, false)
	e.must(e.expect(mapDots+`[data-cc="US"]`).ToHaveCSS("opacity", "1"))

	e.must(e.Page.Locator(mapRows + `[data-cc="JP"]`).Hover())
	expectMapHighlighted(e, "data-cc", "JP")
	expectMapDimmed(e, true)
	e.must(e.expect(mapDots+`[data-cc="US"]`).ToHaveCSS("opacity", "0.15"))

	// Unknown has a row but no cluster: the map is not dimmed.
	e.must(e.Page.Locator(mapRows + `[data-cc="Unknown"]`).Hover())
	expectMapHighlighted(e, "data-cc", "Unknown")
	expectMapDimmed(e, false)

	e.must(e.Page.Locator(`#peers-body td.country-cell[data-cc="US"]`).Hover())
	expectMapHighlighted(e, "data-cc", "US")
	expectMapDimmed(e, true)
	e.must(away.Hover())
	e.must(e.expect(".cc-hl").ToHaveCount(0))

	mapPressMode(e, "Provider")
	e.must(e.Page.Locator(mapRows + `[data-prov="OVH"]`).Hover())
	expectMapHighlighted(e, "data-prov", "OVH")
	expectMapDimmed(e, true)
	e.must(e.expect(mapDots + `[data-prov="OVH"]`).ToHaveCount(2))
	e.must(e.expect(mapDots+`[data-prov="Hetzner"] >> nth=0`).ToHaveCSS("opacity", "0.15"))
	e.must(e.Page.Locator(mapDots + `[data-prov="Vultr"]`).Hover())
	expectMapHighlighted(e, "data-prov", "Vultr")
	e.must(away.Hover())
	e.must(e.expect(".cc-hl").ToHaveCount(0))
	expectMapDimmed(e, false)
}

// mapManyCountries are twenty peers, one per country, in a city of it.
func mapManyCountries() []node.Peer {
	cities := []struct {
		cc       string
		lat, lon float64
	}{
		{"AR", -34.60, -58.38},
		{"AU", -33.87, 151.21},
		{"BR", -23.55, -46.63},
		{"CA", 43.65, -79.38},
		{"CL", -33.45, -70.67},
		{"DE", 50.11, 8.68},
		{"EG", 30.04, 31.24},
		{"ES", 40.42, -3.70},
		{"IN", 19.08, 72.88},
		{"JP", 35.68, 139.69},
		{"KE", -1.29, 36.82},
		{"MX", 19.43, -99.13},
		{"NG", 6.52, 3.38},
		{"NZ", -36.85, 174.76},
		{"RU", 55.76, 37.62},
		{"SE", 59.33, 18.07},
		{"SG", 1.35, 103.82},
		{"TH", 13.76, 100.50},
		{"US", 39.04, -77.49},
		{"ZA", -26.20, 28.05},
	}
	peers := make([]node.Peer, len(cities))
	for i, c := range cities {
		peers[i] = mapLocated(fullPeer("node-"+strings.ToLower(c.cc)), c.lat, c.lon, c.cc, "")
	}
	return peers
}

// TestPageMapHoverScrollsList checks that a pointer on a cluster scrolls
// its country's row into the list's view, and that one on a table cell
// highlights the row without scrolling the list.
func TestPageMapHoverScrollsList(t *testing.T) {
	e := openPage(t, desktop, mapScenario(mapManyCountries()...))
	e.must(e.expect(mapRows).ToHaveCount(20))
	// Thailand and the United States, the list's last two rows, start below
	// its fold. The font sets how many rows fit: 17 of the 20 with Linux's,
	// fewer with macOS's.
	th := e.expect(mapRows + `[data-cc="TH"]`)
	us := e.expect(mapRows + `[data-cc="US"]`)
	e.must(th.Not().ToBeInViewport())
	e.must(us.Not().ToBeInViewport())

	e.must(e.Page.Locator(`#peers-body td.country-cell[data-cc="TH"]`).Hover())
	expectMapHighlighted(e, "data-cc", "TH")
	// A scroll would be under way by the next hover, which waits for two
	// still animation frames first.
	e.must(e.Page.Locator(`#peers-body td.country-cell[data-cc="US"]`).Hover())
	expectMapHighlighted(e, "data-cc", "US")
	e.must(e.expect("#map-country-list").ToHaveJSProperty("scrollTop", 0))

	e.must(e.Page.Locator(mapDots + `[data-cc="TH"]`).Hover())
	e.must(th.ToBeInViewport())
}

// TestPageMapHighlightSurvivesUpdate checks that an update redrawing the map
// and the list under a still pointer keeps its country highlighted and the
// rest of the map dimmed from the redraw on, and stops dimming the map once
// that country has left it; once the pointer has left, a redraw highlights
// nothing.
func TestPageMapHighlightSurvivesUpdate(t *testing.T) {
	onProfiles(t, []profile{desktop, desktopWebKit}, func(t *testing.T, p profile) {
		e := openPage(t, p, mapScenario(mapWorldPeers()...))
		dePeers := mapDots + `:not(.is-val)[data-cc="DE"]`
		e.must(e.Page.Locator(dePeers).Hover())
		expectMapHighlighted(e, "data-cc", "DE")
		expectMapDimmed(e, true)

		// us-a moves to Seattle: once its cluster has moved, the map is
		// redrawn.
		moved := mapWorldPeers()
		moved[len(moved)-1] = mapLocated(fullPeer("us-a"), 47.61, -122.33, "US", "Vultr")
		e.publish(mapSnapshot(moved...))
		e.must(e.expect(mapDots+`[data-cc="US"]`).ToHaveAttribute("cx", "57.67"))
		if !hasClassNow(e, dePeers, "cc-hl") {
			t.Errorf("right after the redraw, %s is not highlighted", dePeers)
		}
		if !hasClassNow(e, "#map-points", "dim-others") {
			t.Error("right after the redraw, the map is not dimmed")
		}
		expectMapHighlighted(e, "data-cc", "DE")
		expectMapDimmed(e, true)
		e.must(e.expect(dePeers).ToHaveCSS("opacity", "1"))
		e.must(e.expect(mapDots+`[data-cc="US"]`).ToHaveCSS("opacity", "0.15"))

		// JP's validators lose their location: JP keeps its row, and the map
		// has nothing left to dim for.
		jpRow := mapRows + `[data-cc="JP"]`
		e.must(e.Page.Locator(jpRow).Hover())
		expectMapHighlighted(e, "data-cc", "JP")
		expectMapDimmed(e, true)
		for i, p := range moved {
			if p.Country == "JP" {
				moved[i].Lat, moved[i].Lon = nil, nil
			}
		}
		e.publish(mapSnapshot(moved...))
		e.must(e.expect(mapDots + `[data-cc="JP"]`).ToHaveCount(0))
		if !hasClassNow(e, jpRow, "cc-hl") {
			t.Errorf("right after the redraw, %s is not highlighted", jpRow)
		}
		if hasClassNow(e, "#map-points", "dim-others") {
			t.Error("right after the redraw, the map is dimmed with JP off it")
		}
		expectMapHighlighted(e, "data-cc", "JP")
		expectMapDimmed(e, false)
		e.must(e.expect(mapDots+`[data-cc="US"]`).ToHaveCSS("opacity", "1"))

		// Once the pointer has left, a redraw highlights nothing.
		e.must(e.Page.Locator("#map-header").Hover())
		e.must(e.expect(".cc-hl").ToHaveCount(0))
		e.publish(mapSnapshot(mapWorldPeers()...))
		e.must(e.expect(mapDots + `[data-cc="JP"]`).ToHaveCount(1))
		e.must(e.expect(".cc-hl").ToHaveCount(0))
		expectMapDimmed(e, false)
	})
}

// TestPageMapHighlightFollowsThePointer checks that a highlight lasts only
// while the pointer is on its country, whatever the redraws: the pointer
// leaving a cluster redrawn under it clears it; once a redraw has taken the
// hovered country away, its return highlights nothing; a redraw moving the
// hovered cluster from under a still pointer clears it at once.
func TestPageMapHighlightFollowsThePointer(t *testing.T) {
	onProfiles(t, []profile{desktop, desktopWebKit}, func(t *testing.T, p profile) {
		e := openPage(t, p, mapScenario(mapWorldPeers()...))
		dePeers := mapDots + `:not(.is-val)[data-cc="DE"]`
		away := e.Page.Locator("#map-header")

		// A redraw under the pointer, which then moves off the map at once:
		// before the browser's own hover update, which comes a frame later
		// on Chromium and several on WebKit.
		header, err := away.BoundingBox()
		e.must(err)
		e.must(e.Page.Locator(dePeers).Hover())
		expectMapHighlighted(e, "data-cc", "DE")
		moved := mapWorldPeers()
		moved[len(moved)-1] = mapLocated(fullPeer("us-a"), 47.61, -122.33, "US", "Vultr")
		e.publish(mapSnapshot(moved...))
		e.must(e.expect(mapDots+`[data-cc="US"]`).ToHaveAttribute("cx", "57.67"))
		e.must(e.Page.Mouse().Move(header.X+header.Width/2, header.Y+header.Height/2))
		e.must(e.expect(".cc-hl").ToHaveCount(0))
		expectMapDimmed(e, false)

		// Again, the pointer moving at once onto JP's row: JP only.
		jp, err := e.Page.Locator(mapRows + `[data-cc="JP"]`).BoundingBox()
		e.must(err)
		e.must(e.Page.Locator(dePeers).Hover())
		expectMapHighlighted(e, "data-cc", "DE")
		e.publish(mapSnapshot(mapWorldPeers()...))
		e.must(e.expect(mapDots+`[data-cc="US"]`).ToHaveAttribute("cx", "102.51"))
		e.must(e.Page.Mouse().Move(jp.X+jp.Width/2, jp.Y+jp.Height/2))
		expectMapHighlighted(e, "data-cc", "JP")

		// DE leaves the network under the pointer, which moves on; DE comes
		// back.
		e.must(e.Page.Locator(dePeers).Hover())
		expectMapHighlighted(e, "data-cc", "DE")
		var withoutDE []node.Peer
		for _, peer := range mapWorldPeers() {
			if peer.Country != "DE" {
				withoutDE = append(withoutDE, peer)
			}
		}
		e.publish(mapSnapshot(withoutDE...))
		e.must(e.expect(mapRows + `[data-cc="DE"]`).ToHaveCount(0))
		e.must(away.Hover())
		e.publish(mapSnapshot(mapWorldPeers()...))
		e.must(e.expect(dePeers).ToHaveCount(1))
		e.must(e.expect(".cc-hl").ToHaveCount(0))
		expectMapDimmed(e, false)

		// The DE peers move to the far south, away from the still pointer.
		e.must(e.Page.Locator(dePeers).Hover())
		expectMapHighlighted(e, "data-cc", "DE")
		far := mapWorldPeers()
		for i, peer := range far {
			if peer.Country == "DE" && peer.ValAddress == "" {
				far[i] = mapLocated(peer, -60, 120, "DE", peer.Provider)
			}
		}
		e.publish(mapSnapshot(far...))
		e.must(e.expect(dePeers).ToHaveAttribute("cy", "150.00"))
		if hasClassNow(e, dePeers, "cc-hl") {
			t.Errorf("right after the redraw, %s is highlighted with the pointer off it", dePeers)
		}
		if hasClassNow(e, "#map-points", "dim-others") {
			t.Error("right after the redraw, the map is dimmed with the pointer off DE")
		}
	})
}

//go:build page

package web

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/node"
)

// ---- Collapsible sections

const (
	// mechCollapsedKey and mechCollapsedMobileKey hold the folded sections
	// of wide screens and of screens up to 768 px wide.
	mechCollapsedKey       = "gnockpit-collapsed"
	mechCollapsedMobileKey = "gnockpit-collapsed-mobile"
)

// TestPageSectionsCollapse checks that a click on a card's title folds or
// unfolds its section, and that the page keeps the folded sections across
// loads, one set for screens up to 768 px wide and one for wider screens,
// chosen once at load.
func TestPageSectionsCollapse(t *testing.T) {
	onProfiles(t, []profile{desktop, tablet, phone}, func(t *testing.T, p profile) {
		own, other := mechCollapsedKey, mechCollapsedMobileKey
		if p != desktop {
			own, other = other, own
		}
		e := openPage(t, p, healthyNetwork(), withStorage(map[string]string{
			own:   `{"peers":true}`,
			other: `{"validators":true}`,
		}))
		e.must(e.expect("#ns-consensus").ToContainText("h="))
		expectSectionFolded(e, "peers", true)
		expectSectionFolded(e, "validators", false)
		expectSectionFolded(e, "network-state", false)
		expectSectionFolded(e, "map", false)

		if p == desktop {
			// Narrowing the window keeps the set chosen at load.
			e.must(e.Page.SetViewportSize(tablet.viewport.Width, tablet.viewport.Height))
		}
		e.press(e.Page.Locator(sectionToggle("validators")))
		expectSectionFolded(e, "validators", true)
		expectStored(e, own, `{"peers":true,"validators":true}`)
		e.press(e.Page.Locator(sectionToggle("peers")))
		expectSectionFolded(e, "peers", false)
		expectStored(e, own, `{"validators":true}`)
		expectStored(e, other, `{"validators":true}`)

		e.navigate("/")
		expectSectionFolded(e, "validators", true)
		expectSectionFolded(e, "peers", false)
	})
}

// TestPageSectionTitlesFromKeyboard checks that each card's title stays a
// heading and holds the button that folds its section, which says whether
// the section is open: Enter, Space and a click each fold or unfold it once,
// and Space does not scroll the page.
func TestPageSectionTitlesFromKeyboard(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		key := mechCollapsedKey
		if p == phone {
			key = mechCollapsedMobileKey
		}
		e := openPage(t, p, healthyNetwork(), withStorage(map[string]string{key: `{"peers":true}`}))
		titles := e.Page.Locator("h2[data-section]")
		e.must(e.Expect.Locator(titles).ToHaveCount(7))
		all, err := titles.All()
		e.must(err)
		for _, title := range all {
			e.must(e.Expect.Locator(title).ToHaveRole(*playwright.AriaRoleHeading))
			e.must(e.Expect.Locator(title.Locator("> button")).ToHaveRole(*playwright.AriaRoleButton))
		}
		e.must(e.expect(sectionToggle("peers")).ToHaveAttribute("aria-expanded", "false"))
		e.must(e.expect(`#validators-header > button`).ToHaveText(regexp.MustCompile(`^Validators \(4\)$`)))

		// The first card's title: the page stays at its top, where a
		// section that unfolds above the view cannot move it.
		toggle := e.Page.Locator(sectionToggle("network-state"))
		expectSectionFolded(e, "network-state", false)
		e.must(toggle.Focus())
		e.must(e.Page.Keyboard().Press("Enter"))
		expectSectionFolded(e, "network-state", true)
		expectStored(e, key, `{"peers":true,"network-state":true}`)
		y := evalInt(e, `() => scrollY`)
		e.must(e.Page.Keyboard().Press(" "))
		expectSectionFolded(e, "network-state", false)
		if got := evalInt(e, `() => scrollY`); got != y {
			t.Errorf("Space on a title scrolled the page from %d to %d", y, got)
		}
		e.press(toggle)
		expectSectionFolded(e, "network-state", true)
		expectStored(e, key, `{"peers":true,"network-state":true}`)
	})
}

// TestPageSectionTitleFoldsFromEnd checks that a click anywhere on a card's
// title folds its section, near its end too: the title's button fills a
// title that fills its row.
func TestPageSectionTitleFoldsFromEnd(t *testing.T) {
	for _, tc := range []struct {
		p       profile
		section string
	}{
		{desktop, "network-state"},
		{phone, "validators"},
	} {
		t.Run(tc.p.name, func(t *testing.T) {
			e := openPage(t, tc.p, healthyNetwork())
			e.must(e.expect("#ns-consensus").ToContainText("h="))
			e.must(e.Page.Locator(sectionTitle(tc.section)).ScrollIntoViewIfNeeded())
			e.must(e.expect(sectionTitle(tc.section)).ToBeInViewport())
			title := boxOf(e, sectionTitle(tc.section))
			x := math.Round(title.X + title.Width - 6)
			textEnd := textRight(e, sectionToggle(tc.section))
			if x < textEnd+40 {
				t.Fatalf("%s: the title's text ends at %.0f px and the title at %.0f px, want room beside the text", tc.section, textEnd, title.X+title.Width)
			}
			mechPressAt(e, tc.p, x, math.Round(title.Y+title.Height/2))
			expectSectionFolded(e, tc.section, true)
		})
	}
}

// ---- Flash

// TestPageValueFlashes checks that a value shown in a card flashes when it
// changes: every new value restarts the flash, the same value set again
// leaves the running one alone.
func TestPageValueFlashes(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		const consensus = "#ns-consensus"
		e.must(e.expect(consensus).ToHaveText(fmt.Sprintf("h=%d r=0 s=4", pageHeight+1)))
		e.must(e.expect(consensus).ToHaveClass(regexp.MustCompile(`\bchanged\b`)))
		e.must(e.expect(consensus).ToHaveCSS("animation-name", "flash"))

		// A restart that depends on the browser's frame timing fails now and
		// then: ten in a row.
		// A published snapshot is the server's from then on: each round
		// publishes a new one.
		roundSnapshot := func(round int) *node.Snapshot {
			snap := healthySnapshot()
			snap.Consensus.Round = strconv.Itoa(round)
			return snap
		}
		for round := 1; round <= mechFlashRestarts; round++ {
			before := runningFlash(e, consensus)
			e.publish(roundSnapshot(round))
			e.must(e.expect(consensus).ToHaveText(fmt.Sprintf("h=%d r=%d s=4", pageHeight+1, round)))
			awaitFlashCancelled(e, before, fmt.Sprintf("round %d", round))
			e.must(e.expect(consensus).ToHaveClass(regexp.MustCompile(`\bchanged\b`)))
		}

		// Another vote redraws the card; the consensus text stays.
		running := runningFlash(e, consensus)
		vote := roundSnapshot(mechFlashRestarts)
		vote.Consensus.Votes[3].Prevoted = true
		e.publish(vote)
		e.must(e.expect("#ns-votes").ToHaveText("35/0 of 35"))
		e.must(e.expect(consensus).ToHaveClass(regexp.MustCompile(`\bchanged\b`)))
		if state := flashState(e, running); state == "idle" {
			t.Errorf("the flash of an unchanged value is idle, want it left running")
		}
	})
}

// mechFlashRestarts is how many restarts in a row a flash test checks.
const mechFlashRestarts = 10

// TestPageFlashSurvivesAnUnchangedValue checks that a value set again
// unchanged, colour included, keeps its running flash: the round age, which
// a redraw for a new vote sets again.
func TestPageFlashSurvivesAnUnchangedValue(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		const age = "#ns-roundage"
		e.must(e.expect(age).ToHaveText("4s"))
		e.must(e.expect(age).ToHaveClass(regexp.MustCompile(`\bchanged\b`)))
		flash := runningFlash(e, age)

		again := healthySnapshot()
		again.Consensus.Votes[3].Prevoted = true
		e.publish(again)
		e.must(e.expect("#ns-votes").ToHaveText("35/0 of 35"))
		e.must(e.expect(age).ToHaveText("4s"))
		e.must(e.expect(age).ToHaveClass(regexp.MustCompile(`\bchanged\b`)))
		if state := flashState(e, flash); state == "idle" {
			t.Errorf("the round age's flash is idle after the redraw, want it left running")
		}
	})
}

// ---- Copy

// TestPageCopyToast checks the copy toast: a status region, empty and clear
// before any copy; a copy shows "Copied!", dark on green, 28 px above the
// pointer, which fades 800 ms after the last copy.
func TestPageCopyToast(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork(), withInstalledClock())
		e.runFor(time.Second)
		const value = "#ns-consensus"
		e.must(e.expect(value).ToContainText("h="))
		toast := e.expect(".copy-toast")
		// The toast is in the page from the start: a status region, empty
		// and clear, which screen readers follow.
		e.must(toast.ToHaveAttribute("role", "status"))
		e.must(toast.ToHaveText(""))
		e.must(toast.ToHaveCSS("opacity", "0"))

		box := boxOf(e, value)
		x, y := math.Round(box.X+4), math.Round(box.Y+box.Height/2)
		mechPressAt(e, p, x, y)
		expectToast(e, "Copied!")
		// The page's dark background colour on its green: about 7:1, where
		// text this small needs 4.5:1 to be read.
		e.must(toast.ToHaveCSS("background-color", "rgb(63, 185, 80)"))
		e.must(toast.ToHaveCSS("color", "rgb(13, 17, 23)"))
		tb := boxOf(e, ".copy-toast")
		if math.Abs(tb.X+tb.Width/2-x) > 1 || math.Abs(tb.Y-(y-28)) > 1 {
			t.Errorf("toast at (%.1f, %.1f) centre x %.1f, want centre x %.1f, top %.1f", tb.X, tb.Y, tb.X+tb.Width/2, x, y-28)
		}
		if p.browser == chromium {
			if got, want := e.clipboard(), fmt.Sprintf("h=%d r=0 s=4", pageHeight+1); got != want {
				t.Errorf("clipboard = %q, want %q", got, want)
			}
		}

		e.runFor(600 * time.Millisecond)
		// A copy shows its toast once the clipboard has taken the text: the
		// second, 20 px right, has when the toast has moved there.
		mechPressAt(e, p, x+20, y)
		e.must(toast.ToHaveAttribute("style", regexp.MustCompile(fmt.Sprintf(`left: %dpx;`, int(x)+20))))
		e.runFor(600 * time.Millisecond)
		e.must(toast.ToHaveAttribute("style", toastOpaque))
		e.runFor(200 * time.Millisecond)
		e.must(toast.ToHaveAttribute("style", toastClear))
		e.must(toast.ToHaveCSS("opacity", "0"))
	})
}

// TestPageCopyNothingFromAnEmptyValue checks that a value of "-" copies
// nothing and shows no toast. A copy's toast comes once the clipboard has
// taken its text, so the test reads the clipboard after the click: the read
// comes after any write the click started, and finds the text copied before
// it. Chromium only, the browser whose clipboard a test reads.
func TestPageCopyNothingFromAnEmptyValue(t *testing.T) {
	// Without signing stats, the average block time reads "-".
	sc := healthyNetwork()
	sc.snap.Signing = nil
	e := openPage(t, desktop, sc, withInstalledClock())
	e.runFor(time.Second)
	consensus := fmt.Sprintf("h=%d r=0 s=4", pageHeight+1)
	e.must(e.expect("#ns-consensus").ToHaveText(consensus))
	e.must(e.expect("#ns-avgblock").ToHaveText("-"))
	e.must(e.Page.Locator("#ns-consensus").Click())
	expectToast(e, "Copied!")
	fadeToast(e)

	box := boxOf(e, "#ns-avgblock")
	mechPressAt(e, desktop, math.Round(box.X+2), math.Round(box.Y+box.Height/2))
	if got := e.clipboard(); got != consensus {
		t.Errorf("clipboard after a click on \"-\" = %q, want the text copied before, %q", got, consensus)
	}
	e.must(e.expect(".copy-toast").ToHaveAttribute("style", toastClear))
}

// mechPressAt clicks, or taps on a touch screen, the page at (x, y).
func mechPressAt(e *pageEnv, p profile, x, y float64) {
	e.T.Helper()
	if p.touch() {
		e.must(e.Page.Touchscreen().Tap(int(math.Round(x)), int(math.Round(y))))
		return
	}
	e.must(e.Page.Mouse().Click(x, y))
}

// TestPageCopyKeyValue checks that a click on a card's value copies its
// copy text when it has one, else its text, and that a click on a link in a
// value follows the link and copies nothing.
func TestPageCopyKeyValue(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := healthyNetwork()
		docs := mechDocsServer(t)
		sc.valopers = map[string]node.Valoper{mechAlphaOperator: {
			Operator: mechAlphaOperator, Moniker: "alpha", SigningAddress: valAlpha.address(),
			Description: "Runs alpha. [docs](" + docs + "/docs)",
		}}
		e := openPage(t, p, sc, withInstalledClock())
		e.runFor(time.Second)
		e.must(e.expect("#ns-consensus").ToContainText("h="))

		if p == phone {
			// A phone's Network State opens compact, without these values.
			e.must(e.Page.Locator("#ns-compact-cb").Uncheck())
		}
		for _, v := range []struct{ selector, copied string }{
			{"#ns-consensus", fmt.Sprintf("h=%d r=0 s=4", pageHeight+1)},
			{"#d-network", pageChainID},
			{"#d-apphash-last", appHashHex(pageHeight)},
		} {
			e.press(e.Page.Locator(v.selector))
			expectToast(e, "Copied!")
			if p.browser == chromium {
				if got := e.clipboard(); got != v.copied {
					t.Errorf("click %s: clipboard = %q, want %q", v.selector, got, v.copied)
				}
			}
			fadeToast(e)
		}
		if p == phone {
			// On a touch screen the app hash's value is also a popover
			// target.
			e.must(e.expect(pagePopover).ToBeVisible())
			e.must(e.expect(pagePopover + " td.l").ToHaveText([]string{"hex", "base64"}))
		}

		e.press(e.Page.Locator(`a.val-link:visible:has-text("alpha")`))
		link := e.Page.Locator(`#val-body .kv .v.val-text a`)
		e.must(e.Expect.Locator(link).ToHaveText("docs ↗"))
		e.must(e.Expect.Locator(link).ToHaveAttribute("href", docs+"/docs"))
		e.must(e.Expect.Locator(link).ToHaveAttribute("target", "_blank"))
		e.must(e.Expect.Locator(link).ToHaveAttribute("rel", "noopener"))
		e.must(e.Expect.Locator(link.Locator(".ext-arrow")).ToHaveText(" ↗"))
		before := ""
		if p.browser == chromium {
			before = e.clipboard()
		}
		popup, err := e.Page.ExpectPopup(func() error {
			e.press(link)
			return nil
		})
		e.must(err)
		e.must(e.Expect.Page(popup).ToHaveURL(docs + "/docs"))
		e.must(e.expect(".copy-toast").ToHaveAttribute("style", toastClear))
		if p.browser == chromium {
			if got := e.clipboard(); got != before {
				t.Errorf("a click on a link in a value: clipboard = %q, want %q", got, before)
			}
		}
	})
}

// TestPageCopyTableCell checks that a click on a table cell copies the
// cell's copy text, except on the ⓘ in it, and that a click on a validator's
// name opens its page without copying.
func TestPageCopyTableCell(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork(), withInstalledClock())
	e.runFor(time.Second)
	e.must(e.expect("#peers-body tr").ToHaveCount(4))
	eu := `#peers-body tr:has(td[data-copy="eu-node"])`

	for _, tc := range []struct{ cell, copied string }{
		{eu + " td:nth-child(1)", "eu-node"},
		{eu + " td:nth-child(2)", nodeID("eu-node")},
		{eu + " td:nth-child(3)", "Germany"},
		{eu + " td:nth-child(4)", node.Provider("Hetzner Online GmbH")},
	} {
		e.must(e.Page.Locator(tc.cell).Click(playwright.LocatorClickOptions{Position: &playwright.Position{X: 16, Y: 6}}))
		expectToast(e, "Copied!")
		if got := e.clipboard(); got != tc.copied {
			t.Errorf("click %s: clipboard = %q, want %q", tc.cell, got, tc.copied)
		}
		fadeToast(e)
	}

	e.must(e.Page.Locator(eu + " td:nth-child(1)").Click())
	expectToast(e, "Copied!")
	fadeToast(e)
	e.must(e.Page.Locator(eu + " td:nth-child(4) .info-btn").Click())
	e.must(e.expect(pagePopover).ToBeVisible())
	e.must(e.expect(".copy-toast").ToHaveAttribute("style", toastClear))
	if got := e.clipboard(); got != "eu-node" {
		t.Errorf("click the ⓘ in a cell: clipboard = %q, want it unchanged (eu-node)", got)
	}
	e.must(e.Page.Locator(`#validators-body a.val-link:has-text("bravo")`).Click())
	e.must(e.expect("#val-modal").ToHaveClass(regexp.MustCompile(`\bopen\b`)))
	e.must(e.expect(".copy-toast").ToHaveAttribute("style", toastClear))
	if got := e.clipboard(); got != "eu-node" {
		t.Errorf("click a validator's name: clipboard = %q, want it unchanged (eu-node)", got)
	}
}

// ---- Copy failures

// TestPageCopyFailureSaysSo checks that a copy the browser refuses, or a
// browser without a clipboard (a page served over plain HTTP), shows "Copy
// failed" in red and logs why, without an unhandled error.
func TestPageCopyFailureSaysSo(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		e := openPage(t, desktop, healthyNetwork(), withInstalledClock(), expectConsole(`^warning: copy failed`))
		e.runFor(time.Second)
		e.must(e.expect("#ns-consensus").ToContainText("h="))
		e.must(e.Ctx.ClearPermissions())
		e.must(e.Page.Locator("#ns-consensus").Click())
		expectToast(e, "Copy failed")
		e.must(e.expect(".copy-toast").ToHaveCSS("background-color", "rgb(248, 81, 73)"))
		e.must(e.expect(".copy-toast").ToHaveCSS("color", "rgb(13, 17, 23)"))
	})
	t.Run("no clipboard", func(t *testing.T) {
		onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
			e := openPage(t, p, healthyNetwork(), noNavigate(), withInstalledClock(),
				expectConsole(`^warning: copy failed: no Clipboard API`))
			e.must(e.Ctx.AddInitScript(playwright.Script{Content: playwright.String(`delete Navigator.prototype.clipboard`)}))
			e.navigate("/")
			e.runFor(time.Second)
			e.must(e.expect("#ns-consensus").ToContainText("h="))
			e.press(e.Page.Locator("#ns-consensus"))
			expectToast(e, "Copy failed")
			e.must(e.expect(".copy-toast").ToHaveCSS("background-color", "rgb(248, 81, 73)"))
			e.must(e.expect(".copy-toast").ToHaveCSS("color", "rgb(13, 17, 23)"))
		})
	})
}

// TestPageKeyboardCopyToast checks that a copy made from the keyboard shows
// its toast over the control pressed: the validator dialog's link button.
func TestPageKeyboardCopyToast(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork(), withInstalledClock())
	e.runFor(time.Second)
	e.must(e.Page.Locator(`a.val-link:visible:has-text("alpha")`).Click())
	e.must(e.expect("#val-close").ToBeFocused())
	// The dialog grows, and its header moves, as the report arrives.
	e.must(e.Expect.Locator(e.Page.Locator(`#val-body .val-sec[data-section="timeline"]`)).ToBeVisible())
	e.must(e.Page.Keyboard().Press("Shift+Tab"))
	e.must(e.expect("#val-link-btn").ToBeFocused())
	e.must(e.Page.Keyboard().Press("Enter"))
	expectToast(e, "Copied!")
	if got, want := e.clipboard(), e.ts.URL+"/?val="+valAlpha.address(); got != want {
		t.Errorf("clipboard = %q, want %q", got, want)
	}
	bb, tb := boxOf(e, "#val-link-btn"), boxOf(e, ".copy-toast")
	if math.Abs(tb.X+tb.Width/2-(bb.X+bb.Width/2)) > 1 || math.Abs(tb.Y-bb.Y) > 1 {
		t.Errorf("toast at (%.1f, %.1f) %.1f wide, want centred over the button at (%.1f, %.1f) %.1f wide",
			tb.X, tb.Y, tb.Width, bb.X, bb.Y, bb.Width)
	}
}

// ---- Storage

// TestPageFoldedSectionsSurviveABadRecord checks that a record of folded
// sections the page cannot read, null or broken JSON, leaves every section
// open and the folding working.
func TestPageFoldedSectionsSurviveABadRecord(t *testing.T) {
	for _, bad := range []string{"null", "{broken"} {
		t.Run(bad, func(t *testing.T) {
			onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
				key := mechCollapsedKey
				if p == phone {
					key = mechCollapsedMobileKey
				}
				e := openPage(t, p, healthyNetwork(), withStorage(map[string]string{key: bad}))
				e.must(e.expect("#ns-consensus").ToContainText("h="))
				for _, s := range []string{"network-state", "map", "validators", "peers"} {
					expectSectionFolded(e, s, false)
				}
				e.press(e.Page.Locator(sectionToggle("peers")))
				expectSectionFolded(e, "peers", true)
				expectStored(e, key, `{"peers":true}`)
			})
		})
	}
}

// TestPageAlertsSurviveBadPrefs checks that alert preferences the page
// cannot read, null or broken JSON, leave the alerts form as it starts.
func TestPageAlertsSurviveBadPrefs(t *testing.T) {
	for _, bad := range []string{"null", "{broken"} {
		t.Run(bad, func(t *testing.T) {
			sc := healthyNetwork()
			sc.push = true
			e := openPage(t, desktop, sc, withStorage(map[string]string{mechAlertPrefsKey: bad}))
			e.must(e.Page.Locator("#push-btn").Click())
			e.must(e.expect("#alerts-modal").ToBeVisible())
			e.must(e.Expect.Locator(e.Page.Locator("#alerts-form input[data-type]").First()).ToBeVisible())
		})
	}
}

// mechAlertPrefsKey holds the alerts form's last saved state.
const mechAlertPrefsKey = "gnockpitAlertPrefs"

// TestPageRunsWithoutStorage checks that a browser whose localStorage
// throws on every use (site data blocked) runs the page on its defaults:
// the chain data, folding, the compact toggle and the alerts.
func TestPageRunsWithoutStorage(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		sc := healthyNetwork()
		sc.push = p.hasPush()
		e := openPage(t, p, sc, noNavigate())
		e.must(e.Ctx.AddInitScript(playwright.Script{Content: playwright.String(storageBlocked)}))
		e.navigate("/")
		e.must(e.expect("#ns-consensus").ToContainText("h="))
		e.press(e.Page.Locator(sectionToggle("peers")))
		expectSectionFolded(e, "peers", true)
		if p == phone {
			// A phone's Network State opens compact.
			e.must(e.expect("#d-network").ToBeHidden())
			e.press(e.Page.Locator("#ns-compact-cb"))
			e.must(e.expect("#d-network").ToBeVisible())
			return
		}
		e.must(e.Page.Locator("#push-btn").Click())
		e.must(e.expect("#alerts-modal").ToBeVisible())
		e.must(e.Expect.Locator(e.Page.Locator("#alerts-form input[data-type]").First()).ToBeVisible())
	})
}

// ---- Dialog focus

// TestPageDialogKeepsFocus checks the validator dialog's focus trap: Tab
// goes round its controls without leaving it, Shift+Tab from the first goes
// to the last, and the focus comes back in from outside.
func TestPageDialogKeepsFocus(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork())
	e.must(e.Page.Locator(`a.val-link:visible:has-text("alpha")`).Click())
	e.must(e.Expect.Locator(e.Page.Locator("#val-body .val-sec").First()).ToBeVisible())
	e.must(e.expect("#val-close").ToBeFocused())

	// The dialog's controls, in Tab order, read once the focus is on each.
	controls := e.Page.Locator(`#val-modal :is(a[href], button:not(:disabled), input, [tabindex="0"])`)
	n, err := controls.Count()
	e.must(err)
	if n < 2 || n > mechDialogControls {
		t.Fatalf("the dialog has %d controls, want 2 to %d", n, mechDialogControls)
	}
	first, last := controls.First(), controls.Last()
	e.must(e.Expect.Locator(first).ToHaveId("val-link-btn"))
	// The dialog opens on its second control, #val-close.
	for i := 2; i < n; i++ {
		e.must(e.Page.Keyboard().Press("Tab"))
		e.must(e.Expect.Locator(controls.Nth(i)).ToBeFocused())
	}
	e.must(e.Page.Keyboard().Press("Tab"))
	e.must(e.Expect.Locator(first).ToBeFocused())
	e.must(e.Page.Keyboard().Press("Shift+Tab"))
	e.must(e.Expect.Locator(last).ToBeFocused())

	outside := e.Page.Locator(`#validators-view button[data-view="live"]`)
	e.must(outside.Focus())
	e.must(e.Page.Keyboard().Press("Tab"))
	e.must(e.Expect.Locator(first).ToBeFocused())
	e.must(outside.Focus())
	e.must(e.Page.Keyboard().Press("Shift+Tab"))
	e.must(e.Expect.Locator(last).ToBeFocused())
}

// mechDialogControls bounds the controls the validator dialog of the healthy
// network has: its two buttons and its ⓘ.
const mechDialogControls = 20

// mechFocusInside tells whether the focused element is inside the element
// selector matches.
func mechFocusInside(e *pageEnv, selector string) bool {
	e.T.Helper()
	v, err := e.Page.Evaluate(`(sel) => document.querySelector(sel).contains(document.activeElement)`, selector)
	e.must(err)
	return v == true
}

// mechFocusedName names the focused element: its id, else its tag and text.
func mechFocusedName(e *pageEnv) string {
	e.T.Helper()
	v, err := e.Page.Evaluate(`() => { const a = document.activeElement; return a.id || a.tagName + ' ' + a.textContent.trim().slice(0, 30) }`)
	e.must(err)
	s, _ := v.(string)
	return s
}

// ---- Breakpoints

// TestPageBreakpointsSplitAtTheirPixel checks each pair of the stylesheet's
// narrow and wide queries on either side of its edge: up to 768 px the node
// lists show as cards with their compact switches, from 769 px as tables
// without them; up to 640 px the validators card's header is a row, from
// 641 px a grid. A fractional width between the two stays untested:
// Playwright sets a viewport in whole pixels only, its protocol refusing a
// float.
func TestPageBreakpointsSplitAtTheirPixel(t *testing.T) {
	onProfiles(t, []profile{desktop, desktopWebKit}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		cards, table := e.expect("#validators-card .node-cards"), e.expect("#validators-card .table-wrap")
		compact := e.expect("#validators-compact-label")
		header := e.expect("#validators-card > .card-header-row")

		e.must(e.Page.SetViewportSize(768, 1000))
		e.must(cards.ToBeVisible())
		e.must(table.ToBeHidden())
		e.must(compact.ToBeVisible())
		e.must(e.Page.SetViewportSize(769, 1000))
		e.must(table.ToBeVisible())
		e.must(cards.ToBeHidden())
		e.must(compact.ToBeHidden())

		e.must(e.Page.SetViewportSize(640, 1000))
		e.must(header.ToHaveCSS("display", "flex"))
		e.must(e.Page.SetViewportSize(641, 1000))
		e.must(header.ToHaveCSS("display", "grid"))
	})
}

// ---- Hidden attribute

// TestPageHiddenSyncLine checks that the block-history sync line, a flex row
// in the Network State header, stays out of sight while it is hidden.
func TestPageHiddenSyncLine(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		e.must(e.expect("#ns-consensus").ToContainText("h="))
		e.must(e.expect("#ns-sync").ToHaveAttribute("hidden", ""))
		e.must(e.expect("#ns-sync").ToBeHidden())
	})
}

// ---- Colours

// TestPageDarkColorScheme checks that the page declares its dark colour
// scheme, which the browser draws native controls with.
func TestPageDarkColorScheme(t *testing.T) {
	onProfiles(t, []profile{desktop, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, bootScenario())
		e.must(e.expect("html").ToHaveCSS("color-scheme", "dark"))
	})
}

// TestPageUnreachableRPCIsGrey checks that a peer card shows an unreachable
// RPC as "closed" in grey, and a reachable one in green.
func TestPageUnreachableRPCIsGrey(t *testing.T) {
	onProfiles(t, []profile{tablet, phone}, func(t *testing.T, p profile) {
		e := openPage(t, p, healthyNetwork())
		card := func(moniker string) playwright.Locator {
			return e.Page.Locator("#peers-cards .peer-card").Filter(playwright.LocatorFilterOptions{HasText: moniker}).Locator(".peer-card-rpc-val")
		}
		e.must(e.Expect.Locator(card("full-norpc")).ToHaveText("closed"))
		e.must(e.Expect.Locator(card("full-norpc")).ToHaveCSS("color", "rgb(139, 148, 158)"))
		e.must(e.Expect.Locator(card("eu-node")).ToHaveCSS("color", "rgb(63, 185, 80)"))
	})
}

// ---- Formats

// TestPageNumberFormats checks how the validators table writes voting powers
// (B, M and K units), long address names (shortened) and vote delays (a sign,
// two decimals and a colour band).
func TestPageNumberFormats(t *testing.T) {
	sc := healthyNetwork()
	powers := []int64{1_500_000_000, 2_500_000, 1_500, 7}
	// The ends of the ok and warn bands, beyond them, and a delay too short
	// to sign.
	delays := []int{-1000, 2500, 2501, 4}
	for i := range sc.snap.Validators {
		sc.snap.Validators[i].VotingPower = strconv.FormatInt(powers[i], 10)
	}
	vals := healthyValidators()
	for i, v := range vals {
		vs := sc.snap.Signing.ValidatorSigning[v.address()]
		vs.VoteDelayMs = delays[i]
		sc.snap.Signing.ValidatorSigning[v.address()] = vs
	}
	e := openPage(t, desktop, sc)
	rows := e.Page.Locator("#validators-body tr")
	e.must(e.Expect.Locator(rows).ToHaveCount(4))

	// Rows by name: alpha, bravo, charlie, then delta's address.
	for i, want := range []struct{ vp, delay, band string }{
		{"1.5B", "−1.00 s", "ok"},
		{"2.5M", "+2.50 s", "warn"},
		{"1.5K", "+2.50 s", "err"},
		{"7", "0.00 s", "ok"},
	} {
		row := rows.Nth(i)
		e.must(e.Expect.Locator(row.Locator("td:nth-child(5)")).ToHaveText(regexp.MustCompile(`^` + regexp.QuoteMeta(want.vp) + ` \(\d+%\)$`)))
		delay := row.Locator("td:nth-child(9) span")
		e.must(e.Expect.Locator(delay).ToHaveText(want.delay))
		e.must(e.Expect.Locator(delay).ToHaveClass(want.band))
	}
	e.must(e.Expect.Locator(rows.Nth(3).Locator("td:nth-child(1) a.val-link")).ToHaveText(shortAddr(valDelta.address())))
	e.must(e.Expect.Locator(rows.Nth(0).Locator("td:nth-child(1) a.val-link")).ToHaveText("alpha"))
}

// TestPageProposalBands checks the colour of a validator's missed proposals
// over 24 h, by their share of those it was expected to make: none missed
// is ok, under 5 % a warning, 5 % and more an error; a validator expected to
// make none gets no colour.
func TestPageProposalBands(t *testing.T) {
	sc := healthyNetwork()
	sc.seed = func(t *testing.T, s *blocks.Store) {
		mechPutProposals(t, s, []mechProposals{
			{valAlpha, 20, 0},
			{valBravo, 20, 1},    // 5 %
			{valCharlie, 200, 1}, // 0.5 %
		})
	}
	e := openPage(t, desktop, sc)
	rows := e.Page.Locator("#validators-body tr")
	for i, want := range []struct{ text, class string }{
		{"0 / 22", "ok"}, // its own 20, and the 2 it made after the others missed
		{"1 / 20", "err"},
		{"1 / 200", "warn"},
		{"0 / 0", ""},
	} {
		props := rows.Nth(i).Locator("td:nth-child(10)")
		e.must(e.Expect.Locator(props).ToHaveText(want.text))
		e.must(e.Expect.Locator(props.Locator("span > span")).ToHaveClass(want.class))
	}
}

// mechProposals is a validator's proposals over the last day: those it was
// expected to make and those it missed.
type mechProposals struct {
	val              pageValidator
	expected, missed int
}

// mechPutProposals stores a block a minute up to the healthy network's head,
// each validator expected to propose as ps says: a block it made at round 0
// per proposal made, and per proposal missed a block alpha made at round 1
// after it.
func mechPutProposals(t *testing.T, s *blocks.Store, ps []mechProposals) {
	t.Helper()
	if err := s.PutValset(t.Context(), "PROPOSALS", members(healthyValidators()...)); err != nil {
		t.Fatal(err)
	}
	var recs []blocks.Record
	add := func(r blocks.Record) {
		r.ValsetHash = "PROPOSALS"
		recs = append(recs, r)
	}
	for _, p := range ps {
		for range p.expected - p.missed {
			add(blocks.Record{Proposer: p.val.address()})
		}
		for range p.missed {
			add(blocks.Record{Proposer: valAlpha.address(), Round: 1, Rounds: []string{p.val.address(), valAlpha.address()}})
		}
	}
	first := int64(pageHeight - len(recs) + 1)
	for i := range recs {
		recs[i].Height = first + int64(i)
		recs[i].Time = healthyBlockTime(recs[i].Height)
	}
	if err := s.Write(t.Context(), recs); err != nil {
		t.Fatal(err)
	}
}

// TestPageTimeFormats checks how long ago a time is written, and the UTC
// minute its popover gives: the first row of the validators timeline, the
// genesis set's, read at several distances from block 1.
func TestPageTimeFormats(t *testing.T) {
	genesis := time.Date(2026, 11, 15, 8, 7, 30, 123e6, time.UTC)
	for _, tc := range []struct {
		after time.Duration
		ago   string
	}{
		{-5 * time.Minute, "just now"},
		{29 * time.Second, "just now"},
		{30 * time.Second, "1 min ago"},
		{59*time.Minute + 29*time.Second, "59 min ago"},
		{59*time.Minute + 30*time.Second, "1 h ago"},
		{47*time.Hour + 29*time.Minute, "47 h ago"},
		{47*time.Hour + 30*time.Minute, "2 d ago"},
		{3*24*time.Hour + 12*time.Hour, "4 d ago"},
	} {
		t.Run(tc.ago+" "+tc.after.String(), func(t *testing.T) {
			e := openPage(t, desktop, mechTimelineScenario(genesis, genesis.Add(tc.after)))
			e.must(e.Page.Locator(`#validators-view button[data-view="timeline"]`).Click())
			h := e.Page.Locator("#validators-timeline .vt-row .h").First()
			e.must(e.Expect.Locator(h).ToHaveText("#1 · " + tc.ago))
			e.must(e.Expect.Locator(h).ToHaveAttribute("data-popover", "2026-11-15 08:07 UTC"))
			e.showPopover(h)
			e.must(e.expect(pagePopover).ToHaveText("2026-11-15 08:07 UTC"))
		})
	}
}

// mechTimelineScenario is a block history of three blocks a minute apart from
// genesis, read at now, without a live chain.
func mechTimelineScenario(genesis, now time.Time) *scenario {
	set := members(healthyValidators()...)
	at := func(h int64) time.Time { return genesis.Add(time.Duration(h-1) * time.Minute) }
	return &scenario{
		now: now,
		seed: func(t *testing.T, s *blocks.Store) {
			putBlocks(t, s, at, "GENESIS", set, 1, 3, nil)
		},
		fill: blocks.Progress{ChainID: pageChainID, From: 1, To: 3, FromTime: genesis, Complete: true},
	}
}

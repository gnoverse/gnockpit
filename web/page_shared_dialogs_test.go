//go:build page

package web

import (
	"net/url"
	"regexp"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// ---- Push menu

// The push menu (the Alerts dialog) and what the page keeps of it.
const (
	pushMenu       = "#alerts-modal"
	pushMenuError  = "#alerts-error"
	pushTestButton = "#push-test-btn"
	// pushDeviceIDKey is the page's localStorage key of the device's id on
	// the server.
	pushDeviceIDKey = "gnockpitPushSubID"
	// saveAnswerWait is how long a save waits for the server's answer, and
	// saveTimedOutText what the dialog or popover says then.
	saveAnswerWait   = 10 * time.Second
	saveTimedOutText = "The server did not answer in time. Try again."
)

// openPushMenu opens the Alerts dialog and waits for its form.
func (e *pageEnv) openPushMenu() {
	e.T.Helper()
	e.must(e.pushButton().Click())
	e.must(e.expect("#chk-chain_stuck").ToBeVisible())
}

// saveChainStuckAlert saves the chain-stuck alert alone and returns the device id
// the page stored.
func (e *pageEnv) saveChainStuckAlert() string {
	e.T.Helper()
	e.openPushMenu()
	e.setChecked(true, "chk-chain_stuck")
	e.savePushMenu()
	id, ok := e.storage(pushDeviceIDKey)
	if !ok || id == "" {
		e.T.Fatalf("localStorage %s = %q (set %t) after a save, want the device id", pushDeviceIDKey, id, ok)
	}
	return id
}

// countRequests counts the page's requests of method to path from now on.
func (e *pageEnv) countRequests(method, path string) *atomic.Int32 {
	var n atomic.Int32
	e.Page.OnRequest(func(r playwright.Request) {
		if r.Method() == method && pathOf(r.URL()) == path {
			n.Add(1)
		}
	})
	return &n
}

// pushButton is the header's Alerts button.
func (e *pageEnv) pushButton() playwright.Locator {
	return e.Page.GetByRole("button", playwright.PageGetByRoleOptions{Name: "Alerts", Exact: playwright.Bool(true)})
}

// savePushMenu clicks Save and waits for the dialog to close.
func (e *pageEnv) savePushMenu() {
	e.T.Helper()
	e.must(e.pushSaveButton().Click())
	e.must(e.expect(pushMenu).ToBeHidden())
}

// setChecked checks, or unchecks, the boxes of ids.
func (e *pageEnv) setChecked(on bool, ids ...string) {
	e.T.Helper()
	for _, id := range ids {
		e.must(e.Page.Locator("#" + id).SetChecked(on))
	}
}

// pushSaveButton is the dialog's Save button.
func (e *pageEnv) pushSaveButton() playwright.Locator {
	return e.Page.Locator(pushMenu).GetByRole("button", playwright.LocatorGetByRoleOptions{Name: "Save", Exact: playwright.Bool(true)})
}

// ---- Block ETA

// cardLink is the page path of a link card for height, with since and label
// when set.
func cardLink(height int64, since time.Time, label string) string {
	q := url.Values{"eta": {strconv.FormatInt(height, 10)}}
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339))
	}
	if label != "" {
		q.Set("label", label)
	}
	return "/?" + q.Encode()
}

// expectFailedCard checks that the card shows only msg, the error of its
// first answer: no count, target, chart or scenarios, and msg alone on the
// compact line.
func (e *pageEnv) expectFailedCard(card, msg string) {
	e.T.Helper()
	e.must(e.expect(card).ToBeVisible())
	e.must(e.expect(card).ToHaveClass(regexp.MustCompile(`\beta-failed\b.*\beta-norange\b|\beta-norange\b.*\beta-failed\b`)))
	e.must(e.expect(card + " .kv .k").ToHaveText([]string{"error"}))
	e.must(e.expect(card + " .kv .v").ToHaveText([]string{msg}))
	e.must(e.expect(card + " .eta-count").ToHaveText("–"))
	e.must(e.expect(card + " .eta-target").ToHaveText(""))
	e.must(e.expect(card + " .eta-inline").ToHaveText(msg))
	for _, hidden := range []string{".eta-count", ".eta-target", ".eta-chart", ".eta-scen-title", ".eta-bar"} {
		e.must(e.expect(card + " " + hidden).ToBeHidden())
	}
	e.must(e.expect(card + " .eta-scen .s").ToHaveCount(0))
}

// openPlanner opens the planner from the header.
func (e *pageEnv) openPlanner() {
	e.T.Helper()
	e.must(e.Page.Locator("#eta-btn").Click())
	e.must(e.expect("#eta-modal").ToHaveClass(regexp.MustCompile(`\bopen\b`)))
}

// plan types input in the planner.
func (e *pageEnv) plan(input string) {
	e.T.Helper()
	e.must(e.Page.Locator("#eta-in").Fill(input))
}

// notifySaveButton is the popover's Save button, which reads Update when
// the card is the device's reminder.
func (e *pageEnv) notifySaveButton() playwright.Locator {
	return e.Page.Locator(linkCard + " .eta-remind-save")
}

// openNotify opens the notify popover.
func (e *pageEnv) openNotify() {
	e.T.Helper()
	e.must(e.notifyButton().Click())
	e.must(e.Expect.Locator(e.notifyPopover()).ToBeVisible())
}

// linkCard is the link card, the one card with a notify button.
const linkCard = "#eta-link-card"

// notifyButton is the link card's notify button.
func (e *pageEnv) notifyButton() playwright.Locator {
	return e.Page.Locator(linkCard + " .eta-remind")
}

// notifyPopover is the notify popover.
func (e *pageEnv) notifyPopover() playwright.Locator {
	return e.Page.GetByRole("dialog", playwright.PageGetByRoleOptions{Name: "Notify this device"})
}

// ---- Validator page

// chartBox is the box of the validator page's chart key.
func (e *pageEnv) chartBox(key string) playwright.Locator {
	return e.Page.Locator("#val-body [data-chart=" + key + "]")
}

// sectionInfo is the selector of the ⓘ in the title of the validator
// page's section key.
func sectionInfo(key string) string {
	return "#val-body [data-section=" + key + "] > h3 > .info-btn"
}

// statItem is an item of a stats line as shown: its label, its value,
// and its value's band class, "" for none.
type statItem struct{ label, value, band string }

// statWindow is a window of a stats line: its name and items.
type statWindow struct {
	name  string
	stats []statItem
}

// expectStats waits until the stats line of section shows want;
// a failure names what the page shows.
func (e *pageEnv) expectStats(what, section string, want ...statWindow) {
	e.T.Helper()
	must := func(err error) {
		e.T.Helper()
		if err != nil {
			e.T.Fatalf("%s, %s: %v", what, section, err)
		}
	}
	wins := e.Page.Locator("#val-body [data-section=" + section + "] > .val-stats > .val-win")
	must(e.Expect.Locator(wins).ToHaveCount(len(want)))
	for i, w := range want {
		win := wins.Nth(i)
		must(e.Expect.Locator(win.Locator(":scope > .w")).ToHaveText(w.name))
		items := win.Locator(":scope > span:not(.w)")
		var labels, values []string
		for _, s := range w.stats {
			labels, values = append(labels, s.label), append(values, s.value)
		}
		must(e.Expect.Locator(items.Locator(":scope > .l")).ToHaveText(labels))
		must(e.Expect.Locator(items.Locator(":scope > b")).ToHaveText(values))
		for j, s := range w.stats {
			must(e.Expect.Locator(items.Nth(j).Locator(":scope > b")).ToHaveClass(s.band))
		}
	}
}

// modalOpenClass matches the validator page's modal while it is open.
var modalOpenClass = regexp.MustCompile(`\bopen\b`)

// expectValidatorReport waits until the modal shows a report titled title.
func (e *pageEnv) expectValidatorReport(title string) {
	e.T.Helper()
	e.must(e.expect("#val-page").ToBeVisible())
	e.must(e.expect("#val-title").ToHaveText(title))
	e.must(e.expect("#val-body [data-section=profile]").ToBeVisible())
}

// expectValidatorPageClosed waits until the modal is closed.
func (e *pageEnv) expectValidatorPageClosed() {
	e.T.Helper()
	e.must(e.expect("#val-modal").Not().ToHaveClass(modalOpenClass))
	e.must(e.expect("#val-page").ToBeHidden())
}

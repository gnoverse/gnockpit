//go:build page

package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/web/push"
)

const (
	alertsPerm = "#alerts-perm-status"
	// alertsPrefsKey is the page's localStorage key of the form as last
	// saved.
	alertsPrefsKey = "gnockpitAlertPrefs"
	// alertsSentShown is how long the Test button reads "Sent!".
	alertsSentShown = 2 * time.Second
	// alertsFailedToSave is the dialog's error when a save fails.
	alertsFailedToSave = "Failed to save. See console for details."
	// alertsSWReadyWait is how long a save waits for the service worker.
	alertsSWReadyWait = 10 * time.Second
	// alertsAnswerWait is how long the menu waits for the server's list of
	// validators, and for its answer to a test push.
	alertsAnswerWait = 10 * time.Second
)

// alertsProfiles are the profiles whose browser offers web push.
var alertsProfiles = []profile{desktop, tablet}

// alertsNetwork is the healthy network with push on, without the block
// history, which the alerts never read.
func alertsNetwork() *scenario {
	sc := healthyNetwork()
	sc.seed = nil
	sc.push = true
	return sc
}

// ---- Dialog helpers

// alertsExpectChecked asserts that the boxes of ids are checked, or unchecked.
func (e *pageEnv) alertsExpectChecked(on bool, ids ...string) {
	e.T.Helper()
	for _, id := range ids {
		e.must(e.expect("#" + id).ToBeChecked(playwright.LocatorAssertionsToBeCheckedOptions{Checked: playwright.Bool(on)}))
	}
}

// alertsBlockServiceWorkers makes service worker registration succeed without
// registering anything, as Playwright's "block" policy does in the scenarios
// without push: no service worker ever gets ready. Call it before the page
// loads.
func (e *pageEnv) alertsBlockServiceWorkers() {
	e.T.Helper()
	script := `if (self.ServiceWorkerContainer) ServiceWorkerContainer.prototype.register = async () => {}`
	if err := e.Ctx.AddInitScript(playwright.Script{Content: &script}); err != nil {
		e.T.Fatalf("block service workers: %v", err)
	}
}

// alertsValBoxID is the id of a validator's missing-blocks box.
func alertsValBoxID(v pageValidator) string { return "chk-validator_missing_blocks__" + v.address() }

// alertsValRecoveryID is the id of a validator's missing-blocks recovery box.
func alertsValRecoveryID(v pageValidator) string {
	return "rec-validator_missing_blocks__" + v.address()
}

// alertsValIDs returns the id of every healthy validator's box, alertsValBoxID or
// alertsValRecoveryID.
func alertsValIDs(id func(pageValidator) string) []string {
	var ids []string
	for _, v := range healthyValidators() {
		ids = append(ids, id(v))
	}
	return ids
}

// alertsValLabel is the label of a validator's missing-blocks alert: its name,
// then the start of its address.
func alertsValLabel(name string, v pageValidator) string {
	return name + " · " + v.address()[:14] + "…"
}

// ---- Tests

// TestPageAlertsButtonNeedsPush checks that the Alerts button shows only
// where push works: a browser with web push and a server with push on.
func TestPageAlertsButtonNeedsPush(t *testing.T) {
	t.Run("push", func(t *testing.T) {
		onProfiles(t, alertsProfiles, func(t *testing.T, p profile) {
			e := openPage(t, p, alertsNetwork())
			e.must(e.Expect.Locator(e.pushButton()).ToBeVisible())
		})
	})
	t.Run("server without push", func(t *testing.T) {
		sc := alertsNetwork()
		sc.push = false
		e := openPage(t, desktop, sc)
		e.must(e.expect("#d-network").ToHaveText(pageChainID))
		e.must(e.expect("#push-btn").ToBeHidden())
	})
	t.Run("browser without web push", func(t *testing.T) {
		e := openPage(t, phone, alertsNetwork(), noNavigate())
		vapid := e.countRequests(http.MethodGet, "/api/push/vapid-key")
		e.navigate("/")
		e.must(e.expect("#d-network").ToHaveText(pageChainID))
		e.must(e.expect("#push-btn").ToBeHidden())
		if n := vapid.Load(); n != 0 {
			t.Errorf("GET /api/push/vapid-key sent %d times, want none", n)
		}
	})
}

// TestPageAlertsButtonHiddenWhenPushInitFails checks that a failed read of
// the push key leaves the Alerts button hidden and says why in the console.
func TestPageAlertsButtonHiddenWhenPushInitFails(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork(), noNavigate(), expectConsole(`^warning: push init failed: TypeError`))
	vapid := e.hold("/api/push/vapid-key")
	vapid.drop()
	e.navigate("/")
	vapid.awaitFailure()
	e.must(e.expect("#d-network").ToHaveText(pageChainID))
	e.must(e.expect("#push-btn").ToBeHidden())
}

// TestPageAlertsButtonHiddenWhenPushKeyUnanswered checks that a read of the
// server's push key it never answers is given up after 10 s, with a warning
// in the console: the Alerts button stays hidden.
func TestPageAlertsButtonHiddenWhenPushKeyUnanswered(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork(), noNavigate(), withInstalledClock(),
		expectConsole(`^warning: push init failed: TimeoutError`))
	vapid := e.hold("/api/push/vapid-key")
	e.navigate("/")
	e.must(e.gate.awaitHeld("/api/push/vapid-key", 1))
	e.runFor(alertsAnswerWait)
	vapid.awaitAbandoned()
	e.must(e.expect("#d-network").ToHaveText(pageChainID))
	e.must(e.expect("#push-btn").ToBeHidden())
}

// TestPageAlertsPermissionLine checks the line on the browser's
// notification permission.
func TestPageAlertsPermissionLine(t *testing.T) {
	for _, tc := range []struct{ state, line string }{
		{"granted", "Notifications are enabled in this browser."},
		{"denied", "Notifications are blocked. Enable them in browser settings."},
		{"default", "You will be asked for permission when saving."},
	} {
		t.Run(tc.state, func(t *testing.T) {
			e := openPage(t, desktop, alertsNetwork(), withNotificationPermission(tc.state, tc.state))
			e.openPushMenu()
			e.must(e.expect(alertsPerm).ToHaveText(tc.line))
		})
	}
}

// TestPageAlertsForm checks the dialog's content: the chain-stuck alert
// with the server's threshold, the chain events with the approaching halt's
// lead, and every validator's missing-blocks alert with the server's run.
func TestPageAlertsForm(t *testing.T) {
	onProfiles(t, alertsProfiles, func(t *testing.T, p profile) {
		sc := alertsNetwork()
		sc.chainStuckSecs, sc.maxMissedInARow = 45, 7
		e := openPage(t, p, sc)
		e.openPushMenu()
		modal := e.Page.Locator(pushMenu)
		e.must(e.Expect.Locator(modal.GetByRole("heading")).ToHaveText("Alert Notifications"))
		e.must(e.Expect.Locator(modal.GetByRole("button", playwright.LocatorGetByRoleOptions{Name: "Close"})).ToHaveText("×"))
		e.must(e.expect(pushMenuError).ToBeEmpty())
		e.must(e.expect(pushTestButton).ToBeHidden())
		e.must(e.Expect.Locator(e.pushSaveButton()).ToBeVisible())

		e.must(e.expect(`label[for="chk-chain_stuck"]`).ToHaveText("Chain stuckfor 45s"))
		e.must(e.expect(`label[for="rec-chain_stuck"]`).ToHaveText("recovery"))
		for _, sel := range []string{"#rec-chain_stuck", `label[for="rec-chain_stuck"]`} {
			e.must(e.expect(sel).ToHaveAttribute("data-popover", "Also notify on recovery"))
		}

		e.must(e.expect(`label[for="chk-valset_changed"]`).ToHaveText("Validator set changesjoins, leaves, power"))
		e.must(e.expect(`label[for="chk-halt_scheduled"]`).ToHaveText("New coordinated halt"))
		e.must(e.expect(`label[for="chk-halt_approaching"]`).ToHaveText("Coordinated halt approaching"))
		e.must(e.Expect.Locator(e.Page.Locator(`#alerts-form`).GetByText("once per event, no recovery")).ToBeVisible())
		for _, id := range []string{"valset_changed", "halt_scheduled", "halt_approaching"} {
			e.must(e.expect("#rec-" + id).ToHaveCount(0))
		}
		lead := e.Page.GetByRole("spinbutton", playwright.PageGetByRoleOptions{Name: "lead", Exact: playwright.Bool(true)})
		e.must(e.Expect.Locator(lead).ToHaveValue("30"))
		e.must(e.Expect.Locator(lead).ToHaveAttribute("min", "1"))
		unit := e.Page.GetByRole("combobox", playwright.PageGetByRoleOptions{Name: "lead unit", Exact: playwright.Bool(true)})
		e.must(e.Expect.Locator(unit).ToHaveValue("m"))
		e.must(e.Expect.Locator(unit.Locator("option")).ToHaveText([]string{"minutes", "blocks"}))
		e.must(e.Expect.Locator(e.Page.Locator(`#alerts-form`).GetByText("before", playwright.LocatorGetByTextOptions{Exact: playwright.Bool(true)})).ToBeVisible())

		e.must(e.Expect.Locator(e.Page.Locator(`#alerts-form`).GetByText("7 in a row")).ToBeVisible())
		e.must(e.expect(`label[for="chk-val-all"]`).ToHaveText("all"))
		e.must(e.expect(`label[for="rec-val-all"]`).ToHaveText("all"))
		e.must(e.expect(`#alerts-form input[data-type="validator_missing_blocks"]`).ToHaveCount(len(healthyValidators())))
		for _, v := range healthyValidators() {
			e.must(e.expect("#"+alertsValBoxID(v)).ToHaveAttribute("data-entity", v.address()))
			e.must(e.expect("#"+alertsValRecoveryID(v)).ToHaveAttribute("data-rec-entity", v.address()))
		}
		for _, v := range []pageValidator{valBravo, valCharlie} {
			label := e.expect(`label[for="` + alertsValBoxID(v) + `"]`)
			e.must(label.ToHaveText(alertsValLabel(v.moniker, v)))
			e.must(label.ToHaveAttribute("data-popover", alertsValLabel(v.moniker, v)))
		}

		e.alertsExpectChecked(false, "chk-chain_stuck", "rec-chain_stuck", "chk-valset_changed", "chk-halt_scheduled",
			"chk-halt_approaching", "chk-val-all", "rec-val-all")
		e.alertsExpectChecked(false, alertsValIDs(alertsValBoxID)...)
		e.alertsExpectChecked(false, alertsValIDs(alertsValRecoveryID)...)
	})
}

// TestPageAlertsValidatorLabels checks the labels the server's names make:
// the source's own validator by its moniker, without the " (us)" its name
// carries, and a validator without a moniker, named by its address, by the
// start of its address alone.
func TestPageAlertsValidatorLabels(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork())
	e.openPushMenu()
	alpha := e.expect(`label[for="` + alertsValBoxID(valAlpha) + `"]`)
	e.must(alpha.ToHaveText(alertsValLabel("alpha", valAlpha)))
	e.must(alpha.ToHaveAttribute("data-popover", alertsValLabel("alpha", valAlpha)))
	delta := e.expect(`label[for="` + alertsValBoxID(valDelta) + `"]`)
	short := valDelta.address()[:14] + "…"
	e.must(delta.ToHaveText(short))
	e.must(delta.ToHaveAttribute("data-popover", short))
}

// TestPageAlertsDialogStyle checks that the dialog wears the page's modal
// styles: the panel's background and height, the muted and error colours,
// and buttons in the page's colours and font.
func TestPageAlertsDialogStyle(t *testing.T) {
	onProfiles(t, alertsProfiles, func(t *testing.T, p profile) {
		e := openPage(t, p, alertsNetwork())
		e.saveChainStuckAlert()
		e.openPushMenu()
		const (
			bg     = "rgb(13, 17, 23)"    // --bg
			bg2    = "rgb(22, 27, 34)"    // --bg2
			fg     = "rgb(201, 209, 217)" // --fg
			fg2    = "rgb(139, 148, 158)" // --fg2
			red    = "rgb(248, 81, 73)"   // --red
			border = "rgb(48, 54, 61)"    // --border
		)
		inner := e.expect("#alerts-modal-inner")
		e.must(inner.ToHaveCSS("background-color", bg2))
		e.must(inner.ToHaveCSS("overflow-y", "auto"))
		e.must(inner.ToHaveCSS("max-height", fmt.Sprintf("%gpx", 0.85*float64(p.viewport.Height))))
		e.must(e.expect(alertsPerm).ToHaveCSS("color", fg2))
		e.must(e.expect(pushMenuError).ToHaveCSS("color", red))
		e.must(e.Expect.Locator(e.Page.Locator("#alerts-form").GetByText("for 30s")).ToHaveCSS("color", fg2))
		e.must(e.expect(`label[for="rec-chain_stuck"]`).ToHaveCSS("color", fg2))

		v, err := e.Page.Evaluate(`() => getComputedStyle(document.body).fontFamily`)
		e.must(err)
		font, ok := v.(string)
		if !ok {
			t.Fatalf("body font-family = %T, want a string", v)
		}
		for _, b := range []playwright.Locator{e.Page.Locator(pushTestButton), e.pushSaveButton()} {
			btn := e.Expect.Locator(b)
			e.must(btn.ToHaveCSS("font-family", font))
			e.must(btn.ToHaveCSS("background-color", bg))
			e.must(btn.ToHaveCSS("border-color", border))
			e.must(btn.ToHaveCSS("color", fg))
		}
		closeBtn := e.Expect.Locator(e.Page.Locator(pushMenu).GetByRole("button", playwright.LocatorGetByRoleOptions{Name: "Close"}))
		e.must(closeBtn.ToHaveCSS("background-color", "rgba(0, 0, 0, 0)"))
		e.must(closeBtn.ToHaveCSS("border-style", "none"))
		e.must(closeBtn.ToHaveCSS("color", fg))
	})
}

// TestPageAlertsEntitiesUnavailable checks that the dialog says so when the
// server's list of validators cannot be read, the connection lost, a proxy's
// error answer or no answer in 10 s, and still offers the chain alerts, with
// main.go's thresholds.
func TestPageAlertsEntitiesUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name, why string
		opts      []pageOption
		// fail makes the read of the list fail.
		fail func(e *pageEnv)
	}{
		{name: "lost", why: "Failed to fetch", fail: func(e *pageEnv) {
			e.hold("/api/push/entities").drop()
			e.openPushMenu()
		}},
		{name: "proxy error", why: "HTTP 502", opts: []pageOption{noNavigate(), expectStatus("/api/push/entities", http.StatusBadGateway)}, fail: func(e *pageEnv) {
			front := e.behindProxy("/api/push/entities", http.StatusBadGateway)
			e.hold("/api/push/entities")
			e.navigateTo(front + "/")
			e.openPushMenu()
		}},
		{name: "no answer", why: "no answer in 10 s", opts: []pageOption{withInstalledClock()}, fail: func(e *pageEnv) {
			stuck := e.hold("/api/push/entities")
			e.must(e.pushButton().Click())
			e.must(e.gate.awaitHeld("/api/push/entities", 1))
			e.runFor(alertsAnswerWait)
			stuck.awaitAbandoned()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc := alertsNetwork()
			sc.chainStuckSecs, sc.maxMissedInARow = 45, 7
			e := openPage(t, desktop, sc, tc.opts...)
			tc.fail(e)
			e.must(e.expect(pushMenuError).ToHaveText("Could not read the validators (" + tc.why + ")."))
			e.must(e.expect(`label[for="chk-chain_stuck"]`).ToHaveText("Chain stuckfor 30s"))
			e.must(e.Expect.Locator(e.Page.Locator(`#alerts-form`).GetByText("10 in a row")).ToBeVisible())
			e.must(e.expect(`#alerts-form input[data-type="validator_missing_blocks"]`).ToHaveCount(0))
			e.must(e.expect("#chk-val-all").ToHaveCount(0))
		})
	}
}

// TestPageAlertsSaveKeepsUnshownValidatorAlerts checks a save while the
// server's list of validators cannot be read: the menu shows no validator,
// and the device's validator alerts, which it could not show, stay on the
// server and in what the page keeps of the form; the alerts it shows are
// saved as changed.
func TestPageAlertsSaveKeepsUnshownValidatorAlerts(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork())
	e.openPushMenu()
	e.setChecked(true, "chk-chain_stuck", alertsValRecoveryID(valBravo))
	e.savePushMenu()
	id, _ := e.storage(pushDeviceIDKey)
	bravo := push.SubscriptionAlert{AlertType: push.AlertValidatorMissingBlocks, EntityID: valBravo.address(), RecoveryNotif: true}
	expectDevice(t, e.Srv, id, []push.SubscriptionAlert{{AlertType: push.AlertChainStuck}, bravo})

	e.navigate("/")
	e.hold("/api/push/entities").drop()
	e.openPushMenu()
	e.must(e.expect(pushMenuError).ToHaveText("Could not read the validators (Failed to fetch)."))
	e.setChecked(true, "chk-valset_changed")
	e.savePushMenu()
	expectDevice(t, e.Srv, id, []push.SubscriptionAlert{
		{AlertType: push.AlertChainStuck}, bravo, {AlertType: push.AlertValsetChanged},
	})

	// The list reads again: the menu shows bravo's alert as the page kept
	// it.
	e.openPushMenu()
	e.alertsExpectChecked(true, "chk-chain_stuck", "chk-valset_changed", alertsValBoxID(valBravo), alertsValRecoveryID(valBravo))
}

// TestPageAlertsSaveNoneShownKeepsTheDevice checks a save with every alert
// the menu shows unchecked while the list of validators is unread: the
// device keeps its validator alerts, so it stays on the server and in the
// page, and the browser keeps its push subscription.
func TestPageAlertsSaveNoneShownKeepsTheDevice(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork())
	e.openPushMenu()
	e.setChecked(true, "chk-chain_stuck", alertsValBoxID(valBravo))
	e.savePushMenu()
	id, _ := e.storage(pushDeviceIDKey)

	e.navigate("/")
	e.hold("/api/push/entities").drop()
	e.openPushMenu()
	e.must(e.expect(pushMenuError).ToHaveText("Could not read the validators (Failed to fetch)."))
	e.setChecked(false, "chk-chain_stuck")
	e.savePushMenu()
	expectDevice(t, e.Srv, id, []push.SubscriptionAlert{{AlertType: push.AlertValidatorMissingBlocks, EntityID: valBravo.address()}})
	if got, ok := e.storage(pushDeviceIDKey); got != id {
		t.Errorf("localStorage %s = %q (%v), want the device's id %q kept", pushDeviceIDKey, got, ok, id)
	}

	// The list reads again: the menu shows bravo's alert as the page kept it,
	// and the device's Test button. The browser still holds its push
	// subscription: a save with nothing checked sends it, which a browser
	// without one does not.
	saves := e.countRequests(http.MethodPost, "/api/push/subscribe")
	e.openPushMenu()
	e.alertsExpectChecked(true, alertsValBoxID(valBravo))
	e.must(e.expect(pushTestButton).ToBeVisible())
	e.setChecked(false, alertsValBoxID(valBravo))
	e.savePushMenu()
	if n := saves.Load(); n != 1 {
		t.Errorf("POST /api/push/subscribe sent %d times, want once: the browser's push subscription stayed", n)
	}
	if d := serverDevices(t, e.Srv); len(d) != 0 {
		t.Errorf("server devices = %+v after a save with no alert, want none", d)
	}
}

// TestPageAlertsSaveKeepsValidatorAlertsWithoutConsensus checks a save while
// every source is down: the server answers the validators as unread, the
// menu says so, and the save keeps the device's validator alerts.
func TestPageAlertsSaveKeepsValidatorAlertsWithoutConsensus(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork(), expectStatus("/api/push/entities", http.StatusServiceUnavailable))
	e.openPushMenu()
	e.setChecked(true, "chk-chain_stuck", alertsValRecoveryID(valBravo))
	e.savePushMenu()
	id, _ := e.storage(pushDeviceIDKey)
	bravo := push.SubscriptionAlert{AlertType: push.AlertValidatorMissingBlocks, EntityID: valBravo.address(), RecoveryNotif: true}

	e.publish(sourcesDown())
	e.openPushMenu()
	e.must(e.expect(pushMenuError).ToHaveText("Could not read the validators (HTTP 503)."))
	e.setChecked(true, "chk-valset_changed")
	e.savePushMenu()
	expectDevice(t, e.Srv, id, []push.SubscriptionAlert{
		{AlertType: push.AlertChainStuck}, bravo, {AlertType: push.AlertValsetChanged},
	})
}

// TestPageAlertsSaveFromAnEmptyListKeepsValidatorAlerts checks a save while
// the server's list of validators is empty, its consensus state holding no
// vote: the menu shows no validator, and the device's validator alerts stay
// on the server as they are.
func TestPageAlertsSaveFromAnEmptyListKeepsValidatorAlerts(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork())
	e.openPushMenu()
	e.setChecked(true, "chk-chain_stuck", alertsValRecoveryID(valBravo))
	e.savePushMenu()
	id, _ := e.storage(pushDeviceIDKey)
	bravo := push.SubscriptionAlert{AlertType: push.AlertValidatorMissingBlocks, EntityID: valBravo.address(), RecoveryNotif: true}

	noVotes := healthySnapshot()
	noVotes.Consensus.Votes = nil
	e.publish(noVotes)
	e.openPushMenu()
	e.must(e.expect(`#alerts-form input[data-type="validator_missing_blocks"]`).ToHaveCount(0))
	e.setChecked(true, "chk-valset_changed")
	e.savePushMenu()
	expectDevice(t, e.Srv, id, []push.SubscriptionAlert{
		{AlertType: push.AlertChainStuck}, bravo, {AlertType: push.AlertValsetChanged},
	})
}

// TestPageAlertsSaveForgetsUncheckedBoxesItDoesNotShow checks what the page
// keeps of the form at a save: the boxes it shows as they are, and of those
// it does not show only the checked ones, whose alerts the server keeps. An
// unchecked box of a validator that left the set is forgotten.
func TestPageAlertsSaveForgetsUncheckedBoxesItDoesNotShow(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork())
	e.openPushMenu()
	e.setChecked(true, "chk-chain_stuck")
	e.savePushMenu()

	e.publish(alertsSetWithout(valBravo))
	e.openPushMenu()
	e.savePushMenu()
	stored, ok := e.storage(alertsPrefsKey)
	if !ok {
		t.Fatalf("localStorage %s unset after a save", alertsPrefsKey)
	}
	var prefs map[string]any
	if err := json.Unmarshal([]byte(stored), &prefs); err != nil {
		t.Fatalf("localStorage %s = %q: %v", alertsPrefsKey, stored, err)
	}
	for id, want := range map[string]any{"chk-chain_stuck": true, alertsValBoxID(valAlpha): false, alertsValRecoveryID(valAlpha): false} {
		if got, ok := prefs[id]; !ok || got != want {
			t.Errorf("stored %s = %v (set %t), want %v: the form shows it", id, got, ok, want)
		}
	}
	for _, id := range []string{alertsValBoxID(valBravo), alertsValRecoveryID(valBravo)} {
		if got, ok := prefs[id]; ok {
			t.Errorf("stored %s = %v, want none: the form does not show it, unchecked", id, got)
		}
	}
}

// alertsSetWithout is the healthy network's live state once v has left the
// validator set.
func alertsSetWithout(v pageValidator) *node.Snapshot {
	snap := healthySnapshot()
	snap.Consensus.Votes = slices.DeleteFunc(snap.Consensus.Votes, func(vote node.VoteInfo) bool { return vote.Address == v.address() })
	snap.Validators = slices.DeleteFunc(snap.Validators, func(val node.Validator) bool { return val.Address == v.address() })
	return snap
}

// TestPageAlertsSaveKeepsADepartedValidatorsAlert checks the alert on a
// validator that left the set: the menu does not offer it while it is out,
// and a save keeps it, on the server and in what the page keeps of the
// form. Once the validator is back, the menu shows its alert checked;
// unchecked there, a save removes it.
func TestPageAlertsSaveKeepsADepartedValidatorsAlert(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork())
	e.openPushMenu()
	e.setChecked(true, "chk-chain_stuck", alertsValRecoveryID(valBravo))
	e.savePushMenu()
	id, _ := e.storage(pushDeviceIDKey)
	bravo := push.SubscriptionAlert{AlertType: push.AlertValidatorMissingBlocks, EntityID: valBravo.address(), RecoveryNotif: true}

	e.publish(alertsSetWithout(valBravo))
	e.openPushMenu()
	e.must(e.expect("#" + alertsValBoxID(valBravo)).ToHaveCount(0))
	e.setChecked(true, "chk-valset_changed")
	e.savePushMenu()
	expectDevice(t, e.Srv, id, []push.SubscriptionAlert{
		{AlertType: push.AlertChainStuck}, bravo, {AlertType: push.AlertValsetChanged},
	})

	e.publish(healthySnapshot())
	e.openPushMenu()
	e.alertsExpectChecked(true, alertsValBoxID(valBravo), alertsValRecoveryID(valBravo))
	e.setChecked(false, alertsValBoxID(valBravo))
	e.savePushMenu()
	expectDevice(t, e.Srv, id, []push.SubscriptionAlert{
		{AlertType: push.AlertChainStuck}, {AlertType: push.AlertValsetChanged},
	})
}

// TestPageAlertsCheckRules checks how the boxes follow each other: a
// recovery needs its alert, and the "all" boxes set and follow the
// validators' boxes.
func TestPageAlertsCheckRules(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork())
	e.openPushMenu()

	// A recovery needs its alert.
	e.setChecked(true, "rec-chain_stuck")
	e.alertsExpectChecked(true, "chk-chain_stuck")
	e.setChecked(false, "chk-chain_stuck")
	e.alertsExpectChecked(false, "rec-chain_stuck")

	// The alerts' "all" sets every validator's alert, and follows them.
	bravoAlert, bravoRec := alertsValBoxID(valBravo), alertsValRecoveryID(valBravo)
	e.setChecked(true, "chk-val-all")
	e.alertsExpectChecked(true, alertsValIDs(alertsValBoxID)...)
	e.alertsExpectChecked(false, alertsValIDs(alertsValRecoveryID)...)
	e.alertsExpectChecked(false, "rec-val-all")
	e.setChecked(false, bravoAlert)
	e.alertsExpectChecked(false, "chk-val-all")
	e.setChecked(true, bravoAlert)
	e.alertsExpectChecked(true, "chk-val-all")
	e.setChecked(false, bravoAlert)

	// The recoveries' "all" sets every recovery, every alert and the alerts'
	// "all".
	e.setChecked(true, "rec-val-all")
	e.alertsExpectChecked(true, alertsValIDs(alertsValRecoveryID)...)
	e.alertsExpectChecked(true, alertsValIDs(alertsValBoxID)...)
	e.alertsExpectChecked(true, "chk-val-all")

	// Unchecking the alerts' "all" unchecks every alert and recovery.
	e.setChecked(false, "chk-val-all")
	e.alertsExpectChecked(false, alertsValIDs(alertsValBoxID)...)
	e.alertsExpectChecked(false, alertsValIDs(alertsValRecoveryID)...)
	e.alertsExpectChecked(false, "rec-val-all")

	// Checking every recovery one by one checks their alerts and both "all".
	e.setChecked(true, bravoRec)
	e.alertsExpectChecked(true, bravoAlert)
	e.alertsExpectChecked(false, "chk-val-all", "rec-val-all")
	e.setChecked(true, alertsValIDs(alertsValRecoveryID)...)
	e.alertsExpectChecked(true, alertsValIDs(alertsValBoxID)...)
	e.alertsExpectChecked(true, "chk-val-all", "rec-val-all")

	// Unchecking an alert unchecks its recovery and both "all".
	e.setChecked(false, bravoAlert)
	e.alertsExpectChecked(false, bravoRec, "chk-val-all", "rec-val-all")
}

// TestPageAlertsRecoveryAllFollowsEachRecovery checks that unchecking one
// validator's recovery unchecks the recoveries' "all", and keeps the alerts.
func TestPageAlertsRecoveryAllFollowsEachRecovery(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork())
	e.openPushMenu()
	e.setChecked(true, "rec-val-all")
	e.setChecked(false, alertsValRecoveryID(valBravo))
	e.alertsExpectChecked(false, "rec-val-all")
	e.alertsExpectChecked(true, "chk-val-all", alertsValBoxID(valBravo))
}

// TestPageAlertsQuotedAddress checks the check rules and the save of a
// validator whose address holds a quote, as a chain may send one: an alert's
// box and its recovery's pair whatever the address holds.
func TestPageAlertsQuotedAddress(t *testing.T) {
	const addr = `g1"quoted`
	sc := alertsNetwork()
	sc.snap.Consensus.Votes = append(sc.snap.Consensus.Votes, node.VoteInfo{Index: len(sc.snap.Consensus.Votes), Address: addr})
	e := openPage(t, desktop, sc)
	e.openPushMenu()
	const alertBox, recBox = "chk-validator_missing_blocks__g1_quoted", "rec-validator_missing_blocks__g1_quoted"
	e.setChecked(true, recBox)
	e.alertsExpectChecked(true, alertBox)
	e.setChecked(false, alertBox)
	e.alertsExpectChecked(false, recBox)
	e.setChecked(true, recBox)
	e.savePushMenu()
	id, _ := e.storage(pushDeviceIDKey)
	expectDevice(t, e.Srv, id, []push.SubscriptionAlert{
		{AlertType: push.AlertValidatorMissingBlocks, EntityID: addr, RecoveryNotif: true},
	})
}

// TestPageAlertsClose checks that the × and a click beside the dialog close
// it, and that a click inside it does not.
func TestPageAlertsClose(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork())
	e.openPushMenu()
	e.must(e.Page.Locator(pushMenu).GetByRole("heading").Click())
	e.must(e.expect(pushMenu).ToBeVisible())
	e.must(e.Page.Locator(pushMenu).GetByRole("button", playwright.LocatorGetByRoleOptions{Name: "Close"}).Click())
	e.must(e.expect(pushMenu).ToBeHidden())

	e.openPushMenu()
	e.must(e.Page.Locator(pushMenu).Click(playwright.LocatorClickOptions{Position: &playwright.Position{X: 5, Y: 5}}))
	e.must(e.expect(pushMenu).ToBeHidden())
}

// TestPageAlertsDialogKeyboard checks the dialog for the keyboard and screen
// readers: a modal dialog named by its heading, focus on its Close button
// when it opens, Tab and Shift+Tab kept inside it, Escape closing it, and
// focus back on Alerts however it closes.
func TestPageAlertsDialogKeyboard(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork())
	e.openPushMenu()
	dialog := e.Page.GetByRole("dialog", playwright.PageGetByRoleOptions{Name: "Alert Notifications"})
	e.must(e.Expect.Locator(dialog).ToBeVisible())
	e.must(e.Expect.Locator(dialog).ToHaveAttribute("aria-modal", "true"))
	closeBtn := dialog.GetByRole("button", playwright.LocatorGetByRoleOptions{Name: "Close"})
	save := dialog.GetByRole("button", playwright.LocatorGetByRoleOptions{Name: "Save"})
	e.must(e.Expect.Locator(closeBtn).ToBeFocused())
	e.must(e.Page.Keyboard().Press("Shift+Tab"))
	e.must(e.Expect.Locator(save).ToBeFocused())
	e.must(e.Page.Keyboard().Press("Tab"))
	e.must(e.Expect.Locator(closeBtn).ToBeFocused())
	e.must(e.Page.Keyboard().Press("Escape"))
	e.must(e.expect(pushMenu).ToBeHidden())
	e.must(e.Expect.Locator(e.pushButton()).ToBeFocused())

	for _, close := range []struct {
		how   string
		close func() error
	}{
		{"the Close button", func() error { return closeBtn.Click() }},
		{"a click beside it", func() error {
			return e.Page.Locator(pushMenu).Click(playwright.LocatorClickOptions{Position: &playwright.Position{X: 5, Y: 5}})
		}},
		{"a save", func() error { return save.Click() }},
	} {
		e.openPushMenu()
		e.must(close.close())
		e.must(e.expect(pushMenu).ToBeHidden())
		if err := e.Expect.Locator(e.pushButton()).ToBeFocused(); err != nil {
			t.Errorf("closed by %s: %v", close.how, err)
		}
	}
}

// TestPageAlertsSave checks a save: the server holds the device and its
// alerts, the page keeps the device id and the form, which a later opening,
// and a later load, show again, with the Test button.
func TestPageAlertsSave(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork())
	e.openPushMenu()
	e.setChecked(true, "rec-chain_stuck", "chk-valset_changed", "chk-halt_approaching", alertsValBoxID(valBravo))
	e.must(e.Page.Locator("#lead-halt_approaching-n").Fill("2"))
	_, err := e.Page.Locator("#lead-halt_approaching-unit").SelectOption(playwright.SelectOptionValues{Values: &[]string{"blocks"}})
	e.must(err)
	e.savePushMenu()

	devices := serverDevices(t, e.Srv)
	if len(devices) != 1 {
		t.Fatalf("server devices = %+v, want one", devices)
	}
	dev := devices[0]
	if id, _ := e.storage(pushDeviceIDKey); id != dev.ID {
		t.Errorf("localStorage %s = %q, want the server's device id %q", pushDeviceIDKey, id, dev.ID)
	}
	if !strings.HasPrefix(dev.Endpoint, e.Push.srv.URL+"/push/") {
		t.Errorf("device endpoint = %q, want one on the push service %s", dev.Endpoint, e.Push.srv.URL)
	}
	for _, k := range []struct {
		name, value string
		size        int
	}{{"p256dh", dev.P256dh, 65}, {"auth", dev.Auth, 16}} {
		b, err := base64.StdEncoding.DecodeString(k.value)
		if err != nil || len(b) != k.size {
			t.Errorf("device %s = %q: %d bytes of base64 (%v), want %d", k.name, k.value, len(b), err, k.size)
		}
	}
	expectDevice(t, e.Srv, dev.ID, []push.SubscriptionAlert{
		{AlertType: push.AlertChainStuck, RecoveryNotif: true},
		{AlertType: push.AlertHaltApproaching, Lead: "2 blocks"},
		{AlertType: push.AlertValidatorMissingBlocks, EntityID: valBravo.address()},
		{AlertType: push.AlertValsetChanged},
	})

	prefs := map[string]any{
		"chk-chain_stuck": true, "rec-chain_stuck": true, "chk-valset_changed": true,
		"chk-halt_scheduled": false, "chk-halt_approaching": true,
		"lead-halt_approaching-n": "2", "lead-halt_approaching-unit": "blocks",
	}
	for _, v := range healthyValidators() {
		prefs[alertsValBoxID(v)] = v == valBravo
		prefs[alertsValRecoveryID(v)] = false
	}
	raw, _ := e.storage(alertsPrefsKey)
	var got map[string]any
	if err := json.Unmarshal([]byte(raw), &got); err != nil || !reflect.DeepEqual(got, prefs) {
		t.Errorf("localStorage %s = %s (%v), want %v", alertsPrefsKey, raw, err, prefs)
	}

	expectRestored := func(n, unit string) {
		t.Helper()
		e.openPushMenu()
		e.alertsExpectChecked(true, "chk-chain_stuck", "rec-chain_stuck", "chk-valset_changed", "chk-halt_approaching", alertsValBoxID(valBravo))
		e.alertsExpectChecked(false, "chk-halt_scheduled", "chk-val-all", "rec-val-all", alertsValRecoveryID(valBravo), alertsValBoxID(valCharlie))
		e.must(e.expect("#lead-halt_approaching-n").ToHaveValue(n))
		e.must(e.expect("#lead-halt_approaching-unit").ToHaveValue(unit))
		e.must(e.expect(pushTestButton).ToBeVisible())
	}
	expectRestored("2", "blocks")

	e.must(e.Page.Locator("#lead-halt_approaching-n").Fill("45"))
	_, err = e.Page.Locator("#lead-halt_approaching-unit").SelectOption(playwright.SelectOptionValues{Values: &[]string{"m"}})
	e.must(err)
	e.savePushMenu()
	if id, _ := e.storage(pushDeviceIDKey); id != dev.ID {
		t.Errorf("localStorage %s = %q after a second save, want the same device %q", pushDeviceIDKey, id, dev.ID)
	}
	expectDevice(t, e.Srv, dev.ID, []push.SubscriptionAlert{
		{AlertType: push.AlertChainStuck, RecoveryNotif: true},
		{AlertType: push.AlertHaltApproaching, Lead: "45m"},
		{AlertType: push.AlertValidatorMissingBlocks, EntityID: valBravo.address()},
		{AlertType: push.AlertValsetChanged},
	})

	e.navigate("/")
	expectRestored("45", "m")
}

// TestPageAlertsSaveInFlight checks that Save is off, reading "Saving…",
// until the server answers, and on again after.
func TestPageAlertsSaveInFlight(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork())
	e.openPushMenu()
	e.setChecked(true, "chk-chain_stuck")
	saveBtn := e.Page.Locator("#push-save-btn")
	subscribe := e.hold("/api/push/subscribe")
	e.must(saveBtn.Click())
	e.must(e.Expect.Locator(saveBtn).ToHaveText("Saving…"))
	e.must(e.Expect.Locator(saveBtn).ToBeDisabled())
	subscribe.release()
	e.must(e.expect(pushMenu).ToBeHidden())
	e.openPushMenu()
	e.must(e.Expect.Locator(saveBtn).ToHaveText("Save"))
	e.must(e.Expect.Locator(saveBtn).ToBeEnabled())
}

// TestPageAlertsServiceWorkerNotReady checks that a save waits 10 s at most
// for the browser's service worker, then fails, says so and gives Save back.
func TestPageAlertsServiceWorkerNotReady(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork(), noNavigate(), withInstalledClock(),
		expectConsole(`^warning: save alerts: Error: service worker not ready`))
	e.alertsBlockServiceWorkers()
	e.navigate("/")
	e.openPushMenu()
	e.setChecked(true, "chk-chain_stuck")
	saveBtn := e.Page.Locator("#push-save-btn")
	e.must(saveBtn.Click())
	e.must(e.Expect.Locator(saveBtn).ToBeDisabled())
	e.runFor(alertsSWReadyWait - time.Second)
	e.must(e.Expect.Locator(saveBtn).ToBeDisabled())
	e.must(e.expect(pushMenuError).ToBeEmpty())
	e.runFor(time.Second)
	e.must(e.expect(pushMenuError).ToHaveText(alertsFailedToSave))
	e.must(e.Expect.Locator(saveBtn).ToHaveText("Save"))
	e.must(e.Expect.Locator(saveBtn).ToBeEnabled())
	e.must(e.expect(pushMenu).ToBeVisible())
	if d := serverDevices(t, e.Srv); len(d) != 0 {
		t.Errorf("server devices = %+v, want none", d)
	}
}

// TestPageAlertsSaveTimesOut checks that a save the server does not answer
// within 10 s is given up, says so, and gives Save back, on Chromium and on
// WebKit, which rejects the given-up request with an error of its own.
func TestPageAlertsSaveTimesOut(t *testing.T) {
	onProfiles(t, []profile{desktop, desktopWebKit}, func(t *testing.T, p profile) {
		e := openPage(t, p, alertsNetwork(), withInstalledClock(),
			expectConsole(`^warning: save alerts: TimeoutError`))
		e.openPushMenu()
		e.setChecked(true, "chk-chain_stuck")
		saveBtn := e.Page.Locator("#push-save-btn")
		subscribe := e.hold("/api/push/subscribe")
		e.must(saveBtn.Click())
		e.must(e.gate.awaitHeld("/api/push/subscribe", 1))
		e.runFor(saveAnswerWait - time.Second)
		e.must(e.Expect.Locator(saveBtn).ToBeDisabled())
		e.runFor(time.Second)
		subscribe.awaitAbandoned()
		e.must(e.expect(pushMenuError).ToHaveText(saveTimedOutText))
		e.must(e.Expect.Locator(saveBtn).ToBeEnabled())
		e.must(e.Expect.Locator(saveBtn).ToBeFocused())
		if d := serverDevices(t, e.Srv); len(d) != 0 {
			t.Errorf("server devices = %+v, want none", d)
		}
	})
}

// TestPageAlertsLeadMustBePositive checks that an approaching halt's lead
// under 1 is refused before anything is saved, and only when that alert is
// on.
func TestPageAlertsLeadMustBePositive(t *testing.T) {
	for _, lead := range []string{"0", ""} {
		t.Run("lead "+lead, func(t *testing.T) {
			e := openPage(t, desktop, alertsNetwork())
			e.openPushMenu()
			e.setChecked(true, "chk-halt_approaching")
			e.must(e.Page.Locator("#lead-halt_approaching-n").Fill(lead))
			e.must(e.pushSaveButton().Click())
			e.must(e.expect(pushMenuError).ToHaveText("Enter how far ahead of the halt, 1 or more."))
			e.must(e.expect(pushMenu).ToBeVisible())
			if d := serverDevices(t, e.Srv); len(d) != 0 {
				t.Errorf("server devices = %+v, want none", d)
			}

			e.setChecked(false, "chk-halt_approaching")
			e.setChecked(true, "chk-chain_stuck")
			e.savePushMenu()
			id, _ := e.storage(pushDeviceIDKey)
			expectDevice(t, e.Srv, id, []push.SubscriptionAlert{{AlertType: push.AlertChainStuck}})
		})
	}
}

// TestPageAlertsSaveNeedsPermission checks that a save with alerts asks for
// the notification permission when the browser has not given it, and saves
// nothing without it.
func TestPageAlertsSaveNeedsPermission(t *testing.T) {
	t.Run("denied", func(t *testing.T) {
		e := openPage(t, desktop, alertsNetwork(), withNotificationPermission("denied", "denied"))
		e.openPushMenu()
		e.setChecked(true, "chk-chain_stuck")
		e.must(e.pushSaveButton().Click())
		e.must(e.expect(pushMenuError).ToHaveText("Notification permission denied."))
		e.must(e.expect(pushMenu).ToBeVisible())
		if d := serverDevices(t, e.Srv); len(d) != 0 {
			t.Errorf("server devices = %+v, want none", d)
		}
	})
	t.Run("granted when asked", func(t *testing.T) {
		e := openPage(t, desktop, alertsNetwork(), withNotificationPermission("default", "granted"))
		id := e.saveChainStuckAlert()
		expectDevice(t, e.Srv, id, []push.SubscriptionAlert{{AlertType: push.AlertChainStuck}})
		e.openPushMenu()
		e.must(e.expect(alertsPerm).ToHaveText("Notifications are enabled in this browser."))
	})
}

// TestPageAlertsSaveNone checks a save with no alert: the server forgets
// the device, the page its id, and the browser its push subscription, so a
// further save with no alert sends nothing, and one with alerts subscribes
// the browser again.
func TestPageAlertsSaveNone(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork())
	e.saveChainStuckAlert()
	saves := e.countRequests(http.MethodPost, "/api/push/subscribe")

	e.openPushMenu()
	e.setChecked(false, "chk-chain_stuck")
	e.savePushMenu()
	if d := serverDevices(t, e.Srv); len(d) != 0 {
		t.Errorf("server devices = %+v after a save with no alert, want none", d)
	}
	if id, ok := e.storage(pushDeviceIDKey); ok {
		t.Errorf("localStorage %s = %q after a save with no alert, want none", pushDeviceIDKey, id)
	}
	e.openPushMenu()
	e.must(e.expect(pushTestButton).ToBeHidden())
	e.savePushMenu()
	if n := saves.Load(); n != 1 {
		t.Errorf("POST /api/push/subscribe sent %d times, want once: none without a push subscription", n)
	}

	id := e.saveChainStuckAlert()
	expectDevice(t, e.Srv, id, []push.SubscriptionAlert{{AlertType: push.AlertChainStuck}})
}

// TestPageAlertsSaveFails checks that a save the server does not take says
// so, in the dialog and the console, and keeps the dialog open.
func TestPageAlertsSaveFails(t *testing.T) {
	t.Run("unanswered", func(t *testing.T) {
		e := openPage(t, desktop, alertsNetwork(), expectConsole(`^warning: save alerts: TypeError`))
		e.hold("/api/push/subscribe").drop()
		e.openPushMenu()
		e.setChecked(true, "chk-chain_stuck")
		e.must(e.pushSaveButton().Click())
		e.must(e.expect(pushMenuError).ToHaveText(alertsFailedToSave))
		e.must(e.expect(pushMenu).ToBeVisible())

		// The error goes with the dialog.
		e.must(e.Page.Locator("#alerts-close").Click())
		e.openPushMenu()
		e.must(e.expect(pushMenuError).ToBeEmpty())
	})
	t.Run("refused", func(t *testing.T) {
		e := openPage(t, desktop, alertsNetwork(),
			expectStatus("/api/push/subscribe", http.StatusInternalServerError),
			expectConsole(`^warning: save alerts: 500 save subscription`))
		e.must(e.Srv.PushManager.DB().Close())
		e.openPushMenu()
		e.setChecked(true, "chk-chain_stuck")
		e.must(e.pushSaveButton().Click())
		e.must(e.expect(pushMenuError).ToHaveText(alertsFailedToSave))
		e.must(e.expect(pushMenu).ToBeVisible())
	})
}

// TestPageAlertsTestPush checks the Test button: it sends a test push
// through the push service to this device, says so, then reads Test again.
func TestPageAlertsTestPush(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork(), withInstalledClock())
	id := e.saveChainStuckAlert()
	e.openPushMenu()
	test := e.hold("/api/push/test")
	e.must(e.Page.Locator(pushTestButton).Click())
	e.must(e.expect(pushTestButton).ToHaveText("Sending…"))
	e.must(e.expect(pushTestButton).ToBeDisabled())
	test.release()
	e.must(e.expect(pushTestButton).ToHaveText("Sent!"))
	e.must(e.expect(pushTestButton).ToBeDisabled())
	e.must(e.expect(pushMenuError).ToBeEmpty())

	got := e.Push.received()
	devices := serverDevices(t, e.Srv)
	if len(got) != 1 || len(devices) != 1 || got[0].endpoint != devices[0].Endpoint || got[0].ttl != 30 || got[0].bodyLen == 0 {
		t.Errorf("push service received %+v, want one push with TTL 30 to device %s %+v", got, id, devices)
	}

	e.runFor(alertsSentShown)
	e.must(e.expect(pushTestButton).ToHaveText("Test"))
	e.must(e.expect(pushTestButton).ToBeEnabled())
}

// TestPageAlertsTestPushRefused checks that a test push the push service
// refuses says so, with the server's 502, and gives the Test button back.
func TestPageAlertsTestPushRefused(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork(), expectStatus("/api/push/test", http.StatusBadGateway))
	e.saveChainStuckAlert()
	e.openPushMenu()
	e.Push.failNext(http.StatusGone)
	e.must(e.Page.Locator(pushTestButton).Click())
	e.must(e.expect(pushMenuError).ToHaveText("Test notification failed (502)."))
	e.must(e.expect(pushTestButton).ToHaveText("Test"))
	e.must(e.expect(pushTestButton).ToBeEnabled())
	if got := e.Push.received(); len(got) != 1 {
		t.Errorf("push service received %+v, want the one test push", got)
	}
}

// TestPageAlertsTestPushForgetsAnUnknownDevice checks that a test push for a
// device the server does not know drops the stored device id and the Test
// button.
func TestPageAlertsTestPushForgetsAnUnknownDevice(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork(),
		withStorage(map[string]string{pushDeviceIDKey: "forgotten"}),
		expectStatus("/api/push/test", http.StatusNotFound))
	e.openPushMenu()
	e.must(e.Page.Locator(pushTestButton).Click())
	e.must(e.expect(pushMenuError).ToHaveText("Test notification failed (404). Save to register this device again."))
	e.must(e.expect(pushTestButton).ToBeHidden())
	if id, ok := e.storage(pushDeviceIDKey); ok {
		t.Errorf("localStorage %s = %q after a 404, want none", pushDeviceIDKey, id)
	}
	e.navigate("/")
	e.openPushMenu()
	e.must(e.expect(pushTestButton).ToBeHidden())
}

// TestPageAlertsFailureKeepsFocus checks that a failed save, and a test
// push for a device the server does not know, say so in the dialog's alert
// and leave the keyboard on Save, inside the dialog.
func TestPageAlertsFailureKeepsFocus(t *testing.T) {
	saveBtn := func(e *pageEnv) playwright.Locator { return e.Page.Locator("#push-save-btn") }
	alert := func(e *pageEnv) playwright.LocatorAssertions {
		return e.Expect.Locator(e.Page.Locator(pushMenu).GetByRole("alert"))
	}
	t.Run("save", func(t *testing.T) {
		e := openPage(t, desktop, alertsNetwork(), expectConsole(`^warning: save alerts: TypeError`))
		e.openPushMenu()
		e.setChecked(true, "chk-chain_stuck")
		e.must(alert(e).ToHaveText(""))
		e.hold("/api/push/subscribe").drop()
		e.must(saveBtn(e).Focus())
		e.must(e.Page.Keyboard().Press("Enter"))
		e.must(alert(e).ToHaveText(alertsFailedToSave))
		e.must(e.Expect.Locator(saveBtn(e)).ToBeFocused())
	})
	t.Run("unknown device", func(t *testing.T) {
		e := openPage(t, desktop, alertsNetwork(),
			withStorage(map[string]string{pushDeviceIDKey: "forgotten"}),
			expectStatus("/api/push/test", http.StatusNotFound))
		e.openPushMenu()
		e.must(e.Page.Locator(pushTestButton).Focus())
		e.must(e.Page.Keyboard().Press("Enter"))
		e.must(alert(e).ToHaveText("Test notification failed (404). Save to register this device again."))
		e.must(e.Expect.Locator(saveBtn(e)).ToBeFocused())
	})
}

// TestPageAlertsLateSaveLeavesFocus checks that a save which ends after
// its dialog was closed leaves the keyboard where the user took it.
func TestPageAlertsLateSaveLeavesFocus(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork())
	e.openPushMenu()
	e.setChecked(true, "chk-chain_stuck")
	saveBtn := e.Page.Locator("#push-save-btn")
	subscribe := e.hold("/api/push/subscribe")
	e.must(saveBtn.Click())
	e.must(e.Expect.Locator(saveBtn).ToBeDisabled())
	e.must(e.Page.Keyboard().Press("Escape"))
	e.must(e.expect(pushMenu).ToBeHidden())
	eta := e.Page.Locator("#eta-btn")
	e.must(eta.Focus())
	subscribe.release()
	e.must(e.Expect.Locator(saveBtn).ToHaveText("Save"))
	e.must(e.Expect.Locator(eta).ToBeFocused())
}

// TestPageAlertsTestPushUnanswered checks that a test push the server never
// answers says so, in the dialog and the console, and gives the Test button
// back.
func TestPageAlertsTestPushUnanswered(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork(), expectConsole(`^error: push test failed: TypeError`))
	e.saveChainStuckAlert()
	e.openPushMenu()
	e.hold("/api/push/test").drop()
	e.must(e.Page.Locator(pushTestButton).Click())
	e.must(e.expect(pushMenuError).ToHaveText("Test notification failed. See console."))
	e.must(e.expect(pushTestButton).ToHaveText("Test"))
	e.must(e.expect(pushTestButton).ToBeEnabled())
}

// TestPageAlertsTestPushTimesOut checks that a test push the server does not
// answer within 10 s is given up: the dialog and the console say it failed,
// and the Test button is back.
func TestPageAlertsTestPushTimesOut(t *testing.T) {
	e := openPage(t, desktop, alertsNetwork(), withInstalledClock(),
		expectConsole(`^error: push test failed: TimeoutError`))
	e.saveChainStuckAlert()
	e.openPushMenu()
	stuck := e.hold("/api/push/test")
	e.must(e.Page.Locator(pushTestButton).Click())
	e.must(e.gate.awaitHeld("/api/push/test", 1))
	e.runFor(alertsAnswerWait - time.Second)
	e.must(e.expect(pushTestButton).ToHaveText("Sending…"))
	e.must(e.expect(pushTestButton).ToBeDisabled())
	e.runFor(time.Second)
	stuck.awaitAbandoned()
	e.must(e.expect(pushMenuError).ToHaveText("Test notification failed. See console."))
	e.must(e.expect(pushTestButton).ToHaveText("Test"))
	e.must(e.expect(pushTestButton).ToBeEnabled())
}

// TestPageAlertsMarkupStaysText checks that a validator's name with markup
// in it shows as text in the dialog.
func TestPageAlertsMarkupStaysText(t *testing.T) {
	const markup = `<img src=x onerror=alert(1)>`
	sc := alertsNetwork()
	sc.names[valBravo.address()] = markup
	e := openPage(t, desktop, sc)
	e.Page.OnDialog(func(d playwright.Dialog) {
		t.Errorf("the page opened a %s dialog: %q", d.Type(), d.Message())
		if err := d.Dismiss(); err != nil {
			t.Errorf("dismiss the dialog: %v", err)
		}
	})
	e.openPushMenu()
	label := e.expect(`label[for="` + alertsValBoxID(valBravo) + `"]`)
	e.must(label.ToHaveText(alertsValLabel(markup, valBravo)))
	e.must(label.ToHaveAttribute("data-popover", alertsValLabel(markup, valBravo)))
	e.must(e.expect("#alerts-form img").ToHaveCount(0))
}

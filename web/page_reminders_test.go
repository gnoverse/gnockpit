//go:build page

package web

import (
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/gnoverse/gnockpit/web/push"
)

// ---- Notify button and popover

// TestPageRemindersNotifyButton checks that the link card offers to notify
// this device where push works: a browser with web push and a server with
// push on.
func TestPageRemindersNotifyButton(t *testing.T) {
	path := cardLink(farHeight, pageNow.Add(-time.Hour), "")
	t.Run("push", func(t *testing.T) {
		onProfiles(t, reminderProfiles, func(t *testing.T, p profile) {
			e, _ := openPushChainPage(t, p, fullView(), path)
			e.reminderExpectButton("notify", false)
			btn := e.Expect.Locator(e.notifyButton())
			e.must(btn.ToHaveAttribute("aria-expanded", "false"))
			e.must(btn.ToHaveAttribute("aria-haspopup", "dialog"))
			e.must(btn.ToHaveAttribute("data-popover", "Notify this device before this height"))
			e.must(e.Expect.Locator(e.notifyPopover()).ToBeHidden())
		})
	})
	t.Run("server without push", func(t *testing.T) {
		e, _ := openChainPage(t, desktop, runningChain, fullView(), path)
		e.must(e.expect(linkCard + " .eta-target").ToContainText("#1,252,000"))
		e.must(e.Expect.Locator(e.notifyButton()).ToBeHidden())
	})
	t.Run("browser without web push", func(t *testing.T) {
		e, _ := openPushChainPage(t, phone, fullView(), path)
		e.must(e.expect(linkCard + " .eta-target").ToContainText("#1,252,000"))
		e.must(e.Expect.Locator(e.notifyButton()).ToBeHidden())
	})
}

// TestPageRemindersPopover checks the notify popover: what it offers for a
// card without a reminder, and how it closes: Cancel, Escape and the button
// give the keyboard back to the button, a click elsewhere on the page does
// not, a click inside leaves it open.
func TestPageRemindersPopover(t *testing.T) {
	e, _ := openPushChainPage(t, desktop, fullView(), cardLink(farHeight, time.Time{}, ""))
	btn := e.Expect.Locator(e.notifyButton())
	pop := e.Expect.Locator(e.notifyPopover())
	const sel = linkCard + " .eta-remind-pop"

	e.openNotify()
	e.must(btn.ToHaveAttribute("aria-expanded", "true"))
	e.must(e.expect(sel + " .t").ToHaveText("Notify this device"))
	e.must(e.Expect.Locator(e.reminderLead()).ToBeFocused())
	e.must(e.Expect.Locator(e.reminderLead()).ToHaveValue("30"))
	e.must(e.Expect.Locator(e.reminderUnit()).ToHaveValue("m"))
	e.must(e.expect(sel + " option").ToHaveText([]string{"minutes", "blocks"}))
	e.must(e.expect(sel + " .eta-remind-before").ToHaveText("before #1,252,000"))
	e.must(e.expect(sel + " .warn").ToBeHidden())
	e.must(e.expect(sel + " .err").ToBeHidden())
	e.must(e.Expect.Locator(e.notifySaveButton()).ToHaveText("Save"))
	e.must(e.Expect.Locator(e.reminderDialogButton("Remove")).ToBeHidden())
	e.must(e.Expect.Locator(e.reminderDialogButton("Cancel")).ToBeVisible())
	e.must(e.expect(sel + " .note").ToHaveText(
		"A push notification on this device only. It comes back with this card when the app opens without a link."))

	// A click inside keeps it open.
	e.must(e.Page.Locator(sel + " .t").Click())
	e.must(pop.ToBeVisible())

	closed := func(refocused bool) {
		t.Helper()
		e.must(pop.ToBeHidden())
		e.must(btn.ToHaveAttribute("aria-expanded", "false"))
		if refocused {
			e.must(btn.ToBeFocused())
		} else {
			e.must(btn.Not().ToBeFocused())
		}
	}
	e.must(e.reminderDialogButton("Cancel").Click())
	closed(true)

	e.openNotify()
	e.must(e.Page.Keyboard().Press("Escape"))
	closed(true)

	e.openNotify()
	e.must(e.notifyButton().Click())
	closed(true)

	e.openNotify()
	e.must(e.Page.Locator("#page-title").Click())
	closed(false)
}

// TestPageRemindersPopoverFieldsSize checks the popover's field and choice:
// 16 px on screens 768 px wide or less, where iOS zooms into a smaller one
// on focus, and the popover's 12 px on wider ones. At a phone's width the
// popover spans the card.
func TestPageRemindersPopoverFieldsSize(t *testing.T) {
	sized := func(t *testing.T, e *pageEnv, size string) {
		t.Helper()
		e.openNotify()
		e.must(e.Expect.Locator(e.reminderLead()).ToHaveCSS("font-size", size))
		e.must(e.Expect.Locator(e.reminderUnit()).ToHaveCSS("font-size", size))
	}
	path := cardLink(farHeight, time.Time{}, "")
	onProfiles(t, reminderProfiles, func(t *testing.T, p profile) {
		e, _ := openPushChainPage(t, p, fullView(), path)
		size := "12px"
		if e.narrow() {
			size = "16px"
		}
		sized(t, e, size)
	})
	t.Run("phone width", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), path)
		vp := pw.Devices[phone.device].Viewport
		e.must(e.Page.SetViewportSize(vp.Width, vp.Height))
		sized(t, e, "16px")
		pop, err := e.notifyPopover().BoundingBox()
		e.must(err)
		card, err := e.Page.Locator(linkCard).BoundingBox()
		e.must(err)
		// 12 px in from the card's padding box, under its header row.
		left, right := pop.X-card.X, card.X+card.Width-(pop.X+pop.Width)
		if math.Abs(left-right) > 0.5 || left < 12 || left > 14 || pop.Y-card.Y < 40 || pop.Y-card.Y > 52 {
			t.Errorf("popover at %.1f px from the card's left, %.1f from its right, %.1f below its top; want 12 px in on both sides (plus the border), 48 px down",
				left, right, pop.Y-card.Y)
		}
	})
}

// TestPageRemindersFieldsOnATouchScreen checks the fields on a touch
// screen wider than 768 px, a large iPhone held sideways, where iOS zooms
// into any field under 16 px on focus: the reminder popover's lead and unit,
// the planner's input, and the alerts' lead are 16 px. WebKit, the iPhone's
// engine, offers no push there: its run checks the planner; Chromium on the
// same screen and touch checks every field.
func TestPageRemindersFieldsOnATouchScreen(t *testing.T) {
	path := cardLink(farHeight, time.Time{}, "")
	sized := func(e *pageEnv, selector string) {
		e.T.Helper()
		e.must(e.expect(selector).ToHaveCSS("font-size", "16px"))
	}
	t.Run("webkit", func(t *testing.T) {
		e, _ := openPushChainPage(t, profile{name: "webkit", browser: webkit, device: pageLandscapeIPhone}, fullView(), path)
		e.openPlanner()
		sized(e, "#eta-in")
	})
	t.Run("chromium", func(t *testing.T) {
		e, _ := openPushChainPage(t, profile{name: "chromium", browser: chromium, device: pageLandscapeIPhone}, fullView(), path)
		e.openNotify()
		sized(e, linkCard+" .eta-remind-n")
		sized(e, linkCard+" .eta-remind-unit")
		e.must(e.Page.Keyboard().Press("Escape"))
		e.openPlanner()
		sized(e, "#eta-in")
		e.must(e.Page.Keyboard().Press("Escape"))
		e.openPushMenu()
		sized(e, "#lead-halt_approaching-n")
		sized(e, "#lead-halt_approaching-unit")
	})
}

// TestPageRemindersTargetChangeClosesThePopover checks that a new link
// target closes the popover for good: closing the card with the popover
// open leaves the next target's button collapsed.
func TestPageRemindersTargetChangeClosesThePopover(t *testing.T) {
	e, _ := openPushChainPage(t, desktop, fullView(), cardLink(farHeight, time.Time{}, ""))
	btn := e.Expect.Locator(e.notifyButton())
	e.openNotify()
	e.must(e.Page.Locator(linkCard + " .eta-close").Click())
	e.must(e.expect(linkCard).ToBeHidden())
	e.must(btn.ToHaveAttribute("aria-expanded", "false"))

	e.openPlanner()
	e.plan("1251000")
	e.must(e.expect("#eta-go").ToBeEnabled())
	e.must(e.Page.Locator("#eta-go").Click())
	e.must(e.expect(linkCard + " .eta-target").ToContainText("#1,251,000"))
	e.reminderExpectButton("notify", false)
	e.must(btn.ToHaveAttribute("aria-expanded", "false"))
	e.must(e.Expect.Locator(e.notifyPopover()).ToBeHidden())
}

// TestPageRemindersOnlyBeforeTheTarget checks that the notify button shows
// only on a card counting down to its target: not once the target is
// reached, closing an open popover, nor on a card whose answer is an error;
// a card that keeps an earlier answer through a failed poll keeps it.
func TestPageRemindersOnlyBeforeTheTarget(t *testing.T) {
	t.Run("reached", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), cardLink(reachedHeight, time.Time{}, ""))
		e.must(e.expect(linkCard + " .eta-count").ToHaveText("reached"))
		e.must(e.Expect.Locator(e.notifyButton()).ToBeHidden())
	})
	t.Run("error", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), "/?eta=1,250,000", expectStatus("/api/eta", http.StatusBadRequest))
		e.expectFailedCard(linkCard, `height must be a positive block height, got "1,250,000"`)
		e.must(e.Expect.Locator(e.notifyButton()).ToBeHidden())
	})
	t.Run("reached while open", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), cardLink(soonHeight, time.Time{}, ""), withInstalledClock())
		e.openNotify()
		e.publish(reminderHeadAt(soonHeight))
		e.runFor(cardPoll)
		e.must(e.expect(linkCard + " .eta-count").ToHaveText("reached"))
		e.must(e.Expect.Locator(e.notifyButton()).ToBeHidden())
		e.must(e.Expect.Locator(e.notifyPopover()).ToBeHidden())
		e.must(e.Expect.Locator(e.notifyButton()).ToHaveAttribute("aria-expanded", "false"))
	})
	t.Run("stale", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), cardLink(soonHeight, time.Time{}, ""),
			withInstalledClock(), expectStatus("/api/eta", http.StatusServiceUnavailable))
		e.must(e.expect(linkCard + " .eta-target").ToHaveText("until #1,244,542 (300 blocks to go)"))
		e.publish(sourcesDown())
		e.runFor(cardPoll)
		e.must(e.expect(linkCard + " .eta-inline .warn").ToHaveCount(1))
		e.reminderExpectButton("notify", false)
	})
}

// TestPageRemindersHaltAtTheTargetClosesThePopover checks a halt scheduled
// at the card's height while its popover is open: the halt card takes over,
// the popover closes, and the card comes back collapsed once the halt goes.
func TestPageRemindersHaltAtTheTargetClosesThePopover(t *testing.T) {
	e, tr := openPushChainPage(t, desktop, fullView(), cardLink(haltHeight, time.Time{}, ""), withInstalledClock())
	btn := e.Expect.Locator(e.notifyButton())
	e.openNotify()
	tr.set(haltedView())
	e.runFor(cardPoll)
	e.must(e.expect("#eta-halt-card").ToBeVisible())
	e.must(e.expect(linkCard).ToBeHidden())
	e.must(btn.ToHaveAttribute("aria-expanded", "false"))

	tr.set(fullView())
	e.runFor(cardPoll)
	e.must(e.expect(linkCard).ToBeVisible())
	e.reminderExpectButton("notify", false)
	e.must(btn.ToHaveAttribute("aria-expanded", "false"))
	e.must(e.Expect.Locator(e.notifyPopover()).ToBeHidden())
}

// ---- Saving

// TestPageRemindersSave checks a save: the server holds the card's height,
// since, label and lead for this device, which keeps its id; the popover
// closes, and the button reads the lead in blue. A card of the same height
// is the reminder's whatever its since and label; another height offers to
// replace it.
func TestPageRemindersSave(t *testing.T) {
	since := pageNow.Add(-time.Hour)
	onProfiles(t, reminderProfiles, func(t *testing.T, p profile) {
		e, _ := openPushChainPage(t, p, fullView(), cardLink(farHeight, since, "v1.9"))
		e.reminderSave("30", "m")
		e.must(e.Expect.Locator(e.notifyButton()).ToBeFocused())
		e.must(e.Expect.Locator(e.notifyButton()).ToHaveAttribute("aria-expanded", "false"))
		e.reminderExpectButton("notify · 30 min", true)
		id := e.reminderDevice()
		devices := serverDevices(t, e.Srv)
		if len(devices) != 1 || devices[0].ID != id {
			t.Fatalf("server devices = %+v, want the one the page stored, %s", devices, id)
		}
		want := push.Reminder{Chain: pageChainID, Height: farHeight, Since: since.Format(time.RFC3339), Label: "v1.9", Lead: reminderLeadOf(t, "30m")}
		reminderExpectStored(t, e.Srv, id, want)

		// Reopened, it offers to update or remove the reminder, from its lead.
		for _, tc := range []struct{ n, unit, lead, text, shownN string }{
			{"120", "m", "2h", "notify · 2 h", "120"},
			{"90", "m", "90m", "notify · 90 min", "90"},
			{"1", "blocks", "1 block", "notify · 1 block", "1"},
			{"100", "blocks", "100 blocks", "notify · 100 blocks", "100"},
		} {
			e.openNotify()
			e.must(e.Expect.Locator(e.notifySaveButton()).ToHaveText("Update"))
			e.must(e.Expect.Locator(e.reminderDialogButton("Remove")).ToBeVisible())
			e.must(e.expect(linkCard + " .eta-remind-pop .warn").ToBeHidden())
			e.must(e.Page.Keyboard().Press("Escape"))
			e.reminderSave(tc.n, tc.unit)
			e.reminderExpectButton(tc.text, true)
			want.Lead = reminderLeadOf(t, tc.lead)
			reminderExpectStored(t, e.Srv, id, want)
			e.openNotify()
			e.must(e.Expect.Locator(e.reminderLead()).ToHaveValue(tc.shownN))
			e.must(e.Expect.Locator(e.reminderUnit()).ToHaveValue(tc.unit))
			e.must(e.Page.Keyboard().Press("Escape"))
		}

		// The same height under another since and label is the reminder's.
		e.navigate(cardLink(farHeight, pageNow.Add(-2*time.Hour), "other"))
		e.reminderExpectButton("notify · 100 blocks", true)

		// Another height offers to replace it: one reminder per device.
		const other int64 = 1_251_000
		e.navigate(cardLink(other, since, ""))
		e.reminderExpectButton("notify", false)
		e.openNotify()
		e.must(e.expect(linkCard + " .eta-remind-pop .warn").ToHaveText("Replaces your reminder for #1,252,000."))
		e.must(e.Expect.Locator(e.notifySaveButton()).ToHaveText("Save"))
		e.must(e.Expect.Locator(e.reminderDialogButton("Remove")).ToBeHidden())
		e.must(e.notifySaveButton().Click())
		e.reminderExpectButton("notify · 30 min", true)
		if got := e.reminderDevice(); got != id {
			t.Errorf("device id = %q after another save, want %q", got, id)
		}
		reminderExpectStored(t, e.Srv, id, push.Reminder{Chain: pageChainID, Height: other, Since: since.Format(time.RFC3339), Lead: reminderLeadOf(t, "30m")})
	})
}

// TestPageRemindersSaveAsksPermission checks that a save asks for the
// notification permission when the browser has not given it, and saves
// nothing without it.
func TestPageRemindersSaveAsksPermission(t *testing.T) {
	path := cardLink(farHeight, time.Time{}, "")
	t.Run("denied", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), path, withNotificationPermission("denied", "denied"))
		e.openNotify()
		e.must(e.notifySaveButton().Click())
		e.reminderExpectError("Notification permission denied.")
		if d := serverDevices(t, e.Srv); len(d) != 0 {
			t.Errorf("server devices = %+v, want none", d)
		}
		e.reminderExpectButton("notify", false)
	})
	t.Run("granted when asked", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), path, withNotificationPermission("default", "granted"))
		e.reminderSave("30", "m")
		e.reminderExpectButton("notify · 30 min", true)
		if _, ok := reminderOf(t, e.Srv, e.reminderDevice()); !ok {
			t.Error("no reminder on the server after the save")
		}
	})
}

// TestPageRemindersSaveFails checks the saves the server does not take:
// the popover stays open and says why, in the server's words, or that the
// save failed on the way; nothing is saved, and the error goes with the
// popover.
func TestPageRemindersSaveFails(t *testing.T) {
	for _, tc := range []struct {
		name    string
		n, unit string
		opts    []pageOption
		cause   func(e *pageEnv)
		msg     string
	}{
		{
			name: "no chain status", n: "30", unit: "m",
			opts:  []pageOption{expectStatus("/api/push/reminder", http.StatusServiceUnavailable)},
			cause: func(e *pageEnv) { e.publish(sourcesDown()) },
			msg:   "no chain status yet",
		},
		{
			name: "store failure", n: "30", unit: "m",
			opts:  []pageOption{expectStatus("/api/push/reminder", http.StatusInternalServerError)},
			cause: func(e *pageEnv) { e.must(e.Srv.PushManager.DB().Close()) },
			msg:   "save reminder",
		},
		{
			// More minutes than a lead holds.
			name: "lead out of range", n: "153722868", unit: "m",
			opts: []pageOption{expectStatus("/api/push/reminder", http.StatusBadRequest)},
			msg:  `lead "153722868m": want "<N> blocks" or whole minutes, such as "30m" or "1h"`,
		},
		{
			name: "unanswered", n: "30", unit: "m",
			opts:  []pageOption{expectConsole(`^warning: save reminder: TypeError`)},
			cause: func(e *pageEnv) { e.hold("/api/push/reminder").drop() },
			msg:   reminderFailedToSave,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := openPushChainPage(t, desktop, fullView(), cardLink(farHeight, time.Time{}, ""),
				append(tc.opts, withInstalledClock())...)
			e.must(e.expect(linkCard + " .eta-target").ToHaveText("until #1,252,000 (7,758 blocks to go)"))
			if tc.cause != nil {
				tc.cause(e)
			}
			e.openNotify()
			e.reminderFill(tc.n, tc.unit)
			e.must(e.notifySaveButton().Click())
			e.reminderExpectError(tc.msg)
			e.reminderExpectButton("notify", false)
			if id, ok := e.storage(pushDeviceIDKey); ok {
				t.Errorf("localStorage %s = %q after a failed save, want none", pushDeviceIDKey, id)
			}

			e.must(e.reminderDialogButton("Cancel").Click())
			e.openNotify()
			e.must(e.expect(linkCard + " .eta-remind-pop .err").ToBeHidden())
		})
	}
}

// TestPageRemindersLeadAlreadyMet checks the save of a lead the chain
// already meets: the popover says the height is that lead away or less,
// exactly the lead included, or that it is reached.
func TestPageRemindersLeadAlreadyMet(t *testing.T) {
	for _, tc := range []struct {
		name    string
		n, unit string
		head    int64 // the chain's newest block at the save; the live tip when zero
		msg     string
	}{
		{name: "in time", n: "30", unit: "m", msg: "#1244542 is 30m away or less"},
		{name: "exactly the blocks", n: "300", unit: "blocks", msg: "#1244542 is 300 blocks away or less"},
		{name: "largest exact lead", n: "9007199254740991", unit: "blocks", msg: "#1244542 is 9007199254740991 blocks away or less"},
		{name: "reached", n: "30", unit: "m", head: soonHeight, msg: "#1244542 is already reached"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _ := openPushChainPage(t, desktop, fullView(), cardLink(soonHeight, time.Time{}, ""),
				withInstalledClock(), expectStatus("/api/push/reminder", http.StatusConflict))
			e.must(e.expect(linkCard + " .eta-target").ToHaveText("until #1,244,542 (300 blocks to go)"))
			if tc.head != 0 {
				e.publish(reminderHeadAt(tc.head))
			}
			e.openNotify()
			e.reminderFill(tc.n, tc.unit)
			e.must(e.notifySaveButton().Click())
			e.reminderExpectError(tc.msg)
			if d := serverDevices(t, e.Srv); len(d) != 0 {
				t.Errorf("server devices = %+v after a refused save, want none", d)
			}
		})
	}
}

// TestPageRemindersLeadMustBeAWholeNumber checks that the lead takes whole
// numbers from 1 only, and none past what the page counts exactly: anything
// else is refused before anything is saved.
func TestPageRemindersLeadMustBeAWholeNumber(t *testing.T) {
	const notWhole, tooFar = "Enter how far ahead, 1 or more.", "That is too far ahead."
	e, _ := openPushChainPage(t, desktop, fullView(), cardLink(farHeight, time.Time{}, ""))
	for _, tc := range []struct{ n, msg string }{
		{"2.5", notWhole},
		{"1e3", notWhole},
		{"", notWhole},
		{"0", notWhole},
		{"9007199254740992", tooFar},
		{"99999999999999999999", tooFar},
	} {
		e.openNotify()
		e.reminderFill(tc.n, "blocks")
		e.must(e.notifySaveButton().Click())
		e.reminderExpectError(tc.msg)
		if d := serverDevices(t, e.Srv); len(d) != 0 {
			t.Fatalf("server devices = %+v after a lead of %q, want none", d, tc.n)
		}
		e.must(e.reminderDialogButton("Cancel").Click())
	}
	e.reminderSave("7", "blocks")
	e.reminderExpectButton("notify · 7 blocks", true)
}

// TestPageRemindersSaveInFlight checks that Save is off and reads Saving…
// while its request is out, a poll's read of the reminder meanwhile
// included, so a second click sends nothing; and on again after: the
// popover closes on success; on a failure Save keeps the keyboard.
func TestPageRemindersSaveInFlight(t *testing.T) {
	path := cardLink(soonHeight, time.Time{}, "")
	t.Run("saved", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), path, withInstalledClock())
		save := e.Expect.Locator(e.notifySaveButton())
		e.saveChainStuckAlert()
		put := e.hold("/api/push/reminder")
		// The next poll comes 5 s into the save, well within its bound.
		e.runFor(cardPoll / 2)
		e.openNotify()
		e.reminderFill("100", "blocks")
		e.must(e.notifySaveButton().Click())
		e.must(e.gate.awaitHeld("/api/push/reminder", 1))
		e.must(save.ToHaveText("Saving…"))
		e.must(save.ToBeDisabled())
		e.runFor(cardPoll / 2)
		e.must(save.ToHaveText("Saving…"))
		e.must(save.ToBeDisabled())
		put.release()
		e.must(e.Expect.Locator(e.notifyPopover()).ToBeHidden())
		e.reminderExpectButton("notify · 100 blocks", true)
		e.openNotify()
		e.must(save.ToHaveText("Update"))
		e.must(save.ToBeEnabled())
	})
	t.Run("refused", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), path, expectStatus("/api/push/reminder", http.StatusConflict))
		save := e.Expect.Locator(e.notifySaveButton())
		put := e.hold("/api/push/reminder")
		e.openNotify()
		e.must(e.notifySaveButton().Focus())
		e.must(e.Page.Keyboard().Press("Enter"))
		e.must(e.gate.awaitHeld("/api/push/reminder", 1))
		e.must(save.ToHaveText("Saving…"))
		e.must(save.ToBeDisabled())
		put.release()
		e.reminderExpectError("#1244542 is 30m away or less")
		e.must(save.ToHaveText("Save"))
		e.must(save.ToBeEnabled())
		e.must(save.ToBeFocused())
	})
}

// TestPageRemindersSaveTimesOut checks that a save the server does not
// answer within 10 s is given up: the popover says so and Save is back, on
// Chromium and on WebKit, which rejects the given-up request with an error
// of its own.
func TestPageRemindersSaveTimesOut(t *testing.T) {
	onProfiles(t, []profile{desktop, desktopWebKit}, func(t *testing.T, p profile) {
		e, _ := openPushChainPage(t, p, fullView(), cardLink(farHeight, time.Time{}, ""),
			withInstalledClock(), expectConsole(`^warning: save reminder: TimeoutError`))
		save := e.Expect.Locator(e.notifySaveButton())
		put := e.hold("/api/push/reminder")
		e.openNotify()
		e.must(e.notifySaveButton().Click())
		e.must(e.gate.awaitHeld("/api/push/reminder", 1))
		e.runFor(saveAnswerWait - time.Second)
		e.must(save.ToBeDisabled())
		e.runFor(time.Second)
		put.awaitAbandoned()
		e.reminderExpectError(saveTimedOutText)
		e.must(save.ToBeEnabled())
		e.must(save.ToBeFocused())
		if d := serverDevices(t, e.Srv); len(d) != 0 {
			t.Errorf("server devices = %+v after a save given up, want none", d)
		}
	})
}

// TestPageRemindersSince checks the since a reminder keeps: the link's,
// written YYYY-MM-DDTHH:MM:SSZ whatever form the link gives it in, or the
// server's time when the link has none.
func TestPageRemindersSince(t *testing.T) {
	for _, tc := range []struct{ name, since, want string }{
		{name: "offset", since: "2026-11-18T11:30:00+01:00", want: "2026-11-18T10:30:00Z"},
		{name: "fraction of a second", since: "2026-11-18T10:30:00.750Z", want: "2026-11-18T10:30:00Z"},
		{name: "none", want: "2026-11-18T12:00:00Z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := url.Values{"eta": {strconv.FormatInt(farHeight, 10)}}
			if tc.since != "" {
				q.Set("since", tc.since)
			}
			// The browser runs 10 minutes behind the server.
			e, _ := openPushChainPage(t, desktop, fullView(), "/?"+q.Encode(), withBrowserOffset(-10*time.Minute))
			e.must(e.expect("#gc-drift").ToContainText("600s"))
			e.reminderSave("30", "m")
			if r, _ := reminderOf(t, e.Srv, e.reminderDevice()); r.Since != tc.want {
				t.Errorf("stored since = %q for the link's %q, want %q", r.Since, tc.since, tc.want)
			}
		})
	}
}

// ---- Removing

// TestPageRemindersRemove checks Remove: the server drops the reminder,
// and the device with it when it has no alert, in which case the page
// forgets the device's id; the popover closes and the button reads notify.
func TestPageRemindersRemove(t *testing.T) {
	path := cardLink(farHeight, time.Time{}, "")
	t.Run("reminder alone", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), path)
		e.reminderSave("30", "m")
		e.openNotify()
		e.must(e.reminderDialogButton("Remove").Click())
		e.must(e.Expect.Locator(e.notifyPopover()).ToBeHidden())
		e.must(e.Expect.Locator(e.notifyButton()).ToBeFocused())
		e.reminderExpectButton("notify", false)
		if id, ok := e.storage(pushDeviceIDKey); ok {
			t.Errorf("localStorage %s = %q after the removal, want none", pushDeviceIDKey, id)
		}
		if d := serverDevices(t, e.Srv); len(d) != 0 {
			t.Errorf("server devices = %+v after the removal, want none", d)
		}

		// The browser subscribes again for the next one.
		e.reminderSave("30", "m")
		e.reminderExpectButton("notify · 30 min", true)
		if _, ok := reminderOf(t, e.Srv, e.reminderDevice()); !ok {
			t.Error("no reminder on the server after saving again")
		}
	})
	t.Run("device with alerts", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), path)
		id := e.saveChainStuckAlert()
		e.reminderSave("30", "m")
		if got := e.reminderDevice(); got != id {
			t.Fatalf("device id = %q after the reminder, want the alerts' %q", got, id)
		}
		e.openNotify()
		e.must(e.reminderDialogButton("Remove").Click())
		e.must(e.Expect.Locator(e.notifyPopover()).ToBeHidden())
		e.reminderExpectButton("notify", false)
		if got, _ := e.storage(pushDeviceIDKey); got != id {
			t.Errorf("localStorage %s = %q after the removal, want the device kept, %q", pushDeviceIDKey, got, id)
		}
		if _, ok := reminderOf(t, e.Srv, id); ok {
			t.Error("the server still holds the reminder")
		}
		expectDevice(t, e.Srv, id, []push.SubscriptionAlert{{AlertType: push.AlertChainStuck}})
	})
	t.Run("refused", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), path, withInstalledClock(),
			expectStatus("/api/push/reminder", http.StatusInternalServerError),
			expectConsole(`^warning: remove reminder: Error: 500 delete reminder`))
		e.reminderSave("30", "m")
		e.openNotify()
		e.must(e.Srv.PushManager.DB().Close())
		e.must(e.reminderDialogButton("Remove").Click())
		e.reminderExpectError(reminderFailedToRemove)
		e.reminderExpectButton("notify · 30 min", true)
	})
}

// TestPageRemindersRemoveOfAForgottenDevice checks Remove once the page has
// forgotten its device, which the server dropped when its push service said
// the subscription expired: nothing is sent, and the reminder reads as
// removed.
func TestPageRemindersRemoveOfAForgottenDevice(t *testing.T) {
	e, _ := openPushChainPage(t, desktop, fullView(), cardLink(farHeight, time.Time{}, ""), withInstalledClock(),
		expectStatus("/api/push/test", http.StatusBadGateway), expectStatus("/api/push/test", http.StatusNotFound))
	e.reminderSave("100", "blocks")
	e.openPushMenu()
	e.Push.failNext(http.StatusGone)
	e.must(e.Page.Locator(pushTestButton).Click())
	e.must(e.expect(pushMenuError).ToHaveText("Test notification failed (502)."))
	e.must(e.Page.Locator(pushTestButton).Click())
	e.must(e.expect(pushMenuError).ToHaveText("Test notification failed (404). Save to register this device again."))
	e.must(e.Page.Locator("#alerts-close").Click())
	e.must(e.expect(pushMenu).ToBeHidden())

	deletes := e.countRequests(http.MethodDelete, "/api/push/reminder")
	e.openNotify()
	e.must(e.reminderDialogButton("Remove").Click())
	e.must(e.Expect.Locator(e.notifyPopover()).ToBeHidden())
	e.reminderExpectButton("notify", false)
	if n := deletes.Load(); n != 0 {
		t.Errorf("DELETE /api/push/reminder sent %d times without a device, want none", n)
	}
}

// TestPageRemindersCloseRemovesTheReminder checks that closing the card of
// this device's reminder removes the reminder first; when it cannot, the
// card stays and its popover says so.
func TestPageRemindersCloseRemovesTheReminder(t *testing.T) {
	path := cardLink(farHeight, pageNow.Add(-time.Hour), "v1.9")
	t.Run("removed", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), path)
		e.reminderSave("30", "m")
		id := e.reminderDevice()
		e.must(e.Page.Locator(linkCard + " .eta-close").Click())
		e.must(e.expect(linkCard).ToBeHidden())
		e.must(e.Expect.Page(e.Page).ToHaveURL(e.ts.URL + "/"))
		if _, ok := reminderOf(t, e.Srv, id); ok {
			t.Error("the server still holds the reminder of the closed card")
		}
	})
	t.Run("kept", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), path, withInstalledClock(),
			expectStatus("/api/push/reminder", http.StatusInternalServerError),
			expectConsole(`^warning: remove reminder: Error: 500 delete reminder`))
		e.reminderSave("30", "m")
		e.must(e.Srv.PushManager.DB().Close())
		e.must(e.Page.Locator(linkCard + " .eta-close").Click())
		e.reminderExpectError("Could not remove the reminder, so the card stays. See console for details.")
		e.must(e.expect(linkCard).ToBeVisible())
		e.must(e.Expect.Page(e.Page).ToHaveURL(e.ts.URL + path))
		e.must(e.Expect.Locator(e.notifyButton()).ToHaveAttribute("aria-expanded", "true"))
	})
}

// TestPageRemindersRemovedAtOnce checks that a removal the server took
// shows at once, while the reminder's reads are slow: the popover closes and
// the button reads notify, or the closed card goes. The device keeps its
// alerts, so the server keeps the device.
func TestPageRemindersRemovedAtOnce(t *testing.T) {
	for _, tc := range []struct {
		name   string
		remove func(e *pageEnv)
	}{
		{"remove", func(e *pageEnv) {
			e.openNotify()
			e.must(e.reminderDialogButton("Remove").Click())
			e.must(e.Expect.Locator(e.notifyPopover()).ToBeHidden())
			e.reminderExpectButton("notify", false)
		}},
		{"close the card", func(e *pageEnv) {
			e.must(e.Page.Locator(linkCard + " .eta-close").Click())
			e.must(e.expect(linkCard).ToBeHidden())
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, _ := chainScenario(runningChain, fullView())
			sc.push = true
			serveBlockTimes(t, sc, runningChain)
			e := openPage(t, desktop, sc, noNavigate(), withInstalledClock())
			way := e.reminderBehindSlowWay()
			e.navigateTo(way.url + cardLink(farHeight, time.Time{}, ""))
			id := e.saveChainStuckAlert()
			e.reminderSave("30", "m")
			way.keepNext()
			tc.remove(e)
			if _, ok := reminderOf(t, e.Srv, id); ok {
				t.Error("the server still holds the reminder")
			}
		})
	}
}

// TestPageRemindersRequestsGiveUp checks that a reminder request the server
// does not answer within 10 s is given up: a removal says it failed and the
// reminder stays; a read leaves the reminder as last read.
func TestPageRemindersRequestsGiveUp(t *testing.T) {
	path := cardLink(farHeight, time.Time{}, "")
	t.Run("remove", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), path, withInstalledClock(),
			expectConsole(`^warning: remove reminder: TimeoutError`))
		e.reminderSave("30", "m")
		e.openNotify()
		del := e.hold("/api/push/reminder?id=")
		e.must(e.reminderDialogButton("Remove").Click())
		e.must(e.gate.awaitHeld("/api/push/reminder?id="+e.reminderDevice(), 1))
		e.runFor(saveAnswerWait)
		del.awaitAbandoned()
		e.reminderExpectError(reminderFailedToRemove)
		e.reminderExpectButton("notify · 30 min", true)
	})
	t.Run("read", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), path, withInstalledClock(),
			expectConsole(`^warning: read reminder: TimeoutError`))
		e.reminderSave("30", "m")
		read := e.hold("/api/push/reminder?id=")
		e.runFor(cardPoll)
		e.must(e.gate.awaitHeld("/api/push/reminder?id="+e.reminderDevice(), 1))
		e.runFor(saveAnswerWait)
		read.awaitAbandoned()
		e.reminderExpectButton("notify · 30 min", true)
	})
}

// TestPageRemindersCloseOnAnErrorCardSaysWhy checks the card of this
// device's reminder whose answer is an error: the button stays, as the
// reminder is still due, and a close that cannot remove the reminder says so
// in the popover.
func TestPageRemindersCloseOnAnErrorCardSaysWhy(t *testing.T) {
	e, _ := openPushChainPage(t, desktop, fullView(), cardLink(farHeight, time.Time{}, ""), withInstalledClock(),
		expectStatus("/api/eta", http.StatusBadRequest),
		expectStatus("/api/push/reminder", http.StatusInternalServerError),
		expectConsole(`^warning: remove reminder: Error: 500 delete reminder`))
	e.reminderSave("30", "m")
	e.navigate("/?eta=1252000&since=yesterday")
	e.expectFailedCard(linkCard, `since must be RFC 3339, such as 2026-11-16T13:47:26Z, got "yesterday"`)
	e.reminderExpectButton("notify · 30 min", true)

	e.must(e.Srv.PushManager.DB().Close())
	e.must(e.Page.Locator(linkCard + " .eta-close").Click())
	e.reminderExpectError("Could not remove the reminder, so the card stays. See console for details.")
	e.must(e.expect(linkCard).ToBeVisible())
}

// TestPageRemindersRemovedThoughUnsubscribeFails checks a removal the
// server took while the browser fails to end its push subscription: the
// reminder reads as removed, and the console says what failed.
func TestPageRemindersRemovedThoughUnsubscribeFails(t *testing.T) {
	path := cardLink(farHeight, time.Time{}, "")
	e, _ := openPushChainPage(t, desktop, fullView(), path,
		expectConsole(`^warning: end push subscription: NetworkError: the push service is out of reach`))
	e.reminderUnsubscribeFails()
	e.navigate(path)
	e.reminderSave("30", "m")
	e.openNotify()
	e.must(e.reminderDialogButton("Remove").Click())
	e.must(e.Expect.Locator(e.notifyPopover()).ToBeHidden())
	e.must(e.Expect.Locator(e.notifyButton()).ToBeFocused())
	e.reminderExpectButton("notify", false)
	if id, ok := e.storage(pushDeviceIDKey); ok {
		t.Errorf("localStorage %s = %q after the removal, want none", pushDeviceIDKey, id)
	}
	if d := serverDevices(t, e.Srv); len(d) != 0 {
		t.Errorf("server devices = %+v after the removal, want none", d)
	}
}

// ---- Reading back

// TestPageRemindersAtTheHaltHeight checks a link to the scheduled halt's
// height, which this device has a reminder for: both cards show, the link
// card as soon as the reminder is read, though the halt was read first.
func TestPageRemindersAtTheHaltHeight(t *testing.T) {
	path := cardLink(haltHeight, pageNow.Add(-time.Hour), "upgrade")
	e, tr := openPushChainPage(t, desktop, fullView(), path, withInstalledClock())
	e.reminderSave("30", "m")
	tr.set(haltedView())

	read := e.hold("/api/push/reminder?id=")
	e.navigate(path)
	e.must(e.expect("#eta-halt-card .eta-target").ToHaveText("until halt #1,250,000 · min v1.8.0 (5,758 blocks to go)"))
	e.must(e.expect(linkCard).ToBeHidden())
	read.release()
	e.must(e.expect(linkCard + " .eta-target").ToHaveText("until #1,250,000 · upgrade (5,758 blocks to go)"))
	e.reminderExpectButton("notify · 30 min", true)
	e.must(e.expect("#eta-halt-card").ToBeVisible())
}

// TestPageRemindersOlderPollAnsweredLateDropped checks two polls of the cards
// in flight at once for the same target: the one the reminder starts, on a
// link to the scheduled halt's height, and the regular poll after it, which
// is answered first. The older poll's answer, coming last, is dropped: here
// its read, given up after 10 s, which would mark the halt card's answer
// stale though a newer poll brought a good one. It runs on WebKit, Safari's
// engine and the iOS Home Screen app's: Chromium holds a read of an address
// while another read of it is out, so a newer poll cannot overtake there.
func TestPageRemindersOlderPollAnsweredLateDropped(t *testing.T) {
	path := cardLink(haltHeight, pageNow.Add(-time.Hour), "upgrade")
	e, tr := openPushChainPage(t, desktopWebKit, fullView(), path, withInstalledClock())
	e.reminderSave("30", "m")
	tr.set(haltedView())
	const haltCard, haltTarget = "#eta-halt-card", "until halt #1,250,000 · min v1.8.0 (5,758 blocks to go)"

	read := e.hold("/api/push/reminder?id=")
	e.navigate(path)
	e.must(e.expect(haltCard + " .eta-target").ToHaveText(haltTarget))
	e.must(e.expect(linkCard).ToBeHidden())

	// The reminder, read 9 s in, starts its poll 1 s before the regular one.
	e.runFor(cardPoll - time.Second)
	older := e.hold("/api/eta")
	read.release()
	e.must(e.gate.awaitHeld("/api/eta", 1))
	e.runFor(time.Second)
	e.must(e.expect(linkCard + " .eta-target").ToHaveText("until #1,250,000 · upgrade (5,758 blocks to go)"))

	e.runFor(cardReadWithin - time.Second)
	older.awaitAbandoned()
	e.must(e.expect(haltCard + " .eta-target").ToHaveText(haltTarget))
	e.must(e.expect(haltCard + " .eta-inline .warn").ToHaveCount(0))
}

// TestPageRemindersPopoverFollowsTheReminder checks an open popover while
// the device's reminder changes, read at each poll: once the reminder fired
// it offers a new one; once another tab of this browser set one for another
// height it warns of the replacement. The lead typed stays.
func TestPageRemindersPopoverFollowsTheReminder(t *testing.T) {
	e, _ := openPushChainPage(t, desktop, fullView(), cardLink(soonHeight, time.Time{}, ""), withInstalledClock())
	const warn = linkCard + " .eta-remind-pop .warn"
	e.reminderSave("100", "blocks")
	e.openNotify()
	e.must(e.Expect.Locator(e.notifySaveButton()).ToHaveText("Update"))
	e.must(e.reminderLead().Fill("90"))

	e.publish(reminderHeadAt(soonHeight - 100))
	e.runFor(cardPoll)
	e.reminderExpectButton("notify", false)
	e.must(e.Expect.Locator(e.notifyPopover()).ToBeVisible())
	e.must(e.Expect.Locator(e.notifySaveButton()).ToHaveText("Save"))
	e.must(e.Expect.Locator(e.reminderDialogButton("Remove")).ToBeHidden())
	e.must(e.expect(warn).ToBeHidden())
	e.must(e.Expect.Locator(e.reminderLead()).ToHaveValue("90"))

	e.reminderSetElsewhere(1_251_000)
	e.runFor(cardPoll)
	e.must(e.expect(warn).ToHaveText("Replaces your reminder for #1,251,000."))
	e.must(e.Expect.Locator(e.notifySaveButton()).ToHaveText("Save"))
	e.must(e.Expect.Locator(e.reminderLead()).ToHaveValue("90"))
}

// TestPageRemindersHiddenRemoveGivesTheKeyboardToSave checks a poll that
// hides the focused Remove, the reminder having fired: Save takes the
// keyboard, which stays in the popover.
func TestPageRemindersHiddenRemoveGivesTheKeyboardToSave(t *testing.T) {
	e, _ := openPushChainPage(t, desktop, fullView(), cardLink(soonHeight, time.Time{}, ""), withInstalledClock())
	e.reminderSave("100", "blocks")
	e.openNotify()
	e.must(e.reminderDialogButton("Remove").Focus())
	e.publish(reminderHeadAt(soonHeight - 100))
	e.runFor(cardPoll)
	e.must(e.Expect.Locator(e.reminderDialogButton("Remove")).ToBeHidden())
	e.must(e.Expect.Locator(e.notifySaveButton()).ToBeFocused())
}

// TestPageRemindersHiddenRemoveDuringASave checks a poll that hides the
// focused Remove while a save runs, Save being off: the lead field takes the
// keyboard, which stays in the popover.
func TestPageRemindersHiddenRemoveDuringASave(t *testing.T) {
	e, _ := openPushChainPage(t, desktop, fullView(), cardLink(soonHeight, time.Time{}, ""), withInstalledClock())
	e.reminderSave("100", "blocks")
	put := e.hold("/api/push/reminder")
	// The next poll comes 5 s into the save, well within its bound.
	e.runFor(cardPoll / 2)
	e.openNotify()
	e.reminderFill("10", "blocks")
	e.must(e.notifySaveButton().Click())
	e.must(e.gate.awaitHeld("/api/push/reminder", 1))
	e.must(e.reminderDialogButton("Remove").Focus())
	e.publish(reminderHeadAt(soonHeight - 100))
	e.runFor(cardPoll / 2)
	e.must(e.Expect.Locator(e.reminderDialogButton("Remove")).ToBeHidden())
	e.must(e.Expect.Locator(e.notifySaveButton()).ToBeDisabled())
	e.must(e.Expect.Locator(e.reminderLead()).ToBeFocused())

	put.release()
	e.must(e.Expect.Locator(e.notifyPopover()).ToBeHidden())
	e.reminderExpectButton("notify · 10 blocks", true)
}

// TestPageRemindersLateReadDropped checks a poll's read of the reminder
// whose answer comes back late, after the user removed the reminder: the
// removal stands, the late answer is dropped. It runs on Chromium and on
// WebKit, Safari's engine and the iOS Home Screen app's.
func TestPageRemindersLateReadDropped(t *testing.T) {
	onProfiles(t, []profile{desktop, desktopWebKit}, func(t *testing.T, p profile) {
		sc, _ := chainScenario(runningChain, fullView())
		sc.push = true
		serveBlockTimes(t, sc, runningChain)
		e := openPage(t, p, sc, noNavigate(), withInstalledClock())
		way := e.reminderBehindSlowWay()
		e.navigateTo(way.url + cardLink(farHeight, time.Time{}, ""))
		e.reminderSave("100", "blocks")

		// The next poll reads the reminder once the card has its answer.
		answer := e.hold("/api/eta?height=")
		e.runFor(cardPoll)
		late := way.keepNext()
		answer.release()
		late.awaitAnswered(t)

		e.openNotify()
		e.must(e.reminderDialogButton("Remove").Click())
		e.must(e.Expect.Locator(e.notifyPopover()).ToBeHidden())
		e.reminderExpectButton("notify", false)
		isRead := func(r playwright.Request) bool {
			return r.Method() == http.MethodGet && pathOf(r.URL()) == "/api/push/reminder"
		}
		if _, err := e.Page.ExpectRequestFinished(func() error { late.release(); return nil },
			playwright.PageExpectRequestFinishedOptions{Predicate: isRead}); err != nil {
			t.Fatalf("let the late read through: %v", err)
		}
		e.openNotify()
		e.must(e.Expect.Locator(e.notifySaveButton()).ToHaveText("Save"))
		e.must(e.Expect.Locator(e.reminderDialogButton("Remove")).ToBeHidden())
		e.reminderExpectButton("notify", false)
		if d := serverDevices(t, e.Srv); len(d) != 0 {
			t.Errorf("server devices = %+v after the removal, want none", d)
		}
	})
}

// TestPageRemindersFailedReadKeepsTheSave checks a save's read of the
// reminder overtaken by a poll's read that fails: the save's answer, the
// newest the page got, is applied; the failed read changes nothing.
func TestPageRemindersFailedReadKeepsTheSave(t *testing.T) {
	onProfiles(t, []profile{desktop, desktopWebKit}, func(t *testing.T, p profile) {
		e, _ := openPushChainPage(t, p, fullView(), cardLink(farHeight, time.Time{}, ""),
			withInstalledClock(), expectConsole(`^warning: read reminder: TypeError`))
		// The poll reads the halt, then the link target, and sets its
		// next timer once the link card shows that second answer: the
		// clock moves only then, so the next poll comes cardPoll later.
		e.must(e.expect(linkCard + " .eta-target").ToHaveText(regexp.MustCompile(`^until #` + regexp.QuoteMeta(etaNF(farHeight)) + ` \(`)))
		saved := e.hold("/api/push/reminder?id=")
		failed := e.hold("/api/push/reminder?id=")
		failed.drop()
		// The next poll comes 5 s into the save's read, well within its bound.
		e.runFor(cardPoll / 2)
		e.reminderSave("30", "m")
		e.must(e.gate.awaitHeld("/api/push/reminder?id="+e.reminderDevice(), 1))

		e.runFor(cardPoll / 2)
		failed.awaitFailure()
		saved.release()
		e.reminderExpectButton("notify · 30 min", true)
	})
}

// TestPageRemindersRestoreOnLoad checks that the page opened without a
// target shows this device's reminder's card, its target put in the link
// without a history entry; a link with a target keeps its own.
func TestPageRemindersRestoreOnLoad(t *testing.T) {
	since := pageNow.Add(-time.Hour)
	e, _ := openPushChainPage(t, desktop, fullView(), cardLink(farHeight, since, "v1.9"))
	e.reminderSave("2", "blocks")
	before := e.historyLength()

	e.navigate("/")
	e.must(e.expect(linkCard + " .eta-target").ToHaveText(regexp.MustCompile(`^until #1,252,000 · v1\.9 `)))
	e.must(e.Expect.Page(e.Page).ToHaveURL(e.ts.URL + "/?eta=1252000&since=2026-11-18T11%3A00%3A00Z&label=v1.9"))
	if after := e.historyLength(); after != before+1 {
		t.Errorf("history.length = %d after loading / and restoring, want %d: the load's entry alone", after, before+1)
	}
	e.reminderExpectButton("notify · 2 blocks", true)

	e.navigate(cardLink(soonHeight, time.Time{}, ""))
	e.must(e.expect(linkCard + " .eta-target").ToHaveText("until #1,244,542 (300 blocks to go)"))
	e.reminderExpectButton("notify", false)
}

// TestPageRemindersFire checks a reminder whose lead is met: the server
// pushes it to this device and drops it, so the button reads notify again
// at the next poll; the link the notification opens shows its card.
func TestPageRemindersFire(t *testing.T) {
	since := pageNow.Add(-time.Hour)
	e, _ := openPushChainPage(t, desktop, fullView(), cardLink(soonHeight, since, "v1.9"), withInstalledClock())
	e.reminderSave("100", "blocks")
	id := e.reminderDevice()
	rem, ok := reminderOf(t, e.Srv, id)
	if !ok {
		t.Fatal("no reminder on the server after the save")
	}
	devices := serverDevices(t, e.Srv)

	e.publish(reminderHeadAt(soonHeight - 101))
	if got := e.Push.received(); len(got) != 0 {
		t.Fatalf("push service received %+v with the height 101 blocks away, want nothing: the lead is 100 blocks", got)
	}
	e.publish(reminderHeadAt(soonHeight - 100))
	got := e.Push.received()
	if len(got) != 1 || got[0].endpoint != devices[0].Endpoint {
		t.Fatalf("push service received %+v, want one push to the device %s", got, devices[0].Endpoint)
	}
	if _, ok := reminderOf(t, e.Srv, id); ok {
		t.Error("the server still holds the reminder it sent")
	}
	e.reminderExpectButton("notify · 100 blocks", true)
	e.runFor(cardPoll)
	e.reminderExpectButton("notify", false)

	// The notification opens its card, as the server links it.
	link := url.Values{"eta": {strconv.FormatInt(rem.Height, 10)}, "since": {rem.Since}, "label": {rem.Label}}
	e.navigate("/?" + link.Encode())
	e.must(e.expect(linkCard + " .eta-target").ToHaveText(regexp.MustCompile(`^until #1,244,542 · v1\.9 \(100 blocks to go\)$`)))
	e.reminderExpectButton("notify", false)
}

// TestPageRemindersReadFails checks that a failed read of the reminder
// keeps the one last read, and says why in the console.
func TestPageRemindersReadFails(t *testing.T) {
	path := cardLink(farHeight, time.Time{}, "")
	t.Run("refused", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), path, withInstalledClock(),
			expectStatus("/api/push/reminder", http.StatusInternalServerError),
			expectConsole(`^warning: read reminder: 500$`))
		e.reminderSave("30", "m")
		e.must(e.Srv.PushManager.DB().Close())
		e.runFor(cardPoll)
		e.reminderExpectButton("notify · 30 min", true)
	})
	t.Run("unanswered", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), path, withInstalledClock(),
			expectConsole(`^warning: read reminder: TypeError`))
		e.reminderSave("30", "m")
		lost := e.hold("/api/push/reminder?id=")
		lost.drop()
		e.runFor(cardPoll)
		lost.awaitFailure()
		e.reminderExpectButton("notify · 30 min", true)
	})
}

// TestPageRemindersLabelMarkupStaysText checks that a label with markup,
// from the link and back from the server, shows as text.
func TestPageRemindersLabelMarkupStaysText(t *testing.T) {
	const label = `<img src=x onerror=alert(1)>`
	e, _ := openPushChainPage(t, desktop, fullView(), cardLink(farHeight, time.Time{}, label))
	e.Page.OnDialog(func(d playwright.Dialog) {
		t.Errorf("the page opened a %s dialog: %q", d.Type(), d.Message())
		if err := d.Dismiss(); err != nil {
			t.Errorf("dismiss the dialog: %v", err)
		}
	})
	e.reminderSave("30", "m")
	if r, _ := reminderOf(t, e.Srv, e.reminderDevice()); r.Label != label {
		t.Errorf("stored label = %q, want %q", r.Label, label)
	}
	e.navigate("/")
	e.must(e.expect(linkCard + " .eta-target").ToHaveText(regexp.MustCompile(`^until #1,252,000 · ` + regexp.QuoteMeta(label) + ` `)))
	e.must(e.expect(linkCard + " img").ToHaveCount(0))
	e.reminderExpectButton("notify · 30 min", true)
}

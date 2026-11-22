//go:build page

package web

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"

	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/web/push"
)

const (
	// reminderFailedToSave and reminderFailedToRemove are what the popover
	// says when a save or a removal fails on the way.
	reminderFailedToSave   = "Failed to save. See console for details."
	reminderFailedToRemove = "Failed to remove. See console for details."
)

// reminderProfiles are the profiles whose browser offers web push.
var reminderProfiles = []profile{desktop, tablet}

// reminderHeadAt is the live chain's snapshot once its newest block is h.
func reminderHeadAt(h int64) *node.Snapshot {
	snap := runningChain.snapshot()
	snap.Status.SyncInfo.LatestBlockHeight = strconv.FormatInt(h, 10)
	snap.Status.SyncInfo.LatestBlockTime = runningChain.at(h).Format(time.RFC3339Nano)
	return snap
}

// ---- Locators

// reminderLead is the popover's how-far-ahead field.
func (e *pageEnv) reminderLead() playwright.Locator {
	return e.notifyPopover().GetByRole("spinbutton", playwright.LocatorGetByRoleOptions{Name: "how far ahead"})
}

// reminderUnit is the popover's minutes-or-blocks choice.
func (e *pageEnv) reminderUnit() playwright.Locator {
	return e.notifyPopover().GetByRole("combobox", playwright.LocatorGetByRoleOptions{Name: "minutes or blocks"})
}

// reminderDialogButton is the popover's button named name.
func (e *pageEnv) reminderDialogButton(name string) playwright.Locator {
	return e.notifyPopover().GetByRole("button", playwright.LocatorGetByRoleOptions{Name: name, Exact: playwright.Bool(true)})
}

// ---- Actions

// reminderFill types n in the popover and picks unit, "m" or "blocks".
func (e *pageEnv) reminderFill(n, unit string) {
	e.T.Helper()
	e.must(e.reminderLead().Fill(n))
	_, err := e.reminderUnit().SelectOption(playwright.SelectOptionValues{Values: &[]string{unit}})
	e.must(err)
}

// reminderSave sets this device's reminder for the link card: n minutes or
// blocks ahead, unit "m" or "blocks"; it waits for the popover to close.
func (e *pageEnv) reminderSave(n, unit string) {
	e.T.Helper()
	e.openNotify()
	e.reminderFill(n, unit)
	e.must(e.notifySaveButton().Click())
	e.must(e.Expect.Locator(e.notifyPopover()).ToBeHidden())
}

// reminderExpectButton checks the notify button reads text, set (blue) or
// not.
func (e *pageEnv) reminderExpectButton(text string, set bool) {
	e.T.Helper()
	btn := e.Expect.Locator(e.notifyButton())
	e.must(btn.ToBeVisible())
	e.must(btn.ToHaveText(text))
	setClass := regexp.MustCompile(`\bset\b`)
	if set {
		e.must(btn.ToHaveClass(setClass))
	} else {
		e.must(btn.Not().ToHaveClass(setClass))
	}
}

// reminderExpectError checks the popover stays open and says msg.
func (e *pageEnv) reminderExpectError(msg string) {
	e.T.Helper()
	e.must(e.Expect.Locator(e.notifyPopover()).ToBeVisible())
	e.must(e.expect(linkCard + " .eta-remind-pop .err").ToHaveText(msg))
	e.must(e.expect(linkCard + " .eta-remind-pop .err").ToBeVisible())
}

// reminderUnsubscribeFails makes the browser fail to end its push
// subscription, as when its push service is out of reach, from the next
// load on. The push stub's init script runs first: this one wraps the
// subscription it gives.
func (e *pageEnv) reminderUnsubscribeFails() {
	e.T.Helper()
	script := `if (self.PushManager) {
    const get = PushManager.prototype.getSubscription
    PushManager.prototype.getSubscription = async function () {
        const sub = await get.call(this)
        if (sub) sub.unsubscribe = async () => { throw new DOMException('the push service is out of reach', 'NetworkError') }
        return sub
    }
}`
	if err := e.Ctx.AddInitScript(playwright.Script{Content: &script}); err != nil {
		e.T.Fatalf("make unsubscribe fail: %v", err)
	}
}

// reminderSetElsewhere sets a reminder for height, 30 minutes ahead, as
// another tab of this browser does: through the server's API, for the one
// device the server holds.
func (e *pageEnv) reminderSetElsewhere(height int64) {
	e.T.Helper()
	devices := serverDevices(e.T, e.Srv)
	if len(devices) != 1 {
		e.T.Fatalf("server devices = %+v, want this browser's alone", devices)
	}
	d := devices[0]
	body, err := json.Marshal(reminderRequest{
		Endpoint: d.Endpoint, P256dh: d.P256dh, Auth: d.Auth,
		Height: height, Since: pageNow.Format(time.RFC3339), Lead: "30m",
	})
	if err != nil {
		e.T.Fatal(err)
	}
	req, err := http.NewRequestWithContext(e.T.Context(), http.MethodPut, e.ts.URL+"/api/push/reminder", bytes.NewReader(body))
	if err != nil {
		e.T.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatalf("PUT /api/push/reminder: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		answer, readErr := io.ReadAll(resp.Body)
		e.T.Fatalf("PUT /api/push/reminder = %d %q (%v), want 200", resp.StatusCode, answer, readErr)
	}
}

// ---- Server state

// reminderDevice returns the device id the page stored, failing the test
// when it stored none.
func (e *pageEnv) reminderDevice() string {
	e.T.Helper()
	id, ok := e.storage(pushDeviceIDKey)
	if !ok || id == "" {
		e.T.Fatalf("localStorage %s = %q (set %t), want the device id", pushDeviceIDKey, id, ok)
	}
	return id
}

// reminderOf returns the reminder the server holds for device id, if any.
func reminderOf(t *testing.T, srv *Server, id string) (push.Reminder, bool) {
	t.Helper()
	r, ok, err := srv.PushManager.DB().Reminder(id)
	if err != nil {
		t.Fatalf("read the reminder of %s: %v", id, err)
	}
	return r, ok
}

// reminderExpectStored fails t unless the server holds want for device id.
func reminderExpectStored(t *testing.T, srv *Server, id string, want push.Reminder) {
	t.Helper()
	got, ok := reminderOf(t, srv, id)
	if !ok {
		t.Fatalf("device %s has no reminder, want %+v", id, want)
	}
	if got != want {
		t.Errorf("reminder of %s = %+v, want %+v", id, got, want)
	}
}

// reminderLeadOf parses a lead as the server writes it.
func reminderLeadOf(t *testing.T, s string) push.Lead {
	t.Helper()
	l, err := push.ParseLead(s)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// ---- A slow way back

// reminderSlowWay is a reverse proxy in front of the server under test, as
// Caddy or nginx are, whose way back to the browser can be slow: it keeps
// the answer to a reminder read the server already gave until the test
// lets it through. The answer is the server's, as it stood when read.
type reminderSlowWay struct {
	url string

	mu   sync.Mutex
	next *reminderKeptAnswer // the answer to keep; nil for none
	kept []*reminderKeptAnswer
}

// reminderKeptAnswer is an answer kept on its way back.
type reminderKeptAnswer struct {
	answered chan struct{} // closed once the server answered
	pass     chan struct{} // closed to let it through
	once     sync.Once
}

// reminderBehindSlowWay serves the server through a reminderSlowWay, which
// the page loads from instead.
func (e *pageEnv) reminderBehindSlowWay() *reminderSlowWay {
	e.T.Helper()
	target, err := url.Parse(e.ts.URL)
	if err != nil {
		e.T.Fatalf("parse the server URL: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	w := &reminderSlowWay{}
	front := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		k := w.take(r)
		if k == nil {
			proxy.ServeHTTP(rw, r)
			return
		}
		got := httptest.NewRecorder()
		proxy.ServeHTTP(got, r)
		close(k.answered)
		select {
		case <-k.pass:
		case <-r.Context().Done():
			return
		}
		maps.Copy(rw.Header(), got.Header())
		rw.WriteHeader(got.Code)
		// A browser gone meanwhile fails the write; the test's own
		// assertions say what that means, so the server log keeps it.
		if _, werr := rw.Write(got.Body.Bytes()); werr != nil {
			log.Printf("reminder slow way: pass the kept answer on: %v", werr)
		}
	}))
	// The kept answers go through first: closing the proxy waits for the
	// requests it serves.
	e.T.Cleanup(func() {
		w.mu.Lock()
		kept := slices.Clone(w.kept)
		w.mu.Unlock()
		for _, k := range kept {
			k.release()
		}
		front.Close()
	})
	w.url = front.URL
	return w
}

// keepNext keeps the answer to the next reminder read on its way back.
func (w *reminderSlowWay) keepNext() *reminderKeptAnswer {
	k := &reminderKeptAnswer{answered: make(chan struct{}), pass: make(chan struct{})}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.next = k
	w.kept = append(w.kept, k)
	return k
}

// take returns the kept answer r is for, if r is the read keepNext asked
// for.
func (w *reminderSlowWay) take(r *http.Request) *reminderKeptAnswer {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.next == nil || r.Method != http.MethodGet || r.URL.Path != "/api/push/reminder" {
		return nil
	}
	k := w.next
	w.next = nil
	return k
}

// awaitAnswered waits until the server answered the kept read.
func (k *reminderKeptAnswer) awaitAnswered(t *testing.T) {
	t.Helper()
	select {
	case <-k.answered:
	case <-time.After(pageTimeout):
		t.Fatalf("no reminder read reached the server within %s", pageTimeout)
	}
}

// release lets the kept answer through.
func (k *reminderKeptAnswer) release() { k.once.Do(func() { close(k.pass) }) }

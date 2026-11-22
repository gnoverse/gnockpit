//go:build page

package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// eventKind is what went wrong on the page.
type eventKind int

const (
	consoleEvent       eventKind = iota // a console error or warning
	pageErrorEvent                      // an uncaught error or unhandled rejection
	failedRequestEvent                  // a request that got no answer
	errorAnswerEvent                    // an answer of 400 or more
)

// pageEvent is one console error or warning, page error, failed request or
// error answer.
type pageEvent struct {
	seq    int // its place among the test's events
	kind   eventKind
	text   string // the message, the error or the failure
	url    string // where the message comes from, or the request's URL
	status int    // the answer's code
}

func (ev pageEvent) String() string {
	switch ev.kind {
	case consoleEvent:
		return fmt.Sprintf("console %s (at %s)", ev.text, ev.url)
	case pageErrorEvent:
		return "page error " + ev.text
	case failedRequestEvent:
		return fmt.Sprintf("failed request %s: %s", ev.url, ev.text)
	default:
		return fmt.Sprintf("answer %d to %s", ev.status, ev.url)
	}
}

// expectation describes events a test declares: they do not fail it, and,
// when it is required and declared, one must occur.
type expectation struct {
	what     string
	required bool
	matches  func(pageEvent) bool
}

// hygiene records the page's events and checks them against what the test
// declared.
type hygiene struct {
	mu       sync.Mutex
	events   []pageEvent
	allowed  []*expectation // the events they match do not fail the test
	required []*expectation // each must be met before the test ends
	changed  chan struct{}  // closed and replaced on each new event
	// answered holds the requests an answer arrived for.
	answered map[playwright.Request]bool
}

// listen records the context's events.
func (e *pageEnv) listen() {
	h := e.hygiene
	e.Ctx.OnConsole(func(m playwright.ConsoleMessage) {
		if m.Type() != "error" && m.Type() != "warning" {
			return
		}
		ev := pageEvent{kind: consoleEvent, text: m.Type() + ": " + m.Text()}
		if loc := m.Location(); loc != nil {
			ev.url = loc.URL
		}
		h.record(ev)
	})
	e.Ctx.OnWebError(func(we playwright.WebError) {
		h.record(pageEvent{kind: pageErrorEvent, text: we.Error().Error()})
	})
	e.Ctx.OnRequest(e.gate.started)
	e.Ctx.OnRequestFinished(e.gate.ended)
	e.Ctx.OnRequestFailed(func(r playwright.Request) {
		e.gate.ended(r)
		e.gate.failed(r.URL())
		ev := pageEvent{kind: failedRequestEvent, url: r.URL()}
		if err := r.Failure(); err != nil {
			ev.text = err.Error()
		}
		// Chromium reports a request whose answer arrived but whose body the
		// page never read as aborted: the request got its answer.
		if ev.text == "net::ERR_ABORTED" && h.wasAnswered(r) {
			return
		}
		h.record(ev)
	})
	e.Ctx.OnResponse(func(r playwright.Response) {
		h.answer(r.Request())
		if r.Status() >= http.StatusBadRequest {
			h.record(pageEvent{kind: errorAnswerEvent, url: r.URL(), status: r.Status()})
		}
	})
}

func (h *hygiene) answer(r playwright.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.answered == nil {
		h.answered = map[playwright.Request]bool{}
	}
	h.answered[r] = true
}

func (h *hygiene) wasAnswered(r playwright.Request) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.answered[r]
}

func (h *hygiene) record(ev pageEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ev.seq = len(h.events)
	h.events = append(h.events, ev)
	if h.changed != nil {
		close(h.changed)
		h.changed = nil
	}
}

// mark returns the place the next event will take.
func (h *hygiene) mark() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.events)
}

// since returns x matching only the events from the place from on.
func since(from int, x *expectation) *expectation {
	return &expectation{
		what:     x.what,
		required: x.required,
		matches:  func(ev pageEvent) bool { return ev.seq >= from && x.matches(ev) },
	}
}

// first returns x matching only the first event it meets from the place
// from on. hygiene calls an expectation's matches with h.mu held, which
// first relies on to read the events.
func (h *hygiene) first(from int, x *expectation) *expectation {
	return &expectation{
		what:     x.what,
		required: x.required,
		matches: func(ev pageEvent) bool {
			if ev.seq < from || !x.matches(ev) {
				return false
			}
			return !slices.ContainsFunc(h.events[from:ev.seq], x.matches)
		},
	}
}

// allow lets the events xs match occur, or not.
func (h *hygiene) allow(xs ...*expectation) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.allowed = append(h.allowed, xs...)
}

// declare allows the events xs match, and requires an event for each
// required one.
func (h *hygiene) declare(xs ...*expectation) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.allowed = append(h.allowed, xs...)
	h.required = append(h.required, requiredOf(xs)...)
}

// requiredOf returns the required expectations of xs.
func requiredOf(xs []*expectation) []*expectation {
	var out []*expectation
	for _, x := range xs {
		if x.required {
			out = append(out, x)
		}
	}
	return out
}

// unmet returns the expectations of xs no event meets yet, and a channel
// closed on the next event.
func (h *hygiene) unmet(xs []*expectation) ([]*expectation, <-chan struct{}) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []*expectation
	for _, x := range xs {
		if !slices.ContainsFunc(h.events, x.matches) {
			out = append(out, x)
		}
	}
	if h.changed == nil {
		h.changed = make(chan struct{})
	}
	return out, h.changed
}

// await waits until an event meets each of xs.
func (h *hygiene) await(xs []*expectation) error {
	timeout := time.NewTimer(pageTimeout)
	defer timeout.Stop()
	for {
		missing, changed := h.unmet(xs)
		if len(missing) == 0 {
			return nil
		}
		select {
		case <-changed:
		case <-timeout.C:
			whats := make([]string, len(missing))
			for i, x := range missing {
				whats[i] = x.what
			}
			return fmt.Errorf("no %s within %s", strings.Join(whats, ", no "), pageTimeout)
		}
	}
}

// verify fails t on a required expectation no event met, and on each event
// no expectation declares.
func (h *hygiene) verify(t *testing.T) {
	t.Helper()
	h.mu.Lock()
	required := slices.Clone(h.required)
	h.mu.Unlock()
	if err := h.await(required); err != nil {
		t.Errorf("declared but not seen: %v", err)
	}
	for _, ev := range h.undeclared() {
		t.Errorf("undeclared %s", ev)
	}
}

// undeclared returns the events no expectation allows.
func (h *hygiene) undeclared() []pageEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []pageEvent
	for _, ev := range h.events {
		if !slices.ContainsFunc(h.allowed, func(x *expectation) bool { return x.matches(ev) }) {
			out = append(out, ev)
		}
	}
	return out
}

// blockedServiceWorker describes the warning Playwright logs for the service
// worker registration it blocks.
var blockedServiceWorker = &expectation{
	what: "Playwright's blocked service worker warning",
	matches: func(ev pageEvent) bool {
		return ev.kind == consoleEvent && ev.text == "warning: Service Worker registration blocked by Playwright"
	},
}

// statusAnswer describes an answer code to a request for path, required, and
// the console error the browser logs for it.
func statusAnswer(path string, code int) []*expectation {
	return []*expectation{
		{
			what:     fmt.Sprintf("answer %d to %s", code, path),
			required: true,
			matches: func(ev pageEvent) bool {
				return ev.kind == errorAnswerEvent && ev.status == code && pathOf(ev.url) == path
			},
		},
		{
			what: fmt.Sprintf("console error of the answer %d to %s", code, path),
			matches: func(ev pageEvent) bool {
				return ev.kind == consoleEvent && pathOf(ev.url) == path &&
					strings.HasPrefix(ev.text, fmt.Sprintf("error: Failed to load resource: the server responded with a status of %d", code))
			},
		},
	}
}

// droppedRequest describes the failure of a request whose URI starts with
// prefix, required, and the console error the browser logs for it.
func droppedRequest(prefix string) (failure, consoleLine *expectation) {
	failure = failedRequest("failure of the dropped request "+prefix, prefix)
	consoleLine = &expectation{
		what: "console error of the dropped request " + prefix,
		matches: func(ev pageEvent) bool {
			return ev.kind == consoleEvent && strings.HasPrefix(requestURIOf(ev.url), prefix) &&
				strings.HasPrefix(ev.text, "error: Failed to load resource")
		},
	}
	return failure, consoleLine
}

// failedRequest describes, as what, the required failure of a request whose
// URI starts with prefix.
func failedRequest(what, prefix string) *expectation {
	return &expectation{
		what:     what,
		required: true,
		matches: func(ev pageEvent) bool {
			return ev.kind == failedRequestEvent && strings.HasPrefix(requestURIOf(ev.url), prefix)
		},
	}
}

// loadAnswers are the error answers each load of the page gets from the
// scenario's disabled features: the push key without push, the block
// history without one, the ETA without a model or a chain status.
func loadAnswers(p profile, sc *scenario) []*expectation {
	var xs []*expectation
	if !sc.push && p.hasPush() {
		xs = append(xs, statusAnswer("/api/push/vapid-key", http.StatusServiceUnavailable)...)
	}
	if sc.seed == nil {
		xs = append(xs, statusAnswer("/api/history", http.StatusServiceUnavailable)...)
	}
	if sc.eta == nil || sc.snap == nil || sc.snap.Status == nil {
		xs = append(xs, statusAnswer("/api/eta", http.StatusServiceUnavailable)...)
	}
	return xs
}

// pathOf returns the path of rawURL, or rawURL when it does not parse.
func pathOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.Path
}

// requestURIOf returns the path and query of rawURL, or rawURL when it does
// not parse.
func requestURIOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	return u.RequestURI()
}

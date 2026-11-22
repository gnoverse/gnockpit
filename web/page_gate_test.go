//go:build page

package web

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// gate serves the routes, holding the requests tests asked to hold before
// the real handler answers them. It also follows the browser's requests in
// flight, so that runFor can let them get their answers.
type gate struct {
	next http.Handler

	mu      sync.Mutex
	waiting []*heldRequest // holds not yet given their request, in the order asked
	// dropping holds the URIs of dropped requests whose failure the browser
	// has not reported: WebKit sends a request again, up to three times, when
	// its connection closes unanswered, and those are dropped too.
	dropping map[string]bool
	held     map[string]int                // requests the gate holds, by URI
	inflight map[playwright.Request]string // the browser's requests not finished, with their URIs
	changed  chan struct{}                 // closed and replaced when held or inflight changes

	closed    chan struct{} // closed once the server stops: held requests are dropped
	closeOnce sync.Once
}

func (g *gate) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	uri := r.URL.RequestURI()
	if g.isDropping(uri) {
		g.drop()
	}
	if h := g.claim(uri); h != nil {
		// net/http notices a client gone only once the request's body is
		// read to its end: the body is read before the wait, and handed on
		// as read.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			// The browser gave the request up while sending it.
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		switch g.wait(h, r) {
		case holdDropped:
			g.setDropping(uri, true)
			g.drop()
		case holdAbandoned:
			// The browser gave the request up: nobody is left to answer.
			return
		}
	}
	g.next.ServeHTTP(w, r)
}

// drop closes the request's connection without an answer, unlogged.
func (g *gate) drop() {
	panic(http.ErrAbortHandler)
}

func (g *gate) isDropping(uri string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.dropping[uri]
}

func (g *gate) setDropping(uri string, on bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.dropping == nil {
		g.dropping = map[string]bool{}
	}
	if on {
		g.dropping[uri] = true
		return
	}
	delete(g.dropping, uri)
}

// failed notes that the browser reported the failure of the request for
// rawURL: a dropped request is not sent again.
func (g *gate) failed(rawURL string) {
	g.setDropping(requestURIOf(rawURL), false)
}

func (g *gate) add(h *heldRequest) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.waiting = append(g.waiting, h)
}

// unclaimed returns the holds no request came for.
func (g *gate) unclaimed() []*heldRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.waiting)
}

// claim returns the first hold waiting for a request to uri, if any.
func (g *gate) claim(uri string) *heldRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, h := range g.waiting {
		if strings.HasPrefix(uri, h.prefix) {
			g.waiting = slices.Delete(g.waiting, i, i+1)
			return h
		}
	}
	return nil
}

// holdEnd is how a held request's wait ends.
type holdEnd int

const (
	holdReleased  holdEnd = iota // the test released it: the real handler answers
	holdDropped                  // the test dropped it, or the server stopped
	holdAbandoned                // the browser gave it up
)

// wait blocks while h holds r, counting r as held until the test releases or
// drops it, the server stops, or the browser gives it up.
func (g *gate) wait(h *heldRequest, r *http.Request) holdEnd {
	uri := r.URL.RequestURI()
	g.count(func() { g.held[uri]++ })
	defer g.count(func() { g.held[uri]-- })
	select {
	case <-h.decided:
		if h.released {
			return holdReleased
		}
		return holdDropped
	case <-g.closed:
		return holdDropped
	case <-r.Context().Done():
		return holdAbandoned
	}
}

func (g *gate) close() {
	g.closeOnce.Do(func() { close(g.closed) })
}

// started notes a request the browser sent, ended one it finished or gave
// up.
func (g *gate) started(r playwright.Request) {
	uri := requestURIOf(r.URL())
	g.count(func() { g.inflight[r] = uri })
}

func (g *gate) ended(r playwright.Request) {
	g.count(func() { delete(g.inflight, r) })
}

// count applies change to the requests held or in flight, and wakes settle.
func (g *gate) count(change func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.held == nil {
		g.held = map[string]int{}
		g.inflight = map[playwright.Request]string{}
	}
	change()
	if g.changed != nil {
		close(g.changed)
		g.changed = nil
	}
}

// unsettled returns the URIs of the browser's requests in flight that the
// gate does not hold, and a channel closed on the next change.
func (g *gate) unsettled() ([]string, <-chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	open := map[string]int{}
	for _, uri := range g.inflight {
		open[uri]++
	}
	var out []string
	for uri, n := range open {
		if n > g.held[uri] {
			out = append(out, uri)
		}
	}
	if g.changed == nil {
		g.changed = make(chan struct{})
	}
	return out, g.changed
}

// awaitHeld waits until the gate holds n requests for uri.
func (g *gate) awaitHeld(uri string, n int) error {
	timeout := time.NewTimer(pageTimeout)
	defer timeout.Stop()
	for {
		g.mu.Lock()
		got := g.held[uri]
		if g.changed == nil {
			g.changed = make(chan struct{})
		}
		changed := g.changed
		g.mu.Unlock()
		if got == n {
			return nil
		}
		select {
		case <-changed:
		case <-timeout.C:
			return fmt.Errorf("the gate holds %d requests for %s after %s, want %d", got, uri, pageTimeout, n)
		}
	}
}

// settle waits until every request the browser has in flight is one the
// gate holds.
func (g *gate) settle() error {
	timeout := time.NewTimer(pageTimeout)
	defer timeout.Stop()
	for {
		open, changed := g.unsettled()
		if len(open) == 0 {
			return nil
		}
		select {
		case <-changed:
		case <-timeout.C:
			slices.Sort(open)
			return fmt.Errorf("requests without an answer after %s: %s", pageTimeout, strings.Join(open, ", "))
		}
	}
}

// heldRequest is the next request whose URI starts with a prefix, held until
// the test releases or drops it. Either may come before the request does.
type heldRequest struct {
	t        *testing.T
	prefix   string
	hygiene  *hygiene
	decided  chan struct{}
	once     sync.Once
	released bool // set before decided is closed
	// failure is the dropped request's failure, declared by drop.
	failure *expectation
	from    int // the place of the first event after the hold was asked
}

// hold holds the next request whose URI (path and query) starts with prefix.
func (e *pageEnv) hold(prefix string) *heldRequest {
	h := &heldRequest{t: e.T, prefix: prefix, hygiene: e.hygiene, decided: make(chan struct{}), from: e.hygiene.mark()}
	e.gate.add(h)
	return h
}

// release lets the real handler answer the request.
func (h *heldRequest) release() { h.decide(true) }

// drop closes the request's connection unanswered; its failure is declared
// and must occur.
func (h *heldRequest) drop() { h.decide(false) }

// awaitFailure waits until the browser reports the failure of the dropped
// request: an assertion made after it sees the page past its error handling.
func (h *heldRequest) awaitFailure() {
	h.t.Helper()
	if h.failure == nil {
		h.t.Fatalf("hold %s: awaitFailure before drop", h.prefix)
	}
	if err := h.hygiene.await([]*expectation{h.failure}); err != nil {
		h.t.Fatal(err)
	}
}

// awaitAbandoned waits until the browser gives the held request up
// unanswered, as a page that bounds its reads does, and declares that
// failure.
func (h *heldRequest) awaitAbandoned() {
	h.t.Helper()
	gaveUp := h.hygiene.first(h.from, failedRequest("give-up of the held request "+h.prefix+" by the browser", h.prefix))
	h.hygiene.declare(gaveUp)
	if err := h.hygiene.await([]*expectation{gaveUp}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *heldRequest) decide(release bool) {
	done := false
	h.once.Do(func() {
		if !release {
			from := h.hygiene.mark()
			failure, consoleLine := droppedRequest(h.prefix)
			h.failure = h.hygiene.first(from, failure)
			h.hygiene.declare(h.failure, h.hygiene.first(from, consoleLine))
		}
		h.released = release
		close(h.decided)
		done = true
	})
	if !done {
		h.t.Errorf("hold %s: released or dropped twice", h.prefix)
	}
}

// TestPageHarnessDropAwaitsItsOwnFailure checks the harness: a dropped
// request's failure is its own, never an earlier drop's of the same prefix.
func TestPageHarnessDropAwaitsItsOwnFailure(t *testing.T) {
	e := openPage(t, desktop, bootScenario(), withInstalledClock())
	first := e.hold("/api/links")
	first.drop()
	e.runFor(readLinksEvery)
	first.awaitFailure()

	second := e.hold("/api/links")
	second.drop()
	if missing, _ := e.hygiene.unmet([]*expectation{second.failure}); len(missing) == 0 {
		t.Fatal("the second drop's failure is met by the first drop's")
	}
	e.runFor(readLinksEvery)
	second.awaitFailure()

	// A third failure of the prefix, which no drop declares, stays
	// undeclared: each drop's declaration covers its own failure only.
	from := e.hygiene.mark()
	third := e.hold("/api/links")
	close(third.decided) // dropped, skipping drop's declaration
	e.runFor(readLinksEvery)
	failure, consoleLine := droppedRequest("/api/links")
	thirdFailure := since(from, failure)
	if err := e.hygiene.await([]*expectation{thirdFailure}); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(e.hygiene.undeclared(), thirdFailure.matches) {
		t.Error("the third failure of /api/links is allowed by the earlier drops' declarations")
	}
	// Declared here, so that the check's own failure does not fail the test.
	e.hygiene.allow(thirdFailure, since(from, consoleLine))
}

// TestPageHarnessServerClockFollowsRunFor checks the harness: runFor moves
// the server's clock with the browser's, so an answer after it reads the
// same now as the page.
func TestPageHarnessServerClockFollowsRunFor(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork(), withInstalledClock())
	e.runFor(time.Minute)
	if got, want := e.Srv.now(), pageNow.Add(time.Minute); !got.Equal(want) {
		t.Errorf("server clock %s a minute on, want %s", got, want)
	}
	// The page sets its clock by the node time of the message that shows
	// the new head, made 2 s before the server's now.
	head := healthyAt(pageHeight + 1)
	head.Status.SyncInfo.LatestBlockTime = pageNow.Add(time.Minute - 2*time.Second).Format(time.RFC3339Nano)
	e.publish(head)
	e.must(e.expect("#d-block").ToContainText(strconv.Itoa(pageHeight + 1)))
	e.must(e.expect("#d-time .ago").ToHaveText(" (2s ago)"))
}

// TestPageHarnessForgetsAnAbandonedHold checks the harness: a held request
// the browser gives up is no longer counted as held, so runFor waits for the
// answer to the next request for the same URI. A request with a body is
// seen given up as one without.
func TestPageHarnessForgetsAnAbandonedHold(t *testing.T) {
	t.Run("GET", func(t *testing.T) {
		e := openPage(t, desktop, healthyNetwork(), withInstalledClock())
		e.must(e.Expect.Page(e.Page).ToHaveTitle("Gnockpit " + pageChainID))
		e.publish(sourcesDown())
		slow := e.hold("/api/boot")
		e.runFor(readBootEvery)
		e.runFor(readBootWithin)
		slow.awaitAbandoned()
		if err := e.gate.awaitHeld("/api/boot", 0); err != nil {
			t.Fatal(err)
		}
		e.runFor(readBootEvery - readBootWithin)
		e.must(e.expect("#boot-panel").ToBeVisible())
	})
	t.Run("PUT", func(t *testing.T) {
		e, _ := openPushChainPage(t, desktop, fullView(), cardLink(farHeight, time.Time{}, ""),
			withInstalledClock(), expectConsole(`^warning: save reminder: TimeoutError`))
		slow := e.hold("/api/push/reminder")
		e.openNotify()
		e.must(e.notifySaveButton().Click())
		e.must(e.gate.awaitHeld("/api/push/reminder", 1))
		e.runFor(saveAnswerWait)
		slow.awaitAbandoned()
		if err := e.gate.awaitHeld("/api/push/reminder", 0); err != nil {
			t.Fatal(err)
		}
	})
}

// TestPageHarnessClosesAConnectionWithoutARequest checks the harness: the
// test server closes a connection on which no request comes, as WebKit
// leaves the one it opens for a dropped request's retry at times.
func TestPageHarnessClosesAConnectionWithoutARequest(t *testing.T) {
	e := openPage(t, desktop, healthyNetwork(), noNavigate())
	conn, err := net.Dial("tcp", e.ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetReadDeadline(time.Now().Add(pageTimeout)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Errorf("read on a connection without a request = %v, want the server to close it (EOF) within %s", err, pageTimeout)
	}
}

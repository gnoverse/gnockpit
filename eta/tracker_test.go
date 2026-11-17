package eta

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/node"
)

// fakeChain is an in-memory chain whose block h is at start + h*3.5s.
type fakeChain struct {
	mu      sync.Mutex
	chainID string
	head    int64
	floor   int64 // heights below are refused, as a node that cannot serve them
	halt    node.Halt
	fail    map[int64][]error // errors returned, in order, before serving a height
	fetched []int64
	onFetch func(h int64) // called before serving each height
}

func newFakeChain(head int64) *fakeChain {
	return &fakeChain{chainID: "gnoland-1", head: head, fail: map[int64][]error{}}
}

func (c *fakeChain) timeOf(h int64) time.Time {
	return t0.Add(time.Duration(h) * 3500 * time.Millisecond)
}

func (c *fakeChain) ChainHead(context.Context) (node.ChainHead, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return node.ChainHead{ChainID: c.chainID, Height: c.head, Time: c.timeOf(c.head)}, nil
}

func (c *fakeChain) BlockTime(_ context.Context, h int64) (time.Time, error) {
	if c.onFetch != nil {
		c.onFetch(h)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fetched = append(c.fetched, h)
	if errs := c.fail[h]; len(errs) > 0 {
		c.fail[h] = errs[1:]
		return time.Time{}, errs[0]
	}
	if h < c.floor || h > c.head {
		return time.Time{}, &node.RPCError{Code: -32603, Message: "Internal error", Data: fmt.Sprintf("height %d not available", h)}
	}
	return c.timeOf(h), nil
}

func (c *fakeChain) HaltParams(context.Context) (node.Halt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.halt, nil
}

// testTracker returns a tracker on chain whose sleeps return at once and are
// recorded, and whose log lines are captured.
func testTracker(t *testing.T, chain *fakeChain) (tr *Tracker, slept *[]time.Duration, logs *[]string) {
	t.Helper()
	tr = NewTracker(newTestStore(t), chain)
	slept, logs = &[]time.Duration{}, &[]string{}
	tr.sleep = func(ctx context.Context, d time.Duration) error {
		*slept = append(*slept, d)
		return ctx.Err()
	}
	tr.logf = func(format string, args ...any) { *logs = append(*logs, fmt.Sprintf(format, args...)) }
	return tr, slept, logs
}

// syncAll runs passes until the backfill has nothing left to fetch.
func syncAll(t *testing.T, tr *Tracker) {
	t.Helper()
	for range 100 {
		more, err := tr.syncPass(context.Background())
		if err != nil {
			t.Fatalf("syncPass() error = %v", err)
		}
		if !more {
			return
		}
	}
	t.Fatal("backfill did not finish in 100 passes")
}

func storedHeights(t *testing.T, tr *Tracker) []int64 {
	t.Helper()
	samples, err := tr.store.Samples(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var hs []int64
	for _, s := range samples {
		hs = append(hs, s.Height)
	}
	return hs
}

func gridHeights(from, to int64) []int64 {
	var hs []int64
	for h := from; h <= to; h += Stride {
		hs = append(hs, h)
	}
	return hs
}

func TestTrackerBackfillsNewestFirst(t *testing.T) {
	chain := newFakeChain(5042)
	tr, slept, logs := testTracker(t, chain)

	syncAll(t, tr)

	if want := newestFirst(Stride, 5000); !slices.Equal(chain.fetched, want) {
		t.Errorf("fetched %v, want the grid newest-first", chain.fetched)
	}
	if got := storedHeights(t, tr); !slices.Equal(got, gridHeights(Stride, 5000)) {
		t.Errorf("stored %v, want every grid height up to the head", got)
	}
	v := tr.View()
	if v.Model.Len() != 50 || !v.Complete {
		t.Errorf("View() = %d samples, complete %v; want 50, true", v.Model.Len(), v.Complete)
	}
	for _, d := range *slept {
		if d != fetchSpacing {
			t.Errorf("slept %v between fetches, want %v", d, fetchSpacing)
		}
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "block-time history complete") {
		t.Errorf("logs = %q, want one line reporting the complete history", *logs)
	}

	// Nothing left: a new pass fetches nothing.
	chain.fetched = nil
	syncAll(t, tr)
	if len(chain.fetched) != 0 {
		t.Errorf("second pass fetched %v, want nothing", chain.fetched)
	}
}

func TestTrackerPassesStartFromTheNewestBlocks(t *testing.T) {
	chain := newFakeChain(5042)
	tr, _, _ := testTracker(t, chain)
	tr.perPass = 10

	more, err := tr.syncPass(context.Background())
	if err != nil || !more {
		t.Fatalf("first pass = %v, %v; want more, nil", more, err)
	}
	if want := newestFirst(4100, 5000); !slices.Equal(chain.fetched, want) {
		t.Errorf("first pass fetched %v, want %v", chain.fetched, want)
	}

	// The chain moved on: the next pass takes the new blocks first.
	chain.head, chain.fetched = 5342, nil
	if _, err := tr.syncPass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []int64{5300, 5200, 5100, 4000, 3900, 3800, 3700, 3600, 3500, 3400}; !slices.Equal(chain.fetched, want) {
		t.Errorf("second pass fetched %v, want %v", chain.fetched, want)
	}
}

func TestTrackerPausesWhenRateLimited(t *testing.T) {
	chain := newFakeChain(1042)
	chain.fail[900] = []error{
		&node.HTTPStatusError{StatusCode: http.StatusTooManyRequests, RetryAfter: 7 * time.Second},
		&node.HTTPStatusError{StatusCode: http.StatusTooManyRequests, RetryAfter: 7 * time.Second},
		&node.HTTPStatusError{StatusCode: http.StatusTooManyRequests},
	}
	tr, slept, logs := testTracker(t, chain)

	syncAll(t, tr)

	if got := storedHeights(t, tr); !slices.Equal(got, gridHeights(Stride, 1000)) {
		t.Errorf("stored %v, want every grid height despite the rate limit", got)
	}
	if !slices.Contains(*slept, 7*time.Second) || !slices.Contains(*slept, rateLimitPause) {
		t.Errorf("slept %v, want the 7s Retry-After then the %v default", *slept, rateLimitPause)
	}
	// The same pause twice in a row is logged once; the recovery is logged.
	want := []string{
		"eta: backfill: RPC rate limited, pausing for 7s",
		"eta: backfill: RPC rate limited, pausing for 1m0s",
		"eta: backfill ok again",
	}
	if len(*logs) < 3 || !slices.Equal((*logs)[:3], want) {
		t.Errorf("logs = %q, want %q first", *logs, want)
	}
}

func TestTrackerStopsAtTheSourceFloor(t *testing.T) {
	chain := newFakeChain(5042)
	chain.floor = 1000
	tr, _, _ := testTracker(t, chain)

	syncAll(t, tr)

	if got := storedHeights(t, tr); !slices.Equal(got, gridHeights(1000, 5000)) {
		t.Errorf("stored %v, want down to the floor", got)
	}
	if floor, _ := tr.store.Floor(context.Background()); floor != 1000 {
		t.Errorf("Floor() = %d, want 1000", floor)
	}
	if !tr.View().Complete {
		t.Error("history not complete after reaching the floor")
	}
	chain.fetched = nil
	syncAll(t, tr)
	if len(chain.fetched) != 0 {
		t.Errorf("next pass fetched %v, want nothing below the floor", chain.fetched)
	}
}

func TestTrackerKeepsWhatItFetchedOnError(t *testing.T) {
	chain := newFakeChain(5042)
	chain.fail[4800] = []error{errors.New("connection reset")}
	tr, _, _ := testTracker(t, chain)

	if _, err := tr.syncPass(context.Background()); err == nil || !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("syncPass() error = %v, want the fetch error", err)
	}
	if got := storedHeights(t, tr); !slices.Equal(got, []int64{4900, 5000}) {
		t.Errorf("stored %v, want the heights fetched before the error", got)
	}
	if tr.View().Model.Len() != 2 {
		t.Errorf("model has %d samples, want 2", tr.View().Model.Len())
	}
}

func TestTrackerResetsOnAnotherChain(t *testing.T) {
	ctx := context.Background()
	chain := newFakeChain(1042)
	tr, _, logs := testTracker(t, chain)
	if err := tr.store.Reset(ctx, "test-13"); err != nil {
		t.Fatal(err)
	}
	if err := tr.store.AddSamples(ctx, []Sample{{Height: 7700, Time: t0}}); err != nil {
		t.Fatal(err)
	}

	syncAll(t, tr)

	if id, _ := tr.store.ChainID(ctx); id != "gnoland-1" {
		t.Errorf("ChainID() = %q, want gnoland-1", id)
	}
	if got := storedHeights(t, tr); !slices.Equal(got, gridHeights(Stride, 1000)) {
		t.Errorf("stored %v, want only gnoland-1's blocks", got)
	}
	if !slices.Contains(*logs, "eta: chain is gnoland-1, dropping the block-time history of test-13") {
		t.Errorf("logs = %q, want the reset reported", *logs)
	}
}

func TestTrackerHalt(t *testing.T) {
	chain := newFakeChain(1042)
	chain.halt = node.Halt{Height: 900, MinVersion: "v1.7.0"} // passed
	tr, _, _ := testTracker(t, chain)
	seen := t0.Add(time.Hour)
	tr.now = func() time.Time { return seen }

	syncAll(t, tr)
	if h := tr.View().Halt; h != nil {
		t.Errorf("Halt = %+v for a halt below the head, want none", h)
	}

	chain.halt = node.Halt{Height: 2000, MinVersion: "v1.8.0"}
	syncAll(t, tr)
	want := ScheduledHalt{Height: 2000, MinVersion: "v1.8.0", FirstSeen: seen}
	if h := tr.View().Halt; h == nil || *h != want {
		t.Errorf("Halt = %+v, want %+v", h, want)
	}

	// Seen again later: the first sighting stays.
	tr.now = func() time.Time { return seen.Add(time.Hour) }
	syncAll(t, tr)
	if h := tr.View().Halt; h == nil || !h.FirstSeen.Equal(seen) {
		t.Errorf("Halt = %+v, want first seen %v", h, seen)
	}
}

func TestTrackerRunLoadsAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	chain := newFakeChain(1042)
	tr, _, _ := testTracker(t, chain)
	if err := tr.store.Reset(ctx, "gnoland-1"); err != nil {
		t.Fatal(err)
	}
	if err := tr.store.AddSamples(ctx, []Sample{{Height: 500, Time: chain.timeOf(500)}}); err != nil {
		t.Fatal(err)
	}
	// Stop at the first wait between passes.
	tr.sleep = func(ctx context.Context, d time.Duration) error {
		if d == syncInterval {
			cancel()
		}
		return ctx.Err()
	}

	done := make(chan struct{})
	go func() {
		tr.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run() did not return after its context was cancelled")
	}
	if got := storedHeights(t, tr); !slices.Equal(got, gridHeights(Stride, 1000)) {
		t.Errorf("stored %v, want the stored sample kept and the rest backfilled", got)
	}
	if chain.fetched[len(chain.fetched)-1] != 100 || slices.Contains(chain.fetched, 500) {
		t.Errorf("fetched %v, want every height but the stored 500", chain.fetched)
	}
}

func newestFirst(from, to int64) []int64 {
	hs := gridHeights(from, to)
	slices.Reverse(hs)
	return hs
}

func TestTrackerRefusalAboveServedHeightsIsTransient(t *testing.T) {
	// A node behind the one that gave the head (a load balancer's lagging
	// backend) refuses the newest heights: retry later, no floor.
	chain := newFakeChain(5042)
	chain.fail[5000] = []error{&node.RPCError{Code: -32603, Message: "Internal error", Data: "min height 5000 can't be greater than max height 4990"}}
	tr, _, _ := testTracker(t, chain)

	if _, err := tr.syncPass(context.Background()); err == nil {
		t.Fatal("syncPass() error = nil, want the refusal reported")
	}
	if floor, _ := tr.store.Floor(context.Background()); floor != 0 {
		t.Errorf("Floor() = %d, want none", floor)
	}
	if tr.View().Complete {
		t.Error("history complete after a refused head height")
	}
	syncAll(t, tr)
	if got := storedHeights(t, tr); !slices.Equal(got, gridHeights(Stride, 5000)) {
		t.Errorf("stored %v, want every grid height once the node caught up", got)
	}
}

func TestTrackerHTTPErrorIsNotAFloor(t *testing.T) {
	chain := newFakeChain(1042)
	chain.fail[900] = []error{&node.HTTPStatusError{
		StatusCode: http.StatusInternalServerError,
		RPC:        &node.RPCError{Code: -32603, Message: "Internal error"},
	}}
	tr, _, _ := testTracker(t, chain)

	if _, err := tr.syncPass(context.Background()); err == nil {
		t.Fatal("syncPass() error = nil, want the HTTP error")
	}
	if floor, _ := tr.store.Floor(context.Background()); floor != 0 {
		t.Errorf("Floor() = %d after an HTTP error, want none", floor)
	}
}

func TestTrackerRebuildsTheModelMidPass(t *testing.T) {
	chain := newFakeChain(25_042)
	tr, _, _ := testTracker(t, chain)
	seen := -1
	chain.onFetch = func(h int64) {
		if h == 25_000-150*Stride {
			seen = tr.View().Model.Len()
		}
	}

	if _, err := tr.syncPass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seen != rebuildEvery {
		t.Errorf("model had %d samples at the 151st fetch, want %d", seen, rebuildEvery)
	}
}

func TestTrackerReport(t *testing.T) {
	tr, _, logs := testTracker(t, newFakeChain(1042))
	boom := errors.New("connection reset")
	tr.report("sync", boom)
	tr.report("sync", boom)
	tr.report("halt params", nil)
	tr.report("sync", nil)
	tr.report("sync", nil)
	want := []string{"eta: sync: connection reset", "eta: sync ok again"}
	if !slices.Equal(*logs, want) {
		t.Errorf("logs = %q, want %q", *logs, want)
	}
}

func TestTrackerRefusalNearTheHeadIsTransient(t *testing.T) {
	// Behind a load balancer, a backend lagging the others refuses a height
	// just below one they served: too close to the head to be the node's
	// oldest block.
	chain := newFakeChain(5042)
	chain.fail[4900] = []error{&node.RPCError{Code: -32603, Message: "Internal error", Data: "min height 4900 can't be greater than max height 4850"}}
	tr, _, _ := testTracker(t, chain)

	if _, err := tr.syncPass(context.Background()); err == nil {
		t.Fatal("syncPass() error = nil, want the refusal reported")
	}
	if floor, _ := tr.store.Floor(context.Background()); floor != 0 {
		t.Errorf("Floor() = %d, want none", floor)
	}
}

func TestTrackerFloorAcrossPasses(t *testing.T) {
	// The height above the refused one was stored by an earlier pass.
	chain := newFakeChain(5042)
	chain.floor = 1000
	tr, _, _ := testTracker(t, chain)
	tr.perPass = len(gridHeights(1000, 5000))

	syncAll(t, tr)

	if floor, _ := tr.store.Floor(context.Background()); floor != 1000 {
		t.Errorf("Floor() = %d, want 1000", floor)
	}
}

package blocks

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

// fakeChain is an in-memory chain whose block h is at t0 + h×3.5 s. Its set
// is valG0Y, val15T and valHQH (10 power each) from height 1, plus valH97 from
// change on; each height's priorities are one increment of the height
// below's, as tm2 does, and every member signs unless told otherwise.
type fakeChain struct {
	mu          sync.Mutex
	chainID     string
	head        int64
	floor       int64            // heights below are refused
	lagging     int64            // a backend this far behind the head: it refuses the heights above its own latest, which it serves with its own commit; 0: none
	maxBatch    int              // a larger batch is refused whole; 0: no limit
	slowOver    int              // a larger batch times out; 0: never
	fail        []error          // errors Commits returns first, in order
	change      int64            // valH97 joins the set at this height; 0: never
	round       map[int64]int    // commit round per height, default 0
	proposer    map[int64]string // header proposer per height, default the expected one
	events      map[int64][]node.BlockEvent
	refusedOnce int64           // this height is refused once, then served; 0: none
	refused     map[int64]bool  // heights refused
	broken      map[int64]error // a batch asking one of these heights fails whole with its error
	setFail     []error         // errors ValidatorSet returns first, in order
	emptySets   int             // empty sets ValidatorSet answers first
	setRefused  map[int64]bool  // heights whose set the source refuses
	eventFail   []error         // errors BlockEvents returns first, in order

	sets       [][]node.SetMember // sets[h-1] is the set at h
	batches    [][]int64
	setCalls   []int64
	eventCalls []int64
}

func newFakeChain(head int64) *fakeChain {
	return &fakeChain{chainID: "gnoland-1", head: head, round: map[int64]int{}, proposer: map[int64]string{}, events: map[int64][]node.BlockEvent{}}
}

func (c *fakeChain) timeOf(h int64) time.Time {
	return t0.Add(time.Duration(h) * 3500 * time.Millisecond)
}

func (c *fakeChain) hashOf(h int64) string {
	if c.change > 0 && h >= c.change {
		return "SET2"
	}
	return "SET1"
}

// setAt is the set at h, with its priorities there.
func (c *fakeChain) setAt(h int64) []node.SetMember {
	for k := int64(len(c.sets)) + 1; k <= h; k++ {
		var s []node.SetMember
		switch k {
		case 1:
			s = setOf(m{valG0Y, 10, 0}, m{val15T, 10, 0}, m{valHQH, 10, 0})
		case c.change:
			s = append(slices.Clone(c.sets[k-2]), node.SetMember{Address: valH97, Power: 10})
		default:
			p, _ := newPrioSet(c.sets[k-2])
			p.increment()
			s = make([]node.SetMember, len(p))
			for i, v := range p {
				s[i] = node.SetMember{Address: v.address, Power: v.power, Priority: v.prio}
			}
		}
		c.sets = append(c.sets, s)
	}
	return c.sets[h-1]
}

// expected is the proposer of round r at h, valG0Y where round 0's is unknown.
func (c *fakeChain) expected(h int64, r int) string {
	var prev []node.SetMember
	if h > 1 {
		prev = c.setAt(h - 1)
	}
	props, _ := roundProposers(prev, c.setAt(h), r)
	if props[r] == "" {
		return valG0Y
	}
	return props[r]
}

func (c *fakeChain) commit(h int64) node.Commit {
	r := c.round[h]
	proposer, ok := c.proposer[h]
	if !ok {
		proposer = c.expected(h, r)
	}
	set := c.setAt(h)
	block := fmt.Sprintf("B%d", h)
	pcs := make([]*node.Precommit, len(set))
	for i, mem := range set {
		pcs[i] = &node.Precommit{Address: mem.Address, Index: i, Round: r, BlockHash: block, Time: c.timeOf(h).Add(time.Second)}
	}
	// A node's latest height has no next block yet: it serves its own commit,
	// which can lack a precommit that arrived late and the next block carries.
	canonical := h < c.head-c.lagging
	if !canonical {
		pcs[len(pcs)-1] = nil
	}
	return node.Commit{
		Height: h, Time: c.timeOf(h), Proposer: proposer, ValidatorsHash: c.hashOf(h),
		BlockHash: block, Canonical: canonical, Precommits: pcs,
	}
}

type timeoutError struct{}

func (timeoutError) Error() string   { return "request timed out" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func (c *fakeChain) ChainHead(context.Context) (node.ChainHead, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return node.ChainHead{ChainID: c.chainID, Height: c.head, Time: c.timeOf(c.head)}, nil
}

func (c *fakeChain) Commits(_ context.Context, heights []int64) ([]node.Commit, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.batches = append(c.batches, slices.Clone(heights))
	if len(c.fail) > 0 {
		err := c.fail[0]
		c.fail = c.fail[1:]
		return nil, err
	}
	for _, h := range heights {
		if err, ok := c.broken[h]; ok {
			return nil, err
		}
	}
	if c.maxBatch > 0 && len(heights) > c.maxBatch {
		return nil, fmt.Errorf("batch of %d calls: %w", len(heights), &node.RPCError{Code: -32600, Message: "Invalid Request", Data: "batch too large"})
	}
	if c.slowOver > 0 && len(heights) > c.slowOver {
		return nil, timeoutError{}
	}
	var out []node.Commit
	for _, h := range heights {
		if c.refused[h] {
			return out, &node.HeightError{Height: h, Err: &node.RPCError{Code: -32603, Message: "Internal error", Data: "refused"}}
		}
		if h == c.refusedOnce {
			c.refusedOnce = 0
			return out, &node.HeightError{Height: h, Err: &node.RPCError{Code: -32603, Message: "Internal error", Data: "flaky backend"}}
		}
		if h < c.floor || h > c.head || (c.lagging > 0 && h > c.head-c.lagging) {
			return out, &node.HeightError{Height: h, Err: &node.RPCError{Code: -32603, Message: "Internal error", Data: fmt.Sprintf("height %d not available", h)}}
		}
		out = append(out, c.commit(h))
	}
	return out, nil
}

func (c *fakeChain) ValidatorSet(_ context.Context, h int64) ([]node.SetMember, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setCalls = append(c.setCalls, h)
	if len(c.setFail) > 0 {
		err := c.setFail[0]
		c.setFail = c.setFail[1:]
		return nil, err
	}
	if c.setRefused[h] {
		return nil, &node.RPCError{Code: -32603, Message: "Internal error", Data: fmt.Sprintf("height %d not available", h)}
	}
	if c.emptySets > 0 {
		c.emptySets--
		return []node.SetMember{}, nil
	}
	return slices.Clone(c.setAt(h)), nil
}

func (c *fakeChain) BlockEvents(_ context.Context, h int64) ([]node.BlockEvent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.eventCalls = append(c.eventCalls, h)
	if h < c.floor {
		return nil, &node.RPCError{Code: -32603, Message: "Internal error", Data: fmt.Sprintf("height %d not available", h)}
	}
	if len(c.eventFail) > 0 {
		err := c.eventFail[0]
		c.eventFail = c.eventFail[1:]
		return nil, err
	}
	return c.events[h], nil
}

type testTracker struct {
	*Tracker
	store *Store
	logs  []string
	waits []time.Duration
}

func newTestTracker(t *testing.T, c *fakeChain) *testTracker {
	t.Helper()
	return newTestTrackerOn(t, c, newTestStore(t), DefaultConfig())
}

// newTestTrackerOn returns a tracker of c into s, logging and sleeping into
// the testTracker, on a clock a second further at each reading.
func newTestTrackerOn(t *testing.T, c *fakeChain, s *Store, cfg Config) *testTracker {
	t.Helper()
	tr, err := NewTracker(s, c, cfg)
	if err != nil {
		t.Fatal(err)
	}
	tt := &testTracker{Tracker: tr, store: s}
	tt.logf = func(format string, args ...any) { tt.logs = append(tt.logs, fmt.Sprintf(format, args...)) }
	tt.sleep = func(ctx context.Context, d time.Duration) error {
		tt.waits = append(tt.waits, d)
		return ctx.Err()
	}
	at := t0
	tt.now = func() time.Time {
		at = at.Add(time.Second)
		return at
	}
	return tt
}

func TestNewTrackerRejectsABadConfig(t *testing.T) {
	for _, cfg := range []Config{{Pace: time.Second, Batch: 0}, {Pace: -time.Second, Batch: 100}} {
		if _, err := NewTracker(newTestStore(t), newFakeChain(10), cfg); err == nil {
			t.Errorf("NewTracker(%+v) accepted it", cfg)
		}
	}
}

func TestPassAsksTheConfiguredBatch(t *testing.T) {
	c := newFakeChain(250)
	tt := newTestTrackerOn(t, c, newTestStore(t), Config{Pace: 0, Batch: 30})
	mustPass(t, tt)
	if sizes := batchSizes(c); !slices.Equal(sizes, []int{30}) {
		t.Errorf("batch sizes = %v, want [30]", sizes)
	}
}

func (tt *testTracker) logged(part string) bool {
	return slices.ContainsFunc(tt.logs, func(l string) bool { return strings.Contains(l, part) })
}

func mustPass(t *testing.T, tt *testTracker) bool {
	t.Helper()
	more, err := tt.pass(context.Background())
	if err != nil {
		t.Fatalf("pass() = %v", err)
	}
	return more
}

func wantRange(t *testing.T, s *Store, low, high int64) {
	t.Helper()
	gotLow, gotHigh, err := s.Range(context.Background())
	if err != nil || gotLow != low || gotHigh != high {
		t.Errorf("Range() = %d, %d, %v; want %d, %d", gotLow, gotHigh, err, low, high)
	}
}

func span(b []int64) string {
	if len(b) == 0 {
		return "[]"
	}
	return fmt.Sprintf("%d..%d", b[0], b[len(b)-1])
}

func TestPassStartsBelowTheHeadOnAnEmptyStore(t *testing.T) {
	c := newFakeChain(250)
	tt := newTestTracker(t, c)
	if !mustPass(t, tt) {
		t.Error("pass() reported nothing left below")
	}
	// The head's own commit is not final: the store starts one below it.
	wantRange(t, tt.store, 150, 249)
	if len(c.batches) != 1 || span(c.batches[0]) != "249..150" {
		t.Errorf("batches = %v, want one of 249..150", c.batches)
	}
}

func TestPassStoresUpToTheHeadThenOneBatchBelow(t *testing.T) {
	c := newFakeChain(250)
	tt := newTestTracker(t, c)
	mustPass(t, tt)
	c.head = 400 // further than one batch: the head pass takes two
	mustPass(t, tt)
	wantRange(t, tt.store, 51, 399)
	var got []string
	for _, b := range c.batches[1:] {
		got = append(got, span(b))
	}
	// Below the range, the lowest stored height, 150, opens the batch of 100.
	if want := []string{"250..349", "350..399", "150..51"}; !slices.Equal(got, want) {
		t.Errorf("batches = %v, want %v", got, want)
	}
}

func TestPassCatchesUpAtThePaceAndShowsIt(t *testing.T) {
	c := newFakeChain(250)
	tt := newTestTracker(t, c)
	mustPass(t, tt) // 249..150
	// Back after downtime: 350 heights to the head, in batches of 100.
	c.head = 600
	var shown []int64
	tt.sleep = func(ctx context.Context, d time.Duration) error {
		tt.waits = append(tt.waits, d)
		shown = append(shown, tt.Progress().To)
		return ctx.Err()
	}
	mustPass(t, tt)
	if !slices.Equal(tt.waits, []time.Duration{DefaultPace, DefaultPace, DefaultPace}) {
		t.Errorf("waits = %v, want the pace between the 4 batches", tt.waits)
	}
	if !slices.Equal(shown, []int64{349, 449, 549}) {
		t.Errorf("progress shown between batches = %v, want [349 449 549]", shown)
	}
}

func TestPassCompletesAtGenesis(t *testing.T) {
	c := newFakeChain(120)
	tt := newTestTracker(t, c)
	if !mustPass(t, tt) {
		t.Error("first pass reported nothing left below")
	}
	if mustPass(t, tt) {
		t.Error("second pass reported heights left below 1")
	}
	wantRange(t, tt.store, 1, 119)
	p := tt.Progress()
	if want := (Progress{ChainID: "gnoland-1", From: 1, To: 119, FromTime: c.timeOf(1), Complete: true}); p != want {
		t.Errorf("Progress() = %+v, want %+v", p, want)
	}
	if !tt.logged("history complete from #1") {
		t.Errorf("logs = %q, want the completion", tt.logs)
	}
}

func TestPassSetsTheFloorWhereTheSourceStops(t *testing.T) {
	c := newFakeChain(2300)
	c.floor = 1000
	tt := newTestTracker(t, c)
	for i := 0; mustPass(t, tt); i++ {
		if i == 20 {
			t.Fatal("the backfill never ended")
		}
	}
	wantRange(t, tt.store, 1000, 2299)
	if f, _ := tt.store.Floor(context.Background()); f != 1000 {
		t.Errorf("Floor() = %d, want 1000", f)
	}
	if !tt.Progress().Complete || !tt.logged("serves no block below #1000") {
		t.Errorf("Progress() = %+v, logs = %q; want complete at the floor", tt.Progress(), tt.logs)
	}
}

func TestPassStopsBeforeACommitNotFinalYet(t *testing.T) {
	c := newFakeChain(250)
	tt := newTestTracker(t, c)
	mustPass(t, tt)
	// A backend one block behind the one that gave the head: its latest
	// height, 252, is the tracker's top, served with its own commit. The pass
	// stops below it, without an error, and goes on with the backfill.
	c.head, c.lagging = 253, 1
	mustPass(t, tt)
	wantRange(t, tt.store, 51, 251)
	if len(tt.logs) != 0 {
		t.Errorf("logs = %q, want none: a lagging backend is routine", tt.logs)
	}
	c.lagging = 0
	mustPass(t, tt)
	if b := storedBlock(t, tt.store, 252); len(b.Absences) != 0 {
		t.Errorf("block 252 = %+v, want it stored from the next block's commit, without absence", b)
	}
}

func TestPassWaitsQuietlyForAHeightNearTheHead(t *testing.T) {
	c := newFakeChain(500)
	c.lagging = 10 // a backend 10 blocks behind the one that gave the head
	tt := newTestTracker(t, c)
	if !mustPass(t, tt) {
		t.Error("pass() reported nothing left below")
	}
	wantRange(t, tt.store, 0, 0)
	if f, _ := tt.store.Floor(context.Background()); f != 0 {
		t.Errorf("Floor() = %d, want none: the refusal is near the head", f)
	}
	if len(tt.logs) != 0 {
		t.Errorf("logs = %q, want none: a lagging backend is routine", tt.logs)
	}
	c.lagging = 0
	mustPass(t, tt)
	wantRange(t, tt.store, 400, 499)
}

func TestPassSetsNoFloorBehindABackendFarBehind(t *testing.T) {
	c := newFakeChain(5000)
	tt := newTestTrackerOn(t, c, newTestStore(t), Config{Pace: 0, Batch: 1100})
	at := t0
	tt.now = func() time.Time { return at }
	mustPass(t, tt) // 4999 to 3900
	// A backend 2,000 blocks behind refuses every height above its own,
	// the lowest stored height among them, pass after pass.
	c.lagging = 2000
	for range 3 {
		mustPass(t, tt)
		at = at.Add(gapRetry)
	}
	if f, _ := tt.store.Floor(context.Background()); f != 0 || tt.Progress().Complete {
		t.Errorf("Floor() = %d, complete %v; want no floor", f, tt.Progress().Complete)
	}
	if tt.logged("serves no block below") {
		t.Errorf("logs = %q, want no floor", tt.logs)
	}
}

func TestPassSetsNoFloorOnASingleRefusal(t *testing.T) {
	c := newFakeChain(5000)
	tt := newTestTrackerOn(t, c, newTestStore(t), Config{Pace: 0, Batch: 1000})
	mustPass(t, tt) // 4999 to 4000
	c.refusedOnce = 3500
	mustPass(t, tt) // 3999 to 3501 after the witness 4000, then 3500 refused once
	mustPass(t, tt) // 3500 served: on to 2502
	if f, _ := tt.store.Floor(context.Background()); f != 0 {
		t.Errorf("Floor() = %d, want none: one refusal is no floor", f)
	}
	wantRange(t, tt.store, 2502, 4999)
}

func TestPassRechecksTheFloorWithoutHoldingTheHead(t *testing.T) {
	c := newFakeChain(2300)
	c.floor = 1000
	tt := newTestTracker(t, c)
	for i := 0; mustPass(t, tt); i++ {
		if i == 20 {
			t.Fatal("the backfill never ended")
		}
	}
	// gnockpit restarts while the source fails the check below the floor.
	c.head, c.broken = 2400, map[int64]error{999: errors.New("502 bad gateway")}
	tt = newTestTrackerOn(t, c, tt.store, DefaultConfig())
	mustPass(t, tt)
	wantRange(t, tt.store, 1000, 2399)
	if f, _ := tt.store.Floor(context.Background()); f != 1000 {
		t.Errorf("Floor() = %d, want 1000 kept", f)
	}
	if !tt.logged("502 bad gateway") {
		t.Errorf("logs = %q, want the failed check", tt.logs)
	}
	// The check concludes on a later pass, once.
	c.broken = nil
	mustPass(t, tt)
	mustPass(t, tt)
	var checks int
	for _, b := range c.batches {
		if slices.Equal(b, []int64{999}) {
			checks++
		}
	}
	if checks != 2 {
		t.Errorf("floor checks = %d, want 2: the failed one, then one that concluded", checks)
	}
}

func behindReports(tt *testTracker) int {
	var n int
	for _, l := range tt.logs {
		if strings.HasPrefix(l, "blocks: behind:") {
			n++
		}
	}
	return n
}

func TestPassReportsABackendBehindForMinutes(t *testing.T) {
	c := newFakeChain(500)
	tt := newTestTracker(t, c)
	at := t0
	tt.now = func() time.Time { return at }
	mustPass(t, tt) // 499..400
	c.head, c.lagging = 600, 10
	for range 4 { // at 0, 1, 2 and 3 minutes
		mustPass(t, tt)
		at = at.Add(time.Minute)
	}
	if n := behindReports(tt); n != 1 {
		t.Errorf("logs = %q, want the backend behind reported once, after %s", tt.logs, behindReport)
	}
	c.lagging = 0
	mustPass(t, tt)
	if !tt.logged("blocks: behind ok again") {
		t.Errorf("logs = %q, want the recovery", tt.logs)
	}
	// Behind again: reported only once it lasts behindReport again.
	c.head, c.lagging = 700, 10
	mustPass(t, tt)
	at = at.Add(time.Minute)
	mustPass(t, tt)
	if n := behindReports(tt); n != 1 {
		t.Errorf("logs = %q, want no report a minute into the new lag", tt.logs)
	}
	at = at.Add(behindReport)
	mustPass(t, tt)
	if n := behindReports(tt); n != 2 {
		t.Errorf("logs = %q, want the new lag reported", tt.logs)
	}
}

func TestPassReportsABackendBehindMetGoingDown(t *testing.T) {
	c := newFakeChain(500)
	c.lagging = 10 // an empty store: only the backfill meets it
	tt := newTestTracker(t, c)
	at := t0
	tt.now = func() time.Time { return at }
	for range 4 {
		mustPass(t, tt)
		at = at.Add(time.Minute)
	}
	if n := behindReports(tt); n != 1 {
		t.Errorf("logs = %q, want the backend behind reported once", tt.logs)
	}
}

func TestPassFailsOnTheBackfillsErrorBehindALaggingHead(t *testing.T) {
	c := newFakeChain(2300)
	tt := newTestTracker(t, c)
	mustPass(t, tt) // 2299..2200
	// Up, the head's height is not final yet; down, the batch fails.
	c.head, c.lagging = 2302, 1
	c.broken = map[int64]error{2150: errors.New("502 bad gateway")}
	if _, err := tt.pass(context.Background()); err == nil || !strings.Contains(err.Error(), "502 bad gateway") {
		t.Errorf("pass() = %v, want the backfill's failure", err)
	}
}

func TestPassSetsNoFloorGoingUp(t *testing.T) {
	c := newFakeChain(300)
	tt := newTestTracker(t, c)
	mustPass(t, tt) // 299..200
	// Back after a long downtime, the source refuses a height well below its
	// head, after serving the one below it: that is no floor, which lies below.
	c.head, c.refused = 5000, map[int64]bool{350: true}
	if _, err := tt.pass(context.Background()); err == nil || !strings.Contains(err.Error(), "height 350") {
		t.Fatalf("pass() = %v, want the refusal of 350", err)
	}
	// Asked first in the next answer, far below the head: still an error.
	if _, err := tt.pass(context.Background()); err == nil {
		t.Fatal("pass() = nil, want the refusal of 350 again")
	}
	if f, _ := tt.store.Floor(context.Background()); f != 0 {
		t.Errorf("Floor() = %d, want none", f)
	}
}

func TestPassRestoresTheBatchToTheRefusedCeiling(t *testing.T) {
	c := newFakeChain(2300)
	c.maxBatch = 30
	tt := newTestTracker(t, c)
	at := t0
	tt.now = func() time.Time { return at }
	for range 3 { // 100 refused, 50 refused, 25 served
		mustPass(t, tt)
	}
	c.fail = []error{timeoutError{}}
	mustPass(t, tt) // 25 time out: 12 for timeoutCut
	mustPass(t, tt)
	at = at.Add(timeoutCut)
	mustPass(t, tt) // back to 25, the size the source accepts, not 100
	if sizes := batchSizes(c); !slices.Equal(sizes, []int{100, 50, 25, 25, 12, 25}) {
		t.Errorf("batch sizes = %v, want [100 50 25 25 12 25]", sizes)
	}
}

func TestPassPausesAOneCallSourceAtItsFloor(t *testing.T) {
	c := newFakeChain(2300)
	c.floor, c.maxBatch = 1000, 1
	tt := newTestTrackerOn(t, c, newTestStore(t), Config{Pace: 0, Batch: 1})
	for i := 0; mustPass(t, tt); i++ {
		if i == 1400 {
			t.Fatal("the backfill never paused")
		}
	}
	// One call per batch proves no floor: 999 refused twice in a row pauses
	// the backfill, reported, rather than set one.
	wantRange(t, tt.store, 1000, 2299)
	if f, _ := tt.store.Floor(context.Background()); f != 0 || tt.Progress().Complete {
		t.Errorf("Floor() = %d, complete %v; want no floor", f, tt.Progress().Complete)
	}
	if !tt.logged("refuses #999, well below the head") || tt.logged("asking") {
		t.Errorf("logs = %q, want the refusal reported and no batch refused", tt.logs)
	}
}

func TestPassSetsTheFloorOfATwoCallSource(t *testing.T) {
	c := newFakeChain(2300)
	tt := newTestTracker(t, c)
	for range 10 {
		mustPass(t, tt) // 2299 to 1300
	}
	// The source takes two calls a batch at most: the refused batches halve
	// down to two calls, enough for a witness and the height below it.
	c.floor, c.maxBatch = 1250, 2
	for i := 0; mustPass(t, tt); i++ {
		if i == 200 {
			t.Fatal("the backfill never ended")
		}
	}
	if f, _ := tt.store.Floor(context.Background()); f != 1250 || !tt.Progress().Complete {
		t.Errorf("Floor() = %d, complete %v; want 1250, complete", f, tt.Progress().Complete)
	}
}

func TestPassPausesTheBackfillWhenTheLowestStoredHeightIsRefused(t *testing.T) {
	c := newFakeChain(5000)
	tt := newTestTrackerOn(t, c, newTestStore(t), Config{Pace: 0, Batch: 1100})
	at := t0
	tt.now = func() time.Time { return at }
	mustPass(t, tt) // 4999 to 3900
	// The source's oldest block rises above the stored history: the witness,
	// 3900, refused twice in a row pauses the backfill.
	c.floor = 4200
	mustPass(t, tt)
	mustPass(t, tt)
	asked := len(c.batches)
	if mustPass(t, tt) {
		t.Error("pass() reported heights left below while paused: passes go at the sync interval")
	}
	if len(c.batches) != asked {
		t.Errorf("batches = %d, want %d: the backfill pauses", len(c.batches), asked)
	}
	at = at.Add(gapRetry)
	asked = len(c.batches)
	mustPass(t, tt) // asked again: refused twice again
	mustPass(t, tt)
	if n := len(c.batches) - asked; n != 2 {
		t.Errorf("batches after the pause = %d, want 2: a pause ends on a first refusal", n)
	}
	var reports int
	for _, l := range tt.logs {
		if strings.Contains(l, "refuses #3900, well below the head") {
			reports++
		}
	}
	if reports != 1 {
		t.Errorf("logs = %q, want the refusal reported once", tt.logs)
	}
	if f, _ := tt.store.Floor(context.Background()); f != 0 || tt.Progress().Complete {
		t.Errorf("Floor() = %d, complete %v; want no floor", f, tt.Progress().Complete)
	}
	c.floor = 0
	at = at.Add(gapRetry)
	mustPass(t, tt)
	wantRange(t, tt.store, 2801, 4999)
	if !tt.logged("blocks: below ok again") {
		t.Errorf("logs = %q, want the recovery", tt.logs)
	}
}

func TestPassPausesTheBackfillWhileTheHeadMoves(t *testing.T) {
	c := newFakeChain(5000)
	tt := newTestTrackerOn(t, c, newTestStore(t), Config{Pace: 0, Batch: 1100})
	mustPass(t, tt) // 4999 to 3900
	// The witness, 3900, is refused twice in a row going down, while the
	// heights going up are served.
	c.floor = 4200
	for range 2 {
		c.head++
		mustPass(t, tt)
	}
	if !tt.logged("refuses #3900, well below the head") {
		t.Errorf("logs = %q, want the backfill paused", tt.logs)
	}
}

func TestPassPausesNotOnRefusalsAServedAnswerSeparates(t *testing.T) {
	c := newFakeChain(5000)
	tt := newTestTrackerOn(t, c, newTestStore(t), Config{Pace: 0, Batch: 1100})
	mustPass(t, tt) // 4999 to 3900
	// The witness, 3900, is refused, then served before 3899's refusal, then
	// refused again: its refusals are not in a row.
	for _, h := range []int64{3900, 3899, 3900} {
		c.refusedOnce = h
		mustPass(t, tt)
	}
	if tt.logged("refuses") {
		t.Errorf("logs = %q, want no pause", tt.logs)
	}
}

func TestPassReportsAPausedBackfillAsNoSourceBehind(t *testing.T) {
	c := newFakeChain(5000)
	tt := newTestTrackerOn(t, c, newTestStore(t), Config{Pace: 0, Batch: 1100})
	at := t0
	tt.now = func() time.Time { return at }
	mustPass(t, tt) // 4999 to 3900
	// The witness is refused first, as by a backend behind, then again
	// minutes later: the pass that pauses reports the refusal only.
	c.floor = 4200
	mustPass(t, tt)
	at = at.Add(behindReport)
	mustPass(t, tt)
	if !tt.logged("refuses #3900") || tt.logged("has not given") {
		t.Errorf("logs = %q, want the pause reported, and no source behind", tt.logs)
	}
}

func TestPassPausesNotOnASingleRefusedWitness(t *testing.T) {
	c := newFakeChain(5000)
	tt := newTestTrackerOn(t, c, newTestStore(t), Config{Pace: 0, Batch: 1100})
	mustPass(t, tt)      // 4999 to 3900
	c.refusedOnce = 3900 // one backend far behind, once
	mustPass(t, tt)
	mustPass(t, tt)
	wantRange(t, tt.store, 2801, 4999)
	if tt.logged("refuses") {
		t.Errorf("logs = %q, want no pause on one refusal", tt.logs)
	}
}

func TestPassKeepsTheFloorWhenItsCheckIsRefused(t *testing.T) {
	c := newFakeChain(2300)
	c.floor = 1000
	tt := newTestTracker(t, c)
	for i := 0; mustPass(t, tt); i++ {
		if i == 20 {
			t.Fatal("the backfill never ended")
		}
	}
	// The source's oldest block rises above the floor, and gnockpit restarts:
	// the check concludes the floor stands, once.
	c.floor = 1200
	tt = newTestTrackerOn(t, c, tt.store, DefaultConfig())
	for range 3 {
		mustPass(t, tt)
	}
	var checks int
	for _, b := range c.batches {
		if slices.Equal(b, []int64{999}) {
			checks++
		}
	}
	if checks != 1 {
		t.Errorf("floor checks = %d, want 1", checks)
	}
	if f, _ := tt.store.Floor(context.Background()); f != 1000 {
		t.Errorf("Floor() = %d, want 1000 kept", f)
	}
}

func TestPassRechecksTheFloorAgainAfterAWholeBatchRefusal(t *testing.T) {
	c := newFakeChain(2300)
	c.floor = 1000
	tt := newTestTracker(t, c)
	for i := 0; mustPass(t, tt); i++ {
		if i == 20 {
			t.Fatal("the backfill never ended")
		}
	}
	// The source becomes an archive node, and gnockpit restarts while it
	// refuses the check's batch whole: a refusal of no height in particular
	// settles nothing.
	c.floor = 0
	c.fail = []error{fmt.Errorf("batch of 1 calls: %w", &node.RPCError{Code: -32600, Message: "Invalid Request"})}
	tt = newTestTrackerOn(t, c, tt.store, DefaultConfig())
	mustPass(t, tt)
	if f, _ := tt.store.Floor(context.Background()); f != 1000 {
		t.Errorf("Floor() = %d, want 1000 kept until a check concludes", f)
	}
	mustPass(t, tt)
	if f, _ := tt.store.Floor(context.Background()); f != 0 {
		t.Errorf("Floor() = %d, want none: the next check finds the heights below served", f)
	}
}

func TestPassWaitsOutARateLimitOnEveryRead(t *testing.T) {
	c := newFakeChain(250)
	c.change = 200
	c.events[198] = addedEvents
	c.setFail = []error{&node.HTTPStatusError{StatusCode: http.StatusTooManyRequests, RetryAfter: 3 * time.Second}}
	c.eventFail = []error{&node.HTTPStatusError{StatusCode: http.StatusTooManyRequests, RetryAfter: 4 * time.Second}}
	tt := newTestTracker(t, c)
	mustPass(t, tt)
	if !slices.Equal(tt.waits, []time.Duration{3 * time.Second, 4 * time.Second}) {
		t.Errorf("waits = %v, want [3s 4s]", tt.waits)
	}
	wantRange(t, tt.store, 150, 249)
	if got := storedEvents(t, tt.store, 200); len(got) != 1 {
		t.Errorf("events of 200 = %v, want one", got)
	}
}

func TestPassRechecksTheFloorAtStart(t *testing.T) {
	c := newFakeChain(2300)
	c.floor = 1000
	tt := newTestTracker(t, c)
	for i := 0; mustPass(t, tt); i++ {
		if i == 20 {
			t.Fatal("the backfill never ended")
		}
	}
	// The source becomes an archive node, and gnockpit restarts.
	c.floor = 0
	tt = newTestTrackerOn(t, c, tt.store, DefaultConfig())
	mustPass(t, tt)
	if f, _ := tt.store.Floor(context.Background()); f != 0 {
		t.Errorf("Floor() = %d, want none: the source serves the heights below", f)
	}
	wantRange(t, tt.store, 901, 2299)
	if !tt.logged("the source serves blocks below #1000") {
		t.Errorf("logs = %q, want the lowered floor", tt.logs)
	}
}

func batchSizes(c *fakeChain) []int {
	var sizes []int
	for _, b := range c.batches {
		sizes = append(sizes, len(b))
	}
	return sizes
}

func TestPassHalvesABatchTheSourceRefuses(t *testing.T) {
	c := newFakeChain(250)
	c.maxBatch = 30
	tt := newTestTracker(t, c)
	at := t0
	tt.now = func() time.Time { return at }
	for range 3 {
		mustPass(t, tt)
	}
	wantRange(t, tt.store, 225, 249)
	if !tt.logged("asking 50 at a time") || !tt.logged("asking 25 at a time") {
		t.Errorf("logs = %q, want each halving", tt.logs)
	}
	// A refusal is the source's limit: the batch stays under it for good.
	at = at.Add(timeoutCut)
	mustPass(t, tt)
	if sizes := batchSizes(c); !slices.Equal(sizes, []int{100, 50, 25, 25}) {
		t.Errorf("batch sizes = %v, want [100 50 25 25]", sizes)
	}
}

func TestPassRestoresTheBatchATimeoutCut(t *testing.T) {
	c := newFakeChain(400)
	c.fail = []error{timeoutError{}}
	tt := newTestTracker(t, c)
	at := t0
	tt.now = func() time.Time { return at }
	mustPass(t, tt)
	mustPass(t, tt)
	// A slow answer can be transient: the cut lasts timeoutCut.
	at = at.Add(timeoutCut)
	mustPass(t, tt)
	if sizes := batchSizes(c); !slices.Equal(sizes, []int{100, 50, 100}) {
		t.Errorf("batch sizes = %v, want [100 50 100]", sizes)
	}
}

func TestPassHalvesTheBatchSizeOnAShortBatchTimingOut(t *testing.T) {
	c := newFakeChain(250)
	tt := newTestTracker(t, c)
	mustPass(t, tt) // 249..150
	// Three heights to the head time out: the batch size halves, from 100,
	// and the next pass asks them again.
	c.head, c.fail = 253, []error{timeoutError{}}
	if _, err := tt.pass(context.Background()); !isTimeout(err) {
		t.Fatalf("pass() = %v, want the timeout", err)
	}
	mustPass(t, tt)
	if sizes := batchSizes(c); !slices.Equal(sizes, []int{100, 3, 3, 50}) {
		t.Errorf("batch sizes = %v, want [100 3 3 50]", sizes)
	}
}

func TestPassEndsOnAShortBatchTimingOut(t *testing.T) {
	c := newFakeChain(250)
	tt := newTestTracker(t, c)
	mustPass(t, tt) // 249..150
	// Three heights to the head keep timing out: the halved size does not
	// shrink them, so the pass ends rather than asking them again.
	c.head = 253
	for range 10 {
		c.fail = append(c.fail, timeoutError{})
	}
	if _, err := tt.pass(context.Background()); !isTimeout(err) {
		t.Fatalf("pass() = %v, want the timeout", err)
	}
	if sizes := batchSizes(c); !slices.Equal(sizes, []int{100, 3}) {
		t.Errorf("batch sizes = %v, want [100 3]", sizes)
	}
}

func TestPassEndsOnABatchOfHalfTheSizeTimingOut(t *testing.T) {
	c := newFakeChain(250)
	tt := newTestTracker(t, c)
	mustPass(t, tt) // 249..150
	// Fifty heights to the head keep timing out: the size halves from 100 to
	// their count, so asking them again would time out the same way.
	c.head = 300
	for range 10 {
		c.fail = append(c.fail, timeoutError{})
	}
	if _, err := tt.pass(context.Background()); !isTimeout(err) {
		t.Fatalf("pass() = %v, want the timeout", err)
	}
	if sizes := batchSizes(c); !slices.Equal(sizes, []int{100, 50}) {
		t.Errorf("batch sizes = %v, want [100 50]", sizes)
	}
}

func TestPassHalvesARefusedShortBatchFromItsLength(t *testing.T) {
	c := newFakeChain(250)
	tt := newTestTracker(t, c)
	mustPass(t, tt) // 249..150
	// The source takes 2 calls a batch: 4 refused give 2, not half of 100.
	c.head, c.maxBatch = 254, 2
	mustPass(t, tt)
	if sizes := batchSizes(c)[1:]; !slices.Equal(sizes, []int{4, 2, 2, 2}) {
		t.Errorf("batch sizes = %v, want [4 2 2 2]", sizes)
	}
}

func TestPassHalvesABatchThatTimesOut(t *testing.T) {
	c := newFakeChain(250)
	c.slowOver = 40
	tt := newTestTracker(t, c)
	for range 3 {
		mustPass(t, tt)
	}
	wantRange(t, tt.store, 225, 249)
}

func TestPassWaitsOutARateLimit(t *testing.T) {
	c := newFakeChain(250)
	c.fail = []error{&node.HTTPStatusError{StatusCode: http.StatusTooManyRequests, RetryAfter: 7 * time.Second}}
	tt := newTestTracker(t, c)
	mustPass(t, tt)
	if !slices.Equal(tt.waits, []time.Duration{7 * time.Second}) {
		t.Errorf("waits = %v, want [7s]", tt.waits)
	}
	wantRange(t, tt.store, 150, 249)
	if !tt.logged("RPC rate limited, pausing for 7s") {
		t.Errorf("logs = %q, want the pause", tt.logs)
	}
}

func TestPassStoresNothingOnATransportError(t *testing.T) {
	c := newFakeChain(250)
	c.fail = []error{errors.New("connection reset by peer")}
	tt := newTestTracker(t, c)
	if _, err := tt.pass(context.Background()); err == nil {
		t.Fatal("pass() = nil, want the transport error")
	}
	wantRange(t, tt.store, 0, 0)
	mustPass(t, tt)
	wantRange(t, tt.store, 150, 249)
}

func TestPassLooksUpEachValidatorSetOnce(t *testing.T) {
	c := newFakeChain(250)
	tt := newTestTracker(t, c)
	mustPass(t, tt)
	mustPass(t, tt)
	if !slices.Equal(c.setCalls, []int64{249}) {
		t.Errorf("validator set calls = %v, want [249]: the first height met with SET1", c.setCalls)
	}
}

func TestPassComputesTheRoundsOfALateBlock(t *testing.T) {
	c := newFakeChain(250)
	c.round[200] = 1
	tt := newTestTracker(t, c)
	mustPass(t, tt)
	props, err := roundProposers(c.setAt(199), c.setAt(200), 1)
	if err != nil {
		t.Fatal(err)
	}
	if got := storedRounds(t, tt.store, 200); !slices.Equal(got, props) {
		t.Errorf("rounds of 200 = %v, want %v", got, props)
	}
	if reason := storedFlag(t, tt.store, 200); reason != "" {
		t.Errorf("flag of 200 = %q, want none", reason)
	}
}

func TestPassFlagsABlockNoRoundOfWhichExpectsItsProposer(t *testing.T) {
	c := newFakeChain(250)
	c.round[200] = 1
	props, _ := roundProposers(c.setAt(199), c.setAt(200), 1)
	for _, v := range []string{valG0Y, val15T, valHQH} {
		if !slices.Contains(props, v) {
			c.proposer[200] = v
		}
	}
	tt := newTestTracker(t, c)
	mustPass(t, tt)
	if reason := storedFlag(t, tt.store, 200); !strings.Contains(reason, "no round's expected proposer") {
		t.Errorf("flag of 200 = %q, want the missing round", reason)
	}
}

func TestPassFlagsALateBlockWhoseSetBelowIsRefused(t *testing.T) {
	c := newFakeChain(2300)
	c.round[1000] = 1
	c.setRefused = map[int64]bool{999: true}
	tt := newTestTrackerOn(t, c, newTestStore(t), Config{Pace: 0, Batch: 2400})
	// One refusal can be a fluke: the batch is asked again, and a second
	// refusal flags the block.
	if _, err := tt.pass(context.Background()); err == nil {
		t.Fatal("pass() = nil, want the first refusal asked again")
	}
	mustPass(t, tt)
	wantRange(t, tt.store, 1, 2299)
	if got := storedRounds(t, tt.store, 1000); len(got) != 2 || got[0] != "" || got[1] != c.expected(1000, 1) {
		t.Errorf("rounds of 1000 = %v, want round 0 unknown, round 1 expected", got)
	}
	if reason := storedFlag(t, tt.store, 1000); !strings.Contains(reason, "the source does not serve the set at #999") {
		t.Errorf("flag of 1000 = %q, want the refused set below", reason)
	}
}

func TestPassFlagsTwoLateBlocksWhoseSetsBelowAreRefused(t *testing.T) {
	c := newFakeChain(2300)
	c.round[1100], c.round[1050] = 1, 1
	c.setRefused = map[int64]bool{1099: true, 1049: true}
	tt := newTestTracker(t, c)
	for range 60 {
		more, err := tt.pass(context.Background())
		if err == nil && !more {
			break
		}
	}
	wantRange(t, tt.store, 1, 2299)
	for _, h := range []int64{1100, 1050} {
		if reason := storedFlag(t, tt.store, h); !strings.Contains(reason, fmt.Sprintf("does not serve the set at #%d", h-1)) {
			t.Errorf("flag of %d = %q, want the refused set below", h, reason)
		}
	}
}

func TestPassFlagsNoLateBlockWhoseSetBelowIsServedBetweenRefusals(t *testing.T) {
	c := newFakeChain(2300)
	c.round[1100], c.round[1050] = 1, 1
	tt := newTestTrackerOn(t, c, newTestStore(t), Config{Pace: 0, Batch: 2400})
	// 1099's set is refused, then served while 1049's refusal loses the
	// batch, then refused again: the refusals are not in a row.
	for _, refused := range []int64{1099, 1049, 1099} {
		c.setRefused = map[int64]bool{refused: true}
		if _, err := tt.pass(context.Background()); err == nil {
			t.Fatalf("pass() with the set at %d refused = nil, want a first refusal asked again", refused)
		}
	}
	c.setRefused = nil
	mustPass(t, tt)
	if reason := storedFlag(t, tt.store, 1100); reason != "" {
		t.Errorf("flag of 1100 = %q, want none: the set below was served", reason)
	}
}

func TestPassForgetsARefusedSetOnAnotherChain(t *testing.T) {
	c := newFakeChain(2300)
	c.round[1000] = 1
	tt := newTestTrackerOn(t, c, newTestStore(t), Config{Pace: 0, Batch: 2400})
	mustPass(t, tt)        // the chain the store holds
	tt.setRefused[999] = 1 // refused once on it
	c.chainID, c.setRefused = "test-13", map[int64]bool{999: true}
	// On the new chain, 999's refusal is a first one: the batch is asked again.
	if _, err := tt.pass(context.Background()); err == nil {
		t.Error("pass() = nil, want the first refusal on this chain asked again")
	}
}

func TestPassForgetsABackfillPauseOnAnotherChain(t *testing.T) {
	c := newFakeChain(250)
	tt := newTestTracker(t, c)
	mustPass(t, tt)               // the chain the store holds
	tt.belowAt = t0.Add(gapRetry) // paused on it
	c.chainID = "test-13"
	mustPass(t, tt)
	wantRange(t, tt.store, 150, 249)
}

func TestPassFlagsNoLateBlockOnASingleSetRefusal(t *testing.T) {
	c := newFakeChain(2300)
	c.round[1000] = 1
	c.setRefused = map[int64]bool{999: true}
	tt := newTestTrackerOn(t, c, newTestStore(t), Config{Pace: 0, Batch: 2400})
	if _, err := tt.pass(context.Background()); err == nil {
		t.Fatal("pass() = nil, want the refusal asked again")
	}
	c.setRefused = nil
	mustPass(t, tt)
	if reason := storedFlag(t, tt.store, 1000); reason != "" {
		t.Errorf("flag of 1000 = %q, want none", reason)
	}
	if got := storedRounds(t, tt.store, 1000); len(got) != 2 || got[0] == "" {
		t.Errorf("rounds of 1000 = %v, want both known", got)
	}
}

func TestPassRetriesALateBlockWhoseSetBelowIsRefusedNearTheHead(t *testing.T) {
	c := newFakeChain(250)
	c.round[200] = 1
	c.setRefused = map[int64]bool{199: true} // a lagging backend's refusal
	tt := newTestTracker(t, c)
	if _, err := tt.pass(context.Background()); err == nil {
		t.Fatal("pass() = nil, want the set at 199 retried, not 200 flagged for good")
	}
	c.setRefused = nil
	mustPass(t, tt)
	if reason := storedFlag(t, tt.store, 200); reason != "" {
		t.Errorf("flag of 200 = %q, want none", reason)
	}
}

func TestPassChecksEveryThousandthBlock(t *testing.T) {
	for _, tc := range []struct {
		name     string
		proposer string
		flagged  bool
	}{
		{"as expected", "", false},
		{"not the expected proposer", "other", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newFakeChain(1100)
			if tc.proposer != "" {
				want := c.expected(1000, 0)
				for _, v := range []string{valG0Y, val15T, valHQH} {
					if v != want {
						c.proposer[1000] = v
					}
				}
			}
			tt := newTestTracker(t, c)
			mustPass(t, tt)
			if !slices.Contains(c.setCalls, 999) || !slices.Contains(c.setCalls, 1000) {
				t.Errorf("validator set calls = %v, want 999 and 1000 checked", c.setCalls)
			}
			if got := storedFlag(t, tt.store, 1000) != ""; got != tc.flagged {
				t.Errorf("1000 flagged = %v, want %v", got, tc.flagged)
			}
			// A check at round 0 flags; it keeps no round proposers.
			if got := storedRounds(t, tt.store, 1000); len(got) != 0 {
				t.Errorf("rounds of 1000 = %v, want none", got)
			}
		})
	}
}

// addedEvents is a validator set change at its source block, plus the same
// event from a realm outside gno.land/r/sys/validators/, left out.
var addedEvents = []node.BlockEvent{
	{Type: "ValidatorAdded", PkgPath: "gno.land/r/sys/validators/v0", Attrs: map[string]string{"op": "g1op", "signingAddr": valH97, "power": "10"}},
	{Type: "ValidatorAdded", PkgPath: "gno.land/r/demo/impostor", Attrs: map[string]string{"op": "g1x", "signingAddr": "g1x", "power": "1"}},
}

func TestPassReadsTheEventsOfASetChange(t *testing.T) {
	c := newFakeChain(250)
	c.change = 200
	c.events[198] = addedEvents
	tt := newTestTracker(t, c)
	mustPass(t, tt)
	if !slices.Equal(c.eventCalls, []int64{198}) {
		t.Errorf("event calls = %v, want [198]", c.eventCalls)
	}
	if got := storedEvents(t, tt.store, 200); !slices.Equal(got, []string{"ValidatorAdded " + valH97 + " 10 from 198"}) {
		t.Errorf("events of 200 = %v", got)
	}
}

func TestPassFailsABatchWhoseSetLookupFails(t *testing.T) {
	c := newFakeChain(250)
	c.setFail = []error{errors.New("validators unavailable")}
	tt := newTestTracker(t, c)
	if _, err := tt.pass(context.Background()); err == nil || !strings.Contains(err.Error(), "validator set at 249: validators unavailable") {
		t.Fatalf("pass() = %v, want the failed lookup of the set at 249", err)
	}
	// A block without its set cannot be stored: the batch is retried whole.
	wantRange(t, tt.store, 0, 0)
}

func TestPassRefusesAnEmptyValidatorSet(t *testing.T) {
	c := newFakeChain(250)
	c.emptySets = 1
	tt := newTestTracker(t, c)
	if _, err := tt.pass(context.Background()); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("pass() = %v, want the empty set refused", err)
	}
	mustPass(t, tt)
	if b := storedBlock(t, tt.store, 249); len(b.Absences) != 0 {
		t.Fatalf("block 249 = %+v", b)
	}
	if set, ok, err := tt.store.Valset(context.Background(), "SET1"); err != nil || !ok || len(set) != 3 {
		t.Errorf("Valset(SET1) = %v, %v, %v; want its 3 members", set, ok, err)
	}
}

func TestPassStoresTheHeightsWhenTheirEventsFail(t *testing.T) {
	c := newFakeChain(250)
	c.change = 200
	c.events[198] = addedEvents
	c.eventFail = []error{errors.New("block_results unavailable")}
	tt := newTestTracker(t, c)
	mustPass(t, tt)
	wantRange(t, tt.store, 150, 249)
	if got := storedEvents(t, tt.store, 200); len(got) != 0 {
		t.Errorf("events of 200 = %v, want none", got)
	}
	if !tt.logged("blocks: events: events of 198: block_results unavailable") {
		t.Errorf("logs = %q, want the failed read of 198", tt.logs)
	}
	if gaps, err := tt.store.EventGaps(context.Background()); err != nil || !slices.Equal(gaps, []int64{200}) {
		t.Errorf("EventGaps() = %v, %v; want [200]", gaps, err)
	}
}

func TestPassRetriesTheEventsItCouldNotRead(t *testing.T) {
	c := newFakeChain(250)
	c.change = 200
	c.events[198] = addedEvents
	c.eventFail = []error{errors.New("block_results unavailable")}
	tt := newTestTracker(t, c)
	at := t0
	tt.now = func() time.Time { return at }
	mustPass(t, tt)
	mustPass(t, tt) // before gapRetry: no new read
	if !slices.Equal(c.eventCalls, []int64{198}) {
		t.Errorf("event calls = %v, want [198]", c.eventCalls)
	}
	at = at.Add(gapRetry)
	mustPass(t, tt)
	if got := storedEvents(t, tt.store, 200); !slices.Equal(got, []string{"ValidatorAdded " + valH97 + " 10 from 198"}) {
		t.Errorf("events of 200 = %v", got)
	}
	if gaps, err := tt.store.EventGaps(context.Background()); err != nil || len(gaps) != 0 {
		t.Errorf("EventGaps() = %v, %v; want none", gaps, err)
	}
}

func TestEventsRecordAGapWhenAStoredSetCannotBeRead(t *testing.T) {
	ctx := context.Background()
	c := newFakeChain(250)
	tt := newTestTracker(t, c)
	mustPass(t, tt) // 249..150
	if _, err := tt.store.db.ExecContext(ctx, `ALTER TABLE blocks_valsets RENAME TO valsets_away`); err != nil {
		t.Fatal(err)
	}
	// 199's and 201's sets are read from the store, which fails.
	tt.events(ctx, []Record{{Height: 200, ValsetHash: "SET1"}})
	if _, err := tt.store.db.ExecContext(ctx, `ALTER TABLE valsets_away RENAME TO blocks_valsets`); err != nil {
		t.Fatal(err)
	}
	if gaps, err := tt.store.EventGaps(ctx); err != nil || !slices.Equal(gaps, []int64{200, 201}) {
		t.Errorf("EventGaps() = %v, %v; want [200 201]", gaps, err)
	}
}

func TestPassDropsAGapThatIsNoSetChange(t *testing.T) {
	c := newFakeChain(250)
	tt := newTestTracker(t, c)
	mustPass(t, tt)
	if err := tt.store.AddEventGap(context.Background(), 200); err != nil {
		t.Fatal(err)
	}
	tt.gapsAt = time.Time{}
	mustPass(t, tt)
	if len(c.eventCalls) != 0 || tt.logged("no validator set event") {
		t.Errorf("event calls = %v, logs = %q; want no read: 200 opens no new set", c.eventCalls, tt.logs)
	}
	if gaps, err := tt.store.EventGaps(context.Background()); err != nil || len(gaps) != 0 {
		t.Errorf("EventGaps() = %v, %v; want none", gaps, err)
	}
}

func TestPassDropsAGapBelowTheFloor(t *testing.T) {
	c := newFakeChain(2300)
	c.floor, c.change = 1000, 1001 // the change's events are at 999, which the source refuses
	tt := newTestTracker(t, c)
	at := t0
	tt.now = func() time.Time { return at }
	for i := 0; mustPass(t, tt); i++ {
		if i == 30 {
			t.Fatal("the backfill never ended")
		}
	}
	at = at.Add(gapRetry)
	mustPass(t, tt)
	if gaps, err := tt.store.EventGaps(context.Background()); err != nil || len(gaps) != 0 {
		t.Errorf("EventGaps() = %v, %v; want the gap at 1001 dropped", gaps, err)
	}
	if !tt.logged("the events of the change at #1001 are at #999, below the oldest block the source serves") {
		t.Errorf("logs = %q, want the dropped gap", tt.logs)
	}
}

func TestPassLogsASetChangeWithoutEvents(t *testing.T) {
	c := newFakeChain(250)
	c.change = 200
	tt := newTestTracker(t, c)
	mustPass(t, tt)
	if got := storedEvents(t, tt.store, 200); len(got) != 0 {
		t.Errorf("events of 200 = %v, want none", got)
	}
	if !tt.logged("blocks: no validator set event at #198 for the change at #200") {
		t.Errorf("logs = %q, want the change without events", tt.logs)
	}
}

func TestPassReadsTheEventsOfASetChangeAtABatchEdge(t *testing.T) {
	c := newFakeChain(250)
	c.change = 150 // the lowest height of the first batch
	c.events[148] = addedEvents
	tt := newTestTracker(t, c)
	mustPass(t, tt)
	if len(c.eventCalls) != 0 {
		t.Errorf("event calls after the first batch = %v, want none: 149 is not stored", c.eventCalls)
	}
	mustPass(t, tt)
	if !slices.Equal(c.eventCalls, []int64{148}) {
		t.Errorf("event calls = %v, want [148]", c.eventCalls)
	}
	if got := storedEvents(t, tt.store, 150); len(got) != 1 {
		t.Errorf("events of 150 = %v, want one", got)
	}
}

func TestPassDropsTheHistoryOfAnotherChain(t *testing.T) {
	c := newFakeChain(250)
	tt := newTestTracker(t, c)
	mustPass(t, tt)
	c.chainID = "test-13"
	c.head = 600
	mustPass(t, tt)
	wantRange(t, tt.store, 500, 599)
	if !tt.logged("dropping the block history of gnoland-1") {
		t.Errorf("logs = %q, want the drop", tt.logs)
	}
	if r := tt.Progress().Rate; r != 0 {
		t.Errorf("Rate = %v, want 0: the new chain's fill has one sample", r)
	}
}

func TestPassForgetsAFloorSightingOnAnotherChain(t *testing.T) {
	c := newFakeChain(2300)
	tt := newTestTrackerOn(t, c, newTestStore(t), Config{Pace: 0, Batch: 1400})
	mustPass(t, tt)     // the chain the store holds
	tt.floorSeen = 1000 // seen once on it
	c.chainID, c.floor = "test-13", 1000
	// On the new chain, 999's refusal right after 1000 is a first sighting.
	mustPass(t, tt)
	if f, _ := tt.store.Floor(context.Background()); f != 0 {
		t.Errorf("Floor() = %d, want none after one sighting on this chain", f)
	}
}

func TestProgressRateIgnoresAnEmptyStore(t *testing.T) {
	c := newFakeChain(1000)
	c.maxBatch = 50
	tt := newTestTracker(t, c)
	at := t0
	tt.now = func() time.Time { return at }
	mustPass(t, tt) // the batch of 100 is refused whole: the store stays empty
	at = at.Add(10 * time.Second)
	mustPass(t, tt) // 999..950
	if r := tt.Progress().Rate; r != 0 {
		t.Errorf("Rate after the first stored batch = %v, want 0: an empty store is no starting height", r)
	}
	at = at.Add(10 * time.Second)
	mustPass(t, tt) // 950 as the witness, then 949..901
	if r := tt.Progress().Rate; r != 4.9 {
		t.Errorf("Rate = %v, want 4.9 blocks/s", r)
	}
}

func TestProgressMeasuresTheFillRate(t *testing.T) {
	c := newFakeChain(1000)
	tt := newTestTracker(t, c)
	at := t0
	tt.now = func() time.Time { return at }
	mustPass(t, tt) // 999..900
	if r := tt.Progress().Rate; r != 0 {
		t.Errorf("Rate after one pass = %v, want 0: nothing to measure yet", r)
	}
	at = at.Add(10 * time.Second)
	// Below the stored range, a batch of 100 is the witness and 99 heights.
	mustPass(t, tt) // 899..801
	if r := tt.Progress().Rate; r != 9.9 {
		t.Errorf("Rate = %v, want 9.9 blocks/s", r)
	}
	// The rate is the last rateWindow's: a slower stretch shows at once.
	at = at.Add(rateWindow)
	mustPass(t, tt) // 800..702
	at = at.Add(50 * time.Second)
	mustPass(t, tt) // 701..603
	if r := tt.Progress().Rate; r != 1.98 {
		t.Errorf("Rate = %v, want 1.98 blocks/s over the last %s", r, rateWindow)
	}
}

func TestRunPacesItsPasses(t *testing.T) {
	for _, tc := range []struct {
		name string
		pace time.Duration
		fail []error
		want []time.Duration
	}{
		{"backfilling, then complete", DefaultPace, nil, []time.Duration{DefaultPace, syncInterval, syncInterval}},
		{"after an error", DefaultPace, []error{errors.New("connection reset by peer")}, []time.Duration{errorPause, DefaultPace, syncInterval}},
		{"at the configured pace", 0, nil, []time.Duration{0, syncInterval, syncInterval}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newFakeChain(150)
			c.fail = tc.fail
			tt := newTestTrackerOn(t, c, newTestStore(t), Config{Pace: tc.pace, Batch: DefaultBatch})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tt.sleep = func(ctx context.Context, d time.Duration) error {
				tt.waits = append(tt.waits, d)
				if len(tt.waits) == 3 {
					cancel()
				}
				return ctx.Err()
			}
			tt.Run(ctx)
			if !slices.Equal(tt.waits, tc.want) {
				t.Errorf("waits = %v, want %v", tt.waits, tc.want)
			}
		})
	}
}

func storedBlock(t *testing.T, s *Store, height int64) Record {
	t.Helper()
	got, err := s.Blocks(context.Background(), height, height)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Blocks) != 1 {
		t.Fatalf("Blocks(%d, %d) = %+v, want the block", height, height, got.Blocks)
	}
	return got.Blocks[0]
}

func storedRounds(t *testing.T, s *Store, height int64) []string {
	t.Helper()
	return storedBlock(t, s, height).Rounds
}

func storedFlag(t *testing.T, s *Store, height int64) string {
	t.Helper()
	return storedBlock(t, s, height).Flag
}

func storedEvents(t *testing.T, s *Store, height int64) []string {
	t.Helper()
	got, err := s.Blocks(context.Background(), height, height)
	if err != nil {
		t.Fatal(err)
	}
	change := got.Changes[height]
	var out []string
	for _, e := range change.Events {
		out = append(out, fmt.Sprintf("%s %s %d from %d", e.Type, e.Address, e.Power, change.Source))
	}
	return out
}

// toldChanges registers a handler on tt and returns the heights it is told.
func toldChanges(tt *testTracker) *[]int64 {
	told := &[]int64{}
	tt.OnValsetChange(func(c ValsetChange) { *told = append(*told, c.Height) })
	return told
}

func TestPassTellsTheValsetChangesStoredGoingUp(t *testing.T) {
	c := newFakeChain(250)
	c.change = 300
	tt := newTestTracker(t, c)
	told := toldChanges(tt)
	mustPass(t, tt) // 249 to 150
	mustPass(t, tt)
	c.head = 350
	for range 3 {
		mustPass(t, tt)
	}
	if !slices.Equal(*told, []int64{300}) {
		t.Errorf("told = %v, want [300] once", *told)
	}
}

func TestPassTellsNoValsetChangeFoundGoingDown(t *testing.T) {
	c := newFakeChain(2300)
	c.change = 1000
	tt := newTestTracker(t, c)
	told := toldChanges(tt)
	for i := 0; mustPass(t, tt); i++ {
		if i == 30 {
			t.Fatal("the backfill never ended")
		}
	}
	wantRange(t, tt.store, 1, 2299)
	if len(*told) != 0 {
		t.Errorf("told = %v, want none: the backfill found the change", *told)
	}
}

func TestPassTellsTheValsetChangesMissedDuringDowntime(t *testing.T) {
	c := newFakeChain(250)
	c.change = 300
	tt := newTestTracker(t, c)
	before := toldChanges(tt)
	mustPass(t, tt)
	mustPass(t, tt)
	// gnockpit stops while the chain goes past the change, then restarts.
	c.head = 350
	tt = newTestTrackerOn(t, c, tt.store, DefaultConfig())
	told := toldChanges(tt)
	mustPass(t, tt)
	if len(*before) != 0 || !slices.Equal(*told, []int64{300}) {
		t.Errorf("told = %v then %v, want none then [300]", *before, *told)
	}
}

func TestPassTellsNoValsetChangeOfANewChain(t *testing.T) {
	c := newFakeChain(250)
	c.change = 350
	tt := newTestTracker(t, c)
	told := toldChanges(tt)
	mustPass(t, tt)
	mustPass(t, tt) // the chain the store holds, told up to 249
	// Another chain, whose first fill goes down across its change at 350.
	c.chainID, c.head = "test-13", 400
	mustPass(t, tt)
	mustPass(t, tt)
	if len(*told) != 0 {
		t.Errorf("told = %v, want none: a new chain's first fill goes down", *told)
	}
}

package blocks

import (
	"context"
	"database/sql"
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
	mu       sync.Mutex
	chainID  string
	head     int64
	floor    int64            // heights below are refused
	lagging  int64            // a backend this far behind the head: it refuses the heights above its own latest, which it serves with its own commit; 0: none
	maxBatch int              // a larger batch is refused whole; 0: no limit
	slowOver int              // a larger batch times out; 0: never
	fail     []error          // errors Commits returns first, in order
	change   int64            // valH97 joins the set at this height; 0: never
	round    map[int64]int    // commit round per height, default 0
	proposer map[int64]string // header proposer per height, default the expected one
	events   map[int64][]node.BlockEvent

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
	if c.maxBatch > 0 && len(heights) > c.maxBatch {
		return nil, fmt.Errorf("batch of %d calls: %w", len(heights), &node.RPCError{Code: -32600, Message: "Invalid Request", Data: "batch too large"})
	}
	if c.slowOver > 0 && len(heights) > c.slowOver {
		return nil, timeoutError{}
	}
	var out []node.Commit
	for _, h := range heights {
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
	return slices.Clone(c.setAt(h)), nil
}

func (c *fakeChain) BlockEvents(_ context.Context, h int64) ([]node.BlockEvent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.eventCalls = append(c.eventCalls, h)
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
	s := newTestStore(t)
	tt := &testTracker{Tracker: NewTracker(s, c), store: s}
	tt.logf = func(format string, args ...any) { tt.logs = append(tt.logs, fmt.Sprintf(format, args...)) }
	tt.sleep = func(ctx context.Context, d time.Duration) error {
		tt.waits = append(tt.waits, d)
		return ctx.Err()
	}
	return tt
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
	wantRange(t, tt.store, 50, 399)
	var got []string
	for _, b := range c.batches[1:] {
		got = append(got, span(b))
	}
	if want := []string{"250..349", "350..399", "149..50"}; !slices.Equal(got, want) {
		t.Errorf("batches = %v, want %v", got, want)
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
	// height, 252, is the tracker's top, served with its own commit.
	c.head, c.lagging = 253, 1
	if _, err := tt.pass(context.Background()); err == nil {
		t.Fatal("pass() = nil, want the commit of 252 not final yet")
	}
	wantRange(t, tt.store, 150, 251)
	c.lagging = 0
	mustPass(t, tt)
	recs, _, err := tt.store.Blocks(context.Background(), 252, 252)
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 1 || len(recs[0].Absences) != 0 {
		t.Errorf("block 252 = %+v, want it stored from the next block's commit, without absence", recs)
	}
}

func TestPassRetriesARefusalNearTheHead(t *testing.T) {
	c := newFakeChain(500)
	c.lagging = 10 // a backend 10 blocks behind the one that gave the head
	tt := newTestTracker(t, c)
	var he *node.HeightError
	if _, err := tt.pass(context.Background()); !errors.As(err, &he) || he.Height != 499 {
		t.Fatalf("pass() = %v, want the refusal of 499", err)
	}
	if f, _ := tt.store.Floor(context.Background()); f != 0 {
		t.Errorf("Floor() = %d, want none: the refusal is near the head", f)
	}
	c.lagging = 0
	mustPass(t, tt)
	wantRange(t, tt.store, 400, 499)
}

func TestPassHalvesABatchTheSourceRefuses(t *testing.T) {
	c := newFakeChain(250)
	c.maxBatch = 30
	tt := newTestTracker(t, c)
	for range 3 {
		mustPass(t, tt)
	}
	wantRange(t, tt.store, 225, 249)
	var sizes []int
	for _, b := range c.batches {
		sizes = append(sizes, len(b))
	}
	if !slices.Equal(sizes, []int{100, 50, 25}) {
		t.Errorf("batch sizes = %v, want [100 50 25]", sizes)
	}
	if !tt.logged("asking 50 at a time") || !tt.logged("asking 25 at a time") {
		t.Errorf("logs = %q, want each halving", tt.logs)
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
}

func TestRunPacesItsPasses(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail []error
		want []time.Duration
	}{
		{"backfilling, then complete", nil, []time.Duration{backfillPace, syncInterval, syncInterval}},
		{"after an error", []error{errors.New("connection reset by peer")}, []time.Duration{errorPause, backfillPace, syncInterval}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newFakeChain(150)
			c.fail = tc.fail
			tt := newTestTracker(t, c)
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

func storedRounds(t *testing.T, s *Store, height int64) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), `SELECT proposer FROM blocks_rounds WHERE height = ? ORDER BY round`, height)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func storedFlag(t *testing.T, s *Store, height int64) string {
	t.Helper()
	var reason string
	err := s.db.QueryRowContext(context.Background(), `SELECT reason FROM blocks_flags WHERE height = ?`, height).Scan(&reason)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	return reason
}

func storedEvents(t *testing.T, s *Store, height int64) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(),
		`SELECT type, address, power, source_height FROM blocks_events WHERE height = ? ORDER BY idx`, height)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var typ, addr string
		var power, source int64
		if err := rows.Scan(&typ, &addr, &power, &source); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%s %s %d from %d", typ, addr, power, source))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

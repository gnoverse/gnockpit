package blocks

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gnoverse/gnockpit/node"
)

const (
	// timeoutCut is how long a batch size halved by a timeout holds: a slow
	// answer can be transient, unlike a refusal of the batch's size.
	timeoutCut = 10 * time.Minute
	// syncInterval is the wait between passes once the history is complete.
	syncInterval = 5 * time.Second
	// errorPause is the wait after a failed pass.
	errorPause = 30 * time.Second
	// rateLimitPause is the wait after a 429 that carries no Retry-After.
	rateLimitPause = time.Minute
	// checkEvery spaces the blocks finalized at round 0 whose proposer is
	// checked against the proposer-priority port.
	checkEvery = 1000
	// floorMargin is how far below the head a refused height must be to mark
	// the oldest block the source keeps; nearer, the refusal comes from a node
	// behind the one that gave the head (a load balancer's lagging backend),
	// and may pass on a retry.
	floorMargin = 1000
	// rateWindow is the span Progress.Rate is measured over.
	rateWindow = 5 * time.Minute
	// behindReport is how long the source can fail to give the heights asked
	// final before it is reported: a load balancer's lagging backend is
	// routine, minutes of it are not.
	behindReport = 2 * time.Minute
	// gapRetry spaces the retries of the validator set changes whose events
	// could not be read.
	gapRetry = 10 * time.Minute
	// setsRealm prefixes the realms whose events change the validator set.
	setsRealm = "gno.land/r/sys/validators/"
)

// Source reads the chain: its head, commits, validator sets and block events.
type Source interface {
	ChainHead(ctx context.Context) (node.ChainHead, error)
	Commits(ctx context.Context, heights []int64) ([]node.Commit, error)
	ValidatorSet(ctx context.Context, height int64) ([]node.SetMember, error)
	BlockEvents(ctx context.Context, height int64) ([]node.BlockEvent, error)
}

var _ Source = (*node.Client)(nil)

// errNotYet ends a fill at a height the source cannot give final yet: a node
// behind the one that gave the head (a load balancer's lagging backend)
// refuses it, or gives its own commit for its latest height rather than the
// next block's. A later pass asks again.
var errNotYet = errors.New("not final at the source yet")

// direction is the way a fill runs from the stored range.
type direction int

const (
	upward   direction = iota // toward the head
	downward                  // toward genesis
)

// Config tunes the fill's load on the source.
type Config struct {
	Pace  time.Duration // wait between batches while heights are left below or the head is far; 0 for none
	Batch int           // commits asked in one JSON-RPC batch, at most
}

const (
	// DefaultPace is the default Config.Pace: about a batch a second.
	DefaultPace = time.Second
	// DefaultBatch is the default Config.Batch.
	DefaultBatch = 100
)

// DefaultConfig returns the default Config.
func DefaultConfig() Config { return Config{Pace: DefaultPace, Batch: DefaultBatch} }

// Tracker fills the block history from a Source: the heights up to the chain
// head first, then the ones below the stored range, newest first.
type Tracker struct {
	store  *Store
	source Source
	cfg    Config

	sleep func(context.Context, time.Duration) error
	logf  func(format string, args ...any)
	now   func() time.Time

	// Owned by the goroutine running passes.
	batch        int       // commits per batch
	maxBatch     int       // the size the source refused half of; batch grows back to it
	regrowAt     time.Time // when batch, cut by a timeout, grows back; zero: not cut
	marks        []lowMark // the lowest stored height at each publish of the last rateWindow
	floorChecked bool      // the recorded floor was checked against the source since start
	floorSeen    int64     // a floor one answer showed, set when a second shows it; 0: none
	behindSince  time.Time // since when passes have met heights not final at the source; zero: none
	belowAt      time.Time // when the backfill asks again a source refusing the lowest stored height; zero: not paused
	setRefused   int64     // a height whose set one read refused, taken as unserved when a second does; 0: none
	gapsAt       time.Time // when the event gaps are read again
	lastErr      map[string]string

	mu       sync.RWMutex
	progress Progress
}

// NewTracker returns a tracker reading source into store. Run starts it.
func NewTracker(store *Store, source Source, cfg Config) (*Tracker, error) {
	if cfg.Batch < 1 {
		return nil, fmt.Errorf("batch of %d commits: want at least 1", cfg.Batch)
	}
	if cfg.Pace < 0 {
		return nil, fmt.Errorf("pace of %s: want 0 or more", cfg.Pace)
	}
	return &Tracker{
		store:    store,
		source:   source,
		cfg:      cfg,
		sleep:    sleepCtx,
		logf:     log.Printf,
		now:      time.Now,
		batch:    cfg.Batch,
		maxBatch: cfg.Batch,
		lastErr:  map[string]string{},
	}, nil
}

// Progress returns how far the stored history reaches.
func (t *Tracker) Progress() Progress {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.progress
}

// Run fills the history until ctx is done: a pass every Config.Pace while
// heights are left below, every syncInterval once complete, errorPause after a
// failed one. A pass catching up with a far head waits Config.Pace between its
// batches too.
func (t *Tracker) Run(ctx context.Context) {
	t.report("load", t.publish(ctx))
	for {
		more, err := t.pass(ctx)
		if ctx.Err() != nil {
			return
		}
		t.report("fill", err)
		wait := syncInterval
		switch {
		case err != nil:
			wait = errorPause
		case more:
			wait = t.cfg.Pace
		}
		if t.sleep(ctx, wait) != nil {
			return
		}
	}
}

// pass stores the heights between the stored range and the chain head, then
// one batch below the range. more reports heights left below.
func (t *Tracker) pass(ctx context.Context) (more bool, err error) {
	var behind bool // the source did not give a height asked final yet
	defer func() {
		if perr := t.publish(ctx); perr != nil && err == nil {
			err = perr
		}
		if err == nil {
			t.watchBehind(behind)
		}
	}()
	head, err := t.chainHead(ctx)
	if err != nil {
		return false, fmt.Errorf("chain head: %w", err)
	}
	if err := t.followChain(ctx, head.ChainID); err != nil {
		return false, err
	}
	t.retryEventGaps(ctx)
	if !t.regrowAt.IsZero() && !t.now().Before(t.regrowAt) {
		t.batch, t.regrowAt = t.maxBatch, time.Time{}
	}
	// The head's own commit is final only once the next block carries it.
	top := head.Height - 1
	if top < 1 {
		return false, nil
	}
	low, high, err := t.store.Range(ctx)
	if err != nil {
		return false, err
	}
	if high == 0 {
		low, high = top+1, top
	}
	for high < top {
		n, err := t.fill(ctx, heightsUp(high+1, min(high+int64(t.batch), top)), head, upward, 0)
		high += int64(n)
		if errors.Is(err, errNotYet) {
			behind = true
			break // the source has not got further yet
		}
		if err != nil {
			return false, err
		}
		if high < top && n > 0 {
			// Catching up after downtime takes several batches: they go at
			// the backfill's pace, the progress showing as they do.
			if err := t.publish(ctx); err != nil {
				return false, err
			}
			if err := t.sleep(ctx, t.cfg.Pace); err != nil {
				return false, err
			}
		}
	}
	if !t.floorChecked {
		t.floorChecked = t.recheckFloor(ctx)
	}
	floor, err := t.store.Floor(ctx)
	if err != nil {
		return false, err
	}
	if low <= max(1, floor) {
		return false, nil
	}
	if t.now().Before(t.belowAt) {
		return false, nil // the source refuses the lowest stored height: passes go at the sync interval
	}
	// The lowest stored height opens the batch as a witness, when there is
	// one and the batch has room for it: a height refused right after a
	// height served, in one answer, is below the oldest block the source keeps.
	var witness int64
	size := int64(t.batch) // calls per batch, the witness among them
	if low <= high && size >= 2 {
		witness, size = low, size-1
	}
	n, err := t.fill(ctx, heightsDown(low-1, max(1, floor, low-size)), head, downward, witness)
	notYet := errors.Is(err, errNotYet)
	behind = behind || notYet
	if err != nil && !notYet {
		return false, err
	}
	if floor, err = t.store.Floor(ctx); err != nil {
		return false, err
	}
	return low-int64(n) > max(1, floor), nil
}

// fill stores the commits of heights, which run outward from the stored
// range in dir, asked after witness, a stored height, when it is not 0. It
// returns how many it stored: the first ones, up to a height the source
// refused or whose commit is not final yet (errNotYet when the answer does
// not show the source's oldest block). A batch refused whole halves the batch
// size until restart; one too slow to answer, for timeoutCut. Either stores
// nothing.
func (t *Tracker) fill(ctx context.Context, heights []int64, head node.ChainHead, dir direction, witness int64) (int, error) {
	asked := heights
	if witness > 0 {
		asked = append([]int64{witness}, heights...)
	}
	commits, cerr := t.commits(ctx, asked)
	var he *node.HeightError
	if cerr != nil && !errors.As(cerr, &he) {
		if (node.IsRefusal(cerr) || isTimeout(cerr)) && len(asked) > 1 {
			before := t.batch
			if node.IsRefusal(cerr) {
				// The source's limit is below this batch's calls.
				t.batch = max(1, len(asked)/2)
				t.maxBatch = t.batch
			} else {
				// Slow, whatever the batch: the size halves, not this batch.
				t.batch = max(1, t.batch/2)
				t.regrowAt = t.now().Add(timeoutCut)
			}
			if t.batch != before {
				t.logf("blocks: a batch of %d commits failed (%v), asking %d at a time", len(asked), cerr, t.batch)
			}
			if isTimeout(cerr) && t.batch >= len(asked) {
				return 0, cerr // asked again now, the same calls would time out the same way
			}
			return 0, nil
		}
		return 0, cerr
	}
	// The witness is stored already: it only shows what the source served
	// before a refusal.
	served := len(commits)
	if witness > 0 && served > 0 {
		commits = commits[1:]
		if !t.belowAt.IsZero() {
			t.belowAt = time.Time{}
			t.report("below", nil)
		}
	}
	// A commit the node does not mark canonical is its own commit for its
	// latest height (a backend behind the one that gave the head); the next
	// block's commit, which can carry late precommits, is the one to store.
	for i, c := range commits {
		if !c.Canonical {
			commits, cerr = commits[:i], fmt.Errorf("height %d: %w", c.Height, errNotYet)
			break
		}
	}
	recs := make([]Record, 0, len(commits))
	for _, c := range commits {
		r, err := t.record(ctx, c, head.Height)
		if err != nil {
			return 0, err
		}
		recs = append(recs, r)
	}
	if err := t.store.Write(ctx, recs); err != nil {
		return 0, err
	}
	t.events(ctx, recs)
	if cerr == nil || errors.Is(cerr, errNotYet) {
		return len(recs), cerr
	}
	if he != nil && node.IsRefusal(cerr) {
		// Near the head, a backend behind the one that gave the head refuses
		// it: a later pass asks again.
		if head.Height-he.Height <= floorMargin {
			return len(recs), fmt.Errorf("height %d: %w", he.Height, errNotYet)
		}
		if dir == downward {
			return len(recs), t.refusedBelow(ctx, he.Height, served, witness)
		}
	}
	return len(recs), cerr
}

// refusedBelow handles the backfill's refusal of height, well below the head,
// served heights after witness (0: none) in the answer. Refused right after a
// served height, the one above may be the source's oldest block. Refused as
// the witness, the lowest stored height is no longer served: the backfill
// pauses for gapRetry. Refused first with no witness, the height above is asked
// alone for the same proof.
func (t *Tracker) refusedBelow(ctx context.Context, height int64, served int, witness int64) error {
	switch {
	case served > 0:
		return t.sawFloor(ctx, height+1)
	case witness > 0:
		t.belowAt = t.now().Add(gapRetry)
		t.report("below", fmt.Errorf("the first -rpc refuses #%d, the lowest stored height; the backfill asks again every %s", witness, gapRetry))
		return nil
	}
	ok, err := t.serves(ctx, height+1)
	if err != nil || !ok {
		return fmt.Errorf("height %d: %w", height, errNotYet)
	}
	return t.sawFloor(ctx, height+1)
}

// watchBehind reports the source once it has failed, for behindReport, to
// give final the heights passes ask, and when it gives them again.
func (t *Tracker) watchBehind(behind bool) {
	if !behind {
		t.behindSince = time.Time{}
		t.report("behind", nil)
		return
	}
	now := t.now()
	if t.behindSince.IsZero() {
		t.behindSince = now
	}
	if now.Sub(t.behindSince) >= behindReport {
		t.report("behind", fmt.Errorf("the first -rpc has not given the heights asked final for %s", behindReport))
	}
}

// sawFloor records an answer that served floor and refused the height below
// it. The floor is set when a second answer shows it: one refusal can be a
// backend's fluke.
func (t *Tracker) sawFloor(ctx context.Context, floor int64) error {
	if t.floorSeen != floor {
		t.floorSeen = floor
		return fmt.Errorf("height %d: %w", floor-1, errNotYet)
	}
	t.floorSeen = 0
	if err := t.store.SetFloor(ctx, floor); err != nil {
		return err
	}
	t.logf("blocks: the source serves no block below #%d", floor)
	return nil
}

// recheckFloor asks the source, once per start, for the height below the
// recorded floor. Served, the floor is cleared for the backfill to go on (a
// source changed for an archive node); refused, the floor stands. It reports
// whether the check concluded: a failed read is reported, and the next pass
// asks again, without holding the fill.
func (t *Tracker) recheckFloor(ctx context.Context) bool {
	floor, err := t.store.Floor(ctx)
	if err != nil {
		t.report("floor", err)
		return false
	}
	if floor <= 1 {
		return true
	}
	ok, err := t.serves(ctx, floor-1)
	if err != nil {
		t.report("floor", fmt.Errorf("check of the floor #%d: %w", floor, err))
		return false
	}
	if ok {
		if err := t.store.ClearFloor(ctx); err != nil {
			t.report("floor", err)
			return false
		}
		t.logf("blocks: the source serves blocks below #%d, filling down to genesis", floor)
	}
	t.report("floor", nil)
	return true
}

// serves reports whether the source gives the commit of height alone: false
// when it refuses it, an error when the read fails.
func (t *Tracker) serves(ctx context.Context, height int64) (bool, error) {
	_, err := t.commits(ctx, []int64{height})
	var he *node.HeightError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &he) && node.IsRefusal(err):
		return false, nil
	}
	return false, err
}

// followChain drops the stored history when it belongs to another chain.
func (t *Tracker) followChain(ctx context.Context, chainID string) error {
	stored, err := t.store.ChainID(ctx)
	if err != nil || stored == chainID {
		return err
	}
	if stored != "" {
		t.logf("blocks: chain is %s, dropping the block history of %s", chainID, stored)
	}
	t.batch, t.maxBatch, t.regrowAt, t.marks = t.cfg.Batch, t.cfg.Batch, time.Time{}, nil
	t.floorSeen, t.belowAt, t.setRefused = 0, time.Time{}, 0
	return t.store.Reset(ctx, chainID)
}

// report logs err when it differs from the last one logged for kind, and the
// recovery once it clears.
func (t *Tracker) report(kind string, err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if msg == t.lastErr[kind] {
		return
	}
	t.lastErr[kind] = msg
	if msg == "" {
		t.logf("blocks: %s ok again", kind)
		return
	}
	t.logf("blocks: %s: %s", kind, msg)
}

// isTimeout reports whether err is a request that ran out of time.
func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func heightsUp(from, to int64) []int64 {
	out := make([]int64, 0, to-from+1)
	for h := from; h <= to; h++ {
		out = append(out, h)
	}
	return out
}

func heightsDown(from, to int64) []int64 {
	out := make([]int64, 0, from-to+1)
	for h := from; h >= to; h-- {
		out = append(out, h)
	}
	return out
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

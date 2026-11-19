package blocks

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
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

// Progress is how far the stored history reaches.
type Progress struct {
	ChainID  string
	From, To int64     // lowest and highest stored heights; 0 when none is
	FromTime time.Time // time of block From
	Floor    int64     // lowest height the source serves, when it refused a lower one
	Complete bool      // From is 1, or the floor
	Rate     float64   // heights stored below From per second over the last rateWindow; 0 when unknown or complete
}

// Config tunes the fill's load on the source.
type Config struct {
	Pace  time.Duration // wait between passes while heights are left below; 0 for none
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
// failed one.
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
	// The lowest stored height opens the batch as a witness, when there is
	// one: a height refused right after a height served, in one answer, is
	// below the oldest block the source keeps.
	var witness int64
	size := int64(t.batch) // calls per batch, the witness among them
	if low <= high {
		witness, size = low, max(1, size-1)
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
// size for good; one too slow to answer, for timeoutCut. Either stores
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
			if node.IsRefusal(cerr) {
				// The source's limit is below this batch's calls.
				t.batch = max(1, len(asked)/2)
				t.maxBatch = t.batch
			} else {
				// Slow, whatever the batch: the size halves, not this batch.
				t.batch = max(1, t.batch/2)
				t.regrowAt = t.now().Add(timeoutCut)
			}
			t.logf("blocks: a batch of %d commits failed (%v), asking %d at a time", len(asked), cerr, t.batch)
			return 0, nil
		}
		return 0, cerr
	}
	// The witness is stored already: it only shows what the source served
	// before a refusal.
	served := len(commits)
	if witness > 0 && served > 0 {
		commits = commits[1:]
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
		r, err := t.record(ctx, c)
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
		// it; refused first in the answer, it shows nothing the source keeps.
		// Either way a later pass asks again.
		if head.Height-he.Height <= floorMargin || served == 0 {
			return len(recs), fmt.Errorf("height %d: %w", he.Height, errNotYet)
		}
		// The backfill met a height refused right below one served, well
		// below the head: the one above may be the oldest the source keeps.
		if dir == downward {
			return len(recs), t.sawFloor(ctx, he.Height+1)
		}
	}
	return len(recs), cerr
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

// limited calls read until it answers anything but HTTP 429, waiting the
// answer's Retry-After (rateLimitPause without one) in between.
func (t *Tracker) limited(ctx context.Context, read func() error) error {
	for {
		err := read()
		var hs *node.HTTPStatusError
		if !errors.As(err, &hs) || hs.StatusCode != http.StatusTooManyRequests {
			return err
		}
		wait := cmp.Or(hs.RetryAfter, rateLimitPause)
		t.report("fill", fmt.Errorf("RPC rate limited, pausing for %s", wait))
		if err := t.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func (t *Tracker) chainHead(ctx context.Context) (head node.ChainHead, err error) {
	err = t.limited(ctx, func() error {
		head, err = t.source.ChainHead(ctx)
		return err
	})
	return head, err
}

func (t *Tracker) commits(ctx context.Context, heights []int64) (got []node.Commit, err error) {
	err = t.limited(ctx, func() error {
		got, err = t.source.Commits(ctx, heights)
		return err
	})
	return got, err
}

func (t *Tracker) validatorSet(ctx context.Context, height int64) (set []node.SetMember, err error) {
	err = t.limited(ctx, func() error {
		set, err = t.source.ValidatorSet(ctx, height)
		return err
	})
	return set, err
}

func (t *Tracker) blockEvents(ctx context.Context, height int64) (evs []node.BlockEvent, err error) {
	err = t.limited(ctx, func() error {
		evs, err = t.source.BlockEvents(ctx, height)
		return err
	})
	return evs, err
}

// record builds the record of a commit against the validator set it names,
// with the expected proposer of each round when it was finalized after round
// 0. Such a block, and every checkEvery-th one, has its proposer checked and is
// flagged when no round expects it.
func (t *Tracker) record(ctx context.Context, c node.Commit) (Record, error) {
	set, err := t.members(ctx, c)
	if err != nil {
		return Record{}, err
	}
	r, err := newRecord(c, set)
	if err != nil {
		return Record{}, err
	}
	if r.Round == 0 && r.Height%checkEvery != 0 {
		return r, nil
	}
	if err := t.rounds(ctx, &r); err != nil {
		return Record{}, err
	}
	return r, nil
}

// members returns the validator set a commit names, from the store, or read
// once from the source at the commit's height and stored.
func (t *Tracker) members(ctx context.Context, c node.Commit) ([]Member, error) {
	set, ok, err := t.store.Valset(ctx, c.ValidatorsHash)
	if err != nil || ok {
		return set, err
	}
	got, err := t.validatorSet(ctx, c.Height)
	if err != nil {
		return nil, fmt.Errorf("validator set at %d: %w", c.Height, err)
	}
	if len(got) == 0 {
		return nil, fmt.Errorf("validator set at %d: empty answer", c.Height)
	}
	set = make([]Member, len(got))
	for i, m := range got {
		set[i] = Member{Address: m.Address, PubKey: m.PubKey, Power: m.Power}
	}
	if err := t.store.PutValset(ctx, c.ValidatorsHash, set); err != nil {
		return nil, err
	}
	return set, nil
}

// rounds fills r's expected proposer of each round, from the validator sets
// at the height below and at the height, and flags r when round 0's proposer
// is unknown or no round's expected proposer is r's proposer.
func (t *Tracker) rounds(ctx context.Context, r *Record) error {
	cur, err := t.validatorSet(ctx, r.Height)
	if err != nil {
		return fmt.Errorf("validator set at %d: %w", r.Height, err)
	}
	var prev []node.SetMember
	if r.Height > 1 {
		if prev, err = t.validatorSet(ctx, r.Height-1); err != nil {
			return fmt.Errorf("validator set at %d: %w", r.Height-1, err)
		}
	}
	proposers, err := roundProposers(prev, cur, r.Round)
	if err != nil {
		return fmt.Errorf("height %d: %w", r.Height, err)
	}
	if r.Round > 0 {
		r.Rounds = proposers
	}
	switch {
	case proposers[0] == "":
		r.Flag = "round 0's proposer unknown: one increment of the set below does not give this one"
	case !slices.Contains(proposers, r.Proposer):
		r.Flag = "no round's expected proposer is the header's " + r.Proposer
	}
	if r.Flag != "" {
		t.logf("blocks: height %d: %s", r.Height, r.Flag)
	}
	return nil
}

// events reads the events behind each validator set change met in recs, or
// at the stored height just above them, from the block two below the change,
// where tm2 took the updates from. A failed read, of the events or of a stored
// height's set, is logged and recorded as an event gap: the heights stay
// stored, and a later pass reads it again.
func (t *Tracker) events(ctx context.Context, recs []Record) {
	if len(recs) == 0 {
		return
	}
	hashes := make(map[int64]string, len(recs))
	var top int64
	candidates := make([]int64, 0, len(recs)+1)
	for _, r := range recs {
		hashes[r.Height] = r.ValsetHash
		top = max(top, r.Height)
		candidates = append(candidates, r.Height)
	}
	candidates = append(candidates, top+1)
	hashAt := func(h int64) (string, bool, error) {
		if hash, ok := hashes[h]; ok {
			return hash, true, nil
		}
		return t.store.ValsetHashAt(ctx, h)
	}
	for _, h := range candidates {
		if h <= 2 {
			continue
		}
		change, err := t.isSetChange(h, hashAt)
		if err != nil {
			t.report("events", err)
			t.addEventGap(ctx, h)
			continue
		}
		if change {
			t.readEvents(ctx, h)
		}
	}
}

// isSetChange reports whether the stored height opens a new validator set,
// reading the sets' hashes with hashAt; false when either height is not
// stored.
func (t *Tracker) isSetChange(h int64, hashAt func(int64) (string, bool, error)) (bool, error) {
	cur, ok, err := hashAt(h)
	if err != nil || !ok {
		return false, err
	}
	below, ok, err := hashAt(h - 1)
	if err != nil || !ok {
		return false, err
	}
	return below != cur, nil
}

func (t *Tracker) addEventGap(ctx context.Context, height int64) {
	if err := t.store.AddEventGap(ctx, height); err != nil {
		t.report("events", err)
	}
}

// readEvents stores the validator set events behind the change at height,
// read from the block two below it, or records the change as an event gap.
func (t *Tracker) readEvents(ctx context.Context, height int64) {
	source := height - 2
	got, err := t.blockEvents(ctx, source)
	var evs []Event
	if err == nil {
		evs, err = setEvents(got)
	}
	if err != nil {
		t.report("events", fmt.Errorf("events of %d: %w", source, err))
		t.addEventGap(ctx, height)
		return
	}
	if len(evs) == 0 {
		t.logf("blocks: no validator set event at #%d for the change at #%d", source, height)
	}
	if err := t.store.PutEvents(ctx, height, source, evs); err != nil {
		t.report("events", err)
		return
	}
	t.report("events", nil)
}

// retryEventGaps reads again, every gapRetry, the events of the validator
// set changes whose read failed. A gap whose events are below the oldest block
// the source serves, or whose height turns out to open no new set, is dropped.
func (t *Tracker) retryEventGaps(ctx context.Context) {
	now := t.now()
	if now.Before(t.gapsAt) {
		return
	}
	t.gapsAt = now.Add(gapRetry)
	gaps, err := t.store.EventGaps(ctx)
	if err != nil {
		t.report("events", err)
		return
	}
	floor, err := t.store.Floor(ctx)
	if err != nil {
		t.report("events", err)
		return
	}
	stored := func(h int64) (string, bool, error) { return t.store.ValsetHashAt(ctx, h) }
	for _, h := range gaps {
		if h-2 < floor {
			t.logf("blocks: the events of the change at #%d are at #%d, below the oldest block the source serves", h, h-2)
			t.dropEventGap(ctx, h)
			continue
		}
		change, err := t.isSetChange(h, stored)
		switch {
		case err != nil:
			t.report("events", err)
		case change:
			t.readEvents(ctx, h)
		default:
			t.dropEventGap(ctx, h)
		}
	}
}

func (t *Tracker) dropEventGap(ctx context.Context, height int64) {
	if err := t.store.DropEventGap(ctx, height); err != nil {
		t.report("events", err)
	}
}

// recheckFloor asks the source, once per start, for the recorded floor and
// the height below it, in one answer. Below served, the floor is cleared for
// the backfill to go on (a source changed for an archive node); below refused
// right after the floor served, it stands. It reports whether the check
// concluded: any other answer is reported, and the next pass asks again,
// without holding the fill.
func (t *Tracker) recheckFloor(ctx context.Context) bool {
	floor, err := t.store.Floor(ctx)
	if err != nil {
		t.report("floor", err)
		return false
	}
	if floor <= 1 {
		return true
	}
	_, err = t.commits(ctx, []int64{floor, floor - 1})
	var he *node.HeightError
	switch {
	case err == nil:
		if err := t.store.ClearFloor(ctx); err != nil {
			t.report("floor", err)
			return false
		}
		t.logf("blocks: the source serves blocks below #%d, filling down to genesis", floor)
	case errors.As(err, &he) && he.Height == floor-1 && node.IsRefusal(err):
		// Still the oldest block the source keeps.
	default:
		t.report("floor", fmt.Errorf("check of the floor #%d: %w", floor, err))
		return false
	}
	t.report("floor", nil)
	return true
}

// setEvents keeps a block's validator set events: ValidatorAdded,
// ValidatorRemoved and ValoperRotated, from a realm under setsRealm. Any
// realm can emit an event of those names; only the system's change the set.
func setEvents(evs []node.BlockEvent) ([]Event, error) {
	var out []Event
	for _, e := range evs {
		if !strings.HasPrefix(e.PkgPath, setsRealm) {
			continue
		}
		var power int64
		if p, ok := e.Attrs["power"]; ok {
			var err error
			if power, err = strconv.ParseInt(p, 10, 64); err != nil {
				return nil, fmt.Errorf("%s power %q: %w", e.Type, p, err)
			}
		}
		switch e.Type {
		case "ValidatorAdded", "ValidatorRemoved":
			out = append(out, Event{Type: e.Type, Operator: e.Attrs["op"], Address: e.Attrs["signingAddr"], Power: power, Applied: true})
		case "ValoperRotated":
			out = append(out, Event{
				Type: e.Type, Operator: e.Attrs["op"], OldAddress: e.Attrs["oldAddr"], NewAddress: e.Attrs["newAddr"],
				Applied: e.Attrs["applied"] == "true",
			})
		}
	}
	return out, nil
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
	t.batch, t.maxBatch, t.regrowAt, t.marks, t.floorSeen = t.cfg.Batch, t.cfg.Batch, time.Time{}, nil, 0
	return t.store.Reset(ctx, chainID)
}

// publish refreshes Progress from the store.
func (t *Tracker) publish(ctx context.Context) error {
	chainID, err := t.store.ChainID(ctx)
	if err != nil {
		return err
	}
	low, high, err := t.store.Range(ctx)
	if err != nil {
		return err
	}
	floor, err := t.store.Floor(ctx)
	if err != nil {
		return err
	}
	var fromTime time.Time
	if low > 0 {
		if fromTime, _, err = t.store.TimeAt(ctx, low); err != nil {
			return err
		}
	}
	p := Progress{
		ChainID: chainID, From: low, To: high, FromTime: fromTime, Floor: floor,
		Complete: low > 0 && low <= max(1, floor), Rate: t.measure(low),
	}
	if p.Complete {
		p.Rate = 0
	}
	t.mu.Lock()
	was := t.progress.Complete
	t.progress = p
	t.mu.Unlock()
	if p.Complete && !was {
		t.logf("blocks: history complete from #%d (%s)", low, fromTime.Format(time.RFC3339))
	}
	return nil
}

// lowMark is the lowest stored height at a time.
type lowMark struct {
	at  time.Time
	low int64
}

// measure records low, the lowest stored height, and returns how fast it fell
// over the last rateWindow, in heights per second: 0 until two records span
// some time. An empty store records nothing: its first height is no progress.
func (t *Tracker) measure(low int64) float64 {
	if low == 0 {
		return 0
	}
	now := t.now()
	t.marks = append(t.marks, lowMark{at: now, low: low})
	for now.Sub(t.marks[0].at) > rateWindow {
		t.marks = t.marks[1:]
	}
	first := t.marks[0]
	span := now.Sub(first.at).Seconds()
	if span <= 0 {
		return 0
	}
	return float64(first.low-low) / span
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

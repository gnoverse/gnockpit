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
	// batchSize is how many commits one JSON-RPC batch asks for.
	batchSize = 100
	// backfillPace is the wait between passes while heights are left below
	// the stored range: about one batch a second.
	backfillPace = time.Second
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

// Progress is how far the stored history reaches.
type Progress struct {
	ChainID  string
	From, To int64     // lowest and highest stored heights; 0 when none is
	FromTime time.Time // time of block From
	Floor    int64     // lowest height the source serves, when it refused a lower one
	Complete bool      // From is 1, or the floor
}

// Tracker fills the block history from a Source: the heights up to the chain
// head first, then the ones below the stored range, newest first.
type Tracker struct {
	store  *Store
	source Source

	sleep func(context.Context, time.Duration) error
	logf  func(format string, args ...any)

	// Owned by the goroutine running passes.
	batch   int
	lastErr map[string]string

	mu       sync.RWMutex
	progress Progress
}

// NewTracker returns a tracker reading source into store. Run starts it.
func NewTracker(store *Store, source Source) *Tracker {
	return &Tracker{
		store:   store,
		source:  source,
		sleep:   sleepCtx,
		logf:    log.Printf,
		batch:   batchSize,
		lastErr: map[string]string{},
	}
}

// Progress returns how far the stored history reaches.
func (t *Tracker) Progress() Progress {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.progress
}

// Run fills the history until ctx is done: a pass every backfillPace while
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
			wait = backfillPace
		}
		if t.sleep(ctx, wait) != nil {
			return
		}
	}
}

// pass stores the heights between the stored range and the chain head, then
// one batch below the range. more reports heights left below.
func (t *Tracker) pass(ctx context.Context) (more bool, err error) {
	defer func() {
		if perr := t.publish(ctx); perr != nil && err == nil {
			err = perr
		}
	}()
	head, err := t.source.ChainHead(ctx)
	if err != nil {
		return false, fmt.Errorf("chain head: %w", err)
	}
	if err := t.followChain(ctx, head.ChainID); err != nil {
		return false, err
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
		n, err := t.fill(ctx, heightsUp(high+1, min(high+int64(t.batch), top)), head, false)
		if err != nil {
			return false, err
		}
		high += int64(n)
	}
	floor, err := t.store.Floor(ctx)
	if err != nil {
		return false, err
	}
	if low <= max(1, floor) {
		return false, nil
	}
	n, err := t.fill(ctx, heightsDown(low-1, max(1, floor, low-int64(t.batch))), head, true)
	if err != nil {
		return false, err
	}
	if floor, err = t.store.Floor(ctx); err != nil {
		return false, err
	}
	return low-int64(n) > max(1, floor), nil
}

// fill stores the commits of heights, which run outward from the stored
// range: up for the head, down for the backfill. It returns how many it
// stored: the first ones, up to a height the source refused. A batch refused
// whole, or too slow to answer, halves the batch size and stores nothing.
func (t *Tracker) fill(ctx context.Context, heights []int64, head node.ChainHead, down bool) (int, error) {
	commits, cerr := t.commits(ctx, heights)
	var he *node.HeightError
	if cerr != nil && !errors.As(cerr, &he) {
		if (node.IsRefusal(cerr) || isTimeout(cerr)) && len(heights) > 1 {
			t.batch = max(1, len(heights)/2)
			t.logf("blocks: a batch of %d commits failed (%v), asking %d at a time", len(heights), cerr, t.batch)
			return 0, nil
		}
		return 0, cerr
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
	if cerr == nil {
		return len(recs), nil
	}
	// The backfill met a height the source refuses, well below the head: the
	// one above it is the oldest the source keeps.
	if down && node.IsRefusal(cerr) && head.Height-he.Height > floorMargin {
		if err := t.store.SetFloor(ctx, he.Height+1); err != nil {
			return len(recs), err
		}
		t.logf("blocks: the source serves no block below #%d", he.Height+1)
		return len(recs), nil
	}
	return len(recs), cerr
}

// commits reads the commits of heights, waiting out rate limits.
func (t *Tracker) commits(ctx context.Context, heights []int64) ([]node.Commit, error) {
	for {
		got, err := t.source.Commits(ctx, heights)
		var hs *node.HTTPStatusError
		if !errors.As(err, &hs) || hs.StatusCode != http.StatusTooManyRequests {
			return got, err
		}
		wait := cmp.Or(hs.RetryAfter, rateLimitPause)
		t.report("fill", fmt.Errorf("RPC rate limited, pausing for %s", wait))
		if err := t.sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

// record builds the record of a commit against the validator set it names,
// with the expected proposer of each round when it was finalized after round
// 0, or when its height is a checked one.
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
	got, err := t.source.ValidatorSet(ctx, c.Height)
	if err != nil {
		return nil, fmt.Errorf("validator set at %d: %w", c.Height, err)
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
	cur, err := t.source.ValidatorSet(ctx, r.Height)
	if err != nil {
		return fmt.Errorf("validator set at %d: %w", r.Height, err)
	}
	var prev []node.SetMember
	if r.Height > 1 {
		if prev, err = t.source.ValidatorSet(ctx, r.Height-1); err != nil {
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
// where tm2 took the updates from. A failure is logged: the heights stay
// stored, without those events.
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
	hashAt := func(h int64) (string, bool) {
		if hash, ok := hashes[h]; ok {
			return hash, true
		}
		hash, ok, err := t.store.ValsetHashAt(ctx, h)
		if err != nil {
			t.report("events", err)
			return "", false
		}
		return hash, ok
	}
	for _, h := range candidates {
		if h <= 2 {
			continue
		}
		cur, ok := hashAt(h)
		if !ok {
			continue
		}
		below, ok := hashAt(h - 1)
		if !ok || below == cur {
			continue
		}
		t.readEvents(ctx, h)
	}
}

// readEvents stores the validator set events behind the change at height,
// read from the block two below it.
func (t *Tracker) readEvents(ctx context.Context, height int64) {
	source := height - 2
	got, err := t.source.BlockEvents(ctx, source)
	if err != nil {
		t.report("events", fmt.Errorf("events of %d: %w", source, err))
		return
	}
	evs, err := setEvents(got)
	if err != nil {
		t.report("events", fmt.Errorf("events of %d: %w", source, err))
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
	t.batch = batchSize
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
	p := Progress{ChainID: chainID, From: low, To: high, FromTime: fromTime, Floor: floor, Complete: low > 0 && low <= max(1, floor)}
	t.mu.Lock()
	was := t.progress.Complete
	t.progress = p
	t.mu.Unlock()
	if p.Complete && !was {
		t.logf("blocks: history complete from #%d (%s)", low, fromTime.Format(time.RFC3339))
	}
	return nil
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

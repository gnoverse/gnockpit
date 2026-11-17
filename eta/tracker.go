package eta

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gnoverse/gnockpit/node"
)

const (
	// fetchSpacing paces the backfill: about four requests a second, gentle
	// on a public RPC.
	fetchSpacing = 250 * time.Millisecond
	// passFetches caps the heights one pass fetches (about a minute of
	// requests), so a long backfill re-reads the head and the halt that often.
	passFetches = 240
	// rebuildEvery is how many new samples refresh the model mid-pass.
	rebuildEvery = 100
	// syncInterval is the wait between passes once the history is complete.
	syncInterval = time.Minute
	// errorPause is the wait after a failed pass.
	errorPause = 30 * time.Second
	// rateLimitPause is the wait after a 429 that carries no Retry-After.
	rateLimitPause = time.Minute
)

// Source reads the chain: its head, the time of a block, and the halt params.
type Source interface {
	ChainHead(ctx context.Context) (node.ChainHead, error)
	BlockTime(ctx context.Context, height int64) (time.Time, error)
	HaltParams(ctx context.Context) (node.Halt, error)
}

var _ Source = (*node.Client)(nil)

// ScheduledHalt is a halt set on chain above the chain's height, with the
// time gnockpit first saw it.
type ScheduledHalt struct {
	Height     int64
	MinVersion string
	FirstSeen  time.Time
}

// View is what the tracker knows at one moment. Complete reports that the
// history reaches back to the first grid height, or to the oldest block the
// source serves.
type View struct {
	Model    *Model
	Halt     *ScheduledHalt
	Complete bool
}

// Tracker keeps the block-time history and the scheduled halt up to date from
// a Source, and publishes them as a View.
type Tracker struct {
	store  *Store
	source Source

	sleep   func(context.Context, time.Duration) error
	logf    func(format string, args ...any)
	now     func() time.Time
	perPass int

	// Owned by the goroutine running passes.
	have    map[int64]bool    // heights in the store
	lastErr map[string]string // last error logged, per kind

	mu   sync.RWMutex
	view View
}

// NewTracker returns a tracker reading source into store. Run starts it.
func NewTracker(store *Store, source Source) *Tracker {
	return &Tracker{
		store:   store,
		source:  source,
		sleep:   sleepCtx,
		logf:    log.Printf,
		now:     time.Now,
		perPass: passFetches,
		have:    map[int64]bool{},
		lastErr: map[string]string{},
		view:    View{Model: NewModel(nil)},
	}
}

// View returns the current model, scheduled halt and history coverage.
func (t *Tracker) View() View {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.view
}

// Run loads the stored history, then keeps it and the halt up to date until
// ctx is done: passes run back to back while the backfill has more to fetch,
// then every syncInterval, and errorPause after a failed one.
func (t *Tracker) Run(ctx context.Context) {
	t.report("load", t.reload(ctx))
	for {
		more, err := t.syncPass(ctx)
		if ctx.Err() != nil {
			return
		}
		t.report("sync", err)
		if err == nil && more {
			continue
		}
		wait := syncInterval
		if err != nil {
			wait = errorPause
		}
		if t.sleep(ctx, wait) != nil {
			return
		}
	}
}

// syncPass reads the head, follows a chain change, refreshes the halt, and
// fetches up to perPass missing heights. more reports heights left to fetch.
func (t *Tracker) syncPass(ctx context.Context) (more bool, err error) {
	head, err := t.source.ChainHead(ctx)
	if err != nil {
		return false, fmt.Errorf("chain head: %w", err)
	}
	if err := t.followChain(ctx, head.ChainID); err != nil {
		return false, err
	}
	t.report("halt params", t.refreshHalt(ctx, head))
	return t.backfill(ctx, head)
}

// followChain drops the stored history when it belongs to another chain.
func (t *Tracker) followChain(ctx context.Context, chainID string) error {
	stored, err := t.store.ChainID(ctx)
	if err != nil || stored == chainID {
		return err
	}
	if stored != "" {
		t.logf("eta: chain is %s, dropping the block-time history of %s", chainID, stored)
	}
	if err := t.store.Reset(ctx, chainID); err != nil {
		return err
	}
	t.mu.Lock()
	t.view.Halt, t.view.Complete = nil, false
	t.mu.Unlock()
	return t.reload(ctx)
}

// refreshHalt publishes the halt params when they are above the head.
func (t *Tracker) refreshHalt(ctx context.Context, head node.ChainHead) error {
	h, err := t.source.HaltParams(ctx)
	if err != nil {
		return err
	}
	var scheduled *ScheduledHalt
	if h.Height > head.Height {
		seen, err := t.store.NoteHalt(ctx, h.Height, t.now())
		if err != nil {
			return err
		}
		scheduled = &ScheduledHalt{Height: h.Height, MinVersion: h.MinVersion, FirstSeen: seen}
	}
	t.mu.Lock()
	t.view.Halt = scheduled
	t.mu.Unlock()
	return nil
}

// backfill fetches the missing grid heights from the head down, newest first,
// up to perPass of them. What it fetched is stored whatever ends the pass.
func (t *Tracker) backfill(ctx context.Context, head node.ChainHead) (more bool, err error) {
	floor, err := t.store.Floor(ctx)
	if err != nil {
		return false, err
	}
	var batch []Sample
	defer func() {
		if ferr := t.flush(ctx, batch); ferr != nil && err == nil {
			err = ferr
		}
	}()
	fetched := 0
	for h := head.Height - head.Height%Stride; h >= max(Stride, floor); h -= Stride {
		if t.have[h] {
			continue
		}
		if fetched == t.perPass {
			return true, nil
		}
		if fetched > 0 {
			if err := t.sleep(ctx, fetchSpacing); err != nil {
				return false, err
			}
		}
		bt, ferr := t.blockTime(ctx, h)
		if refused(ferr) {
			if err := t.store.SetFloor(ctx, h+Stride); err != nil {
				return false, err
			}
			break
		}
		if ferr != nil {
			return false, fmt.Errorf("block time %d: %w", h, ferr)
		}
		batch = append(batch, Sample{Height: h, Time: bt})
		fetched++
		if len(batch) == rebuildEvery {
			if err := t.flush(ctx, batch); err != nil {
				return false, err
			}
			batch = nil
		}
	}
	if err := t.flush(ctx, batch); err != nil {
		return false, err
	}
	batch = nil
	t.markComplete()
	return false, nil
}

// blockTime fetches the time of height, waiting out rate limits.
func (t *Tracker) blockTime(ctx context.Context, height int64) (time.Time, error) {
	for {
		bt, err := t.source.BlockTime(ctx, height)
		var hs *node.HTTPStatusError
		if !errors.As(err, &hs) || hs.StatusCode != http.StatusTooManyRequests {
			return bt, err
		}
		wait := cmp.Or(hs.RetryAfter, rateLimitPause)
		t.logf("eta: RPC rate limited, pausing the block-time backfill for %s", wait)
		if err := t.sleep(ctx, wait); err != nil {
			return time.Time{}, err
		}
	}
}

// refused reports whether the node answered that it cannot serve a height, as
// opposed to failing to answer.
func refused(err error) bool {
	var hs *node.HTTPStatusError
	var re *node.RPCError
	return !errors.As(err, &hs) && errors.As(err, &re)
}

// flush stores batch and publishes the model rebuilt with it.
func (t *Tracker) flush(ctx context.Context, batch []Sample) error {
	if len(batch) == 0 {
		return nil
	}
	if err := t.store.AddSamples(ctx, batch); err != nil {
		return err
	}
	return t.reload(ctx)
}

// reload rebuilds the model from the store and publishes it.
func (t *Tracker) reload(ctx context.Context) error {
	samples, err := t.store.Samples(ctx)
	if err != nil {
		return err
	}
	clear(t.have)
	for _, s := range samples {
		t.have[s.Height] = true
	}
	m := NewModel(samples)
	t.mu.Lock()
	t.view.Model = m
	t.mu.Unlock()
	return nil
}

func (t *Tracker) markComplete() {
	t.mu.Lock()
	was := t.view.Complete
	t.view.Complete = true
	first, ok := t.view.Model.First()
	t.mu.Unlock()
	if !was && ok {
		t.logf("eta: block-time history complete from #%d (%s)", first.Height, first.Time.Format(time.RFC3339))
	}
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
		t.logf("eta: %s ok again", kind)
		return
	}
	t.logf("eta: %s: %s", kind, msg)
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

package ranking

import (
	"context"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
)

// refreshEvery spaces Run's passes: the state served follows the fill, and
// today's ranking is published within it of today's first stored block.
const refreshEvery = 30 * time.Second

// Snapshot is what the publisher serves: the ranking's state, and the latest
// day published, nil when none is.
type Snapshot struct {
	State     State
	Published *Published // shared, read only
}

// Publisher publishes the ranking once per UTC day, stores each day
// published in Days, and serves the latest with the ranking's state. A pass
// reads its fields without a lock: change none while Refresh or Run runs.
// OnPublish may be nil.
type Publisher struct {
	Blocks   *blocks.Store
	Fill     interface{ Progress() blocks.Progress } // the block tracker filling Blocks
	Days     *DayStore
	Profiles func() (Profiles, bool) // the valoper profiles; false while one the registry lists is unread
	Names    func(string) string     // a signing key's name, for an operator without a profile
	Exclude  []string                // the operators left out of the order, by address
	Now      func() time.Time
	// OnPublish is told of each day published, with the latest stored before
	// it, nil when none. cur shares its slices (Exclusions, Entries,
	// ProposalsDisabled) with the day served: OnPublish only reads them. It
	// runs inside the pass: it must not call Refresh, which would wait for
	// that pass forever.
	OnPublish func(cur Published, prev *Published)

	logf func(format string, args ...any) // Run's log; nil: log.Printf

	pass sync.Mutex // held through a Refresh: one pass at a time
	// chain is the chain whose days are served, its latest stored read by
	// load; "" while none is read: before the first chain id, and from a
	// chain change until load reads the new chain's. Guarded by pass.
	chain string

	mu   sync.Mutex // guards snap: Snapshot does not wait for a pass
	snap Snapshot
}

// Refresh runs one pass: it serves the ranking's state as of Now, and the
// latest day stored for the fill's chain, read when the chain changes; a day
// of another chain goes first, even when the state cannot be read. It
// publishes the chain's ranking for today, Now's UTC date, unless the day
// served is today's with the same formula and exclusion list. It waits for a
// ready state, the chain id, a stored block of today (the window's last
// height is unknown before it), and the profiles: none of these is a
// failure. It stores the day, serves it, then tells OnPublish, before
// Refresh returns.
func (p *Publisher) Refresh(ctx context.Context) error {
	p.pass.Lock()
	defer p.pass.Unlock()
	now, progress := p.Now(), p.Fill.Progress()
	if progress.ChainID != p.chain {
		p.serve(nil)
		p.chain = ""
	}
	st, err := Readiness(ctx, p.Blocks, progress, now)
	if err != nil {
		return fmt.Errorf("readiness: %w", err)
	}
	p.mu.Lock()
	p.snap.State = st
	p.mu.Unlock()
	if err := p.load(ctx, progress.ChainID); err != nil {
		return err
	}
	if st.Kind != Ready {
		return nil
	}
	return p.publish(ctx, progress.ChainID, now)
}

// load serves chain's latest stored day unless chain's days are served
// already: Refresh serves each day it publishes. The chain "", before the
// tracker reads the chain id, has no day.
func (p *Publisher) load(ctx context.Context, chain string) error {
	if chain == p.chain {
		return nil
	}
	latest, ok, err := p.Days.Latest(ctx, chain)
	if err != nil {
		return err
	}
	var served *Published
	if ok {
		served = &latest
	}
	p.serve(served)
	p.chain = chain
	return nil
}

// publish publishes chain's ranking as of now, unless the day served is
// now's, with the same formula and exclusion list, or the chain id, the
// profiles or today's first block are not there yet.
func (p *Publisher) publish(ctx context.Context, chain string, now time.Time) error {
	if chain == "" {
		return nil
	}
	day := now.UTC().Format(time.DateOnly)
	exclusions := listOf(slices.Compact(slices.Sorted(slices.Values(p.Exclude))))
	if served := p.Snapshot().Published; served != nil &&
		served.Day == day && served.Formula == FormulaVersion && slices.Equal(served.Exclusions, exclusions) {
		return nil
	}
	profiles, read := p.Profiles()
	if !read {
		return nil
	}
	w, err := p.Blocks.ScoringWindow(ctx, now)
	if err != nil {
		return fmt.Errorf("scoring window: %w", err)
	}
	if !w.Complete {
		return nil
	}
	in, err := Gather(ctx, p.Blocks, profiles, p.Names, exclusions, now)
	if err != nil {
		return fmt.Errorf("gather: %w", err)
	}
	// Read before the day is stored: a failure leaves it unpublished, for the
	// next pass to publish and tell OnPublish of.
	prev, hasPrev, err := p.Days.Previous(ctx, chain, day)
	if err != nil {
		return err
	}
	cur := Published{Chain: chain, Day: day, ComputedAt: now.UTC(), Exclusions: exclusions, Result: Rank(in)}
	if err := p.Days.Put(ctx, cur); err != nil {
		return err
	}
	p.serve(&cur)
	if p.OnPublish == nil {
		return nil
	}
	var prevDay *Published
	if hasPrev {
		prevDay = &prev
	}
	p.OnPublish(cur, prevDay)
	return nil
}

// serve serves the day pub, nil for none; no one changes it after.
func (p *Publisher) serve(pub *Published) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snap.Published = pub
}

// Snapshot returns what the publisher serves: filling, with nothing stored,
// until a pass reads the state. It is safe for concurrent use, and does not
// wait for a pass to end.
func (p *Publisher) Snapshot() Snapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	snap := p.snap
	if snap.State.Kind == "" {
		snap.State = State{Kind: Filling}
	}
	return snap
}

// Run refreshes at once and every refreshEvery until ctx ends. It logs a
// failed pass's error once while the passes keep failing with it, and the
// first pass that succeeds after.
func (p *Publisher) Run(ctx context.Context) {
	l := passLog{logf: p.logf}
	if l.logf == nil {
		l.logf = log.Printf
	}
	ticker := time.NewTicker(refreshEvery)
	defer ticker.Stop()
	for {
		err := p.Refresh(ctx)
		if ctx.Err() != nil {
			return
		}
		l.report(err)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// passLog logs Run's passes: a failed pass's error when it differs from the
// last pass's, and the first pass that succeeds after one that failed.
type passLog struct {
	logf func(format string, args ...any)
	last string // the last pass's error, "" when it succeeded
}

func (l *passLog) report(err error) {
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if msg == l.last {
		return
	}
	l.last = msg
	if msg == "" {
		l.logf("ranking: refresh ok again")
		return
	}
	l.logf("ranking: refresh: %s", msg)
}

package ranking

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
)

// ---- Fixtures

// fill is a block fill whose progress is fixed.
type fill blocks.Progress

func (f fill) Progress() blocks.Progress { return blocks.Progress(f) }

// stoppedAt returns a clock stopped at t.
func stoppedAt(t time.Time) func() time.Time { return func() time.Time { return t } }

// publications records the calls of OnPublish.
type publications struct {
	cur  []Published
	prev []*Published
}

func (r *publications) record(cur Published, prev *Published) {
	r.cur = append(r.cur, cur)
	r.prev = append(r.prev, prev)
}

// readyStore returns a block store holding the test chain, on which the
// ranking is ready as of gatherNow and its window complete, and its database.
func readyStore(t *testing.T) (*blocks.Store, *sql.DB) {
	t.Helper()
	s, db := openStore(t)
	writeChain(t, s, map[string][]string{"SET": {"g1keya", "g1keyb"}}, 1, testChain(always("SET")))
	return s, db
}

// newPublisher returns a publisher over the block store s and the days d, as
// of gatherNow, its fill's progress the one s shows on chainA, the profiles
// read and nothing excluded; and the record of its OnPublish calls.
func newPublisher(t *testing.T, s *blocks.Store, d *DayStore) (*Publisher, *publications) {
	t.Helper()
	calls := &publications{}
	progress := progressOf(t, s, 0)
	progress.ChainID = chainA
	return &Publisher{
		Blocks:    s,
		Fill:      fill(progress),
		Days:      d,
		Profiles:  func() (Profiles, bool) { return nil, true },
		Names:     nodeName,
		Now:       stoppedAt(gatherNow),
		OnPublish: calls.record,
	}, calls
}

// followChain sets the chain p's fill reads.
func followChain(p *Publisher, chain string) {
	f := p.Fill.(fill)
	f.ChainID = chain
	p.Fill = f
}

func refresh(t *testing.T, p *Publisher) {
	t.Helper()
	if err := p.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
}

// days returns the days of ps, in order.
func days(ps []Published) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.Day)
	}
	return out
}

// dayOf returns the day p points to, "" for none.
func dayOf(p *Published) string {
	if p == nil {
		return ""
	}
	return p.Day
}

// ---- Refresh

func TestPublisherPublishesOncePerDay(t *testing.T) {
	s, db := readyStore(t)
	d := newDayStore(t, db)
	p, calls := newPublisher(t, s, d)
	refresh(t, p)
	p.Now = stoppedAt(gatherNow.Add(time.Hour))
	refresh(t, p)

	if got := days(calls.cur); !slices.Equal(got, []string{"2026-11-25"}) {
		t.Fatalf("OnPublish's days over two passes on 2026-11-25 = %v, want [2026-11-25]", got)
	}
	if calls.prev[0] != nil {
		t.Errorf("OnPublish's prev = %s, want nil: no day stored before", calls.prev[0].Day)
	}
	cur := calls.cur[0]
	if cur.Chain != chainA || !cur.ComputedAt.Equal(gatherNow) || cur.Formula != FormulaVersion || cur.FirstHeight != windowFirst || cur.LastHeight != windowLast {
		t.Errorf("published = chain %s, computed at %s, formula %d, heights %d to %d; want %s, %s, %d, %d to %d",
			cur.Chain, cur.ComputedAt, cur.Formula, cur.FirstHeight, cur.LastHeight, chainA, gatherNow, FormulaVersion, windowFirst, windowLast)
	}
	for _, addr := range []string{"g1keya", "g1keyb"} {
		if got := entryOf(t, cur.Result, addr).Status; got != Ranked {
			t.Errorf("published %s's status = %s, want %s", addr, got, Ranked)
		}
	}
	snap := p.Snapshot()
	if snap.State.Kind != Ready || snap.Published == nil || !reflect.DeepEqual(*snap.Published, cur) {
		t.Errorf("Snapshot() = state %s, day %s; want ready, the day published", snap.State.Kind, dayOf(snap.Published))
	}
	if got, ok := latest(t, d, chainA); !ok || !reflect.DeepEqual(got, cur) {
		t.Errorf("Latest() = %s (found %v), want the day published", got.Day, ok)
	}
	if got, ok := previous(t, d, chainA, "2026-11-25"); ok {
		t.Errorf("Previous(2026-11-25) = %s, want none: one day published", got.Day)
	}
}

func TestPublisherDatesTheDayInUTC(t *testing.T) {
	s, db := readyStore(t)
	p, calls := newPublisher(t, s, newDayStore(t, db))
	// Noon UTC on 2026-11-25 is 23:00 on 2026-11-24 in UTC-13.
	p.Now = stoppedAt(gatherNow.In(time.FixedZone("UTC-13", -13*60*60)))
	refresh(t, p)
	if len(calls.cur) != 1 {
		t.Fatalf("OnPublish's days = %v, want [2026-11-25]", days(calls.cur))
	}
	cur := calls.cur[0]
	if cur.Day != "2026-11-25" || cur.ComputedAt.Location() != time.UTC || !cur.ComputedAt.Equal(gatherNow) {
		t.Errorf("published = day %s, computed at %s; want 2026-11-25, %s", cur.Day, cur.ComputedAt, gatherNow)
	}
}

func TestPublisherWaitsForTodaysFirstBlock(t *testing.T) {
	s, db := openStore(t)
	sets := map[string][]string{"SET": {"g1keya"}}
	// A block every 6 hours, the newest at 23:59:58 the day before today.
	writeChain(t, s, sets, 1, onSet("SET", spaced(130, 6*time.Hour, today.Add(-2*time.Second))))
	p, calls := newPublisher(t, s, newDayStore(t, db))
	p.Now = stoppedAt(today.Add(-12 * time.Hour))
	refresh(t, p)

	p.Now = stoppedAt(today.Add(30 * time.Second))
	if err := p.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() at 00:00:30, today's first block not stored = %v, want nil: not yet is no failure", err)
	}
	if got := days(calls.cur); !slices.Equal(got, []string{"2026-11-24"}) {
		t.Errorf("OnPublish's days = %v, want [2026-11-24]: nothing for today before its first block", got)
	}
	if snap := p.Snapshot(); snap.State.Kind != Ready || dayOf(snap.Published) != "2026-11-24" {
		t.Errorf("Snapshot() = state %s, day %q; want ready, 2026-11-24 still served", snap.State.Kind, dayOf(snap.Published))
	}

	// Today's first block stored, today's ranking follows.
	writeChain(t, s, sets, 131, onSet("SET", []time.Time{today.Add(time.Second)}))
	refresh(t, p)
	if got := days(calls.cur); !slices.Equal(got, []string{"2026-11-24", "2026-11-25"}) {
		t.Fatalf("OnPublish's days = %v, want [2026-11-24 2026-11-25]", got)
	}
	if got := dayOf(calls.prev[1]); got != "2026-11-24" {
		t.Errorf("OnPublish's prev for 2026-11-25 = %q, want 2026-11-24", got)
	}
	if got := dayOf(p.Snapshot().Published); got != "2026-11-25" {
		t.Errorf("Snapshot().Published = %q, want 2026-11-25", got)
	}
}

func TestPublisherLoadsAMatchingDayAfterRestart(t *testing.T) {
	s, db := readyStore(t)
	// A ranking over another window than the store's: served as stored, not
	// computed again.
	stored := publishedOn(t, "2026-11-25", "g1keya", "g1keyb")
	put(t, newDayStore(t, db), stored)

	p, calls := newPublisher(t, s, newDayStore(t, db))
	p.Exclude = []string{"g1keyb", "g1keya", "g1keyb"} // the same list, unsorted, an entry twice
	refresh(t, p)
	if got := p.Snapshot().Published; got == nil || !reflect.DeepEqual(*got, stored) {
		t.Errorf("Snapshot().Published = %+v, want the stored day %+v", got, stored)
	}
	if len(calls.cur) != 0 {
		t.Errorf("OnPublish's days = %v, want none", days(calls.cur))
	}
	if got, ok := latest(t, p.Days, chainA); !ok || !reflect.DeepEqual(got, stored) {
		t.Errorf("Latest() = %+v (found %v), want the stored day kept", got, ok)
	}
}

func TestPublisherServesTheStoredDayBeforeTheFillRuns(t *testing.T) {
	// A restart: the block store holds chainA's history and today's ranking;
	// the block tracker, built over it, has not run yet when the first pass
	// reads its progress.
	ctx := context.Background()
	s, db := openStore(t)
	if err := s.Reset(ctx, chainA); err != nil {
		t.Fatal(err)
	}
	writeChain(t, s, map[string][]string{"SET": {"g1keya", "g1keyb"}}, 1, testChain(always("SET")))
	stored := publishedOn(t, "2026-11-25")
	put(t, newDayStore(t, db), stored)
	tracker, err := blocks.NewTracker(ctx, s, nil, blocks.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	p, calls := newPublisher(t, s, newDayStore(t, db))
	p.Fill = tracker
	refresh(t, p)
	snap := p.Snapshot()
	if snap.State.Kind != Ready || snap.Published == nil || !reflect.DeepEqual(*snap.Published, stored) {
		t.Errorf("Snapshot() = state %+v, day %q; want ready, the stored day", snap.State, dayOf(snap.Published))
	}
	if len(calls.cur) != 0 {
		t.Errorf("OnPublish's days = %v, want none", days(calls.cur))
	}
}

// checkRecomputed checks that a publisher excluding g1keya, over a store
// holding the day before today and stored as today's, publishes today's
// ranking again and replaces stored.
func checkRecomputed(t *testing.T, stored Published) {
	t.Helper()
	s, db := readyStore(t)
	d := newDayStore(t, db)
	before := publishedOn(t, "2026-11-24")
	put(t, d, before)
	put(t, d, stored)

	p, calls := newPublisher(t, s, d)
	p.Exclude = []string{"g1keya"}
	refresh(t, p)
	if len(calls.cur) != 1 {
		t.Fatalf("OnPublish's days = %v, want [2026-11-25]", days(calls.cur))
	}
	cur := calls.cur[0]
	if cur.Day != "2026-11-25" || !cur.ComputedAt.Equal(gatherNow) || cur.Formula != FormulaVersion || !slices.Equal(cur.Exclusions, []string{"g1keya"}) {
		t.Errorf("published = day %s, computed at %s, formula %d, exclusions %v; want 2026-11-25, %s, %d, [g1keya]",
			cur.Day, cur.ComputedAt, cur.Formula, cur.Exclusions, gatherNow, FormulaVersion)
	}
	if got := entryOf(t, cur.Result, "g1keya").Status; got != Excluded {
		t.Errorf("g1keya's status = %s, want %s", got, Excluded)
	}
	if prev := calls.prev[0]; prev == nil || !reflect.DeepEqual(*prev, before) {
		t.Errorf("OnPublish's prev = %+v, want the day before, %+v", prev, before)
	}
	if got, ok := latest(t, d, chainA); !ok || !reflect.DeepEqual(got, cur) {
		t.Errorf("Latest() = %+v (found %v), want the stored day replaced by the one published", got, ok)
	}
	if got := p.Snapshot().Published; got == nil || !reflect.DeepEqual(*got, cur) {
		t.Errorf("Snapshot().Published = %q, want the day published", dayOf(got))
	}
}

func TestPublisherRecomputesWhenTheExclusionsChange(t *testing.T) {
	checkRecomputed(t, publishedOn(t, "2026-11-25", "g1keyb"))
}

func TestPublisherRecomputesWhenTheFormulaChanges(t *testing.T) {
	stored := publishedOn(t, "2026-11-25", "g1keya")
	stored.Formula = FormulaVersion + 1
	checkRecomputed(t, stored)
}

func TestPublisherWaitsForTheProfiles(t *testing.T) {
	s, db := readyStore(t)
	d := newDayStore(t, db)
	p, calls := newPublisher(t, s, d)
	read := false
	p.Profiles = func() (Profiles, bool) { return nil, read }
	refresh(t, p)
	if snap := p.Snapshot(); snap.State.Kind != Ready || snap.Published != nil || len(calls.cur) != 0 {
		t.Errorf("before the profiles are read: Snapshot() = state %s, day %q, OnPublish's days %v; want ready, none, none",
			snap.State.Kind, dayOf(snap.Published), days(calls.cur))
	}
	if got, ok := latest(t, d, chainA); ok {
		t.Errorf("Latest() = %s, want none", got.Day)
	}

	read = true
	refresh(t, p)
	if got := days(calls.cur); !slices.Equal(got, []string{"2026-11-25"}) {
		t.Errorf("once the profiles are read: OnPublish's days = %v, want [2026-11-25]", got)
	}
}

func TestPublisherKeepsEachChainsDays(t *testing.T) {
	s, db := readyStore(t)
	d := newDayStore(t, db)
	a24 := publishedOn(t, "2026-11-24")
	put(t, d, a24)
	p, calls := newPublisher(t, s, d)
	read := true
	p.Profiles = func() (Profiles, bool) { return nil, read }
	refresh(t, p)
	if len(calls.cur) != 1 || calls.cur[0].Chain != chainA || dayOf(calls.prev[0]) != "2026-11-24" {
		t.Fatalf("on chain A: OnPublish's days %v, prev %q; want [2026-11-25] of A, 2026-11-24", days(calls.cur), dayOf(calls.prev[0]))
	}
	a25 := calls.cur[0]

	// Chain B: A's days are neither served nor B's day before.
	followChain(p, chainB)
	read = false
	refresh(t, p)
	if got := p.Snapshot().Published; got != nil {
		t.Errorf("on chain B before its first day: Snapshot().Published = %s of %s, want none", got.Day, got.Chain)
	}
	read = true
	refresh(t, p)
	if len(calls.cur) != 2 || calls.cur[1].Chain != chainB || calls.cur[1].Day != "2026-11-25" || calls.prev[1] != nil {
		t.Fatalf("on chain B: OnPublish's days %v, the last's prev %q; want 2026-11-25 of B published, no prev", days(calls.cur), dayOf(calls.prev[len(calls.prev)-1]))
	}
	if got, ok := latest(t, d, chainA); !ok || !reflect.DeepEqual(got, a25) {
		t.Errorf("Latest(A) after B's publication = %+v (found %v), want A's 2026-11-25 kept", got, ok)
	}

	// Back on chain A, A's day is served: it is today's, no publication.
	followChain(p, chainA)
	refresh(t, p)
	if got := p.Snapshot().Published; got == nil || !reflect.DeepEqual(*got, a25) {
		t.Errorf("back on chain A: Snapshot().Published = %+v, want A's 2026-11-25", got)
	}
	if len(calls.cur) != 2 {
		t.Errorf("back on chain A: OnPublish's days = %v, want no third", days(calls.cur))
	}
}

func TestPublisherWaitsForTheChainID(t *testing.T) {
	s, db := readyStore(t)
	d := newDayStore(t, db)
	p, calls := newPublisher(t, s, d)
	followChain(p, "") // the tracker has not read the chain yet
	if err := p.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() without a chain id = %v, want nil: not yet is no failure", err)
	}
	if snap := p.Snapshot(); snap.State.Kind != Ready || snap.Published != nil || len(calls.cur) != 0 {
		t.Errorf("without a chain id: Snapshot() = state %s, day %q, OnPublish's days %v; want ready, none, none",
			snap.State.Kind, dayOf(snap.Published), days(calls.cur))
	}
}

func TestPublisherServesFilling(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stored string // the day stored, "" for none
	}{
		{"no day stored", ""},
		{"the day before stored", "2026-11-24"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db := openStore(t)
			// Heights after the span's start, the newest today: the window
			// is complete, the fill is not.
			storeSpaced(t, s, 601, 1000, time.Hour)
			d := newDayStore(t, db)
			var want *Published
			if tc.stored != "" {
				stored := publishedOn(t, tc.stored)
				put(t, d, stored)
				want = &stored
			}
			p, calls := newPublisher(t, s, d)
			refresh(t, p)
			snap := p.Snapshot()
			if snap.State.Kind != Filling || !reflect.DeepEqual(snap.Published, want) {
				t.Errorf("Snapshot() = state %s, day %q; want filling, %q", snap.State.Kind, dayOf(snap.Published), dayOf(want))
			}
			if len(calls.cur) != 0 {
				t.Errorf("OnPublish's days = %v, want none", days(calls.cur))
			}
		})
	}
}

func TestPublisherServesFillingBeforeItsFirstPass(t *testing.T) {
	s, db := readyStore(t)
	p, _ := newPublisher(t, s, newDayStore(t, db))
	if got, want := p.Snapshot(), (Snapshot{State: State{Kind: Filling}}); !reflect.DeepEqual(got, want) {
		t.Errorf("Snapshot() before the first pass = %+v, want %+v", got, want)
	}
}

func TestPublisherKeepsItsStateWhenReadinessFails(t *testing.T) {
	for _, tc := range []struct {
		name      string
		passes    int    // the passes that succeed on chainA before the database closes
		chain     string // the fill's chain on the pass that fails
		wantState State
		wantDay   string // "" for none
	}{
		{"on the first pass", 0, chainA, State{Kind: Filling}, ""},
		{"after a pass that published", 1, chainA, State{Kind: Ready}, "2026-11-25"},
		// The chain id is known without the state: the other chain's day goes.
		{"after a pass that published, on another chain", 1, chainB, State{Kind: Ready}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, db := readyStore(t)
			p, _ := newPublisher(t, s, newDayStore(t, db))
			for range tc.passes {
				refresh(t, p)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			followChain(p, tc.chain)
			if err := p.Refresh(context.Background()); err == nil || !strings.Contains(err.Error(), "readiness") {
				t.Errorf("Refresh() on a closed database = %v, want a readiness error", err)
			}
			if snap := p.Snapshot(); !reflect.DeepEqual(snap.State, tc.wantState) || dayOf(snap.Published) != tc.wantDay {
				t.Errorf("Snapshot() = state %+v, day %q; want %+v, %q", snap.State, dayOf(snap.Published), tc.wantState, tc.wantDay)
			}
		})
	}
}

func TestPublisherServesAChainsDayAgainAfterAFailedPassOnAnother(t *testing.T) {
	s, db := readyStore(t)
	p, calls := newPublisher(t, s, newDayStore(t, db))
	read := true
	p.Profiles = func() (Profiles, bool) { return nil, read }
	refresh(t, p) // publishes A's 2026-11-25

	// A pass on chain B fails its readiness: A's day goes.
	followChain(p, chainB)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.Refresh(canceled); err == nil {
		t.Fatal("Refresh() on a canceled context = nil, want a readiness error")
	}

	// Back on chain A, its stored day is served again, and not published a
	// second time.
	followChain(p, chainA)
	read = false
	refresh(t, p)
	if got := dayOf(p.Snapshot().Published); got != "2026-11-25" {
		t.Errorf("back on chain A: Snapshot().Published = %q, want A's stored 2026-11-25", got)
	}
	read = true
	refresh(t, p)
	if got := days(calls.cur); !slices.Equal(got, []string{"2026-11-25"}) {
		t.Errorf("OnPublish's days = %v, want [2026-11-25]: A's day is published once", got)
	}
}

func TestPublisherStoresAndServesTheDayBeforeOnPublish(t *testing.T) {
	s, db := readyStore(t)
	d := newDayStore(t, db)
	p, _ := newPublisher(t, s, d)
	var served, stored *Published
	p.OnPublish = func(cur Published, prev *Published) {
		served = p.Snapshot().Published
		if got, ok := latest(t, d, chainA); ok {
			stored = &got
		}
	}
	refresh(t, p)
	if dayOf(served) != "2026-11-25" || dayOf(stored) != "2026-11-25" {
		t.Errorf("during OnPublish: served %q, stored %q; want 2026-11-25 both", dayOf(served), dayOf(stored))
	}
}

func TestPublisherKeepsTheServedDayOnAFailedPass(t *testing.T) {
	s, db := readyStore(t)
	d := newDayStore(t, db)
	before := publishedOn(t, "2026-11-24")
	put(t, d, before)
	// The set of the window's last height is missing: Gather fails.
	if _, err := db.Exec("DELETE FROM blocks_valset_members"); err != nil {
		t.Fatal(err)
	}
	p, calls := newPublisher(t, s, d)
	err := p.Refresh(context.Background())
	if want := fmt.Sprintf("at %d", windowLast); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Refresh() = %v, want an error naming the set %s", err, want)
	}
	if got := p.Snapshot().Published; got == nil || !reflect.DeepEqual(*got, before) {
		t.Errorf("Snapshot().Published = %q, want 2026-11-24 still served", dayOf(got))
	}
	if got, ok := latest(t, d, chainA); !ok || !reflect.DeepEqual(got, before) || len(calls.cur) != 0 {
		t.Errorf("Latest() = %q, OnPublish's days %v; want 2026-11-24 and none", got.Day, days(calls.cur))
	}
}

func TestPublisherSnapshotWhileRefreshing(t *testing.T) {
	s, db := readyStore(t)
	p, _ := newPublisher(t, s, newDayStore(t, db))
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if got := dayOf(p.Snapshot().Published); got != "" && got != "2026-11-25" {
				t.Errorf("Snapshot().Published = %q while refreshing, want none or 2026-11-25", got)
			}
		}
	})
	err := p.Refresh(context.Background())
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
}

// ---- Run

// countingFill is a block fill counting the reads of its progress, one a
// pass.
type countingFill struct{ reads atomic.Int64 }

func (f *countingFill) Progress() blocks.Progress {
	f.reads.Add(1)
	return blocks.Progress{}
}

// logLines records log lines.
type logLines struct {
	mu    sync.Mutex
	lines []string
}

func (l *logLines) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
}

func (l *logLines) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.lines)
}

func TestPublisherRunRefreshesEvery30Seconds(t *testing.T) {
	s, db := openStore(t)
	d := newDayStore(t, db)
	// A closed database fails every pass at its first read.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	f := &countingFill{}
	logs := &logLines{}
	p := &Publisher{Blocks: s, Fill: f, Days: d, Now: time.Now, logf: logs.logf}
	wantLog := []string{"ranking: refresh: readiness: begin read: sql: database is closed"}

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			p.Run(ctx)
			close(done)
		}()
		check := func(when string, wantPasses int64) {
			t.Helper()
			synctest.Wait()
			if got := f.reads.Load(); got != wantPasses {
				t.Errorf("%s: %d passes, want %d", when, got, wantPasses)
			}
			if got := logs.all(); !slices.Equal(got, wantLog) {
				t.Errorf("%s: log = %q, want %q: a repeated error logged once", when, got, wantLog)
			}
		}
		check("at start", 1)
		time.Sleep(refreshEvery - time.Nanosecond)
		check("just before 30 s", 1)
		time.Sleep(time.Nanosecond)
		check("at 30 s", 2)
		time.Sleep(refreshEvery)
		check("at 60 s", 3)
		cancel()
		<-done
	})
}

// cancelingFill is a block fill ending the context of the pass that reads its
// progress.
type cancelingFill struct{ cancel context.CancelFunc }

func (f cancelingFill) Progress() blocks.Progress {
	f.cancel()
	return blocks.Progress{}
}

func TestPublisherRunLeavesAPassCutShortUnlogged(t *testing.T) {
	s, db := openStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logs := &logLines{}
	p := &Publisher{Blocks: s, Fill: cancelingFill{cancel}, Days: newDayStore(t, db), Now: stoppedAt(gatherNow), logf: logs.logf}
	p.Run(ctx)
	if got := logs.all(); len(got) != 0 {
		t.Errorf("log of a pass its context cut short = %q, want none", got)
	}
}

func TestPassLogReport(t *testing.T) {
	errA, errB := errors.New("a failed"), errors.New("b failed")
	for _, tc := range []struct {
		name   string
		passes []error
		want   []string
	}{
		{"passes that succeed", []error{nil, nil}, nil},
		{"an error repeated", []error{errA, errA, errA}, []string{"ranking: refresh: a failed"}},
		{"another error", []error{errA, errB, errA}, []string{"ranking: refresh: a failed", "ranking: refresh: b failed", "ranking: refresh: a failed"}},
		{
			"a pass that succeeds after errors",
			[]error{errA, nil, nil, errA},
			[]string{"ranking: refresh: a failed", "ranking: refresh ok again", "ranking: refresh: a failed"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := &logLines{}
			l := passLog{logf: logs.logf}
			for _, err := range tc.passes {
				l.report(err)
			}
			if got := logs.all(); !slices.Equal(got, tc.want) {
				t.Errorf("report(%v) logged %q, want %q", tc.passes, got, tc.want)
			}
		})
	}
}

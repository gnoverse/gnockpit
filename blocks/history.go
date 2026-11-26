package blocks

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"
)

// The scoring spec's constants, fixed in code.
const (
	// OutageMin is the number of consecutive absences that make an outage:
	// a run counts as one in the scoring window once this many of its
	// heights are inside it.
	OutageMin      = 10
	recoveryRunCap = 24 * time.Hour // a recovery run leaves the outages up to this much block time after block b+1
	scoringDays    = 30             // the window: the complete UTC days before today
	recoveryDays   = 180            // the days whose recovery samples count
	recoveryLast   = 3              // the recovery samples the median takes, newest first
)

// Day is a validator's counters over a UTC day.
type Day struct {
	Day                                time.Time // midnight UTC
	InSet, Absent, Nil, OtherBlock     int64     // InSet = Votes + Absent
	ProposalsExpected, ProposalsMissed int64
	Votes, DelaySumMs                  int64
	DelayBuckets                       [DelayBuckets]int64
}

// Signed returns the blocks the validator signed: a nil vote signs.
func (d Day) Signed() int64 { return d.InSet - d.Absent - d.OtherBlock }

// Outage is a run of OutageMin or more consecutive absences.
type Outage struct {
	First, Last        int64
	FirstTime, EndTime time.Time // EndTime: block Last+1's time, or the last block read's while Ongoing
	Ongoing            bool      // the run reaches the last height whose absences were read
}

// Recovery is how long the validator took to sign again after a restart.
type Recovery struct {
	Restart     int64         // b: block b+1 came more than RestartGap after block b
	RestartTime time.Time     // block b+1's time
	SignedAt    int64         // h, the first height from b its precommit is in; 0 while Pending
	Duration    time.Duration // time(h+1) − time(b+1)
	Pending     bool          // block h+1, which times it, is not stored yet
	Scored      bool          // one of the last recoveryLast samples of the recoveryDays window
}

// Summary is a validator's raw figures over the scoring windows.
type Summary struct {
	FirstDay, LastDay                               time.Time // the 30 complete UTC days before today
	InSet, Signed, Absent, Nil, OtherBlock, Outages int64
	Recovering                                      int64 // heights of recovery runs in the window, up to recoveryRunCap of block time after block b+1
	LongestAbsence                                  int64 // the longest run of absences in the window, clipped to it, recovery runs left out, runs under OutageMin included
	InSetMs                                         int64 // block time in the set in the window: each of its heights' time to the next stored block
	// The scoring spec's proposals: proposalCounts' k rule on the heights
	// signed, restart heights b left out. A Day's counters count every height.
	ProposalsExpected, ProposalsMissed int64
	Votes, DelaySumMs                  int64
	RecoverySamples                    int           // within the 180 complete UTC days before today
	RecoveryMedian                     time.Duration // of the last 3 of those; 0 when none
	RecoverySlowest                    time.Duration // of those; 0 when none
}

// Uptime returns the share of the blocks in the set the validator signed,
// recovery runs left out of both (the scoring spec's uptime); false when no
// block counts.
func (s Summary) Uptime() (float64, bool) {
	counted := s.InSet - s.Recovering
	if counted == 0 {
		return 0, false
	}
	return float64(s.Signed) / float64(counted), true
}

// History is what the block history holds about a validator's keys.
type History struct {
	Days       []Day      // ascending, days with a row only
	Outages    []Outage   // ascending
	Recoveries []Recovery // ascending
	Summary    Summary
}

// ValidatorHistory reads the history of a validator's signing keys, in one
// read transaction: its days (summed across keys), its outages, its
// recoveries after restarts, and their summary over the scoring windows as of
// now, from every stored height. A key the store never saw adds nothing.
func (s *Store) ValidatorHistory(ctx context.Context, keys []string, now time.Time) (History, error) {
	return s.validatorHistory(ctx, keys, now, historyEnd{})
}

// WindowHistory reads the history of a validator's signing keys as the
// complete scoring window w ends, as ValidatorHistory does as of the day
// after w.LastDay: from its days up to w.LastDay, its absences up to w.Last
// (a run going on there ends there) and the block times up to w.Last+1,
// which times w.Last (a recovery whose block h+1 is above it is pending).
// Blocks stored after w.Last+1 change nothing: whenever it is read, a
// window's history is the same.
func (s *Store) WindowHistory(ctx context.Context, keys []string, w ScoringWindow) (History, error) {
	if !w.Complete || w.Last == 0 {
		return History{}, fmt.Errorf("history of the scoring window %s to %s: the window has no last height",
			w.FirstDay.Format(time.DateOnly), w.LastDay.Format(time.DateOnly))
	}
	return s.validatorHistory(ctx, keys, w.LastDay.AddDate(0, 0, 1), historyEnd{last: w.Last, lastDay: w.LastDay})
}

// historyEnd is where a history read ends: the absences up to last, the
// block times up to last+1 and the day counters up to lastDay. The zero
// historyEnd reads every stored height and day.
type historyEnd struct {
	last    int64
	lastDay time.Time
}

func (s *Store) validatorHistory(ctx context.Context, keys []string, now time.Time, end historyEnd) (History, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return History{}, fmt.Errorf("begin read: %w", err)
	}
	defer tx.Rollback()
	r := historyReader{tx: tx}
	if r.keys, err = r.keyIDs(ctx, keys); err != nil {
		return History{}, err
	}
	r.ids = slices.Sorted(maps.Values(r.keys))
	if r.low, r.high, err = storedRange(ctx, tx); err != nil {
		return History{}, err
	}
	r.last, r.lastDay = r.high, int64(math.MaxInt64)
	if end.last > 0 {
		if end.last >= r.high {
			return History{}, fmt.Errorf("history up to %d: block %d, which times it, is not stored", end.last, end.last+1)
		}
		r.last, r.high, r.lastDay = end.last, end.last+1, dayOf(end.lastDay)
	}
	var h History
	if h.Days, err = r.days(ctx); err != nil {
		return History{}, err
	}
	merged, err := r.mergedRuns(ctx)
	if err != nil {
		return History{}, err
	}
	// A recovery run, by its first height → its restart's block b+1 time, in
	// ms.
	recoveryRuns := map[int64]int64{}
	if h.Recoveries, err = r.recoveries(ctx, merged, recoveryRuns); err != nil {
		return History{}, err
	}
	runs, err := r.splitRecovering(ctx, merged, recoveryRuns)
	if err != nil {
		return History{}, err
	}
	if h.Outages, err = r.outages(ctx, runs.counted); err != nil {
		return History{}, err
	}
	if err := r.summarize(ctx, &h, runs, now); err != nil {
		return History{}, err
	}
	return h, nil
}

// historyReader reads a validator's history within one transaction.
type historyReader struct {
	tx        *sql.Tx
	low, high int64            // the heights read: the stored range, or up to a window's end+1
	last      int64            // the last height whose absences are read: high, or a window's end
	lastDay   int64            // the dayOf number of the last day whose counters are read
	keys      map[string]int64 // the id of each of the validator's keys the store saw, by address
	ids       []int64          // those ids, ascending
}

// keyIDs returns the id of each key the store saw, by address.
func (r historyReader) keyIDs(ctx context.Context, keys []string) (map[string]int64, error) {
	ids := map[string]int64{}
	for _, k := range keys {
		var id int64
		err := r.tx.QueryRowContext(ctx, `SELECT id FROM blocks_validators WHERE address = ?`, k).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("validator %s: %w", k, err)
		}
		ids[k] = id
	}
	return ids, nil
}

func (r historyReader) timeAt(ctx context.Context, h int64) (time.Time, error) {
	ms, err := timeOf(ctx, r.tx, h)
	return time.UnixMilli(ms).UTC(), err
}

// days sums the keys' day counters and vote delays per UTC day, up to
// lastDay. Blocks in the set are the precommits present (votes) plus the
// absent slots.
func (r historyReader) days(ctx context.Context) ([]Day, error) {
	byDay := map[int64]*Day{}
	get := func(day int64) *Day {
		if d, ok := byDay[day]; ok {
			return d
		}
		d := &Day{Day: time.Unix(day*secondsPerDay, 0).UTC()}
		byDay[day] = d
		return d
	}
	for _, id := range r.ids {
		err := r.each(ctx, func(rows *sql.Rows) error {
			var day, absent, nilVotes, other, expected, missed int64
			if err := rows.Scan(&day, &absent, &nilVotes, &other, &expected, &missed); err != nil {
				return err
			}
			d := get(day)
			d.Absent += absent
			d.Nil += nilVotes
			d.OtherBlock += other
			d.ProposalsExpected += expected
			d.ProposalsMissed += missed
			return nil
		}, `SELECT day, absent, nil_votes, other_block, proposals_expected, proposals_missed FROM blocks_days WHERE validator = ? AND day <= ?`, id, r.lastDay)
		if err != nil {
			return nil, fmt.Errorf("day counters of %d: %w", id, err)
		}
		err = r.each(ctx, func(rows *sql.Rows) error {
			var day, votes, sum int64
			var b [DelayBuckets]int64
			if err := rows.Scan(&day, &votes, &sum, &b[0], &b[1], &b[2], &b[3], &b[4], &b[5], &b[6]); err != nil {
				return err
			}
			d := get(day)
			d.Votes += votes
			d.DelaySumMs += sum
			for i := range b {
				d.DelayBuckets[i] += b[i]
			}
			return nil
		}, `SELECT day, votes, sum_ms, b0, b1, b2, b3, b4, b5, b6 FROM blocks_vote_delays WHERE validator = ? AND day <= ?`, id, r.lastDay)
		if err != nil {
			return nil, fmt.Errorf("vote delays of %d: %w", id, err)
		}
	}
	out := make([]Day, 0, len(byDay))
	for _, d := range byDay {
		d.InSet = d.Votes + d.Absent
		out = append(out, *d)
	}
	slices.SortFunc(out, func(a, b Day) int { return a.Day.Compare(b.Day) })
	return out, nil
}

func (r historyReader) each(ctx context.Context, scan func(*sql.Rows) error, query string, args ...any) error {
	rows, err := r.tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// inSet reports whether one of the validator's keys is a member of height's
// set.
func (r historyReader) inSet(ctx context.Context, height int64) (bool, error) {
	list, args := idList(r.ids)
	var one int
	err := r.tx.QueryRowContext(ctx,
		`SELECT 1 FROM blocks b JOIN blocks_valset_members m ON m.valset = b.valset
		 WHERE b.height = ? AND m.validator IN `+list+` LIMIT 1`, slices.Concat([]any{height}, args)...,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("membership at %d: %w", height, err)
	}
	return true, nil
}

// mergedRuns returns the validator's runs of absences up to last,
// ascending: its keys' runs, a key's run and the next key's starting right
// after it (a rotation) being one, and a run going on past last ending there.
// The scoring spec judges the validator, not its keys.
func (r historyReader) mergedRuns(ctx context.Context) ([]Run, error) {
	var runs []Run
	for _, id := range r.ids {
		keyRuns, err := runsOf(ctx, r.tx, id, 1)
		if err != nil {
			return nil, err
		}
		runs = append(runs, keyRuns...)
	}
	slices.SortFunc(runs, func(a, b Run) int { return cmp.Compare(a.First, b.First) })
	var out []Run
	for _, run := range runs {
		if run.First > r.last {
			break
		}
		run.Last = min(run.Last, r.last)
		if n := len(out); n > 0 && run.First == out[n-1].Last+1 {
			out[n-1].Last = run.Last
			continue
		}
		out = append(out, run)
	}
	return out, nil
}

// runHolding returns the run of runs, ascending, that holds height h.
func runHolding(runs []Run, h int64) (Run, bool) {
	i, found := slices.BinarySearchFunc(runs, h, func(run Run, height int64) int { return cmp.Compare(run.First, height) })
	if found {
		return runs[i], true
	}
	if i > 0 && runs[i-1].Last >= h {
		return runs[i-1], true
	}
	return Run{}, false
}

// recoveries returns, per restart gap b, the validator's sample (the scoring
// spec's "Recovery sample") on its merged runs: R is the run holding b, or
// the run ending at b−1; it signs again at h = R.Last+1 when R holds b, else
// at b; it has a sample when one of its keys is in the set at b−1 and one at
// b, and R held fewer than OutageMin heights at or below b−1, pending while
// block h+1 is above high. Each sampled R is a recovery run, recorded in
// recoveryRuns: the run ending at b−1 before a signed b as much as the run
// holding b.
func (r historyReader) recoveries(ctx context.Context, runs []Run, recoveryRuns map[int64]int64) ([]Recovery, error) {
	// A gap at b needs block b+1: b is below high.
	gaps, err := gapsIn(ctx, r.tx, r.low, r.high-1)
	if err != nil {
		return nil, err
	}
	var out []Recovery
	for _, b := range gaps {
		sample, ok, err := r.recovery(ctx, runs, b)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out = append(out, sample.Recovery)
		if sample.run.Last > 0 {
			recoveryRuns[sample.run.First] = sample.RestartTime.UnixMilli()
		}
	}
	return out, nil
}

// recoverySample is a restart's recovery and the run it was timed on.
type recoverySample struct {
	Recovery
	run Run
}

// recovery returns the validator's sample for the restart gap b. None when
// its run may reach below the stored history, or ends with none of its keys
// in the set: it never signed again.
func (r historyReader) recovery(ctx context.Context, runs []Run, b int64) (recoverySample, bool, error) {
	for _, h := range []int64{b - 1, b} {
		if in, err := r.inSet(ctx, h); err != nil || !in {
			return recoverySample{}, false, err
		}
	}
	run, holds := runHolding(runs, b)
	if !holds {
		run, _ = runHolding(runs, b-1) // Run{} when none holds b−1
	}
	if run.Last > 0 && run.First == r.low && r.low > 1 {
		return recoverySample{}, false, nil
	}
	signedAt, pre := b, int64(0)
	if run.Last >= b {
		signedAt = run.Last + 1
	}
	if run.Last > 0 {
		pre = max(0, min(run.Last, b-1)-run.First+1)
	}
	if pre >= OutageMin {
		return recoverySample{}, false, nil
	}
	if run.Last >= b && signedAt <= r.high {
		if in, err := r.inSet(ctx, signedAt); err != nil || !in {
			return recoverySample{}, false, err
		}
	}
	sample := recoverySample{Recovery: Recovery{Restart: b}, run: run}
	var err error
	if sample.RestartTime, err = r.timeAt(ctx, b+1); err != nil {
		return recoverySample{}, false, err
	}
	if signedAt+1 > r.high {
		sample.Pending = true
		return sample, true, nil
	}
	end, err := r.timeAt(ctx, signedAt+1)
	if err != nil {
		return recoverySample{}, false, err
	}
	sample.SignedAt, sample.Duration = signedAt, end.Sub(sample.RestartTime)
	return sample, true, nil
}

// absenceRuns are the validator's merged runs of absences, ascending, split
// by the recovery runs.
type absenceRuns struct {
	recovering []Run // recovery runs, up to recoveryRunCap of block time after their restart's block b+1
	counted    []Run // the other runs, and the recovery runs' rest: they count in uptime and outages
}

// splitRecovering splits the validator's merged runs: a recovery run, by its
// first height in recoveryRuns, is recovering up to recoveryRunCap of block
// time after its restart's block b+1, and counted past it.
func (r historyReader) splitRecovering(ctx context.Context, runs []Run, recoveryRuns map[int64]int64) (absenceRuns, error) {
	var out absenceRuns
	for _, run := range runs {
		restartMs, recovering := recoveryRuns[run.First]
		if !recovering {
			out.counted = append(out.counted, run)
			continue
		}
		capMs := restartMs + recoveryRunCap.Milliseconds() + 1
		past, ok, err := firstFrom(ctx, r.tx, capMs, run.First, run.Last)
		if err != nil {
			return absenceRuns{}, err
		}
		if !ok {
			out.recovering = append(out.recovering, run)
			continue
		}
		// A recovery run starts at or below b, timed before b+1: past > run.First.
		out.recovering = append(out.recovering, Run{First: run.First, Last: past - 1})
		out.counted = append(out.counted, Run{First: past, Last: run.Last})
	}
	return out, nil
}

// outages returns the counted runs of OutageMin or more absences, ascending.
func (r historyReader) outages(ctx context.Context, counted []Run) ([]Outage, error) {
	var out []Outage
	for _, run := range counted {
		if run.Last-run.First+1 < OutageMin {
			continue
		}
		o, err := r.outage(ctx, run)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

func (r historyReader) outage(ctx context.Context, run Run) (Outage, error) {
	o := Outage{First: run.First, Last: run.Last}
	var err error
	if o.FirstTime, err = r.timeAt(ctx, run.First); err != nil {
		return Outage{}, err
	}
	end := run.Last + 1
	if run.Last >= r.last {
		o.Ongoing, end = true, r.high
	}
	if o.EndTime, err = r.timeAt(ctx, end); err != nil {
		return Outage{}, err
	}
	return o, nil
}

// summarize sets h's summary over the scoring windows as of now: the days,
// runs of absences, block time in the set and proposals of the 30 complete
// UTC days before today (a run is clipped to them, an outage counts once
// OutageMin of its heights are inside), and the recovery samples of the 180
// complete UTC days before today (a sample belongs to the day of block b+1),
// marking the last recoveryLast of them Scored.
func (r historyReader) summarize(ctx context.Context, h *History, runs absenceRuns, now time.Time) error {
	w, err := scoringWindow(ctx, r.tx, r.low, r.high, now)
	if err != nil {
		return err
	}
	s := Summary{FirstDay: w.FirstDay, LastDay: w.LastDay}
	for _, d := range h.Days {
		if d.Day.Before(w.FirstDay) || d.Day.After(w.LastDay) {
			continue
		}
		s.InSet += d.InSet
		s.Signed += d.Signed()
		s.Absent += d.Absent
		s.Nil += d.Nil
		s.OtherBlock += d.OtherBlock
		s.Votes += d.Votes
		s.DelaySumMs += d.DelaySumMs
	}
	// Without a key the store saw, every figure of the window is 0: its
	// queries, each a scan of the window's blocks, are skipped.
	if w.First > 0 && len(r.keys) > 0 {
		lo, hi := w.First, w.Last
		for _, run := range runs.recovering {
			s.Recovering += overlap(run, lo, hi)
		}
		for _, run := range runs.counted {
			inside := overlap(run, lo, hi)
			s.LongestAbsence = max(s.LongestAbsence, inside)
			if inside >= OutageMin {
				s.Outages++
			}
		}
		if s.InSetMs, err = r.inSetMs(ctx, lo, hi); err != nil {
			return err
		}
		if s.ProposalsExpected, s.ProposalsMissed, err = r.proposals(ctx, lo, hi); err != nil {
			return err
		}
	}
	var samples []int // indices in h.Recoveries
	for i, rec := range h.Recoveries {
		day := rec.RestartTime.Truncate(24 * time.Hour)
		if rec.Pending || day.Before(w.SpanStart) || day.After(w.LastDay) {
			continue
		}
		samples = append(samples, i)
		s.RecoverySlowest = max(s.RecoverySlowest, rec.Duration)
	}
	var last []time.Duration
	for _, i := range samples[max(0, len(samples)-recoveryLast):] {
		h.Recoveries[i].Scored = true
		last = append(last, h.Recoveries[i].Duration)
	}
	s.RecoverySamples = len(samples)
	s.RecoveryMedian = median(last)
	h.Summary = s
	return nil
}

// overlap returns how many of run's heights are within lo to hi.
func overlap(run Run, lo, hi int64) int64 {
	return max(0, min(run.Last, hi)-max(run.First, lo)+1)
}

// inSetMs returns the block time the keys spent in the set from height lo to
// hi: the sum, over those heights whose set holds one of the keys, of each
// one's time to the next stored block; hi counts 0 when the next is not
// stored. The stored heights run unbroken (firstFrom), so the next stored
// block is h+1, joined by its key: LEAD over the span would materialise
// every row of it first.
func (r historyReader) inSetMs(ctx context.Context, lo, hi int64) (int64, error) {
	list, args := idList(r.ids)
	var ms int64
	err := r.tx.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(n.time_ms - b.time_ms), 0) FROM blocks b JOIN blocks n ON n.height = b.height + 1
		 WHERE b.height BETWEEN ? AND ? AND b.valset IN (SELECT valset FROM blocks_valset_members WHERE validator IN `+list+`)`,
		slices.Concat([]any{lo, hi}, args)...,
	).Scan(&ms)
	if err != nil {
		return 0, fmt.Errorf("block time in the set %d-%d: %w", lo, hi, err)
	}
	return ms, nil
}

// proposals returns the keys' expected and missed proposals from height lo
// to hi, by proposalCounts' k rule: round-0 blocks by one aggregate, late
// blocks one by one. A height counts for a key only when it signed it, and
// a restart height b for none: a round can fail there only because too few
// validators are up yet.
func (r historyReader) proposals(ctx context.Context, lo, hi int64) (expected, missed int64, err error) {
	list, args := idList(r.ids)
	err = r.tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blocks b WHERE b.height BETWEEN ? AND ? AND b.round = 0 AND b.proposer IN `+list+`
		 AND NOT EXISTS (SELECT 1 FROM blocks_absences a WHERE a.height = b.height AND a.validator = b.proposer AND a.kind IN (?, ?))
		 AND NOT EXISTS (SELECT 1 FROM blocks_gaps g WHERE g.height = b.height)`,
		slices.Concat([]any{lo, hi}, args, []any{Absent, OtherBlock})...,
	).Scan(&expected)
	if err != nil {
		return 0, 0, fmt.Errorf("round-0 proposals %d-%d: %w", lo, hi, err)
	}
	gaps, err := gapsIn(ctx, r.tx, lo, hi)
	if err != nil {
		return 0, 0, err
	}
	late, err := lateBlocks(ctx, r.tx, lo, hi)
	if err != nil {
		return 0, 0, err
	}
	for _, block := range late {
		if _, restart := slices.BinarySearch(gaps, block.Height); restart {
			continue
		}
		counts, misses, ok := proposalCounts(block)
		if !ok {
			continue
		}
		for addr, id := range r.keys {
			if counts[addr] == 0 {
				continue
			}
			signed, signErr := r.signed(ctx, id, block.Height)
			if signErr != nil {
				return 0, 0, signErr
			}
			if signed {
				expected += counts[addr]
				missed += misses[addr]
			}
		}
	}
	return expected, missed, nil
}

// signed reports whether a member of height's set signed it: no absence of
// kind Absent or OtherBlock there, a nil vote signing.
func (r historyReader) signed(ctx context.Context, id, height int64) (bool, error) {
	var unsigned bool
	err := r.tx.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM blocks_absences WHERE height = ? AND validator = ? AND kind IN (?, ?))`,
		height, id, Absent, OtherBlock,
	).Scan(&unsigned)
	if err != nil {
		return false, fmt.Errorf("signature of %d at %d: %w", id, height, err)
	}
	return !unsigned, nil
}

// idList returns the SQL list "(?, ?, …)" of ids, and its arguments.
func idList(ids []int64) (string, []any) {
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?, ", len(args)), ", ") + ")", args
}

// median returns the middle of ds, the mean of the two middles for an even
// count, 0 for none.
func median(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	sorted := slices.Clone(ds)
	slices.Sort(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

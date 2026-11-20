package blocks

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"
)

// The scoring spec's constants, fixed in code.
const (
	outageMin      = 10             // consecutive absences that make an outage
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

// Outage is a run of outageMin or more consecutive absences.
type Outage struct {
	First, Last        int64
	FirstTime, EndTime time.Time // EndTime: block Last+1's time, or the newest block's while Ongoing
	Ongoing            bool
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
	FirstDay, LastDay                                     time.Time // the 30 complete UTC days before today
	InSet, Signed, Absent, Nil, OtherBlock, Outages       int64
	ProposalsExpected, ProposalsMissed, Votes, DelaySumMs int64
	RecoverySamples                                       int           // within the 180 complete UTC days before today
	RecoveryMedian                                        time.Duration // of the last 3 of those; 0 when none
	RecoverySlowest                                       time.Duration // of those; 0 when none
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
// now. A key the store never saw adds nothing.
func (s *Store) ValidatorHistory(ctx context.Context, keys []string, now time.Time) (History, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return History{}, fmt.Errorf("begin read: %w", err)
	}
	defer tx.Rollback()
	r := historyReader{tx: tx}
	ids, err := r.keyIDs(ctx, keys)
	if err != nil {
		return History{}, err
	}
	if r.low, r.high, err = storedRange(ctx, tx); err != nil {
		return History{}, err
	}
	var h History
	if h.Days, err = r.days(ctx, ids); err != nil {
		return History{}, err
	}
	// A recovery run, by its first height (one key is in the set at a time)
	// → its restart's block b+1 time, in ms.
	recoveryRuns := map[int64]int64{}
	if h.Recoveries, err = r.recoveries(ctx, ids, recoveryRuns); err != nil {
		return History{}, err
	}
	if h.Outages, err = r.outages(ctx, ids, recoveryRuns); err != nil {
		return History{}, err
	}
	if err := r.summarize(ctx, &h, now); err != nil {
		return History{}, err
	}
	return h, nil
}

// historyReader reads a validator's history within one transaction.
type historyReader struct {
	tx        *sql.Tx
	low, high int64 // the stored range
}

func (r historyReader) keyIDs(ctx context.Context, keys []string) ([]int64, error) {
	var ids []int64
	for _, k := range keys {
		var id int64
		err := r.tx.QueryRowContext(ctx, `SELECT id FROM blocks_validators WHERE address = ?`, k).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("validator %s: %w", k, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (r historyReader) timeAt(ctx context.Context, h int64) (time.Time, error) {
	ms, err := timeOf(ctx, r.tx, h)
	return time.UnixMilli(ms).UTC(), err
}

// days sums the keys' day counters and vote delays per UTC day. Blocks in
// the set are the precommits present (votes) plus the absent slots.
func (r historyReader) days(ctx context.Context, ids []int64) ([]Day, error) {
	byDay := map[int64]*Day{}
	get := func(day int64) *Day {
		if d, ok := byDay[day]; ok {
			return d
		}
		d := &Day{Day: time.Unix(day*secondsPerDay, 0).UTC()}
		byDay[day] = d
		return d
	}
	for _, id := range ids {
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
		}, `SELECT day, absent, nil_votes, other_block, proposals_expected, proposals_missed FROM blocks_days WHERE validator = ?`, id)
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
		}, `SELECT day, votes, sum_ms, b0, b1, b2, b3, b4, b5, b6 FROM blocks_vote_delays WHERE validator = ?`, id)
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

// inSet reports whether the validator is a member of height's set.
func (r historyReader) inSet(ctx context.Context, id, height int64) (bool, error) {
	var one int
	err := r.tx.QueryRowContext(ctx,
		`SELECT 1 FROM blocks b JOIN blocks_valset_members m ON m.valset = b.valset
		 WHERE b.height = ? AND m.validator = ? LIMIT 1`, height, id,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("membership of %d at %d: %w", id, height, err)
	}
	return true, nil
}

// recoveries returns, per restart gap b, the sample of the key in the set at
// b−1 and b (the scoring spec's "Recovery sample"): R is the run holding b,
// or the run ending at b−1; it signs again at h = R.Last+1 when R holds b,
// else at b; it has a sample when R held fewer than outageMin heights at or
// below b−1, pending while block h+1 is not stored. Each sampled run holding b
// is recorded in recoveryRuns.
func (r historyReader) recoveries(ctx context.Context, ids []int64, recoveryRuns map[int64]int64) ([]Recovery, error) {
	gaps, err := gapsIn(ctx, r.tx, r.low, r.high)
	if err != nil {
		return nil, err
	}
	var out []Recovery
	for _, b := range gaps {
		for _, id := range ids {
			sample, ok, err := r.recovery(ctx, id, b)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			out = append(out, sample.Recovery)
			if sample.run.Last >= b {
				recoveryRuns[sample.run.First] = sample.RestartTime.UnixMilli()
			}
			break // one key is in the set at a time
		}
	}
	return out, nil
}

// recoverySample is a restart's recovery and the run it was timed on.
type recoverySample struct {
	Recovery
	run Run
}

// recovery returns the key's sample for the restart gap b. None when its run
// may reach below the stored history, or ends with the key out of the set: it
// never signed again.
func (r historyReader) recovery(ctx context.Context, id, b int64) (recoverySample, bool, error) {
	for _, h := range []int64{b - 1, b} {
		if in, err := r.inSet(ctx, id, h); err != nil || !in {
			return recoverySample{}, false, err
		}
	}
	run, holds, err := runAt(ctx, r.tx, id, b)
	if err != nil {
		return recoverySample{}, false, err
	}
	if !holds {
		if run, holds, err = runAt(ctx, r.tx, id, b-1); err != nil {
			return recoverySample{}, false, err
		}
		if !holds {
			run = Run{}
		}
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
	if pre >= outageMin {
		return recoverySample{}, false, nil
	}
	if run.Last >= b && signedAt <= r.high {
		if in, err := r.inSet(ctx, id, signedAt); err != nil || !in {
			return recoverySample{}, false, err
		}
	}
	sample := recoverySample{Recovery: Recovery{Restart: b}, run: run}
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

// outages returns the keys' runs of outageMin or more absences, ascending; a
// key's run and the next key's starting right after it (a rotation) are one.
// A recovery run is none, up to recoveryRunCap of block time after its
// restart's block b+1; past that, its rest is one when it holds outageMin or
// more heights.
func (r historyReader) outages(ctx context.Context, ids []int64, recoveryRuns map[int64]int64) ([]Outage, error) {
	var runs []Run
	for _, id := range ids {
		keyRuns, err := runsOf(ctx, r.tx, id, 1)
		if err != nil {
			return nil, err
		}
		runs = append(runs, keyRuns...)
	}
	slices.SortFunc(runs, func(a, b Run) int { return cmp.Compare(a.First, b.First) })
	var out []Outage
	for i := 0; i < len(runs); {
		run := runs[i]
		restartMs, recovering := recoveryRuns[run.First]
		for i++; i < len(runs) && runs[i].First == run.Last+1; i++ {
			run.Last = runs[i].Last
			if ms, ok := recoveryRuns[runs[i].First]; ok && !recovering {
				restartMs, recovering = ms, true
			}
		}
		if recovering {
			capMs := restartMs + recoveryRunCap.Milliseconds() + 1
			from, past, err := firstFrom(ctx, r.tx, capMs, run.First, run.Last)
			if err != nil {
				return nil, err
			}
			if !past {
				continue
			}
			run.First = from
		}
		if run.Last-run.First+1 < outageMin {
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
	if run.Last >= r.high {
		o.Ongoing, end = true, r.high
	}
	if o.EndTime, err = r.timeAt(ctx, end); err != nil {
		return Outage{}, err
	}
	return o, nil
}

// summarize sets h's summary over the scoring windows as of now: the days and
// outages of the 30 complete UTC days before today (an outage counts once
// outageMin of its heights are inside), and the recovery samples of the 180
// complete UTC days before today (a sample belongs to the day of block b+1),
// marking the last recoveryLast of them Scored.
func (r historyReader) summarize(ctx context.Context, h *History, now time.Time) error {
	today := now.UTC().Truncate(24 * time.Hour)
	s := Summary{FirstDay: today.AddDate(0, 0, -scoringDays), LastDay: today.AddDate(0, 0, -1)}
	for _, d := range h.Days {
		if d.Day.Before(s.FirstDay) || !d.Day.Before(today) {
			continue
		}
		s.InSet += d.InSet
		s.Signed += d.Signed()
		s.Absent += d.Absent
		s.Nil += d.Nil
		s.OtherBlock += d.OtherBlock
		s.ProposalsExpected += d.ProposalsExpected
		s.ProposalsMissed += d.ProposalsMissed
		s.Votes += d.Votes
		s.DelaySumMs += d.DelaySumMs
	}
	lo, ok, err := firstFrom(ctx, r.tx, s.FirstDay.UnixMilli(), r.low, r.high)
	if err != nil {
		return err
	}
	if ok {
		hi := r.high
		if after, ok, err := firstFrom(ctx, r.tx, today.UnixMilli(), lo, r.high); err != nil {
			return err
		} else if ok {
			hi = after - 1
		}
		for _, o := range h.Outages {
			if min(o.Last, hi)-max(o.First, lo)+1 >= outageMin {
				s.Outages++
			}
		}
	}
	from := today.AddDate(0, 0, -recoveryDays)
	var samples []int // indices in h.Recoveries
	for i, rec := range h.Recoveries {
		day := rec.RestartTime.Truncate(24 * time.Hour)
		if rec.Pending || day.Before(from) || !day.Before(today) {
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

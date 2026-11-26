package blocks

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
)

// chainBlock is a block of a test chain: its time, its set, who is absent.
type chainBlock struct {
	at     time.Time
	set    string
	absent []Absence
}

// soloSet is a set without valG0Y.
var soloSet = []Member{{Address: val15T, PubKey: "KB", Power: 20}}

// valRot is valG0Y's key after a rotation, in rotSet.
const valRot = "g1rotatedkey"

var rotSet = []Member{{Address: valRot, PubKey: "KR", Power: 10}, {Address: val15T, PubKey: "KB", Power: 20}}

// writeChain stores blocks 1 to len(blocks), each member's precommit 100 ms
// after the median when present.
func writeChain(t *testing.T, s *Store, blocks []chainBlock) {
	t.Helper()
	writeChainFrom(t, s, 1, blocks)
}

// writeChainFrom stores blocks from height from on.
func writeChainFrom(t *testing.T, s *Store, from int64, blocks []chainBlock) {
	t.Helper()
	ctx := context.Background()
	putTestSet(t, s)
	members := map[string][]Member{"SET": testSet, "SOLO": soloSet, "ROT": rotSet}
	for _, hash := range []string{"SOLO", "ROT"} {
		if err := s.PutValset(ctx, hash, members[hash]); err != nil {
			t.Fatal(err)
		}
	}
	recs := make([]Record, len(blocks))
	for i, b := range blocks {
		set := b.set
		if set == "" {
			set = "SET"
		}
		r := Record{Height: from + int64(i), Time: b.at, Proposer: members[set][0].Address, ValsetHash: set, Absences: b.absent}
		for _, m := range members[set] {
			absent := false
			for _, a := range b.absent {
				absent = absent || (a.Address == m.Address && a.Kind == Absent)
			}
			if !absent {
				r.Delays = append(r.Delays, Delay{Address: m.Address, Ms: 100})
			}
		}
		recs[i] = r
	}
	if err := s.Write(ctx, recs); err != nil {
		t.Fatal(err)
	}
}

// chainOf is blocks 1 to n, block h at at(h); absentAt marks valG0Y absent.
func chainOf(n int, at func(h int64) time.Time, absentAt func(h int64) bool) []chainBlock {
	out := make([]chainBlock, n)
	for i := range out {
		h := int64(i + 1)
		out[i].at = at(h)
		if absentAt != nil && absentAt(h) {
			out[i].absent = []Absence{{valG0Y, Absent}}
		}
	}
	return out
}

// secondly is n blocks one second apart from t0; absentAt marks valG0Y absent.
func secondly(n int, absentAt func(h int64) bool) []chainBlock {
	return chainOf(n, func(h int64) time.Time { return t0.Add(time.Duration(h) * time.Second) }, absentAt)
}

// minutelyAfterARestart is the time of block h: one a second up to 20, block
// 21 5 min after block 20, then one a minute, so block 21+1440 is 24 h after
// block 21.
func minutelyAfterARestart(h int64) time.Time {
	if h <= 20 {
		return t0.Add(time.Duration(h) * time.Second)
	}
	return t0.Add(20*time.Second + 5*time.Minute + time.Duration(h-21)*time.Minute)
}

func between(lo, hi int64) func(int64) bool {
	return func(h int64) bool { return h >= lo && h <= hi }
}

func history(t *testing.T, s *Store, now time.Time, keys ...string) History {
	t.Helper()
	h, err := s.ValidatorHistory(context.Background(), keys, now)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestHistorySumsKeysPerDay(t *testing.T) {
	s := newTestStore(t)
	blocks := secondly(4, nil)
	blocks[0].absent = []Absence{{valG0Y, Absent}}
	blocks[1].absent = []Absence{{val15T, Absent}}
	blocks[2].absent = []Absence{{valG0Y, Nil}}
	blocks[3].absent = []Absence{{val15T, OtherBlock}}
	writeChain(t, s, blocks)
	h := history(t, s, t0.Add(48*time.Hour), valG0Y, val15T)
	if len(h.Days) != 1 {
		t.Fatalf("days = %+v, want one", h.Days)
	}
	d := h.Days[0]
	if !d.Day.Equal(t0.Truncate(24*time.Hour)) || d.Absent != 2 || d.Nil != 1 || d.OtherBlock != 1 || d.Votes != 6 || d.InSet != 8 || d.Signed() != 5 {
		t.Errorf("day = %+v (signed %d), want day %v, absent 2, nil 1, other 1, votes 6, in the set 8, signed 5", d, d.Signed(), t0.Truncate(24*time.Hour))
	}
	if d.ProposalsExpected != 4 || d.DelaySumMs != 600 || d.DelayBuckets[2] != 6 {
		t.Errorf("day = %+v, want 4 proposals expected, 600 ms of delays, 6 in the ±250 ms bucket", d)
	}
}

func TestHistoryOutages(t *testing.T) {
	timeAt := func(h int64) time.Time { return t0.Add(time.Duration(h) * time.Second) }
	for _, tc := range []struct {
		name   string
		absent func(int64) bool
		want   []Outage
	}{
		{"9 is no outage", between(1, 9), nil},
		{"10 is one", between(1, 10), []Outage{{First: 1, Last: 10, FirstTime: timeAt(1), EndTime: timeAt(11)}}},
		{"ongoing", between(21, 30), []Outage{{First: 21, Last: 30, FirstTime: timeAt(21), EndTime: timeAt(30), Ongoing: true}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			writeChain(t, s, secondly(30, tc.absent))
			if got := history(t, s, t0.Add(time.Hour), valG0Y).Outages; !reflect.DeepEqual(got, tc.want) {
				t.Errorf("outages = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// restartAfter20 is 40 blocks one second apart but for a 5 min restart gap
// between blocks 20 and 21.
func restartAfter20(absent func(int64) bool) []chainBlock {
	blocks := secondly(40, absent)
	for i := 20; i < 40; i++ {
		blocks[i].at = blocks[i].at.Add(5 * time.Minute)
	}
	return blocks
}

func TestHistoryRecoveries(t *testing.T) {
	timeAt := func(h int64) time.Time {
		at := t0.Add(time.Duration(h) * time.Second)
		if h > 20 {
			at = at.Add(5 * time.Minute)
		}
		return at
	}
	solo := func(lo, hi int64) func(int64) bool { return between(lo, hi) }
	for _, tc := range []struct {
		name    string
		absent  func(int64) bool
		outside func(int64) bool // the heights valG0Y is not in the set for
		want    []Recovery
		outages int
	}{
		{"absent after the gap", between(20, 25), nil, []Recovery{{Restart: 20, RestartTime: timeAt(21), SignedAt: 26, Duration: timeAt(27).Sub(timeAt(21))}}, 0},
		{"signed through it", nil, nil, []Recovery{{Restart: 20, RestartTime: timeAt(21), SignedAt: 20}}, 0},
		{"absent up to b−1, signs b", between(18, 19), nil, []Recovery{{Restart: 20, RestartTime: timeAt(21), SignedAt: 20}}, 0},
		{"10 down up to b−1", between(10, 19), nil, nil, 1},
		{"9 down before it", between(11, 25), nil, []Recovery{{Restart: 20, RestartTime: timeAt(21), SignedAt: 26, Duration: timeAt(27).Sub(timeAt(21))}}, 0},
		{"10 down before it", between(10, 25), nil, nil, 1},
		{"not in the set before it", between(20, 25), solo(1, 19), nil, 0},
		{"not in the set at it", nil, solo(20, 20), nil, 0},
		{"left the set while down", between(20, 25), solo(26, 40), nil, 0},
		{"still down", between(20, 40), nil, []Recovery{{Restart: 20, RestartTime: timeAt(21), Pending: true}}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			blocks := restartAfter20(tc.absent)
			for i := range blocks {
				if tc.outside != nil && tc.outside(int64(i+1)) {
					blocks[i].set, blocks[i].absent = "SOLO", nil
				}
			}
			writeChain(t, s, blocks)
			h := history(t, s, t0.Add(time.Hour), valG0Y)
			if !reflect.DeepEqual(h.Recoveries, tc.want) {
				t.Errorf("recoveries = %+v, want %+v", h.Recoveries, tc.want)
			}
			if len(h.Outages) != tc.outages {
				t.Errorf("outages = %+v, want %d", h.Outages, tc.outages)
			}
		})
	}
}

func TestHistoryRecoveryRunAtTheStoredLow(t *testing.T) {
	// Stored from 15: valG0Y's absences may reach below, so the run holding
	// the gap gives no sample.
	s := newTestStore(t)
	writeChainFrom(t, s, 15, restartAfter20(between(15, 25))[14:])
	h := history(t, s, t0.Add(time.Hour), valG0Y)
	if len(h.Recoveries) != 0 {
		t.Errorf("recoveries = %+v, want none", h.Recoveries)
	}
	if len(h.Outages) != 1 || h.Outages[0].First != 15 {
		t.Errorf("outages = %+v, want one from 15", h.Outages)
	}
}

func TestHistoryOutageAcrossARotation(t *testing.T) {
	// valG0Y rotates to valRot at 20: absent for its last 6 blocks, the new
	// key for its first 6.
	blocks := secondly(40, between(14, 19))
	for i := 19; i < 40; i++ {
		blocks[i].set = "ROT"
		if i < 25 {
			blocks[i].absent = []Absence{{valRot, Absent}}
		}
	}
	s := newTestStore(t)
	writeChain(t, s, blocks)
	h := history(t, s, t0.Add(time.Hour), valG0Y, valRot)
	if len(h.Outages) != 1 || h.Outages[0].First != 14 || h.Outages[0].Last != 25 {
		t.Errorf("outages = %+v, want one from 14 to 25", h.Outages)
	}
}

func TestHistoryRecoveryAcrossARotation(t *testing.T) {
	// valG0Y rotates to valRot at height rotation; the validator is absent
	// from oldFrom on its old key, then on its new key through the restart
	// gap at 20, to 24: one run of the validator's.
	for _, tc := range []struct {
		name              string
		oldFrom, rotation int64
		sampled           bool
		outages           int
		recovering        int64
	}{
		{"17 absences before b, 12 on the old key", 3, 15, false, 1, 0},
		{"5 absences before b, across the rotation", 15, 17, true, 0, 10},
		{"rotated at the gap", 15, 20, true, 0, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			blocks := restartAfter20(nil)
			for i := range blocks {
				h := int64(i + 1)
				switch {
				case h < tc.rotation && h >= tc.oldFrom:
					blocks[i].absent = []Absence{{valG0Y, Absent}}
				case h >= tc.rotation && h <= 24:
					blocks[i].set, blocks[i].absent = "ROT", []Absence{{valRot, Absent}}
				case h >= tc.rotation:
					blocks[i].set = "ROT"
				}
			}
			s := newTestStore(t)
			writeChain(t, s, blocks)
			h := history(t, s, dayAfterT0, valG0Y, valRot)
			sampled := len(h.Recoveries) == 1 && h.Recoveries[0].Restart == 20 && h.Recoveries[0].SignedAt == 25 &&
				h.Recoveries[0].Duration == blocks[25].at.Sub(blocks[20].at)
			if sampled != tc.sampled || len(h.Recoveries) > 1 {
				t.Errorf("recoveries = %+v, want a sample signed again at 25: %v", h.Recoveries, tc.sampled)
			}
			if len(h.Outages) != tc.outages || h.Summary.Outages != int64(tc.outages) {
				t.Errorf("outages = %+v (%d in the window), want %d", h.Outages, h.Summary.Outages, tc.outages)
			}
			if h.Summary.Recovering != tc.recovering {
				t.Errorf("recovering = %d, want %d", h.Summary.Recovering, tc.recovering)
			}
		})
	}
}

func TestHistoryExcludesRecoveryRuns(t *testing.T) {
	s := newTestStore(t)
	writeChain(t, s, restartAfter20(between(20, 35)))
	h := history(t, s, t0.Add(time.Hour), valG0Y)
	if len(h.Recoveries) != 1 || h.Recoveries[0].SignedAt != 36 {
		t.Errorf("recoveries = %+v, want one, signed again at 36", h.Recoveries)
	}
	if len(h.Outages) != 0 {
		t.Errorf("outages = %+v, want none: a recovery run is no outage", h.Outages)
	}
}

func TestHistoryCapsRecoveryRunsAt24Hours(t *testing.T) {
	// Block 21 comes 5 min after block 20, then one block a minute: block
	// 21+1440 is 24 h after block 21, so the cap leaves out heights from 1462.
	for _, tc := range []struct {
		name string
		last int64
		want []int64 // the outage's first and last, none when nil
	}{{"15 past the cap", 1476, []int64{1462, 1476}}, {"9 past the cap", 1470, nil}} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			writeChain(t, s, chainOf(1480, minutelyAfterARestart, between(20, tc.last)))
			h := history(t, s, t0.Add(48*time.Hour), valG0Y)
			var got []int64
			for _, o := range h.Outages {
				got = append(got, o.First, o.Last)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("outages = %+v, want %v", h.Outages, tc.want)
			}
		})
	}
}

func TestHistorySummaryWindow(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	day := func(m time.Month, d int) time.Time { return time.Date(2026, m, d, 12, 0, 0, 0, time.UTC) }
	absent := []Absence{{valG0Y, Absent}}
	s := newTestStore(t)
	writeChain(t, s, []chainBlock{
		{at: day(10, 19), absent: absent},
		{at: day(10, 20), absent: absent},
		{at: day(11, 18), absent: absent},
		{at: day(11, 19), absent: absent},
	})
	sum := history(t, s, now, valG0Y).Summary
	if !sum.FirstDay.Equal(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)) || !sum.LastDay.Equal(time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("window = %v to %v, want 2026-10-20 to 2026-11-18", sum.FirstDay, sum.LastDay)
	}
	if sum.Absent != 2 || sum.InSet != 2 || sum.Signed != 0 {
		t.Errorf("summary = %+v, want the 2 blocks of 2026-10-20 and 2026-11-18, both absent", sum)
	}
}

func TestHistorySummaryOutageInWindow(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	// Block 11 opens the window (2026-10-20 00:00), one block a minute from
	// 2026-10-19 23:50.
	start := time.Date(2026, 10, 19, 23, 50, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		last int64
		want int64
	}{{"9 heights inside", 19, 0}, {"10 heights inside", 20, 1}, {"10 heights, the last 5 today", 0, 0}} {
		t.Run(tc.name, func(t *testing.T) {
			blocks := make([]chainBlock, 30)
			for i := range blocks {
				h := int64(i + 1)
				blocks[i].at = start.Add(time.Duration(i) * time.Minute)
				if h >= 5 && h <= tc.last {
					blocks[i].absent = []Absence{{valG0Y, Absent}}
				}
			}
			if tc.last == 0 {
				// Block 6 is the window's last minute (2026-11-18 23:59),
				// block 7 today's first.
				for i := range blocks {
					blocks[i].at = time.Date(2026, 11, 18, 23, 54, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute)
					if i >= 1 && i < 11 {
						blocks[i].absent = []Absence{{valG0Y, Absent}}
					}
				}
			}
			s := newTestStore(t)
			writeChain(t, s, blocks)
			if got := history(t, s, now, valG0Y).Summary.Outages; got != tc.want {
				t.Errorf("outages in the window = %d, want %d", got, tc.want)
			}
		})
	}
}

// restartsOn builds a chain with one restart on each day, valG0Y absent for
// downs[i] blocks from the block before restart i: each sample is downs[i]
// seconds.
func restartsOn(days []time.Time, downs []int) []chainBlock {
	blocks := []chainBlock{{at: days[0].Add(-time.Hour)}, {at: days[0].Add(-time.Hour + time.Second)}}
	for i, d := range days {
		b := len(blocks) // height of the block before the restart
		for j := 0; j < downs[i]+5; j++ {
			blocks = append(blocks, chainBlock{at: d.Add(time.Duration(j) * time.Second)})
		}
		for h := b; h < b+downs[i]; h++ {
			blocks[h-1].absent = []Absence{{valG0Y, Absent}}
		}
	}
	return blocks
}

func TestHistorySummaryRecovery(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	ago := func(d int) time.Time { return now.Truncate(24*time.Hour).AddDate(0, 0, -d).Add(6 * time.Hour) }
	for _, tc := range []struct {
		name            string
		days            []time.Time
		downs           []int
		samples         int
		median, slowest time.Duration
	}{
		{"last 3 of 4, one too old", []time.Time{ago(181), ago(100), ago(50), ago(20), ago(5)}, []int{90, 40, 30, 10, 20}, 4, 20 * time.Second, 40 * time.Second},
		{"two samples", []time.Time{ago(20), ago(5)}, []int{10, 30}, 2, 20 * time.Second, 30 * time.Second},
		{"the 180th day in, the 181st out", []time.Time{ago(181), ago(180)}, []int{10, 20}, 1, 20 * time.Second, 20 * time.Second},
		{"today out", []time.Time{ago(5), ago(0)}, []int{10, 30}, 1, 10 * time.Second, 10 * time.Second},
		{"none", nil, nil, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			if len(tc.days) > 0 {
				writeChain(t, s, restartsOn(tc.days, tc.downs))
			} else {
				writeChain(t, s, secondly(5, nil))
			}
			sum := history(t, s, now, valG0Y).Summary
			if sum.RecoverySamples != tc.samples || sum.RecoveryMedian != tc.median || sum.RecoverySlowest != tc.slowest {
				t.Errorf("recovery summary = %d samples, median %v, slowest %v; want %d, %v, %v",
					sum.RecoverySamples, sum.RecoveryMedian, sum.RecoverySlowest, tc.samples, tc.median, tc.slowest)
			}
		})
	}
}

func TestHistoryMarksTheScoredRecoveries(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	ago := func(d int) time.Time { return now.Truncate(24*time.Hour).AddDate(0, 0, -d).Add(6 * time.Hour) }
	s := newTestStore(t)
	writeChain(t, s, restartsOn([]time.Time{ago(181), ago(100), ago(50), ago(20), ago(5), ago(0)}, []int{10, 10, 10, 10, 10, 10}))
	var got []bool
	for _, r := range history(t, s, now, valG0Y).Recoveries {
		got = append(got, r.Scored)
	}
	// The last 3 of the 180 complete UTC days before today.
	if want := []bool{false, false, true, true, true, false}; !reflect.DeepEqual(got, want) {
		t.Errorf("scored = %v, want %v", got, want)
	}
}

func TestHistoryUnknownKey(t *testing.T) {
	s := newTestStore(t)
	writeChain(t, s, secondly(5, nil))
	h := history(t, s, dayAfterT0, "g1unknown")
	if len(h.Days)+len(h.Outages)+len(h.Recoveries) != 0 {
		t.Errorf("history of an unknown key = %+v, want empty", h)
	}
	if want := (Summary{FirstDay: h.Summary.FirstDay, LastDay: h.Summary.LastDay}); !reflect.DeepEqual(h.Summary, want) {
		t.Errorf("summary of an unknown key = %+v, want no figure", h.Summary)
	}
}

func TestWindowHistoryEndsAtTheWindowsEnd(t *testing.T) {
	// One block a minute from 2026-11-18 22:50, block 61 today's first; a
	// restart gap at 50 (block 51 comes 11 min after it). valG0Y is absent
	// from 48 to 70: it signs again at 71, after the window. val15T is absent
	// from 30 to 80, already down before the gap: an outage going on at the
	// window's end, 60.
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	at := func(h int64) time.Time {
		if h <= 50 {
			return time.Date(2026, 11, 18, 22, 50, 0, 0, time.UTC).Add(time.Duration(h-1) * time.Minute)
		}
		return now.Truncate(24 * time.Hour).Add(time.Duration(h-61) * time.Minute)
	}
	chain := chainOf(90, at, between(48, 70))
	for h := int64(30); h <= 80; h++ {
		chain[h-1].absent = append(chain[h-1].absent, Absence{val15T, Absent})
	}
	early, late := newTestStore(t), newTestStore(t)
	writeChain(t, early, chain[:61])
	writeChain(t, late, chain)
	w := windowAsOf(t, late, now)
	if w.Last != 60 || !w.Complete {
		t.Fatalf("window = %+v, want complete, its last height 60", w)
	}
	for _, s := range []*Store{early, late} {
		g, err := s.WindowHistory(context.Background(), []string{valG0Y}, w)
		if err != nil {
			t.Fatal(err)
		}
		if want := []Recovery{{Restart: 50, RestartTime: at(51), Pending: true}}; !reflect.DeepEqual(g.Recoveries, want) {
			t.Errorf("valG0Y's recoveries = %+v, want %+v: block 71+1, which times its sample, is after the window", g.Recoveries, want)
		}
		if n := len(g.Days); n == 0 || !g.Days[n-1].Day.Equal(w.LastDay) {
			t.Errorf("valG0Y's days = %+v, want the last %v", g.Days, w.LastDay)
		}
		if sum := g.Summary; sum.Recovering != 13 || sum.RecoverySamples != 0 {
			t.Errorf("valG0Y's summary = %+v, want 13 recovering (48 to 60), no sample", sum)
		}
		b, err := s.WindowHistory(context.Background(), []string{val15T}, w)
		if err != nil {
			t.Fatal(err)
		}
		if want := []Outage{{First: 30, Last: 60, FirstTime: at(30), EndTime: at(61), Ongoing: true}}; !reflect.DeepEqual(b.Outages, want) {
			t.Errorf("val15T's outages = %+v, want %+v: going on at the window's end", b.Outages, want)
		}
	}
}

func TestWindowHistoryNeedsTheWindowsEnd(t *testing.T) {
	s := newTestStore(t)
	writeChain(t, s, secondly(5, nil))
	for _, tc := range []struct {
		name string
		w    ScoringWindow
		want string // a part of the error
	}{
		{"an incomplete window", ScoringWindow{First: 1, Last: 4}, "no last height"},
		{"a window without a height", ScoringWindow{Complete: true}, "no last height"},
		{"its end's next block not stored", ScoringWindow{First: 1, Last: 5, Complete: true}, "block 6"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.WindowHistory(context.Background(), []string{valG0Y}, tc.w)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("WindowHistory(%+v) = %v, want an error naming %q", tc.w, err, tc.want)
			}
		})
	}
}

// dayAfterT0 is a now whose scoring window holds t0's day.
var dayAfterT0 = t0.Add(24 * time.Hour)

func TestSummaryLeavesRecoveryRunsOutOfUptime(t *testing.T) {
	// The restart gap is at 20, b. Every sampled run is a recovery run, the
	// one ending at b−1 before a signed b included.
	for _, tc := range []struct {
		name                     string
		absent                   func(int64) bool
		recovering, signed       int64
		uptime                   float64
		longestAbsence, signedAt int64
	}{
		{"absent from b to 24, and from 30 to 32", func(h int64) bool { return between(20, 24)(h) || between(30, 32)(h) }, 5, 32, 32.0 / 35.0, 3, 25},
		{"absent from 18 to b−1, signs b", between(18, 19), 2, 38, 1, 0, 20},
		{"absent from 18 to b, signs b+1", between(18, 20), 3, 37, 1, 0, 21},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			writeChain(t, s, restartAfter20(tc.absent))
			h := history(t, s, dayAfterT0, valG0Y)
			if len(h.Recoveries) != 1 || h.Recoveries[0].SignedAt != tc.signedAt {
				t.Errorf("recoveries = %+v, want one, signed again at %d", h.Recoveries, tc.signedAt)
			}
			sum := h.Summary
			if sum.Recovering != tc.recovering || sum.Signed != tc.signed || sum.InSet != 40 || sum.LongestAbsence != tc.longestAbsence {
				t.Errorf("summary = %+v, want %d recovering, %d signed, 40 in the set, the longest absence %d",
					sum, tc.recovering, tc.signed, tc.longestAbsence)
			}
			if got, ok := sum.Uptime(); !ok || got != tc.uptime {
				t.Errorf("Uptime() = %v, %v; want %d / (40 − %d) = %v, true", got, ok, tc.signed, tc.recovering, tc.uptime)
			}
		})
	}
}

func TestSummaryUptimeWithNothingCounted(t *testing.T) {
	for _, sum := range []Summary{{}, {InSet: 5, Recovering: 5}} {
		if got, ok := sum.Uptime(); ok {
			t.Errorf("Summary{InSet: %d, Recovering: %d}.Uptime() = %v, true; want false", sum.InSet, sum.Recovering, got)
		}
	}
}

func TestSummaryCapsARecoveryRunAt24Hours(t *testing.T) {
	// valG0Y, absent from the restart gap at 20, signs again at 1520: block
	// 1521 is 25 h after block 21. Its run is recovering up to 1461, 24 h
	// after block 21, and an outage from 1462 to 1519.
	s := newTestStore(t)
	writeChain(t, s, chainOf(1530, minutelyAfterARestart, between(20, 1519)))
	h := history(t, s, t0.Add(72*time.Hour), valG0Y)
	if len(h.Recoveries) != 1 || h.Recoveries[0].Duration != 25*time.Hour {
		t.Fatalf("recoveries = %+v, want one of 25 h", h.Recoveries)
	}
	if sum := h.Summary; sum.Recovering != 1442 || sum.Outages != 1 || sum.LongestAbsence != 58 {
		t.Errorf("summary = %+v, want 1442 recovering (20 to 1461), one outage, the longest absence 58 (1462 to 1519)", sum)
	}
}

func TestSummaryLongestAbsenceCountsShortRuns(t *testing.T) {
	absent := func(h int64) bool { return between(5, 7)(h) || between(15, 21)(h) }
	s := newTestStore(t)
	writeChain(t, s, secondly(30, absent))
	if sum := history(t, s, dayAfterT0, valG0Y).Summary; sum.LongestAbsence != 7 || sum.Outages != 0 {
		t.Errorf("summary = %+v, want the longest absence 7, no outage", sum)
	}
}

func TestSummaryLongestAbsenceIsClippedToTheWindow(t *testing.T) {
	// One block a minute from 2026-10-19 23:30: block 31 opens the window
	// (2026-10-20 00:00); valG0Y is absent from 16 to 55.
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	start := time.Date(2026, 10, 19, 23, 30, 0, 0, time.UTC)
	minutely := func(h int64) time.Time { return start.Add(time.Duration(h-1) * time.Minute) }
	s := newTestStore(t)
	writeChain(t, s, chainOf(60, minutely, between(16, 55)))
	if sum := history(t, s, now, valG0Y).Summary; sum.LongestAbsence != 25 {
		t.Errorf("longest absence = %d, want 25 (31 to 55)", sum.LongestAbsence)
	}
}

func TestSummaryInSetMs(t *testing.T) {
	// valG0Y is out of the set for blocks 1 to 5, in it from 6 to 105, 3 s
	// apart, block 105 at 23:59:57 of the window's last day; block 106 is
	// today's first. val15T is in the set at every height.
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	at := func(h int64) time.Time {
		return now.Truncate(24 * time.Hour).Add(time.Duration(h-106) * 3 * time.Second)
	}
	for _, tc := range []struct {
		name   string
		keys   []string
		stored int
		want   int64
	}{
		{"105 counts its time to today's first block", []string{valG0Y}, 106, 300_000},
		{"105 counts 0 without the next block", []string{valG0Y}, 105, 297_000},
		{"a height counts once for two keys in its set", []string{valG0Y, val15T}, 106, 315_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			blocks := chainOf(tc.stored, at, nil)
			for i := range 5 {
				blocks[i].set = "SOLO"
			}
			s := newTestStore(t)
			writeChain(t, s, blocks)
			if got := history(t, s, now, tc.keys...).Summary.InSetMs; got != tc.want {
				t.Errorf("in the set = %d ms, want %d", got, tc.want)
			}
		})
	}
}

// missedRound0 is the record of a block made by val15T in round 1, round 0
// being valG0Y's.
func missedRound0(h int64, absences ...Absence) Record {
	r := rec(h, absences...)
	r.Proposer, r.Round, r.Rounds = val15T, 1, []string{valG0Y, val15T}
	return r
}

func TestSummaryProposalsCountSignedHeightsOnly(t *testing.T) {
	// Ten round-0 blocks proposed by valG0Y: absent at 3, nil at 5 (signed),
	// for another block at 7.
	round0 := make([]Record, 10)
	for i := range round0 {
		round0[i] = rec(int64(i + 1))
	}
	round0[2].Absences = []Absence{{valG0Y, Absent}}
	round0[4].Absences = []Absence{{valG0Y, Nil}}
	round0[6].Absences = []Absence{{valG0Y, OtherBlock}}
	for _, tc := range []struct {
		name             string
		recs             []Record
		expected, missed int64
		dayExpected      int64 // the day's counter, every height counted
	}{
		{"round-0 blocks", round0, 8, 0, 10},
		{"a late block, signed", []Record{missedRound0(1)}, 1, 1, 1},
		{"a late block, nil", []Record{missedRound0(1, Absence{valG0Y, Nil})}, 1, 1, 1},
		{"a late block, absent", []Record{missedRound0(1, Absence{valG0Y, Absent})}, 0, 0, 1},
		{"a late block, for another block", []Record{missedRound0(1, Absence{valG0Y, OtherBlock})}, 0, 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			putTestSet(t, s)
			writeRecs(t, s, tc.recs)
			h := history(t, s, dayAfterT0, valG0Y)
			if sum := h.Summary; sum.ProposalsExpected != tc.expected || sum.ProposalsMissed != tc.missed {
				t.Errorf("proposals = %d expected, %d missed; want %d, %d", sum.ProposalsExpected, sum.ProposalsMissed, tc.expected, tc.missed)
			}
			if len(h.Days) != 1 || h.Days[0].ProposalsExpected != tc.dayExpected {
				t.Errorf("days = %+v, want one expecting %d proposals", h.Days, tc.dayExpected)
			}
		})
	}
}

func TestSummaryProposalsSumTheKeys(t *testing.T) {
	// Height 1: valG0Y was due to propose round 0, and did. Height 2: val15T
	// was due to propose round 0, and did. Height 3: valG0Y was due to
	// propose round 0 and missed it, then val15T was due to propose round 1
	// and did. Expected: 1 + 1 + 2 = 4; missed: valG0Y's round 0 at 3.
	val15TsBlock := rec(2)
	val15TsBlock.Proposer = val15T
	s := newTestStore(t)
	putTestSet(t, s)
	writeRecs(t, s, []Record{rec(1), val15TsBlock, missedRound0(3)})
	if sum := history(t, s, dayAfterT0, valG0Y, val15T).Summary; sum.ProposalsExpected != 4 || sum.ProposalsMissed != 1 {
		t.Errorf("proposals = %d expected, %d missed; want 4, 1", sum.ProposalsExpected, sum.ProposalsMissed)
	}
}

func TestSummaryClipsRecoveringToTheWindow(t *testing.T) {
	// One block a minute from 2026-10-19 23:30, but block 21 comes 5 min
	// after block 20, at 23:54: block 27 opens the window (2026-10-20
	// 00:00). valG0Y, absent from the restart gap at 20 to 35, is recovering
	// at 9 of the window's heights, 27 to 35.
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	start := time.Date(2026, 10, 19, 23, 30, 0, 0, time.UTC)
	at := func(h int64) time.Time {
		if h <= 20 {
			return start.Add(time.Duration(h-1) * time.Minute)
		}
		return start.Add(time.Duration(h+3) * time.Minute)
	}
	s := newTestStore(t)
	writeChain(t, s, chainOf(40, at, between(20, 35)))
	h := history(t, s, now, valG0Y)
	if len(h.Recoveries) != 1 || h.Recoveries[0].SignedAt != 36 {
		t.Fatalf("recoveries = %+v, want one, signed again at 36", h.Recoveries)
	}
	if h.Summary.Recovering != 9 {
		t.Errorf("recovering = %d, want 9 (27 to 35)", h.Summary.Recovering)
	}
}

func TestSummaryProposalsSkipRestartHeights(t *testing.T) {
	// Block 3 comes 5 min after block 2: height 2 is a restart gap, b.
	for _, tc := range []struct {
		name string
		b    Record
	}{
		{"valG0Y proposes it in round 0", rec(2)},
		{"valG0Y misses its round 0", missedRound0(2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after := rec(3)
			after.Time = after.Time.Add(5 * time.Minute)
			s := newTestStore(t)
			putTestSet(t, s)
			writeRecs(t, s, []Record{rec(1), tc.b, after})
			if sum := history(t, s, dayAfterT0, valG0Y).Summary; sum.ProposalsExpected != 2 || sum.ProposalsMissed != 0 {
				t.Errorf("proposals = %d expected, %d missed; want 2 (heights 1 and 3), 0", sum.ProposalsExpected, sum.ProposalsMissed)
			}
		})
	}
}

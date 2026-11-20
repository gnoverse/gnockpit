package blocks

import (
	"context"
	"reflect"
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

// secondly is n blocks one second apart from t0; absentAt marks valG0Y absent.
func secondly(n int, absentAt func(h int64) bool) []chainBlock {
	out := make([]chainBlock, n)
	for i := range out {
		h := int64(i + 1)
		out[i].at = t0.Add(time.Duration(h) * time.Second)
		if absentAt != nil && absentAt(h) {
			out[i].absent = []Absence{{valG0Y, Absent}}
		}
	}
	return out
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
			const n = 1480
			blocks := make([]chainBlock, n)
			for i := range blocks {
				h := int64(i + 1)
				at := t0.Add(time.Duration(h) * time.Second)
				if h > 20 {
					at = t0.Add(20*time.Second + 5*time.Minute + time.Duration(h-21)*time.Minute)
				}
				blocks[i].at = at
				if h >= 20 && h <= tc.last {
					blocks[i].absent = []Absence{{valG0Y, Absent}}
				}
			}
			s := newTestStore(t)
			writeChain(t, s, blocks)
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
	h := history(t, s, t0.Add(time.Hour), "g1unknown")
	if len(h.Days)+len(h.Outages)+len(h.Recoveries) != 0 {
		t.Errorf("history of an unknown key = %+v, want empty", h)
	}
}

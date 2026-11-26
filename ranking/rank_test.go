package ranking

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
)

// tolerance bounds the float error of a sub-score, weight or score computed
// from exact counts.
const tolerance = 1e-9

// today is the day the tests rank as of; window is its scoring window,
// heights 1,001 to 2,000.
var (
	today  = time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	window = blocks.ScoringWindow{
		FirstDay:  today.AddDate(0, 0, -30),
		LastDay:   today.AddDate(0, 0, -1),
		SpanStart: today.AddDate(0, 0, -180),
		First:     1001,
		Last:      2000,
		Complete:  true,
	}
)

// hoursMs returns h hours of block time, in ms.
func hoursMs(h int64) int64 { return (time.Duration(h) * time.Hour).Milliseconds() }

// flawless returns a summary with every sub-score at 1 and 168 hours in the
// set: every block signed, no outage, back at once after its one restart, no
// proposal missed.
func flawless() blocks.Summary {
	return blocks.Summary{
		InSet:             1000,
		Signed:            1000,
		InSetMs:           hoursMs(168),
		RecoverySamples:   1,
		ProposalsExpected: 100,
	}
}

// operator returns the operator addr with the history h.
func operator(addr string, h blocks.History) Operator {
	return Operator{Address: addr, Name: "name of " + addr, Keys: []string{addr + "-key"}, History: h}
}

// summarized returns the operator addr whose history holds the summary s and
// the s.Outages outages it counts, inside the window.
func summarized(addr string, s blocks.Summary) Operator {
	return operator(addr, blocks.History{Outages: outagesInside(int(s.Outages)), Summary: s})
}

func rankAll(ops ...Operator) Result {
	return Rank(Input{Window: window, Operators: ops})
}

// rankOne ranks the operator alone and returns its entry.
func rankOne(t *testing.T, op Operator) Entry {
	t.Helper()
	r := rankAll(op)
	if len(r.Entries) != 1 {
		t.Fatalf("Rank() holds %d entries, want 1", len(r.Entries))
	}
	return r.Entries[0]
}

// entryOf returns the entry of the operator addr.
func entryOf(t *testing.T, r Result, addr string) Entry {
	t.Helper()
	for _, e := range r.Entries {
		if e.Operator == addr {
			return e
		}
	}
	t.Fatalf("Rank() holds no entry for %s", addr)
	return Entry{}
}

// subOf returns a part's sub-score, failing the test without one.
func subOf(t *testing.T, name string, p Part) float64 {
	t.Helper()
	if p.Sub == nil {
		t.Fatalf("%s.Sub = nil, want a sub-score", name)
	}
	return *p.Sub
}

func near(got, want float64) bool { return math.Abs(got-want) <= tolerance }

// placings returns each entry as "operator status #rank", in the result's
// order.
func placings(r Result) []string {
	out := make([]string, 0, len(r.Entries))
	for _, e := range r.Entries {
		out = append(out, fmt.Sprintf("%s %s #%d", e.Operator, e.Status, e.Rank))
	}
	return out
}

// outagesInside returns n outages of 10 heights each, inside the window.
func outagesInside(n int) []blocks.Outage {
	var out []blocks.Outage
	for i := range n {
		first := window.First + int64(i)*20
		out = append(out, blocks.Outage{First: first, Last: first + 9})
	}
	return out
}

// restartedOnce returns a history with one scored recovery sample of d, and
// s as its summary with that sample's median.
func restartedOnce(s blocks.Summary, d time.Duration) blocks.History {
	s.RecoverySamples, s.RecoveryMedian = 1, d
	restart := today.AddDate(0, 0, -10)
	return blocks.History{
		Recoveries: []blocks.Recovery{{Restart: 1500, RestartTime: restart, SignedAt: 1510, Duration: d, Scored: true}},
		Summary:    s,
	}
}

func TestUptimeMapping(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		inSet, recovering, signed int64
		want                      float64
	}{
		{"under 95 %", 10000, 0, 9000, 0},
		{"95 %", 10000, 0, 9500, 0},
		{"97.45 %", 10000, 0, 9745, 0.5},
		{"99.9 %", 10000, 0, 9990, 1},
		{"100 %", 10000, 0, 10000, 1},
		{"97.45 % with recovery runs left out of both counts", 10100, 100, 9745, 0.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := flawless()
			s.InSet, s.Recovering, s.Signed = tc.inSet, tc.recovering, tc.signed
			e := rankOne(t, summarized("g1op", s))
			if got := subOf(t, "Uptime", e.Uptime); !near(got, tc.want) {
				t.Errorf("Uptime.Sub of %d signed, %d in the set, %d recovering = %v, want %v", tc.signed, tc.inSet, tc.recovering, got, tc.want)
			}
		})
	}
}

func TestOutagesMapping(t *testing.T) {
	for _, tc := range []struct {
		outages int
		want    float64
	}{
		{0, 1},
		{5, 0.5},
		{10, 0},
		{12, 0},
	} {
		t.Run(fmt.Sprintf("%d outages", tc.outages), func(t *testing.T) {
			s := flawless()
			s.Outages = int64(tc.outages)
			e := rankOne(t, summarized("g1op", s))
			if got := subOf(t, "Outages", e.Outages); !near(got, tc.want) {
				t.Errorf("Outages.Sub of %d outages = %v, want %v", tc.outages, got, tc.want)
			}
		})
	}
}

func TestRecoveryMapping(t *testing.T) {
	for _, tc := range []struct {
		median time.Duration
		want   float64
	}{
		{0, 1},
		{10 * time.Minute, 1},
		{35 * time.Minute, 0.83},
		{time.Hour, 0.66},
		{6 * time.Hour, 0.33},
		{15 * time.Hour, 0.165},
		{24 * time.Hour, 0},
		{30 * time.Hour, 0},
	} {
		t.Run(tc.median.String(), func(t *testing.T) {
			e := rankOne(t, operator("g1op", restartedOnce(flawless(), tc.median)))
			if got := subOf(t, "Recovery", e.Recovery); !near(got, tc.want) {
				t.Errorf("Recovery.Sub of a %v median = %v, want %v", tc.median, got, tc.want)
			}
		})
	}
}

func TestProposalsMapping(t *testing.T) {
	for _, tc := range []struct {
		name             string
		missed, expected int64
		want             float64
	}{
		{"0 %", 0, 1000, 1},
		{"2.5 %", 25, 1000, 0.5},
		{"5 %", 50, 1000, 0},
		{"6 %", 60, 1000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := flawless()
			s.ProposalsMissed, s.ProposalsExpected = tc.missed, tc.expected
			e := rankOne(t, summarized("g1op", s))
			if got := subOf(t, "Proposals", e.Proposals); !near(got, tc.want) {
				t.Errorf("Proposals.Sub of %d missed of %d = %v, want %v", tc.missed, tc.expected, got, tc.want)
			}
		})
	}
}

func TestScoreIsTheWeightedSum(t *testing.T) {
	// Every sub-score at 0.5: 97.45 % uptime, 5 outages, 2.5 % of the
	// proposals missed, and a median recovery of 1 h + 5 h × 0.16 / 0.33,
	// rounded to the ms. That rounding moves the recovery sub-score by under
	// 1e-8, and the score by under 1e-6.
	const loose = 1e-6
	s := flawless()
	s.InSet, s.Signed = 10000, 9745
	s.Outages = 5
	s.ProposalsMissed, s.ProposalsExpected = 25, 1000
	h := restartedOnce(s, 12_327_273*time.Millisecond)
	h.Outages = outagesInside(5)
	e := rankOne(t, operator("g1op", h))
	for _, p := range []struct {
		name   string
		part   Part
		weight float64
	}{
		{"Uptime", e.Uptime, 50},
		{"Outages", e.Outages, 20},
		{"Recovery", e.Recovery, 20},
		{"Proposals", e.Proposals, 10},
	} {
		if got := subOf(t, p.name, p.part); math.Abs(got-0.5) > loose {
			t.Errorf("%s.Sub = %v, want 0.5", p.name, got)
		}
		if p.part.Weight != p.weight {
			t.Errorf("%s.Weight = %v, want %v", p.name, p.part.Weight, p.weight)
		}
		if want := *p.part.Sub * p.weight; !near(p.part.Points, want) {
			t.Errorf("%s.Points = %v, want %v (its sub-score × its weight)", p.name, p.part.Points, want)
		}
	}
	if math.Abs(e.Score-50) > loose {
		t.Errorf("Score = %v, want 50", e.Score)
	}
}

func TestMissingRecoveryPassesItsWeight(t *testing.T) {
	// No recovery sample: its 20 spreads over the other 80 in proportion.
	s := flawless()
	s.RecoverySamples = 0
	s.InSet, s.Signed = 10000, 9745                   // 0.5
	s.ProposalsMissed, s.ProposalsExpected = 25, 1000 // 0.5
	e := rankOne(t, summarized("g1op", s))
	if e.Recovery != (Part{}) {
		t.Errorf("Recovery = %+v, want no sub-score, weight or points", e.Recovery)
	}
	for _, p := range []struct {
		name   string
		part   Part
		weight float64
	}{
		{"Uptime", e.Uptime, 62.5},
		{"Outages", e.Outages, 25},
		{"Proposals", e.Proposals, 12.5},
	} {
		if p.part.Weight != p.weight {
			t.Errorf("%s.Weight = %v, want %v", p.name, p.part.Weight, p.weight)
		}
	}
	sum := e.Uptime.Points + e.Outages.Points + e.Recovery.Points + e.Proposals.Points
	if !near(e.Score, sum) || !near(e.Score, 62.5) {
		t.Errorf("Score = %v, want 62.5, the sum of the points (%v)", e.Score, sum)
	}
}

func TestNoProposalDuePassesItsWeight(t *testing.T) {
	// Expected 0: no share to score; its 10 spreads over the other 90.
	s := flawless()
	s.ProposalsExpected = 0
	e := rankOne(t, summarized("g1op", s))
	if e.Proposals != (Part{}) {
		t.Errorf("Proposals = %+v, want no sub-score, weight or points", e.Proposals)
	}
	for _, p := range []struct {
		name   string
		part   Part
		weight float64
	}{
		{"Uptime", e.Uptime, 50 * 100.0 / 90},
		{"Outages", e.Outages, 20 * 100.0 / 90},
		{"Recovery", e.Recovery, 20 * 100.0 / 90},
	} {
		if !near(p.part.Weight, p.weight) {
			t.Errorf("%s.Weight = %v, want %v", p.name, p.part.Weight, p.weight)
		}
	}
	if !near(e.Score, 100) {
		t.Errorf("Score = %v, want 100", e.Score)
	}
}

func TestNoCountedBlockPassesUptimesWeight(t *testing.T) {
	// Every height in the set belongs to a recovery run: no block counts for
	// uptime, and its 50 spreads over the other 50.
	s := flawless()
	s.Recovering, s.Signed = s.InSet, 0
	e := rankOne(t, summarized("g1op", s))
	if e.Uptime != (Part{}) {
		t.Errorf("Uptime = %+v, want no sub-score, weight or points", e.Uptime)
	}
	for _, p := range []struct {
		name   string
		part   Part
		weight float64
	}{
		{"Outages", e.Outages, 40},
		{"Recovery", e.Recovery, 40},
		{"Proposals", e.Proposals, 20},
	} {
		if p.part.Weight != p.weight {
			t.Errorf("%s.Weight = %v, want %v", p.name, p.part.Weight, p.weight)
		}
	}
}

func TestProposalFlagsDisableProposalsForEveryone(t *testing.T) {
	flags := []int64{1500, 1700}
	missing := flawless()
	missing.ProposalsMissed, missing.ProposalsExpected = 60, 1000
	r := Rank(Input{
		Window:        window,
		Operators:     []Operator{summarized("g1a", flawless()), summarized("g1b", missing)},
		ProposalFlags: flags,
	})
	for _, e := range r.Entries {
		if e.Proposals != (Part{}) {
			t.Errorf("%s: Proposals = %+v, want no sub-score, weight or points", e.Operator, e.Proposals)
		}
		if want := 50 * 100.0 / 90; !near(e.Uptime.Weight, want) {
			t.Errorf("%s: Uptime.Weight = %v, want %v", e.Operator, e.Uptime.Weight, want)
		}
		if !near(e.Score, 100) {
			t.Errorf("%s: Score = %v, want 100", e.Operator, e.Score)
		}
	}
	if !reflect.DeepEqual(r.ProposalsDisabled, flags) {
		t.Errorf("ProposalsDisabled = %v, want %v", r.ProposalsDisabled, flags)
	}
}

func TestProposalFlagsLeaveTheMissedShareOutOfTheOrder(t *testing.T) {
	// g1a and g1b are equal on the first four keys and miss 6 % and 10 % of
	// their proposals: a sub-score of 0 for both while proposals count.
	// While they are disabled, counts the self-check cannot trust decide no
	// order: the two share a rank.
	low, high := flawless(), flawless()
	low.ProposalsMissed, low.ProposalsExpected = 60, 1000
	high.ProposalsMissed, high.ProposalsExpected = 100, 1000
	for _, tc := range []struct {
		name  string
		flags []int64
		want  []string
	}{
		{"proposals disabled", []int64{1500}, []string{"g1a ranked #1", "g1b ranked #1"}},
		{"proposals counted", nil, []string{"g1b ranked #1", "g1a ranked #2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Rank(Input{
				Window:        window,
				Operators:     []Operator{summarized("g1a", high), summarized("g1b", low)},
				ProposalFlags: tc.flags,
			})
			if got := placings(r); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Rank() with flags %v = %v, want %v", tc.flags, got, tc.want)
			}
		})
	}
}

func TestRankNeeds168HoursInTheSet(t *testing.T) {
	for _, tc := range []struct {
		name    string
		inSetMs int64
		status  Status
		rank    int
	}{
		{"167 h", hoursMs(167), NotRankedYet, 0},
		{"a ms short of 168 h", hoursMs(168) - 1, NotRankedYet, 0},
		{"168 h", hoursMs(168), Ranked, 1},
		{"200 h", hoursMs(200), Ranked, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := flawless()
			s.InSetMs = tc.inSetMs
			e := rankOne(t, summarized("g1op", s))
			if e.Status != tc.status || e.Rank != tc.rank {
				t.Errorf("Status, Rank = %q, %d, want %q, %d", e.Status, e.Rank, tc.status, tc.rank)
			}
			if want := float64(tc.inSetMs) / float64(time.Hour.Milliseconds()); e.HoursInSet != want {
				t.Errorf("HoursInSet = %v, want %v", e.HoursInSet, want)
			}
		})
	}
}

func TestRankOrder(t *testing.T) {
	// Each pair starts flawless and is equal on the keys before the one
	// named, and the worse operator is better on a later key: only the named
	// key decides. The better operator's address sorts last, so the address
	// cannot decide. Absences cover the longest absence, and an outage's 10
	// heights.
	for _, tc := range []struct {
		key           string
		better, worse func(*blocks.Summary)
	}{
		{
			// 99.89 % signed in short runs, about 99.9 points, against
			// 99.90 % signed in one outage, 98 points.
			key: "score",
			better: func(s *blocks.Summary) {
				s.InSet, s.Signed, s.LongestAbsence = 10000, 9989, 9
			},
			worse: func(s *blocks.Summary) {
				s.InSet, s.Signed, s.Outages, s.LongestAbsence = 10000, 9990, 1, 10
			},
		},
		{
			// 99.98 against 99.96: both 100.0 to one decimal, and equal on
			// raw uptime, so only the unrounded score puts the better first.
			key: "score, unrounded",
			better: func(s *blocks.Summary) {
				s.InSet, s.Signed, s.LongestAbsence = 10000, 9990, 5
				s.ProposalsMissed, s.ProposalsExpected = 1, 10000
			},
			worse: func(s *blocks.Summary) {
				s.InSet, s.Signed, s.LongestAbsence = 10000, 9990, 1
				s.ProposalsMissed, s.ProposalsExpected = 2, 10000
			},
		},
		{
			key:    "raw uptime",
			better: func(s *blocks.Summary) { s.InSet, s.Signed, s.LongestAbsence = 10000, 9995, 5 },
			worse:  func(s *blocks.Summary) { s.InSet, s.Signed, s.LongestAbsence = 10000, 9991, 1 },
		},
		{
			// Both score exactly 98 from other sub-scores: 99.9 % signed (an
			// uptime sub-score of 1) and one outage, against 99.704 % signed
			// (exactly 0.96) in short runs. Summed as floats, the second's
			// score comes out above 98.
			key: "raw uptime, equal scores from other sub-scores",
			better: func(s *blocks.Summary) {
				s.InSet, s.Signed, s.Outages, s.LongestAbsence = 10010, 10000, 1, 10
			},
			worse: func(s *blocks.Summary) {
				s.InSet, s.Signed, s.LongestAbsence = 12500, 12463, 9
			},
		},
		{
			key: "longest absence",
			better: func(s *blocks.Summary) {
				s.InSet, s.Signed, s.LongestAbsence, s.RecoveryMedian = 10000, 9990, 3, 5*time.Minute
			},
			worse: func(s *blocks.Summary) {
				s.InSet, s.Signed, s.LongestAbsence, s.RecoveryMedian = 10000, 9990, 7, 0
			},
		},
		{
			// Both miss 5 % or more of their proposals: that sub-score is 0
			// for both, and the scores are equal.
			key: "median recovery",
			better: func(s *blocks.Summary) {
				s.RecoveryMedian, s.ProposalsMissed, s.ProposalsExpected = time.Minute, 100, 1000
			},
			worse: func(s *blocks.Summary) {
				s.RecoveryMedian, s.ProposalsMissed, s.ProposalsExpected = 5*time.Minute, 60, 1000
			},
		},
		{
			key:    "missed-proposal share",
			better: func(s *blocks.Summary) { s.ProposalsMissed, s.ProposalsExpected = 60, 1000 },
			worse:  func(s *blocks.Summary) { s.ProposalsMissed, s.ProposalsExpected = 100, 1000 },
		},
	} {
		t.Run(tc.key, func(t *testing.T) {
			better, worse := flawless(), flawless()
			tc.better(&better)
			tc.worse(&worse)
			r := rankAll(summarized("g1a", worse), summarized("g1b", better))
			want := []string{"g1b ranked #1", "g1a ranked #2"}
			if got := placings(r); !reflect.DeepEqual(got, want) {
				t.Errorf("Rank() = %v, want %v", got, want)
			}
		})
	}
}

// subStepScores returns the exact scores 98 + 10^-18 and 98, which round to
// the same float64: only an exact comparison tells them apart.
func subStepScores(t *testing.T) (higher, lower *big.Rat) {
	t.Helper()
	higher, ok := new(big.Rat).SetString("98.000000000000000001")
	if !ok {
		t.Fatal("cannot parse the higher score")
	}
	lower = big.NewRat(98, 1)
	if nearest(higher) != nearest(lower) {
		t.Fatalf("nearest floats %v and %v, want them equal", nearest(higher), nearest(lower))
	}
	return higher, lower
}

func TestRankOrderComparesExactScores(t *testing.T) {
	// The higher exact score comes first, against its worse raw uptime.
	higher, lower := subStepScores(t)
	a := scored{Entry: Entry{Operator: "g1a", Score: nearest(higher), Inputs: Inputs{InSet: 1000, Signed: 999}}, exact: higher}
	b := scored{Entry: Entry{Operator: "g1b", Score: nearest(lower), Inputs: Inputs{InSet: 1000, Signed: 1000}}, exact: lower}
	order := rankOrder(false)
	if got := order(a, b); got >= 0 {
		t.Errorf("rankOrder(false)(higher, lower) = %d, want < 0: the higher exact score first", got)
	}
	if got := order(b, a); got <= 0 {
		t.Errorf("rankOrder(false)(lower, higher) = %d, want > 0: the higher exact score first", got)
	}
}

func TestExcludedOrder(t *testing.T) {
	// The excluded are listed by exact score, higher first, then by operator
	// address.
	higher, lower := subStepScores(t)
	entry := func(addr string, exact *big.Rat) scored {
		return scored{Entry: Entry{Operator: addr, Status: Excluded, Score: nearest(exact)}, exact: exact}
	}
	for _, tc := range []struct {
		name          string
		first, second scored
	}{
		{"the higher exact score, its address sorting last", entry("g1b", higher), entry("g1a", lower)},
		{"equal scores, by address", entry("g1a", lower), entry("g1b", lower)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := excludedOrder(tc.first, tc.second); got >= 0 {
				t.Errorf("excludedOrder(%s, %s) = %d, want < 0", tc.first.Operator, tc.second.Operator, got)
			}
			if got := excludedOrder(tc.second, tc.first); got <= 0 {
				t.Errorf("excludedOrder(%s, %s) = %d, want > 0", tc.second.Operator, tc.first.Operator, got)
			}
		})
	}
}

func TestRankSharesFullTies(t *testing.T) {
	// g1a and g1c are equal on all five keys: the same uptime over other
	// counts, neither with a recovery sample. g1b scores less.
	tied := flawless()
	tied.RecoverySamples = 0
	tied.InSet, tied.Signed = 1000, 999
	other := tied
	other.InSet, other.Signed = 2000, 1998
	lower := tied
	lower.Signed = 990
	r := rankAll(summarized("g1c", tied), summarized("g1b", lower), summarized("g1a", other))
	want := []string{"g1a ranked #1", "g1c ranked #1", "g1b ranked #3"}
	if got := placings(r); !reflect.DeepEqual(got, want) {
		t.Errorf("Rank() = %v, want %v", got, want)
	}
	for addr, peers := range map[string][]string{"g1a": {"g1c"}, "g1c": {"g1a"}, "g1b": {}} {
		if got := entryOf(t, r, addr).TiedWith; !reflect.DeepEqual(got, peers) {
			t.Errorf("%s: TiedWith = %v, want %v", addr, got, peers)
		}
	}
}

func TestRankSharesFullMarksWithAndWithoutProposalsDue(t *testing.T) {
	// Full marks score exactly 100 whatever the redistribution: g1a has no
	// proposal due, its weights 500/9, 200/9 and 200/9.
	idle := flawless()
	idle.ProposalsExpected = 0
	r := rankAll(summarized("g1a", idle), summarized("g1b", flawless()))
	want := []string{"g1a ranked #1", "g1b ranked #1"}
	if got := placings(r); !reflect.DeepEqual(got, want) {
		t.Errorf("Rank() = %v, want %v", got, want)
	}
	for _, e := range r.Entries {
		if e.Score != 100 {
			t.Errorf("%s: Score = %v, want exactly 100", e.Operator, e.Score)
		}
	}
}

func TestRankPutsNoRecoverySampleLast(t *testing.T) {
	// Without a sample, recovery's weight spreads over the other full marks:
	// both score 100, equal on the keys before recovery.
	unsampled := flawless()
	unsampled.RecoverySamples = 0
	r := rankAll(summarized("g1a", unsampled), summarized("g1b", flawless()))
	if a, b := entryOf(t, r, "g1a").Score, entryOf(t, r, "g1b").Score; a != b {
		t.Fatalf("scores %v and %v, want them equal", a, b)
	}
	want := []string{"g1b ranked #1", "g1a ranked #2"}
	if got := placings(r); !reflect.DeepEqual(got, want) {
		t.Errorf("Rank() = %v, want %v", got, want)
	}
}

func TestRankSkipsExcludedOperators(t *testing.T) {
	excluded := summarized("g1x", flawless())
	excluded.Excluded = true
	good, fair := flawless(), flawless()
	good.Signed = 995
	fair.Signed = 990
	r := rankAll(excluded, summarized("g1b", fair), summarized("g1a", good))
	want := []string{"g1a ranked #1", "g1b ranked #2", "g1x excluded #0"}
	if got := placings(r); !reflect.DeepEqual(got, want) {
		t.Errorf("Rank() = %v, want %v", got, want)
	}
	if got := entryOf(t, r, "g1x").Score; !near(got, 100) {
		t.Errorf("g1x: Score = %v, want 100: scored the same way", got)
	}
}

func TestRankExcludedUnder168Hours(t *testing.T) {
	// An excluded operator is never ranked, whatever its hours in the set.
	s := flawless()
	s.InSetMs = hoursMs(100)
	excluded := summarized("g1x", s)
	excluded.Excluded = true
	if e := rankOne(t, excluded); e.Status != Excluded {
		t.Errorf("Status = %q, want %q", e.Status, Excluded)
	}
}

func TestRankEntriesOrder(t *testing.T) {
	// Ranked in order, then the excluded by score, then those not ranked yet
	// by hours in the set, longest first. Within each group the address
	// sorts the other way.
	good, fair := flawless(), flawless()
	fair.Signed = 990
	short, shorter := flawless(), flawless()
	short.InSetMs, shorter.InSetMs = hoursMs(150), hoursMs(100)
	excludedGood, excludedFair := summarized("g1e2", good), summarized("g1e1", fair)
	excludedGood.Excluded, excludedFair.Excluded = true, true
	r := rankAll(
		summarized("g1w1", shorter),
		excludedFair,
		summarized("g1r1", fair),
		summarized("g1w2", short),
		excludedGood,
		summarized("g1r2", good),
	)
	want := []string{
		"g1r2 ranked #1",
		"g1r1 ranked #2",
		"g1e2 excluded #0",
		"g1e1 excluded #0",
		"g1w2 not_ranked_yet #0",
		"g1w1 not_ranked_yet #0",
	}
	if got := placings(r); !reflect.DeepEqual(got, want) {
		t.Errorf("Rank() = %v, want %v", got, want)
	}
}

func TestRankInputs(t *testing.T) {
	at := func(day int) time.Time { return today.AddDate(0, 0, day) }
	s := blocks.Summary{
		InSet: 1000, Recovering: 20, Signed: 950, Nil: 5, Absent: 40, OtherBlock: 10,
		LongestAbsence: 7, InSetMs: hoursMs(200),
		ProposalsExpected: 100, ProposalsMissed: 2,
		RecoverySamples: 4, RecoveryMedian: 90 * time.Second,
	}
	h := blocks.History{
		Recoveries: []blocks.Recovery{
			{Restart: 100, RestartTime: at(-170), SignedAt: 101, Duration: time.Minute},
			{Restart: 200, RestartTime: at(-120), SignedAt: 210, Duration: 2 * time.Minute, Scored: true},
			{Restart: 1100, RestartTime: at(-25), SignedAt: 1100, Scored: true},
			{Restart: 1500, RestartTime: at(-10), SignedAt: 1530, Duration: 90 * time.Second, Scored: true},
			{Restart: 2010, RestartTime: at(0), Pending: true},
		},
		Summary: s,
	}
	e := rankOne(t, operator("g1op", h))
	medianMs := int64(90000)
	want := Inputs{
		InSet: 1000, Recovering: 20, Signed: 950, Nil: 5, Absent: 40, OtherBlock: 10,
		Outages:        []Run{},
		LongestAbsence: 7,
		Recovery: []Sample{
			{RestartHeight: 200, RestartTime: at(-120), DurationMs: 120000},
			{RestartHeight: 1100, RestartTime: at(-25), DurationMs: 0},
			{RestartHeight: 1500, RestartTime: at(-10), DurationMs: 90000},
		},
		RecoveryMedianMs:  &medianMs,
		ProposalsExpected: 100,
		ProposalsMissed:   2,
	}
	if !reflect.DeepEqual(e.Inputs, want) {
		t.Errorf("Inputs = %+v, want %+v", e.Inputs, want)
	}
	type identity struct {
		operator, name string
		keys           []string
		hoursInSet     float64
	}
	got := identity{e.Operator, e.Name, e.Keys, e.HoursInSet}
	if wantID := (identity{"g1op", "name of g1op", []string{"g1op-key"}, 200}); !reflect.DeepEqual(got, wantID) {
		t.Errorf("operator, name, keys, hours in the set = %+v, want %+v", got, wantID)
	}
}

func TestRankInputsWithoutARecoverySample(t *testing.T) {
	s := flawless()
	s.RecoverySamples = 0
	e := rankOne(t, summarized("g1op", s))
	if e.Inputs.RecoveryMedianMs != nil || !reflect.DeepEqual(e.Inputs.Recovery, []Sample{}) {
		t.Errorf("RecoveryMedianMs, Recovery = %v, %v, want nil, []", e.Inputs.RecoveryMedianMs, e.Inputs.Recovery)
	}
}

func TestRankInputsListTheCountedOutages(t *testing.T) {
	// The window holds heights 1,001 to 2,000. An outage counts once 10 of
	// its heights are inside it; Summary.Outages is the blocks package's
	// count of the same rule.
	end := today.AddDate(0, 0, -5)
	for _, tc := range []struct {
		name        string
		first, last int64
		counted     int64
	}{
		{"before the window", 100, 200, 0},
		{"straddling its start, 9 heights inside", 992, 1009, 0},
		{"straddling its start, 10 heights inside", 991, 1010, 1},
		{"inside it", 1500, 1509, 1},
		{"straddling its end, 10 heights inside", 1991, 2005, 1},
		{"after it", 2001, 2050, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outage := blocks.Outage{First: tc.first, Last: tc.last, FirstTime: end.Add(-time.Minute), EndTime: end}
			s := flawless()
			s.Outages = tc.counted
			e := rankOne(t, operator("g1op", blocks.History{Outages: []blocks.Outage{outage}, Summary: s}))
			if got := int64(len(e.Inputs.Outages)); got != s.Outages {
				t.Fatalf("len(Inputs.Outages) = %d, want Summary.Outages = %d", got, s.Outages)
			}
			want := []Run{}
			if tc.counted > 0 {
				want = []Run{{First: tc.first, Last: tc.last, FirstTime: outage.FirstTime, EndTime: end}}
			}
			if !reflect.DeepEqual(e.Inputs.Outages, want) {
				t.Errorf("Inputs.Outages = %+v, want %+v", e.Inputs.Outages, want)
			}
		})
	}
}

func TestResultJSONKeepsMissingValuesAsNull(t *testing.T) {
	// Without a recovery sample, its sub-score and the median are null, never
	// left out.
	s := flawless()
	s.RecoverySamples = 0
	e := rankOne(t, summarized("g1op", s))
	part, err := json.Marshal(e.Recovery)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"sub":null,"weight":0,"points":0}`; string(part) != want {
		t.Errorf("Recovery JSON = %s, want %s", part, want)
	}
	inputs, err := json.Marshal(e.Inputs)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(inputs, &fields); err != nil {
		t.Fatal(err)
	}
	if got, ok := fields["recovery_median_ms"]; !ok || string(got) != "null" {
		t.Errorf("Inputs JSON recovery_median_ms = %s (present %v), want null", got, ok)
	}
}

func TestResultJSON(t *testing.T) {
	s := flawless()
	s.InSet, s.Signed, s.Absent = 10000, 9990, 10
	s.Outages, s.LongestAbsence = 1, 10
	h := restartedOnce(s, 90*time.Second)
	h.Recoveries[0].Restart, h.Recoveries[0].RestartTime = 1200, time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	h.Outages = []blocks.Outage{{
		First:     1500,
		Last:      1509,
		FirstTime: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 11, 10, 12, 0, 40, 0, time.UTC),
	}}
	got, err := json.MarshalIndent(rankAll(operator("g1op", h)), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	const want = `{
  "formula": 1,
  "first_day": "2026-10-26",
  "last_day": "2026-11-24",
  "first_height": 1001,
  "last_height": 2000,
  "entries": [
    {
      "operator": "g1op",
      "name": "name of g1op",
      "keys": [
        "g1op-key"
      ],
      "status": "ranked",
      "rank": 1,
      "hours_in_set": 168,
      "score": 98,
      "uptime": {
        "sub": 1,
        "weight": 50,
        "points": 50
      },
      "outages": {
        "sub": 0.9,
        "weight": 20,
        "points": 18
      },
      "recovery": {
        "sub": 1,
        "weight": 20,
        "points": 20
      },
      "proposals": {
        "sub": 1,
        "weight": 10,
        "points": 10
      },
      "tied_with": [],
      "inputs": {
        "in_set": 10000,
        "recovering": 0,
        "signed": 9990,
        "nil": 0,
        "absent": 10,
        "other_block": 0,
        "outages": [
          {
            "first": 1500,
            "last": 1509,
            "first_time": "2026-11-10T12:00:00Z",
            "end_time": "2026-11-10T12:00:40Z"
          }
        ],
        "longest_absence": 10,
        "recovery": [
          {
            "restart_height": 1200,
            "restart_time": "2026-11-01T08:00:00Z",
            "duration_ms": 90000
          }
        ],
        "recovery_median_ms": 90000,
        "proposals_expected": 100,
        "proposals_missed": 0
      }
    }
  ],
  "proposals_disabled": []
}`
	if string(got) != want {
		t.Errorf("JSON = %s\nwant %s", got, want)
	}
}

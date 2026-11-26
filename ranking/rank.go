// Package ranking scores and orders gno.land validator operators by the
// agreed scoring formula. Rank is pure code over each operator's block
// history, with no database access; Gather reads that input from the block
// store, and Readiness tells whether the store holds the history the input
// needs.
package ranking

import (
	"cmp"
	"math/big"
	"slices"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
)

// FormulaVersion names the formula's constants: the weights, the mappings
// and the 168-hour minimum here, and the blocks package's scoring constants
// (the outage minimum, the restart gap, the windows, the recovery cap). A
// change to any of them bumps it.
const FormulaVersion = 1

// The formula's constants, fixed in code: a change bumps FormulaVersion.
const (
	maxScore = 100 // the score with every sub-score at 1

	// Each sub-score's points out of maxScore, before the weights of the
	// sub-scores without data are spread over the others.
	uptimeWeight    = 50
	outagesWeight   = 20
	recoveryWeight  = 20
	proposalsWeight = 10

	minInSet = 168 * time.Hour // block time in the set within the window that ranks an operator
)

// The sub-scores' mappings from their raw values, in exact fractions: Rank
// computes every score exactly, so two operators whose scores are equal tie
// on the order's first key.
var (
	// uptimeMapping maps signed over blocks in the set, recovery runs left
	// out of both.
	uptimeMapping = mapping{{ratio{95, 100}, whole(0)}, {ratio{999, 1000}, whole(1)}}
	// outagesMapping maps the outages the window counts.
	outagesMapping = mapping{{whole(0), whole(1)}, {whole(10), whole(0)}}
	// recoveryMapping maps the median recovery time, in ms.
	recoveryMapping = mapping{
		{whole((10 * time.Minute).Milliseconds()), whole(1)},
		{whole(time.Hour.Milliseconds()), ratio{66, 100}},
		{whole((6 * time.Hour).Milliseconds()), ratio{33, 100}},
		{whole((24 * time.Hour).Milliseconds()), whole(0)},
	}
	// proposalsMapping maps missed over expected proposals.
	proposalsMapping = mapping{{whole(0), whole(1)}, {ratio{5, 100}, whole(0)}}
)

// Operator is a validator operator to score.
type Operator struct {
	Address  string         // the operator, or the signing key no chain record links to one
	Name     string         // its moniker
	Keys     []string       // its signing keys
	Excluded bool           // on the deployment's exclusion list: scored, left out of the order
	History  blocks.History // its keys' history as the window ends (blocks.Store.WindowHistory)
}

// Input is what Rank scores. Rank checks none of these invariants; its
// caller holds them:
//   - Operators are the operators in the validator set at Window.Last;
//   - every height of ProposalFlags lies within Window.First to Window.Last;
//   - Window.Complete holds: Window.Last is the window's last height;
//   - every operator's History is computed for this window: each Summary's
//     FirstDay and LastDay equal Window's.
type Input struct {
	Window        blocks.ScoringWindow
	Operators     []Operator // those in the validator set at Window.Last
	ProposalFlags []int64    // the window's heights whose proposals cannot be checked: one disables the proposals sub-score
}

// Status is an entry's place in the ranking.
type Status string

// An operator is excluded when the deployment's exclusion list names it,
// else ranked once it has 168 hours of block time in the set within the
// window.
const (
	Ranked       Status = "ranked"
	NotRankedYet Status = "not_ranked_yet"
	Excluded     Status = "excluded" // scored the same way, left out of the order
)

// Part is a sub-score's part of a score.
type Part struct {
	Sub    *float64 `json:"sub"`    // 0 to 1; nil without data
	Weight float64  `json:"weight"` // the points a sub-score of 1 gives, the weights of the sub-scores without data spread in
	Points float64  `json:"points"` // Sub × Weight, computed exactly
}

// Sample is a recovery sample: how long the operator took to sign again
// after a restart.
type Sample struct {
	RestartHeight int64     `json:"restart_height"` // b: block b+1 came more than blocks.RestartGap after block b
	RestartTime   time.Time `json:"restart_time"`   // block b+1's
	DurationMs    int64     `json:"duration_ms"`
}

// Run is an outage: a run of blocks.OutageMin or more consecutive absences.
type Run struct {
	First     int64     `json:"first"`
	Last      int64     `json:"last"`
	FirstTime time.Time `json:"first_time"`
	EndTime   time.Time `json:"end_time"` // block Last+1's time; a run going on at the window's end ends at its last height
}

// Inputs are an operator's raw figures, from which its sub-scores and its
// place in the order follow.
type Inputs struct {
	InSet             int64    `json:"in_set"`
	Recovering        int64    `json:"recovering"` // heights of recovery runs, left out of uptime
	Signed            int64    `json:"signed"`     // a nil vote signs
	Nil               int64    `json:"nil"`
	Absent            int64    `json:"absent"`
	OtherBlock        int64    `json:"other_block"` // precommits for another block, counted absent
	Outages           []Run    `json:"outages"`     // the outages the window counts, each whole
	LongestAbsence    int64    `json:"longest_absence"`
	Recovery          []Sample `json:"recovery"`           // the last 3 samples within the 180 complete UTC days before the day ranked
	RecoveryMedianMs  *int64   `json:"recovery_median_ms"` // their median, to the ms, truncated; nil without a sample
	ProposalsExpected int64    `json:"proposals_expected"`
	ProposalsMissed   int64    `json:"proposals_missed"`
}

// Entry is an operator's score and place in the ranking.
type Entry struct {
	Operator   string   `json:"operator"`
	Name       string   `json:"name"`
	Keys       []string `json:"keys"`
	Status     Status   `json:"status"`
	Rank       int      `json:"rank"`         // 0 unless ranked
	HoursInSet float64  `json:"hours_in_set"` // block time in the set within the window
	Score      float64  `json:"score"`        // unrounded: the float64 nearest the exact score the order compares
	Uptime     Part     `json:"uptime"`
	Outages    Part     `json:"outages"`
	Recovery   Part     `json:"recovery"`
	Proposals  Part     `json:"proposals"`
	TiedWith   []string `json:"tied_with"` // the operators sharing its rank
	Inputs     Inputs   `json:"inputs"`
}

// Result is a ranking over a window.
type Result struct {
	Formula     int    `json:"formula"`   // FormulaVersion
	FirstDay    string `json:"first_day"` // YYYY-MM-DD, UTC
	LastDay     string `json:"last_day"`
	FirstHeight int64  `json:"first_height"`
	LastHeight  int64  `json:"last_height"`
	// Entries lists the ranked operators in order, then the excluded by
	// score, then those not ranked yet by hours in the set, longest first;
	// the operator address orders equals.
	Entries           []Entry `json:"entries"`
	ProposalsDisabled []int64 `json:"proposals_disabled"` // Input.ProposalFlags
}

// Rank scores every operator of in over its window and orders the ranked
// ones: by score, raw uptime, longest absence, median recovery time and
// missed-proposal share, the last left out while the proposals are
// disabled. Operators equal on every key share a rank. Scores are computed
// and compared exactly; Score and the parts carry their nearest float64.
// The median recovery time, for its sub-score and its key, is
// Inputs.RecoveryMedianMs: to the ms, truncated.
func Rank(in Input) Result {
	proposalsDisabled := len(in.ProposalFlags) > 0
	var ranked, excluded, waiting []scored
	for _, op := range in.Operators {
		e := score(op, in.Window, proposalsDisabled)
		switch e.Status {
		case Ranked:
			ranked = append(ranked, e)
		case Excluded:
			excluded = append(excluded, e)
		case NotRankedYet:
			waiting = append(waiting, e)
		}
	}
	placeRanked(ranked, rankOrder(proposalsDisabled))
	slices.SortFunc(excluded, excludedOrder)
	slices.SortFunc(waiting, func(a, b scored) int {
		return cmp.Or(cmp.Compare(b.HoursInSet, a.HoursInSet), cmp.Compare(a.Operator, b.Operator))
	})
	entries := make([]Entry, 0, len(in.Operators))
	for _, group := range [][]scored{ranked, excluded, waiting} {
		for _, e := range group {
			entries = append(entries, e.Entry)
		}
	}
	return Result{
		Formula:           FormulaVersion,
		FirstDay:          in.Window.FirstDay.Format(time.DateOnly),
		LastDay:           in.Window.LastDay.Format(time.DateOnly),
		FirstHeight:       in.Window.First,
		LastHeight:        in.Window.Last,
		Entries:           entries,
		ProposalsDisabled: listOf(in.ProposalFlags),
	}
}

// ---- Scores

// scored is an entry and its exact score, which the order compares: two
// scores equal as fractions can differ as sums of float64 points.
type scored struct {
	Entry
	exact *big.Rat
}

// score returns an operator's entry over the window, its rank left unset.
func score(op Operator, w blocks.ScoringWindow, proposalsDisabled bool) scored {
	in := inputsOf(op.History, w)
	inSetMs := op.History.Summary.InSetMs
	e := Entry{
		Operator:   op.Address,
		Name:       op.Name,
		Keys:       listOf(op.Keys),
		Status:     statusOf(op.Excluded, inSetMs),
		HoursInSet: float64(inSetMs) / float64(time.Hour.Milliseconds()),
		TiedWith:   []string{},
		Inputs:     in,
	}
	p, exact := parts(
		weighted{uptimeSub(in), uptimeWeight},
		weighted{outagesSub(in), outagesWeight},
		weighted{recoverySub(in), recoveryWeight},
		weighted{proposalsSub(in, proposalsDisabled), proposalsWeight},
	)
	e.Uptime, e.Outages, e.Recovery, e.Proposals = p[0], p[1], p[2], p[3]
	e.Score = nearest(exact)
	return scored{Entry: e, exact: exact}
}

func statusOf(excluded bool, inSetMs int64) Status {
	switch {
	case excluded:
		return Excluded
	case inSetMs >= minInSet.Milliseconds():
		return Ranked
	default:
		return NotRankedYet
	}
}

func uptimeSub(in Inputs) *big.Rat {
	u, ok := uptime(in)
	if !ok {
		return nil
	}
	return uptimeMapping.at(u.rat())
}

func outagesSub(in Inputs) *big.Rat {
	return outagesMapping.at(whole(int64(len(in.Outages))).rat())
}

func recoverySub(in Inputs) *big.Rat {
	if in.RecoveryMedianMs == nil {
		return nil
	}
	return recoveryMapping.at(whole(*in.RecoveryMedianMs).rat())
}

// proposalsSub returns nil while the proposals are disabled, and for an
// operator with no proposal due: it has no proposing to score.
func proposalsSub(in Inputs, disabled bool) *big.Rat {
	share, ok := missedShare(in)
	if disabled || !ok {
		return nil
	}
	return proposalsMapping.at(share.rat())
}

// weighted is a sub-score, nil without data, and its weight before
// redistribution.
type weighted struct {
	sub    *big.Rat
	weight int64
}

// parts returns each sub-score's part and the exact score, their points'
// sum: the weights of the sub-scores without data are spread over the others
// in proportion to their weights.
func parts(subs ...weighted) ([]Part, *big.Rat) {
	var present int64
	for _, s := range subs {
		if s.sub != nil {
			present += s.weight
		}
	}
	out := make([]Part, len(subs))
	total := new(big.Rat)
	for i, s := range subs {
		if s.sub == nil {
			continue
		}
		weight := big.NewRat(s.weight*maxScore, present)
		points := new(big.Rat).Mul(s.sub, weight)
		total.Add(total, points)
		out[i] = Part{Sub: ptr(nearest(s.sub)), Weight: nearest(weight), Points: nearest(points)}
	}
	return out, total
}

// anchor is a point of a sub-score's mapping: the sub-score at a raw value.
type anchor struct{ raw, sub ratio }

// mapping maps a raw value to its sub-score: anchors ascending by raw value,
// linear between two, flat past either end.
type mapping []anchor

// at returns raw's sub-score, exactly.
func (m mapping) at(raw *big.Rat) *big.Rat {
	if raw.Cmp(m[0].raw.rat()) <= 0 {
		return m[0].sub.rat()
	}
	for i := 1; i < len(m); i++ {
		lo, hi := m[i-1], m[i]
		if raw.Cmp(hi.raw.rat()) < 0 {
			// lo.sub + (hi.sub − lo.sub) × (raw − lo.raw) / (hi.raw − lo.raw)
			sub := new(big.Rat).Sub(raw, lo.raw.rat())
			sub.Mul(sub, new(big.Rat).Sub(hi.sub.rat(), lo.sub.rat()))
			sub.Quo(sub, new(big.Rat).Sub(hi.raw.rat(), lo.raw.rat()))
			return sub.Add(sub, lo.sub.rat())
		}
	}
	return m[len(m)-1].sub.rat()
}

// nearest returns the float64 nearest r.
func nearest(r *big.Rat) float64 {
	f, _ := r.Float64()
	return f
}

// ---- Order

// ratio is an exact fraction, a count over a positive count. The order
// compares two by cross products, never as floats, so an equality between
// two operators is exact.
type ratio struct{ num, den int64 }

// whole returns n as a ratio.
func whole(n int64) ratio { return ratio{n, 1} }

func (r ratio) compare(s ratio) int { return cmp.Compare(r.num*s.den, s.num*r.den) }

func (r ratio) rat() *big.Rat { return big.NewRat(r.num, r.den) }

// uptime returns the raw uptime: signed over the blocks in the set, recovery
// runs left out of both. False, and 0, when no block counts.
func uptime(in Inputs) (ratio, bool) {
	counted := in.InSet - in.Recovering
	if counted <= 0 {
		return ratio{0, 1}, false
	}
	return ratio{in.Signed, counted}, true
}

// missedShare returns the missed over the expected proposals. False, and 0,
// when none is expected.
func missedShare(in Inputs) (ratio, bool) {
	if in.ProposalsExpected <= 0 {
		return ratio{0, 1}, false
	}
	return ratio{in.ProposalsMissed, in.ProposalsExpected}, true
}

// rankOrder returns the order of ranked entries by the formula's five keys,
// the better first: 0 when two share a rank. While the proposals are
// disabled, the missed-proposal share is left out: counts the self-check
// cannot trust decide no order either.
func rankOrder(proposalsDisabled bool) func(a, b scored) int {
	return func(a, b scored) int {
		uptimeA, _ := uptime(a.Inputs)
		uptimeB, _ := uptime(b.Inputs)
		byShare := 0
		if !proposalsDisabled {
			shareA, _ := missedShare(a.Inputs)
			shareB, _ := missedShare(b.Inputs)
			byShare = shareA.compare(shareB)
		}
		return cmp.Or(
			b.exact.Cmp(a.exact),
			uptimeB.compare(uptimeA),
			cmp.Compare(a.Inputs.LongestAbsence, b.Inputs.LongestAbsence),
			compareRecovery(a.Inputs.RecoveryMedianMs, b.Inputs.RecoveryMedianMs),
			byShare,
		)
	}
}

// compareRecovery orders two median recovery times, the shorter first, and
// no sample after any sample.
func compareRecovery(a, b *int64) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	return cmp.Compare(*a, *b)
}

// excludedOrder orders the excluded entries: by exact score, higher first,
// then by operator address.
func excludedOrder(a, b scored) int {
	return cmp.Or(b.exact.Cmp(a.exact), cmp.Compare(a.Operator, b.Operator))
}

// placeRanked sorts the ranked entries by order, the operator address
// ordering those sharing a rank, and sets each one's rank (1, 1, 3) and the
// operators it shares it with.
func placeRanked(ranked []scored, order func(a, b scored) int) {
	slices.SortFunc(ranked, func(a, b scored) int {
		return cmp.Or(order(a, b), cmp.Compare(a.Operator, b.Operator))
	})
	for start := 0; start < len(ranked); {
		end := start + 1
		for end < len(ranked) && order(ranked[start], ranked[end]) == 0 {
			end++
		}
		shared := ranked[start:end]
		for i := range shared {
			shared[i].Rank = start + 1
			for _, peer := range shared {
				if peer.Operator != shared[i].Operator {
					shared[i].TiedWith = append(shared[i].TiedWith, peer.Operator)
				}
			}
		}
		start = end
	}
}

// ---- Inputs

// inputsOf returns a history's raw figures over the window.
func inputsOf(h blocks.History, w blocks.ScoringWindow) Inputs {
	s := h.Summary
	in := Inputs{
		InSet:             s.InSet,
		Recovering:        s.Recovering,
		Signed:            s.Signed,
		Nil:               s.Nil,
		Absent:            s.Absent,
		OtherBlock:        s.OtherBlock,
		Outages:           countedOutages(h.Outages, w),
		LongestAbsence:    s.LongestAbsence,
		Recovery:          scoredSamples(h.Recoveries),
		ProposalsExpected: s.ProposalsExpected,
		ProposalsMissed:   s.ProposalsMissed,
	}
	if s.RecoverySamples > 0 {
		median := s.RecoveryMedian.Milliseconds()
		in.RecoveryMedianMs = &median
	}
	return in
}

// countedOutages returns the outages the window counts, by the rule
// Summary.Outages counts them: blocks.OutageMin or more of their heights
// inside it.
func countedOutages(outages []blocks.Outage, w blocks.ScoringWindow) []Run {
	out := []Run{}
	for _, o := range outages {
		if min(o.Last, w.Last)-max(o.First, w.First)+1 < blocks.OutageMin {
			continue
		}
		out = append(out, Run{First: o.First, Last: o.Last, FirstTime: o.FirstTime, EndTime: o.EndTime})
	}
	return out
}

// scoredSamples returns the recovery samples the median takes, the
// history's Scored ones, ascending.
func scoredSamples(recoveries []blocks.Recovery) []Sample {
	out := []Sample{}
	for _, r := range recoveries {
		if !r.Scored {
			continue
		}
		out = append(out, Sample{RestartHeight: r.Restart, RestartTime: r.RestartTime, DurationMs: r.Duration.Milliseconds()})
	}
	return out
}

// listOf returns a copy of s that encodes as a JSON array, [] when empty.
func listOf[T any](s []T) []T { return append(make([]T, 0, len(s)), s...) }

func ptr(v float64) *float64 { return &v }

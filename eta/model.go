package eta

import (
	"cmp"
	"math"
	"slices"
	"sort"
	"time"
)

const (
	// Stride is the spacing of the sampled heights: the model works on the
	// blocks at multiples of Stride.
	Stride int64 = 100
	// paceBlocks is the span the pace is measured over: short enough to follow
	// a change of block-time regime, long enough to smooth out slow rounds.
	paceBlocks int64 = 3000
	// marginLookback bounds the past predictions the margin learns from.
	// Shorter lookbacks under-cover at multi-day horizons on gnoland-1.
	marginLookback = 30 * 24 * time.Hour
	// minWindows is the fewest past predictions a horizon needs for a margin.
	minWindows = 30
	// maxTrailPoints caps the estimates of a trail.
	maxTrailPoints = 200
)

// quantileLevels are the probabilities of the error quantiles kept per
// horizon: the 95% range runs from the first to the last, the 80% range from
// the second to the fourth.
var quantileLevels = [5]float64{0.025, 0.10, 0.50, 0.90, 0.975}

// Pace is a block rate measured over a span of blocks.
type Pace struct {
	SecondsPerBlock float64
	OverBlocks      int64
}

// Duration returns how long blocks take at this pace, saturating at the
// longest time.Duration.
func (p Pace) Duration(blocks int64) time.Duration {
	return seconds(p.SecondsPerBlock * float64(blocks))
}

// TimeRange is the interval a time falls in with a given probability.
type TimeRange struct {
	From, To time.Time
}

// HeightRange is the interval a height falls in with a given probability.
type HeightRange struct {
	From, To int64
}

// Estimate is when Target is expected, as seen from block From: the ETA at
// the pace of the blocks before From, and the ranges the actual time fell in
// for 80% and 95% of past predictions at the same distance. The ranges are nil
// when the history holds too few past predictions.
type Estimate struct {
	From    Sample
	Target  int64
	Pace    Pace
	ETA     time.Time
	Range80 *TimeRange
	Range95 *TimeRange
}

// HeightEstimate is the height expected at time At, as seen from block From.
type HeightEstimate struct {
	From    Sample
	At      time.Time
	Pace    Pace
	Height  int64
	Range80 *HeightRange
	Range95 *HeightRange
}

// horizonErrors holds, for predictions blocks ahead, the quantiles at
// quantileLevels of how many seconds the actual time came after (positive) or
// before (negative) the predicted one.
type horizonErrors struct {
	blocks int64
	q      [5]float64
}

// Model estimates from a fixed set of block samples. It is immutable and safe
// for concurrent use.
type Model struct {
	grid     []Sample // samples at multiples of Stride, by ascending height
	horizons []horizonErrors
}

// NewModel builds a model from samples; those off the Stride grid are ignored.
// The margin learns from the predictions the model would have made over the
// marginLookback before the newest sample.
func NewModel(samples []Sample) *Model {
	m := &Model{}
	for _, s := range samples {
		if s.Height%Stride == 0 {
			m.grid = append(m.grid, s)
		}
	}
	slices.SortFunc(m.grid, func(a, b Sample) int { return cmp.Compare(a.Height, b.Height) })
	m.horizons = m.marginTable()
	return m
}

// Len returns the number of samples the model holds.
func (m *Model) Len() int { return len(m.grid) }

// First returns the oldest sample, which bounds how far back the model sees.
func (m *Model) First() (Sample, bool) {
	if len(m.grid) == 0 {
		return Sample{}, false
	}
	return m.grid[0], true
}

// Pace returns the pace of the paceBlocks (or slightly more) before at.
func (m *Model) Pace(at Sample) (Pace, bool) {
	g, ok := m.atOrBelow(at.Height - paceBlocks)
	if !ok {
		return Pace{}, false
	}
	return paceBetween(g, at), true
}

// PaceSince returns the pace from the last sample at or before t up to tip.
func (m *Model) PaceSince(tip Sample, t time.Time) (Pace, bool) {
	g, ok := m.AsOf(t)
	if !ok || g.Height >= tip.Height {
		return Pace{}, false
	}
	return paceBetween(g, tip), true
}

// AsOf returns the last sample at or before t: the newest block the model
// would have seen at t.
func (m *Model) AsOf(t time.Time) (Sample, bool) {
	i := sort.Search(len(m.grid), func(i int) bool { return m.grid[i].Time.After(t) }) - 1
	if i < 0 {
		return Sample{}, false
	}
	return m.grid[i], true
}

// Estimate returns when target is expected as seen from block from. It is
// not ok when target is already reached or the pace cannot be measured.
func (m *Model) Estimate(from Sample, target int64) (Estimate, bool) {
	blocks := target - from.Height
	p, ok := m.Pace(from)
	if blocks <= 0 || !ok {
		return Estimate{}, false
	}
	e := Estimate{From: from, Target: target, Pace: p}
	e.ETA = from.Time.Add(p.Duration(blocks))
	if q, ok := m.quantiles(blocks); ok {
		e.Range80 = &TimeRange{From: e.ETA.Add(seconds(q[1])), To: e.ETA.Add(seconds(q[3]))}
		e.Range95 = &TimeRange{From: e.ETA.Add(seconds(q[0])), To: e.ETA.Add(seconds(q[4]))}
	}
	return e, true
}

// HeightAt returns the height expected at t as seen from block from. It is
// not ok when t is not after from or the pace cannot be measured.
func (m *Model) HeightAt(from Sample, t time.Time) (HeightEstimate, bool) {
	p, ok := m.Pace(from)
	if !t.After(from.Time) || !ok || p.SecondsPerBlock <= 0 {
		return HeightEstimate{}, false
	}
	blocks := t.Sub(from.Time).Seconds() / p.SecondsPerBlock
	// A chain running late by err seconds is err/pace blocks short at t.
	heightIf := func(errSecs float64) int64 {
		return from.Height + int64(math.Round(blocks-errSecs/p.SecondsPerBlock))
	}
	h := HeightEstimate{From: from, At: t, Pace: p, Height: heightIf(0)}
	if q, ok := m.quantiles(int64(math.Round(blocks))); ok {
		h.Range80 = &HeightRange{From: heightIf(q[3]), To: heightIf(q[1])}
		h.Range95 = &HeightRange{From: heightIf(q[4]), To: heightIf(q[0])}
	}
	return h, true
}

// Trail returns how the estimate for target evolved: as seen from the last
// sample at or before since, then from each later sample below target, thinned
// to maxTrailPoints and always ending on the newest. Its ranges come from the
// current margin.
func (m *Model) Trail(since time.Time, target int64) []Estimate {
	start := sort.Search(len(m.grid), func(i int) bool { return m.grid[i].Time.After(since) }) - 1
	end := sort.Search(len(m.grid), func(i int) bool { return m.grid[i].Height >= target })
	if start < 0 || end <= start {
		return nil
	}
	step := 1
	if n := end - start; n > maxTrailPoints {
		step = (n - 1 + maxTrailPoints - 2) / (maxTrailPoints - 1)
	}
	var out []Estimate
	add := func(i int) {
		if e, ok := m.Estimate(m.grid[i], target); ok {
			out = append(out, e)
		}
	}
	for i := start; i < end-1; i += step {
		add(i)
	}
	add(end - 1)
	return out
}

// quantiles returns the error quantiles for a prediction blocks ahead:
// interpolated between the horizons that bracket it, scaled from the nearest
// horizon outside them. It is not ok when no horizon has a margin.
func (m *Model) quantiles(blocks int64) ([5]float64, bool) {
	var q [5]float64
	hs := m.horizons
	if len(hs) == 0 {
		return q, false
	}
	scale := func(h horizonErrors) [5]float64 {
		f := float64(blocks) / float64(h.blocks)
		for i, v := range h.q {
			q[i] = v * f
		}
		return q
	}
	if blocks <= hs[0].blocks {
		return scale(hs[0]), true
	}
	for i := 1; i < len(hs); i++ {
		if blocks <= hs[i].blocks {
			a, b := hs[i-1], hs[i]
			f := float64(blocks-a.blocks) / float64(b.blocks-a.blocks)
			for k := range q {
				q[k] = a.q[k] + f*(b.q[k]-a.q[k])
			}
			return q, true
		}
	}
	return scale(hs[len(hs)-1]), true
}

// marginTable replays the pace predictor over the lookback: for each horizon
// with at least minWindows past predictions, the quantiles of their errors.
func (m *Model) marginTable() []horizonErrors {
	if len(m.grid) == 0 {
		return nil
	}
	cutoff := m.grid[len(m.grid)-1].Time.Add(-marginLookback)
	first := sort.Search(len(m.grid), func(i int) bool { return !m.grid[i].Time.Before(cutoff) })
	// The pace each sample in the lookback would have predicted at.
	type origin struct {
		from Sample
		pace float64
	}
	var origins []origin
	for _, g := range m.grid[first:] {
		if p, ok := m.Pace(g); ok {
			origins = append(origins, origin{g, p.SecondsPerBlock})
		}
	}
	var table []horizonErrors
	for _, k := range horizonStrides(len(m.grid)) {
		blocks := k * Stride
		var errs []float64
		for _, o := range origins {
			if end, ok := m.timeAt(o.from.Height + blocks); ok {
				errs = append(errs, end.Sub(o.from.Time).Seconds()-o.pace*float64(blocks))
			}
		}
		if len(errs) < minWindows {
			continue
		}
		slices.Sort(errs)
		h := horizonErrors{blocks: blocks}
		for i, l := range quantileLevels {
			h.q[i] = errs[int(math.Round(l*float64(len(errs)-1)))]
		}
		table = append(table, h)
	}
	return table
}

// horizonStrides lists the horizons, in strides, the margin is kept for: 1, 2,
// 3, 5, 7, 10, 20, … below n.
func horizonStrides(n int) []int64 {
	var out []int64
	for mult := int64(1); ; mult *= 10 {
		for _, b := range []int64{1, 2, 3, 5, 7} {
			if b*mult >= int64(n) {
				return out
			}
			out = append(out, b*mult)
		}
	}
}

// timeAt returns the time of the sample at exactly height.
func (m *Model) timeAt(height int64) (time.Time, bool) {
	i, ok := slices.BinarySearchFunc(m.grid, height, func(s Sample, h int64) int { return cmp.Compare(s.Height, h) })
	if !ok {
		return time.Time{}, false
	}
	return m.grid[i].Time, true
}

// atOrBelow returns the last sample at or below height.
func (m *Model) atOrBelow(height int64) (Sample, bool) {
	i := sort.Search(len(m.grid), func(i int) bool { return m.grid[i].Height > height }) - 1
	if i < 0 {
		return Sample{}, false
	}
	return m.grid[i], true
}

func paceBetween(from, to Sample) Pace {
	n := to.Height - from.Height
	return Pace{SecondsPerBlock: to.Time.Sub(from.Time).Seconds() / float64(n), OverBlocks: n}
}

// seconds converts s seconds to a duration, saturating at the longest and
// shortest time.Duration: converting an out-of-range float is undefined in Go
// and wraps on some platforms.
func seconds(s float64) time.Duration {
	ns := s * float64(time.Second)
	switch {
	case ns >= math.MaxInt64:
		return math.MaxInt64
	case ns <= math.MinInt64:
		return math.MinInt64
	}
	return time.Duration(ns)
}

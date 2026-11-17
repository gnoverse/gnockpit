package eta

import (
	"math"
	"testing"
	"time"
)

// series returns n grid samples, heights Stride to n*Stride from start, where
// the blocks of stride i (ending at height (i+1)*Stride) take bt(i) seconds each.
func series(start time.Time, n int, bt func(i int) float64) []Sample {
	out := make([]Sample, 0, n)
	at := start
	for i := range n {
		if i > 0 {
			at = at.Add(time.Duration(bt(i) * float64(Stride) * float64(time.Second)))
		}
		out = append(out, Sample{Height: int64(i+1) * Stride, Time: at})
	}
	return out
}

func constant(s float64) func(int) float64 { return func(int) float64 { return s } }

// stridesPerDay at 3.5 s/block: 86400 / 3.5 / 100 ≈ 247.
const stridesPerDay = 247

func near(a, b time.Time) bool { return a.Sub(b).Abs() <= time.Millisecond }

func TestEstimateConstantPace(t *testing.T) {
	samples := series(t0, 40*stridesPerDay, constant(3.5))
	m := NewModel(samples)
	tip := Sample{Height: samples[len(samples)-1].Height + 42, Time: samples[len(samples)-1].Time.Add(147 * time.Second)}

	e, ok := m.Estimate(tip, tip.Height+24000)
	if !ok {
		t.Fatal("Estimate() not ok with 40 days of history")
	}
	if math.Abs(e.Pace.SecondsPerBlock-3.5) > 1e-9 {
		t.Errorf("pace = %v s/block, want 3.5", e.Pace.SecondsPerBlock)
	}
	if want := tip.Time.Add(24000 * 3500 * time.Millisecond); !near(e.ETA, want) {
		t.Errorf("ETA = %v, want %v", e.ETA, want)
	}
	// A perfectly regular chain never missed: both ranges collapse on the ETA.
	if e.Range80 == nil || e.Range95 == nil {
		t.Fatal("ranges missing with 40 days of history")
	}
	for _, r := range []*TimeRange{e.Range80, e.Range95} {
		if !near(r.From, e.ETA) || !near(r.To, e.ETA) {
			t.Errorf("range = %v..%v, want both ends on the ETA %v", r.From, r.To, e.ETA)
		}
	}
}

func TestPaceFollowsRecentBlocks(t *testing.T) {
	// 3.0 s/block, then 4.0 s/block for the last 5000 blocks: the pace is
	// measured over the last paceBlocks, so it reads 4.0.
	n := 20 * stridesPerDay
	samples := series(t0, n, func(i int) float64 {
		if i >= n-50 {
			return 4.0
		}
		return 3.0
	})
	tip := samples[len(samples)-1]
	p, ok := NewModel(samples).Pace(tip)
	if !ok {
		t.Fatal("Pace() not ok")
	}
	if math.Abs(p.SecondsPerBlock-4.0) > 1e-9 || p.OverBlocks != paceBlocks {
		t.Errorf("Pace() = %+v, want 4.0 s/block over %d blocks", p, paceBlocks)
	}
}

func TestRangesWidenWithHorizon(t *testing.T) {
	// Block time alternates between 3 s and 4 s every 3000 blocks: the pace
	// predictor misses by more the further it looks, and the ranges must say so.
	samples := series(t0, 40*stridesPerDay, func(i int) float64 {
		if (i/30)%2 == 0 {
			return 3.0
		}
		return 4.0
	})
	m := NewModel(samples)
	tip := samples[len(samples)-1]

	var prev80 time.Duration
	for _, blocks := range []int64{1000, 6000, 20000} {
		e, ok := m.Estimate(tip, tip.Height+blocks)
		if !ok || e.Range80 == nil || e.Range95 == nil {
			t.Fatalf("Estimate(+%d) = %+v, %v; want ranges", blocks, e, ok)
		}
		w80 := e.Range80.To.Sub(e.Range80.From)
		if w80 <= prev80 {
			t.Errorf("80%% width at +%d blocks = %v, want wider than %v", blocks, w80, prev80)
		}
		prev80 = w80
		if e.Range95.From.After(e.Range80.From) || e.Range95.To.Before(e.Range80.To) {
			t.Errorf("+%d blocks: 95%% range %v..%v does not contain the 80%% range %v..%v",
				blocks, e.Range95.From, e.Range95.To, e.Range80.From, e.Range80.To)
		}
	}
}

func TestMarginLooksBackThirtyDays(t *testing.T) {
	// Erratic blocks more than 31 days ago, perfectly regular since: the margin
	// learns only from the last 30 days, so the ranges collapse on the ETA.
	n := 60 * stridesPerDay
	samples := series(t0, n, func(i int) float64 {
		if i < n-31*stridesPerDay && i%2 == 0 {
			return 5.0
		}
		if i < n-31*stridesPerDay {
			return 2.0
		}
		return 3.5
	})
	m := NewModel(samples)
	tip := samples[len(samples)-1]
	e, ok := m.Estimate(tip, tip.Height+6000)
	if !ok || e.Range80 == nil {
		t.Fatalf("Estimate() = %+v, %v; want ranges", e, ok)
	}
	if !near(e.Range80.From, e.ETA) || !near(e.Range80.To, e.ETA) {
		t.Errorf("80%% range = %v..%v, want it collapsed on %v (old erratic blocks out of the lookback)",
			e.Range80.From, e.Range80.To, e.ETA)
	}
}

func TestNoRangesWithoutHistory(t *testing.T) {
	// One day of samples: a pace, but too few past windows for a margin at a
	// three-day horizon... and at any horizon below minWindows windows.
	samples := series(t0, 32, constant(3.5))
	m := NewModel(samples)
	tip := samples[len(samples)-1]
	e, ok := m.Estimate(tip, tip.Height+1000)
	if !ok {
		t.Fatal("Estimate() not ok with a measurable pace")
	}
	if e.Range80 != nil || e.Range95 != nil {
		t.Errorf("ranges = %v, %v; want none from %d samples", e.Range80, e.Range95, len(samples))
	}
	if _, ok := NewModel(samples[:10]).Estimate(samples[9], samples[9].Height+100); ok {
		t.Error("Estimate() ok without paceBlocks of history, want not ok")
	}
}

func TestHeightAt(t *testing.T) {
	samples := series(t0, 40*stridesPerDay, constant(3.5))
	m := NewModel(samples)
	tip := samples[len(samples)-1]

	h, ok := m.HeightAt(tip, tip.Time.Add(3500*time.Second))
	if !ok {
		t.Fatal("HeightAt() not ok")
	}
	if h.Height != tip.Height+1000 {
		t.Errorf("HeightAt(+3500s) = %d, want %d", h.Height, tip.Height+1000)
	}
	if h.Range80 == nil || h.Range80.From != h.Height || h.Range80.To != h.Height {
		t.Errorf("80%% range = %+v, want collapsed on %d", h.Range80, h.Height)
	}
}

func TestAsOfAndTrail(t *testing.T) {
	samples := series(t0, 40*stridesPerDay, constant(3.5))
	m := NewModel(samples)
	tip := samples[len(samples)-1]
	target := tip.Height + 24000

	since := samples[len(samples)-500].Time.Add(time.Minute)
	g, ok := m.AsOf(since)
	if !ok || g != samples[len(samples)-500] {
		t.Errorf("AsOf(%v) = %+v, %v; want the latest sample at or before it, %+v", since, g, ok, samples[len(samples)-500])
	}
	if _, ok := m.AsOf(t0.Add(-time.Second)); ok {
		t.Error("AsOf() before the first sample is ok, want not ok")
	}

	trail := m.Trail(since, target)
	if len(trail) == 0 || len(trail) > maxTrailPoints {
		t.Fatalf("Trail() has %d points, want 1..%d", len(trail), maxTrailPoints)
	}
	if trail[0].From != g || trail[len(trail)-1].From != tip {
		t.Errorf("Trail() runs %v..%v, want %v..%v", trail[0].From, trail[len(trail)-1].From, g, tip)
	}
	want := tip.Time.Add(24000 * 3500 * time.Millisecond)
	for _, e := range trail {
		if !near(e.ETA, want) {
			t.Errorf("trail point from %d: ETA %v, want %v at a constant pace", e.From.Height, e.ETA, want)
			break
		}
	}
	// Points at or past the target are not estimates of it.
	if tr := m.Trail(since, samples[len(samples)-100].Height); tr[len(tr)-1].From.Height >= samples[len(samples)-100].Height {
		t.Errorf("Trail() to a reached height ends at %d, want before it", tr[len(tr)-1].From.Height)
	}
}

func TestPaceSince(t *testing.T) {
	n := 20 * stridesPerDay
	samples := series(t0, n, func(i int) float64 {
		if i >= n-stridesPerDay {
			return 4.0
		}
		return 3.0
	})
	m := NewModel(samples)
	tip := samples[len(samples)-1]

	p, ok := m.PaceSince(tip, tip.Time.Add(-time.Hour))
	if !ok || math.Abs(p.SecondsPerBlock-4.0) > 1e-9 {
		t.Errorf("PaceSince(1h) = %+v, %v; want 4.0 s/block", p, ok)
	}
	all, ok := m.PaceSince(tip, samples[0].Time)
	wantAll := tip.Time.Sub(samples[0].Time).Seconds() / float64(tip.Height-samples[0].Height)
	if !ok || math.Abs(all.SecondsPerBlock-wantAll) > 1e-9 {
		t.Errorf("PaceSince(first) = %+v, %v; want %v s/block", all, ok, wantAll)
	}
	if _, ok := m.PaceSince(tip, samples[0].Time.Add(-time.Hour)); ok {
		t.Error("PaceSince() before the history is ok, want not ok")
	}
}

func TestQuantilesInterpolate(t *testing.T) {
	m := &Model{horizons: []horizonErrors{
		{blocks: 1000, q: [5]float64{-10, -4, 0, 4, 10}},
		{blocks: 3000, q: [5]float64{-30, -12, 0, 8, 30}},
	}}
	tests := []struct {
		blocks int64
		want   [5]float64
	}{
		{500, [5]float64{-5, -2, 0, 2, 5}},      // below the first horizon: scaled
		{2000, [5]float64{-20, -8, 0, 6, 20}},   // between: linear
		{6000, [5]float64{-60, -24, 0, 16, 60}}, // beyond the last: scaled
		{3000, [5]float64{-30, -12, 0, 8, 30}},  // on a horizon
	}
	for _, tc := range tests {
		got, ok := m.quantiles(tc.blocks)
		if !ok || got != tc.want {
			t.Errorf("quantiles(%d) = %v, %v; want %v", tc.blocks, got, ok, tc.want)
		}
	}
	if _, ok := (&Model{}).quantiles(1000); ok {
		t.Error("quantiles() without horizons is ok, want not ok")
	}
}

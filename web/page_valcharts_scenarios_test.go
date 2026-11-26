//go:build page

package web

import (
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
)

// valChartsFirstDay and valChartsDayCount are monthNetwork's chart
// range: from its first block's day to the report's today, 2026-11-18.
var valChartsFirstDay = utc2026(time.October, 20, 0, 0)

const valChartsDayCount = 30

// valChartsDayIndex is the place of day (YYYY-MM-DD) on monthNetwork's
// chart range.
func valChartsDayIndex(t *testing.T, day string) int {
	t.Helper()
	at, err := time.Parse(time.DateOnly, day)
	if err != nil {
		t.Fatalf("parse day %q: %v", day, err)
	}
	return int(at.Sub(valChartsFirstDay) / (24 * time.Hour))
}

// valChartsBravoUptime is the fill of each of bravo's days in
// monthNetwork: green, but where the day says otherwise.
func valChartsBravoUptime(t *testing.T) []string {
	t.Helper()
	fills := make([]string, valChartsDayCount)
	for i := range fills {
		fills[i] = greenColor
	}
	for day, fill := range map[string]string{
		"2026-10-21": yellowColor, // 96.80 % signed
		"2026-10-22": yellowColor, // 95.83 %
		"2026-10-23": redColor,    // 93.61 %
		"2026-10-24": borderColor, // no block: not in the set
		"2026-10-25": yellowColor, // 98.89 %
		"2026-11-03": yellowColor, // 99.72 %
		"2026-11-04": redColor,    // 0.00 %
		"2026-11-10": yellowColor, // 98.94 %
		"2026-11-18": redColor,    // 90.60 %
	} {
		fills[valChartsDayIndex(t, day)] = fill
	}
	return fills
}

// valChartsSince is the healthy network whose block history begins with a
// lone first block at first: the chain halted there until three days
// before pageNow. Its charts run from first's day to the report's today.
func valChartsSince(first time.Time) *scenario {
	sc := healthyNetwork()
	set := members(healthyValidators()...)
	sc.seed = func(t *testing.T, s *blocks.Store) {
		putBlocks(t, s, func(int64) time.Time { return first }, "HEALTHY", set, 1, 1, nil)
		putBlocks(t, s, healthyBlockTime, "HEALTHY", set, 2, pageHeight, healthyAbsences())
	}
	sc.fill.FromTime = first
	return sc
}

// valChartsEdgesStep is the time between two blocks of valChartsEdges: 960
// blocks a day.
const valChartsEdgesStep = 90 * time.Second

// valChartsEdges is the healthy network on a block history of alpha, bravo
// and charlie at 10 each, one block every valChartsEdgesStep from
// 2026-10-28 04:00: 20,000 blocks in the scoring window, then today's.
// Alpha is absent from the block at noon of each day of the window, 21 in
// all: it signs 99.895 % of the window, 959 of a whole day's 960, 99.896 %.
// Bravo proposes 594 blocks of 2026-11-10 and misses 31 more: 4.96 % of the
// 625 it was expected for.
func valChartsEdges(t *testing.T) *scenario {
	t.Helper()
	c := newHistoryChain(t, valAlpha, valBravo, valCharlie).
		every(valChartsEdgesStep, utc2026(time.October, 28, 4, 0), utc2026(time.November, 18, 11, 58).Add(30*time.Second))
	for day := utc2026(time.October, 28, 12, 0); day.Before(utc2026(time.November, 18, 0, 0)); day = day.AddDate(0, 0, 1) {
		c.absent(valAlpha, day, day)
	}
	proposes := utc2026(time.November, 10, 0, 0)
	misses := proposes.Add(594 * valChartsEdgesStep)
	c.propose(valBravo, proposes, misses.Add(-valChartsEdgesStep)).
		missProposals(valBravo, misses, misses.Add(30*valChartsEdgesStep))
	sc := healthyNetwork()
	sc.seed = func(t *testing.T, s *blocks.Store) { c.write(t, s, "EDGES") }
	sc.fill = blocks.Progress{ChainID: pageChainID, From: 1, To: int64(len(c.blocks)), FromTime: c.blocks[0].at, Complete: true}
	return sc
}

// valChartsRecoveryEdges is the healthy network on a block history of
// alpha, bravo and charlie at 10 each, a block every historyCadence, the
// chain restarting at 2026-11-14 08:00 after two hours down: bravo signs
// again 600.4 s after, charlie 86,399.6 s after.
func valChartsRecoveryEdges(t *testing.T) *scenario {
	t.Helper()
	down := utc2026(time.November, 14, 6, 0)
	restart := utc2026(time.November, 14, 8, 0)
	bravoBack := restart.Add(600400 * time.Millisecond)
	charlieBack := restart.Add(86399600 * time.Millisecond)
	c := newHistoryChain(t, valAlpha, valBravo, valCharlie).
		session(utc2026(time.November, 14, 0, 0), down).
		session(restart, restart.Add(10*time.Minute)).
		session(bravoBack, charlieBack.Add(-time.Millisecond)).
		at(charlieBack).
		session(charlieBack.Add(historyCadence), utc2026(time.November, 15, 12, 0)).
		// Absent from the block before the restart to the one before the
		// block it signs, whose precommit the next block holds.
		absent(valBravo, down, restart.Add(8*time.Minute)).
		absent(valCharlie, down, charlieBack.Add(-3*time.Minute))
	sc := healthyNetwork()
	sc.seed = func(t *testing.T, s *blocks.Store) { c.write(t, s, "RECOVERY-EDGES") }
	sc.fill = blocks.Progress{ChainID: pageChainID, From: 1, To: int64(len(c.blocks)), FromTime: c.blocks[0].at, Complete: true}
	return sc
}

// valChartsOneMiss is the healthy network on a block history of a single
// day, 2026-11-12, one block every 30 s, alpha, bravo and charlie at 10
// each: bravo proposes 2,000 blocks and misses 1, under 0.05 % of the
// 2,001 it was expected for.
func valChartsOneMiss(t *testing.T) *scenario {
	t.Helper()
	const step = 30 * time.Second
	day := utc2026(time.November, 12, 0, 0)
	missed := day.Add(2000 * step)
	c := newHistoryChain(t, valAlpha, valBravo, valCharlie).
		every(step, day, day.Add(24*time.Hour-step)).
		propose(valBravo, day, missed.Add(-step)).
		missProposals(valBravo, missed, missed)
	sc := healthyNetwork()
	sc.seed = func(t *testing.T, s *blocks.Store) { c.write(t, s, "ONE-MISS") }
	sc.fill = blocks.Progress{ChainID: pageChainID, From: 1, To: int64(len(c.blocks)), FromTime: c.blocks[0].at, Complete: true}
	return sc
}

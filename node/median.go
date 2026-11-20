package node

import (
	"slices"
	"time"
)

// WeightedTime is a precommit's time and its validator's voting power.
type WeightedTime struct {
	Address string
	At      time.Time
	Power   int64
}

// MedianTime is tm2's MedianTime over a commit's precommits: with the votes
// sorted by time, the time at which their cumulative power reaches half the
// total. It is the block time of the block that carries the commit.
func MedianTime(votes []WeightedTime) time.Time {
	if len(votes) == 0 {
		return time.Time{}
	}
	var total int64
	for _, v := range votes {
		total += v.Power
	}
	sorted := slices.Clone(votes)
	slices.SortStableFunc(sorted, func(a, b WeightedTime) int { return a.At.Compare(b.At) })
	median := total / 2
	for _, v := range sorted {
		if median <= v.Power {
			return v.At
		}
		median -= v.Power
	}
	return sorted[len(sorted)-1].At
}

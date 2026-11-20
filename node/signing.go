package node

import "slices"

// ValSigning holds a validator's signing activity over a recent-blocks window,
// scoped to the blocks where the validator was actually in the set. A validator
// added mid-window is not charged for blocks before it joined.
type ValSigning struct {
	From         int // window index the validator became eligible (len(window) if never)
	Signed       int // blocks signed
	Eligible     int // blocks it was in the set for
	Missed       int // Eligible - Signed
	MissedInARow int // consecutive missed blocks ending at the most recent block
	SignedInARow int // consecutive signed blocks ending at the most recent block
	Nil          int // blocks it voted nil for (signed)
	OtherBlock   int // blocks it voted for another block in (missed)
	Votes        int // precommits it cast in the window
	VoteDelayMs  int // the median of those precommits' delays from each commit's weighted median time
}

// windowBlock is a block of the live window: who signed it (a precommit for
// the block or for nil), who voted nil, who voted for another block, and each
// precommit's delay from the commit's weighted median time, in ms.
type windowBlock struct {
	signed, nilVotes, otherBlock map[string]bool
	delays                       map[string]int64
}

// computeSigning derives per-validator signing stats over an ordered window of
// blocks (oldest first); atStart is the validator set at the window's first
// block; current is the present set (stats are returned for these addresses).
//
// A validator is eligible from the window's start if it was in atStart,
// otherwise from its first signature in the window — a newly added validator has
// no set membership, and therefore no missed blocks, before it joined. This is
// why /commit alone is not enough: absent slots don't carry an address, so the
// start-of-window set is needed to tell "in the set but down" from "not yet in
// the set".
func computeSigning(window []windowBlock, atStart, current map[string]bool) map[string]ValSigning {
	n := len(window)

	// First block each current validator signed (n if never).
	from := make(map[string]int, len(current))
	for a := range current {
		from[a] = n
	}
	for i := 0; i < n; i++ {
		for a := range window[i].signed {
			if cur, ok := from[a]; ok && cur == n {
				from[a] = i
			}
		}
	}
	// A validator present at the window's start is eligible from block 0.
	for a := range current {
		if atStart[a] {
			from[a] = 0
		}
	}

	out := make(map[string]ValSigning, len(current))
	for a := range current {
		f := from[a]
		if f >= n {
			out[a] = ValSigning{From: n}
			continue
		}
		signed := 0
		for i := f; i < n; i++ {
			if window[i].signed[a] {
				signed++
			}
		}
		// Consecutive missed / signed blocks ending at the most recent block,
		// bounded to the validator's eligible range.
		missedInARow, signedInARow := 0, 0
		for i := n - 1; i >= f; i-- {
			if window[i].signed[a] {
				break
			}
			missedInARow++
		}
		for i := n - 1; i >= f; i-- {
			if !window[i].signed[a] {
				break
			}
			signedInARow++
		}
		eligible := n - f
		v := ValSigning{
			From: f, Signed: signed, Eligible: eligible, Missed: eligible - signed,
			MissedInARow: missedInARow, SignedInARow: signedInARow,
		}
		var delays []int64
		for i := f; i < n; i++ {
			if window[i].nilVotes[a] {
				v.Nil++
			}
			if window[i].otherBlock[a] {
				v.OtherBlock++
			}
			if d, ok := window[i].delays[a]; ok {
				delays = append(delays, d)
			}
		}
		v.Votes, v.VoteDelayMs = len(delays), medianMs(delays)
		out[a] = v
	}
	return out
}

// medianMs returns the middle of ds, the mean of the two middles for an even
// count (truncated to a whole ms), 0 for none.
func medianMs(ds []int64) int {
	if len(ds) == 0 {
		return 0
	}
	sorted := slices.Clone(ds)
	slices.Sort(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return int(sorted[n/2])
	}
	return int((sorted[n/2-1] + sorted[n/2]) / 2)
}

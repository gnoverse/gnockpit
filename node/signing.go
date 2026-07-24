package node

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
}

// computeSigning derives per-validator signing stats over an ordered window of
// blocks (oldest first). signers[i] is the set of addresses that signed block i;
// atStart is the validator set at the window's first block; current is the
// present set (stats are returned for these addresses).
//
// A validator is eligible from the window's start if it was in atStart,
// otherwise from its first signature in the window — a newly added validator has
// no set membership, and therefore no missed blocks, before it joined. This is
// why /commit alone is not enough: absent slots don't carry an address, so the
// start-of-window set is needed to tell "in the set but down" from "not yet in
// the set".
func computeSigning(signers []map[string]bool, atStart, current map[string]bool) map[string]ValSigning {
	n := len(signers)

	// First block each current validator signed (n if never).
	from := make(map[string]int, len(current))
	for a := range current {
		from[a] = n
	}
	for i := 0; i < n; i++ {
		for a := range signers[i] {
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
			if signers[i][a] {
				signed++
			}
		}
		// Consecutive missed / signed blocks ending at the most recent block,
		// bounded to the validator's eligible range.
		missedInARow, signedInARow := 0, 0
		for i := n - 1; i >= f; i-- {
			if signers[i][a] {
				break
			}
			missedInARow++
		}
		for i := n - 1; i >= f; i-- {
			if !signers[i][a] {
				break
			}
			signedInARow++
		}
		eligible := n - f
		out[a] = ValSigning{
			From: f, Signed: signed, Eligible: eligible, Missed: eligible - signed,
			MissedInARow: missedInARow, SignedInARow: signedInARow,
		}
	}
	return out
}

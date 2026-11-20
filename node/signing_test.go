package node

import "testing"

func addrSet(addrs ...string) map[string]bool {
	m := map[string]bool{}
	for _, a := range addrs {
		m[a] = true
	}
	return m
}

// signedBy is a window whose blocks each validator signs for the block.
func signedBy(signers ...map[string]bool) []windowBlock {
	out := make([]windowBlock, len(signers))
	for i, s := range signers {
		out[i] = windowBlock{signed: s}
	}
	return out
}

func TestComputeSigning(t *testing.T) {
	// A 5-block window (oldest first).
	//   A: in set from the start, signs every block.
	//   B: in set from the start, signs 0-2 then misses 3-4.
	//   C: in set from the start, never signs (fully down all window).
	//   D: NOT in the start set — joins at block 3 and signs 3-4 (new validator).
	//   E: NOT in the start set — joins at block 3, signs 3, misses 4.
	window := signedBy(
		addrSet("A", "B"),      // 0
		addrSet("A", "B"),      // 1
		addrSet("A", "B"),      // 2
		addrSet("A", "D", "E"), // 3
		addrSet("A", "D"),      // 4
	)
	atStart := addrSet("A", "B", "C")
	current := addrSet("A", "B", "C", "D", "E")

	got := computeSigning(window, atStart, current)

	want := map[string]ValSigning{
		"A": {From: 0, Signed: 5, Eligible: 5, Missed: 0, MissedInARow: 0, SignedInARow: 5},
		"B": {From: 0, Signed: 3, Eligible: 5, Missed: 2, MissedInARow: 2, SignedInARow: 0},
		"C": {From: 0, Signed: 0, Eligible: 5, Missed: 5, MissedInARow: 5, SignedInARow: 0},
		// New validator that signs everything since joining must NOT show the
		// pre-join blocks as missed (the reported bug).
		"D": {From: 3, Signed: 2, Eligible: 2, Missed: 0, MissedInARow: 0, SignedInARow: 2},
		"E": {From: 3, Signed: 1, Eligible: 2, Missed: 1, MissedInARow: 1, SignedInARow: 0},
	}
	for addr, w := range want {
		if got[addr] != w {
			t.Errorf("%s = %+v, want %+v", addr, got[addr], w)
		}
	}
}

func TestComputeSigningNewAndNeverSigned(t *testing.T) {
	// A validator in the current set that is neither in the start set nor has
	// signed anything in the window (just added, not yet producing) has no
	// eligible blocks — it must not be charged any missed blocks.
	window := signedBy(addrSet("A"), addrSet("A"), addrSet("A"))
	got := computeSigning(window, addrSet("A"), addrSet("A", "N"))
	if v := got["N"]; v.Eligible != 0 || v.Missed != 0 || v.MissedInARow != 0 || v.SignedInARow != 0 {
		t.Errorf("N = %+v, want zero eligibility/missed/streaks", v)
	}
	if v := got["A"]; v.Missed != 0 || v.Signed != 3 || v.SignedInARow != 3 {
		t.Errorf("A = %+v, want signed 3 / missed 0 / signed-streak 3", v)
	}
}

func TestComputeSigningSplitsAbsences(t *testing.T) {
	// A votes nil in block 0 (signed), for another block in 1, is absent in
	// 2; N joins at block 1, so block 0's nil vote is not its own.
	window := []windowBlock{
		{signed: addrSet("A", "B"), nilVotes: addrSet("A")},
		{signed: addrSet("B", "N"), otherBlock: addrSet("A")},
		{signed: addrSet("B", "N")},
	}
	got := computeSigning(window, addrSet("A", "B"), addrSet("A", "B", "N"))
	if a := got["A"]; a.Missed != 2 || a.Nil != 1 || a.OtherBlock != 1 {
		t.Errorf("A = %+v, want missed 2, nil 1, other block 1", a)
	}
	if n := got["N"]; n.Nil != 0 || n.Missed != 0 {
		t.Errorf("N = %+v, want no nil vote and no miss before it joined", n)
	}
}

func TestComputeSigningVoteDelay(t *testing.T) {
	delays := func(ms ...int64) []windowBlock {
		out := make([]windowBlock, len(ms))
		for i, d := range ms {
			out[i] = windowBlock{signed: addrSet("A"), delays: map[string]int64{"A": d}}
		}
		return out
	}
	// Added mid-window, A votes for another block before it first signs: its
	// window starts at its first signature.
	joined := append([]windowBlock{{signed: addrSet(), otherBlock: addrSet("A"), delays: map[string]int64{"A": 500}}}, delays(100)...)
	for _, tc := range []struct {
		name    string
		window  []windowBlock
		atStart map[string]bool
		votes   int
		median  int
	}{
		{"odd count", delays(100, 300, -50), addrSet("A"), 3, 100},
		{"even count: the middles' mean", delays(100, 300), addrSet("A"), 2, 200},
		{"none", signedBy(addrSet("A")), addrSet("A"), 0, 0},
		{"from its first signature", joined, addrSet(), 1, 100},
	} {
		got := computeSigning(tc.window, tc.atStart, addrSet("A"))["A"]
		if got.Votes != tc.votes || got.VoteDelayMs != tc.median {
			t.Errorf("%s: votes %d, vote delay %d ms; want %d, %d ms", tc.name, got.Votes, got.VoteDelayMs, tc.votes, tc.median)
		}
	}
}

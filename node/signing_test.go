package node

import "testing"

func addrSet(addrs ...string) map[string]bool {
	m := map[string]bool{}
	for _, a := range addrs {
		m[a] = true
	}
	return m
}

func TestComputeSigning(t *testing.T) {
	// A 5-block window (oldest first).
	//   A: in set from the start, signs every block.
	//   B: in set from the start, signs 0-2 then misses 3-4.
	//   C: in set from the start, never signs (fully down all window).
	//   D: NOT in the start set — joins at block 3 and signs 3-4 (new validator).
	//   E: NOT in the start set — joins at block 3, signs 3, misses 4.
	signers := []map[string]bool{
		addrSet("A", "B"),      // 0
		addrSet("A", "B"),      // 1
		addrSet("A", "B"),      // 2
		addrSet("A", "D", "E"), // 3
		addrSet("A", "D"),      // 4
	}
	atStart := addrSet("A", "B", "C")
	current := addrSet("A", "B", "C", "D", "E")

	got := computeSigning(signers, atStart, current)

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
	signers := []map[string]bool{addrSet("A"), addrSet("A"), addrSet("A")}
	got := computeSigning(signers, addrSet("A"), addrSet("A", "N"))
	if v := got["N"]; v.Eligible != 0 || v.Missed != 0 || v.MissedInARow != 0 || v.SignedInARow != 0 {
		t.Errorf("N = %+v, want zero eligibility/missed/streaks", v)
	}
	if v := got["A"]; v.Missed != 0 || v.Signed != 3 || v.SignedInARow != 3 {
		t.Errorf("A = %+v, want signed 3 / missed 0 / signed-streak 3", v)
	}
}

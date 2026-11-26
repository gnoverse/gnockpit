package blocks

import (
	"slices"
	"testing"

	"github.com/gnoverse/gnockpit/node"
)

// Mainnet validators (rpc.gno.land, read 2026-11-19).
const (
	val180 = "g180gwv8tjclsqqj6hlgm7gmmrzl6l69xfg3r88p"
	valG0Y = "g1g0yrmdhney8pcaf9rfut4604equz604pszdsyt"
	val15T = "g15t7f9q6km3ldt885duwl8xu5dncs98528amk4f"
	valHQH = "g1hqhetnnz0raw5hps6yxexl7q09a6f8w3anlptt"
	valH97 = "g1h97x8geqqwhh0prepcwrqskcd35g6gy5s39eny"
	valMMG = "g1mmgvcssjw6x4fzphupfg6mtxqt36v000c5rf2a"
	valL98 = "g1l983yy3kpmapyzcfy53y5charfxupa5czjalea"
)

// m is a validator of a test set: address, voting power, proposer priority.
type m struct {
	addr  string
	power int64
	prio  int64
}

func setOf(ms ...m) []node.SetMember {
	out := make([]node.SetMember, len(ms))
	for i, v := range ms {
		out[i] = node.SetMember{Address: v.addr, Power: v.power, Priority: v.prio}
	}
	return out
}

// Mainnet's sets (/validators?height=h), each one increment of the one below.
var (
	set1183001 = setOf(m{valG0Y, 60, -20}, m{val15T, 60, -128}, m{valHQH, 60, -188}, m{valH97, 60, -4}, m{valMMG, 60, 171}, m{valL98, 60, 171})
	set1183002 = setOf(m{valG0Y, 60, 40}, m{val15T, 60, -68}, m{valHQH, 60, -128}, m{valH97, 60, 56}, m{valMMG, 60, -129}, m{valL98, 60, 231})
	set1662998 = setOf(m{val180, 60, 16}, m{valG0Y, 60, 108}, m{val15T, 60, 0}, m{valHQH, 60, -60}, m{valH97, 60, 124}, m{valMMG, 60, -61}, m{valL98, 60, -121})
	set1662999 = setOf(m{val180, 60, 76}, m{valG0Y, 60, 168}, m{val15T, 60, 60}, m{valHQH, 60, 0}, m{valH97, 60, -236}, m{valMMG, 60, -1}, m{valL98, 60, -61})
	set1663000 = setOf(m{val180, 60, 136}, m{valG0Y, 60, -192}, m{val15T, 60, 120}, m{valHQH, 60, 60}, m{valH97, 60, -176}, m{valMMG, 60, 59}, m{valL98, 60, -1})
)

func TestRoundProposers(t *testing.T) {
	for _, tc := range []struct {
		name      string
		prev, cur []node.SetMember
		last      int
		want      []string // header proposer is the last one
	}{
		// valMMG and valL98 tie at 231 after the increment; tm2 picks the
		// lower address bytes, valMMG, whatever the bech32 order says.
		{"1183002, round 1 after the restart", set1183001, set1183002, 1, []string{valMMG, valL98}},
		{"1662999, round 1, round 0's proposer absent", set1662998, set1662999, 1, []string{valH97, valG0Y}},
		{"1663000, round 0", set1662999, set1663000, 0, []string{valG0Y}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := roundProposers(tc.prev, tc.cur, tc.last)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("roundProposers() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRoundProposersRoundZeroUnknown(t *testing.T) {
	for _, tc := range []struct {
		name string
		prev []node.SetMember
	}{
		{"across a set change", set1662998[:6]},
		{"without the set below", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := roundProposers(tc.prev, set1662999, 1)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"", valG0Y}; !slices.Equal(got, want) {
				t.Errorf("roundProposers() = %q, want %q", got, want)
			}
		})
	}
}

func TestIncrementRescales(t *testing.T) {
	// Priorities 200 apart for a total power of 2 are divided by
	// ceil(200 / (2 × 2)) = 50 before the increment: 2 and -2, then 3 and -1,
	// and the proposer is charged the total, 2.
	set, err := newPrioSet(setOf(m{valG0Y, 1, 100}, m{val15T, 1, -100}))
	if err != nil {
		t.Fatal(err)
	}
	if p := set.increment(); p.address != valG0Y {
		t.Errorf("proposer = %s, want %s", p.address, valG0Y)
	}
	if set[0].prio != 1 || set[1].prio != -1 {
		t.Errorf("priorities = %d, %d; want 1, -1", set[0].prio, set[1].prio)
	}
}

func TestIncrementCentresOnTheFlooredAverage(t *testing.T) {
	// The average of -3 and 0 is -1.5; tm2 takes it with big.Int's Euclidean
	// division, -2, so the priorities become -1 and 2, then 9 and 12.
	set, err := newPrioSet(setOf(m{valG0Y, 10, -3}, m{val15T, 10, 0}))
	if err != nil {
		t.Fatal(err)
	}
	if p := set.increment(); p.address != val15T {
		t.Errorf("proposer = %s, want %s", p.address, val15T)
	}
	if set[0].prio != 9 || set[1].prio != -8 {
		t.Errorf("priorities = %d, %d; want 9, -8", set[0].prio, set[1].prio)
	}
}

func TestNewPrioSetRejectsABadAddress(t *testing.T) {
	// The second is a g bech32 string of 32 bytes, not an address's 20.
	for _, addr := range []string{"not-an-address", "g1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqemfmay"} {
		if _, err := newPrioSet(setOf(m{addr, 1, 0})); err == nil {
			t.Errorf("newPrioSet() accepted %q", addr)
		}
	}
}

func TestRoundProposersRejectsAnEmptySet(t *testing.T) {
	if _, err := roundProposers(nil, nil, 1); err == nil {
		t.Error("roundProposers() accepted an empty validator set")
	}
}

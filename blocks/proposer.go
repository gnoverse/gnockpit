package blocks

import (
	"errors"
	"fmt"
	"math"
	"math/big"

	"github.com/gnolang/gno/tm2/pkg/crypto"
	"github.com/gnoverse/gnockpit/node"
)

// priorityWindow is tm2's PriorityWindowSizeFactor: priorities are rescaled to
// spread over at most this many times the total power.
const priorityWindow = 2

// prioVal is a validator in a proposer-priority computation.
type prioVal struct {
	address string
	bytes   crypto.Address
	power   int64
	prio    int64
}

// prioSet is a validator set as tm2's ValidatorSet sees it when choosing a
// proposer: the methods below port IncrementProposerPriority and its helpers.
type prioSet []*prioVal

func newPrioSet(members []node.SetMember) (prioSet, error) {
	set := make(prioSet, len(members))
	for i, m := range members {
		b, err := crypto.AddressFromBech32(m.Address)
		if err != nil {
			return nil, fmt.Errorf("validator %q: %w", m.Address, err)
		}
		set[i] = &prioVal{address: m.Address, bytes: b, power: m.Power, prio: m.Priority}
	}
	return set, nil
}

func (s prioSet) copy() prioSet {
	out := make(prioSet, len(s))
	for i, v := range s {
		c := *v
		out[i] = &c
	}
	return out
}

// equal reports whether two sets hold the same validators in the same order,
// with the same power and priority.
func (s prioSet) equal(o prioSet) bool {
	if len(s) != len(o) {
		return false
	}
	for i := range s {
		if s[i].address != o[i].address || s[i].power != o[i].power || s[i].prio != o[i].prio {
			return false
		}
	}
	return true
}

func (s prioSet) totalPower() int64 {
	var total int64
	for _, v := range s {
		total = safeAddClip(total, v.power)
	}
	return total
}

// increment is tm2's IncrementProposerPriority(1): it rescales and centres the
// priorities, adds each validator's power to its priority, and charges the
// highest the total power. It returns that validator, the proposer.
func (s prioSet) increment() *prioVal {
	total := s.totalPower()
	s.rescale(priorityWindow * total)
	s.centre()
	for _, v := range s {
		v.prio = safeAddClip(v.prio, v.power)
	}
	var most *prioVal
	for _, v := range s {
		most = most.higher(v)
	}
	most.prio = safeSubClip(most.prio, total)
	return most
}

// higher is tm2's CompareProposerPriority: the higher priority, a tie going to
// the lower address bytes.
func (v *prioVal) higher(other *prioVal) *prioVal {
	switch {
	case v == nil:
		return other
	case v.prio > other.prio:
		return v
	case v.prio < other.prio:
		return other
	case v.bytes.Compare(other.bytes) < 0:
		return v
	default:
		return other
	}
}

// rescale is tm2's RescalePriorities: priorities spread over more than
// diffMax are divided by ceil(spread / diffMax).
func (s prioSet) rescale(diffMax int64) {
	if diffMax <= 0 || len(s) == 0 {
		return
	}
	lo, hi := int64(math.MaxInt64), int64(math.MinInt64)
	for _, v := range s {
		lo, hi = min(lo, v.prio), max(hi, v.prio)
	}
	diff := hi - lo
	if diff < 0 {
		diff = -diff
	}
	if diff <= diffMax {
		return
	}
	ratio := (diff + diffMax - 1) / diffMax
	for _, v := range s {
		v.prio /= ratio
	}
}

// centre is tm2's shiftByAvgProposerPriority: priorities minus their
// average, taken with big.Int's Euclidean division as tm2 does.
func (s prioSet) centre() {
	if len(s) == 0 {
		return
	}
	sum := big.NewInt(0)
	for _, v := range s {
		sum.Add(sum, big.NewInt(v.prio))
	}
	avg := sum.Div(sum, big.NewInt(int64(len(s)))).Int64()
	for _, v := range s {
		v.prio = safeSubClip(v.prio, avg)
	}
}

func safeAddClip(a, b int64) int64 {
	c := a + b
	switch {
	case b > 0 && c < a:
		return math.MaxInt64
	case b < 0 && c > a:
		return math.MinInt64
	}
	return c
}

func safeSubClip(a, b int64) int64 {
	c := a - b
	switch {
	case b > 0 && c > a:
		return math.MinInt64
	case b < 0 && c < a:
		return math.MaxInt64
	}
	return c
}

// roundProposers returns the expected proposer of rounds 0 to last at a
// height, from the validator sets /validators returns at the height below
// (prev, nil at height 1) and at the height (cur). /validators gives a set
// after its round-0 proposer was chosen and charged, so round 0's proposer is
// the one an increment of prev charges; it is "" when that increment does not
// give cur (the set changed between the two heights). Rounds 1 and up come
// from increments of cur, as consensus does on a copy at each new round.
func roundProposers(prev, cur []node.SetMember, last int) ([]string, error) {
	if len(cur) == 0 {
		return nil, errors.New("empty validator set")
	}
	out := make([]string, last+1)
	c, err := newPrioSet(cur)
	if err != nil {
		return nil, err
	}
	if len(prev) > 0 {
		p, err := newPrioSet(prev)
		if err != nil {
			return nil, err
		}
		proposer := p.increment()
		if p.equal(c) {
			out[0] = proposer.address
		}
	}
	work := c.copy()
	for r := 1; r <= last; r++ {
		out[r] = work.increment().address
	}
	return out, nil
}

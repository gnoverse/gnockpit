package blocks

import (
	"fmt"
	"time"

	"github.com/gnoverse/gnockpit/node"
)

// Member is a validator of a stored set, in set order.
type Member struct {
	Address string
	PubKey  string
	Power   int64
}

// Kind is how a member of the set failed to sign a block for it.
type Kind int

const (
	Absent     Kind = 1 // no precommit in the commit
	Nil        Kind = 2 // a precommit for nil
	OtherBlock Kind = 3 // a precommit for another block
)

// Absence is a member of the set whose precommit in the commit is not for the
// block.
type Absence struct {
	Address string
	Kind    Kind
}

// Delay is how far a precommit's timestamp is from the voting-power-weighted
// median of the commit's precommit timestamps, in ms.
type Delay struct {
	Address string
	Ms      int64
}

// Record is what is stored about one height.
type Record struct {
	Height     int64
	Time       time.Time
	Proposer   string
	Round      int // the commit round
	Txs        int
	ValsetHash string
	Absences   []Absence
	Delays     []Delay
	Rounds     []string // expected proposer of rounds 0..Round, when computed; "" when unknown
	Flag       string   // why a check failed, empty when none did
}

// newRecord builds the record of a commit against the validator set its
// header names, whose members are in the order of the commit's precommits.
func newRecord(c node.Commit, set []Member) (Record, error) {
	if len(c.Precommits) != len(set) {
		return Record{}, fmt.Errorf("height %d: %d precommits for a set of %d", c.Height, len(c.Precommits), len(set))
	}
	r := Record{Height: c.Height, Time: c.Time, Proposer: c.Proposer, Txs: c.NumTxs, ValsetHash: c.ValidatorsHash}
	var votes []node.WeightedTime
	for i, pc := range c.Precommits {
		member := set[i]
		if pc == nil {
			r.Absences = append(r.Absences, Absence{Address: member.Address, Kind: Absent})
			continue
		}
		if pc.Address != member.Address {
			return Record{}, fmt.Errorf("height %d: precommit %d is %s's, the set has %s there", c.Height, i, pc.Address, member.Address)
		}
		r.Round = pc.Round
		switch pc.BlockHash {
		case c.BlockHash:
		case "":
			r.Absences = append(r.Absences, Absence{Address: member.Address, Kind: Nil})
		default:
			r.Absences = append(r.Absences, Absence{Address: member.Address, Kind: OtherBlock})
		}
		votes = append(votes, node.WeightedTime{Address: member.Address, At: pc.Time, Power: member.Power})
	}
	if len(votes) > 0 {
		median := node.MedianTime(votes)
		for _, v := range votes {
			r.Delays = append(r.Delays, Delay{Address: v.Address, Ms: v.At.Sub(median).Milliseconds()})
		}
	}
	return r, nil
}

package node

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Commit is a block's header and the precommits that finalized it, as
// /commit?height=h returns them.
type Commit struct {
	Height         int64
	Time           time.Time
	Proposer       string
	NumTxs         int
	ValidatorsHash string
	BlockHash      string       // the committed block's id
	Canonical      bool         // read from the next block, so final
	Precommits     []*Precommit // in validator set order; nil where the slot is empty
}

// Precommit is one validator's precommit in a commit.
type Precommit struct {
	Address   string
	Index     int
	Round     int
	BlockHash string // empty for a precommit for nil
	Time      time.Time
}

type commitJSON struct {
	Canonical    bool `json:"canonical"`
	SignedHeader struct {
		Header struct {
			Height          string    `json:"height"`
			Time            time.Time `json:"time"`
			ProposerAddress string    `json:"proposer_address"`
			NumTxs          string    `json:"num_txs"`
			ValidatorsHash  string    `json:"validators_hash"`
		} `json:"header"`
		Commit struct {
			BlockID struct {
				Hash string `json:"hash"`
			} `json:"block_id"`
			Precommits []*struct {
				Round   string `json:"round"`
				BlockID struct {
					Hash string `json:"hash"`
				} `json:"block_id"`
				Timestamp        time.Time `json:"timestamp"`
				ValidatorAddress string    `json:"validator_address"`
				ValidatorIndex   string    `json:"validator_index"`
			} `json:"precommits"`
		} `json:"commit"`
	} `json:"signed_header"`
}

func (j commitJSON) commit() (Commit, error) {
	h := j.SignedHeader.Header
	height, err := strconv.ParseInt(h.Height, 10, 64)
	if err != nil {
		return Commit{}, fmt.Errorf("height %q: %w", h.Height, err)
	}
	txs, err := strconv.Atoi(cmp.Or(h.NumTxs, "0"))
	if err != nil {
		return Commit{}, fmt.Errorf("num_txs %q: %w", h.NumTxs, err)
	}
	c := Commit{
		Height:         height,
		Time:           h.Time,
		Proposer:       h.ProposerAddress,
		NumTxs:         txs,
		ValidatorsHash: h.ValidatorsHash,
		BlockHash:      j.SignedHeader.Commit.BlockID.Hash,
		Canonical:      j.Canonical,
		Precommits:     make([]*Precommit, len(j.SignedHeader.Commit.Precommits)),
	}
	for i, p := range j.SignedHeader.Commit.Precommits {
		if p == nil {
			continue
		}
		round, err := strconv.Atoi(p.Round)
		if err != nil {
			return Commit{}, fmt.Errorf("precommit %d round %q: %w", i, p.Round, err)
		}
		index, err := strconv.Atoi(p.ValidatorIndex)
		if err != nil {
			return Commit{}, fmt.Errorf("precommit %d validator index %q: %w", i, p.ValidatorIndex, err)
		}
		c.Precommits[i] = &Precommit{
			Address:   p.ValidatorAddress,
			Index:     index,
			Round:     round,
			BlockHash: p.BlockID.Hash,
			Time:      p.Timestamp,
		}
	}
	return c, nil
}

// Commits reads the commits of heights in one JSON-RPC batch, in heights'
// order. A height the node refuses ends the result there, with a
// *HeightError wrapping its *RPCError.
func (c *Client) Commits(ctx context.Context, heights []int64) ([]Commit, error) {
	calls := make([]rpcCall, len(heights))
	for i, h := range heights {
		calls[i] = rpcCall{Method: "commit", Params: map[string]string{"height": strconv.FormatInt(h, 10)}}
	}
	answers, err := c.rpcBatch(ctx, calls)
	if err != nil {
		return nil, err
	}
	out := make([]Commit, 0, len(heights))
	for i, a := range answers {
		if a.Error != nil {
			return out, &HeightError{Height: heights[i], Err: a.Error}
		}
		var j commitJSON
		if err := json.Unmarshal(a.Result, &j); err != nil {
			return out, &HeightError{Height: heights[i], Err: fmt.Errorf("unmarshal commit: %w", err)}
		}
		cm, err := j.commit()
		if err != nil {
			return out, &HeightError{Height: heights[i], Err: err}
		}
		out = append(out, cm)
	}
	return out, nil
}

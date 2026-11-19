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

// SetMember is a validator of the set at a height, with its proposer priority
// there.
type SetMember struct {
	Address  string
	PubKey   string // the key's base64 value
	Power    int64
	Priority int64
}

// ValidatorSet reads the validator set at height, in set order (the order of
// a commit's precommits).
func (c *Client) ValidatorSet(ctx context.Context, height int64) ([]SetMember, error) {
	var res struct {
		Validators []struct {
			Address string `json:"address"`
			PubKey  struct {
				Value string `json:"value"`
			} `json:"pub_key"`
			VotingPower      string `json:"voting_power"`
			ProposerPriority string `json:"proposer_priority"`
		} `json:"validators"`
	}
	if err := c.rpcGetJSON(ctx, fmt.Sprintf("/validators?height=%d", height), &res); err != nil {
		return nil, err
	}
	out := make([]SetMember, len(res.Validators))
	for i, v := range res.Validators {
		power, err := strconv.ParseInt(v.VotingPower, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("validator %s voting power %q: %w", v.Address, v.VotingPower, err)
		}
		priority, err := strconv.ParseInt(v.ProposerPriority, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("validator %s proposer priority %q: %w", v.Address, v.ProposerPriority, err)
		}
		out[i] = SetMember{Address: v.Address, PubKey: v.PubKey.Value, Power: power, Priority: priority}
	}
	return out, nil
}

// BlockEvent is an event a transaction of a block emitted with chain.Emit.
type BlockEvent struct {
	Type    string
	PkgPath string
	Attrs   map[string]string
}

// BlockEvents reads the events the successful transactions of the block at
// height emitted with chain.Emit, in order (/block_results). Other events,
// such as storage deposits, are left out.
func (c *Client) BlockEvents(ctx context.Context, height int64) ([]BlockEvent, error) {
	var res struct {
		Results struct {
			DeliverTx []struct {
				ResponseBase struct {
					Error  json.RawMessage `json:"Error"`
					Events []struct {
						Kind    string `json:"@type"`
						Type    string `json:"type"`
						PkgPath string `json:"pkg_path"`
						Attrs   []struct {
							Key   string `json:"key"`
							Value string `json:"value"`
						} `json:"attrs"`
					} `json:"Events"`
				} `json:"ResponseBase"`
			} `json:"deliver_tx"`
		} `json:"results"`
	}
	if err := c.rpcGetJSON(ctx, fmt.Sprintf("/block_results?height=%d", height), &res); err != nil {
		return nil, err
	}
	var out []BlockEvent
	for _, tx := range res.Results.DeliverTx {
		if e := tx.ResponseBase.Error; len(e) > 0 && string(e) != "null" {
			continue
		}
		for _, ev := range tx.ResponseBase.Events {
			if ev.Kind != "/tm.Event" {
				continue
			}
			attrs := make(map[string]string, len(ev.Attrs))
			for _, a := range ev.Attrs {
				attrs[a.Key] = a.Value
			}
			out = append(out, BlockEvent{Type: ev.Type, PkgPath: ev.PkgPath, Attrs: attrs})
		}
	}
	return out, nil
}

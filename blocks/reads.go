package blocks

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/gnoverse/gnockpit/node"
)

// limited calls read until it answers anything but HTTP 429, waiting the
// answer's Retry-After (rateLimitPause without one) in between.
func (t *Tracker) limited(ctx context.Context, read func() error) error {
	for {
		err := read()
		var hs *node.HTTPStatusError
		if !errors.As(err, &hs) || hs.StatusCode != http.StatusTooManyRequests {
			return err
		}
		wait := cmp.Or(hs.RetryAfter, rateLimitPause)
		t.report("fill", fmt.Errorf("RPC rate limited, pausing for %s", wait))
		if err := t.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func (t *Tracker) chainHead(ctx context.Context) (head node.ChainHead, err error) {
	err = t.limited(ctx, func() error {
		head, err = t.source.ChainHead(ctx)
		return err
	})
	return head, err
}

func (t *Tracker) commits(ctx context.Context, heights []int64) (got []node.Commit, err error) {
	err = t.limited(ctx, func() error {
		got, err = t.source.Commits(ctx, heights)
		return err
	})
	return got, err
}

func (t *Tracker) validatorSet(ctx context.Context, height int64) (set []node.SetMember, err error) {
	err = t.limited(ctx, func() error {
		set, err = t.source.ValidatorSet(ctx, height)
		return err
	})
	return set, err
}

func (t *Tracker) blockEvents(ctx context.Context, height int64) (evs []node.BlockEvent, err error) {
	err = t.limited(ctx, func() error {
		evs, err = t.source.BlockEvents(ctx, height)
		return err
	})
	return evs, err
}

// record builds the record of a commit against the validator set it names,
// with the expected proposer of each round when it was finalized after round
// 0. Such a block, and every checkEvery-th one, has its proposer checked and is
// flagged when no round expects it or round 0's proposer cannot be known.
func (t *Tracker) record(ctx context.Context, c node.Commit, head int64) (Record, error) {
	set, err := t.members(ctx, c)
	if err != nil {
		return Record{}, err
	}
	r, err := newRecord(c, set)
	if err != nil {
		return Record{}, err
	}
	if r.Round == 0 && r.Height%checkEvery != 0 {
		return r, nil
	}
	if err := t.rounds(ctx, &r, head); err != nil {
		return Record{}, err
	}
	return r, nil
}

// members returns the validator set a commit names, from the store, or read
// once from the source at the commit's height and stored.
func (t *Tracker) members(ctx context.Context, c node.Commit) ([]Member, error) {
	set, ok, err := t.store.Valset(ctx, c.ValidatorsHash)
	if err != nil || ok {
		return set, err
	}
	got, err := t.validatorSet(ctx, c.Height)
	if err != nil {
		return nil, fmt.Errorf("validator set at %d: %w", c.Height, err)
	}
	if len(got) == 0 {
		return nil, fmt.Errorf("validator set at %d: empty answer", c.Height)
	}
	set = make([]Member, len(got))
	for i, m := range got {
		set[i] = Member{Address: m.Address, PubKey: m.PubKey, Power: m.Power}
	}
	if err := t.store.PutValset(ctx, c.ValidatorsHash, set); err != nil {
		return nil, err
	}
	return set, nil
}

// rounds fills r's expected proposer of each round, from the validator sets
// at the height below and at the height, and flags r when round 0's proposer
// is unknown or no round's expected proposer is r's proposer. A set below the
// source refuses well below the head (a block at the oldest it keeps), in two
// reads, leaves round 0's proposer unknown; a first refusal, or one near the
// head (a backend behind), fails the read for a later pass.
func (t *Tracker) rounds(ctx context.Context, r *Record, head int64) error {
	cur, err := t.validatorSet(ctx, r.Height)
	if err != nil {
		return fmt.Errorf("validator set at %d: %w", r.Height, err)
	}
	var prev []node.SetMember
	var prevRefused bool
	if r.Height > 1 {
		prev, err = t.validatorSet(ctx, r.Height-1)
		switch {
		case node.IsRefusal(err) && head-(r.Height-1) > floorMargin && t.setRefused == r.Height-1:
			prev, prevRefused, t.setRefused = nil, true, 0
		case node.IsRefusal(err) && head-(r.Height-1) > floorMargin:
			t.setRefused = r.Height - 1
			return fmt.Errorf("validator set at %d: %w", r.Height-1, err)
		case err != nil:
			return fmt.Errorf("validator set at %d: %w", r.Height-1, err)
		}
	}
	proposers, err := roundProposers(prev, cur, r.Round)
	if err != nil {
		return fmt.Errorf("height %d: %w", r.Height, err)
	}
	if r.Round > 0 {
		r.Rounds = proposers
	}
	switch {
	case prevRefused:
		r.Flag = fmt.Sprintf("round 0's proposer unknown: the source does not serve the set at #%d", r.Height-1)
	case proposers[0] == "":
		r.Flag = "round 0's proposer unknown: one increment of the set below does not give this one"
	case !slices.Contains(proposers, r.Proposer):
		r.Flag = "no round's expected proposer is the header's " + r.Proposer
	}
	if r.Flag != "" {
		t.logf("blocks: height %d: %s", r.Height, r.Flag)
	}
	return nil
}

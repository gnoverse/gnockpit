package ranking

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/node"
)

// Profiles are the valoper profiles, by operator address.
type Profiles map[string]node.Valoper

// Gather reads the ranking's input as of now from the block store: the
// scoring window, which must be complete; the operators in the validator set
// at its last height; and the window's proposal flags. Each operator's
// history is read as the window ends (blocks.Store.WindowHistory), so blocks
// stored after it change no history; a link can still change, as keys are
// linked by every stored event and the profiles, as of now: a key's operator
// is the one whose profile names it as its signing key, else the one an event
// links it to; a key neither links is ranked alone, its own operator under
// its signing address. An operator's keys are those its events and profile
// give it, and its keys in the set; its name is its profile's moniker, else
// what names gives for its first key in the set. An operator whose address
// exclude lists is Excluded. Gather returns an error naming the first of
// Input's invariants that what it read breaks.
func Gather(ctx context.Context, s *blocks.Store, profiles Profiles, names func(addr string) string, exclude []string, now time.Time) (Input, error) {
	w, err := s.ScoringWindow(ctx, now)
	if err != nil {
		return Input{}, err
	}
	if !w.Complete {
		return Input{}, fmt.Errorf("scoring window %s to %s: not complete, no block of %s stored",
			w.FirstDay.Format(time.DateOnly), w.LastDay.Format(time.DateOnly), now.UTC().Format(time.DateOnly))
	}
	g := gatherer{store: s, profiles: profiles, signers: signers(profiles), names: names, exclude: exclude}
	in := Input{Window: w}
	if in.Operators, err = g.operators(ctx, w); err != nil {
		return Input{}, err
	}
	if in.ProposalFlags, err = s.ProposalFlags(ctx, w.First, w.Last); err != nil {
		return Input{}, err
	}
	if err = in.check(); err != nil {
		return Input{}, err
	}
	return in, nil
}

// gatherer reads the operators of a validator set from the block store.
type gatherer struct {
	store    *blocks.Store
	profiles Profiles
	signers  map[string]string // the operator whose profile names a signing key, by key
	names    func(addr string) string
	exclude  []string
}

// signers returns the operator whose profile names each signing key, by key:
// of several profiles naming one key, the operator first in byte order.
func signers(profiles Profiles) map[string]string {
	out := map[string]string{}
	for _, op := range slices.Sorted(maps.Keys(profiles)) {
		key := profiles[op].SigningAddress
		if _, named := out[key]; !named {
			out[key] = op
		}
	}
	return out
}

// operators returns the operators in the validator set at the window's last
// height, in set order, each once however many of its keys the set holds,
// with its history as the window ends. A window without a block, Last 0, has
// none.
func (g gatherer) operators(ctx context.Context, w blocks.ScoringWindow) ([]Operator, error) {
	if w.Last == 0 {
		return nil, nil
	}
	members, err := g.setAt(ctx, w.Last)
	if err != nil {
		return nil, err
	}
	out, err := g.group(ctx, members)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].History, err = g.store.WindowHistory(ctx, out[i].Keys, w); err != nil {
			return nil, fmt.Errorf("history of %s: %w", out[i].Address, err)
		}
	}
	return out, nil
}

// group returns the operators of a set's members, in set order, each once
// however many of its keys the set holds, their histories left unread.
func (g gatherer) group(ctx context.Context, members []blocks.Member) ([]Operator, error) {
	var out []Operator
	index := map[string]int{} // out's entries, by operator address
	for _, m := range members {
		addr, keys, err := g.link(ctx, m.Address)
		if err != nil {
			return nil, err
		}
		if i, met := index[addr]; met {
			out[i].Keys = appendNew(out[i].Keys, keys...)
			continue
		}
		index[addr] = len(out)
		out = append(out, Operator{
			Address:  addr,
			Name:     cmp.Or(g.profiles[addr].Moniker, g.names(m.Address)),
			Keys:     keys,
			Excluded: slices.Contains(g.exclude, addr),
		})
	}
	return out, nil
}

// setAt returns the members of the validator set of the stored height.
func (g gatherer) setAt(ctx context.Context, height int64) ([]blocks.Member, error) {
	hash, ok, err := g.store.ValsetHashAt(ctx, height)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("validator set at %d: not stored", height)
	}
	members, ok, err := g.store.Valset(ctx, hash)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("validator set %s at %d: not stored", hash, height)
	}
	return members, nil
}

// link returns the operator of key, a signing key in the set, and the
// operator's keys, key among them: key alone, its own operator, when no
// profile or event links it to one.
func (g gatherer) link(ctx context.Context, key string) (string, []string, error) {
	op, ok := g.signers[key]
	if !ok {
		var err error
		if op, err = g.store.OperatorOf(ctx, key); err != nil {
			return "", nil, err
		}
	}
	if op == "" {
		return key, []string{key}, nil
	}
	spans, err := g.store.OperatorKeys(ctx, op, g.profiles[op].SigningAddress)
	if err != nil {
		return "", nil, err
	}
	var keys []string
	for _, span := range spans {
		keys = appendNew(keys, span.Address)
	}
	// An event OperatorKeys leaves out, a rotation that never reached the
	// set, can be the one linking key: the set's key still counts.
	return op, appendNew(keys, key), nil
}

// appendNew appends to list each of items it does not hold.
func appendNew(list []string, items ...string) []string {
	for _, item := range items {
		if !slices.Contains(list, item) {
			list = append(list, item)
		}
	}
	return list
}

// check returns an error naming the first of Input's invariants in breaks,
// of those Gather does not hold by how it reads: each operator's history
// computed for the window's days, and each proposal flag within its heights.
func (in Input) check() error {
	w := in.Window
	for _, op := range in.Operators {
		s := op.History.Summary
		if !s.FirstDay.Equal(w.FirstDay) || !s.LastDay.Equal(w.LastDay) {
			return fmt.Errorf("history of %s: over %s to %s, the window is %s to %s", op.Address,
				s.FirstDay.Format(time.DateOnly), s.LastDay.Format(time.DateOnly), w.FirstDay.Format(time.DateOnly), w.LastDay.Format(time.DateOnly))
		}
	}
	for _, h := range in.ProposalFlags {
		if h < w.First || h > w.Last {
			return fmt.Errorf("proposal flag at %d: outside the window's heights %d to %d", h, w.First, w.Last)
		}
	}
	return nil
}

// ---- Readiness

// StateKind is how far the block store holds what the ranking needs.
type StateKind string

// The ranking is ready once the store holds what it needs, filling until
// then, and unavailable when the store never will.
const (
	Ready       StateKind = "ready"
	Filling     StateKind = "filling"
	Unavailable StateKind = "unavailable" // the source serves no block back to the span's start
)

// State is the ranking's readiness: the ranking needs every block of the
// recovery span (the 180 complete UTC days before today, back to genesis on
// a younger chain) and the events of every validator set change in it.
type State struct {
	Kind          StateKind     `json:"kind"`
	StoredShare   float64       `json:"stored_share"`        // while filling: the stored heights' share of the chain's whole history
	Left          time.Duration `json:"-"`                   // while filling blocks: the fill's time to genesis at its rate, 0 when unknown; encoded as left_s
	EventsPending bool          `json:"events_pending"`      // while filling: the span's blocks are stored, the events of a set change in it are not read yet
	Floor         int64         `json:"floor,omitzero"`      // while unavailable: the lowest height the source serves
	FloorTime     time.Time     `json:"floor_time,omitzero"` // its block's time
}

// MarshalJSON encodes s, Left as left_s: in whole seconds, rounded up, so
// that a known time left never encodes as 0, unknown.
func (s State) MarshalJSON() ([]byte, error) {
	type fields State // State's fields, without its MarshalJSON
	left := int64(s.Left / time.Second)
	if s.Left%time.Second > 0 {
		left++
	}
	return json.Marshal(struct {
		fields
		LeftS int64 `json:"left_s"`
	}{fields(s), left})
}

// Readiness returns the ranking's state as of now, from the fill's progress
// p and the block store. Ready: the store holds every block of the span,
// back to its start or to genesis, and no set change from the span's first
// stored height on has its events unread. Unavailable: the history reaches
// the source's floor, not the span's start. Filling otherwise.
func Readiness(ctx context.Context, s *blocks.Store, p blocks.Progress, now time.Time) (State, error) {
	w, err := s.ScoringWindow(ctx, now)
	if err != nil {
		return State{}, err
	}
	fromTime, stored, err := s.TimeAt(ctx, p.From)
	if err != nil {
		return State{}, err
	}
	switch {
	case stored && !fromTime.After(w.SpanStart) || p.Complete && p.Floor <= 1:
		return spanStoredState(ctx, s, p, w.SpanStart)
	case p.Complete && p.Floor > 1:
		// Reaching here implies time(floor) > SpanStart: block From is after it, and the floor at or above From.
		return floorState(ctx, s, p.Floor)
	}
	st := State{Kind: Filling, StoredShare: storedShare(p)}
	if p.Rate > 0 {
		st.Left = durationOf(float64(p.From-1) / p.Rate)
	}
	return st, nil
}

// spanStoredState returns the state of a store holding every block of the
// span: filling while a set change in it has its events unread, else ready.
func spanStoredState(ctx context.Context, s *blocks.Store, p blocks.Progress, spanStart time.Time) (State, error) {
	pending, err := eventsPending(ctx, s, spanStart)
	if err != nil {
		return State{}, err
	}
	if pending {
		return State{Kind: Filling, StoredShare: storedShare(p), EventsPending: true}, nil
	}
	return State{Kind: Ready}, nil
}

// floorState returns the state of a history that reaches the source's floor,
// the lowest height it serves, and not the span's start: unavailable.
func floorState(ctx context.Context, s *blocks.Store, floor int64) (State, error) {
	at, ok, err := s.TimeAt(ctx, floor)
	if err != nil {
		return State{}, err
	}
	if !ok {
		return State{}, fmt.Errorf("the source's floor %d: not stored", floor)
	}
	return State{Kind: Unavailable, Floor: floor, FloorTime: at}, nil
}

// durationOf returns secs seconds as a Duration, the longest one past its
// range.
func durationOf(secs float64) time.Duration {
	ns := secs * float64(time.Second)
	if ns >= math.MaxInt64 { // 2^63 as a float64: every ns below it converts without overflow
		return math.MaxInt64
	}
	return time.Duration(ns)
}

// storedShare returns the stored heights' share of the chain's whole
// history, 0 with none stored.
func storedShare(p blocks.Progress) float64 {
	if p.To == 0 {
		return 0
	}
	return float64(p.To-p.From+1) / float64(p.To)
}

// eventsPending reports whether a set change from the span's first stored
// height on has its events unread. Block times grow with height, so the
// newest event gap decides: it is in the span when its block is timed at or
// after spanStart, or is not stored, above the stored heights.
func eventsPending(ctx context.Context, s *blocks.Store, spanStart time.Time) (bool, error) {
	gaps, err := s.EventGaps(ctx)
	if err != nil || len(gaps) == 0 {
		return false, err
	}
	at, stored, err := s.TimeAt(ctx, gaps[len(gaps)-1])
	if err != nil {
		return false, err
	}
	return !stored || !at.Before(spanStart), nil
}

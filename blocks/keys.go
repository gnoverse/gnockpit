package blocks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
)

// KeySpan is a signing key of an operator and the heights it signed for.
type KeySpan struct {
	Address  string
	From, To int64 // From 0: before the stored history; To 0: still its key
}

// OperatorKeys returns an operator's signing keys over time, from its
// ValidatorAdded, applied ValoperRotated and ValidatorRemoved events, in the
// order they appear: an addition at H opens a key from H; a rotation at H
// closes the old key at H−1 and opens the new one from H; a removal at H
// closes the key at H−1. An addition of a key the stored set below H holds is
// a power change: it keeps the key's span, or opens one from before the
// stored history. A key closed without being opened starts before the stored
// history too. profileKey, when given and no event names it, is a key from
// before the stored history: its events are not read.
func (s *Store) OperatorKeys(ctx context.Context, operator, profileKey string) ([]KeySpan, error) {
	type keyEvent struct {
		height int64
		Event
	}
	var events []keyEvent
	err := s.each(ctx, func(rows *sql.Rows) error {
		var e keyEvent
		if err := rows.Scan(&e.height, &e.Type, &e.Address, &e.OldAddress, &e.NewAddress, &e.Applied); err != nil {
			return err
		}
		events = append(events, e)
		return nil
	}, `SELECT height, type, address, old_address, new_address, applied FROM blocks_events
	    WHERE operator = ? ORDER BY height, idx`, operator)
	if err != nil {
		return nil, fmt.Errorf("keys of %s: %w", operator, err)
	}
	var spans []KeySpan
	open := map[string]int{} // a key's span still open, by address
	openFrom := func(addr string, h int64) {
		spans = append(spans, KeySpan{Address: addr, From: h})
		open[addr] = len(spans) - 1
	}
	closeAt := func(addr string, h int64) {
		i, ok := open[addr]
		if !ok {
			spans = append(spans, KeySpan{Address: addr})
			i = len(spans) - 1
		}
		spans[i].To = h - 1
		delete(open, addr)
	}
	for _, e := range events {
		switch e.Type {
		case "ValidatorAdded":
			if _, ok := open[e.Address]; ok {
				continue
			}
			before, err := s.memberAt(ctx, e.Address, e.height-1)
			if err != nil {
				return nil, err
			}
			if before {
				openFrom(e.Address, 0)
			} else {
				openFrom(e.Address, e.height)
			}
		case "ValidatorRemoved":
			closeAt(e.Address, e.height)
		case "ValoperRotated":
			if e.Applied {
				closeAt(e.OldAddress, e.height)
				openFrom(e.NewAddress, e.height)
			}
		}
	}
	if profileKey != "" && !slices.ContainsFunc(spans, func(k KeySpan) bool { return k.Address == profileKey }) {
		spans = append(spans, KeySpan{Address: profileKey})
	}
	return spans, nil
}

// memberAt reports whether the stored block at height has addr in its set;
// false when the height is not stored.
func (s *Store) memberAt(ctx context.Context, addr string, height int64) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM blocks b JOIN blocks_valset_members m ON m.valset = b.valset
		 JOIN blocks_validators v ON v.id = m.validator WHERE b.height = ? AND v.address = ? LIMIT 1`, height, addr,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("membership of %s at %d: %w", addr, height, err)
	}
	return true, nil
}

// OperatorOf returns the operator an event links addr to, as an added,
// removed or rotated signing key, or "".
func (s *Store) OperatorOf(ctx context.Context, addr string) (string, error) {
	var op string
	err := s.db.QueryRowContext(ctx,
		`SELECT operator FROM blocks_events WHERE ? IN (address, old_address, new_address) ORDER BY height DESC LIMIT 1`, addr,
	).Scan(&op)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("operator of %s: %w", addr, err)
	}
	return op, nil
}

// Known reports whether addr is a member of a stored validator set.
func (s *Store) Known(ctx context.Context, addr string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM blocks_validators v JOIN blocks_valset_members m ON m.validator = v.id WHERE v.address = ? LIMIT 1`, addr,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("validator %s: %w", addr, err)
	}
	return true, nil
}

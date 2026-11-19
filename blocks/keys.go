package blocks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
// closes the key at H−1. A key closed without being opened starts before the
// stored history. With no event, the operator's key is profileKey alone, when
// one is given.
func (s *Store) OperatorKeys(ctx context.Context, operator, profileKey string) ([]KeySpan, error) {
	var spans []KeySpan
	open := map[string]int{} // a key's span still open, by address
	span := func(addr string) int {
		if i, ok := open[addr]; ok {
			return i
		}
		spans = append(spans, KeySpan{Address: addr})
		open[addr] = len(spans) - 1
		return len(spans) - 1
	}
	closeAt := func(addr string, h int64) {
		spans[span(addr)].To = h - 1
		delete(open, addr)
	}
	err := s.each(ctx, func(rows *sql.Rows) error {
		var h int64
		var e Event
		if err := rows.Scan(&h, &e.Type, &e.Address, &e.OldAddress, &e.NewAddress, &e.Applied); err != nil {
			return err
		}
		switch e.Type {
		case "ValidatorAdded":
			spans = append(spans, KeySpan{Address: e.Address, From: h})
			open[e.Address] = len(spans) - 1
		case "ValidatorRemoved":
			closeAt(e.Address, h)
		case "ValoperRotated":
			if !e.Applied {
				return nil
			}
			closeAt(e.OldAddress, h)
			spans = append(spans, KeySpan{Address: e.NewAddress, From: h})
			open[e.NewAddress] = len(spans) - 1
		}
		return nil
	}, `SELECT height, type, address, old_address, new_address, applied FROM blocks_events
	    WHERE operator = ? ORDER BY height, idx`, operator)
	if err != nil {
		return nil, fmt.Errorf("keys of %s: %w", operator, err)
	}
	if len(spans) == 0 && profileKey != "" {
		spans = []KeySpan{{Address: profileKey}}
	}
	return spans, nil
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

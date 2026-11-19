package blocks

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	"github.com/gnoverse/gnockpit/node"
)

// Event is a validator set event a block's transactions emitted.
type Event struct {
	Type       string // ValidatorAdded, ValidatorRemoved or ValoperRotated
	Operator   string
	Address    string // the signing address added or removed
	OldAddress string // a rotation's
	NewAddress string // a rotation's
	Power      int64
	Applied    bool // the change reached the set; false for a rotation of an operator outside it
}

// PutEvents stores the events that changed the validator set at height, read
// from the block at source, unless they are stored, and fills the height's
// event gap.
func (s *Store) PutEvents(ctx context.Context, height, source int64, events []Event) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM blocks_event_gaps WHERE height = ?`, height); err != nil {
		return fmt.Errorf("fill event gap %d: %w", height, err)
	}
	for i, e := range events {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO blocks_events(height, idx, source_height, type, operator, address, old_address, new_address, power, applied)
			 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			height, i, source, e.Type, e.Operator, e.Address, e.OldAddress, e.NewAddress, e.Power, e.Applied,
		); err != nil {
			return fmt.Errorf("insert event %d/%d: %w", height, i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit events: %w", err)
	}
	return nil
}

// AddEventGap records the validator set change at height as one whose events
// are not read yet.
func (s *Store) AddEventGap(ctx context.Context, height int64) error {
	if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO blocks_event_gaps(height) VALUES(?)`, height); err != nil {
		return fmt.Errorf("add event gap %d: %w", height, err)
	}
	return nil
}

// DropEventGap forgets the event gap at height, for a change whose events
// cannot be read or that is no change.
func (s *Store) DropEventGap(ctx context.Context, height int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blocks_event_gaps WHERE height = ?`, height); err != nil {
		return fmt.Errorf("drop event gap %d: %w", height, err)
	}
	return nil
}

// EventGaps returns the heights of the validator set changes whose events are
// not read yet, ascending.
func (s *Store) EventGaps(ctx context.Context) ([]int64, error) {
	var out []int64
	err := s.each(ctx, func(rows *sql.Rows) error {
		var h int64
		if err := rows.Scan(&h); err != nil {
			return err
		}
		out = append(out, h)
		return nil
	}, `SELECT height FROM blocks_event_gaps ORDER BY height`)
	if err != nil {
		return nil, fmt.Errorf("event gaps: %w", err)
	}
	return out, nil
}

// events reads the events behind each validator set change met in recs, or
// at the stored height just above them, from the block two below the change,
// where tm2 took the updates from. A failed read, of the events or of a stored
// height's set, is logged and recorded as an event gap: the heights stay
// stored, and a later pass reads it again.
func (t *Tracker) events(ctx context.Context, recs []Record) {
	if len(recs) == 0 {
		return
	}
	hashes := make(map[int64]string, len(recs))
	var top int64
	candidates := make([]int64, 0, len(recs)+1)
	for _, r := range recs {
		hashes[r.Height] = r.ValsetHash
		top = max(top, r.Height)
		candidates = append(candidates, r.Height)
	}
	candidates = append(candidates, top+1)
	hashAt := func(h int64) (string, bool, error) {
		if hash, ok := hashes[h]; ok {
			return hash, true, nil
		}
		return t.store.ValsetHashAt(ctx, h)
	}
	for _, h := range candidates {
		if h <= 2 {
			continue
		}
		change, err := t.isSetChange(h, hashAt)
		if err != nil {
			t.report("events", err)
			t.addEventGap(ctx, h)
			continue
		}
		if change {
			t.readEvents(ctx, h)
		}
	}
}

// isSetChange reports whether the stored height opens a new validator set,
// reading the sets' hashes with hashAt; false when either height is not
// stored.
func (t *Tracker) isSetChange(h int64, hashAt func(int64) (string, bool, error)) (bool, error) {
	cur, ok, err := hashAt(h)
	if err != nil || !ok {
		return false, err
	}
	below, ok, err := hashAt(h - 1)
	if err != nil || !ok {
		return false, err
	}
	return below != cur, nil
}

func (t *Tracker) addEventGap(ctx context.Context, height int64) {
	if err := t.store.AddEventGap(ctx, height); err != nil {
		t.report("events", err)
	}
}

// readEvents stores the validator set events behind the change at height,
// read from the block two below it, or records the change as an event gap.
func (t *Tracker) readEvents(ctx context.Context, height int64) {
	source := height - 2
	got, err := t.blockEvents(ctx, source)
	var evs []Event
	if err == nil {
		evs, err = setEvents(got)
	}
	if err != nil {
		t.report("events", fmt.Errorf("events of %d: %w", source, err))
		t.addEventGap(ctx, height)
		return
	}
	if len(evs) == 0 {
		t.logf("blocks: no validator set event at #%d for the change at #%d", source, height)
	}
	if err := t.store.PutEvents(ctx, height, source, evs); err != nil {
		t.report("events", err)
		return
	}
	t.report("events", nil)
}

// retryEventGaps reads again, every gapRetry, the events of the validator
// set changes whose read failed. A gap whose events are below the oldest block
// the source serves, or whose height turns out to open no new set, is dropped.
func (t *Tracker) retryEventGaps(ctx context.Context) {
	now := t.now()
	if now.Before(t.gapsAt) {
		return
	}
	t.gapsAt = now.Add(gapRetry)
	gaps, err := t.store.EventGaps(ctx)
	if err != nil {
		t.report("events", err)
		return
	}
	floor, err := t.store.Floor(ctx)
	if err != nil {
		t.report("events", err)
		return
	}
	stored := func(h int64) (string, bool, error) { return t.store.ValsetHashAt(ctx, h) }
	for _, h := range gaps {
		if h-2 < floor {
			t.logf("blocks: the events of the change at #%d are at #%d, below the oldest block the source serves", h, h-2)
			t.dropEventGap(ctx, h)
			continue
		}
		change, err := t.isSetChange(h, stored)
		switch {
		case err != nil:
			t.report("events", err)
		case change:
			t.readEvents(ctx, h)
		default:
			t.dropEventGap(ctx, h)
		}
	}
}

func (t *Tracker) dropEventGap(ctx context.Context, height int64) {
	if err := t.store.DropEventGap(ctx, height); err != nil {
		t.report("events", err)
	}
}

// setEvents keeps a block's validator set events: ValidatorAdded,
// ValidatorRemoved and ValoperRotated, from a realm under setsRealm. Any
// realm can emit an event of those names; only the system's change the set.
func setEvents(evs []node.BlockEvent) ([]Event, error) {
	var out []Event
	for _, e := range evs {
		if !strings.HasPrefix(e.PkgPath, setsRealm) {
			continue
		}
		var power int64
		if p, ok := e.Attrs["power"]; ok {
			var err error
			if power, err = strconv.ParseInt(p, 10, 64); err != nil {
				return nil, fmt.Errorf("%s power %q: %w", e.Type, p, err)
			}
		}
		switch e.Type {
		case "ValidatorAdded", "ValidatorRemoved":
			out = append(out, Event{Type: e.Type, Operator: e.Attrs["op"], Address: e.Attrs["signingAddr"], Power: power, Applied: true})
		case "ValoperRotated":
			out = append(out, Event{
				Type: e.Type, Operator: e.Attrs["op"], OldAddress: e.Attrs["oldAddr"], NewAddress: e.Attrs["newAddr"],
				Applied: e.Attrs["applied"] == "true",
			})
		}
	}
	return out, nil
}

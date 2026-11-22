package blocks

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"
)

// markValsetChanges records, in tx, the validator set changes that writing
// heights settles: a height is a change when it and the height below are both
// stored on different sets, so a written height settles itself and the height
// above, whichever direction the fill goes.
func markValsetChanges(ctx context.Context, tx *sql.Tx, heights []int64) error {
	if len(heights) == 0 {
		return nil
	}
	low, high := slices.Min(heights), slices.Max(heights)+1
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO blocks_valset_changes(height)
		 SELECT b.height FROM blocks b JOIN blocks p ON p.height = b.height - 1
		 WHERE b.height BETWEEN ? AND ? AND b.valset != p.valset`, low, high,
	); err != nil {
		return fmt.Errorf("mark validator set changes %d-%d: %w", low, high, err)
	}
	return nil
}

// fillValsetChanges records the validator set changes of the blocks stored
// before blocks_valset_changes existed, once: one scan of the stored blocks.
func (s *Store) fillValsetChanges(ctx context.Context) error {
	filled, err := s.meta(ctx, metaChangesFilled)
	if err != nil || filled != "" {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO blocks_valset_changes(height)
		 SELECT height FROM (
		   SELECT height, valset, LAG(height) OVER w AS below, LAG(valset) OVER w AS below_valset
		   FROM blocks WINDOW w AS (ORDER BY height))
		 WHERE below = height - 1 AND below_valset != valset`,
	); err != nil {
		return fmt.Errorf("fill validator set changes: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR REPLACE INTO blocks_meta(key, value) VALUES(?, ?)`, metaChangesFilled, "1",
	); err != nil {
		return fmt.Errorf("record the validator set changes filled: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit validator set changes: %w", err)
	}
	return nil
}

// ValsetChange is a change of the validator set at Height against the set of
// the block below; at height 1, the genesis set, all of it Added.
type ValsetChange struct {
	Height                  int64
	Time                    time.Time
	Added                   []Member // the new set's order
	Removed                 []Member // the old set's order
	Repowered               []PowerChange
	Rotated                 []Rotation
	SizeBefore, SizeAfter   int     // SizeBefore 0 at genesis
	PowerBefore, PowerAfter int64   // total voting power
	Events                  []Event // read at Height−2
	EventsPending           bool    // an event gap: not read yet
}

// PowerChange is a member that stays in the set with another voting power.
type PowerChange struct {
	Address       string
	Before, After int64
}

// Rotation is an operator's signing key replaced in the set: an applied
// ValoperRotated event linking a removed and an added member.
type Rotation struct {
	Operator, OldAddress, NewAddress string
	Power                            int64
}

const (
	valsetChangesBeforeQuery = `SELECT height FROM blocks_valset_changes WHERE height < ? ORDER BY height DESC LIMIT ?`
	valsetChangesInQuery     = `SELECT height FROM blocks_valset_changes WHERE height BETWEEN ? AND ? ORDER BY height`
	valsetChangeBelowQuery   = `SELECT height FROM blocks_valset_changes WHERE height < ? LIMIT 1`
)

// ValsetChanges returns at most limit validator set changes below height
// before, newest first; the genesis set comes last once height 1 is stored.
func (s *Store) ValsetChanges(ctx context.Context, before int64, limit int) ([]ValsetChange, error) {
	heights, err := s.changeHeights(ctx, valsetChangesBeforeQuery, before, limit)
	if err != nil {
		return nil, err
	}
	if len(heights) < limit && before > 1 {
		genesis, err := s.hasGenesis(ctx)
		if err != nil {
			return nil, err
		}
		if genesis {
			heights = append(heights, 1)
		}
	}
	return s.valsetChanges(ctx, heights)
}

// ValsetChangesIn returns the validator set changes from height from to height
// to, ascending; the genesis set first when 1 is in range and stored.
func (s *Store) ValsetChangesIn(ctx context.Context, from, to int64) ([]ValsetChange, error) {
	heights, err := s.changeHeights(ctx, valsetChangesInQuery, from, to)
	if err != nil {
		return nil, err
	}
	if from <= 1 && to >= 1 {
		genesis, err := s.hasGenesis(ctx)
		if err != nil {
			return nil, err
		}
		if genesis {
			heights = append([]int64{1}, heights...)
		}
	}
	return s.valsetChanges(ctx, heights)
}

// HasValsetChangeBelow tells whether a validator set change lies below
// height, the genesis set counting once height 1 is stored: whether
// ValsetChanges(height, …) returns any.
func (s *Store) HasValsetChangeBelow(ctx context.Context, height int64) (bool, error) {
	var below int64
	err := s.db.QueryRowContext(ctx, valsetChangeBelowQuery, height).Scan(&below)
	switch {
	case err == nil:
		return true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return false, fmt.Errorf("validator set changes below %d: %w", height, err)
	case height <= 1:
		return false, nil
	}
	return s.hasGenesis(ctx)
}

func (s *Store) changeHeights(ctx context.Context, query string, args ...any) ([]int64, error) {
	var out []int64
	err := s.each(ctx, func(rows *sql.Rows) error {
		var h int64
		if err := rows.Scan(&h); err != nil {
			return err
		}
		out = append(out, h)
		return nil
	}, query, args...)
	if err != nil {
		return nil, fmt.Errorf("validator set changes: %w", err)
	}
	return out, nil
}

func (s *Store) hasGenesis(ctx context.Context) (bool, error) {
	_, ok, err := s.TimeAt(ctx, 1)
	return ok, err
}

func (s *Store) valsetChanges(ctx context.Context, heights []int64) ([]ValsetChange, error) {
	out := make([]ValsetChange, 0, len(heights))
	for _, h := range heights {
		c, err := s.valsetChange(ctx, h)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// valsetChange reads the change at height h, a stored height whose set
// differs from the stored height below's, or 1.
func (s *Store) valsetChange(ctx context.Context, h int64) (ValsetChange, error) {
	c := ValsetChange{Height: h}
	var ms int64
	var hash string
	if err := s.db.QueryRowContext(ctx,
		`SELECT b.time_ms, v.hash FROM blocks b JOIN blocks_valsets v ON v.id = b.valset WHERE b.height = ?`, h,
	).Scan(&ms, &hash); err != nil {
		return ValsetChange{}, fmt.Errorf("validator set change %d: %w", h, err)
	}
	c.Time = time.UnixMilli(ms).UTC()
	cur, _, err := s.Valset(ctx, hash)
	if err != nil {
		return ValsetChange{}, err
	}
	var prev []Member
	if h > 1 {
		below, _, err := s.ValsetHashAt(ctx, h-1)
		if err != nil {
			return ValsetChange{}, err
		}
		if prev, _, err = s.Valset(ctx, below); err != nil {
			return ValsetChange{}, err
		}
	}
	c.diff(prev, cur)
	if err := s.changeEvents(ctx, &c); err != nil {
		return ValsetChange{}, err
	}
	c.linkRotations()
	return c, nil
}

// diff fills the members added, removed and re-weighted from prev to cur, and
// the sets' sizes and total power.
func (c *ValsetChange) diff(prev, cur []Member) {
	before := make(map[string]int64, len(prev))
	for _, m := range prev {
		before[m.Address] = m.Power
		c.PowerBefore += m.Power
	}
	after := make(map[string]bool, len(cur))
	for _, m := range cur {
		after[m.Address] = true
		c.PowerAfter += m.Power
		power, ok := before[m.Address]
		switch {
		case !ok:
			c.Added = append(c.Added, m)
		case power != m.Power:
			c.Repowered = append(c.Repowered, PowerChange{Address: m.Address, Before: power, After: m.Power})
		}
	}
	for _, m := range prev {
		if !after[m.Address] {
			c.Removed = append(c.Removed, m)
		}
	}
	c.SizeBefore, c.SizeAfter = len(prev), len(cur)
}

// changeEvents fills the change's stored events, or marks them pending while
// its event gap stands.
func (s *Store) changeEvents(ctx context.Context, c *ValsetChange) error {
	err := s.each(ctx, func(rows *sql.Rows) error {
		var e Event
		if err := rows.Scan(&e.Type, &e.Operator, &e.Address, &e.OldAddress, &e.NewAddress, &e.Power, &e.Applied); err != nil {
			return err
		}
		c.Events = append(c.Events, e)
		return nil
	}, `SELECT type, operator, address, old_address, new_address, power, applied FROM blocks_events WHERE height = ? ORDER BY idx`, c.Height)
	if err != nil {
		return fmt.Errorf("events of the validator set change %d: %w", c.Height, err)
	}
	var gaps int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocks_event_gaps WHERE height = ?`, c.Height).Scan(&gaps); err != nil {
		return fmt.Errorf("event gap %d: %w", c.Height, err)
	}
	c.EventsPending = gaps > 0
	return nil
}

// linkRotations turns each applied ValoperRotated event whose old address
// left the set and new address joined it into one Rotation.
func (c *ValsetChange) linkRotations() {
	for _, e := range c.Events {
		if e.Type != "ValoperRotated" || !e.Applied {
			continue
		}
		old := slices.IndexFunc(c.Removed, func(m Member) bool { return m.Address == e.OldAddress })
		added := slices.IndexFunc(c.Added, func(m Member) bool { return m.Address == e.NewAddress })
		if old < 0 || added < 0 {
			continue
		}
		c.Rotated = append(c.Rotated, Rotation{Operator: e.Operator, OldAddress: e.OldAddress, NewAddress: e.NewAddress, Power: c.Added[added].Power})
		c.Removed = slices.Delete(c.Removed, old, old+1)
		c.Added = slices.Delete(c.Added, added, added+1)
	}
}

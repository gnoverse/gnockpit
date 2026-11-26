package blocks

import (
	"context"
	"time"
)

// Progress is how far the stored history reaches.
type Progress struct {
	ChainID  string
	From, To int64     // lowest and highest stored heights; 0 when none is
	FromTime time.Time // time of block From
	Floor    int64     // lowest height the source serves, when it refused a lower one
	Complete bool      // From is 1, or the floor
	Rate     float64   // heights stored below From per second over the last rateWindow; 0 when unknown or complete
}

// read returns the stored history's progress, its rate 0: measuring it is
// publish's.
func (t *Tracker) read(ctx context.Context) (Progress, error) {
	chainID, err := t.store.ChainID(ctx)
	if err != nil {
		return Progress{}, err
	}
	low, high, err := t.store.Range(ctx)
	if err != nil {
		return Progress{}, err
	}
	floor, err := t.store.Floor(ctx)
	if err != nil {
		return Progress{}, err
	}
	var fromTime time.Time
	if low > 0 {
		if fromTime, _, err = t.store.TimeAt(ctx, low); err != nil {
			return Progress{}, err
		}
	}
	return Progress{
		ChainID: chainID, From: low, To: high, FromTime: fromTime, Floor: floor,
		Complete: low > 0 && low <= max(1, floor),
	}, nil
}

// publish refreshes Progress from the store, with the fill's rate, and logs
// the history's completion at the first publish that sees it complete.
func (t *Tracker) publish(ctx context.Context) error {
	p, err := t.read(ctx)
	if err != nil {
		return err
	}
	if p.Rate = t.measure(p.From); p.Complete {
		p.Rate = 0
	}
	t.mu.Lock()
	t.progress = p
	t.mu.Unlock()
	if p.Complete && !t.completeTold {
		t.logf("blocks: history complete from #%d (%s)", p.From, p.FromTime.Format(time.RFC3339))
	}
	t.completeTold = p.Complete
	return nil
}

// lowMark is the lowest stored height at a time.
type lowMark struct {
	at  time.Time
	low int64
}

// measure records low, the lowest stored height, and returns how fast it fell
// over the last rateWindow, in heights per second: 0 until two records span
// some time. An empty store records nothing: its first height is no progress.
func (t *Tracker) measure(low int64) float64 {
	if low == 0 {
		return 0
	}
	now := t.now()
	t.marks = append(t.marks, lowMark{at: now, low: low})
	for now.Sub(t.marks[0].at) > rateWindow {
		t.marks = t.marks[1:]
	}
	first := t.marks[0]
	span := now.Sub(first.at).Seconds()
	if span <= 0 {
		return 0
	}
	return float64(first.low-low) / span
}

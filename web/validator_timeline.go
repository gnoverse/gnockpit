package web

import (
	"cmp"
	"slices"

	"github.com/gnoverse/gnockpit/blocks"
)

// timelineEntry is one entry of a validator's timeline: its kind's fields
// are set, the other kinds' are nil.
type timelineEntry struct {
	Kind   string `json:"kind"` // recovered, recovering, outage, power, rotated, joined, left or genesis
	Height int64  `json:"height"`
	Time   string `json:"time"`
	*timelineRecovery
	*timelineOutage
	*timelinePower
	*timelineRotation
	*timelineMembership
}

// timelineRecovery is a recovered entry: Height is the first height signed
// after the restart at RestartHeight, Time the time that signature landed. A
// recovering entry, still absent since the restart, is at RestartHeight and
// the restart's time, without a duration.
type timelineRecovery struct {
	RestartHeight int64  `json:"restart_height"`
	DurationMs    *int64 `json:"duration_ms,omitempty"`
}

// timelineOutage is an outage entry: Height is its first height.
type timelineOutage struct {
	To      int64  `json:"to"`
	Blocks  int64  `json:"blocks"`
	EndTime string `json:"end_time"`
	Ongoing bool   `json:"ongoing"`
}

type timelinePower struct {
	Before int64 `json:"before"`
	After  int64 `json:"after"`
}

type timelineRotation struct {
	OldAddress string `json:"old_address"`
	NewAddress string `json:"new_address"`
}

// timelineMembership is a joined, left or genesis entry: the power the key
// came in or went out with.
type timelineMembership struct {
	Power int64 `json:"power"`
}

// setChangeEntries returns the timeline entries of the set changes that
// touch the keys addresses, ascending. A rotation is one entry; a key the
// genesis set holds is a genesis entry.
func setChangeEntries(changes []blocks.ValsetChange, addresses []string) []timelineEntry {
	var out []timelineEntry
	for _, c := range changes {
		entry := func(kind string) timelineEntry {
			return timelineEntry{Kind: kind, Height: c.Height, Time: c.Time.UTC().Format(historyTime)}
		}
		for _, m := range c.Added {
			if slices.Contains(addresses, m.Address) {
				e := entry("joined")
				if c.Height == 1 {
					e.Kind = "genesis"
				}
				e.timelineMembership = &timelineMembership{Power: m.Power}
				out = append(out, e)
			}
		}
		for _, m := range c.Removed {
			if slices.Contains(addresses, m.Address) {
				e := entry("left")
				e.timelineMembership = &timelineMembership{Power: m.Power}
				out = append(out, e)
			}
		}
		for _, p := range c.Repowered {
			if slices.Contains(addresses, p.Address) {
				e := entry("power")
				e.timelinePower = &timelinePower{Before: p.Before, After: p.After}
				out = append(out, e)
			}
		}
		for _, r := range c.Rotated {
			if slices.Contains(addresses, r.OldAddress) || slices.Contains(addresses, r.NewAddress) {
				e := entry("rotated")
				e.timelineRotation = &timelineRotation{OldAddress: r.OldAddress, NewAddress: r.NewAddress}
				out = append(out, e)
			}
		}
	}
	return out
}

// statusSince returns the set change entry (ascending entries) a status
// counts from: in the set, its latest join with no leave after; out of it,
// its latest leave. ok is false when the stored history holds none.
func statusSince(entries []timelineEntry, inSet bool) (since timelineEntry, ok bool) {
	var join, leave timelineEntry
	for _, e := range entries {
		switch e.Kind {
		case "joined", "genesis":
			join = e
		case "left":
			leave = e
		}
	}
	if inSet {
		return join, join.Height > 0 && join.Height >= leave.Height
	}
	return leave, leave.Height > 0 && leave.Height >= join.Height
}

// historyEntries returns the timeline: the set change entries, the outages
// and the recoveries, the pending ones as recovering, newest first.
func historyEntries(setEntries []timelineEntry, h blocks.History) []timelineEntry {
	out := slices.Clone(setEntries)
	for _, o := range h.Outages {
		out = append(out, timelineEntry{
			Kind: "outage", Height: o.First, Time: o.FirstTime.UTC().Format(historyTime),
			timelineOutage: &timelineOutage{To: o.Last, Blocks: o.Last - o.First + 1, EndTime: o.EndTime.UTC().Format(historyTime), Ongoing: o.Ongoing},
		})
	}
	for _, r := range h.Recoveries {
		if r.Pending {
			out = append(out, timelineEntry{
				Kind: "recovering", Height: r.Restart, Time: r.RestartTime.UTC().Format(historyTime),
				timelineRecovery: &timelineRecovery{RestartHeight: r.Restart},
			})
			continue
		}
		// Its precommit for SignedAt is in block SignedAt+1, whose time is
		// RestartTime + Duration.
		ms := r.Duration.Milliseconds()
		out = append(out, timelineEntry{
			Kind: "recovered", Height: r.SignedAt, Time: r.RestartTime.Add(r.Duration).UTC().Format(historyTime),
			timelineRecovery: &timelineRecovery{RestartHeight: r.Restart, DurationMs: &ms},
		})
	}
	slices.SortStableFunc(out, func(a, b timelineEntry) int { return cmp.Compare(b.Height, a.Height) })
	if out == nil {
		out = []timelineEntry{}
	}
	return out
}

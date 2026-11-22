//go:build page

package web

import (
	"crypto/sha256"
	"fmt"
	"slices"
	"testing"

	"github.com/gnoverse/gnockpit/blocks"
)

// timelineFillRate is the rate, in heights per second, of a history filling.
const timelineFillRate = 2.5

// timelineProgress is the fill progress the tracker reports for a history
// holding from to to, whose source serves the heights from floor on (0: all of
// them).
func timelineProgress(from, to, floor int64) blocks.Progress {
	p := blocks.Progress{ChainID: pageChainID, From: from, To: to, FromTime: healthyBlockTime(from), Floor: floor}
	p.Complete = from <= max(1, floor)
	if !p.Complete {
		p.Rate = timelineFillRate
	}
	return p
}

// timelineEra is a run of blocks signed by one validator set, from a height up
// to the next era's.
type timelineEra struct {
	from    int64
	members []blocks.Member
}

// timelinePutEras stores the heights from the first era's to to, at the healthy
// network's times, each era's on its set.
func timelinePutEras(t *testing.T, s *blocks.Store, to int64, eras ...timelineEra) {
	t.Helper()
	for i, era := range eras {
		end := to
		if i+1 < len(eras) {
			end = eras[i+1].from - 1
		}
		putBlocks(t, s, healthyBlockTime, timelineValsetHash(era.members), era.members, era.from, end, nil)
	}
}

// timelineValsetHash names a validator set after its members.
func timelineValsetHash(members []blocks.Member) string {
	return fmt.Sprintf("%X", sha256.Sum256([]byte(fmt.Sprint(members))))
}

// timelineRotated is the applied event of the operator's key rotating from the
// key of from to the key of to.
func timelineRotated(operator string, from, to pageValidator) blocks.Event {
	return blocks.Event{
		Type: "ValoperRotated", Operator: operator, OldAddress: from.address(), NewAddress: to.address(),
		Power: to.power, Applied: true,
	}
}

// timelineScenario is the healthy network with a block history of validator set
// changes, and that history's fill progress, which the test moves as it
// writes more history.
type timelineScenario struct {
	sc   *scenario
	fill *settableFill
}

// newTimelineScenario is the healthy network with the history seed writes,
// filled as p says, and the registry naming the validators of the history.
func newTimelineScenario(p blocks.Progress, seed func(t *testing.T, s *blocks.Store)) *timelineScenario {
	sc := healthyNetwork()
	sc.seed = seed
	for _, v := range []pageValidator{timelineEcho, timelineFoxtrot, timelineGolf, timelineHotel, timelineIndia, timelineJuliet, timelineKilo, timelineDeltaNew, timelineHotelNew} {
		if v.moniker != "" {
			sc.names[v.address()] = v.moniker
		}
	}
	fill := &settableFill{p: p}
	sc.fillFrom = fill
	return &timelineScenario{sc: sc, fill: fill}
}

// timelineOpenUnloaded opens a browser on tl's server, the page not loaded yet:
// the test loads it with navigate. The server reads the history's fill
// from tl.fill.
func timelineOpenUnloaded(t *testing.T, p profile, tl *timelineScenario, opts ...pageOption) *pageEnv {
	t.Helper()
	return openPage(t, p, tl.sc, append([]pageOption{noNavigate()}, opts...)...)
}

// timelineOpen loads the dashboard on tl's server.
func timelineOpen(t *testing.T, p profile, tl *timelineScenario, opts ...pageOption) *pageEnv {
	t.Helper()
	e := timelineOpenUnloaded(t, p, tl, opts...)
	e.navigate("/")
	return e
}

// The validators that join and leave the set over the history, beside the
// healthy network's four. delta's and hotel's keys rotate: the registry names
// delta's new key, not hotel's.
var (
	timelineEcho     = newPageValidator("echo", "echo", 10)
	timelineFoxtrot  = newPageValidator("foxtrot", "foxtrot", 10)
	timelineGolf     = newPageValidator("golf", "golf", 1)
	timelineHotel    = newPageValidator("hotel", "hotel", 2)
	timelineIndia    = newPageValidator("india", "india", 3)
	timelineJuliet   = newPageValidator("juliet", "juliet", 4)
	timelineKilo     = newPageValidator("kilo", "kilo", 1)
	timelineDeltaNew = newPageValidator("delta rotated", "delta", 5)
	timelineHotelNew = newPageValidator("hotel rotated", "", 2)
)

// timelineGenesisSet is the history's first set: alpha, bravo and charlie.
func timelineGenesisSet() []blocks.Member {
	return members(valAlpha, valBravo, valCharlie)
}

// timelineRichSets are the sets of timelineRich's eras after the genesis set's,
// from height 1,000 on.
func timelineRichSets() []timelineEra {
	rotated := []blocks.Member{
		memberAt(valAlpha, 12), memberAt(valBravo, 20), memberAt(valCharlie, 15),
		memberAt(timelineDeltaNew, 5), memberAt(timelineHotelNew, 2), memberAt(timelineIndia, 3), memberAt(timelineJuliet, 4),
	}
	return []timelineEra{
		{1000, members(valAlpha, valBravo, valCharlie, valDelta)},
		{2000, members(valAlpha, valBravo, valCharlie, valDelta, timelineEcho, timelineFoxtrot)},
		{2500, members(valAlpha, valBravo, valCharlie, valDelta, timelineEcho, timelineFoxtrot, timelineGolf, timelineHotel, timelineIndia)},
		{3000, members(valAlpha, valBravo, valCharlie, valDelta, timelineGolf, timelineHotel, timelineIndia)},
		{3500, []blocks.Member{
			memberAt(valAlpha, 10), memberAt(valBravo, 20), memberAt(valCharlie, 15),
			memberAt(valDelta, 5), memberAt(timelineGolf, 1), memberAt(timelineHotel, 2), memberAt(timelineIndia, 3),
		}},
		{4000, rotated},
		{4310, append(slices.Clone(rotated), memberAt(timelineKilo, 1))},
		{4320, rotated},
	}
}

// timelineRich is three days of history from the genesis set on, with a
// change of each kind: one, two and three validators joining, two and one
// leaving, power changes, key rotations (one change at 4,000 holds a join, a
// departure, a power change and two rotations), and a last change at the
// head whose events are not read yet.
func timelineRich() *timelineScenario {
	return newTimelineScenario(timelineProgress(1, pageHeight, 0), func(t *testing.T, s *blocks.Store) {
		timelinePutEras(t, s, pageHeight, append([]timelineEra{{1, timelineGenesisSet()}}, timelineRichSets()...)...)
		putValEvents(t, s, 4000, timelineRotated(valOperator, valDelta, timelineDeltaNew), timelineRotated(valOperatorTwo, timelineHotel, timelineHotelNew))
		if err := s.AddEventGap(t.Context(), pageHeight); err != nil {
			t.Fatal(err)
		}
	})
}

// timelineRow is what one row of the Timeline reads: the change's height and
// age, the symbol and its class, what happened, and the set's size. at is the
// change's height.
type timelineRow struct {
	at                     int64
	age, k, kind, what, sz string
}

// timelineRichRows are timelineRich's rows, newest first.
func timelineRichRows() []timelineRow {
	rotated := func(name string, from, to pageValidator) string {
		return name + " rotated its key " + shortAddr(from.address()) + " → " + shortAddr(to.address())
	}
	return []timelineRow{
		{4320, "#4,320 · just now", "−", "vt-rm", "kilo left power 1events pending", "8 → 7 validators"},
		{4310, "#4,310 · 10 min ago", "+", "vt-add", "kilo joined power 1", "7 → 8 validators"},
		{4000, "#4,000 · 5 h ago", "+", "vt-add", "juliet joined power 4", "7 validators"},
		{4000, "", "−", "vt-rm", "golf left power 1", ""},
		{4000, "", "↕", "vt-pow", "alpha power 10 → 12", ""},
		{4000, "", "⇄", "vt-rot", rotated("delta", valDelta, timelineDeltaNew), ""},
		{4000, "", "⇄", "vt-rot", rotated(shortAddr(timelineHotelNew.address()), timelineHotel, timelineHotelNew), ""},
		{3500, "#3,500 · 14 h ago", "↕", "vt-pow", "bravo power 10 → 20", "7 validators"},
		{3500, "", "↕", "vt-pow", "charlie power 10 → 15", ""},
		{3000, "#3,000 · 22 h ago", "−", "vt-rm", "echo and foxtrot left power 10 each", "9 → 7 validators"},
		{2500, "#2,500 · 30 h ago", "+", "vt-add", "golf, hotel and india joined power 1, 2, 3", "6 → 9 validators"},
		{2000, "#2,000 · 39 h ago", "+", "vt-add", "echo and foxtrot joined power 10 each", "4 → 6 validators"},
		{1000, "#1,000 · 2 d ago", "+", "vt-add", shortAddr(valDelta.address()) + " joined power 5", "3 → 4 validators"},
		{1, "#1 · 3 d ago", "◆", "vt-gen", "Genesis set", "3 validators"},
	}
}

// timelineUTCMinute is the time of height h as the page writes it to the
// minute.
func timelineUTCMinute(h int64) string {
	return healthyBlockTime(h).UTC().Format("2006-01-02 15:04") + " UTC"
}

// timelineFlipSet is the set of height h of a flipping history: alpha, bravo
// and charlie, and delta at even heights.
func timelineFlipSet(h int64) []blocks.Member {
	if h%2 == 0 {
		return members(valAlpha, valBravo, valCharlie, valDelta)
	}
	return timelineGenesisSet()
}

// timelineFlipEras are one-block eras for each height from from to to on its
// timelineFlipSet: each height above from is a change, delta joining or
// leaving.
func timelineFlipEras(from, to int64) []timelineEra {
	eras := make([]timelineEra, 0, to-from+1)
	for h := from; h <= to; h++ {
		eras = append(eras, timelineEra{h, timelineFlipSet(h)})
	}
	return eras
}

// timelinePutFlips stores the heights from to to of a flipping history.
func timelinePutFlips(t *testing.T, s *blocks.Store, from, to int64) {
	t.Helper()
	timelinePutEras(t, s, to, timelineFlipEras(from, to)...)
}

// timelinePaged is three days of history from the genesis set on, delta
// joining and leaving at each height from 4,200: 121 changes and the genesis
// set, read in pages of 50, 50 and 22.
func timelinePaged() *timelineScenario {
	return newTimelineScenario(timelineProgress(1, pageHeight, 0), func(t *testing.T, s *blocks.Store) {
		timelinePutEras(t, s, 4199, timelineEra{1, timelineGenesisSet()})
		timelinePutFlips(t, s, 4200, pageHeight)
	})
}

package ranking

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
	_ "modernc.org/sqlite"
)

// ---- Fixtures

// gatherNow is the time the tests read the store as of: noon UTC on today,
// whose scoring window runs from 2026-10-26 to 2026-11-24 and whose recovery
// span starts on spanStart.
var (
	gatherNow = today.Add(12 * time.Hour)
	spanStart = today.AddDate(0, 0, -180)
)

// The test chain holds a block every 6 hours, from two days before the
// window's first day to today 06:00: its window holds heights windowFirst to
// windowLast, and today's first block is stored.
const (
	windowFirst = 9
	windowLast  = 128
	chainHead   = 130
)

// chainTime returns the time of block h of the test chain.
func chainTime(h int64) time.Time {
	return today.AddDate(0, 0, -32).Add(time.Duration(h-1) * 6 * time.Hour)
}

// openStore returns an empty block store on an in-memory database, and the
// database.
func openStore(t *testing.T) (*blocks.Store, *sql.DB) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// A single connection keeps the in-memory database alive across queries.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	s, err := blocks.NewStore(context.Background(), db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s, db
}

func newStore(t *testing.T) *blocks.Store {
	t.Helper()
	s, _ := openStore(t)
	return s
}

// testBlock is a block of a test chain: its time, its set's hash, the
// members absent from it, and why its proposer check failed, if it did.
type testBlock struct {
	at     time.Time
	set    string
	absent []string
	flag   string
}

// testChain returns the test chain's blocks 1 to chainHead, block h on the
// set setAt(h).
func testChain(setAt func(h int64) string) []testBlock {
	out := make([]testBlock, chainHead)
	for i := range out {
		h := int64(i + 1)
		out[i] = testBlock{at: chainTime(h), set: setAt(h)}
	}
	return out
}

// always is a set schedule holding the set hash at every height.
func always(hash string) func(int64) string { return func(int64) string { return hash } }

// switchAt is a set schedule holding the set before below height h, after
// from it on.
func switchAt(h int64, before, after string) func(int64) string {
	return func(height int64) string {
		if height < h {
			return before
		}
		return after
	}
}

// onSet returns a block at each of times, all on the set hash.
func onSet(hash string, times []time.Time) []testBlock {
	out := make([]testBlock, len(times))
	for i, at := range times {
		out[i] = testBlock{at: at, set: hash}
	}
	return out
}

// spaced returns n times step apart, the last at last.
func spaced(n int, step time.Duration, last time.Time) []time.Time {
	out := make([]time.Time, n)
	for i := range out {
		out[i] = last.Add(-time.Duration(n-1-i) * step)
	}
	return out
}

// writeChain stores chain at heights from on, on sets: each set's members by
// its hash, each of power 1. A member present signs the block, 100 ms after
// the median.
func writeChain(t *testing.T, s *blocks.Store, sets map[string][]string, from int64, chain []testBlock) {
	t.Helper()
	ctx := context.Background()
	for _, hash := range slices.Sorted(maps.Keys(sets)) {
		members := make([]blocks.Member, len(sets[hash]))
		for i, addr := range sets[hash] {
			members[i] = blocks.Member{Address: addr, PubKey: "pub-" + addr, Power: 1}
		}
		if err := s.PutValset(ctx, hash, members); err != nil {
			t.Fatal(err)
		}
	}
	recs := make([]blocks.Record, len(chain))
	for i, b := range chain {
		members := sets[b.set]
		r := blocks.Record{Height: from + int64(i), Time: b.at, Proposer: members[0], ValsetHash: b.set, Flag: b.flag}
		for _, addr := range members {
			if slices.Contains(b.absent, addr) {
				r.Absences = append(r.Absences, blocks.Absence{Address: addr, Kind: blocks.Absent})
				continue
			}
			r.Delays = append(r.Delays, blocks.Delay{Address: addr, Ms: 100})
		}
		recs[i] = r
	}
	if err := s.Write(ctx, recs); err != nil {
		t.Fatal(err)
	}
}

// putEvents stores the events of the set change at height, read from the
// block two below it.
func putEvents(t *testing.T, s *blocks.Store, height int64, evs ...blocks.Event) {
	t.Helper()
	if err := s.PutEvents(context.Background(), height, height-2, evs); err != nil {
		t.Fatal(err)
	}
}

// nodeName is the names the tests give Gather: a key's name by its node.
func nodeName(key string) string { return "node of " + key }

// gather returns Gather's input as of gatherNow.
func gather(t *testing.T, s *blocks.Store, profiles Profiles, exclude ...string) Input {
	t.Helper()
	in, err := Gather(context.Background(), s, profiles, nodeName, exclude, gatherNow)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	return in
}

// addressesOf returns the addresses of in's operators, in order.
func addressesOf(in Input) []string {
	out := []string{}
	for _, op := range in.Operators {
		out = append(out, op.Address)
	}
	return out
}

// operatorIn returns in's operator addr.
func operatorIn(t *testing.T, in Input, addr string) Operator {
	t.Helper()
	for _, op := range in.Operators {
		if op.Address == addr {
			return op
		}
	}
	t.Fatalf("operators %v: no %s", addressesOf(in), addr)
	return Operator{}
}

// ---- Gather

func TestGatherJoinsARotatedOperator(t *testing.T) {
	const rotation = 70 // the set change giving g1opa's blocks to its new key, inside the window
	s := newStore(t)
	writeChain(t, s, map[string][]string{"OLD": {"g1keya1", "g1keyb"}, "NEW": {"g1keya2", "g1keyb"}}, 1, testChain(switchAt(rotation, "OLD", "NEW")))
	putEvents(t, s, rotation, blocks.Event{Type: "ValoperRotated", Operator: "g1opa", OldAddress: "g1keya1", NewAddress: "g1keya2", Applied: true})
	in := gather(t, s, nil)
	if in.Window.First != windowFirst || in.Window.Last != windowLast {
		t.Fatalf("window heights = %d to %d, want %d to %d", in.Window.First, in.Window.Last, windowFirst, windowLast)
	}
	if got, want := addressesOf(in), []string{"g1opa", "g1keyb"}; !reflect.DeepEqual(got, want) {
		t.Errorf("operators = %v, want %v: the rotated keys are one operator", got, want)
	}
	a := operatorIn(t, in, "g1opa")
	if want := []string{"g1keya1", "g1keya2"}; !reflect.DeepEqual(a.Keys, want) {
		t.Errorf("g1opa's keys = %v, want %v", a.Keys, want)
	}
	if got, want := a.History.Summary.InSet, int64(windowLast-windowFirst+1); got != want {
		t.Errorf("g1opa in the set = %d heights, want %d: every height of the window, under one key or the other", got, want)
	}
}

func TestGatherLinksAGenesisKeyByItsProfile(t *testing.T) {
	const rotation = 70
	sets := map[string][]string{"GEN": {"g1keyg", "g1keyb"}, "ROT": {"g1keyg2", "g1keyb"}}
	for _, tc := range []struct {
		name       string
		setAt      func(int64) string
		events     []blocks.Event // at rotation
		profileKey string
		wantKeys   []string
	}{
		{"never rotated", always("GEN"), nil, "g1keyg", []string{"g1keyg"}},
		{
			"rotated later", switchAt(rotation, "GEN", "ROT"),
			[]blocks.Event{{Type: "ValoperRotated", Operator: "g1opg", OldAddress: "g1keyg", NewAddress: "g1keyg2", Applied: true}},
			"g1keyg2",
			[]string{"g1keyg", "g1keyg2"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			writeChain(t, s, sets, 1, testChain(tc.setAt))
			putEvents(t, s, rotation, tc.events...)
			profiles := Profiles{"g1opg": {Operator: "g1opg", Moniker: "Genesis", SigningAddress: tc.profileKey}}
			in := gather(t, s, profiles, "g1opg")
			if got, want := addressesOf(in), []string{"g1opg", "g1keyb"}; !reflect.DeepEqual(got, want) {
				t.Fatalf("operators = %v, want %v", got, want)
			}
			if g := operatorIn(t, in, "g1opg"); !reflect.DeepEqual(g.Keys, tc.wantKeys) || !g.Excluded {
				t.Errorf("g1opg = keys %v, excluded %v; want keys %v, excluded", g.Keys, g.Excluded, tc.wantKeys)
			}
			if operatorIn(t, in, "g1keyb").Excluded {
				t.Errorf("g1keyb excluded, want not: the list names g1opg only")
			}
		})
	}
}

func TestGatherRanksAnUnlinkedKeyAlone(t *testing.T) {
	s := newStore(t)
	writeChain(t, s, map[string][]string{"SET": {"g1keyu"}}, 1, testChain(always("SET")))
	// A profile naming another key links nothing here; the exclusion list
	// names the key itself.
	in := gather(t, s, Profiles{"g1opa": {Operator: "g1opa", Moniker: "Alpha", SigningAddress: "g1keya"}}, "g1keyu")
	u := operatorIn(t, in, "g1keyu")
	if !reflect.DeepEqual(u.Keys, []string{"g1keyu"}) || u.Name != "node of g1keyu" || !u.Excluded {
		t.Errorf("g1keyu = keys %v, name %q, excluded %v; want keys [g1keyu], name %q, excluded", u.Keys, u.Name, u.Excluded, "node of g1keyu")
	}
	if got, want := u.History.Summary.InSet, int64(windowLast-windowFirst+1); got != want {
		t.Errorf("g1keyu in the set = %d heights, want %d", got, want)
	}
}

func TestGatherLeavesOutOperatorsThatLeftTheSet(t *testing.T) {
	const removal = windowLast + 1 - 10*4 // 10 days of 4 blocks before the window's end
	s := newStore(t)
	writeChain(t, s, map[string][]string{"BOTH": {"g1keya", "g1keyb"}, "B": {"g1keyb"}}, 1, testChain(switchAt(removal, "BOTH", "B")))
	putEvents(t, s, removal, blocks.Event{Type: "ValidatorRemoved", Operator: "g1opa", Address: "g1keya", Applied: true})
	if got, want := addressesOf(gather(t, s, nil)), []string{"g1keyb"}; !reflect.DeepEqual(got, want) {
		t.Errorf("operators = %v, want %v: g1opa left the set before the window's last height, after 20 days in it", got, want)
	}
}

func TestGatherNames(t *testing.T) {
	s := newStore(t)
	writeChain(t, s, map[string][]string{"SET": {"g1keya", "g1keyb", "g1keyc", "g1keyd"}}, 1, testChain(always("SET")))
	putEvents(t, s, 50, blocks.Event{Type: "ValidatorAdded", Operator: "g1opd", Address: "g1keyd", Power: 1, Applied: true})
	in := gather(t, s, Profiles{
		"g1opa": {Operator: "g1opa", Moniker: "Alpha", SigningAddress: "g1keya"},
		"g1opc": {Operator: "g1opc", SigningAddress: "g1keyc"},
		"g1opd": {Operator: "g1opd", Moniker: "Delta", SigningAddress: "g1keyd2"},
	})
	for _, tc := range []struct{ name, operator, want string }{
		{"the profile's moniker", "g1opa", "Alpha"},
		{"no profile", "g1keyb", "node of g1keyb"},
		{"a profile without a moniker", "g1opc", "node of g1keyc"},
		{"the profile of an operator its event links", "g1opd", "Delta"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := operatorIn(t, in, tc.operator).Name; got != tc.want {
				t.Errorf("name of %s = %q, want %q", tc.operator, got, tc.want)
			}
		})
	}
}

func TestGatherLinksAKeyByAProfileFirst(t *testing.T) {
	for _, tc := range []struct {
		name     string
		profiles Profiles
		want     string
	}{
		{"over an event", Profiles{"g1opp": {SigningAddress: "g1keyk"}}, "g1opp"},
		{
			"several profiles naming it: the first operator in byte order",
			Profiles{
				"g1opv": {SigningAddress: "g1keyk"}, "g1opw": {SigningAddress: "g1keyk"}, "g1opp": {SigningAddress: "g1keyk"},
				"g1opx": {SigningAddress: "g1keyk"}, "g1opy": {SigningAddress: "g1keyk"},
			},
			"g1opp",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			writeChain(t, s, map[string][]string{"SET": {"g1keyk"}}, 1, testChain(always("SET")))
			putEvents(t, s, 50, blocks.Event{Type: "ValidatorAdded", Operator: "g1ope", Address: "g1keyk", Power: 1, Applied: true})
			if got, want := addressesOf(gather(t, s, tc.profiles)), []string{tc.want}; !reflect.DeepEqual(got, want) {
				t.Errorf("operators = %v, want %v", got, want)
			}
		})
	}
}

func TestGatherJoinsKeysAddedOneAfterTheOther(t *testing.T) {
	const swap = 70 // the set change removing g1keya1 and adding g1keya2, inside the window
	s := newStore(t)
	sets := map[string][]string{"B": {"g1keyb"}, "A1": {"g1keya1", "g1keyb"}, "A2": {"g1keya2", "g1keyb"}}
	writeChain(t, s, sets, 1, testChain(func(h int64) string {
		switch {
		case h < windowFirst-4:
			return "B"
		case h < swap:
			return "A1"
		default:
			return "A2"
		}
	}))
	putEvents(t, s, windowFirst-4, blocks.Event{Type: "ValidatorAdded", Operator: "g1opa", Address: "g1keya1", Power: 1, Applied: true})
	putEvents(t, s, swap,
		blocks.Event{Type: "ValidatorRemoved", Operator: "g1opa", Address: "g1keya1", Applied: true},
		blocks.Event{Type: "ValidatorAdded", Operator: "g1opa", Address: "g1keya2", Power: 1, Applied: true},
	)
	in := gather(t, s, nil)
	if got, want := addressesOf(in), []string{"g1opa", "g1keyb"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("operators = %v, want %v", got, want)
	}
	a := operatorIn(t, in, "g1opa")
	if want := []string{"g1keya1", "g1keya2"}; !reflect.DeepEqual(a.Keys, want) {
		t.Errorf("g1opa's keys = %v, want %v", a.Keys, want)
	}
	if got, want := a.History.Summary.InSet, int64(windowLast-windowFirst+1); got != want {
		t.Errorf("g1opa in the set = %d heights, want %d: every height of the window, under one key or the other", got, want)
	}
}

func TestGatherJoinsTwoKeysOfAnOperatorInTheSet(t *testing.T) {
	// A store the chain cannot produce: two keys of g1opa in the set at
	// once, the second linked to it only by a rotation that never reached
	// the set, which g1opa's keys over time leave out.
	s := newStore(t)
	sets := map[string][]string{"B": {"g1keyb"}, "A12": {"g1keya1", "g1keya2", "g1keyb"}}
	writeChain(t, s, sets, 1, testChain(switchAt(windowFirst-4, "B", "A12")))
	putEvents(t, s, windowFirst-4, blocks.Event{Type: "ValidatorAdded", Operator: "g1opa", Address: "g1keya1", Power: 1, Applied: true})
	putEvents(t, s, 50, blocks.Event{Type: "ValoperRotated", Operator: "g1opa", OldAddress: "g1keyx", NewAddress: "g1keya2", Applied: false})
	in := gather(t, s, nil)
	if got, want := addressesOf(in), []string{"g1opa", "g1keyb"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("operators = %v, want %v: g1opa once, though two of its keys are in the set", got, want)
	}
	if got, want := operatorIn(t, in, "g1opa").Keys, []string{"g1keya1", "g1keya2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("g1opa's keys = %v, want %v: each of its keys in the set", got, want)
	}
}

func TestGatherKeepsAnOperatorsKeyInTheSet(t *testing.T) {
	s := newStore(t)
	writeChain(t, s, map[string][]string{"SET": {"g1keya"}}, 1, testChain(always("SET")))
	// The one event naming g1keya, a rotation that never reached the set,
	// links it to g1opa; g1opa's keys over time leave it out.
	putEvents(t, s, 50, blocks.Event{Type: "ValoperRotated", Operator: "g1opa", OldAddress: "g1keyx", NewAddress: "g1keya", Applied: false})
	a := operatorIn(t, gather(t, s, nil), "g1opa")
	if !reflect.DeepEqual(a.Keys, []string{"g1keya"}) {
		t.Errorf("g1opa's keys = %v, want [g1keya]", a.Keys)
	}
	if got, want := a.History.Summary.InSet, int64(windowLast-windowFirst+1); got != want {
		t.Errorf("g1opa in the set = %d heights, want %d", got, want)
	}
}

func TestGatherReadsHistoriesAsOfTheWindowsEnd(t *testing.T) {
	// Two stores, the same up to today's first block, windowLast+1: one holds
	// no block after it, the other 20 more, a minute apart. g1keya is absent
	// from 127, across the restart gaps at 127 and 128 (blocks 6 h apart), to
	// 140: it signs again at 141, which only the second store holds. g1keyb,
	// g1opb's by an event of the window, is absent from 132 to 145, an outage
	// after the window, then rotates to g1keyb2 at 146: an event the second
	// store holds only, which adds g1keyb2 to g1opb's keys and nothing else.
	sets := map[string][]string{"SET": {"g1keya", "g1keyb"}, "ROT": {"g1keya", "g1keyb2"}}
	chain := testChain(always("SET"))[:windowLast+1]
	for i := range 20 {
		set := "SET"
		if windowLast+2+i >= 146 {
			set = "ROT"
		}
		chain = append(chain, testBlock{at: today.Add(time.Duration(i+1) * time.Minute), set: set})
	}
	for h := int64(127); h <= 145; h++ {
		if h <= 140 {
			chain[h-1].absent = append(chain[h-1].absent, "g1keya")
		}
		if h >= 132 {
			chain[h-1].absent = append(chain[h-1].absent, "g1keyb")
		}
	}
	early, late := newStore(t), newStore(t)
	writeChain(t, early, sets, 1, chain[:windowLast+1])
	writeChain(t, late, sets, 1, chain)
	for _, s := range []*blocks.Store{early, late} {
		putEvents(t, s, 50, blocks.Event{Type: "ValidatorAdded", Operator: "g1opb", Address: "g1keyb", Power: 1, Applied: true})
	}
	putEvents(t, late, 146, blocks.Event{Type: "ValoperRotated", Operator: "g1opb", OldAddress: "g1keyb", NewAddress: "g1keyb2", Applied: true})
	inEarly, inLate := gather(t, early, nil), gather(t, late, nil)
	if got, want := addressesOf(inLate), addressesOf(inEarly); !reflect.DeepEqual(got, want) {
		t.Fatalf("operators with blocks and events stored after the window = %v, want %v", got, want)
	}
	if got, want := operatorIn(t, inLate, "g1opb").Keys, []string{"g1keyb", "g1keyb2"}; !reflect.DeepEqual(got, want) {
		t.Errorf("g1opb's keys with a rotation stored after the window = %v, want %v", got, want)
	}
	for _, addr := range addressesOf(inEarly) {
		a, b := operatorIn(t, inEarly, addr).History, operatorIn(t, inLate, addr).History
		for _, part := range []struct {
			name         string
			early, later any
		}{{"days", a.Days, b.Days}, {"outages", a.Outages, b.Outages}, {"recoveries", a.Recoveries, b.Recoveries}, {"summary", a.Summary, b.Summary}} {
			if !reflect.DeepEqual(part.early, part.later) {
				t.Errorf("%s's %s = %+v; with 20 blocks more, %+v; want the same", addr, part.name, part.early, part.later)
			}
		}
	}
	for _, in := range []Input{inEarly, inLate} {
		for i := range in.Operators {
			in.Operators[i].Keys = nil
		}
	}
	if !reflect.DeepEqual(inEarly, inLate) {
		t.Errorf("Gather's input, keys aside, differs with 20 blocks stored after the window's end and today's first block")
	}
	publishedEarly, err := json.Marshal(withoutKeys(Rank(inEarly)))
	if err != nil {
		t.Fatal(err)
	}
	publishedLate, err := json.Marshal(withoutKeys(Rank(inLate)))
	if err != nil {
		t.Fatal(err)
	}
	if string(publishedEarly) != string(publishedLate) {
		t.Errorf("published ranking, keys aside = %s;\nwith 20 blocks more, %s; want the same", publishedEarly, publishedLate)
	}
	recs := operatorIn(t, inLate, "g1keya").History.Recoveries
	if n := len(recs); n < 2 || recs[n-2].Restart != 127 || !recs[n-2].Pending || recs[n-1].Restart != 128 || !recs[n-1].Pending {
		t.Errorf("g1keya's recoveries end with %+v, want the restarts at 127 and 128 pending: it signs again after the window", recs[max(0, len(recs)-2):])
	}
}

func TestGatherLinksAGenesisKeyRotatedAfterTheWindow(t *testing.T) {
	// g1opg's genesis key g1keyg, linked by its profile only, signs every
	// block of the window and rotates to g1keyg2 after it: the set change is
	// stored at chainHead, above windowLast+1, and the profile, read as the
	// ranking is computed, names g1keyg2. The deployment excludes g1opg.
	gatherAt := func(rotated bool) Input {
		s := newStore(t)
		writeChain(t, s, map[string][]string{"GEN": {"g1keyg", "g1keyb"}}, 1, testChain(always("GEN")))
		profileKey := "g1keyg"
		if rotated {
			putEvents(t, s, chainHead, blocks.Event{Type: "ValoperRotated", Operator: "g1opg", OldAddress: "g1keyg", NewAddress: "g1keyg2", Applied: true})
			profileKey = "g1keyg2"
		}
		return gather(t, s, Profiles{"g1opg": {Operator: "g1opg", Moniker: "Genesis", SigningAddress: profileKey}}, "g1opg")
	}
	before, after := gatherAt(false), gatherAt(true)
	for _, in := range []Input{before, after} {
		if got, want := addressesOf(in), []string{"g1opg", "g1keyb"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("operators = %v, want %v: g1keyg is g1opg's", got, want)
		}
		if g := operatorIn(t, in, "g1opg"); !g.Excluded || !slices.Contains(g.Keys, "g1keyg") {
			t.Errorf("g1opg = keys %v, excluded %v; want g1keyg among its keys, excluded", g.Keys, g.Excluded)
		}
	}
	if got, want := withoutKeys(Rank(after)), withoutKeys(Rank(before)); !reflect.DeepEqual(got, want) {
		t.Errorf("ranking after the rotation = %+v, want the one before, keys aside: %+v", got, want)
	}
}

// withoutKeys returns r with no entry's keys: a key rotated in after the
// window can join an operator's keys, and nothing else.
func withoutKeys(r Result) Result {
	r.Entries = slices.Clone(r.Entries)
	for i := range r.Entries {
		r.Entries[i].Keys = nil
	}
	return r
}

func TestGatherFillsTheProposalFlags(t *testing.T) {
	s := newStore(t)
	chain := testChain(always("SET"))
	for _, h := range []int64{windowFirst - 1, 50, windowLast + 1} {
		chain[h-1].flag = "round 0's proposer unknown"
	}
	writeChain(t, s, map[string][]string{"SET": {"g1keya"}}, 1, chain)
	if got, want := gather(t, s, nil).ProposalFlags, []int64{50}; !reflect.DeepEqual(got, want) {
		t.Errorf("proposal flags = %v, want %v: the window's only", got, want)
	}
}

func TestGatherNeedsACompleteWindow(t *testing.T) {
	s := newStore(t)
	writeChain(t, s, map[string][]string{"SET": {"g1keya"}}, 1, testChain(always("SET"))[:windowLast])
	_, err := Gather(context.Background(), s, nil, nodeName, nil, gatherNow)
	if err == nil || !strings.Contains(err.Error(), "not complete") {
		t.Errorf("Gather without today's first block = %v, want an error saying the window is not complete", err)
	}
}

func TestGatherWithoutABlockInTheWindow(t *testing.T) {
	s := newStore(t)
	// Blocks up to 40 days before today, then today's first.
	times := append(spaced(10, time.Hour, today.AddDate(0, 0, -40)), today.Add(time.Hour))
	writeChain(t, s, map[string][]string{"SET": {"g1keya"}}, 1, onSet("SET", times))
	in := gather(t, s, nil)
	if !in.Window.Complete || in.Window.Last != 0 || len(in.Operators) != 0 || len(in.ProposalFlags) != 0 {
		t.Errorf("Gather = window %+v, operators %v, flags %v; want a complete window without a height, no operator, no flag",
			in.Window, addressesOf(in), in.ProposalFlags)
	}
}

func TestGatherFailsWithoutTheSetAtTheWindowsEnd(t *testing.T) {
	// A store missing the set of the window's last height, which Write never
	// leaves: its hash, or its members.
	for _, table := range []string{"blocks_valsets", "blocks_valset_members"} {
		t.Run(table, func(t *testing.T) {
			s, db := openStore(t)
			writeChain(t, s, map[string][]string{"SET": {"g1keya"}}, 1, testChain(always("SET")))
			if _, err := db.Exec("DELETE FROM " + table); err != nil {
				t.Fatal(err)
			}
			_, err := Gather(context.Background(), s, nil, nodeName, nil, gatherNow)
			if want := fmt.Sprintf("at %d", windowLast); err == nil || !strings.Contains(err.Error(), want) {
				t.Errorf("Gather = %v, want an error naming the set %s", err, want)
			}
		})
	}
}

func TestGatherCountsOutagesAsTheWindowDoes(t *testing.T) {
	firstDay := today.AddDate(0, 0, -30)
	// A block every 6 hours, but one a second for the 40 s around the
	// window's first day: no restart gap near the outage, which a recovery
	// run would take.
	var times []time.Time
	for at := firstDay.AddDate(0, 0, -2); at.Before(firstDay.Add(-time.Hour)); at = at.Add(6 * time.Hour) {
		times = append(times, at)
	}
	for at := firstDay.Add(-20 * time.Second); at.Before(firstDay.Add(20 * time.Second)); at = at.Add(time.Second) {
		times = append(times, at)
	}
	for at := firstDay.Add(6 * time.Hour); at.Before(gatherNow); at = at.Add(6 * time.Hour) {
		times = append(times, at)
	}
	first := int64(slices.IndexFunc(times, func(at time.Time) bool { return !at.Before(firstDay) })) + 1
	for _, tc := range []struct {
		name   string
		inside int64 // the outage's heights inside the window, after 15 before it
		want   int
	}{
		{"blocks.OutageMin inside", blocks.OutageMin, 1},
		{"one fewer", blocks.OutageMin - 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			chain := onSet("SET", times)
			for h := first - 15; h < first+tc.inside; h++ {
				chain[h-1].absent = []string{"g1keya"}
			}
			writeChain(t, s, map[string][]string{"SET": {"g1keya", "g1keyb"}}, 1, chain)
			in := gather(t, s, nil)
			if in.Window.First != first {
				t.Fatalf("window's first height = %d, want %d", in.Window.First, first)
			}
			summary := operatorIn(t, in, "g1keya").History.Summary
			got := entryOf(t, Rank(in), "g1keya").Inputs.Outages
			if int64(len(got)) != summary.Outages || len(got) != tc.want {
				t.Errorf("Inputs.Outages = %+v, Summary.Outages = %d; want %d of each", got, summary.Outages, tc.want)
			}
		})
	}
}

func TestInputCheck(t *testing.T) {
	over := func(firstDay, lastDay time.Time) Operator {
		return Operator{Address: "g1opa", History: blocks.History{Summary: blocks.Summary{FirstDay: firstDay, LastDay: lastDay}}}
	}
	dayAfter := func(d time.Time) time.Time { return d.AddDate(0, 0, 1) }
	for _, tc := range []struct {
		name    string
		in      Input
		wantErr string // a part of the error, "" for none
	}{
		{"held", Input{Window: window, Operators: []Operator{over(window.FirstDay, window.LastDay)}, ProposalFlags: []int64{window.First, window.Last}}, ""},
		{"a history from another first day", Input{Window: window, Operators: []Operator{over(dayAfter(window.FirstDay), window.LastDay)}}, "g1opa"},
		{"a history to another last day", Input{Window: window, Operators: []Operator{over(window.FirstDay, dayAfter(window.LastDay))}}, "g1opa"},
		{"a flag below the window", Input{Window: window, ProposalFlags: []int64{window.First - 1}}, "1000"},
		{"a flag above it", Input{Window: window, ProposalFlags: []int64{window.Last + 1}}, "2001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.in.check()
			if tc.wantErr == "" && err != nil {
				t.Errorf("check() = %v, want nil", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Errorf("check() = %v, want an error naming %s", err, tc.wantErr)
			}
		})
	}
}

// ---- Readiness

// storeSpaced stores heights from to to, step apart, the last an hour before
// gatherNow.
func storeSpaced(t *testing.T, s *blocks.Store, from, to int64, step time.Duration) {
	t.Helper()
	writeChain(t, s, map[string][]string{"SET": {"g1keya"}}, from, onSet("SET", spaced(int(to-from+1), step, gatherNow.Add(-time.Hour))))
}

func setFloor(t *testing.T, s *blocks.Store, height int64) {
	t.Helper()
	if err := s.SetFloor(context.Background(), height); err != nil {
		t.Fatal(err)
	}
}

// progressOf returns the fill's progress over s as the tracker publishes it,
// at rate heights a second until complete.
func progressOf(t *testing.T, s *blocks.Store, rate float64) blocks.Progress {
	t.Helper()
	ctx := context.Background()
	low, high, err := s.Range(ctx)
	if err != nil {
		t.Fatal(err)
	}
	floor, err := s.Floor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fromTime, _, err := s.TimeAt(ctx, low)
	if err != nil {
		t.Fatal(err)
	}
	p := blocks.Progress{From: low, To: high, FromTime: fromTime, Floor: floor, Complete: low > 0 && low <= max(1, floor), Rate: rate}
	if p.Complete {
		p.Rate = 0
	}
	return p
}

// readiness returns Readiness over s as of gatherNow, the fill going at rate.
func readiness(t *testing.T, s *blocks.Store, rate float64) State {
	t.Helper()
	st, err := Readiness(context.Background(), s, progressOf(t, s, rate), gatherNow)
	if err != nil {
		t.Fatalf("Readiness: %v", err)
	}
	return st
}

func TestReadinessFillingShareAndTimeLeft(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to int64 // the stored heights, an hour apart after the span's start; none when 0
		rate     float64
		want     State
	}{
		{"heights 601 to 1000 at 10 a second", 601, 1000, 10, State{Kind: Filling, StoredShare: 0.4, Left: 60 * time.Second}},
		{"no rate yet", 601, 1000, 0, State{Kind: Filling, StoredShare: 0.4}},
		{"nothing stored", 0, 0, 0, State{Kind: Filling}},
		{
			"heights from 40,000,001 at 1 per 5 min, past the longest Duration", 40_000_001, 40_000_010, 1.0 / 300,
			State{Kind: Filling, StoredShare: 10.0 / 40_000_010, Left: time.Duration(math.MaxInt64)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			if tc.to > 0 {
				storeSpaced(t, s, tc.from, tc.to, time.Hour)
			}
			if got := readiness(t, s, tc.rate); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Readiness() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestReadinessReadyAtGenesis(t *testing.T) {
	for _, tc := range []struct {
		name  string
		floor int64
	}{
		{"no floor", 0},
		{"a floor at 1", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			storeSpaced(t, s, 1, 300, time.Hour) // a chain younger than the span
			setFloor(t, s, tc.floor)
			if got, want := readiness(t, s, 0), (State{Kind: Ready}); !reflect.DeepEqual(got, want) {
				t.Errorf("Readiness() = %+v, want %+v", got, want)
			}
		})
	}
}

func TestReadinessNeedsTheSpansStart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		lowest time.Time // the time of height 50, the lowest stored
		want   StateKind
	}{
		{"the lowest block before the span's start", spanStart.Add(-time.Hour), Ready},
		{"at it", spanStart, Ready},
		{"a ms after it", spanStart.Add(time.Millisecond), Filling},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			times := append([]time.Time{tc.lowest}, spaced(10, 24*time.Hour, gatherNow.Add(-time.Hour))...)
			writeChain(t, s, map[string][]string{"SET": {"g1keya"}}, 50, onSet("SET", times))
			if got := readiness(t, s, 5).Kind; got != tc.want {
				t.Errorf("Readiness().Kind = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestReadinessWaitsForEvents(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from, to int64         // the stored heights, the last an hour before gatherNow
		step     time.Duration // between them
		gaps     []int64       // the set changes whose events are not read
		rate     float64
		want     State
	}{
		{"an event gap at a stored height", 1, 300, time.Hour, []int64{250}, 0, State{Kind: Filling, StoredShare: 1, EventsPending: true}},
		{"an event gap above the stored heights", 1, 300, time.Hour, []int64{301}, 0, State{Kind: Filling, StoredShare: 1, EventsPending: true}},
		{"an event gap before the span's start", 1, 200, 24 * time.Hour, []int64{5}, 0, State{Kind: Ready}},
		{
			"event gaps before the span's start and in it", 1, 200, 24 * time.Hour,
			[]int64{5, 195},
			0,
			State{Kind: Filling, StoredShare: 1, EventsPending: true},
		},
		{
			"an event gap in the span, the fill going on below it", 50, 250, 24 * time.Hour,
			[]int64{240},
			5,
			State{Kind: Filling, StoredShare: 201.0 / 250, EventsPending: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			storeSpaced(t, s, tc.from, tc.to, tc.step)
			for _, gap := range tc.gaps {
				if err := s.AddEventGap(context.Background(), gap); err != nil {
					t.Fatal(err)
				}
			}
			if got := readiness(t, s, tc.rate); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Readiness() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestReadinessWaitsForEventsFromTheSpansFirstBlock(t *testing.T) {
	// Heights 50 on: 50 a day before the span's start, 51 at it.
	times := append([]time.Time{spanStart.Add(-24 * time.Hour), spanStart}, spaced(10, 24*time.Hour, gatherNow.Add(-time.Hour))...)
	for _, tc := range []struct {
		name string
		gap  int64
		want StateKind
	}{
		{"an event gap at the span's first block", 51, Filling},
		{"at the block below it", 50, Ready},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			writeChain(t, s, map[string][]string{"SET": {"g1keya"}}, 50, onSet("SET", times))
			if err := s.AddEventGap(context.Background(), tc.gap); err != nil {
				t.Fatal(err)
			}
			if got := readiness(t, s, 5).Kind; got != tc.want {
				t.Errorf("Readiness().Kind = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestReadinessUnavailableUnderAFloor(t *testing.T) {
	s := newStore(t)
	storeSpaced(t, s, 500, 1000, time.Hour)
	setFloor(t, s, 500)
	floorTime := gatherNow.Add(-time.Hour - 500*time.Hour) // block 500's, after the span's start
	want := State{Kind: Unavailable, Floor: 500, FloorTime: floorTime}
	if got := readiness(t, s, 0); !reflect.DeepEqual(got, want) {
		t.Errorf("Readiness() = %+v, want %+v", got, want)
	}
}

func TestReadinessReadyOverAFloorBeforeTheSpansStart(t *testing.T) {
	s := newStore(t)
	storeSpaced(t, s, 500, 700, 24*time.Hour) // block 500 200 days before gatherNow
	setFloor(t, s, 500)
	if got, want := readiness(t, s, 0), (State{Kind: Ready}); !reflect.DeepEqual(got, want) {
		t.Errorf("Readiness() = %+v, want %+v", got, want)
	}
}

func TestReadinessFailsOnAFloorNotStored(t *testing.T) {
	s := newStore(t)
	storeSpaced(t, s, 300, 400, time.Hour)
	setFloor(t, s, 500)
	_, err := Readiness(context.Background(), s, progressOf(t, s, 0), gatherNow)
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("Readiness() = %v, want an error naming the floor 500", err)
	}
}

func TestStateJSON(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state State
		want  string
	}{
		{
			"filling, a part second left rounded up",
			State{Kind: Filling, StoredShare: 0.4, Left: 1500 * time.Millisecond},
			`{"kind": "filling", "stored_share": 0.4, "left_s": 2, "events_pending": false}`,
		},
		{
			"filling, a ms left",
			State{Kind: Filling, StoredShare: 0.4, Left: time.Millisecond},
			`{"kind": "filling", "stored_share": 0.4, "left_s": 1, "events_pending": false}`,
		},
		{
			"filling, whole seconds left",
			State{Kind: Filling, StoredShare: 0.4, Left: 60 * time.Second},
			`{"kind": "filling", "stored_share": 0.4, "left_s": 60, "events_pending": false}`,
		},
		{
			"filling, the longest Duration left", State{Kind: Filling, StoredShare: 0.4, Left: time.Duration(math.MaxInt64)},
			`{"kind": "filling", "stored_share": 0.4, "left_s": 9223372037, "events_pending": false}`,
		},
		{
			"filling, events pending",
			State{Kind: Filling, StoredShare: 1, EventsPending: true},
			`{"kind": "filling", "stored_share": 1, "left_s": 0, "events_pending": true}`,
		},
		{
			"ready",
			State{Kind: Ready},
			`{"kind": "ready", "stored_share": 0, "left_s": 0, "events_pending": false}`,
		},
		{
			"unavailable",
			State{Kind: Unavailable, Floor: 500, FloorTime: time.Date(2026, 11, 3, 4, 5, 6, 0, time.UTC)},
			`{"kind": "unavailable", "stored_share": 0, "left_s": 0, "events_pending": false, "floor": 500, "floor_time": "2026-11-03T04:05:06Z"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.state)
			if err != nil {
				t.Fatal(err)
			}
			var got, want map[string]any
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("json.Marshal(%+v) = %s, want %s", tc.state, b, tc.want)
			}
		})
	}
}

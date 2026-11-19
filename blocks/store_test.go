package blocks

import (
	"context"
	"database/sql"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// A single connection keeps the in-memory database alive across queries.
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	s, err := NewStore(context.Background(), db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

var t0 = time.Date(2026, 11, 16, 10, 51, 11, 208_000_000, time.UTC)

var testSet = []Member{{Address: valG0Y, PubKey: "KA", Power: 10}, {Address: val15T, PubKey: "KB", Power: 20}}

// rec is the record of height, at t0 + height seconds, on testSet.
func rec(height int64, absences ...Absence) Record {
	return Record{Height: height, Time: t0.Add(time.Duration(height) * time.Second), Proposer: valG0Y, Txs: 1, ValsetHash: "SET", Absences: absences}
}

func putTestSet(t *testing.T, s *Store) {
	t.Helper()
	if err := s.PutValset(context.Background(), "SET", testSet); err != nil {
		t.Fatal(err)
	}
}

func TestStoreWriteAndRead(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	if err := s.Write(ctx, []Record{rec(11, Absence{val15T, Nil}), rec(10), rec(12, Absence{val15T, Absent})}); err != nil {
		t.Fatal(err)
	}
	if low, high, err := s.Range(ctx); err != nil || low != 10 || high != 12 {
		t.Errorf("Range() = %d, %d, %v; want 10, 12, nil", low, high, err)
	}
	got, err := s.Blocks(ctx, 9, 11)
	if err != nil {
		t.Fatal(err)
	}
	if want := []Record{rec(10), rec(11, Absence{val15T, Nil})}; !reflect.DeepEqual(got.Blocks, want) {
		t.Errorf("Blocks(9, 11) =\n%+v\nwant\n%+v", got.Blocks, want)
	}
	if want := map[string][]Member{"SET": testSet}; !reflect.DeepEqual(got.Sets, want) {
		t.Errorf("Blocks(9, 11) sets = %+v, want %+v", got.Sets, want)
	}
	if hash, ok, err := s.ValsetHashAt(ctx, 12); err != nil || !ok || hash != "SET" {
		t.Errorf("ValsetHashAt(12) = %q, %v, %v; want SET", hash, ok, err)
	}
	if _, ok, err := s.ValsetHashAt(ctx, 13); err != nil || ok {
		t.Errorf("ValsetHashAt(13) found = %v, %v; want not found", ok, err)
	}
	if at, ok, err := s.TimeAt(ctx, 10); err != nil || !ok || !at.Equal(t0.Add(10*time.Second)) {
		t.Errorf("TimeAt(10) = %v, %v, %v", at, ok, err)
	}
	if members, ok, err := s.Valset(ctx, "SET"); err != nil || !ok || !reflect.DeepEqual(members, testSet) {
		t.Errorf("Valset(SET) = %+v, %v, %v", members, ok, err)
	}
}

func TestStoreStaysCompact(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	addrs := []string{val180, valG0Y, val15T, valHQH, valH97, valMMG, valL98}
	set := make([]Member, len(addrs))
	for i, a := range addrs {
		set[i] = Member{Address: a, PubKey: "KEY" + a, Power: 10}
	}
	if err := s.PutValset(ctx, "SET7", set); err != nil {
		t.Fatal(err)
	}
	recs := make([]Record, 1000)
	for i := range recs {
		h := int64(i + 1)
		r := Record{Height: h, Time: t0.Add(time.Duration(h) * time.Second), Proposer: addrs[i%7], Round: 0, Txs: 1, ValsetHash: "SET7"}
		for j := range 4 {
			r.Absences = append(r.Absences, Absence{Address: addrs[(i+j)%7], Kind: Absent})
		}
		for j := 4; j < 7; j++ {
			r.Delays = append(r.Delays, Delay{Address: addrs[(i+j)%7], Ms: int64(j * 100)})
		}
		recs[i] = r
	}
	if err := s.Write(ctx, recs); err != nil {
		t.Fatal(err)
	}
	var pages, pageSize int64
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	// Each address is stored once and no index is kept beside the keys: 1,000
	// blocks with 4 absences each take under 160 KB. Of that, the runs of
	// absences take 12 KB (a run of 4 every 7 blocks per validator: 1,000
	// runs), the day counters and restart gaps a page each. An index on block
	// time adds over 8 KB; the address in every absence with two indexes over
	// them, over 650 KB.
	if size := pages * pageSize; size > 160<<10 {
		t.Errorf("store size = %d KB, want at most 160 KB", size>>10)
	}
}

func TestStoreReadsAbsencesInSetOrder(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	// Another set stores the validators first, in the other order.
	if err := s.PutValset(ctx, "OTHER", []Member{testSet[1], testSet[0]}); err != nil {
		t.Fatal(err)
	}
	putTestSet(t, s)
	r := rec(10, Absence{valG0Y, Absent}, Absence{val15T, Nil})
	if err := s.Write(ctx, []Record{r}); err != nil {
		t.Fatal(err)
	}
	if got := storedBlock(t, s, 10); !reflect.DeepEqual(got.Absences, r.Absences) {
		t.Errorf("absences = %+v, want %+v (set order)", got.Absences, r.Absences)
	}
}

func TestStorePutValsetRejectsAnEmptySet(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.PutValset(ctx, "SET", nil); err == nil {
		t.Error("PutValset() of an empty set succeeded")
	}
	putTestSet(t, s)
	if members, ok, err := s.Valset(ctx, "SET"); err != nil || !ok || !reflect.DeepEqual(members, testSet) {
		t.Errorf("Valset(SET) = %+v, %v, %v; want the set stored after the refusal", members, ok, err)
	}
}

func TestStoreWriteNeedsTheSet(t *testing.T) {
	s := newTestStore(t)
	if err := s.Write(context.Background(), []Record{rec(10)}); err == nil {
		t.Error("Write() of a record on an unknown set succeeded")
	}
}

func TestStoreWriteCountsAHeightOnce(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	r := rec(10, Absence{val15T, Absent})
	r.Delays = []Delay{{valG0Y, 300}}
	for range 2 {
		if err := s.Write(ctx, []Record{r}); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.MissedByWindows(ctx, []time.Duration{0}, t0.Add(time.Hour)); err != nil || !reflect.DeepEqual(got.Missed, map[string][]int{val15T: {1}}) {
		t.Errorf("missed = %v, %v; want %s 1", got.Missed, err, val15T)
	}
	day := t0.Truncate(24 * time.Hour)
	got, err := s.VoteDelays(ctx, day, day)
	if err != nil {
		t.Fatal(err)
	}
	// 300 ms falls in [250, 1000).
	want := []DayDelays{{Day: day, Address: valG0Y, Votes: 1, SumMs: 300, Buckets: [DelayBuckets]int64{3: 1}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("VoteDelays() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestStoreVoteDelaysByDay(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	day1 := t0.Truncate(24 * time.Hour)
	day2 := day1.Add(24 * time.Hour)
	r1, r2 := rec(10), rec(11)
	r1.Delays = []Delay{{valG0Y, 100}}
	r2.Time, r2.Delays = day2.Add(time.Hour), []Delay{{valG0Y, 6000}, {valG0Y, 0}}
	if err := s.Write(ctx, []Record{r1, r2}); err != nil {
		t.Fatal(err)
	}
	got, err := s.VoteDelays(ctx, day2, day2)
	if err != nil {
		t.Fatal(err)
	}
	if want := []DayDelays{{Day: day2, Address: valG0Y, Votes: 2, SumMs: 6000, Buckets: [DelayBuckets]int64{2: 1, 6: 1}}}; !reflect.DeepEqual(got, want) {
		t.Errorf("VoteDelays(day 2) = %+v, want %+v", got, want)
	}
	if got, err := s.VoteDelays(ctx, day1, day2); err != nil || len(got) != 2 || !got[0].Day.Equal(day1) || !got[1].Day.Equal(day2) {
		t.Errorf("VoteDelays(days 1–2) = %+v, %v; want day 1 then day 2", got, err)
	}
}

func TestDelayBucketEdges(t *testing.T) {
	// An edge falls in the bucket farther from 0.
	for ms, want := range map[int64]int{-1000: 0, -999: 1, -250: 1, -249: 2, 0: 2, 249: 2, 250: 3, 999: 3, 1000: 4, 2500: 5, 5000: 6} {
		if got := delayBucket(ms); got != want {
			t.Errorf("delayBucket(%d) = %d, want %d", ms, got, want)
		}
	}
}

func TestStoreRoundsAndFlags(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	r := rec(10)
	// Round 0's expected proposer is unknown: "".
	r.Round, r.Rounds, r.Flag = 1, []string{"", valG0Y}, "test flag"
	if err := s.Write(ctx, []Record{r}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Blocks(ctx, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Blocks) != 1 || !reflect.DeepEqual(got.Blocks[0], r) {
		t.Errorf("Blocks(10, 10) = %+v, want %+v", got.Blocks, r)
	}
}

func TestStoreMissedByWindows(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	now := t0.Add(10 * time.Hour)
	r1, r2, r3 := rec(1, Absence{val15T, Absent}), rec(2, Absence{val15T, OtherBlock}, Absence{valG0Y, Nil}), rec(3)
	r1.Time, r2.Time, r3.Time = now.Add(-2*time.Hour), now.Add(-30*time.Minute), now.Add(-10*time.Minute)
	if err := s.Write(ctx, []Record{r1, r2, r3}); err != nil {
		t.Fatal(err)
	}
	got, err := s.MissedByWindows(ctx, []time.Duration{time.Hour, 0}, now)
	if err != nil {
		t.Fatal(err)
	}
	// A nil vote counts as signed. valG0Y proposed every block, at round 0.
	want := WindowReport{
		Windows: []time.Duration{time.Hour, 0}, Blocks: []int{2, 3}, Missed: map[string][]int{val15T: {1, 2}},
		ProposalsExpected: map[string][]int{valG0Y: {2, 3}}, ProposalsMissed: map[string][]int{valG0Y: {0, 0}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MissedByWindows() = %+v, want %+v", got, want)
	}
}

// queryPlan returns the details of SQLite's plan for query.
func queryPlan(t *testing.T, s *Store, query string, args ...any) []string {
	t.Helper()
	rows, err := s.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestStoreMissedByWindowsSearchesTheWindowOnly(t *testing.T) {
	plan := queryPlan(t, newTestStore(t), windowMissesQuery, 1, 2, Absent, OtherBlock)
	// The 24h window is read on every publish: it must cost its own blocks'
	// absences, not a pass over the whole history's.
	if !slices.ContainsFunc(plan, func(d string) bool {
		return strings.HasPrefix(d, "SEARCH blocks_absences") && strings.Contains(d, "height>?")
	}) {
		t.Errorf("plan = %q, want a search of blocks_absences by height", plan)
	}
}

func TestStoreRangeReadsTwoHeights(t *testing.T) {
	// Range runs on every pass and window: it must read the lowest and the
	// highest height, not every stored block.
	if plan := queryPlan(t, newTestStore(t), rangeQuery); slices.ContainsFunc(plan, func(d string) bool {
		return strings.HasPrefix(d, "SCAN blocks")
	}) {
		t.Errorf("plan = %q, want no scan of blocks", plan)
	}
}

func TestStoreMissedByWindowsFindsEachWindowsFirstBlock(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	recs := make([]Record, 100)
	for i := range recs {
		recs[i] = rec(int64(i + 1)) // at t0 + height seconds
	}
	if err := s.Write(ctx, recs); err != nil {
		t.Fatal(err)
	}
	now := t0.Add(100 * time.Second)
	got, err := s.MissedByWindows(ctx, []time.Duration{time.Second, 10 * time.Second, 0}, now)
	if err != nil {
		t.Fatal(err)
	}
	// Blocks 99 and 100 are within 1 s of now, 90 to 100 within 10 s.
	if !slices.Equal(got.Blocks, []int{2, 11, 100}) {
		t.Errorf("window blocks = %v, want [2 11 100]", got.Blocks)
	}
	got, err = s.MissedByWindows(ctx, []time.Duration{time.Second}, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Blocks, []int{0}) {
		t.Errorf("window blocks an hour later = %v, want [0]", got.Blocks)
	}
}

// writeDays stores blocks 1 to 12, every 6 h from the UTC day of t0: val15T
// absent from each, valG0Y precommitting another block on even heights and
// nil on odd ones.
func writeDays(t *testing.T, s *Store) time.Time {
	t.Helper()
	day0 := t0.Truncate(24 * time.Hour)
	recs := make([]Record, 12)
	for i := range recs {
		h := int64(i + 1)
		kind := Nil
		if h%2 == 0 {
			kind = OtherBlock
		}
		r := rec(h, Absence{valG0Y, kind}, Absence{val15T, Absent})
		r.Time = day0.Add(time.Duration(h) * 6 * time.Hour)
		recs[i] = r
	}
	if err := s.Write(context.Background(), recs); err != nil {
		t.Fatal(err)
	}
	return day0
}

func TestStoreMissedByWindowsAcrossDays(t *testing.T) {
	s := newTestStore(t)
	putTestSet(t, s)
	day0 := writeDays(t, s)
	now := day0.Add(12*6*time.Hour + time.Hour) // day 3, 01:00
	windows := []time.Duration{2 * time.Hour, 25 * time.Hour, 0, 40 * time.Hour}
	got, err := s.MissedByWindows(context.Background(), windows, now)
	if err != nil {
		t.Fatal(err)
	}
	// 2 h: block 12; 25 h: 8 to 12, from day 2's start; all: 1 to 12; 40 h:
	// 6 to 12, from day 1's middle.
	want := WindowReport{
		Windows: windows, Blocks: []int{1, 5, 12, 7}, Missed: map[string][]int{
			val15T: {1, 5, 12, 7},
			valG0Y: {1, 3, 6, 4},
		},
		// valG0Y proposed every block, at round 0.
		ProposalsExpected: map[string][]int{valG0Y: {1, 5, 12, 7}}, ProposalsMissed: map[string][]int{valG0Y: {0, 0, 0, 0}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MissedByWindows() =\n%+v\nwant\n%+v", got, want)
	}
}

func TestStoreMissedTotalsReadTheDayCounters(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	writeDays(t, s)
	// The whole history's misses come from the per-day counters, not from
	// every stored absence: without the absences, they stand.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM blocks_absences`); err != nil {
		t.Fatal(err)
	}
	got, err := s.MissedByWindows(ctx, []time.Duration{0}, t0.Add(10*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string][]int{val15T: {12}, valG0Y: {6}}; !reflect.DeepEqual(got.Missed, want) {
		t.Errorf("missed = %v, want %v", got.Missed, want)
	}
}

func TestStoreEarliest(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if at, err := s.Earliest(ctx); err != nil || !at.IsZero() {
		t.Errorf("Earliest() on an empty store = %v, %v; want zero", at, err)
	}
	putTestSet(t, s)
	if err := s.Write(ctx, []Record{rec(12), rec(11)}); err != nil {
		t.Fatal(err)
	}
	if at, err := s.Earliest(ctx); err != nil || !at.Equal(t0.Add(11*time.Second)) {
		t.Errorf("Earliest() = %v, %v; want %v", at, err, t0.Add(11*time.Second))
	}
}

func TestStorePutEvents(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	evs := []Event{{Type: "ValidatorAdded", Operator: "g1op", Address: valH97, Power: 10, Applied: true}}
	for range 2 {
		if err := s.PutEvents(ctx, 200, 198, evs); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Blocks(ctx, 199, 201)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[int64]SetChange{200: {Source: 198, Events: evs}}; !reflect.DeepEqual(got.Changes, want) {
		t.Errorf("changes = %+v, want %+v", got.Changes, want)
	}
}

func TestStoreMetaAndReset(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if id, err := s.ChainID(ctx); err != nil || id != "" {
		t.Fatalf("ChainID() on a new store = %q, %v", id, err)
	}
	if err := s.Reset(ctx, "gnoland-1"); err != nil {
		t.Fatal(err)
	}
	putTestSet(t, s)
	if err := s.Write(ctx, []Record{rec(10)}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetFloor(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.PutEvents(ctx, 10, 8, []Event{{Type: "ValidatorAdded"}}); err != nil {
		t.Fatal(err)
	}
	if f, err := s.Floor(ctx); err != nil || f != 5 {
		t.Errorf("Floor() = %d, %v; want 5", f, err)
	}

	if err := s.Reset(ctx, "test-13"); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.ChainID(ctx); id != "test-13" {
		t.Errorf("ChainID() after Reset = %q, want test-13", id)
	}
	if low, high, _ := s.Range(ctx); low != 0 || high != 0 {
		t.Errorf("Range() after Reset = %d, %d; want empty", low, high)
	}
	if f, _ := s.Floor(ctx); f != 0 {
		t.Errorf("Floor() after Reset = %d, want 0", f)
	}
	if _, ok, _ := s.Valset(ctx, "SET"); ok {
		t.Error("Valset(SET) found after Reset")
	}
	if got, err := s.Blocks(ctx, 1, 100); err != nil || len(got.Changes) != 0 {
		t.Errorf("changes after Reset = %+v, %v; want none", got.Changes, err)
	}
}

func TestStoreEventGaps(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	for _, h := range []int64{200, 150, 200} {
		if err := s.AddEventGap(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.EventGaps(ctx); err != nil || !slices.Equal(got, []int64{150, 200}) {
		t.Errorf("EventGaps() = %v, %v; want [150 200]", got, err)
	}
	// Storing a change's events fills its gap, even with no event.
	if err := s.PutEvents(ctx, 200, 198, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EventGaps(ctx); err != nil || !slices.Equal(got, []int64{150}) {
		t.Errorf("EventGaps() after PutEvents(200) = %v, %v; want [150]", got, err)
	}
	if err := s.DropEventGap(ctx, 150); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EventGaps(ctx); err != nil || len(got) != 0 {
		t.Errorf("EventGaps() after DropEventGap(150) = %v, %v; want none", got, err)
	}
	if err := s.AddEventGap(ctx, 150); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(ctx, "test-13"); err != nil {
		t.Fatal(err)
	}
	if got, err := s.EventGaps(ctx); err != nil || len(got) != 0 {
		t.Errorf("EventGaps() after Reset = %v, %v; want none", got, err)
	}
}

func TestStoreClearFloor(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	if err := s.SetFloor(ctx, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.ClearFloor(ctx); err != nil {
		t.Fatal(err)
	}
	if f, err := s.Floor(ctx); err != nil || f != 0 {
		t.Errorf("Floor() after ClearFloor = %d, %v; want 0", f, err)
	}
}

func TestNewStoreDropsTheSigningHistoryTables(t *testing.T) {
	ctx := context.Background()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err := db.ExecContext(ctx, `CREATE TABLE signing_blocks (height INTEGER PRIMARY KEY); CREATE TABLE signing_misses (height INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(ctx, db); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE name IN ('signing_blocks', 'signing_misses')`,
	).Scan(&n); err != nil || n != 0 {
		t.Errorf("signing history tables left: %d, %v", n, err)
	}
}

func TestStoreMissedByWindowsCountsProposals(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	putTestSet(t, s)
	day0 := t0.Truncate(24 * time.Hour)
	at := func(h time.Duration) time.Time { return day0.Add(h) }
	late := func(r Record) Record {
		r.Round, r.Rounds = 1, []string{val15T, valG0Y}
		return r
	}
	byVal := func(r Record, p string) Record { r.Proposer = p; return r }
	recs := []Record{
		{Height: 1, Time: at(6 * time.Hour), Proposer: valG0Y, ValsetHash: "SET"},
		late(Record{Height: 2, Time: at(12 * time.Hour), Proposer: valG0Y, ValsetHash: "SET"}),
		{Height: 3, Time: at(18 * time.Hour), Proposer: valG0Y, ValsetHash: "SET"},
		byVal(Record{Height: 4, Time: at(30 * time.Hour), ValsetHash: "SET"}, val15T),
		{Height: 5, Time: at(34 * time.Hour), Proposer: valG0Y, ValsetHash: "SET"},
	}
	if err := s.Write(ctx, recs); err != nil {
		t.Fatal(err)
	}
	// 24 h before day 1's 11:00: from block 2, the late one, on day 0 (a
	// partial day); day 1 from the counters.
	got, err := s.MissedByWindows(ctx, []time.Duration{24 * time.Hour, 0}, at(35*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	wantExpected := map[string][]int{valG0Y: {3, 4}, val15T: {2, 2}}
	wantMissed := map[string][]int{valG0Y: {0, 0}, val15T: {1, 1}}
	if !reflect.DeepEqual(got.ProposalsExpected, wantExpected) || !reflect.DeepEqual(got.ProposalsMissed, wantMissed) {
		t.Errorf("proposals expected %v, missed %v; want %v, %v", got.ProposalsExpected, got.ProposalsMissed, wantExpected, wantMissed)
	}
}

func TestStoreWindowProposalsSearchTheWindowOnly(t *testing.T) {
	plan := queryPlan(t, newTestStore(t), windowProposalsQuery, 1, 2)
	// Read on every publish for the 24 h window: its own blocks only.
	if !slices.ContainsFunc(plan, func(d string) bool {
		return strings.HasPrefix(d, "SEARCH b USING INTEGER PRIMARY KEY") || strings.HasPrefix(d, "SEARCH blocks USING INTEGER PRIMARY KEY")
	}) {
		t.Errorf("plan = %q, want a search of blocks by height", plan)
	}
}

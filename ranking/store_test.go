package ranking

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"testing"
	"time"
)

// The chains the tests publish for.
const (
	chainA = "test-chain-a"
	chainB = "test-chain-b"
)

// newDayStore returns a day store on db.
func newDayStore(t *testing.T, db *sql.DB) *DayStore {
	t.Helper()
	d, err := NewDayStore(context.Background(), db)
	if err != nil {
		t.Fatalf("NewDayStore: %v", err)
	}
	return d
}

// publishedOn returns the day of chainA published for day, computed at its
// noon, with exclusions as given: a ranking of one operator with an outage
// and a recovery sample.
func publishedOn(t *testing.T, day string, exclusions ...string) Published {
	t.Helper()
	midnight, err := time.Parse(time.DateOnly, day)
	if err != nil {
		t.Fatal(err)
	}
	s := flawless()
	s.Outages = 1
	h := restartedOnce(s, 90*time.Second)
	h.Outages = outagesInside(1)
	return Published{
		Chain:      chainA,
		Day:        day,
		ComputedAt: midnight.Add(12*time.Hour + 345*time.Millisecond),
		Exclusions: listOf(exclusions),
		Result:     rankAll(operator("g1op", h)),
	}
}

func put(t *testing.T, d *DayStore, p Published) {
	t.Helper()
	if err := d.Put(context.Background(), p); err != nil {
		t.Fatalf("Put(%s, %s): %v", p.Chain, p.Day, err)
	}
}

func latest(t *testing.T, d *DayStore, chain string) (Published, bool) {
	t.Helper()
	p, ok, err := d.Latest(context.Background(), chain)
	if err != nil {
		t.Fatalf("Latest(%s): %v", chain, err)
	}
	return p, ok
}

func previous(t *testing.T, d *DayStore, chain, day string) (Published, bool) {
	t.Helper()
	p, ok, err := d.Previous(context.Background(), chain, day)
	if err != nil {
		t.Fatalf("Previous(%s, %s): %v", chain, day, err)
	}
	return p, ok
}

func TestDayStorePutLatestPrevious(t *testing.T) {
	_, db := openStore(t)
	d := newDayStore(t, db)
	if got, ok := latest(t, d, chainA); ok {
		t.Errorf("Latest() on an empty store = %s, want none", got.Day)
	}
	stored := map[string]Published{}
	for _, day := range []string{"2026-11-23", "2026-11-25", "2026-11-24"} {
		stored[day] = publishedOn(t, day)
		put(t, d, stored[day])
	}
	if got, ok := latest(t, d, chainA); !ok || !reflect.DeepEqual(got, stored["2026-11-25"]) {
		t.Errorf("Latest() = %+v (found %v), want %+v", got, ok, stored["2026-11-25"])
	}
	for _, tc := range []struct {
		day  string
		want string // "" for none
	}{
		{"2026-11-26", "2026-11-25"}, // a day after the stored ones
		{"2026-11-25", "2026-11-24"},
		{"2026-11-24", "2026-11-23"},
		{"2026-11-23", ""},
	} {
		t.Run("Previous of "+tc.day, func(t *testing.T) {
			got, ok := previous(t, d, chainA, tc.day)
			switch {
			case tc.want == "" && ok:
				t.Errorf("Previous(%s) = %s, want none", tc.day, got.Day)
			case tc.want != "" && (!ok || !reflect.DeepEqual(got, stored[tc.want])):
				t.Errorf("Previous(%s) = %+v (found %v), want %+v", tc.day, got, ok, stored[tc.want])
			}
		})
	}

	// Put of a stored day replaces it.
	again := publishedOn(t, "2026-11-24", "g1excluded")
	again.ComputedAt = again.ComputedAt.Add(time.Hour)
	put(t, d, again)
	if got, ok := previous(t, d, chainA, "2026-11-25"); !ok || !reflect.DeepEqual(got, again) {
		t.Errorf("Previous(2026-11-25) after a second Put of 2026-11-24 = %+v (found %v), want %+v", got, ok, again)
	}
}

func TestDayStoreRowColumns(t *testing.T) {
	_, db := openStore(t)
	d := newDayStore(t, db)
	for _, tc := range []struct {
		day            string
		exclusions     []string
		wantExclusions string
	}{
		{"2026-11-24", nil, ""},
		{"2026-11-25", []string{"g1a", "g1b"}, "g1a,g1b"},
	} {
		t.Run(tc.day, func(t *testing.T) {
			put(t, d, publishedOn(t, tc.day, tc.exclusions...))
			var formula int
			var exclusions string
			row := db.QueryRow(`SELECT formula, exclusions FROM ranking_days WHERE chain = ? AND day = ?`, chainA, tc.day)
			if err := row.Scan(&formula, &exclusions); err != nil {
				t.Fatal(err)
			}
			if formula != FormulaVersion || exclusions != tc.wantExclusions {
				t.Errorf("row of %s = formula %d, exclusions %q; want %d, %q", tc.day, formula, exclusions, FormulaVersion, tc.wantExclusions)
			}
		})
	}
}

func TestDayStoreRefusesAMalformedDay(t *testing.T) {
	_, db := openStore(t)
	d := newDayStore(t, db)
	for _, day := range []string{"", "2026-11-5", "2026-02-30", "25/11/2026"} {
		t.Run(day, func(t *testing.T) {
			p := publishedOn(t, "2026-11-25")
			p.Day = day
			if err := d.Put(context.Background(), p); err == nil {
				t.Errorf("Put(day %q) = nil, want an error: days order as YYYY-MM-DD text", day)
			}
		})
	}
	if got, ok := latest(t, d, chainA); ok {
		t.Errorf("Latest() after refused puts = %q, want none", got.Day)
	}
}

func TestDayStoreRefusesADayWithoutAChain(t *testing.T) {
	_, db := openStore(t)
	d := newDayStore(t, db)
	p := publishedOn(t, "2026-11-25")
	p.Chain = ""
	if err := d.Put(context.Background(), p); err == nil {
		t.Errorf("Put(chain \"\") = nil, want an error")
	}
	if got, ok := latest(t, d, ""); ok {
		t.Errorf("Latest(\"\") after a refused put = %q, want none", got.Day)
	}
}

func TestDayStoreKeepsEachChainsDays(t *testing.T) {
	_, db := openStore(t)
	d := newDayStore(t, db)
	onChain := func(chain, day string) Published {
		p := publishedOn(t, day)
		p.Chain = chain
		return p
	}
	a24, a25, b25 := onChain(chainA, "2026-11-24"), onChain(chainA, "2026-11-25"), onChain(chainB, "2026-11-25")
	for _, p := range []Published{a24, a25, b25} {
		put(t, d, p)
	}
	for _, tc := range []struct {
		name string
		read func() (Published, bool)
		want *Published
	}{
		{"Latest(A)", func() (Published, bool) { return latest(t, d, chainA) }, &a25},
		{"Latest(B)", func() (Published, bool) { return latest(t, d, chainB) }, &b25},
		{"Previous(A, 2026-11-25)", func() (Published, bool) { return previous(t, d, chainA, "2026-11-25") }, &a24},
		{"Previous(B, 2026-11-25)", func() (Published, bool) { return previous(t, d, chainB, "2026-11-25") }, nil},
		{"Previous(B, 2026-11-26)", func() (Published, bool) { return previous(t, d, chainB, "2026-11-26") }, &b25},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.read()
			switch {
			case tc.want == nil && ok:
				t.Errorf("%s = %s of %s, want none", tc.name, got.Day, got.Chain)
			case tc.want != nil && (!ok || !reflect.DeepEqual(got, *tc.want)):
				t.Errorf("%s = %+v (found %v), want %+v", tc.name, got, ok, *tc.want)
			}
		})
	}
}

func TestPublishedJSON(t *testing.T) {
	// Result's fields sit inline beside the day's; an empty exclusion list
	// is [], never null.
	p := publishedOn(t, "2026-11-25")
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"chain", "computed_at", "day", "entries", "exclusions", "first_day", "first_height",
		"formula", "last_day", "last_height", "proposals_disabled",
	}
	if got := slices.Sorted(maps.Keys(fields)); !slices.Equal(got, want) {
		t.Errorf("JSON keys = %v, want %v", got, want)
	}
	if got := string(fields["exclusions"]); got != "[]" {
		t.Errorf("JSON exclusions = %s, want []", got)
	}
}

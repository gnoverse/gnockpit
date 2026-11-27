package ranking

import (
	"slices"
	"strings"
	"testing"
)

// digestDay is a published day of the given formula and exclusions, listing
// entries.
func digestDay(formula int, exclusions []string, entries ...Entry) Published {
	return Published{
		Chain:      "gnoland-1",
		Day:        "2026-11-25",
		Exclusions: exclusions,
		Result:     Result{Formula: formula, FirstDay: "2026-10-27", LastDay: "2026-11-25", Entries: entries},
	}
}

func ranked(op, name string, rank int, score float64) Entry {
	return Entry{Operator: op, Name: name, Status: Ranked, Rank: rank, Score: score}
}

func TestDigestLinesAndChanges(t *testing.T) {
	prev := digestDay(1, nil, ranked("a", "A", 1, 99), ranked("b", "B", 2, 98), ranked("c", "C", 3, 97))
	cur := digestDay(1, nil, ranked("b", "B", 1, 99.04), ranked("a", "A", 2, 98.96), ranked("d", "D", 3, 90))
	d := DigestOf(cur, &prev)
	title, body := d.Title, d.Body()
	if want := "Validator ranking, 2026-10-27 – 2026-11-25 UTC (formula v1)"; title != want {
		t.Errorf("DigestOf title = %q, want %q", title, want)
	}
	want := "#1 B 99.0 ▲1\n#2 A 98.9 ▼1\n#3 D 90.0 new"
	if body != want {
		t.Errorf("DigestOf body = %q, want %q", body, want)
	}
}

func TestDigestRowsAndNotes(t *testing.T) {
	prev := digestDay(1, nil, ranked("a", "A", 1, 99), ranked("b", "B", 2, 98), ranked("c", "C", 3, 97))
	cur := digestDay(1, nil, ranked("b", "B", 1, 99.04), ranked("a", "A", 2, 98.96), ranked("d", "D", 3, 90))
	d := DigestOf(cur, &prev)
	wantRows := []DigestRow{
		{Rank: 1, Name: "B", Score: "99.0", Change: "▲1"},
		{Rank: 2, Name: "A", Score: "98.9", Change: "▼1"},
		{Rank: 3, Name: "D", Score: "90.0", Change: "new"},
	}
	if !slices.Equal(d.Rows, wantRows) {
		t.Errorf("DigestOf rows = %+v, want %+v", d.Rows, wantRows)
	}
	if len(d.Notes) != 0 {
		t.Errorf("DigestOf notes = %q, want none", d.Notes)
	}

	d = DigestOf(cur, nil)
	for _, r := range d.Rows {
		if r.Change != "" {
			t.Errorf("DigestOf(cur, nil) row %+v, want no change", r)
		}
	}
	if want := []string{"Rank changes not shown: no earlier ranking"}; !slices.Equal(d.Notes, want) {
		t.Errorf("DigestOf(cur, nil) notes = %q, want %q", d.Notes, want)
	}
}

func TestDigestNotesOrder(t *testing.T) {
	prev := digestDay(1, nil, ranked("a", "A", 1, 99))
	cur := digestDay(2, nil, ranked("a", "A", 1, 99), Entry{Operator: "n", Status: NotRankedYet})
	want := []string{"Not ranked yet: 1", "Rank changes not shown: formula v2 since the previous ranking"}
	if got := DigestOf(cur, &prev).Notes; !slices.Equal(got, want) {
		t.Errorf("DigestOf notes = %q, want %q", got, want)
	}
}

func TestDigestRepeatsSharedRanksAndMarksUnchanged(t *testing.T) {
	prev := digestDay(1, nil, ranked("a", "A", 1, 99), ranked("b", "B", 1, 99), ranked("c", "C", 3, 90))
	cur := digestDay(1, nil, ranked("a", "A", 1, 99), ranked("b", "B", 1, 99), ranked("c", "C", 3, 90))
	body := DigestOf(cur, &prev).Body()
	if want := "#1 A 99.0 =\n#1 B 99.0 =\n#3 C 90.0 ="; body != want {
		t.Errorf("DigestOf body = %q, want %q", body, want)
	}
}

func TestDigestOmitsChangesAcrossFormulas(t *testing.T) {
	prev := digestDay(1, nil, ranked("a", "A", 1, 99))
	cur := digestDay(2, nil, ranked("a", "A", 1, 99))
	body := DigestOf(cur, &prev).Body()
	if want := "#1 A 99.0\nRank changes not shown: formula v2 since the previous ranking"; body != want {
		t.Errorf("DigestOf body = %q, want %q", body, want)
	}
}

func TestDigestOmitsChangesAcrossExclusionLists(t *testing.T) {
	prev := digestDay(1, []string{"x"}, ranked("a", "A", 1, 99))
	cur := digestDay(1, []string{"x", "y"}, ranked("a", "A", 1, 99))
	body := DigestOf(cur, &prev).Body()
	if want := "#1 A 99.0\nRank changes not shown: exclusion list changed since the previous ranking"; body != want {
		t.Errorf("DigestOf body = %q, want %q", body, want)
	}
}

func TestDigestWithoutAnEarlierRanking(t *testing.T) {
	cur := digestDay(1, nil, ranked("a", "A", 1, 99))
	body := DigestOf(cur, nil).Body()
	if want := "#1 A 99.0\nRank changes not shown: no earlier ranking"; body != want {
		t.Errorf("DigestOf body = %q, want %q", body, want)
	}
}

func TestDigestCountsNotRankedYet(t *testing.T) {
	prev := digestDay(1, nil, ranked("a", "A", 1, 99))
	cur := digestDay(1, nil,
		ranked("a", "A", 1, 99),
		Entry{Operator: "x", Name: "X", Status: Excluded, Score: 80},
		Entry{Operator: "n1", Name: "N1", Status: NotRankedYet},
		Entry{Operator: "n2", Name: "N2", Status: NotRankedYet},
	)
	body := DigestOf(cur, &prev).Body()
	if want := "#1 A 99.0 =\nNot ranked yet: 2"; body != want {
		t.Errorf("DigestOf body = %q, want %q", body, want)
	}
	if strings.Contains(body, "X") {
		t.Errorf("DigestOf body = %q, lists an excluded operator", body)
	}
}

func TestDigestCarriesItsDayWindowAndCounts(t *testing.T) {
	prev := digestDay(1, nil, ranked("a", "A", 1, 99))
	cur := digestDay(1, nil,
		ranked("a", "A", 1, 99),
		Entry{Operator: "x", Name: "X", Status: Excluded, Score: 80},
		Entry{Operator: "n1", Name: "N1", Status: NotRankedYet},
		Entry{Operator: "n2", Name: "N2", Status: NotRankedYet},
	)
	// The day published follows its window's last day.
	cur.Day = "2026-11-26"
	d := DigestOf(cur, &prev)
	if d.Day != "2026-11-26" || d.FirstDay != "2026-10-27" || d.LastDay != "2026-11-25" || d.Formula != 1 {
		t.Errorf("DigestOf day, window, formula = %q, %q – %q, v%d, want 2026-11-26, 2026-10-27 – 2026-11-25, v1", d.Day, d.FirstDay, d.LastDay, d.Formula)
	}
	if d.Excluded != 1 || d.NotRankedYet != 2 {
		t.Errorf("DigestOf counts = %d excluded, %d not ranked yet, want 1, 2", d.Excluded, d.NotRankedYet)
	}
	if body, want := d.Body(), "#1 A 99.0 =\nNot ranked yet: 2"; body != want {
		t.Errorf("DigestOf body = %q, want %q", body, want)
	}
}

func TestDigestWritesTheShortAddressOfAnOperatorWithoutAName(t *testing.T) {
	cur := digestDay(1, nil, ranked("g1g0yrmdhney8pcaf9rfut4604equz604pszdsyt", "", 1, 97.5))
	body := DigestOf(cur, nil).Body()
	if want := "#1 g1g0yrmd…szdsyt 97.5\nRank changes not shown: no earlier ranking"; body != want {
		t.Errorf("DigestOf body = %q, want %q: the page's short form of the address", body, want)
	}
}

func TestDigestKeepsItsInputsIntact(t *testing.T) {
	cur := digestDay(1, []string{"x"}, ranked("a", "A", 1, 99))
	before := slices.Clone(cur.Entries)
	DigestOf(cur, nil)
	if !slices.EqualFunc(before, cur.Entries, func(a, b Entry) bool { return a.Operator == b.Operator && a.Rank == b.Rank }) {
		t.Errorf("DigestOf changed its day's entries: %+v, want %+v", cur.Entries, before)
	}
}

// TestDigestRoundsAScoreDown checks that a score is written rounded down
// to one decimal, as the page writes it: never better than it is, so 99.96
// reads 99.9, not 100.0.
func TestDigestRoundsAScoreDown(t *testing.T) {
	cur := digestDay(1, nil, ranked("a", "A", 1, 100), ranked("b", "B", 2, 99.96), ranked("c", "C", 3, 93.25))
	body := DigestOf(cur, nil).Body()
	if want := "#1 A 100.0\n#2 B 99.9\n#3 C 93.2\nRank changes not shown: no earlier ranking"; body != want {
		t.Errorf("DigestOf body = %q, want %q", body, want)
	}
}

// TestScoreText checks a score written to one decimal, rounded down, its
// float64 error absorbed first, as the page's scoreText writes it: 0.7 +
// 0.1, computed as 0.7999999999999999, reads 0.8.
func TestScoreText(t *testing.T) {
	seven, one := 0.7, 0.1 // added at run time, as float64s
	for _, tc := range []struct {
		score float64
		want  string
	}{
		{93.25, "93.2"},
		{62.25, "62.2"},
		{51.63123, "51.6"},
		{96.95, "96.9"},
		{99.96, "99.9"},
		{99.94999, "99.9"},
		{0.15, "0.1"},
		{0.05, "0.0"},
		{seven + one, "0.8"},
		{0, "0.0"},
		{100, "100.0"},
	} {
		if got := scoreText(tc.score); got != tc.want {
			t.Errorf("scoreText(%v) = %q, want %q", tc.score, got, tc.want)
		}
	}
}

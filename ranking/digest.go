package ranking

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Digest is the notification of a published day: a title naming the window
// and formula, the ranked operators in order, and notes; beside them, the
// day, window and formula, and the counts of the operators the rows leave
// out, for a service that lays them out itself.
type Digest struct {
	Title string
	Rows  []DigestRow
	Notes []string
	// Day is the day published, FirstDay and LastDay its window's first and
	// last days: YYYY-MM-DD, UTC.
	Day, FirstDay, LastDay string
	Formula                int // the formula version the day is ranked by
	// Excluded counts the operators on the exclusion list, NotRankedYet those
	// not ranked yet; neither is in Rows.
	Excluded, NotRankedYet int
}

// DigestRow is one ranked operator of a Digest. Name is the operator's name,
// or its address shortened as the page shows it. Score is written by
// scoreText. Change since the previous ranking is "▲n" or "▼n" places, "="
// or "new"; "" when the ranks are not comparable. Operators sharing a rank
// repeat its number.
type DigestRow struct {
	Rank                int
	Name, Score, Change string
}

// DigestOf writes the notification of the published day cur: its day,
// window and formula, its ranked operators with their change since prev,
// the counts of excluded operators and of those not ranked yet, then the
// notes: the count not ranked yet, and, when the changes are not shown, why.
// Excluded operators are counted, never listed.
//
// Changes are comparable only against a prev of the same formula and
// exclusion list: against any other prev, or none, the rows show no change
// and a note says why.
func DigestOf(cur Published, prev *Published) Digest {
	d := Digest{
		Title:    fmt.Sprintf("Validator ranking, %s – %s UTC (formula v%d)", cur.FirstDay, cur.LastDay, cur.Formula),
		Day:      cur.Day,
		FirstDay: cur.FirstDay,
		LastDay:  cur.LastDay,
		Formula:  cur.Formula,
	}
	reason := incomparable(cur, prev)
	var before map[string]int
	if reason == "" {
		before = ranksOf(*prev)
	}
	for _, e := range cur.Entries {
		switch e.Status {
		case Ranked:
			name := e.Name
			if name == "" {
				name = shortAddress(e.Operator)
			}
			row := DigestRow{Rank: e.Rank, Name: name, Score: scoreText(e.Score)}
			if reason == "" {
				row.Change = change(e.Rank, before[e.Operator])
			}
			d.Rows = append(d.Rows, row)
		case Excluded:
			d.Excluded++
		case NotRankedYet:
			d.NotRankedYet++
		}
	}
	if d.NotRankedYet > 0 {
		d.Notes = append(d.Notes, fmt.Sprintf("Not ranked yet: %d", d.NotRankedYet))
	}
	if reason != "" {
		d.Notes = append(d.Notes, "Rank changes not shown: "+reason)
	}
	return d
}

// Body writes the digest as lines: each row as "#<rank> <name> <score>",
// then its change when it has one, then the notes.
func (d Digest) Body() string {
	lines := make([]string, 0, len(d.Rows)+len(d.Notes))
	for _, r := range d.Rows {
		line := fmt.Sprintf("#%d %s %s", r.Rank, r.Name, r.Score)
		if r.Change != "" {
			line += " " + r.Change
		}
		lines = append(lines, line)
	}
	return strings.Join(append(lines, d.Notes...), "\n")
}

// scoreText writes a score to one decimal, rounded down as the page's
// scoreText writes it: never better than it is, so 99.96 reads 99.9. A
// nudge far below a tenth absorbs the float64 error of the score's sum:
// 0.7 + 0.1, 0.7999999999999999, reads 0.8. Scores are never negative.
func scoreText(score float64) string {
	// The conversion rounds the product before the sum, as JavaScript does:
	// Go may otherwise fuse them into one multiply-add.
	tenths := int64(math.Floor(float64(score*10) + 1e-9))
	return fmt.Sprintf("%d.%d", tenths/10, tenths%10)
}

// shortAddress shortens an address as the page's shortAddr does: its first 8
// and last 6 characters; one under 12 characters stays whole.
func shortAddress(addr string) string {
	if len(addr) < 12 {
		return addr
	}
	return addr[:8] + "…" + addr[len(addr)-6:]
}

// incomparable says why cur's ranks cannot be compared with prev's, "" when
// they can.
func incomparable(cur Published, prev *Published) string {
	switch {
	case prev == nil:
		return "no earlier ranking"
	case prev.Formula != cur.Formula:
		return fmt.Sprintf("formula v%d since the previous ranking", cur.Formula)
	case !slices.Equal(prev.Exclusions, cur.Exclusions):
		return "exclusion list changed since the previous ranking"
	}
	return ""
}

// ranksOf maps each ranked operator of p to its rank.
func ranksOf(p Published) map[string]int {
	ranks := make(map[string]int, len(p.Entries))
	for _, e := range p.Entries {
		if e.Status == Ranked {
			ranks[e.Operator] = e.Rank
		}
	}
	return ranks
}

// change shows a move from rank before (0: not ranked) to rank now.
func change(now, before int) string {
	switch {
	case before == 0:
		return "new"
	case now < before:
		return fmt.Sprintf("▲%d", before-now)
	case now > before:
		return fmt.Sprintf("▼%d", now-before)
	}
	return "="
}

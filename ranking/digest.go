package ranking

import (
	"fmt"
	"math"
	"slices"
	"strings"
)

// Digest writes the notification of a published day: a title naming the
// window and formula, and a body listing the ranked operators in order, each
// with its score and its change since prev, then the count of operators not
// ranked yet. An operator without a name shows its address shortened, as the
// page shows it. Operators sharing a rank repeat its number. Excluded
// operators are neither listed nor counted.
//
// Changes are comparable only against a prev of the same formula and
// exclusion list: against any other prev, or none, the body shows no change
// and ends with a line saying why.
func Digest(cur Published, prev *Published) (title, body string) {
	title = fmt.Sprintf("Validator ranking, %s – %s UTC (formula v%d)", cur.FirstDay, cur.LastDay, cur.Formula)
	var lines []string
	reason := incomparable(cur, prev)
	var before map[string]int
	if reason == "" {
		before = ranksOf(*prev)
	}
	waiting := 0
	for _, e := range cur.Entries {
		switch e.Status {
		case Ranked:
			name := e.Name
			if name == "" {
				name = shortAddress(e.Operator)
			}
			line := fmt.Sprintf("#%d %s %s", e.Rank, name, scoreText(e.Score))
			if reason == "" {
				line += " " + change(e.Rank, before[e.Operator])
			}
			lines = append(lines, line)
		case NotRankedYet:
			waiting++
		}
	}
	if waiting > 0 {
		lines = append(lines, fmt.Sprintf("Not ranked yet: %d", waiting))
	}
	if reason != "" {
		lines = append(lines, "Rank changes not shown: "+reason)
	}
	return title, strings.Join(lines, "\n")
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

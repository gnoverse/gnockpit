package push

import (
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gnoverse/gnockpit/ranking"
)

// discordGold is the ranking's colour, a trophy's.
const discordGold = 0xf1c40f

// rankingNameLimit is the most runes of a name the ranking's table shows.
const rankingNameLimit = 20

// discordRanking tells the ranking digest d as gold Discord embeds, titled
// with the trophy, the chain and the day published: its table in ANSI code
// blocks, continued in further embeds and messages when it is long, then its
// fields under the last one (rankingFields). The fields count the operators
// the table leaves out, so the digest's notes are not sent; a table whose
// ranks are not comparable shows no change.
func discordRanking(chainName, publicURL string, d ranking.Digest) []discordPayload {
	var lines []discordLine
	for _, l := range rankingTable(d.Rows) {
		lines = append(lines, discordLine{text: l, code: true})
	}
	head := discordEmbed{
		Title:  heading("🏆", chainName, "Validator ranking · "+monthDay(d.Day)),
		Color:  discordGold,
		Fields: rankingFields(publicURL, d),
	}
	return packEmbeds(head, lines)
}

// rankingFields are the fields under the ranking's table, two rows of two,
// a blank third field closing each row: the formula, linking the Ranking
// view's "How is this ranked?" dialog, and the window; the counts ranked and
// not ranked. With a public URL, a last field, not inline, holds the line
// that opens the Ranking view; without one, the formula links nothing.
func rankingFields(publicURL string, d ranking.Digest) []discordField {
	formula := fmt.Sprintf("v%d", d.Formula)
	if link := pageLink(publicURL, rankingExplainLink); link != "" {
		formula += " · " + maskedLink("More info ↗", link)
	}
	ranked := fmt.Sprintf("%d validators", len(d.Rows))
	if len(d.Rows) == 1 {
		ranked = "1 validator"
	}
	var notRanked []string
	if d.Excluded > 0 {
		notRanked = append(notRanked, fmt.Sprintf("%d excluded", d.Excluded))
	}
	if d.NotRankedYet > 0 {
		notRanked = append(notRanked, fmt.Sprintf("%d not ranked yet", d.NotRankedYet))
	}
	if len(notRanked) == 0 {
		notRanked = []string{"none"}
	}
	blank := discordField{Name: zwsp, Value: zwsp, Inline: true}
	fields := []discordField{
		{Name: "Formula", Value: formula, Inline: true},
		{Name: "Window", Value: monthDay(d.FirstDay) + " – " + monthDay(d.LastDay) + " UTC", Inline: true},
		blank,
		{Name: "Ranked", Value: ranked, Inline: true},
		{Name: "Not ranked", Value: strings.Join(notRanked, " · "), Inline: true},
		blank,
	}
	if link := pageLink(publicURL, rankingLink); link != "" {
		fields = append(fields, discordField{Name: zwsp, Value: openLine(link)})
	}
	return fields
}

// monthDay writes day, YYYY-MM-DD, as its month and day: "Nov 26". A day
// that does not parse is written as it is.
func monthDay(day string) string {
	t, err := time.Parse(time.DateOnly, day)
	if err != nil {
		return day
	}
	return t.Format("Jan 2")
}

// The ANSI codes of the ranking's table: the rank, name and change text is
// set between its code and ansiReset, its padding outside them.
const (
	ansiReset = "\x1b[0m"
	ansiRank  = "\x1b[1;33m" // bold yellow
	ansiName  = "\x1b[1m"    // bold, in the default text colour
	ansiUp    = "\x1b[0;32m" // green
	ansiDown  = "\x1b[0;31m" // red
	ansiNew   = "\x1b[0;34m" // blue
)

// backtickBreaker writes a zero-width space after each backtick, so that no
// name holds a code fence: no escape works inside a code block.
var backtickBreaker = strings.NewReplacer("`", "`"+zwsp)

// rankingTable sets rows as aligned lines: the rank "#<n>" in bold yellow,
// the name in bold, the score and the change, two spaces apart, each column
// as wide as its widest cell in runes. The score is right-aligned. The
// change is green "▲n", red "▼n", blue "new" or a plain "=", and left out
// when a row has none. A name's control and format characters show as
// U+FFFD (withoutControls), so no name writes an ANSI code or upsets the
// columns; a name over rankingNameLimit runes is then cut
// to its first rankingNameLimit-1 and "…". Backticks are broken once the
// line is set, so the zero-width space takes no column and a name cannot
// close the table's code block.
func rankingTable(rows []ranking.DigestRow) []string {
	type cells struct{ rank, name, score, change string }
	table := make([]cells, len(rows))
	var rankW, nameW, scoreW int
	for i, r := range rows {
		name := cutName(withoutControls(r.Name), rankingNameLimit)
		table[i] = cells{fmt.Sprintf("#%d", r.Rank), name, r.Score, r.Change}
		rankW = max(rankW, utf8.RuneCountInString(table[i].rank))
		nameW = max(nameW, utf8.RuneCountInString(name))
		scoreW = max(scoreW, utf8.RuneCountInString(r.Score))
	}
	pad := func(s string, w int) string { return strings.Repeat(" ", w-utf8.RuneCountInString(s)) }
	lines := make([]string, len(table))
	for i, c := range table {
		line := ansiCell(ansiRank, c.rank) + pad(c.rank, rankW) + "  " +
			ansiCell(ansiName, c.name) + pad(c.name, nameW) + "  " +
			pad(c.score, scoreW) + c.score
		if c.change != "" {
			line += "  " + changeCell(c.change)
		}
		lines[i] = backtickBreaker.Replace(line)
	}
	return lines
}

// ansiCell sets text between code and ansiReset.
func ansiCell(code, text string) string { return code + text + ansiReset }

// changeCell colours a row's change: "▲n" green, "▼n" red, "new" blue; "="
// stays plain.
func changeCell(change string) string {
	switch {
	case strings.HasPrefix(change, "▲"):
		return ansiCell(ansiUp, change)
	case strings.HasPrefix(change, "▼"):
		return ansiCell(ansiDown, change)
	case change == "new":
		return ansiCell(ansiNew, change)
	}
	return change
}

// zeroWidthJoiner joins the emoji of a sequence, such as a person and a
// laptop, into one.
const zeroWidthJoiner = '\u200d'

// withoutControls returns s with each control character (U+0000 to U+001F,
// U+007F to U+009F) and each format character (Unicode's Cf) as U+FFFD: an
// ESC or a CSI would open an ANSI code, a line break end the row, a
// direction override reorder it and a zero-width character take no column.
// The zero-width joiner stays, as the emoji it joins need it; the table
// counts their width in runes, as for any emoji, so their row may sit a
// column off.
func withoutControls(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || (unicode.Is(unicode.Cf, r) && r != zeroWidthJoiner) {
			return utf8.RuneError
		}
		return r
	}, s)
}

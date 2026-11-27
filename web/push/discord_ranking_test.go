package push

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/gnoverse/gnockpit/ranking"
)

func TestDiscordRankingLeavesNamesInTheBlockAsWritten(t *testing.T) {
	d := rankingDigest(ranking.DigestRow{Rank: 1, Name: "gno_validator_1", Score: "99.0", Change: "="})
	if lines := codeLines(discordRanking("gnoland-1", discordPublicURL, d)); len(lines) != 1 || !strings.Contains(lines[0], "gno_validator_1") {
		t.Errorf("code lines = %q, want the table's name as it is", lines)
	}
}

// rankingDigest is the digest of the ranking published on 2026-11-26 over
// 2026-10-27 – 2026-11-25 by formula v1, one operator excluded and two not
// ranked yet, listing rows. Its note, which a Discord target does not get,
// counts the operators not ranked yet.
func rankingDigest(rows ...ranking.DigestRow) ranking.Digest {
	return ranking.Digest{
		Title:        "Validator ranking, 2026-10-27 – 2026-11-25 UTC (formula v1)",
		Rows:         rows,
		Notes:        []string{"Not ranked yet: 2"},
		Day:          "2026-11-26",
		FirstDay:     "2026-10-27",
		LastDay:      "2026-11-25",
		Formula:      1,
		Excluded:     1,
		NotRankedYet: 2,
	}
}

// codeLines returns the lines of payloads' code blocks, in order.
func codeLines(payloads []discordPayload) []string {
	var out []string
	for _, p := range payloads {
		for _, e := range p.Embeds {
			in := false
			for _, l := range strings.Split(e.Description, "\n") {
				switch {
				case !in && l == "```ansi":
					in = true
				case in && l == "```":
					in = false
				case in:
					out = append(out, l)
				}
			}
		}
	}
	return out
}

// ansiCodes matches the ANSI codes the ranking's table colours its cells
// with.
var ansiCodes = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// checkScoresAligned checks that the scores of the table's lines, each
// starting with 9, start at one rune column once the lines' ANSI codes and
// zero-width spaces are taken out.
func checkScoresAligned(t *testing.T, lines []string) {
	t.Helper()
	col := -1
	for _, l := range lines {
		l = strings.ReplaceAll(ansiCodes.ReplaceAllString(l, ""), zwsp, "")
		i := strings.Index(l, "  9")
		if i < 0 {
			t.Errorf("line %q holds no score starting with 9", l)
			continue
		}
		c := utf8.RuneCountInString(l[:i+2])
		if col >= 0 && c != col {
			t.Errorf("lines %q: scores start at runes %d and %d, want one column", lines, col, c)
		}
		col = c
	}
}

// fieldValue returns the value of the field named name.
func fieldValue(t *testing.T, fields []discordField, name string) string {
	t.Helper()
	for _, f := range fields {
		if f.Name == name {
			return f.Value
		}
	}
	t.Fatalf("fields %+v hold none named %q", fields, name)
	return ""
}

func TestDiscordRankingPayload(t *testing.T) {
	d := rankingDigest(
		ranking.DigestRow{Rank: 1, Name: "Alice", Score: "99.0", Change: "▲1"},
		ranking.DigestRow{Rank: 1, Name: "Bob", Score: "99.0", Change: "="},
		ranking.DigestRow{Rank: 3, Name: "Carol", Score: "97.5", Change: "▼2"},
		ranking.DigestRow{Rank: 4, Name: "Dave", Score: "90.0", Change: "new"},
	)
	got := marshalPayloads(t, discordRanking("gnoland-1", discordPublicURL, d))
	const fence = "```"
	blank := `{"name":"` + zwsp + `","value":"` + zwsp + `","inline":true}`
	want := `{"embeds":[{"title":"🏆 gnoland-1 — Validator ranking · Nov 26","description":"` + fence + `ansi\n` +
		`\u001b[1;33m#1\u001b[0m  \u001b[1mAlice\u001b[0m  99.0  \u001b[0;32m▲1\u001b[0m\n` +
		`\u001b[1;33m#1\u001b[0m  \u001b[1mBob\u001b[0m    99.0  =\n` +
		`\u001b[1;33m#3\u001b[0m  \u001b[1mCarol\u001b[0m  97.5  \u001b[0;31m▼2\u001b[0m\n` +
		`\u001b[1;33m#4\u001b[0m  \u001b[1mDave\u001b[0m   90.0  \u001b[0;34mnew\u001b[0m\n` + fence + `",` +
		`"color":15844367,"fields":[` +
		`{"name":"Formula","value":"v1 · [More info ↗](https://gnockpit.example.com/?view=ranking\u0026explain)","inline":true},` +
		`{"name":"Window","value":"Oct 27 – Nov 25 UTC","inline":true},` + blank + `,` +
		`{"name":"Ranked","value":"4 validators","inline":true},` +
		`{"name":"Not ranked","value":"1 excluded · 2 not ranked yet","inline":true},` + blank + `,` +
		`{"name":"` + zwsp + `","value":"-# [Open in Gnockpit ↗](https://gnockpit.example.com/?view=ranking)","inline":false}` +
		`]}],"allowed_mentions":{"parse":[]}}`
	if len(got) != 1 || got[0] != want {
		t.Errorf("discordRanking(...) = %q, want [%s]", got, want)
	}
}

func TestDiscordRankingFieldsLinks(t *testing.T) {
	d := rankingDigest(ranking.DigestRow{Rank: 1, Name: "Alice", Score: "99.0", Change: "="})
	fields := rankingFields("https://x/", d)
	if got, want := fieldValue(t, fields, "Formula"), "v1 · [More info ↗](https://x/?view=ranking&explain)"; got != want {
		t.Errorf("rankingFields(https://x/) Formula = %q, want %q", got, want)
	}
	if got, want := fields[len(fields)-1].Value, "-# [Open in Gnockpit ↗](https://x/?view=ranking)"; got != want {
		t.Errorf("rankingFields(https://x/) last field = %q, want %q", got, want)
	}

	// A ')' would end a masked link: both parentheses are percent-encoded.
	fields = rankingFields("https://x/a(b)", d)
	if got, want := fieldValue(t, fields, "Formula"), "v1 · [More info ↗](https://x/a%28b%29/?view=ranking&explain)"; got != want {
		t.Errorf("rankingFields(https://x/a(b)) Formula = %q, want %q", got, want)
	}
	if got, want := fields[len(fields)-1].Value, "-# [Open in Gnockpit ↗](https://x/a%28b%29/?view=ranking)"; got != want {
		t.Errorf("rankingFields(https://x/a(b)) last field = %q, want %q", got, want)
	}

	fields = rankingFields("", d)
	if len(fields) != 6 {
		t.Errorf("rankingFields(no public URL) = %d fields, want 6", len(fields))
	}
	if got := fieldValue(t, fields, "Formula"); got != "v1" {
		t.Errorf("rankingFields(no public URL) Formula = %q, want v1", got)
	}
	for _, f := range fields {
		if strings.Contains(f.Value, "](") {
			t.Errorf("rankingFields(no public URL) field %+v holds a link, want none", f)
		}
	}
}

func TestDiscordRankingCounts(t *testing.T) {
	for _, tc := range []struct {
		excluded, notRankedYet int
		want                   string
	}{
		{0, 0, "none"},
		{1, 0, "1 excluded"},
		{0, 2, "2 not ranked yet"},
		{3, 2, "3 excluded · 2 not ranked yet"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			d := rankingDigest()
			d.Excluded, d.NotRankedYet = tc.excluded, tc.notRankedYet
			if got := fieldValue(t, rankingFields(discordPublicURL, d), "Not ranked"); got != tc.want {
				t.Errorf("rankingFields(%d excluded, %d not ranked yet) Not ranked = %q, want %q", tc.excluded, tc.notRankedYet, got, tc.want)
			}
		})
	}
	d := rankingDigest(ranking.DigestRow{Rank: 1, Name: "Alice", Score: "99.0", Change: "="})
	if got := fieldValue(t, rankingFields(discordPublicURL, d), "Ranked"); got != "1 validator" {
		t.Errorf("rankingFields(one row) Ranked = %q, want 1 validator", got)
	}
}

func TestMonthDay(t *testing.T) {
	for _, tc := range []struct{ name, day, want string }{
		{"a day", "2026-11-26", "Nov 26"},
		{"a day of one digit", "2026-01-05", "Jan 5"},
		{"a month out of range", "2026-13-01", "2026-13-01"},
		{"no date", "soon", "soon"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := monthDay(tc.day); got != tc.want {
				t.Errorf("monthDay(%q) = %q, want %q", tc.day, got, tc.want)
			}
		})
	}
}

func TestDiscordRankingWithoutChanges(t *testing.T) {
	rows := []ranking.DigestRow{
		{Rank: 1, Name: "Alice", Score: "99.0"},
		{Rank: 2, Name: "Bob", Score: "98.5"},
	}
	lines := rankingTable(rows)
	if len(lines) != len(rows) {
		t.Fatalf("rankingTable(%d rows) = %d lines, want %d", len(rows), len(lines), len(rows))
	}
	for i, l := range lines {
		if !strings.HasSuffix(l, " "+rows[i].Score) {
			t.Errorf("rankingTable line %d = %q, want it to end with the score %s", i, l, rows[i].Score)
		}
	}
}

func TestDiscordRankingTableAlignsColumns(t *testing.T) {
	var rows []ranking.DigestRow
	for i := 1; i <= 12; i++ {
		rows = append(rows, ranking.DigestRow{Rank: i, Name: "v", Score: "98.5", Change: "="})
	}
	rows[1].Score = "100.0"
	rows[2].Score = "99.9"
	long := strings.Repeat("é", 25)
	rows[3].Name = long
	rows[4].Change = ""
	exact := strings.Repeat("ü", 20)
	rows[5].Name = exact
	rows[6].Name = strings.Repeat("ü", 21)
	got := rankingTable(rows)
	// The name column is 20 runes wide, the score column 5.
	pad := func(n int) string { return strings.Repeat(" ", n) }
	want := []string{
		"#1   v" + pad(22) + "98.5  =",
		"#2   v" + pad(21) + "100.0  =",
		"#3   v" + pad(22) + "99.9  =",
		"#4   " + strings.Repeat("é", 19) + "…" + pad(3) + "98.5  =",
		"#5   v" + pad(22) + "98.5",
		"#6   " + exact + pad(2) + pad(1) + "98.5  =",
		"#7   " + strings.Repeat("ü", 19) + "…" + pad(3) + "98.5  =",
		"#8   v" + pad(22) + "98.5  =",
		"#9   v" + pad(22) + "98.5  =",
		"#10  v" + pad(22) + "98.5  =",
		"#11  v" + pad(22) + "98.5  =",
		"#12  v" + pad(22) + "98.5  =",
	}
	if len(got) != len(want) {
		t.Fatalf("rankingTable(%d rows) = %d lines, want %d", len(rows), len(got), len(want))
	}
	for i := range want {
		plain := ansiCodes.ReplaceAllString(got[i], "")
		if plain != want[i] {
			t.Errorf("rankingTable line %d without its codes = %q, want %q", i, plain, want[i])
		}
		if strings.HasSuffix(plain, " ") {
			t.Errorf("rankingTable line %d = %q, has a trailing space", i, got[i])
		}
	}
}

func TestDiscordRankingShowsNamesAsWritten(t *testing.T) {
	d := rankingDigest(
		ranking.DigestRow{Rank: 1, Name: "@everyone", Score: "99.0", Change: "="},
		ranking.DigestRow{Rank: 2, Name: "<!channel>", Score: "98.0", Change: "="},
		ranking.DigestRow{Rank: 3, Name: "plain", Score: "97.0", Change: "="},
	)
	lines := codeLines(discordRanking("gnoland-1", discordPublicURL, d))
	if len(lines) != 3 {
		t.Fatalf("discordRanking(...) code lines = %q, want 3", lines)
	}
	if !strings.Contains(lines[0], "@everyone") || !strings.Contains(lines[1], "<!channel>") {
		t.Errorf("code lines = %q, want the names as written", lines)
	}
	for _, l := range lines {
		if strings.Contains(l, zwsp) {
			t.Errorf("code line %q holds a zero-width space, want none", l)
		}
	}
	checkScoresAligned(t, lines)
}

func TestDiscordRankingReplacesControlCharacters(t *testing.T) {
	d := rankingDigest(
		ranking.DigestRow{Rank: 1, Name: "evil\x1b[31mred\x07", Score: "99.0", Change: "="},
		ranking.DigestRow{Rank: 2, Name: "line\nbreak\x7f", Score: "98.0", Change: "="},
		// The range's ends, U+0000 and U+001F, beside a space it leaves out.
		ranking.DigestRow{Rank: 3, Name: "a b\x00c\x1fd", Score: "97.0", Change: "="},
		// A C1 control, CSI, which opens an ANSI code as ESC [ does.
		ranking.DigestRow{Rank: 4, Name: "csi\u009b31mred", Score: "96.0", Change: "="},
		// Format characters: a right-to-left override, which reorders the
		// row, and zero-width spaces, which take no column.
		ranking.DigestRow{Rank: 5, Name: "rlo\u202eabc", Score: "95.0", Change: "="},
		ranking.DigestRow{Rank: 6, Name: "zw\u200b\u200b\u200bx", Score: "94.0", Change: "="},
		// The zero-width joiner, a format character too, stays: it joins an
		// emoji's parts into one. Widths count runes, as for any emoji, so
		// the row may sit a column off.
		ranking.DigestRow{Rank: 7, Name: "\U0001f469\u200d\U0001f4bb dev", Score: "93.0", Change: "="},
		ranking.DigestRow{Rank: 8, Name: "plain", Score: "92.0", Change: "="},
	)
	lines := codeLines(discordRanking("gnoland-1", discordPublicURL, d))
	if len(lines) != 8 {
		t.Fatalf("code lines = %q, want one per row", lines)
	}
	for i, want := range []string{"evil�[31mred�", "line�break�", "a b�c�d", "csi�31mred", "rlo�abc", "zw���x", "\U0001f469\u200d\U0001f4bb dev", "plain"} {
		if !strings.Contains(lines[i], want) {
			t.Errorf("code line %d = %q, want it to show the name %q, each control or format character but the joiner as U+FFFD", i, lines[i], want)
		}
	}
	for _, l := range lines {
		if strings.Contains(ansiCodes.ReplaceAllString(l, ""), "\x1b") {
			t.Errorf("code line %q holds an ESC beside the table's own codes", l)
		}
		if strings.ContainsAny(l, "\u009b\u202e\u200b") {
			t.Errorf("code line %q holds a CSI, an override or a zero-width space", l)
		}
	}
	checkScoresAligned(t, lines)
}

func TestDiscordRankingKeepsBackticksInsideTheBlock(t *testing.T) {
	d := rankingDigest(
		ranking.DigestRow{Rank: 1, Name: "evil```[x](https://e.co)", Score: "99.0", Change: "="},
		ranking.DigestRow{Rank: 2, Name: "plain", Score: "98.0", Change: "="},
	)
	lines := codeLines(discordRanking("gnoland-1", discordPublicURL, d))
	if len(lines) != 2 {
		t.Fatalf("code lines = %q, want one per row", lines)
	}
	for _, l := range lines {
		if strings.Contains(l, "``") {
			t.Errorf("row %q holds two backticks in a row", l)
		}
	}
	checkScoresAligned(t, lines)
}

func TestDiscordRankingWithNothingRanked(t *testing.T) {
	d := rankingDigest()
	d.Excluded, d.NotRankedYet = 0, 3
	payloads := discordRanking("gnoland-1", discordPublicURL, d)
	if len(payloads) != 1 || len(payloads[0].Embeds) != 1 {
		t.Fatalf("discordRanking(no rows) = %+v, want one embed", payloads)
	}
	e := payloads[0].Embeds[0]
	if want := "🏆 gnoland-1 — Validator ranking · Nov 26"; e.Title != want {
		t.Errorf("title = %q, want %q", e.Title, want)
	}
	if got := marshalPayloads(t, payloads)[0]; strings.Contains(got, `"description"`) {
		t.Errorf("payload = %s, want no description", got)
	}
	if got := fieldValue(t, e.Fields, "Ranked"); got != "0 validators" {
		t.Errorf("Ranked = %q, want 0 validators", got)
	}
	if got := fieldValue(t, e.Fields, "Not ranked"); got != "3 not ranked yet" {
		t.Errorf("Not ranked = %q, want 3 not ranked yet", got)
	}
}

func rankingRows(n int) []ranking.DigestRow {
	rows := make([]ranking.DigestRow, n)
	for i := range rows {
		rows[i] = ranking.DigestRow{Rank: i + 1, Name: fmt.Sprintf("operator-%011d", i), Score: "99.0", Change: "="}
	}
	return rows
}

func TestDiscordRankingSpansEmbedsAndMessages(t *testing.T) {
	rows := rankingRows(400)
	d := rankingDigest(rows...)
	payloads := discordRanking("gnoland-1", discordPublicURL, d)
	if len(payloads) < 3 {
		t.Fatalf("discordRanking(400 rows) = %d payloads, want at least 3", len(payloads))
	}
	if n := len(payloads[0].Embeds); n != 2 {
		t.Errorf("first payload holds %d embeds, want 2", n)
	}
	for i, desc := range discordDescriptions(t, payloads, discordGold) {
		if !strings.HasPrefix(desc, "```ansi\n") || !strings.HasSuffix(desc, "\n```") || strings.Count(desc, "```") != 2 {
			t.Errorf("description %d = %q…, want one ansi code block", i, desc[:min(20, len(desc))])
		}
	}
	for i, p := range payloads {
		for j, e := range p.Embeds {
			var want []discordField
			if i == len(payloads)-1 && j == len(p.Embeds)-1 {
				want = rankingFields(discordPublicURL, d)
			}
			if !slices.Equal(e.Fields, want) {
				t.Errorf("payload %d embed %d fields = %+v, want %+v", i, j, e.Fields, want)
			}
		}
	}
	if got, want := codeLines(payloads), rankingTable(rows); !slices.Equal(got, want) {
		t.Errorf("code lines = %d lines, want the table's %d once each, in order", len(got), len(want))
	}
}

func TestNotifyRankingSendsDiscordEmbeds(t *testing.T) {
	posts := recordDiscord(t)
	m := newTestManager(t, false)
	captureLog(t)
	cfg := NotifyConfig{Targets: []NotifyRoute{{URL: "discord://SECRETTOKEN@123456789", Alerts: map[AlertType]AlertOptions{AlertRanking: {}}}}}
	if err := m.SetNotifyConfig(cfg); err != nil {
		t.Fatal(err)
	}
	m.SetPublicURL(discordPublicURL)
	d := rankingDigest(rankingRows(400)...)
	m.NotifyRanking("gnoland-1:2026-11-25", d)
	want := marshalPayloads(t, discordRanking(m.ChainName(), discordPublicURL, d))
	if len(want) < 3 {
		t.Fatalf("want %d payloads, expected at least 3", len(want))
	}
	if len(*posts) != len(want) {
		t.Fatalf("NotifyRanking posted %d times, want %d", len(*posts), len(want))
	}
	for i, p := range *posts {
		if p.body != want[i] {
			t.Errorf("post %d body = %.120s…, want %.120s…", i, p.body, want[i])
		}
	}
	m.NotifyRanking("gnoland-1:2026-11-25", d)
	if len(*posts) != len(want) {
		t.Errorf("NotifyRanking of the same key again posted %d times in all, want %d", len(*posts), len(want))
	}
}

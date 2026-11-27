package push

import (
	"strings"
	"testing"
)

// markdownText returns the text of markdownLines(b), its lines joined by
// line breaks.
func markdownText(b Body) string {
	lines := markdownLines(b)
	texts := make([]string, len(lines))
	for i, l := range lines {
		texts[i] = l.text
	}
	return strings.Join(texts, "\n")
}

func TestMarkdownLinesBoldsAdjacentBoldSpansAsOne(t *testing.T) {
	for _, tc := range []struct {
		name string
		body Body
		want string
	}{
		{"two bold spans", Body{bold("a"), bold("b")}, "**ab**"},
		{"an empty span between them", Body{bold("a"), plain(""), bold("b")}, "**ab**"},
		{"across a line break", Body{bold("a\nb"), bold("c")}, "**a**\n**bc**"},
		{"a plain span between them", Body{bold("a"), plain(" "), bold("b")}, "**a** **b**"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := markdownText(tc.body); got != tc.want {
				t.Errorf("markdownLines(%q) = %q, want %q", marked(tc.body), got, tc.want)
			}
		})
	}
}

func TestMarkdownLinesBoldsTheTextOfABoldSpanOnly(t *testing.T) {
	for _, tc := range []struct {
		name string
		body Body
		want string
	}{
		{"a name ending with a space", missedBody("foo ", 10), "**foo**  missed **10** blocks in a row"},
		{"a name starting with spaces", missedBody("  foo", 10), "  **foo** missed **10** blocks in a row"},
		{"a name ending with a tab", missedBody("foo\t", 10), "**foo**\t missed **10** blocks in a row"},
		{"a name starting with a tab", missedBody("\tfoo", 10), "\t**foo** missed **10** blocks in a row"},
		{"a name starting with a no-break space", missedBody("\u00a0foo", 10), "\u00a0**foo** missed **10** blocks in a row"},
		{"a name with a line break", missedBody("foo\nbar", 10), "**foo**\n**bar** missed **10** blocks in a row"},
		{"a name of spaces", missedBody("   ", 10), "    missed **10** blocks in a row"},
		{"a name of a line break", missedBody("\n", 10), "\n missed **10** blocks in a row"},
		{"an empty bold span before a line's dash", Body{bold(""), plain("-x")}, `\-x`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := markdownText(tc.body); got != tc.want {
				t.Errorf("markdownLines(%q) = %q, want %q", marked(tc.body), got, tc.want)
			}
		})
	}
}

func TestDiscordAlertEscapesMarkdownInTheBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		body Body
		want string
	}{
		{
			"plain text",
			Body{plain("gno_validator_1 and [x](https://e.co) missed *10* blocks\n- ~~a~~ |b| `c` \\d\n# e\n> f\ng-h #i >j<t:0>")},
			"gno\\_validator\\_1 and \\[x\\](https\\://e.co) missed \\*10\\* blocks\n" +
				"\\- \\~\\~a\\~\\~ \\|b\\| \\`c\\` \\\\d\n\\# e\n\\> f\ng-h #i >j\\<t:0>",
		},
		{"a bold span opening a line", Body{bold("-a_b"), plain(" x")}, `**\-a\_b** x`},
		{"a bold span opening a later line", Body{plain("x\n"), bold("#y")}, "x\n**\\#y**"},
		{"a bold quote opening a line", Body{bold("> x")}, `**\> x**`},
		{"a bold list item of a plus opening a line", Body{bold("+ x")}, `**\+ x**`},
		{"a bold span after white space", Body{bold("  # x"), plain(" y")}, `  **\# x** y`},
		{"a bold span after a plain span of white space", Body{plain(" "), bold("- x")}, ` **\- x**`},
		{"a bold span's later line", Body{bold("a\n12. b")}, "**a**\n**12\\. b**"},
		{"a bold span within a line", Body{plain("a "), bold("- x"), plain(" "), bold("1. y")}, "a **- x** **1. y**"},
		{"a plain span after a bold span of white space", Body{bold("  "), plain("- x")}, `  \- x`},
		{"a plain span within a line", Body{bold("a"), plain("-b #c >d")}, "**a**-b #c >d"},
		{"numbered lines", Body{plain("1. Validator\n12. b")}, "1\\. Validator\n12\\. b"},
		{"a list item after a space", Body{plain(" - item")}, ` \- item`},
		{"a heading after spaces", Body{plain("  # big")}, `  \# big`},
		{"a list item after a tab", Body{plain("\t- item")}, "\t\\- item"},
		{"a list item after a no-break space", Body{plain("\u00a0- item")}, "\u00a0\\- item"},
		{"a list item of a plus", Body{plain("+ plus")}, `\+ plus`},
		{"a number within a line", Body{plain("Mid 1. text")}, "Mid 1. text"},
		{"a bold span opening a line with a number", Body{bold("1. Validator"), plain(" x")}, "**1\\. Validator** x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := Alert{Type: AlertValidatorMissingBlocks, Firing: true, Title: "Validator missing blocks", Body: tc.body}
			payloads := discordAlert("gnoland-1", "", a)
			if len(payloads) != 1 || len(payloads[0].Embeds) != 1 || payloads[0].Embeds[0].Description != tc.want {
				t.Fatalf("discordAlert(%q) = %+v, want one embed described %q", marked(tc.body), payloads, tc.want)
			}
			if got := payloads[0].Embeds[0].Title; got != "🚨 gnoland-1 — Validator missing blocks" {
				t.Errorf("title = %q, want it as it is", got)
			}
		})
	}
}

// TestDiscordAlertShowsMentionsAndURLsAsText checks that a mention or a URL
// a body quotes shows as plain text in Discord, never as a pill or a link,
// while a text target gets the body as FormatAlert writes it.
func TestDiscordAlertShowsMentionsAndURLsAsText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		body    Body
		discord string
		text    string
	}{
		{"mentions", Body{plain("@everyone <@123>")}, `\@everyone \<\@123>`, "@" + zwsp + "everyone <" + zwsp + "@" + zwsp + "123>"},
		{"a mention in bold", Body{bold("@here"), plain(" x")}, `**\@here** x`, "@" + zwsp + "here x"},
		{"a mention within a line", Body{plain("ops@e.co")}, `ops\@e.co`, "ops@" + zwsp + "e.co"},
		{"a URL", Body{plain("see https://e.co/x")}, `see https\://e.co/x`, "see https://e.co/x"},
		{
			"a URL in bold",
			Body{bold("gno_validator_[x](https://e.co/)"), plain(" x")},
			`**gno\_validator\_\[x\](https\://e.co/)** x`, "gno_validator_[x](https://e.co/) x",
		},
		{"every URL of a line", Body{plain("a://b c://d")}, `a\://b c\://d`, "a://b c://d"},
		{"a URL with a backslash before its colon", Body{plain(`https\://e.co`)}, `https\\\://e.co`, `https\://e.co`},
		{"colons of no URL", Body{plain("at 10:30, mailto:x :/")}, "at 10:30, mailto:x :/", "at 10:30, mailto:x :/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := Alert{Type: AlertValidatorMissingBlocks, Firing: true, Title: "Validator missing blocks", Body: tc.body}
			payloads := discordAlert("gnoland-1", "", a)
			if len(payloads) != 1 || len(payloads[0].Embeds) != 1 || payloads[0].Embeds[0].Description != tc.discord {
				t.Errorf("discordAlert(%q) = %+v, want one embed described %q", marked(tc.body), payloads, tc.discord)
			}
			want := "🚨 gnoland-1 — Validator missing blocks\n" + tc.text
			if got := FormatAlert("gnoland-1", "", a); got != want {
				t.Errorf("FormatAlert(%q) = %q, want %q", marked(tc.body), got, want)
			}
		})
	}
}

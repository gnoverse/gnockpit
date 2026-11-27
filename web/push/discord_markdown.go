package push

import (
	"strings"
	"unicode"
)

// markdownEscaper puts a backslash before each character Discord reads as
// Markdown inside a line; before '<' and '@', which open the mentions,
// channels and timestamps Discord draws as pills even when nothing pings;
// and before the ':' of each "://", as Discord links a URL written with its
// scheme.
var markdownEscaper = strings.NewReplacer(
	`\`, `\\`, "*", `\*`, "_", `\_`, "~", `\~`, "|", `\|`, "`", "\\`",
	"[", `\[`, "]", `\]`, "<", `\<`, "@", `\@`, "://", `\://`,
)

// markdownLines splits b at line breaks into lines whose Markdown Discord
// shows as written, each run of adjacent bold spans between one pair of
// "**": a name such as gno_validator_1 stays as it is instead of turning
// italic, a "[x](https://…)" is no masked link, an "https://…" no link and
// an "@everyone" or a "<@id>" no mention. A
// span opening a line, plain or bold, has its block marker escaped too
// (blockMarker), which would open a heading, a quote or a list item. An
// empty body has no lines.
func markdownLines(b Body) []discordLine {
	if b.String() == "" {
		return nil
	}
	var lines []discordLine
	var line strings.Builder
	for _, s := range joinBold(b) {
		for i, text := range strings.Split(s.Text, "\n") {
			if i > 0 {
				lines = append(lines, discordLine{text: line.String()})
				line.Reset()
			}
			writeMarkdown(&line, Span{Text: text, Bold: s.Bold})
		}
	}
	return append(lines, discordLine{text: line.String()})
}

// joinBold returns b without its empty spans, each run of adjacent bold spans
// joined into one: written apart, two bold spans would read "**a****b**".
func joinBold(b Body) Body {
	var out Body
	for _, s := range b {
		if s.Text == "" {
			continue
		}
		if n := len(out); n > 0 && s.Bold && out[n-1].Bold {
			out[n-1].Text += s.Text
			continue
		}
		out = append(out, s)
	}
	return out
}

// writeMarkdown appends s, a span's text within one line, to line, escaped.
// Discord reads no bold that opens or closes on white space, so a bold span's
// white space at either end goes outside its "**", and a bold span of white
// space only is written plain. A span opening the line, only white space
// before it, has its block marker escaped too, inside the "**" of a bold
// span as well: Discord reads the block within the bold.
func writeMarkdown(line *strings.Builder, s Span) {
	opens := strings.TrimLeftFunc(line.String(), unicode.IsSpace) == ""
	lead := len(s.Text) - len(strings.TrimLeftFunc(s.Text, unicode.IsSpace))
	core := strings.TrimRightFunc(s.Text[lead:], unicode.IsSpace)
	if !s.Bold || core == "" {
		line.WriteString(escapeMarkdown(s.Text, opens))
		return
	}
	line.WriteString(s.Text[:lead])
	line.WriteString("**" + escapeMarkdown(core, opens) + "**")
	line.WriteString(s.Text[lead+len(core):])
}

// escapeMarkdown returns text escaped (markdownEscaper), with a backslash
// before its block marker (blockMarker) when it opens a line.
func escapeMarkdown(text string, opensLine bool) string {
	if at := blockMarker(text); opensLine && at >= 0 {
		// What comes before the marker, white space and digits, holds
		// nothing to escape.
		return text[:at] + `\` + markdownEscaper.Replace(text[at:])
	}
	return markdownEscaper.Replace(text)
}

// blockMarker returns where text, opening a line, has the character that
// makes the line a block in Discord, a backslash before it showing it as
// written: after any white space, a '#', '>', '-' or '+' opening a heading,
// a quote or a list item, or the '.' after the digits that open a numbered
// item. It returns -1 for a line that opens none.
func blockMarker(text string) int {
	at := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
	rest := text[at:]
	if rest != "" && strings.ContainsRune("#>-+", rune(rest[0])) {
		return at
	}
	digits := len(rest) - len(strings.TrimLeft(rest, "0123456789"))
	if digits > 0 && strings.HasPrefix(rest[digits:], ".") {
		return at + digits
	}
	return -1
}

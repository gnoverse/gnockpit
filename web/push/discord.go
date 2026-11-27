package push

import (
	"encoding/json"
	"log"
	"net/url"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/containrrr/shoutrrr/pkg/services/discord"
)

// The dashboard's own red, green and blue.
const (
	discordRed   = 0xf85149
	discordGreen = 0x3fb950
	discordBlue  = 0x58a6ff
)

// A run of code lines is set in an ANSI code block, which Discord colours
// by the ANSI codes it holds.
const (
	discordFenceOpen  = "```ansi\n"
	discordFenceClose = "\n```"
)

// Discord's limits, counted in UTF-8 bytes, never fewer than the characters
// Discord counts.
const (
	discordDescriptionLimit = 4096
	discordEmbedsPerPayload = 10
	discordPayloadLimit     = 6000
	// discordFenceRoom is what a run of code lines adds: its two fences.
	discordFenceRoom = len(discordFenceOpen) + len(discordFenceClose)
	discordLineLimit = discordDescriptionLimit - discordFenceRoom
)

// discordPayload is the JSON a Discord webhook takes, in shoutrrr's json mode.
type discordPayload struct {
	Username        string          `json:"username,omitempty"`
	AvatarURL       string          `json:"avatar_url,omitempty"`
	Embeds          []discordEmbed  `json:"embeds"`
	AllowedMentions discordMentions `json:"allowed_mentions"`
}

// discordMentions lists the kinds of mention Discord may act on in a
// payload's text: an empty Parse allows none, so no name the text quotes
// pings anyone.
type discordMentions struct {
	Parse []string `json:"parse"`
}

type discordEmbed struct {
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	Color       int            `json:"color"`
	Fields      []discordField `json:"fields,omitempty"`
}

// discordField is a name and value set under an embed's description; inline
// fields share a row, up to three.
type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline"`
}

// discordLine is one line of an embed's description; a code line is set in a
// code block.
type discordLine struct {
	text string
	code bool
}

// discordHook is the part of a Discord URL that json mode drops: shoutrrr
// posts the payload as given, so gnockpit puts them in it.
type discordHook struct{ username, avatarURL string }

// senderURL returns the URL a target's sender is created from, and its hook.
// A discord:// URL gets json=yes in its query, which shoutrrr reads only
// from the URL; any other URL is returned as it is, without a hook.
func senderURL(rawURL string) (string, *discordHook, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", nil, err
	}
	if u.Scheme != "discord" {
		return rawURL, nil, nil
	}
	var cfg discord.Config
	if err := cfg.SetURL(u); err != nil {
		return "", nil, err
	}
	q := u.Query()
	for key := range q {
		if strings.EqualFold(key, "json") {
			q.Del(key)
		}
	}
	q.Set("json", "yes")
	u.RawQuery = q.Encode()
	return u.String(), &discordHook{username: cfg.Username, avatarURL: cfg.Avatar}, nil
}

// discordAlert tells a as Discord embeds: one in the colour of its kind,
// titled with its kind's icon, the chain and its title, its body the
// description, key facts bold. With a public URL, the description ends with
// the line that opens the alert's page, after a blank line when the body
// holds one, so the link stands apart from the body's last paragraph. A long
// body continues in further embeds and messages.
func discordAlert(chainName, publicURL string, a Alert) []discordPayload {
	kind := alertKindOf(a)
	lines := markdownLines(a.Body)
	if link := alertLink(publicURL, a); link != "" {
		if slices.ContainsFunc(lines, func(l discordLine) bool { return l.text == "" }) {
			lines = append(lines, discordLine{})
		}
		lines = append(lines, discordLine{text: openLine(link)})
	}
	return packEmbeds(discordEmbed{Title: heading(kind.icon, chainName, a.Title), Color: kind.color}, lines)
}

// openLine is the line that opens link, a masked link in Discord's small
// text.
func openLine(link string) string {
	return "-# " + maskedLink("Open in Gnockpit ↗", link)
}

// linkParens percent-encodes the parentheses of a URL, which name the same
// characters to the server.
var linkParens = strings.NewReplacer("(", "%28", ")", "%29")

// maskedLink is Discord's masked link: text, linking link. A ')' in link
// would end the masked link, so its parentheses are percent-encoded
// (linkParens).
func maskedLink(text, link string) string {
	return "[" + text + "](" + linkParens.Replace(link) + ")"
}

// discordTest tells a test message as one blue embed, its lines as they are.
func discordTest(message string) []discordPayload {
	return packEmbeds(discordEmbed{Color: discordBlue}, plainLines(message))
}

// plainLines splits s at line breaks into lines set as they are; "" has none.
func plainLines(s string) []discordLine {
	if s == "" {
		return nil
	}
	var lines []discordLine
	for _, l := range strings.Split(s, "\n") {
		lines = append(lines, discordLine{text: l})
	}
	return lines
}

// packEmbeds sets lines in embeds and the embeds in payloads within Discord's
// limits. head's title goes on the first embed only, its colour on every
// embed, its fields on the last embed of the last payload; every payload
// counts the fields against its limit, as which one is last is known only
// once all are packed. Every payload allows no mention, so no name it quotes
// pings anyone. A line too long for an embed is cut at rune boundaries.
func packEmbeds(head discordEmbed, lines []discordLine) []discordPayload {
	var pieces []discordLine
	for _, l := range lines {
		for len(l.text) > discordLineLimit {
			// A rune is at most UTFMax bytes: invalid bytes have no start to
			// back off to, so the cut stays where the search ends.
			cut := discordLineLimit
			for i := 1; i < utf8.UTFMax && !utf8.RuneStart(l.text[cut]); i++ {
				cut--
			}
			pieces = append(pieces, discordLine{text: l.text[:cut], code: l.code})
			l.text = l.text[cut:]
		}
		pieces = append(pieces, l)
	}

	// payloads[i][j] holds the lines of embed j of payload i. size is what a
	// payload counts against Discord's limit: p's embeds and extra, one more
	// embed, the fields, and the title when the payload is the first.
	fieldsLen := 0
	for _, f := range head.Fields {
		fieldsLen += len(f.Name) + len(f.Value)
	}
	payloads := [][][]discordLine{{{}}}
	size := func(p [][]discordLine, extra []discordLine) int {
		n := descriptionLen(extra) + fieldsLen
		if len(payloads) == 1 {
			n += len(head.Title)
		}
		for _, e := range p {
			n += descriptionLen(e)
		}
		return n
	}
	for _, l := range pieces {
		p := payloads[len(payloads)-1]
		cur := p[len(p)-1]
		next := append(cur[:len(cur):len(cur)], l)
		// A line joins an embed holding a line when the two stay within the
		// limits; the first line of all opens the first embed.
		joins := len(cur) > 0 && descriptionLen(next) <= discordDescriptionLimit && size(p[:len(p)-1], next) <= discordPayloadLimit
		if l.text == "" && !joins {
			// A blank line opens no embed, so no payload: alone in an embed
			// it shows nothing, and the break between two embeds already
			// parts the paragraphs.
			continue
		}
		if joins || len(cur) == 0 {
			p[len(p)-1] = next
			continue
		}
		// With these limits the embed count cannot bind. A line opens an
		// embed when joining the one before passes the description's limit,
		// the two then holding at least 4,096 bytes, or the payload's, which
		// the line then fills: opening costs at most a byte less than
		// joining, and no blank line opens one. So a payload holds at most 4
		// embeds; the check stays as Discord's own rule.
		if len(p) < discordEmbedsPerPayload && size(p, []discordLine{l}) <= discordPayloadLimit {
			payloads[len(payloads)-1] = append(p, []discordLine{l})
			continue
		}
		payloads = append(payloads, [][]discordLine{{l}})
	}

	out := make([]discordPayload, len(payloads))
	for pi, p := range payloads {
		out[pi].AllowedMentions = discordMentions{Parse: []string{}}
		for ei, e := range p {
			embed := discordEmbed{Description: description(e), Color: head.Color}
			if pi == 0 && ei == 0 {
				embed.Title = head.Title
			}
			out[pi].Embeds = append(out[pi].Embeds, embed)
		}
	}
	last := out[len(out)-1].Embeds
	last[len(last)-1].Fields = head.Fields
	return out
}

// description joins lines with line breaks, each run of code lines in an
// ANSI code block.
func description(lines []discordLine) string {
	var sb strings.Builder
	for i, l := range lines {
		if i > 0 {
			sb.WriteByte('\n')
		}
		if l.code && (i == 0 || !lines[i-1].code) {
			sb.WriteString(discordFenceOpen)
		}
		sb.WriteString(l.text)
		if l.code && (i == len(lines)-1 || !lines[i+1].code) {
			sb.WriteString(discordFenceClose)
		}
	}
	return sb.String()
}

func descriptionLen(lines []discordLine) int { return len(description(lines)) }

// discordMessages marshals payloads as t's webhook takes them, with the
// username and avatar of its URL.
func discordMessages(t notifyTarget, payloads []discordPayload) []string {
	messages := make([]string, 0, len(payloads))
	for _, p := range payloads {
		if t.discord != nil {
			p.Username, p.AvatarURL = t.discord.username, t.discord.avatarURL
		}
		b, err := json.Marshal(p)
		if err != nil {
			log.Printf("notify: discord payload: %v", err)
			continue
		}
		messages = append(messages, string(b))
	}
	return messages
}

// sendDiscord posts payloads to t, one of targets, in order.
func (m *Manager) sendDiscord(t notifyTarget, payloads []discordPayload, targets []notifyTarget) {
	for _, msg := range discordMessages(t, payloads) {
		m.sendText(t, msg, targets)
	}
}

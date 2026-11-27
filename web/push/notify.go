package push

import (
	"fmt"
	"strings"
)

// zwsp is the zero-width space: written after a character, it keeps the
// text reading the same while breaking what the character would open.
const zwsp = "\u200b"

// mentionBreaker writes a zero-width space after each '@' and '<': a name a
// message quotes comes from a peer, the genesis or a valoper profile, and
// can read as a mention, such as Discord's @everyone, <@id> and <@&id> or
// Slack's <!channel>, which would ping a channel's members. The text reads
// the same.
var mentionBreaker = strings.NewReplacer("@", "@"+zwsp, "<", "<"+zwsp)

// FormatAlert formats an Alert as a plain text message for external services,
// its heading then its body plain, every mention its text could hold broken
// (mentionBreaker). If publicURL is non-empty, it is appended as a link on
// its own line, as it is, with the alert's Link, a path from the site's root,
// when it has one.
func FormatAlert(chainName, publicURL string, a Alert) string {
	msg := mentionBreaker.Replace(heading(alertKindOf(a).icon, chainName, a.Title) + "\n" + a.Body.String())
	if link := alertLink(publicURL, a); link != "" {
		msg += "\n" + link
	}
	return msg
}

// alertKind is how a kind of alert is told: its icon, and its colour in a
// Discord embed.
type alertKind struct {
	icon  string
	color int
}

// alertKindOf returns the kind of a: a notice, a recovery or, otherwise,
// a firing alert.
func alertKindOf(a Alert) alertKind {
	switch {
	case a.Notice:
		return alertKind{"📣", discordBlue}
	case !a.Firing:
		return alertKind{"✅", discordGreen}
	}
	return alertKind{"🚨", discordRed}
}

// heading is a message's title line: the icon and the title, the chain
// between them unless chainName is "".
func heading(icon, chainName, title string) string {
	if chainName != "" {
		return fmt.Sprintf("%s %s — %s", icon, chainName, title)
	}
	return fmt.Sprintf("%s %s", icon, title)
}

// alertLink is the page a tap on a opens: publicURL, with the alert's Link
// when it has one (pageLink).
func alertLink(publicURL string, a Alert) string {
	return pageLink(publicURL, a.Link)
}

// pageLink is the address of path, a path from the site's root, at
// publicURL: publicURL without its trailing '/', then path; publicURL alone
// for no path; "" without a public URL.
func pageLink(publicURL, path string) string {
	if publicURL == "" || path == "" {
		return publicURL
	}
	return strings.TrimSuffix(publicURL, "/") + path
}

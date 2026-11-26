package push

import (
	"fmt"
	"strings"
)

// mentionBreaker writes a zero-width space after each '@' and '<': a name a
// message quotes comes from a peer, the genesis or a valoper profile, and
// can read as a mention, such as Discord's @everyone, <@id> and <@&id> or
// Slack's <!channel>, which would ping a channel's members. The text reads
// the same.
var mentionBreaker = strings.NewReplacer("@", "@\u200b", "<", "<\u200b")

// FormatAlert formats an Alert as a plain text message for external services,
// every mention its text could hold broken (mentionBreaker). If publicURL is
// non-empty, it is appended as a link on its own line, as it is, with the
// alert's Link, a path from the site's root, when it has one.
func FormatAlert(chainName, publicURL string, a Alert) string {
	icon := "🚨"
	switch {
	case a.Notice:
		icon = "🔔"
	case !a.Firing:
		icon = "✅"
	}
	var msg string
	if chainName != "" {
		msg = fmt.Sprintf("%s %s — %s\n%s", icon, chainName, a.Title, a.Body)
	} else {
		msg = fmt.Sprintf("%s %s\n%s", icon, a.Title, a.Body)
	}
	msg = mentionBreaker.Replace(msg)
	if publicURL != "" {
		link := publicURL
		if a.Link != "" {
			link = strings.TrimSuffix(publicURL, "/") + a.Link
		}
		msg += "\n" + link
	}
	return msg
}

package push

import (
	"fmt"
)

// FormatAlert formats an Alert as a plain text message for external services.
// If publicURL is non-empty, it is appended as a link on its own line.
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
	if publicURL != "" {
		msg += "\n" + publicURL
	}
	return msg
}

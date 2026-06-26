package push

import (
	"fmt"
	"log"

	"github.com/containrrr/shoutrrr"
	"github.com/containrrr/shoutrrr/pkg/router"
)

// NewNotifier creates a Shoutrrr service router for the given URLs.
// Empty strings are filtered out. Returns nil if no valid URLs remain.
func NewNotifier(urls []string) (*router.ServiceRouter, error) {
	var valid []string
	for _, u := range urls {
		if u != "" {
			valid = append(valid, u)
		}
	}
	if len(valid) == 0 {
		return nil, nil
	}
	r, err := shoutrrr.CreateSender(valid...)
	if err != nil {
		return nil, fmt.Errorf("create shoutrrr sender: %w", err)
	}
	log.Printf("notify: configured %d notification URL(s)", len(valid))
	return r, nil
}

// FormatAlert formats an Alert as a plain text message for external services.
// If publicURL is non-empty, it is appended as a link on its own line.
func FormatAlert(chainName, publicURL string, a Alert) string {
	icon := "🚨"
	if !a.Firing {
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

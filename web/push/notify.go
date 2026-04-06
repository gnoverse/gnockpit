package push

import (
	"fmt"
	"log"

	"github.com/containrrr/shoutrrr"
	"github.com/containrrr/shoutrrr/pkg/router"
)

// NewNotifier creates a Shoutrrr service router for the given URLs.
// Returns nil if urls is empty.
func NewNotifier(urls []string) (*router.ServiceRouter, error) {
	if len(urls) == 0 {
		return nil, nil
	}
	r, err := shoutrrr.CreateSender(urls...)
	if err != nil {
		return nil, fmt.Errorf("create shoutrrr sender: %w", err)
	}
	log.Printf("notify: configured %d notification URL(s)", len(urls))
	return r, nil
}

// FormatAlert formats an Alert as a plain text message for external services.
func FormatAlert(chainName string, a Alert) string {
	icon := "🚨"
	if !a.Firing {
		icon = "✅"
	}
	if chainName != "" {
		return fmt.Sprintf("%s %s — %s\n%s", icon, chainName, a.Title, a.Body)
	}
	return fmt.Sprintf("%s %s\n%s", icon, a.Title, a.Body)
}

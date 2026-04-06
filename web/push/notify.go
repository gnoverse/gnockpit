package push

import (
	"fmt"
	"log"
	"strings"

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

// Channel describes a configured notification backend for the channels API.
type Channel struct {
	ID          int    `json:"id"`
	Type        string `json:"type"`
	URL         string `json:"url,omitempty"`
	Subscribers int    `json:"subscribers,omitempty"`
}

// TestResult reports the outcome of a test notification for one channel.
type TestResult struct {
	ID    int    `json:"id"`
	Type  string `json:"type"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// MaskURL replaces the auth portion (between :// and @) of a Shoutrrr URL with ****.
// If there is no @ after the scheme, the URL is returned unchanged.
func MaskURL(raw string) string {
	if raw == "" {
		return ""
	}
	schemeEnd := strings.Index(raw, "://")
	if schemeEnd < 0 {
		return raw
	}
	rest := raw[schemeEnd+3:]
	atIdx := strings.Index(rest, "@")
	if atIdx < 0 {
		return raw
	}
	return raw[:schemeEnd+3] + "****" + rest[atIdx:]
}

// URLScheme extracts the scheme (service type) from a Shoutrrr URL.
func URLScheme(raw string) string {
	if idx := strings.Index(raw, "://"); idx >= 0 {
		return raw[:idx]
	}
	return raw
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

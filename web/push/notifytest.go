package push

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/containrrr/shoutrrr"
)

// NotifyTarget describes a configured Shoutrrr notification URL without
// exposing its secret contents — the raw URL holds tokens/passwords, so only
// redacted metadata is ever surfaced.
type NotifyTarget struct {
	Index       int    `json:"index"`
	Service     string `json:"service"`     // URL scheme, e.g. "discord"
	Fingerprint string `json:"fingerprint"` // short, non-reversible hash to tell duplicates apart
}

// NotifyTargets lists the configured Shoutrrr notification URLs as redacted
// metadata. The index aligns with SendTestNotify.
func (m *Manager) NotifyTargets() []NotifyTarget {
	targets := make([]NotifyTarget, 0, len(m.notifyURLs))
	for i, u := range m.notifyURLs {
		targets = append(targets, NotifyTarget{
			Index:       i,
			Service:     notifyScheme(u),
			Fingerprint: notifyFingerprint(u),
		})
	}
	return targets
}

// NotifyTargetCount returns the number of configured notification URLs.
func (m *Manager) NotifyTargetCount() int { return len(m.notifyURLs) }

// SendTestNotify sends message to the single configured notification URL at
// index. Only already-configured URLs can be targeted (by index) — never an
// arbitrary URL from the caller.
func (m *Manager) SendTestNotify(index int, message string) error {
	if index < 0 || index >= len(m.notifyURLs) {
		return fmt.Errorf("notify target index %d out of range (%d configured)", index, len(m.notifyURLs))
	}
	if err := shoutrrr.Send(m.notifyURLs[index], message); err != nil {
		return fmt.Errorf("send test notification: %w", err)
	}
	return nil
}

// notifyScheme returns the URL scheme (the part before "://"), or "unknown".
func notifyScheme(rawURL string) string {
	if i := strings.Index(rawURL, "://"); i > 0 {
		return rawURL[:i]
	}
	return "unknown"
}

// notifyFingerprint returns a short, non-reversible hash of the raw URL, so
// callers can distinguish two URLs of the same service without seeing either.
func notifyFingerprint(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:])[:8]
}

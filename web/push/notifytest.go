package push

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/containrrr/shoutrrr"
)

// NotifyTarget describes a configured Shoutrrr notification URL without
// exposing its secret contents — the raw URL holds tokens/passwords, so only
// redacted metadata is ever surfaced.
type NotifyTarget struct {
	Index       int      `json:"index"`
	Service     string   `json:"service"`     // URL scheme, e.g. "discord"
	Fingerprint string   `json:"fingerprint"` // short, non-reversible hash to tell duplicates apart
	Alerts      []string `json:"alerts"`      // the alerts the target receives, by name
}

// NotifyTargets lists the notification config's targets as redacted
// metadata. The index aligns with SendTestNotify.
func (m *Manager) NotifyTargets() []NotifyTarget {
	targets := m.currentTargets()
	out := make([]NotifyTarget, 0, len(targets))
	for i, t := range targets {
		alerts := make([]string, 0, len(t.route.Alerts))
		for a := range t.route.Alerts {
			alerts = append(alerts, string(a))
		}
		slices.Sort(alerts)
		out = append(out, NotifyTarget{Index: i, Service: notifyScheme(t.route.URL), Fingerprint: t.fingerprint, Alerts: alerts})
	}
	return out
}

// NotifyTargetCount returns the number of notification targets.
func (m *Manager) NotifyTargetCount() int { return len(m.currentTargets()) }

// SendTestNotify sends message to the notification target at index. Only
// configured targets can be sent to (by index), never an arbitrary URL from
// the caller.
func (m *Manager) SendTestNotify(index int, message string) error {
	targets := m.currentTargets()
	if index < 0 || index >= len(targets) {
		return fmt.Errorf("notify target index %d out of range (%d configured)", index, len(targets))
	}
	if err := shoutrrr.Send(targets[index].route.URL, message); err != nil {
		return fmt.Errorf("send test notification: %s", m.notifyErrorText(err, targetURLs(targets)))
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

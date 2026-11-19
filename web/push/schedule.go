package push

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gnoverse/gnockpit/blocks"
)

// Halt is a coordinated halt the chain schedules.
type Halt struct {
	Height     int64
	MinVersion string // the version to run from the halt, when the chain names one
}

// Schedule is what the lead-time alerts are checked against at a refresh.
type Schedule struct {
	Chain    string
	Head     int64
	Halt     *Halt                                    // nil: none scheduled
	TimeLeft func(height int64) (time.Duration, bool) // the block-time model's estimate; false when unknown
}

// EvaluateSchedule sends the alerts s calls for: a halt height new to a
// device or target, once each, and its approach once within each one's lead
// (what was sent is kept: a restart or a re-saved device sends nothing
// twice); and each device's reminder whose lead is met, deleted once sent.
func (m *Manager) EvaluateSchedule(s Schedule) {
	if s.Halt != nil {
		m.evaluateHalt(s)
	}
	m.evaluateReminders(s)
}

func (m *Manager) evaluateHalt(s Schedule) {
	key := fmt.Sprintf("%s:%d", s.Chain, s.Halt.Height)
	m.sendOnce(Alert{
		Type: AlertHaltScheduled, Firing: true, Notice: true,
		Title: fmt.Sprintf("Coordinated halt scheduled at #%d", s.Halt.Height), Body: haltBody(s, ""),
	},
		key, func(Lead) bool { return true })
	// The block-time model's view drops a halt once reached: the block before
	// it is the last refresh that sees it, and meets any lead.
	reached := s.Head >= s.Halt.Height-1
	m.sendOnce(Alert{
		Type: AlertHaltApproaching, Firing: true, Notice: true,
		Title: approachingTitle(s.Halt.Height, s.Head), Body: haltBody(s, "left"),
	},
		key, func(l Lead) bool { return reached || l.Met(s.Halt.Height, s.Head, s.TimeLeft) })
}

// sendOnce sends a to each device and target subscribed to it whose lead due
// accepts, once per key.
func (m *Manager) sendOnce(a Alert, key string, due func(Lead) bool) {
	subs, err := m.db.SubscribersForAlert(a.Type, "")
	if err != nil {
		log.Printf("push: subscribers of %s: %v", a.Type, err)
	}
	for _, sb := range subs {
		if due(sb.Lead) && m.markSent("device:"+sb.Sub.ID, a.Type, key) {
			m.sendPush(sb.Sub, a)
		}
	}
	targets := m.currentTargets()
	for _, t := range targets {
		if t.route.Wants(a) && due(t.route.Alerts[a.Type].Lead) && m.markSent("target:"+t.fingerprint, a.Type, key) {
			m.sendTarget(t, a, targets)
		}
	}
}

func (m *Manager) markSent(owner string, t AlertType, key string) bool {
	fresh, err := m.db.MarkSent(owner, t, key)
	if err != nil {
		log.Printf("push: %v", err)
	}
	return fresh
}

// approachingTitle says how far the halt at height is from head.
func approachingTitle(height, head int64) string {
	return fmt.Sprintf("Coordinated halt at #%d in %s", height, blocksText(height-head))
}

// blocksText counts n blocks: "1 block", "98 blocks".
func blocksText(n int64) string {
	if n == 1 {
		return "1 block"
	}
	return fmt.Sprintf("%d blocks", n)
}

// haltBody tells the halt's minimum version and, when the model gives it,
// the time to it ("in about 5 h", or with suffix "left", "about 5 h left").
func haltBody(s Schedule, suffix string) string {
	var parts []string
	if left, ok := s.TimeLeft(s.Halt.Height); ok {
		if suffix == "" {
			parts = append(parts, fmt.Sprintf("Expected in about %s.", about(left)))
		} else {
			parts = append(parts, fmt.Sprintf("About %s %s.", about(left), suffix))
		}
	}
	if s.Halt.MinVersion != "" {
		parts = append(parts, fmt.Sprintf("Minimum version %s.", s.Halt.MinVersion))
	}
	return strings.Join(parts, " ")
}

// NotifyValsetChange sends the alert of a validator set change to its
// subscribers, naming addresses by name.
func (m *Manager) NotifyValsetChange(c blocks.ValsetChange, name func(address string) string) {
	m.NotifyAlert(valsetChangeAlert(c, name, m.now()))
}

// valsetChangeAlert says what changed in the set, one line each, and when
// the change is more than 5 minutes old (caught up after downtime), when.
func valsetChangeAlert(c blocks.ValsetChange, name func(string) string, now time.Time) Alert {
	label := func(addr string) string {
		if n := name(addr); n != addr {
			return n
		}
		return short(addr)
	}
	var lines []string
	for _, m := range c.Added {
		lines = append(lines, fmt.Sprintf("+ %s joined (power %d)", label(m.Address), m.Power))
	}
	for _, m := range c.Removed {
		lines = append(lines, fmt.Sprintf("− %s left (power %d)", label(m.Address), m.Power))
	}
	for _, p := range c.Repowered {
		lines = append(lines, fmt.Sprintf("↕ %s power %d → %d", label(p.Address), p.Before, p.After))
	}
	for _, r := range c.Rotated {
		lines = append(lines, fmt.Sprintf("⇄ %s rotated its key %s → %s", label(r.NewAddress), short(r.OldAddress), short(r.NewAddress)))
	}
	lines = append(lines, fmt.Sprintf("Validators: %d → %d", c.SizeBefore, c.SizeAfter))
	if age := now.Sub(c.Time); age > 5*time.Minute {
		lines = append(lines, fmt.Sprintf("At %s (%s ago)", c.Time.UTC().Format("2006-01-02 15:04 UTC"), about(age)))
	}
	return Alert{Type: AlertValsetChanged, Firing: true, Notice: true, Title: fmt.Sprintf("Validator set changed at #%d", c.Height), Body: strings.Join(lines, "\n")}
}

// short shortens an address to its first 8 and last 4 characters.
func short(addr string) string {
	if len(addr) <= 16 {
		return addr
	}
	return addr[:8] + "…" + addr[len(addr)-4:]
}

// about rounds d for reading: minutes under an hour, hours under two days,
// days beyond.
func about(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d min", max(1, d.Round(time.Minute)/time.Minute))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h", d.Round(time.Hour)/time.Hour)
	}
	return fmt.Sprintf("%d days", d.Round(24*time.Hour)/(24*time.Hour))
}

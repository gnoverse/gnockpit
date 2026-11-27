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
// accepts, once per key. A device has one lead and a target a series: a
// target's route with leads records every lead due under key and the lead,
// and gets one message when any of them was new.
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
		if !t.route.Wants(a) {
			continue
		}
		owner := "target:" + t.fingerprint
		leads := t.route.Alerts[a.Type].Leads
		// An alert without leads (halt_scheduled) is sent once per key; a parsed
		// halt_approaching always has at least one lead.
		if len(leads) == 0 {
			if m.markSent(owner, a.Type, key) {
				m.sendTarget(t, a, targets)
			}
			continue
		}
		fresh := false
		for _, l := range leads {
			if due(l) && m.markSent(owner, a.Type, key+":"+l.String()) {
				fresh = true
			}
		}
		if fresh {
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
// the time to it ("Expected in about 5 h.", or with suffix "left", "About
// 5 h left."), the time and the version bold; nil when it knows neither.
func haltBody(s Schedule, suffix string) Body {
	var b Body
	if left, ok := s.TimeLeft(s.Halt.Height); ok {
		if suffix == "" {
			b = append(b, plain("Expected in about "), bold(about(left)), plain("."))
		} else {
			b = append(b, plain("About "), bold(about(left)), plain(" "+suffix+"."))
		}
	}
	if s.Halt.MinVersion != "" {
		lead := "Minimum version "
		if len(b) > 0 {
			// The space between the sentences stays in this plain span, so no
			// bold span carries it.
			lead = " " + lead
		}
		b = append(b, plain(lead), bold(s.Halt.MinVersion), plain("."))
	}
	return b
}

// NotifyValsetChange sends the alert of a validator set change to its
// subscribers, naming addresses by name.
func (m *Manager) NotifyValsetChange(c blocks.ValsetChange, name func(address string) string) {
	m.NotifyAlert(valsetChangeAlert(c, name, m.now()))
}

// valsetChangeAlert tells a set change in titled sections (who joined, who
// left, the size, voting power, rotated keys), each only when it holds an
// entry, and when it happened if it was caught up after downtime. A title,
// count included, is one bold span. A validator reads as its name, cut to
// alertNameLimit, else its short address.
func valsetChangeAlert(c blocks.ValsetChange, name func(string) string, now time.Time) Alert {
	label := func(addr string) string {
		if n := name(addr); n != addr {
			return cutName(n, alertNameLimit)
		}
		return short(addr)
	}
	var body Body
	paragraph := func(spans ...Span) {
		if body != nil {
			body = append(body, plain("\n\n"))
		}
		body = append(body, spans...)
	}
	section := func(title string, lines []string) {
		if len(lines) > 0 {
			paragraph(bold(title), plain("\n"+strings.Join(lines, "\n")))
		}
	}
	var joined, left, repowered, rotated []string
	for _, m := range c.Added {
		joined = append(joined, fmt.Sprintf("%s (power %d)", label(m.Address), m.Power))
	}
	for _, m := range c.Removed {
		left = append(left, fmt.Sprintf("%s (power %d)", label(m.Address), m.Power))
	}
	for _, p := range c.Repowered {
		repowered = append(repowered, fmt.Sprintf("%s %d → %d", label(p.Address), p.Before, p.After))
	}
	for _, r := range c.Rotated {
		rotated = append(rotated, fmt.Sprintf("%s %s → %s", label(r.NewAddress), short(r.OldAddress), short(r.NewAddress)))
	}
	section(fmt.Sprintf("Joined (%d)", len(joined)), joined)
	section(fmt.Sprintf("Left (%d)", len(left)), left)
	if d := c.SizeAfter - c.SizeBefore; d != 0 {
		sign, mag := "+", d
		if d < 0 {
			sign, mag = "−", -d
		}
		section(fmt.Sprintf("Validator set size (%s%d)", sign, mag), []string{fmt.Sprintf("%d → %d", c.SizeBefore, c.SizeAfter)})
	}
	section(fmt.Sprintf("Voting power changed (%d)", len(repowered)), repowered)
	section(fmt.Sprintf("Rotated signing key (%d)", len(rotated)), rotated)
	if age := now.Sub(c.Time); age > 5*time.Minute {
		paragraph(plain(fmt.Sprintf("At %s (%s ago)", c.Time.UTC().Format("2006-01-02 15:04 UTC"), about(age))))
	}
	return Alert{Type: AlertValsetChanged, Firing: true, Notice: true, Title: fmt.Sprintf("Validator set changed at #%d", c.Height), Body: body}
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

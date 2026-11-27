package push

import (
	"fmt"
	"log"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/containrrr/shoutrrr"
	"github.com/containrrr/shoutrrr/pkg/router"

	"github.com/gnoverse/gnockpit/ranking"
)

// notifyTarget is a notification config target ready to send.
type notifyTarget struct {
	route       NotifyRoute
	sender      *router.ServiceRouter
	fingerprint string       // of the URL, as NotifyTargets shows it
	discord     *discordHook // nil unless the target is a Discord webhook
}

// SetNotifyConfig sends each of cfg's targets the alerts it lists, in place of
// the config in force. A URL Shoutrrr cannot use is an error, and the config
// in force stays.
func (m *Manager) SetNotifyConfig(cfg NotifyConfig) error {
	targets := make([]notifyTarget, 0, len(cfg.Targets))
	for i, r := range cfg.Targets {
		sendURL, hook, err := senderURL(r.URL)
		var sender *router.ServiceRouter
		if err == nil {
			sender, err = shoutrrr.CreateSender(sendURL)
		}
		if err != nil {
			urls := make([]string, len(cfg.Targets))
			for j, t := range cfg.Targets {
				urls[j] = t.URL
			}
			return fmt.Errorf("target %d: %s", i+1, m.notifyErrorText(err, urls))
		}
		if m.sendTimeout > 0 {
			sender.Timeout = m.sendTimeout
		}
		targets = append(targets, notifyTarget{route: r, sender: sender, fingerprint: notifyFingerprint(r.URL), discord: hook})
	}
	m.notifyMu.Lock()
	m.targets = targets
	m.notifyMu.Unlock()
	log.Printf("notify: %d notification target(s)", len(targets))
	return nil
}

// LoadNotifyConfig reads the notification config file at path and sends by
// it; ReloadNotifyConfigIfChanged rereads it once it changes.
func (m *Manager) LoadNotifyConfig(path string) error {
	cfg, mod, err := readNotifyConfig(path)
	if err == nil {
		err = m.SetNotifyConfig(cfg)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	m.notifyPath, m.notifyModTime = path, mod
	return nil
}

// ReloadNotifyConfigIfChanged rereads the notification config file when its
// modification time differs from the config in force's. A file that cannot
// be read, parsed or sent by is logged once, and the config in force stays;
// it is read again at each call until it loads, so a fix written within the
// same clock tick is not missed.
func (m *Manager) ReloadNotifyConfigIfChanged() {
	if m.notifyPath == "" {
		return
	}
	info, err := os.Stat(m.notifyPath)
	if err != nil {
		m.reloadFailed(err)
		return
	}
	if info.ModTime().Equal(m.notifyModTime) && m.notifyErr == "" {
		return
	}
	cfg, mod, err := readNotifyConfig(m.notifyPath)
	if err == nil {
		err = m.SetNotifyConfig(cfg)
	}
	if err != nil {
		m.reloadFailed(err)
		return
	}
	m.notifyModTime, m.notifyErr = mod, ""
}

func (m *Manager) reloadFailed(err error) {
	if msg := err.Error(); msg != m.notifyErr {
		m.notifyErr = msg
		log.Printf("notify config %s: %s; keeping the previous one", m.notifyPath, msg)
	}
}

func readNotifyConfig(path string) (NotifyConfig, time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return NotifyConfig{}, time.Time{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return NotifyConfig{}, time.Time{}, err
	}
	cfg, err := ParseNotifyConfig(data)
	return cfg, info.ModTime(), err
}

// notifyTargets sends a to the targets that want it.
func (m *Manager) notifyTargets(a Alert) {
	targets := m.currentTargets()
	if len(targets) == 0 {
		return
	}
	for _, t := range targets {
		if t.route.Wants(a) {
			m.sendTarget(t, a, targets)
		}
	}
}

// sendTarget sends a to t, one of targets, whose URLs a failed send's log
// is stripped of.
func (m *Manager) sendTarget(t notifyTarget, a Alert, targets []notifyTarget) {
	if t.discord != nil {
		m.sendDiscord(t, discordAlert(m.chainOf(t), m.publicURL, a), targets)
		return
	}
	m.sendText(t, FormatAlert(m.chainOf(t), m.publicURL, a), targets)
}

// chainOf is the chain name t's messages carry: none when t hides it.
func (m *Manager) chainOf(t notifyTarget) string {
	if t.route.HideChain {
		return ""
	}
	return m.ChainName()
}

// sendText sends one message to t, one of targets, whose URLs a failed send's
// log is stripped of.
func (m *Manager) sendText(t notifyTarget, text string, targets []notifyTarget) {
	for _, err := range t.sender.Send(text, nil) {
		if err != nil {
			log.Printf("notify: %s", m.notifyErrorText(err, targetURLs(targets)))
		}
	}
}

// maxMessageChars is the most characters, Unicode code points, of one
// message sent to a target: 2,000 of them stay within every service's limit,
// Telegram's 4,096 UTF-16 units included.
const maxMessageChars = 2000

// rankingLink is the page a tap on the ranking notification opens.
const rankingLink = "/?view=ranking"

// rankingExplainLink is the Ranking view with its "How is this ranked?"
// dialog open, which a Discord ranking's formula links.
const rankingExplainLink = "/?view=ranking&explain"

// rankingAlert is the alert a ranking digest is sent as, its body the
// digest's text, each name cut to alertNameLimit as every alert quotes one.
// d's rows stay as they are.
func rankingAlert(d ranking.Digest) Alert {
	d.Rows = slices.Clone(d.Rows)
	for i := range d.Rows {
		d.Rows[i].Name = cutName(d.Rows[i].Name, alertNameLimit)
	}
	return Alert{Type: AlertRanking, Firing: true, Notice: true, Title: d.Title, Body: Body{plain(d.Body())}, Link: rankingLink}
}

// NotifyRanking sends the day's ranking digest to each target that lists the
// ranking, once per key: the chain and day it ranks. A Discord target gets it
// as gold embeds, a coloured table and fields (discordRanking). Any other
// target gets it as text, over maxMessageChars as several messages, split at
// line breaks, in order. It sends synchronously, so it holds the publisher's
// pass for up to the targets times the parts times the send timeout, and it
// never calls back into the publisher.
func (m *Manager) NotifyRanking(key string, d ranking.Digest) {
	a := rankingAlert(d)
	targets := m.currentTargets()
	for _, t := range targets {
		if !t.route.Wants(a) || !m.markSent("target:"+t.fingerprint, a.Type, key) {
			continue
		}
		if t.discord != nil {
			m.sendDiscord(t, discordRanking(m.chainOf(t), m.publicURL, d), targets)
			continue
		}
		for _, part := range splitMessage(FormatAlert(m.chainOf(t), m.publicURL, a), maxMessageChars) {
			m.sendText(t, part, targets)
		}
	}
}

// splitMessage cuts text into parts of at most limit characters (Unicode
// code points), at line breaks, so that joining the parts with line breaks
// gives text back, unless a line longer than limit was cut: it is cut at
// limit, and the join adds a line break at each cut.
func splitMessage(text string, limit int) []string {
	var parts []string
	var part []rune
	open := false // part holds at least one line, possibly an empty one
	for _, line := range strings.Split(text, "\n") {
		runes := []rune(line)
		if open && len(part)+1+len(runes) <= limit {
			part = append(append(part, '\n'), runes...)
			continue
		}
		if open {
			parts = append(parts, string(part))
		}
		for len(runes) > limit {
			parts = append(parts, string(runes[:limit]))
			runes = runes[limit:]
		}
		part, open = runes, true
	}
	return append(parts, string(part))
}

func (m *Manager) currentTargets() []notifyTarget {
	m.notifyMu.RLock()
	defer m.notifyMu.RUnlock()
	return m.targets
}

func targetURLs(targets []notifyTarget) []string {
	urls := make([]string, len(targets))
	for i, t := range targets {
		urls[i] = t.route.URL
	}
	return urls
}

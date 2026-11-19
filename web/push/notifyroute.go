package push

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/containrrr/shoutrrr"
	"github.com/containrrr/shoutrrr/pkg/router"
)

// notifyTarget is a notification config target ready to send.
type notifyTarget struct {
	route       NotifyRoute
	sender      *router.ServiceRouter
	fingerprint string // of the URL, as NotifyTargets shows it
}

// SetNotifyConfig sends each of cfg's targets the alerts it lists, in place of
// the config in force. A URL Shoutrrr cannot use is an error, and the config
// in force stays.
func (m *Manager) SetNotifyConfig(cfg NotifyConfig) error {
	targets := make([]notifyTarget, 0, len(cfg.Targets))
	for i, r := range cfg.Targets {
		sender, err := shoutrrr.CreateSender(r.URL)
		if err != nil {
			urls := make([]string, len(cfg.Targets))
			for j, t := range cfg.Targets {
				urls[j] = t.URL
			}
			return fmt.Errorf("target %d: %s", i+1, m.notifyErrorText(err, urls))
		}
		targets = append(targets, notifyTarget{route: r, sender: sender, fingerprint: notifyFingerprint(r.URL)})
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
// modification time changed. A file that cannot be read, parsed or sent by
// is logged once, and the config in force stays.
func (m *Manager) ReloadNotifyConfigIfChanged() {
	if m.notifyPath == "" {
		return
	}
	info, err := os.Stat(m.notifyPath)
	if err != nil {
		m.reloadFailed(err)
		return
	}
	if info.ModTime().Equal(m.notifyModTime) {
		return
	}
	m.notifyModTime = info.ModTime()
	cfg, _, err := readNotifyConfig(m.notifyPath)
	if err == nil {
		err = m.SetNotifyConfig(cfg)
	}
	if err != nil {
		m.reloadFailed(err)
		return
	}
	m.notifyErr = ""
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
	for _, err := range t.sender.Send(FormatAlert(m.ChainName(), m.publicURL, a), nil) {
		if err != nil {
			log.Printf("notify: %s", m.notifyErrorText(err, targetURLs(targets)))
		}
	}
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

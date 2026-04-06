package push

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/containrrr/shoutrrr"
	"github.com/containrrr/shoutrrr/pkg/router"
	"github.com/gnoverse/gnockpit/node"
)

// vapidSubject is the VAPID JWT "sub" claim identifying this server.
// webpush-go prepends "mailto:" unless the value starts with "https:", so we
// use the project URL directly to avoid needing a real email address. Apple
// APNs rejects "mailto:" with non-routable domains like localhost (403 BadJwtToken).
const vapidSubject = "https://github.com/gnoverse/gnockpit"

// Manager coordinates VAPID key lifecycle, alert detection, and push delivery.
type Manager struct {
	db              *DB
	detector        *AlertDetector
	vapidPublicKey  string
	vapidPrivateKey string
	chainStuckSecs  int
	missedBlocksPct int
	chainName       string
	notifier        *router.ServiceRouter
	notifyURLs      []string
}

// NewManager creates a Manager, loading or auto-generating VAPID keys.
func NewManager(db *DB, chainStuckSecs, missedBlocksPct int) (*Manager, error) {
	pub, priv, err := db.LoadVAPIDKeys()
	if err != nil {
		return nil, fmt.Errorf("load VAPID keys: %w", err)
	}
	if pub == "" {
		priv, pub, err = webpush.GenerateVAPIDKeys()
		if err != nil {
			return nil, fmt.Errorf("generate VAPID keys: %w", err)
		}
		if err := db.SaveVAPIDKeys(pub, priv); err != nil {
			return nil, fmt.Errorf("save VAPID keys: %w", err)
		}
	}
	return &Manager{
		db:              db,
		detector:        NewAlertDetector(chainStuckSecs, missedBlocksPct),
		vapidPublicKey:  pub,
		vapidPrivateKey: priv,
		chainStuckSecs:  chainStuckSecs,
		missedBlocksPct: missedBlocksPct,
	}, nil
}

// ChainStuckSecs returns the chain-stuck threshold in seconds.
func (m *Manager) ChainStuckSecs() int { return m.chainStuckSecs }

// MissedBlocksPct returns the validator missing-blocks threshold percentage.
func (m *Manager) MissedBlocksPct() int { return m.missedBlocksPct }

// ChainName returns the last observed chain network ID.
func (m *Manager) ChainName() string { return m.chainName }

// VAPIDPublicKey returns the VAPID public key for use in browser push subscriptions.
func (m *Manager) VAPIDPublicKey() string { return m.vapidPublicKey }

// DB returns the underlying database (used by HTTP handlers).
func (m *Manager) DB() *DB { return m.db }

// SetNotifyURLs configures external notification delivery via Shoutrrr service URLs.
// Pass nil or empty to disable. Call before the publish loop starts.
func (m *Manager) SetNotifyURLs(urls []string) error {
	r, err := NewNotifier(urls)
	if err != nil {
		return err
	}
	m.notifier = r
	m.notifyURLs = urls
	return nil
}

// NotifyChannels returns the list of configured notification channels.
// Shoutrrr channels are listed first by index, push channel is listed last.
func (m *Manager) NotifyChannels() []Channel {
	var channels []Channel
	for i, u := range m.notifyURLs {
		channels = append(channels, Channel{
			ID:   i,
			Type: URLScheme(u),
			URL:  MaskURL(u),
		})
	}
	subs, _ := m.db.AllSubscriptions()
	channels = append(channels, Channel{
		ID:          len(m.notifyURLs),
		Type:        "push",
		Subscribers: len(subs),
	})
	return channels
}

// SendTestNotify sends a test message to the specified channel IDs.
// If channelIDs is nil or empty, sends to all channels.
// Push channel sends to all subscribers. Returns one result per channel attempted.
func (m *Manager) SendTestNotify(channelIDs []int, message string) []TestResult {
	if message == "" {
		message = FormatAlert(m.chainName, Alert{
			Firing: true,
			Title:  "Test alert",
			Body:   "This is a test notification from gnockpit.",
		})
	}

	channels := m.NotifyChannels()

	// Resolve which channels to test
	var targets []Channel
	if len(channelIDs) == 0 {
		targets = channels
	} else {
		for _, id := range channelIDs {
			if id >= 0 && id < len(channels) {
				targets = append(targets, channels[id])
			}
		}
	}

	var results []TestResult
	for _, ch := range targets {
		if ch.Type == "push" {
			results = append(results, m.testPushChannel(ch.ID, message)...)
			continue
		}
		// Shoutrrr channel — send individually
		if ch.ID < 0 || ch.ID >= len(m.notifyURLs) {
			continue
		}
		err := shoutrrr.Send(m.notifyURLs[ch.ID], message)
		r := TestResult{ID: ch.ID, Type: ch.Type, OK: err == nil}
		if err != nil {
			r.Error = err.Error()
		}
		results = append(results, r)
	}
	return results
}

func (m *Manager) testPushChannel(id int, message string) []TestResult {
	subs, err := m.db.AllSubscriptions()
	if err != nil || len(subs) == 0 {
		return nil
	}
	a := Alert{Firing: true, Title: "gnockpit test", Body: message}
	for _, sub := range subs {
		m.sendPush(sub, a)
	}
	return []TestResult{{ID: id, Type: "push", OK: true}}
}

// EvaluateAndNotify detects alert transitions from snap and delivers push notifications.
func (m *Manager) EvaluateAndNotify(snap *node.Snapshot) {
	if snap.Status != nil && snap.Status.NodeInfo.Network != "" {
		m.chainName = snap.Status.NodeInfo.Network
	}
	for _, a := range m.detector.Detect(snap) {
		m.NotifyAlert(a)
	}
}

// NotifyAlert sends push notifications to all subscribers opted in for the given alert.
func (m *Manager) NotifyAlert(a Alert) {
	subscribers, err := m.db.SubscribersForAlert(a.Type, a.EntityID)
	if err != nil {
		log.Printf("push: query subscribers for %s/%s: %v", a.Type, a.EntityID, err)
		return
	}
	for _, sb := range subscribers {
		if !a.Firing && !sb.RecoveryNotif {
			continue
		}
		m.sendPush(sb.Sub, a)
	}

	if m.notifier != nil {
		msg := FormatAlert(m.chainName, a)
		if errs := m.notifier.Send(msg, nil); len(errs) > 0 {
			for _, err := range errs {
				if err != nil {
					log.Printf("notify: %v", err)
				}
			}
		}
	}
}

// SendTest sends a test notification to a specific subscription.
func (m *Manager) SendTest(subID string) error {
	sub, err := m.db.GetSubscription(subID)
	if err != nil {
		return fmt.Errorf("get subscription: %w", err)
	}
	m.sendPush(sub, Alert{
		Firing: true,
		Title:  "gnockpit test",
		Body:   "Push notifications are working.",
	})
	return nil
}

type pushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

func (m *Manager) sendPush(sub Subscription, a Alert) {
	title := a.Title
	if m.chainName != "" {
		title = m.chainName + " - " + title
	}
	payload, err := json.Marshal(pushPayload{Title: title, Body: a.Body})
	if err != nil {
		log.Printf("push: marshal payload: %v", err)
		return
	}
	resp, err := webpush.SendNotification(payload, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			P256dh: sub.P256dh,
			Auth:   sub.Auth,
		},
	}, &webpush.Options{
		VAPIDPublicKey:  m.vapidPublicKey,
		VAPIDPrivateKey: m.vapidPrivateKey,
		Subscriber:      vapidSubject,
		TTL:             30,
	})
	if err != nil {
		log.Printf("push: send to %s: %v", sub.ID, err)
		return
	}
	defer resp.Body.Close()

	log.Printf("push: send to %s: status %d", sub.ID, resp.StatusCode)
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("push: send to %s: response body: %s", sub.ID, body)
	}

	// 410 Gone: FCM/standard expired subscription.
	// 400 Bad Request: WNS (Edge on Windows) uses this for expired channels.
	if resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusBadRequest {
		log.Printf("push: subscription %s expired (%d), removing", sub.ID, resp.StatusCode)
		if err := m.db.DeleteSubscription(sub.ID); err != nil {
			log.Printf("push: delete expired subscription: %v", err)
		}
	}
}

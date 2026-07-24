package push

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"
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
	maxMissedInARow int
	chainName       string
	notifier        *router.ServiceRouter
	notifyURLs      []string
	publicURL       string
}

// NewManager creates a Manager, loading or auto-generating VAPID keys.
func NewManager(db *DB, chainStuckSecs, maxMissedInARow int) (*Manager, error) {
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
		detector:        NewAlertDetector(chainStuckSecs, maxMissedInARow),
		vapidPublicKey:  pub,
		vapidPrivateKey: priv,
		chainStuckSecs:  chainStuckSecs,
		maxMissedInARow: maxMissedInARow,
	}, nil
}

// ChainStuckSecs returns the chain-stuck threshold in seconds.
func (m *Manager) ChainStuckSecs() int { return m.chainStuckSecs }

// MaxMissedInARow returns the consecutive missed/signed blocks that flip a
// validator down/up (fires and recovers the missing-blocks alert).
func (m *Manager) MaxMissedInARow() int { return m.maxMissedInARow }

// MissingBlocksFiring returns the set of validator addresses currently flagged
// down (missing-blocks alert firing), for the active-validator count.
func (m *Manager) MissingBlocksFiring() map[string]bool { return m.detector.MissingBlocksFiring() }

// ChainName returns the last observed chain network ID.
func (m *Manager) ChainName() string { return m.chainName }

// VAPIDPublicKey returns the VAPID public key for use in browser push subscriptions.
func (m *Manager) VAPIDPublicKey() string { return m.vapidPublicKey }

// DB returns the underlying database (used by HTTP handlers).
func (m *Manager) DB() *DB { return m.db }

// SetPublicURL sets the public-facing URL appended to Shoutrrr alert messages.
func (m *Manager) SetPublicURL(url string) { m.publicURL = url }

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
		msg := FormatAlert(m.chainName, m.publicURL, a)
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

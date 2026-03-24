package push

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/gnoverse/gnockpit/node"
)

const vapidSubject = "mailto:gnockpit@localhost"

// Manager coordinates VAPID key lifecycle, alert detection, and push delivery.
type Manager struct {
	db              *DB
	detector        *AlertDetector
	vapidPublicKey  string
	vapidPrivateKey string
}

// NewManager creates a Manager, loading or auto-generating VAPID keys.
func NewManager(db *DB, chainStuckSecs int) (*Manager, error) {
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
		detector:        NewAlertDetector(chainStuckSecs),
		vapidPublicKey:  pub,
		vapidPrivateKey: priv,
	}, nil
}

// VAPIDPublicKey returns the VAPID public key for use in browser push subscriptions.
func (m *Manager) VAPIDPublicKey() string { return m.vapidPublicKey }

// DB returns the underlying database (used by HTTP handlers).
func (m *Manager) DB() *DB { return m.db }

// EvaluateAndNotify detects alert transitions from snap and delivers push notifications.
func (m *Manager) EvaluateAndNotify(snap *node.Snapshot) {
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
}

type pushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

func (m *Manager) sendPush(sub Subscription, a Alert) {
	payload, err := json.Marshal(pushPayload{Title: a.Title, Body: a.Body})
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

	if resp.StatusCode == http.StatusGone {
		log.Printf("push: subscription %s expired (410), removing", sub.ID)
		if err := m.db.DeleteSubscription(sub.ID); err != nil {
			log.Printf("push: delete expired subscription: %v", err)
		}
	}
}

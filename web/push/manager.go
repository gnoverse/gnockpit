package push

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/gnoverse/gnockpit/node"
)

// DefaultSendTimeout is how long a push or webhook send may take unless
// SetSendTimeout says otherwise: Shoutrrr's own bound.
const DefaultSendTimeout = 10 * time.Second

// vapidSubject is the VAPID JWT "sub" claim identifying this server.
// webpush-go prepends "mailto:" unless the value starts with "https:", so we
// use the project URL directly to avoid needing a real email address. Apple
// APNs rejects "mailto:" with non-routable domains like localhost (403 BadJwtToken).
const vapidSubject = "https://github.com/gnoverse/gnockpit"

// maxRefusalBody is how much of a push service's refusal an error carries.
const maxRefusalBody = 1 << 10

// Manager coordinates VAPID key lifecycle, alert detection, and push delivery.
type Manager struct {
	db              *DB
	detector        *AlertDetector
	vapidPublicKey  string
	vapidPrivateKey string
	chainStuckSecs  int
	maxMissedInARow int
	chainName       atomic.Pointer[string] // written by the publish loop, read by every sender
	publicURL       string
	notifyMu        sync.RWMutex
	targets         []notifyTarget // the notification config's targets; guarded by notifyMu
	notifyPath      string         // the notification config file; "" when none
	notifyModTime   time.Time      // its modification time when last read
	notifyErr       string         // the last reload error logged
	now             func() time.Time
	sendTimeout     time.Duration // how long a push or webhook send may take
	redactSecrets   bool
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
		now:             time.Now,
		sendTimeout:     DefaultSendTimeout,
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
func (m *Manager) ChainName() string {
	if n := m.chainName.Load(); n != nil {
		return *n
	}
	return ""
}

// VAPIDPublicKey returns the VAPID public key for use in browser push subscriptions.
func (m *Manager) VAPIDPublicKey() string { return m.vapidPublicKey }

// DB returns the underlying database (used by HTTP handlers).
func (m *Manager) DB() *DB { return m.db }

// SetPublicURL sets the public-facing URL appended to Shoutrrr alert messages.
func (m *Manager) SetPublicURL(url string) { m.publicURL = url }

// SetSendTimeout bounds how long a push or webhook send may take, so an
// endpoint that never answers holds no caller. Call before sending.
func (m *Manager) SetSendTimeout(d time.Duration) { m.sendTimeout = d }

// SetRedactSecrets sets whether the Shoutrrr errors the Manager logs or returns
// are stripped of notification URL secrets (see redactNotifySecrets); when off,
// they are kept verbatim. Call before LoadNotifyConfig, whose error it also
// covers.
func (m *Manager) SetRedactSecrets(on bool) { m.redactSecrets = on }

// notifyErrorText returns the text of err, a Shoutrrr error, stripped of the
// secrets of urls when secret redaction is on.
func (m *Manager) notifyErrorText(err error, urls []string) string {
	if !m.redactSecrets {
		return err.Error()
	}
	return redactNotifySecrets(err.Error(), urls)
}

// OverrideLastHeightChange is for testing only: it moves the chain-stuck
// detector's clock of the last head change.
func (m *Manager) OverrideLastHeightChange(t time.Time) { m.detector.OverrideLastHeightChange(t) }

// EvaluateAndNotify detects alert transitions from snap and delivers push notifications.
func (m *Manager) EvaluateAndNotify(snap *node.Snapshot, halt int64) {
	if snap.Status != nil && snap.Status.NodeInfo.Network != "" {
		name := snap.Status.NodeInfo.Network
		m.chainName.Store(&name)
	}
	for _, a := range m.detector.Detect(snap, halt) {
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

	m.notifyTargets(a)
}

var (
	// ErrSubscriptionNotFound reports a device the server does not know.
	ErrSubscriptionNotFound = errors.New("subscription not found")
	// ErrDelivery reports a push its push service did not take.
	ErrDelivery = errors.New("push delivery failed")
)

// SendTest sends a test notification to device subID. It returns
// ErrSubscriptionNotFound for a device the server does not know, and an
// ErrDelivery error when the device's push service does not take the push.
func (m *Manager) SendTest(subID string) error {
	sub, err := m.db.GetSubscription(subID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSubscriptionNotFound
	}
	if err != nil {
		return fmt.Errorf("get subscription: %w", err)
	}
	return m.deliver(sub, Alert{
		Firing: true,
		Title:  "gnockpit test",
		Body:   Body{plain("Push notifications are working.")},
	})
}

type pushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url,omitempty"` // the page a tap opens (the service worker's notificationclick)
}

// payloadOf is what a push of a says, its title prefixed with the chain's
// name when known, its body plain.
func payloadOf(chain string, a Alert) pushPayload {
	title := a.Title
	if chain != "" {
		title = chain + " - " + title
	}
	return pushPayload{Title: title, Body: a.Body.String(), URL: a.Link}
}

// sendPush delivers a to sub, logging a failure: an alert goes on to its
// other devices.
func (m *Manager) sendPush(sub Subscription, a Alert) {
	if err := m.deliver(sub, a); err != nil {
		log.Printf("push: send to %s: %v", sub.ID, err)
	}
}

// deliver sends a to sub through its push service. A push the push service
// does not take is an ErrDelivery error; a subscription it reports expired
// is removed.
func (m *Manager) deliver(sub Subscription, a Alert) error {
	payload, err := json.Marshal(payloadOf(m.ChainName(), a))
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	resp, err := webpush.SendNotification(payload, &webpush.Subscription{
		Endpoint: sub.Endpoint,
		Keys: webpush.Keys{
			P256dh: sub.P256dh,
			Auth:   sub.Auth,
		},
	}, &webpush.Options{
		HTTPClient:      &http.Client{Timeout: m.sendTimeout},
		VAPIDPublicKey:  m.vapidPublicKey,
		VAPIDPrivateKey: m.vapidPrivateKey,
		Subscriber:      vapidSubject,
		TTL:             30,
	})
	// A request that reached no push service is a delivery failure, and so
	// is a malformed stored endpoint, which fails as a *url.Error too; the
	// other errors come before the request (a malformed key, say).
	var unreached *url.Error
	if errors.As(err, &unreached) {
		return fmt.Errorf("%w: %w", ErrDelivery, err)
	}
	if err != nil {
		return fmt.Errorf("prepare the push: %w", err)
	}
	defer resp.Body.Close()

	// 410 Gone: FCM/standard expired subscription.
	// 400 Bad Request: WNS (Edge on Windows) uses this for expired channels.
	if resp.StatusCode == http.StatusGone || resp.StatusCode == http.StatusBadRequest {
		log.Printf("push: subscription %s expired (%d), removing", sub.ID, resp.StatusCode)
		if err := m.db.DeleteSubscription(sub.ID); err != nil {
			log.Printf("push: delete expired subscription: %v", err)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The answer is the remote endpoint's text: bounded, and quoted so
		// that it adds no line to the log.
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxRefusalBody))
		if err != nil {
			return fmt.Errorf("%w: status %d, read body: %w", ErrDelivery, resp.StatusCode, err)
		}
		return fmt.Errorf("%w: status %d: %q", ErrDelivery, resp.StatusCode, bytes.TrimSpace(body))
	}
	log.Printf("push: send to %s: status %d", sub.ID, resp.StatusCode)
	return nil
}

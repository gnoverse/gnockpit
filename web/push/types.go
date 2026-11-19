package push

import "fmt"

// AlertType identifies a category of push notification alert.
type AlertType string

const (
	AlertValidatorMissingBlocks AlertType = "validator_missing_blocks"
	AlertChainStuck             AlertType = "chain_stuck"
	AlertValsetChanged          AlertType = "valset_changed"
	AlertHaltScheduled          AlertType = "halt_scheduled"
	AlertHaltApproaching        AlertType = "halt_approaching"
	AlertETAReminder            AlertType = "eta_reminder" // devices only, never routed to a target
)

// EntityIDLocal is the sentinel entity ID for the locally monitored node.
const EntityIDLocal = "local"

// Alert represents a single fired or recovered alert.
type Alert struct {
	Type     AlertType
	EntityID string // empty for global alerts, EntityIDLocal for local node, NodeID for peers
	Firing   bool   // true = problem detected, false = recovery
	Notice   bool   // a one-off event, Firing: no recovery follows
	Title    string
	Body     string
}

// Subscription represents a browser push subscription for one device.
type Subscription struct {
	ID       string
	Endpoint string
	P256dh   string
	Auth     string
}

// SubscriptionAlert is one alert preference row for a subscription.
type SubscriptionAlert struct {
	SubscriptionID string    `json:"subscription_id"`
	AlertType      AlertType `json:"alert_type"`
	EntityID       string    `json:"entity_id"`
	RecoveryNotif  bool      `json:"recovery_notif"`
	Lead           string    `json:"lead,omitempty"` // halt_approaching: how far ahead of the halt, as ParseLead reads it
}

// ValidateDeviceAlerts checks the alerts a device saves: each a routable
// alert, halt_approaching with a lead, no other with one.
func ValidateDeviceAlerts(alerts []SubscriptionAlert) error {
	for _, a := range alerts {
		if _, err := routable(string(a.AlertType)); err != nil {
			return err
		}
		if a.AlertType != AlertHaltApproaching {
			if a.Lead != "" {
				return fmt.Errorf("%s takes no lead", a.AlertType)
			}
			continue
		}
		if _, err := ParseLead(a.Lead); err != nil {
			return fmt.Errorf("halt_approaching: %w", err)
		}
	}
	return nil
}

// Entity is a selectable validator target returned by the entities API.
type Entity struct {
	ValAddress string `json:"val_address,omitempty"`
	Moniker    string `json:"moniker,omitempty"`
}

// EntitiesResponse is the payload for GET /api/push/entities.
type EntitiesResponse struct {
	Validators      []Entity `json:"validators"`
	ChainStuckSecs  int      `json:"chain_stuck_secs"`
	MaxMissedInARow int      `json:"max_missed_in_a_row"`
}

// SubscribeRequest is the payload for POST /api/push/subscribe.
type SubscribeRequest struct {
	Endpoint string              `json:"endpoint"`
	P256dh   string              `json:"p256dh"`
	Auth     string              `json:"auth"`
	Alerts   []SubscriptionAlert `json:"alerts"`
}

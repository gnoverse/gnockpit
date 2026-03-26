package push

// AlertType identifies a category of push notification alert.
type AlertType string

const (
	AlertValidatorMissingVotes AlertType = "validator_missing_votes"
	AlertChainStuck            AlertType = "chain_stuck"
)

// EntityIDLocal is the sentinel entity ID for the locally monitored node.
const EntityIDLocal = "local"

// Alert represents a single fired or recovered alert.
type Alert struct {
	Type     AlertType
	EntityID string // empty for global alerts, EntityIDLocal for local node, NodeID for peers
	Firing   bool   // true = problem detected, false = recovery
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
}

// Entity is a selectable target for per-node alerts, returned by the entities API.
type Entity struct {
	NodeID     string `json:"node_id,omitempty"`
	ValAddress string `json:"val_address,omitempty"`
	Moniker    string `json:"moniker,omitempty"`
	IP         string `json:"ip,omitempty"`
	IsLocal    bool   `json:"is_local,omitempty"`
}

// EntitiesResponse is the payload for GET /api/push/entities.
type EntitiesResponse struct {
	Validators     []Entity `json:"validators"`
	ChainStuckSecs int      `json:"chain_stuck_secs"`
	MissedBlocks   int      `json:"missed_blocks"`
}

// SubscribeRequest is the payload for POST /api/push/subscribe.
type SubscribeRequest struct {
	Endpoint string              `json:"endpoint"`
	P256dh   string              `json:"p256dh"`
	Auth     string              `json:"auth"`
	Alerts   []SubscriptionAlert `json:"alerts"`
}

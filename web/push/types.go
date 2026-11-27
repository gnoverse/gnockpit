package push

import (
	"fmt"
	"slices"
	"strings"
)

// AlertType identifies a category of push notification alert.
type AlertType string

const (
	AlertValidatorMissingBlocks AlertType = "validator_missing_blocks"
	AlertChainStuck             AlertType = "chain_stuck"
	AlertValsetChanged          AlertType = "valset_changed"
	AlertHaltScheduled          AlertType = "halt_scheduled"
	AlertHaltApproaching        AlertType = "halt_approaching"
	AlertETAReminder            AlertType = "eta_reminder" // devices only, never routed to a target
	AlertRanking                AlertType = "ranking"      // targets only, never sent to a device
)

// EntityIDLocal is the sentinel entity ID for the locally monitored node.
const EntityIDLocal = "local"

// Alert represents a single fired or recovered alert.
type Alert struct {
	Type     AlertType
	EntityID string // empty for global alerts, EntityIDLocal for local node, NodeID for peers
	Firing   bool   // true = problem detected, false = recovery
	Notice   bool   // told as information rather than a fault: a one-off event, or the chain stopping at its scheduled halt, whose recovery still follows
	Title    string
	Body     Body
	Link     string // the page a tap on the notification opens, from the site's root; "": the dashboard
}

// Body is what an alert says under its title, as spans of text: each key
// fact, such as a validator's name, a count or a version, is a bold span.
// Text targets and browser push get its String, the text without emphasis.
type Body []Span

// Span is a run of a Body's text, bold or plain.
type Span struct {
	Text string
	Bold bool
}

// String returns b's text, its spans' text joined; "" for none.
func (b Body) String() string {
	var sb strings.Builder
	for _, s := range b {
		sb.WriteString(s.Text)
	}
	return sb.String()
}

// plain returns s as a plain span.
func plain(s string) Span { return Span{Text: s} }

// bold returns s as a bold span, a key fact. s may hold spaces at either end
// or line breaks, as a name a peer or a profile writes can: the Discord
// renderer (markdownLines) keeps them outside the bold.
func bold(s string) Span { return Span{Text: s, Bold: true} }

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

// ValidateKeep checks the alert types a save keeps: each a routable alert,
// none of the alerts it saves.
func ValidateKeep(keep []AlertType, alerts []SubscriptionAlert) error {
	for _, t := range keep {
		if _, err := routable(string(t)); err != nil {
			return err
		}
		for _, a := range alerts {
			if a.AlertType == t {
				return fmt.Errorf("%s is kept and saved at once", t)
			}
		}
	}
	return nil
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
	// Keep names the alert types whose stored alerts the save leaves as they
	// are, as a page sends for alerts it could not show: Alerts has none of
	// them.
	Keep []AlertType `json:"keep,omitempty"`
	// Shown lists, by address, the validators whose alerts the page's menu
	// showed: the device's stored alerts on any other validator, such as one
	// that left the set, stay as they are unless Alerts names it.
	Shown []string `json:"shown,omitempty"`
}

// ValidateShown checks the validators a save says its menu showed: each an
// address, and none while the save keeps the validator alerts, as a menu that
// could show no validator does.
func ValidateShown(shown []string, keep []AlertType) error {
	if len(shown) > 0 && slices.Contains(keep, AlertValidatorMissingBlocks) {
		return fmt.Errorf("%s is kept and shown at once", AlertValidatorMissingBlocks)
	}
	for _, v := range shown {
		if v == "" {
			return fmt.Errorf("shown: %q is no validator address", v)
		}
	}
	return nil
}

package push

import (
	"fmt"
	"time"

	"github.com/gnoverse/gnockpit/node"
)

// AlertDetector holds in-memory state for transition-based alert detection.
// Not safe for concurrent use; callers must ensure single-goroutine access
// (Manager.EvaluateAndNotify is called from the publishLoop goroutine only).
type AlertDetector struct {
	chainStuckSecs   int
	lastHeightChange time.Time
	lastHeight       int64
	initialized      bool
	firingAlerts     map[alertKey]bool
}

type alertKey struct {
	alertType AlertType
	entityID  string
}

// NewAlertDetector creates a detector with the given chain-stuck threshold in seconds.
func NewAlertDetector(chainStuckSecs int) *AlertDetector {
	return &AlertDetector{
		chainStuckSecs: chainStuckSecs,
		firingAlerts:   make(map[alertKey]bool),
	}
}

// OverrideLastHeightChange is for testing only.
func (d *AlertDetector) OverrideLastHeightChange(t time.Time) {
	d.lastHeightChange = t
}

// Detect returns newly fired or recovered alerts based on snap.
// Each alert fires at most once per state transition (bad→good or good→bad).
func (d *AlertDetector) Detect(snap *node.Snapshot) []Alert {
	var alerts []Alert
	alerts = append(alerts, d.detectLocalUnreachable(snap)...)
	alerts = append(alerts, d.detectLocalOutOfSync(snap)...)
	alerts = append(alerts, d.detectChainStuck(snap)...)
	alerts = append(alerts, d.detectValidatorMissingVotes(snap)...)
	alerts = append(alerts, d.detectPeerAlerts(snap)...)
	return alerts
}

// transition returns an Alert if the state for (alertType, entityID) changed,
// or nil if it is the same as before.
func (d *AlertDetector) transition(alertType AlertType, entityID string, firing bool, firingTitle, firingBody, recoveryTitle, recoveryBody string) *Alert {
	key := alertKey{alertType, entityID}
	if d.firingAlerts[key] == firing {
		return nil
	}
	d.firingAlerts[key] = firing
	a := Alert{Type: alertType, EntityID: entityID, Firing: firing}
	if firing {
		a.Title, a.Body = firingTitle, firingBody
	} else {
		a.Title, a.Body = recoveryTitle, recoveryBody
	}
	return &a
}

func (d *AlertDetector) detectLocalUnreachable(snap *node.Snapshot) []Alert {
	unreachable := snap.Error != "" || snap.Status == nil
	a := d.transition(AlertNodeUnreachable, EntityIDLocal, unreachable,
		"Node unreachable", "Local node is unreachable",
		"Node reachable", "Local node is back online",
	)
	if a != nil {
		return []Alert{*a}
	}
	return nil
}

func (d *AlertDetector) detectLocalOutOfSync(snap *node.Snapshot) []Alert {
	if snap.Status == nil {
		return nil
	}
	a := d.transition(AlertNodeOutOfSync, EntityIDLocal, snap.Status.SyncInfo.CatchingUp,
		"Node out of sync", "Local node is catching up with the chain",
		"Node in sync", "Local node is back in sync",
	)
	if a != nil {
		return []Alert{*a}
	}
	return nil
}

func (d *AlertDetector) detectChainStuck(snap *node.Snapshot) []Alert {
	if snap.Status == nil {
		return nil
	}
	// LatestBlockHeight is a string in node.SyncInfo; parse to int64 for comparison.
	var height int64
	fmt.Sscanf(snap.Status.SyncInfo.LatestBlockHeight, "%d", &height)
	now := time.Now()

	if !d.initialized || height != d.lastHeight {
		d.lastHeight = height
		d.lastHeightChange = now
		d.initialized = true
	}

	stuck := time.Since(d.lastHeightChange) > time.Duration(d.chainStuckSecs)*time.Second
	a := d.transition(AlertChainStuck, "", stuck,
		"Chain stuck", fmt.Sprintf("No new block for %d+ seconds", d.chainStuckSecs),
		"Chain resumed", fmt.Sprintf("New block at height %d", height),
	)
	if a != nil {
		return []Alert{*a}
	}
	return nil
}

func (d *AlertDetector) detectValidatorMissingVotes(snap *node.Snapshot) []Alert {
	if snap.Consensus == nil {
		return nil
	}
	var alerts []Alert
	for _, v := range snap.Consensus.Votes {
		missing := !v.Prevoted && !v.Precommit
		name := v.Name
		if name == "" {
			name = v.Address
		}
		a := d.transition(AlertValidatorMissingVotes, v.Address, missing,
			"Validator missing votes", fmt.Sprintf("%s is not voting", name),
			"Validator back online", fmt.Sprintf("%s is voting again", name),
		)
		if a != nil {
			alerts = append(alerts, *a)
		}
	}
	return alerts
}

func (d *AlertDetector) detectPeerAlerts(snap *node.Snapshot) []Alert {
	var alerts []Alert
	for _, p := range snap.Peers {
		if p.NodeID == "" {
			continue
		}
		name := p.Moniker
		if name == "" {
			name = p.NodeID
		}

		if a := d.transition(AlertNodeUnreachable, p.NodeID, p.Error != "",
			"Peer unreachable", fmt.Sprintf("Peer %s is unreachable", name),
			"Peer reachable", fmt.Sprintf("Peer %s is back online", name),
		); a != nil {
			alerts = append(alerts, *a)
		}

		if a := d.transition(AlertNodeOutOfSync, p.NodeID, p.CatchingUp,
			"Peer out of sync", fmt.Sprintf("Peer %s is catching up", name),
			"Peer in sync", fmt.Sprintf("Peer %s is back in sync", name),
		); a != nil {
			alerts = append(alerts, *a)
		}
	}
	return alerts
}

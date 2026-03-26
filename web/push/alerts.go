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
	chainStuckSecs       int
	missedBlocksThreshold int
	lastHeightChange     time.Time
	lastHeight           int64
	initialized          bool
	firingAlerts         map[alertKey]bool
	missedCounts         map[string]int
}

type alertKey struct {
	alertType AlertType
	entityID  string
}

// NewAlertDetector creates a detector with the given thresholds.
func NewAlertDetector(chainStuckSecs, missedBlocksThreshold int) *AlertDetector {
	return &AlertDetector{
		chainStuckSecs:        chainStuckSecs,
		missedBlocksThreshold: missedBlocksThreshold,
		firingAlerts:          make(map[alertKey]bool),
		missedCounts:          make(map[string]int),
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
	alerts = append(alerts, d.detectChainStuck(snap)...)
	alerts = append(alerts, d.detectValidatorMissingVotes(snap)...)
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
		if missing {
			d.missedCounts[v.Address]++
			if d.missedCounts[v.Address] >= d.missedBlocksThreshold {
				if a := d.transition(AlertValidatorMissingVotes, v.Address, true,
					"Validator missing votes", fmt.Sprintf("%s is not voting", name),
					"", "",
				); a != nil {
					alerts = append(alerts, *a)
				}
			}
		} else {
			d.missedCounts[v.Address] = 0
			if a := d.transition(AlertValidatorMissingVotes, v.Address, false,
				"", "",
				"Validator back online", fmt.Sprintf("%s is voting again", name),
			); a != nil {
				alerts = append(alerts, *a)
			}
		}
	}
	return alerts
}

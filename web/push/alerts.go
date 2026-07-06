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
	maxMissedInARow  int
	lastHeightChange time.Time
	lastHeight       int64
	initialized      bool
	firingAlerts     map[alertKey]bool
}

type alertKey struct {
	alertType AlertType
	entityID  string
}

// NewAlertDetector creates a detector with the given thresholds.
func NewAlertDetector(chainStuckSecs, maxMissedInARow int) *AlertDetector {
	return &AlertDetector{
		chainStuckSecs:  chainStuckSecs,
		maxMissedInARow: maxMissedInARow,
		firingAlerts:    make(map[alertKey]bool),
	}
}

// MissingBlocksFiring returns the set of validator addresses whose missing-blocks
// alert is currently firing — i.e. those currently considered down/inactive.
func (d *AlertDetector) MissingBlocksFiring() map[string]bool {
	out := make(map[string]bool)
	for k, firing := range d.firingAlerts {
		if k.alertType == AlertValidatorMissingBlocks && firing {
			out[k.entityID] = true
		}
	}
	return out
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
	alerts = append(alerts, d.detectValidatorMissingBlocks(snap)...)
	return alerts
}

// transition returns an Alert if the state for (alertType, entityID) changed,
// or nil if it is the same as before. The first time a key is seen, the state
// is recorded silently without emitting an alert — this prevents spurious
// notifications after a restart when pre-existing conditions are re-detected.
func (d *AlertDetector) transition(alertType AlertType, entityID string, firing bool, firingTitle, firingBody, recoveryTitle, recoveryBody string) *Alert {
	key := alertKey{alertType, entityID}
	prev, exists := d.firingAlerts[key]
	d.firingAlerts[key] = firing
	if !exists {
		return nil
	}
	if prev == firing {
		return nil
	}
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
	if n, _ := fmt.Sscanf(snap.Status.SyncInfo.LatestBlockHeight, "%d", &height); n == 0 {
		return nil
	}
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

func (d *AlertDetector) detectValidatorMissingBlocks(snap *node.Snapshot) []Alert {
	if snap.Signing == nil || d.maxMissedInARow < 1 {
		return nil
	}
	n := d.maxMissedInARow

	name := make(map[string]string, len(snap.Validators))
	for _, v := range snap.Validators {
		nm := v.Name
		if nm == "" {
			nm = v.Address
		}
		name[v.Address] = nm
	}

	var alerts []Alert
	for addr, vs := range snap.Signing.ValidatorSigning {
		// Hysteresis: fire after n missed in a row, recover only after n signed in
		// a row. In between, hold the previous state so a flapping validator does
		// not toggle the alert. First observation records state silently.
		prev, exists := d.firingAlerts[alertKey{AlertValidatorMissingBlocks, addr}]
		var firing bool
		switch {
		case !exists:
			firing = vs.MissedInARow >= n
		case !prev && vs.MissedInARow >= n:
			firing = true
		case prev && vs.SignedInARow >= n:
			firing = false
		default:
			firing = prev
		}

		nm := name[addr]
		if nm == "" {
			nm = addr
		}
		if a := d.transition(AlertValidatorMissingBlocks, addr, firing,
			"Validator missing blocks", fmt.Sprintf("%s missed %d blocks in a row", nm, vs.MissedInARow),
			"Validator back online", fmt.Sprintf("%s is signing again (%d in a row)", nm, vs.SignedInARow),
		); a != nil {
			alerts = append(alerts, *a)
		}
	}
	// Drop state for validators no longer in the set, so firingAlerts doesn't grow
	// unbounded as the set rotates and a rejoining validator starts fresh.
	for k := range d.firingAlerts {
		if k.alertType != AlertValidatorMissingBlocks {
			continue
		}
		if _, ok := snap.Signing.ValidatorSigning[k.entityID]; !ok {
			delete(d.firingAlerts, k)
		}
	}
	return alerts
}

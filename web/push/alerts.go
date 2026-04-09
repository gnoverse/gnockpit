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
	chainStuckSecs  int
	missedBlocksPct int
	lastHeightChange time.Time
	lastHeight       int64
	initialized      bool
	firingAlerts     map[alertKey]bool
	firstSeenHeight  map[string]int64
}

type alertKey struct {
	alertType AlertType
	entityID  string
}

// NewAlertDetector creates a detector with the given thresholds.
func NewAlertDetector(chainStuckSecs, missedBlocksPct int) *AlertDetector {
	return &AlertDetector{
		chainStuckSecs:  chainStuckSecs,
		missedBlocksPct: missedBlocksPct,
		firingAlerts:    make(map[alertKey]bool),
		firstSeenHeight: make(map[string]int64),
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
	if snap.Signing == nil || snap.Signing.WindowSize == 0 || snap.Status == nil {
		return nil
	}
	var currentHeight int64
	if n, _ := fmt.Sscanf(snap.Status.SyncInfo.LatestBlockHeight, "%d", &currentHeight); n == 0 {
		return nil
	}

	windowSize := int64(snap.Signing.WindowSize)

	// Collect all validator addresses: those with signing data and those in the validator set.
	addrs := make(map[string]string) // addr -> display name
	for _, v := range snap.Validators {
		name := v.Name
		if name == "" {
			name = v.Address
		}
		addrs[v.Address] = name
	}
	for addr := range snap.Signing.ValidatorSigns {
		if _, ok := addrs[addr]; !ok {
			addrs[addr] = addr
		}
	}

	var alerts []Alert
	for addr, name := range addrs {
		// Record the first block height at which we observed this validator.
		if _, seen := d.firstSeenHeight[addr]; !seen {
			d.firstSeenHeight[addr] = currentHeight
		}
		firstSeen := d.firstSeenHeight[addr]

		// Effective window starts at the later of (currentHeight-windowSize) and firstSeen,
		// preventing false positives for new validators and after restarts.
		windowStart := currentHeight - windowSize
		if firstSeen > windowStart {
			windowStart = firstSeen
		}
		effectiveWindow := currentHeight - windowStart
		if effectiveWindow <= 0 {
			continue
		}

		signed := int64(snap.Signing.ValidatorSigns[addr])
		missed := effectiveWindow - signed
		if missed < 0 {
			missed = 0
		}
		missedPct := int(missed * 100 / effectiveWindow)

		firing := missedPct >= d.missedBlocksPct
		if a := d.transition(AlertValidatorMissingBlocks, addr, firing,
			"Validator missing blocks", fmt.Sprintf("%s missed %d%% of recent blocks", name, missedPct),
			"Validator back online", fmt.Sprintf("%s is signing again (%d%% missed)", name, missedPct),
		); a != nil {
			alerts = append(alerts, *a)
		}
	}
	return alerts
}

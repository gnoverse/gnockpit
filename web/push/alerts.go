package push

import (
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"

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
// halt is the chain's halt param, 0 for none: a stop at that height is the
// planned halt.
func (d *AlertDetector) Detect(snap *node.Snapshot, halt int64) []Alert {
	var alerts []Alert
	alerts = append(alerts, d.detectChainStuck(snap, halt)...)
	alerts = append(alerts, d.detectValidatorMissingBlocks(snap)...)
	return alerts
}

// transition returns an Alert if the state for (alertType, entityID) changed,
// or nil if it is the same as before. The first time a key is seen, the state
// is recorded silently without emitting an alert — this prevents spurious
// notifications after a restart when pre-existing conditions are re-detected.
func (d *AlertDetector) transition(alertType AlertType, entityID string, firing bool, firingTitle string, firingBody Body, recoveryTitle string, recoveryBody Body) *Alert {
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

func (d *AlertDetector) detectChainStuck(snap *node.Snapshot, halt int64) []Alert {
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
		"Chain stuck", stuckBody(d.chainStuckSecs),
		"Chain resumed", resumedBody(height),
	)
	if a == nil {
		return nil
	}
	if a.Firing && halt != 0 && height == halt {
		a.Notice = true
		a.Title = fmt.Sprintf("Coordinated halt reached at #%d", height)
		a.Body = Body{plain("The chain stopped at the scheduled height, as planned.")}
	}
	return []Alert{*a}
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
			"Validator missing blocks", missedBody(nm, vs.MissedInARow),
			"Validator back online", signingBody(nm, vs.SignedInARow),
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

// alertNameLimit is the most runes of a validator's name an alert quotes.
// Its operator or a peer sets the name, at any length: cut, it keeps the
// message readable, and every line of a Discord description short of the
// length at which packEmbeds cuts a line, which could split a bold span or
// an escape.
const alertNameLimit = 64

// cutName returns name whole up to limit runes, else its first limit-1 runes
// and "…".
func cutName(name string, limit int) string {
	if utf8.RuneCountInString(name) <= limit {
		return name
	}
	return string([]rune(name)[:limit-1]) + "…"
}

// missedBody tells the blocks a validator missed in a row, its name (cut to
// alertNameLimit) and the count bold.
func missedBody(name string, missed int) Body {
	return Body{bold(cutName(name, alertNameLimit)), plain(" missed "), bold(strconv.Itoa(missed)), plain(" blocks in a row")}
}

// signingBody tells a validator signing again and the blocks it signed in a
// row, its name (cut to alertNameLimit) and the count bold.
func signingBody(name string, signed int) Body {
	return Body{bold(cutName(name, alertNameLimit)), plain(" is signing again ("), bold(strconv.Itoa(signed)), plain(" in a row)")}
}

// stuckBody says no block came for over secs seconds, the duration bold.
func stuckBody(secs int) Body {
	return Body{plain("No new block for "), bold(fmt.Sprintf("%d+ seconds", secs))}
}

// resumedBody says a block came at height, the height bold.
func resumedBody(height int64) Body {
	return Body{plain("New block at height "), bold(strconv.FormatInt(height, 10))}
}

package push_test

import (
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/web/push"
)

// height is a string because node.Status.SyncInfo.LatestBlockHeight is a string.
func makeSnap(height string, snapErr string, votes []node.VoteInfo, catchingUp bool) *node.Snapshot {
	snap := &node.Snapshot{
		Error:     snapErr,
		Timestamp: time.Now(),
	}
	if snapErr == "" {
		snap.Status = &node.Status{
			SyncInfo: node.SyncInfo{
				LatestBlockHeight: height,
				CatchingUp:        catchingUp,
			},
		}
		snap.Consensus = &node.ConsensusState{Votes: votes}
	}
	return snap
}

// makeSnapWithSigning builds a snapshot whose signing stats carry per-validator
// consecutive missed/signed streaks — what the missing-blocks alert keys on.
func makeSnapWithSigning(height string, signing map[string]node.ValSigning) *node.Snapshot {
	return &node.Snapshot{
		Timestamp: time.Now(),
		Status:    &node.Status{SyncInfo: node.SyncInfo{LatestBlockHeight: height}},
		Signing:   &node.SigningStats{WindowSize: 100, ValidatorSigning: signing},
	}
}

// vs is a ValSigning with the given consecutive missed / signed streaks.
func vs(missedInARow, signedInARow int) node.ValSigning {
	return node.ValSigning{MissedInARow: missedInARow, SignedInARow: signedInARow}
}

func findAlert(alerts []push.Alert, t push.AlertType, entityID string) *push.Alert {
	for i := range alerts {
		if alerts[i].Type == t && alerts[i].EntityID == entityID {
			return &alerts[i]
		}
	}
	return nil
}

func TestDetect_ChainStuck_Fires(t *testing.T) {
	d := push.NewAlertDetector(30, 10)
	snap := makeSnap("100", "", nil, false)

	alerts := d.Detect(snap)
	if a := findAlert(alerts, push.AlertChainStuck, ""); a != nil {
		t.Error("expected no alert on first call")
	}

	d.OverrideLastHeightChange(time.Now().Add(-31 * time.Second))
	alerts = d.Detect(snap)
	a := findAlert(alerts, push.AlertChainStuck, "")
	if a == nil || !a.Firing {
		t.Error("expected chain_stuck to fire after threshold exceeded")
	}
}

func TestDetect_ChainStuck_Recovers(t *testing.T) {
	d := push.NewAlertDetector(30, 10)
	snap100 := makeSnap("100", "", nil, false)
	d.Detect(snap100)
	d.OverrideLastHeightChange(time.Now().Add(-31 * time.Second))
	d.Detect(snap100) // fires

	snap101 := makeSnap("101", "", nil, false)
	alerts := d.Detect(snap101)
	a := findAlert(alerts, push.AlertChainStuck, "")
	if a == nil || a.Firing {
		t.Error("expected chain_stuck recovery when height advances")
	}
}

func TestDetect_ChainStuck_NoDoubleFireWithoutRecovery(t *testing.T) {
	d := push.NewAlertDetector(30, 10)
	snap := makeSnap("100", "", nil, false)
	d.Detect(snap)
	d.OverrideLastHeightChange(time.Now().Add(-31 * time.Second))
	d.Detect(snap) // fires

	alerts := d.Detect(snap) // same state, must not re-fire
	if a := findAlert(alerts, push.AlertChainStuck, ""); a != nil {
		t.Error("expected no re-fire while still stuck")
	}
}

func TestDetect_ChainStuck_EmptyHeightDoesNotResetClock(t *testing.T) {
	d := push.NewAlertDetector(30, 10)
	// Establish a known height.
	d.Detect(makeSnap("100", "", nil, false))
	d.OverrideLastHeightChange(time.Now().Add(-31 * time.Second))
	// Simulate a transient empty height string (RPC hiccup).
	alerts := d.Detect(makeSnap("", "", nil, false))
	if a := findAlert(alerts, push.AlertChainStuck, ""); a != nil {
		t.Error("empty height should be ignored, not reset the stuck clock")
	}
	// Height still stuck — alert should fire on next valid poll.
	alerts = d.Detect(makeSnap("100", "", nil, false))
	if a := findAlert(alerts, push.AlertChainStuck, ""); a == nil || !a.Firing {
		t.Error("expected chain_stuck to fire when height is still the same after empty-height poll")
	}
}

// ---- validator_missing_blocks tests ----
// The alert is hysteretic: it fires after `testThreshold` consecutive missed
// blocks and only recovers after `testThreshold` consecutive signed blocks. The
// first observation of a validator records state silently (startup suppression).

const testThreshold = 5 // consecutive blocks to fire / recover

func TestDetect_ValidatorMissingBlocks_FirstDetectionSilent(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	// Already down on first sight (e.g. after a restart): record silently.
	alerts := d.Detect(makeSnapWithSigning("100", map[string]node.ValSigning{"g1bbb": vs(testThreshold, 0)}))
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a != nil {
		t.Error("expected no alert on first detection (startup suppression)")
	}
}

func TestDetect_ValidatorMissingBlocks_BelowThresholdDoesNotFire(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	// Baseline active, then missing fewer than the threshold in a row: stays active.
	d.Detect(makeSnapWithSigning("100", map[string]node.ValSigning{"g1bbb": vs(0, testThreshold)}))
	alerts := d.Detect(makeSnapWithSigning("104", map[string]node.ValSigning{"g1bbb": vs(testThreshold-1, 0)}))
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a != nil {
		t.Error("expected no fire within the missed band (hysteresis hold)")
	}
}

func TestDetect_ValidatorMissingBlocks_Fires(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	active := map[string]node.ValSigning{"g1bbb": vs(0, testThreshold), "g1aaa": vs(0, testThreshold)}
	d.Detect(makeSnapWithSigning("100", active)) // baseline (silent, active)
	// bbb misses the threshold in a row — fires. aaa keeps signing.
	bad := map[string]node.ValSigning{"g1bbb": vs(testThreshold, 0), "g1aaa": vs(0, testThreshold)}
	alerts := d.Detect(makeSnapWithSigning("200", bad))
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a == nil || !a.Firing {
		t.Error("expected fire for g1bbb after missing the streak")
	}
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1aaa"); a != nil {
		t.Error("unexpected alert for still-signing g1aaa")
	}
}

func TestDetect_ValidatorMissingBlocks_NoDoubleFire(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	d.Detect(makeSnapWithSigning("100", map[string]node.ValSigning{"g1bbb": vs(0, testThreshold)}))
	down := map[string]node.ValSigning{"g1bbb": vs(testThreshold+5, 0)}
	d.Detect(makeSnapWithSigning("200", down)) // fires
	alerts := d.Detect(makeSnapWithSigning("201", down))
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a != nil {
		t.Error("expected no re-fire while still down")
	}
}

func TestDetect_ValidatorMissingBlocks_HoldsUntilRecoveryStreak(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	d.Detect(makeSnapWithSigning("100", map[string]node.ValSigning{"g1bbb": vs(0, testThreshold)})) // active
	d.Detect(makeSnapWithSigning("200", map[string]node.ValSigning{"g1bbb": vs(testThreshold, 0)})) // fires
	// Signs some, but fewer than the threshold in a row: must stay down.
	alerts := d.Detect(makeSnapWithSigning("205", map[string]node.ValSigning{"g1bbb": vs(0, testThreshold-1)}))
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a != nil {
		t.Error("expected NO recovery before signing the full streak (hysteresis hold)")
	}
	// Signs the full streak: recovers.
	alerts = d.Detect(makeSnapWithSigning("210", map[string]node.ValSigning{"g1bbb": vs(0, testThreshold)}))
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a == nil || a.Firing {
		t.Error("expected recovery after signing the full streak")
	}
}

func TestDetect_ValidatorMissingBlocks_RefireAfterRecovery(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	down := map[string]node.ValSigning{"g1bbb": vs(testThreshold, 0)}
	up := map[string]node.ValSigning{"g1bbb": vs(0, testThreshold)}
	d.Detect(makeSnapWithSigning("100", up))   // baseline active
	d.Detect(makeSnapWithSigning("200", down)) // fires
	d.Detect(makeSnapWithSigning("300", up))   // recovers
	alerts := d.Detect(makeSnapWithSigning("400", down))
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a == nil || !a.Firing {
		t.Error("expected re-fire after recovery when missing the streak again")
	}
}

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

// makeSnapWithSigning creates a snapshot with signing stats and a block height.
func makeSnapWithSigning(height string, windowSize int, validatorSigns map[string]int) *node.Snapshot {
	return &node.Snapshot{
		Timestamp: time.Now(),
		Status: &node.Status{
			SyncInfo: node.SyncInfo{LatestBlockHeight: height},
		},
		Signing: &node.SigningStats{
			WindowSize:     windowSize,
			ValidatorSigns: validatorSigns,
		},
	}
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
// Pattern: first Detect call at height H establishes firstSeenHeight for each validator.
// Second call at H+windowSize gives a full effective window, making assertions predictable.

const (
	testWindow    = 100
	testThreshold = 5 // 5%
)

func TestDetect_ValidatorMissingBlocks_WarmupPreventsEarlyFire(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	// Even with 0 signed blocks (100% miss rate), first call should not fire
	// because effectiveWindow = 0.
	alerts := d.Detect(makeSnapWithSigning("100", testWindow, map[string]int{"g1bbb": 0}))
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a != nil {
		t.Error("expected no alert on first detection (warmup period)")
	}
}

func TestDetect_ValidatorMissingBlocks_ThresholdNotReached(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	// 3% missed (3/100) — below the 5% threshold.
	signs := map[string]int{"g1bbb": 97}
	d.Detect(makeSnapWithSigning("100", testWindow, signs)) // establish firstSeen
	alerts := d.Detect(makeSnapWithSigning("200", testWindow, signs))
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a != nil {
		t.Error("expected no alert when miss rate is below threshold")
	}
}

func TestDetect_ValidatorMissingBlocks_Fires(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	// bbb: 15% missed (85/100 signed) — above threshold.
	// aaa: 0% missed (100/100 signed) — no alert.
	signs := map[string]int{"g1bbb": 85, "g1aaa": 100}
	d.Detect(makeSnapWithSigning("100", testWindow, signs))
	alerts := d.Detect(makeSnapWithSigning("200", testWindow, signs))
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a == nil || !a.Firing {
		t.Error("expected validator_missing_blocks to fire for g1bbb")
	}
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1aaa"); a != nil {
		t.Error("unexpected alert for g1aaa who is signing all blocks")
	}
}

func TestDetect_ValidatorMissingBlocks_NoDoubleFireWithoutRecovery(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	signs := map[string]int{"g1bbb": 85} // 15% missed
	d.Detect(makeSnapWithSigning("100", testWindow, signs))
	d.Detect(makeSnapWithSigning("200", testWindow, signs)) // fires
	// Same state: must not re-fire.
	alerts := d.Detect(makeSnapWithSigning("201", testWindow, signs))
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a != nil {
		t.Error("expected no re-fire while still above threshold")
	}
}

func TestDetect_ValidatorMissingBlocks_Recovers(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	d.Detect(makeSnapWithSigning("100", testWindow, map[string]int{"g1bbb": 85}))
	d.Detect(makeSnapWithSigning("200", testWindow, map[string]int{"g1bbb": 85})) // fires
	// Miss rate drops below threshold.
	alerts := d.Detect(makeSnapWithSigning("201", testWindow, map[string]int{"g1bbb": 98})) // 2% missed
	a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb")
	if a == nil || a.Firing {
		t.Error("expected recovery when miss rate drops below threshold")
	}
}

func TestDetect_ValidatorMissingBlocks_RefireAfterRecovery(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	d.Detect(makeSnapWithSigning("100", testWindow, map[string]int{"g1bbb": 85}))
	d.Detect(makeSnapWithSigning("200", testWindow, map[string]int{"g1bbb": 85})) // fires
	d.Detect(makeSnapWithSigning("201", testWindow, map[string]int{"g1bbb": 98})) // recovers
	// Miss rate climbs again — must re-fire.
	alerts := d.Detect(makeSnapWithSigning("202", testWindow, map[string]int{"g1bbb": 85}))
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a == nil || !a.Firing {
		t.Error("expected re-fire after recovery when miss rate exceeds threshold again")
	}
}

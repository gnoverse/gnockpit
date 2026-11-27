package push_test

import (
	"reflect"
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

	alerts := d.Detect(snap, 0)
	if a := findAlert(alerts, push.AlertChainStuck, ""); a != nil {
		t.Error("expected no alert on first call")
	}

	d.OverrideLastHeightChange(time.Now().Add(-31 * time.Second))
	alerts = d.Detect(snap, 0)
	a := findAlert(alerts, push.AlertChainStuck, "")
	if a == nil || !a.Firing {
		t.Error("expected chain_stuck to fire after threshold exceeded")
	}
}

func TestDetect_ChainStuck_Recovers(t *testing.T) {
	d := push.NewAlertDetector(30, 10)
	snap100 := makeSnap("100", "", nil, false)
	d.Detect(snap100, 0)
	d.OverrideLastHeightChange(time.Now().Add(-31 * time.Second))
	d.Detect(snap100, 0) // fires

	snap101 := makeSnap("101", "", nil, false)
	alerts := d.Detect(snap101, 0)
	a := findAlert(alerts, push.AlertChainStuck, "")
	if a == nil || a.Firing {
		t.Error("expected chain_stuck recovery when height advances")
	}
}

func TestDetect_ChainStuck_NoDoubleFireWithoutRecovery(t *testing.T) {
	d := push.NewAlertDetector(30, 10)
	snap := makeSnap("100", "", nil, false)
	d.Detect(snap, 0)
	d.OverrideLastHeightChange(time.Now().Add(-31 * time.Second))
	d.Detect(snap, 0) // fires

	alerts := d.Detect(snap, 0) // same state, must not re-fire
	if a := findAlert(alerts, push.AlertChainStuck, ""); a != nil {
		t.Error("expected no re-fire while still stuck")
	}
}

func TestDetect_ChainStuck_EmptyHeightDoesNotResetClock(t *testing.T) {
	d := push.NewAlertDetector(30, 10)
	// Establish a known height.
	d.Detect(makeSnap("100", "", nil, false), 0)
	d.OverrideLastHeightChange(time.Now().Add(-31 * time.Second))
	// Simulate a transient empty height string (RPC hiccup).
	alerts := d.Detect(makeSnap("", "", nil, false), 0)
	if a := findAlert(alerts, push.AlertChainStuck, ""); a != nil {
		t.Error("empty height should be ignored, not reset the stuck clock")
	}
	// Height still stuck — alert should fire on next valid poll.
	alerts = d.Detect(makeSnap("100", "", nil, false), 0)
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
	alerts := d.Detect(makeSnapWithSigning("100", map[string]node.ValSigning{"g1bbb": vs(testThreshold, 0)}), 0)
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a != nil {
		t.Error("expected no alert on first detection (startup suppression)")
	}
}

func TestDetect_ValidatorMissingBlocks_BelowThresholdDoesNotFire(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	// Baseline active, then missing fewer than the threshold in a row: stays active.
	d.Detect(makeSnapWithSigning("100", map[string]node.ValSigning{"g1bbb": vs(0, testThreshold)}), 0)
	alerts := d.Detect(makeSnapWithSigning("104", map[string]node.ValSigning{"g1bbb": vs(testThreshold-1, 0)}), 0)
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a != nil {
		t.Error("expected no fire within the missed band (hysteresis hold)")
	}
}

func TestDetect_ValidatorMissingBlocks_Fires(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	active := map[string]node.ValSigning{"g1bbb": vs(0, testThreshold), "g1aaa": vs(0, testThreshold)}
	d.Detect(makeSnapWithSigning("100", active), 0) // baseline (silent, active)
	// bbb misses the threshold in a row — fires. aaa keeps signing.
	bad := map[string]node.ValSigning{"g1bbb": vs(testThreshold, 0), "g1aaa": vs(0, testThreshold)}
	alerts := d.Detect(makeSnapWithSigning("200", bad), 0)
	bbb := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb")
	if bbb == nil || !bbb.Firing {
		t.Fatal("expected fire for g1bbb after missing the streak")
	}
	wantBody := push.Body{{Text: "g1bbb", Bold: true}, {Text: " missed "}, {Text: "5", Bold: true}, {Text: " blocks in a row"}}
	if !reflect.DeepEqual(bbb.Body, wantBody) {
		t.Errorf("g1bbb's body = %#v, want %#v", bbb.Body, wantBody)
	}
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1aaa"); a != nil {
		t.Error("unexpected alert for still-signing g1aaa")
	}
}

func TestDetect_ValidatorMissingBlocks_NoDoubleFire(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	d.Detect(makeSnapWithSigning("100", map[string]node.ValSigning{"g1bbb": vs(0, testThreshold)}), 0)
	down := map[string]node.ValSigning{"g1bbb": vs(testThreshold+5, 0)}
	d.Detect(makeSnapWithSigning("200", down), 0) // fires
	alerts := d.Detect(makeSnapWithSigning("201", down), 0)
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a != nil {
		t.Error("expected no re-fire while still down")
	}
}

func TestDetect_ValidatorMissingBlocks_HoldsUntilRecoveryStreak(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	d.Detect(makeSnapWithSigning("100", map[string]node.ValSigning{"g1bbb": vs(0, testThreshold)}), 0) // active
	d.Detect(makeSnapWithSigning("200", map[string]node.ValSigning{"g1bbb": vs(testThreshold, 0)}), 0) // fires
	// Signs some, but fewer than the threshold in a row: must stay down.
	alerts := d.Detect(makeSnapWithSigning("205", map[string]node.ValSigning{"g1bbb": vs(0, testThreshold-1)}), 0)
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a != nil {
		t.Error("expected NO recovery before signing the full streak (hysteresis hold)")
	}
	// Signs the full streak: recovers.
	alerts = d.Detect(makeSnapWithSigning("210", map[string]node.ValSigning{"g1bbb": vs(0, testThreshold)}), 0)
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a == nil || a.Firing {
		t.Error("expected recovery after signing the full streak")
	}
}

func TestDetect_ValidatorMissingBlocks_RefireAfterRecovery(t *testing.T) {
	d := push.NewAlertDetector(30, testThreshold)
	down := map[string]node.ValSigning{"g1bbb": vs(testThreshold, 0)}
	up := map[string]node.ValSigning{"g1bbb": vs(0, testThreshold)}
	d.Detect(makeSnapWithSigning("100", up), 0)   // baseline active
	d.Detect(makeSnapWithSigning("200", down), 0) // fires
	d.Detect(makeSnapWithSigning("300", up), 0)   // recovers
	alerts := d.Detect(makeSnapWithSigning("400", down), 0)
	if a := findAlert(alerts, push.AlertValidatorMissingBlocks, "g1bbb"); a == nil || !a.Firing {
		t.Error("expected re-fire after recovery when missing the streak again")
	}
}

func TestDetect_ChainStuckAtTheHaltIsANotice(t *testing.T) {
	d := push.NewAlertDetector(30, 10)
	d.Detect(makeSnap("1000", "", nil, false), 1000)
	d.OverrideLastHeightChange(time.Now().Add(-31 * time.Second))
	alerts := d.Detect(makeSnap("1000", "", nil, false), 1000)
	want := push.Alert{
		Type: push.AlertChainStuck, Firing: true, Notice: true,
		Title: "Coordinated halt reached at #1000",
		Body:  push.Body{{Text: "The chain stopped at the scheduled height, as planned."}},
	}
	if len(alerts) != 1 || !reflect.DeepEqual(alerts[0], want) {
		t.Fatalf("Detect(stuck at the halt) = %+v, want [%+v]", alerts, want)
	}

	alerts = d.Detect(makeSnap("1001", "", nil, false), 1000)
	want = push.Alert{
		Type: push.AlertChainStuck, Title: "Chain resumed",
		Body: push.Body{{Text: "New block at height "}, {Text: "1001", Bold: true}},
	}
	if len(alerts) != 1 || !reflect.DeepEqual(alerts[0], want) {
		t.Errorf("Detect(resumed) = %+v, want [%+v]", alerts, want)
	}
}

func TestDetect_ChainStuckOffTheHaltStaysRed(t *testing.T) {
	for _, tc := range []struct {
		name       string
		height     string
		halt       int64
		wantTitle  string
		wantNotice bool
	}{
		{"below the halt", "999", 1000, "Chain stuck", false},
		{"no halt param", "1000", 0, "Chain stuck", false},
		{"above a passed halt", "1001", 1000, "Chain stuck", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := push.NewAlertDetector(30, 10)
			d.Detect(makeSnap(tc.height, "", nil, false), tc.halt)
			d.OverrideLastHeightChange(time.Now().Add(-31 * time.Second))
			alerts := d.Detect(makeSnap(tc.height, "", nil, false), tc.halt)
			a := findAlert(alerts, push.AlertChainStuck, "")
			if a == nil || !a.Firing {
				t.Fatalf("Detect(stuck at %s, halt %d) = %+v, want a firing chain_stuck", tc.height, tc.halt, alerts)
			}
			if a.Title != tc.wantTitle || a.Notice != tc.wantNotice {
				t.Errorf("alert = {Title %q, Notice %v}, want {%q, %v}", a.Title, a.Notice, tc.wantTitle, tc.wantNotice)
			}
		})
	}
}

func TestDetect_ChainResumedBeforeTheHaltIsNotANotice(t *testing.T) {
	d := push.NewAlertDetector(30, 10)
	d.Detect(makeSnap("999", "", nil, false), 1000)
	d.OverrideLastHeightChange(time.Now().Add(-31 * time.Second))
	if a := findAlert(d.Detect(makeSnap("999", "", nil, false), 1000), push.AlertChainStuck, ""); a == nil || !a.Firing {
		t.Fatal("expected a firing chain_stuck below the halt")
	}

	alerts := d.Detect(makeSnap("1000", "", nil, false), 1000)
	want := push.Alert{
		Type: push.AlertChainStuck, Title: "Chain resumed",
		Body: push.Body{{Text: "New block at height "}, {Text: "1000", Bold: true}},
	}
	if len(alerts) != 1 || !reflect.DeepEqual(alerts[0], want) {
		t.Errorf("Detect(resumed at the halt height) = %+v, want [%+v]", alerts, want)
	}
}

package push_test

import (
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/web/push"
)

// height is a string because node.Status.SyncInfo.LatestBlockHeight is a string.
func makeSnap(height string, snapErr string, peers []node.Peer, votes []node.VoteInfo, catchingUp bool) *node.Snapshot {
	snap := &node.Snapshot{
		Error:     snapErr,
		Peers:     peers,
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

func findAlert(alerts []push.Alert, t push.AlertType, entityID string) *push.Alert {
	for i := range alerts {
		if alerts[i].Type == t && alerts[i].EntityID == entityID {
			return &alerts[i]
		}
	}
	return nil
}

func TestDetect_ChainStuck_Fires(t *testing.T) {
	d := push.NewAlertDetector(30)
	snap := makeSnap("100", "", nil, nil, false)

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
	d := push.NewAlertDetector(30)
	snap100 := makeSnap("100", "", nil, nil, false)
	d.Detect(snap100)
	d.OverrideLastHeightChange(time.Now().Add(-31 * time.Second))
	d.Detect(snap100) // fires

	snap101 := makeSnap("101", "", nil, nil, false)
	alerts := d.Detect(snap101)
	a := findAlert(alerts, push.AlertChainStuck, "")
	if a == nil || a.Firing {
		t.Error("expected chain_stuck recovery when height advances")
	}
}

func TestDetect_ChainStuck_NoDoubleFireWithoutRecovery(t *testing.T) {
	d := push.NewAlertDetector(30)
	snap := makeSnap("100", "", nil, nil, false)
	d.Detect(snap)
	d.OverrideLastHeightChange(time.Now().Add(-31 * time.Second))
	d.Detect(snap) // fires

	alerts := d.Detect(snap) // same state, must not re-fire
	if a := findAlert(alerts, push.AlertChainStuck, ""); a != nil {
		t.Error("expected no re-fire while still stuck")
	}
}

func TestDetect_ValidatorMissingVotes_Fires(t *testing.T) {
	d := push.NewAlertDetector(30)
	votes := []node.VoteInfo{
		{Address: "g1aaa", Name: "alice", Prevoted: true, Precommit: true},
		{Address: "g1bbb", Name: "bob", Prevoted: false, Precommit: false},
	}
	alerts := d.Detect(makeSnap("1", "", nil, votes, false))
	if a := findAlert(alerts, push.AlertValidatorMissingVotes, "g1bbb"); a == nil || !a.Firing {
		t.Error("expected validator_missing_votes for g1bbb")
	}
	if a := findAlert(alerts, push.AlertValidatorMissingVotes, "g1aaa"); a != nil {
		t.Error("unexpected alert for alice who is voting")
	}
}

func TestDetect_ValidatorMissingVotes_Recovers(t *testing.T) {
	d := push.NewAlertDetector(30)
	missing := []node.VoteInfo{{Address: "g1bbb", Prevoted: false, Precommit: false}}
	d.Detect(makeSnap("1", "", nil, missing, false)) // fires

	voting := []node.VoteInfo{{Address: "g1bbb", Prevoted: true, Precommit: true}}
	alerts := d.Detect(makeSnap("2", "", nil, voting, false))
	a := findAlert(alerts, push.AlertValidatorMissingVotes, "g1bbb")
	if a == nil || a.Firing {
		t.Error("expected recovery when validator starts voting")
	}
}

func TestDetect_LocalNodeUnreachable(t *testing.T) {
	d := push.NewAlertDetector(30)
	snap := makeSnap("", "connection refused", nil, nil, false)
	alerts := d.Detect(snap)
	if a := findAlert(alerts, push.AlertNodeUnreachable, push.EntityIDLocal); a == nil || !a.Firing {
		t.Error("expected node_unreachable for local node")
	}
}

func TestDetect_LocalNodeOutOfSync(t *testing.T) {
	d := push.NewAlertDetector(30)
	snap := makeSnap("1", "", nil, nil, true) // catchingUp = true
	alerts := d.Detect(snap)
	if a := findAlert(alerts, push.AlertNodeOutOfSync, push.EntityIDLocal); a == nil || !a.Firing {
		t.Error("expected node_out_of_sync for local node")
	}
}

func TestDetect_PeerUnreachable(t *testing.T) {
	d := push.NewAlertDetector(30)
	peers := []node.Peer{{NodeID: "peer1", Moniker: "peerA", Error: "timeout"}}
	alerts := d.Detect(makeSnap("1", "", peers, nil, false))
	if a := findAlert(alerts, push.AlertNodeUnreachable, "peer1"); a == nil || !a.Firing {
		t.Error("expected node_unreachable for peer1")
	}
}

func TestDetect_PeerOutOfSync(t *testing.T) {
	d := push.NewAlertDetector(30)
	peers := []node.Peer{{NodeID: "peer1", Moniker: "peerA", CatchingUp: true}}
	alerts := d.Detect(makeSnap("1", "", peers, nil, false))
	if a := findAlert(alerts, push.AlertNodeOutOfSync, "peer1"); a == nil || !a.Firing {
		t.Error("expected node_out_of_sync for peer1 that is catching up")
	}
}

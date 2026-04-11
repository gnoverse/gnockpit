package hub

import (
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/node"
)

func TestHealthAllHealthy(t *testing.T) {
	h := &Hub{
		probes: map[string]*ProbeState{
			"a": {Name: "a", Connected: true, LastUpdate: time.Now(), Height: "100", ChainID: "test3",
				Snapshot: &node.Snapshot{Status: &node.Status{NodeInfo: node.NodeInfo{Network: "test3", Version: "0.1"}}}},
			"b": {Name: "b", Connected: true, LastUpdate: time.Now(), Height: "100", ChainID: "test3",
				Snapshot: &node.Snapshot{Status: &node.Status{NodeInfo: node.NodeInfo{Network: "test3", Version: "0.1"}}}},
		},
	}
	report := h.ComputeHealth()
	if report.Healthy != 2 {
		t.Errorf("healthy = %d, want 2", report.Healthy)
	}
	if len(report.Issues) != 0 {
		t.Errorf("issues = %v, want none", report.Issues)
	}
}

func TestHealthDisconnected(t *testing.T) {
	h := &Hub{
		probes: map[string]*ProbeState{
			"a": {Name: "a", Connected: false},
		},
	}
	report := h.ComputeHealth()
	if report.Error != 1 {
		t.Errorf("error = %d, want 1", report.Error)
	}
	if len(report.Issues) != 1 || report.Issues[0].Check != "disconnected" {
		t.Errorf("issues = %v", report.Issues)
	}
}

func TestHealthHeightDrift(t *testing.T) {
	h := &Hub{
		probes: map[string]*ProbeState{
			"a": {Name: "a", Connected: true, LastUpdate: time.Now(), Height: "100", ChainID: "test3",
				Snapshot: &node.Snapshot{Status: &node.Status{NodeInfo: node.NodeInfo{Network: "test3"}}}},
			"b": {Name: "b", Connected: true, LastUpdate: time.Now(), Height: "90", ChainID: "test3",
				Snapshot: &node.Snapshot{Status: &node.Status{NodeInfo: node.NodeInfo{Network: "test3"}}}},
		},
	}
	report := h.ComputeHealth()
	found := false
	for _, issue := range report.Issues {
		if issue.Check == "height_drift" {
			found = true
		}
	}
	if !found {
		t.Error("expected height_drift issue")
	}
}

func TestHealthChainIDMismatch(t *testing.T) {
	h := &Hub{
		probes: map[string]*ProbeState{
			"a": {Name: "a", Connected: true, LastUpdate: time.Now(), Height: "100", ChainID: "test3",
				Snapshot: &node.Snapshot{Status: &node.Status{NodeInfo: node.NodeInfo{Network: "test3"}}}},
			"b": {Name: "b", Connected: true, LastUpdate: time.Now(), Height: "100", ChainID: "test4",
				Snapshot: &node.Snapshot{Status: &node.Status{NodeInfo: node.NodeInfo{Network: "test4"}}}},
		},
	}
	report := h.ComputeHealth()
	found := false
	for _, issue := range report.Issues {
		if issue.Check == "chain_id_mismatch" {
			found = true
		}
	}
	if !found {
		t.Error("expected chain_id_mismatch issue")
	}
	if report.Error != 2 {
		t.Errorf("error = %d, want 2", report.Error)
	}
}

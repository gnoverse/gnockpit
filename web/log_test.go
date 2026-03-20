package web

import (
	"strings"
	"testing"
)

func TestParseGnolandLogJSON(t *testing.T) {
	// ts=0 → 1970-01-01 epoch (deterministic)
	line := `{"level":"info","ts":0,"msg":"Starting multi","module":"proxy","impl":"multi"}`
	e := parseGnolandLog(line)

	if e.Time != "01-01 00:00:00.000" {
		t.Errorf("Time = %q, want %q", e.Time, "01-01 00:00:00.000")
	}
	if e.Level != "INFO" {
		t.Errorf("Level = %q, want INFO", e.Level)
	}
	if e.Module != "proxy" {
		t.Errorf("Module = %q, want proxy", e.Module)
	}
	if e.Msg != "Starting multi" {
		t.Errorf("Msg = %q, want 'Starting multi'", e.Msg)
	}
	if e.Extra == nil {
		t.Fatal("Extra should not be nil")
	}
	if _, ok := e.Extra["impl"]; !ok {
		t.Errorf("Extra missing 'impl' field: %v", e.Extra)
	}
	for k := range e.Extra {
		if k == "ts" || k == "level" || k == "msg" || k == "module" {
			t.Errorf("Extra contains core key %q", k)
		}
	}
}

func TestParseGnolandLogNonJSON(t *testing.T) {
	line := "plain text log line"
	e := parseGnolandLog(line)
	if e.Msg != line {
		t.Errorf("Msg = %q, want %q", e.Msg, line)
	}
	if e.Time != "" || e.Level != "" || e.Module != "" {
		t.Errorf("non-JSON should have empty Time/Level/Module, got %+v", e)
	}
	if e.Extra != nil {
		t.Errorf("non-JSON should have nil Extra, got %v", e.Extra)
	}
}

func TestParseGnolandLogEmptyModule(t *testing.T) {
	line := `{"level":"debug","ts":0,"msg":"hello"}`
	e := parseGnolandLog(line)
	if e.Module != "" {
		t.Errorf("Module = %q, want empty", e.Module)
	}
}

func TestParseGnolandLogPeerExtra(t *testing.T) {
	line := `{"level":"info","ts":0,"msg":"dial","module":"p2p","peer":"abc123@1.2.3.4:26656"}`
	e := parseGnolandLog(line)
	peer, ok := e.Extra["peer"].(string)
	if !ok {
		t.Fatalf("Extra['peer'] not a string: %T %v", e.Extra["peer"], e.Extra["peer"])
	}
	if !strings.Contains(peer, "@1.2.3.4:26656") {
		t.Errorf("peer = %q, missing expected address", peer)
	}
}

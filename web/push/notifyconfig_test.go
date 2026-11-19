package push

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseLead(t *testing.T) {
	for in, want := range map[string]Lead{
		"100 blocks": {Blocks: 100},
		"1 blocks":   {Blocks: 1},
		"1h":         {Time: time.Hour},
		"30m":        {Time: 30 * time.Minute},
		"90m":        {Time: 90 * time.Minute},
	} {
		got, err := ParseLead(in)
		if err != nil || got != want {
			t.Errorf("ParseLead(%q) = %+v, %v; want %+v", in, got, err, want)
		}
		if again, err := ParseLead(got.String()); err != nil || again != got {
			t.Errorf("ParseLead(%q.String() = %q) = %+v, %v; want it back", in, got.String(), again, err)
		}
	}
	for _, in := range []string{"", "0 blocks", "-5 blocks", "30s", "90s", "ten blocks", "5 minutes", "0m"} {
		if got, err := ParseLead(in); err == nil {
			t.Errorf("ParseLead(%q) = %+v, want an error", in, got)
		}
	}
}

func TestLeadMet(t *testing.T) {
	left := func(d time.Duration) func(int64) (time.Duration, bool) {
		return func(int64) (time.Duration, bool) { return d, true }
	}
	unknown := func(int64) (time.Duration, bool) { return 0, false }
	blocks, minutes := Lead{Blocks: 100}, Lead{Time: 30 * time.Minute}
	for _, tc := range []struct {
		name     string
		lead     Lead
		head     int64
		timeLeft func(int64) (time.Duration, bool)
		want     bool
	}{
		{"101 blocks left", blocks, 899, unknown, false},
		{"100 blocks left", blocks, 900, unknown, true},
		{"31 min left", minutes, 500, left(31 * time.Minute), false},
		{"30 min left", minutes, 500, left(30 * time.Minute), true},
		{"no estimate, not reached", minutes, 999, unknown, false},
		{"no estimate, reached", minutes, 1000, unknown, true},
		{"past it", blocks, 1200, unknown, true},
	} {
		if got := tc.lead.Met(1000, tc.head, tc.timeLeft); got != tc.want {
			t.Errorf("%s: Met = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestParseNotifyConfig(t *testing.T) {
	cfg, err := ParseNotifyConfig([]byte(`{"targets": [
	  {"url": "discord://token@id", "alerts": ["chain_stuck", "valset_changed", "halt_scheduled",
	    {"halt_approaching": "1h"}, {"validator_missing_blocks": ["g1abc"]}]},
	  {"url": "telegram://token@telegram?chats=@c", "alerts": [{"halt_approaching": "100 blocks"}, "validator_missing_blocks"]}
	]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := NotifyConfig{Targets: []NotifyRoute{
		{URL: "discord://token@id", Alerts: map[AlertType]AlertOptions{
			AlertChainStuck: {}, AlertValsetChanged: {}, AlertHaltScheduled: {},
			AlertHaltApproaching: {Lead: Lead{Time: time.Hour}}, AlertValidatorMissingBlocks: {Validators: []string{"g1abc"}},
		}},
		{URL: "telegram://token@telegram?chats=@c", Alerts: map[AlertType]AlertOptions{
			AlertHaltApproaching: {Lead: Lead{Blocks: 100}}, AlertValidatorMissingBlocks: {},
		}},
	}}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("ParseNotifyConfig = %+v, want %+v", cfg, want)
	}
	if empty, err := ParseNotifyConfig([]byte(`{"targets": []}`)); err != nil || len(empty.Targets) != 0 {
		t.Errorf("no targets: %+v, %v; want none, no error", empty, err)
	}
}

func TestParseNotifyConfigRejects(t *testing.T) {
	for _, tc := range []struct{ name, file, want string }{
		{"unknown type", `{"targets": [{"url": "x://y", "alerts": ["node_down"]}]}`, `unknown alert "node_down"`},
		{"reminder", `{"targets": [{"url": "x://y", "alerts": ["eta_reminder"]}]}`, `unknown alert "eta_reminder"`},
		{"no alerts", `{"targets": [{"url": "x://y", "alerts": []}]}`, "lists no alerts"},
		{"twice", `{"targets": [{"url": "x://y", "alerts": ["chain_stuck", "chain_stuck"]}]}`, `"chain_stuck" listed twice`},
		{"no lead", `{"targets": [{"url": "x://y", "alerts": ["halt_approaching"]}]}`, "halt_approaching needs a lead"},
		{"options", `{"targets": [{"url": "x://y", "alerts": [{"chain_stuck": "1h"}]}]}`, "chain_stuck takes no options"},
		{"bad lead", `{"targets": [{"url": "x://y", "alerts": [{"halt_approaching": "soon"}]}]}`, "halt_approaching"},
		{"no validators", `{"targets": [{"url": "x://y", "alerts": [{"validator_missing_blocks": []}]}]}`, "at least one validator"},
		{"two keys", `{"targets": [{"url": "x://y", "alerts": [{"chain_stuck": 1, "valset_changed": 1}]}]}`, "one alert per entry"},
		{"no url", `{"targets": [{"alerts": ["chain_stuck"]}]}`, "target 1: no url"},
		{"unknown field", `{"targets": [], "urls": []}`, "unknown field"},
		{"not json", `{"targets": [`, "unexpected EOF"},
	} {
		_, err := ParseNotifyConfig([]byte(tc.file))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one naming %q", tc.name, err, tc.want)
		}
	}
}

func TestRouteWants(t *testing.T) {
	route := NotifyRoute{URL: "x://y", Alerts: map[AlertType]AlertOptions{
		AlertValidatorMissingBlocks: {Validators: []string{"g1a"}}, AlertChainStuck: {},
	}}
	for _, tc := range []struct {
		alert Alert
		want  bool
	}{
		{Alert{Type: AlertValidatorMissingBlocks, EntityID: "g1a"}, true},
		{Alert{Type: AlertValidatorMissingBlocks, EntityID: "g1b"}, false},
		{Alert{Type: AlertChainStuck, Firing: false}, true},
		{Alert{Type: AlertValsetChanged}, false},
	} {
		if got := route.Wants(tc.alert); got != tc.want {
			t.Errorf("Wants(%+v) = %v, want %v", tc.alert, got, tc.want)
		}
	}
	every := NotifyRoute{Alerts: map[AlertType]AlertOptions{AlertValidatorMissingBlocks: {}}}
	if !every.Wants(Alert{Type: AlertValidatorMissingBlocks, EntityID: "g1z"}) {
		t.Error("a route naming validator_missing_blocks alone does not want every validator's")
	}
}

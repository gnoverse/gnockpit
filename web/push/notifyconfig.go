package push

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Lead is how far ahead of a height a lead-time alert fires: Blocks before
// it, or Time before its estimated time. Exactly one is set.
type Lead struct {
	Blocks int64
	Time   time.Duration
}

// ParseLead reads a lead: "<N> blocks" (N at least 1), or a duration of whole
// minutes, at least one ("30m", "1h", "1h30m").
func ParseLead(s string) (Lead, error) {
	if n, ok := strings.CutSuffix(s, " blocks"); ok {
		blocks, err := strconv.ParseInt(n, 10, 64)
		if err != nil || blocks < 1 {
			return Lead{}, fmt.Errorf("lead %q: want at least 1 block", s)
		}
		return Lead{Blocks: blocks}, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < time.Minute || d%time.Minute != 0 {
		return Lead{}, fmt.Errorf("lead %q: want \"<N> blocks\" or whole minutes, such as \"30m\" or \"1h\"", s)
	}
	return Lead{Time: d}, nil
}

// String writes the lead as ParseLead reads it: "100 blocks", "30m", "1h".
func (l Lead) String() string {
	switch {
	case l.Blocks > 0:
		return strconv.FormatInt(l.Blocks, 10) + " blocks"
	case l.Time%time.Hour == 0:
		return strconv.FormatInt(int64(l.Time/time.Hour), 10) + "h"
	}
	return strconv.FormatInt(int64(l.Time/time.Minute), 10) + "m"
}

// Met reports whether target is within the lead of head: by the blocks left,
// or by the estimated time left. A target reached meets any lead, so a lead
// in time fires even when no estimate is known.
func (l Lead) Met(target, head int64, timeLeft func(height int64) (time.Duration, bool)) bool {
	if head >= target {
		return true
	}
	if l.Blocks > 0 {
		return target-head <= l.Blocks
	}
	left, ok := timeLeft(target)
	return ok && left <= l.Time
}

// NotifyConfig is the notification config file: the Shoutrrr targets and the
// alerts each receives.
type NotifyConfig struct {
	Targets []NotifyRoute
}

// NotifyRoute is one target and the alerts it receives, with their options.
type NotifyRoute struct {
	URL    string
	Alerts map[AlertType]AlertOptions
}

// AlertOptions tunes an alert for a target.
type AlertOptions struct {
	Validators []string // validator_missing_blocks: those followed; nil: every validator
	Lead       Lead     // halt_approaching: how far ahead of the halt
}

// Wants reports whether the route receives a, firing or recovering.
func (r NotifyRoute) Wants(a Alert) bool {
	opts, ok := r.Alerts[a.Type]
	if !ok {
		return false
	}
	if a.Type == AlertValidatorMissingBlocks && opts.Validators != nil {
		return slices.Contains(opts.Validators, a.EntityID)
	}
	return true
}

// routableAlerts are the alerts a target can receive; the ETA reminder goes
// only to the device that set it.
var routableAlerts = []AlertType{AlertChainStuck, AlertValidatorMissingBlocks, AlertValsetChanged, AlertHaltScheduled, AlertHaltApproaching}

// ParseNotifyConfig reads a notification config file:
//
//	{"targets": [{"url": "discord://…", "alerts": ["chain_stuck",
//	  {"halt_approaching": "1h"}, {"validator_missing_blocks": ["g1…"]}]}]}
func ParseNotifyConfig(data []byte) (NotifyConfig, error) {
	var file struct {
		Targets []struct {
			URL    string            `json:"url"`
			Alerts []json.RawMessage `json:"alerts"`
		} `json:"targets"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return NotifyConfig{}, fmt.Errorf("notify config: %w", err)
	}
	cfg := NotifyConfig{Targets: make([]NotifyRoute, 0, len(file.Targets))}
	for i, t := range file.Targets {
		route, err := parseRoute(t.URL, t.Alerts)
		if err != nil {
			return NotifyConfig{}, fmt.Errorf("notify config: target %d: %w", i+1, err)
		}
		cfg.Targets = append(cfg.Targets, route)
	}
	return cfg, nil
}

func parseRoute(url string, alerts []json.RawMessage) (NotifyRoute, error) {
	if url == "" {
		return NotifyRoute{}, errors.New("no url")
	}
	if len(alerts) == 0 {
		return NotifyRoute{}, errors.New("lists no alerts")
	}
	route := NotifyRoute{URL: url, Alerts: make(map[AlertType]AlertOptions, len(alerts))}
	for _, raw := range alerts {
		typ, opts, err := parseAlert(raw)
		if err != nil {
			return NotifyRoute{}, err
		}
		if _, dup := route.Alerts[typ]; dup {
			return NotifyRoute{}, fmt.Errorf("%q listed twice", typ)
		}
		route.Alerts[typ] = opts
	}
	return route, nil
}

// parseAlert reads an alert entry: its name alone, or {name: options}.
func parseAlert(raw json.RawMessage) (AlertType, AlertOptions, error) {
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		typ, err := routable(name)
		if err == nil && typ == AlertHaltApproaching {
			err = errors.New(`halt_approaching needs a lead, such as {"halt_approaching": "1h"}`)
		}
		return typ, AlertOptions{}, err
	}
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entry); err != nil || len(entry) != 1 {
		return "", AlertOptions{}, fmt.Errorf("alert %s: want a name or one alert per entry, {name: options}", raw)
	}
	var value json.RawMessage
	for name, value = range entry {
	}
	typ, err := routable(name)
	if err != nil {
		return "", AlertOptions{}, err
	}
	opts, err := parseOptions(typ, value)
	return typ, opts, err
}

func parseOptions(typ AlertType, value json.RawMessage) (AlertOptions, error) {
	switch typ {
	case AlertHaltApproaching:
		var s string
		if err := json.Unmarshal(value, &s); err != nil {
			return AlertOptions{}, fmt.Errorf("halt_approaching: want a lead, such as \"1h\" or \"100 blocks\": %w", err)
		}
		lead, err := ParseLead(s)
		if err != nil {
			return AlertOptions{}, fmt.Errorf("halt_approaching: %w", err)
		}
		return AlertOptions{Lead: lead}, nil
	case AlertValidatorMissingBlocks:
		var validators []string
		if err := json.Unmarshal(value, &validators); err != nil || len(validators) == 0 {
			return AlertOptions{}, errors.New("validator_missing_blocks: list at least one validator address, or name the alert alone for every validator")
		}
		return AlertOptions{Validators: validators}, nil
	}
	return AlertOptions{}, fmt.Errorf("%s takes no options", typ)
}

func routable(name string) (AlertType, error) {
	if !slices.Contains(routableAlerts, AlertType(name)) {
		return "", fmt.Errorf("unknown alert %q", name)
	}
	return AlertType(name), nil
}

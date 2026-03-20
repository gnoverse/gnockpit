package web

import (
	"encoding/json"
	"strings"
	"time"
)

// LogEntry is a parsed gnoland JSON log line, sent to the browser as a structured object.
type LogEntry struct {
	Time   string                 `json:"time"`
	Level  string                 `json:"level"`
	Module string                 `json:"module,omitempty"`
	Msg    string                 `json:"msg"`
	Extra  map[string]interface{} `json:"extra,omitempty"`
}

var logCoreKeys = map[string]bool{
	"ts": true, "level": true, "msg": true, "module": true, "caller": true,
}

// parseGnolandLog parses a gnoland JSON log line into a LogEntry.
// Non-JSON lines (docker daemon messages, journalctl separators, etc.) are returned
// as LogEntry{Msg: line} with all other fields empty.
func parseGnolandLog(line string) LogEntry {
	if line == "" || line[0] != '{' {
		return LogEntry{Msg: line}
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return LogEntry{Msg: line}
	}

	str := func(key string) string {
		v, ok := raw[key]
		if !ok {
			return ""
		}
		var s string
		json.Unmarshal(v, &s) //nolint:errcheck — zero value on error is the correct fallback
		return s
	}

	var ts float64
	if v, ok := raw["ts"]; ok {
		json.Unmarshal(v, &ts) //nolint:errcheck
	}
	sec := int64(ts)
	nsec := int64((ts - float64(sec)) * 1e9)
	t := time.Unix(sec, nsec).UTC().Format("01-02 15:04:05.000")

	// Extra: all fields except the core ones.
	var full map[string]interface{}
	json.Unmarshal([]byte(line), &full) //nolint:errcheck
	var extra map[string]interface{}
	for k, v := range full {
		if !logCoreKeys[k] {
			if extra == nil {
				extra = make(map[string]interface{})
			}
			extra[k] = v
		}
	}

	return LogEntry{
		Time:   t,
		Level:  strings.ToUpper(str("level")),
		Module: str("module"),
		Msg:    str("msg"),
		Extra:  extra,
	}
}

package hub

import (
	"fmt"
	"sort"
	"strconv"
	"time"
)

// HealthIssue is a single detected problem across the cluster.
type HealthIssue struct {
	Severity string   `json:"severity"` // "error", "warn", "ok"
	Check    string   `json:"check"`
	Probes   []string `json:"probes,omitempty"`
	Detail   string   `json:"detail"`
}

// HealthReport summarizes cluster health.
type HealthReport struct {
	Issues    []HealthIssue `json:"issues"`
	Timestamp string        `json:"timestamp"`
	Total     int           `json:"total_probes"`
	Healthy   int           `json:"healthy"`
	Warning   int           `json:"warning"`
	Error     int           `json:"error"`
}

// ComputeHealth evaluates all probes and returns a health report.
func (h *Hub) ComputeHealth() *HealthReport {
	h.mu.RLock()
	probes := make(map[string]*ProbeState, len(h.probes))
	for k, v := range h.probes {
		cp := *v
		probes[k] = &cp
	}
	h.mu.RUnlock()

	now := time.Now()
	report := &HealthReport{
		Timestamp: now.UTC().Format(time.RFC3339),
		Total:     len(probes),
	}

	if len(probes) == 0 {
		return report
	}

	// Collect heights
	heights := map[string]int{}
	maxHeight := 0
	chainIDs := map[string][]string{}
	versions := map[string][]string{}
	valCounts := map[int][]string{}
	probeIssues := map[string]string{} // probe -> worst severity

	for name, ps := range probes {
		// Disconnected
		if !ps.Connected {
			report.Issues = append(report.Issues, HealthIssue{
				Severity: "error",
				Check:    "disconnected",
				Probes:   []string{name},
				Detail:   "Probe WebSocket disconnected",
			})
			probeIssues[name] = "error"
			continue
		}

		// Stale (>30s since last update)
		if !ps.LastUpdate.IsZero() && now.Sub(ps.LastUpdate) > 30*time.Second {
			report.Issues = append(report.Issues, HealthIssue{
				Severity: "warn",
				Check:    "stale",
				Probes:   []string{name},
				Detail:   fmt.Sprintf("No data for %s", now.Sub(ps.LastUpdate).Truncate(time.Second)),
			})
			worstSeverity(probeIssues, name, "warn")
		}

		if ps.Snapshot == nil {
			continue
		}

		// Parse height
		if ps.Height != "" {
			h, _ := strconv.Atoi(ps.Height)
			heights[name] = h
			if h > maxHeight {
				maxHeight = h
			}
		}

		// Collect chain IDs
		if ps.ChainID != "" {
			chainIDs[ps.ChainID] = append(chainIDs[ps.ChainID], name)
		}

		// Collect versions
		if ps.Snapshot.Status != nil {
			v := ps.Snapshot.Status.NodeInfo.Version
			if v != "" {
				versions[v] = append(versions[v], name)
			}
		}

		// Collect validator counts
		if ps.Snapshot.Consensus != nil {
			vc := len(ps.Snapshot.Consensus.Votes)
			valCounts[vc] = append(valCounts[vc], name)
		}
	}

	// Height drift (>5 blocks behind max)
	for name, h := range heights {
		if maxHeight-h > 5 {
			report.Issues = append(report.Issues, HealthIssue{
				Severity: "warn",
				Check:    "height_drift",
				Probes:   []string{name},
				Detail:   fmt.Sprintf("Height %d, cluster max %d (-%d)", h, maxHeight, maxHeight-h),
			})
			worstSeverity(probeIssues, name, "warn")
		}
	}

	// Chain ID mismatch
	if len(chainIDs) > 1 {
		var detail string
		for cid, names := range chainIDs {
			detail += fmt.Sprintf("%s: %v; ", cid, names)
		}
		allProbes := []string{}
		for _, names := range chainIDs {
			allProbes = append(allProbes, names...)
		}
		report.Issues = append(report.Issues, HealthIssue{
			Severity: "error",
			Check:    "chain_id_mismatch",
			Probes:   allProbes,
			Detail:   "Different chain IDs: " + detail,
		})
		for _, name := range allProbes {
			worstSeverity(probeIssues, name, "error")
		}
	}

	// Version mismatch
	if len(versions) > 1 {
		var detail string
		for v, names := range versions {
			detail += fmt.Sprintf("%s: %v; ", v, names)
		}
		allProbes := []string{}
		for _, names := range versions {
			allProbes = append(allProbes, names...)
		}
		report.Issues = append(report.Issues, HealthIssue{
			Severity: "warn",
			Check:    "version_mismatch",
			Probes:   allProbes,
			Detail:   "Different versions: " + detail,
		})
		for _, name := range allProbes {
			worstSeverity(probeIssues, name, "warn")
		}
	}

	// Validator set divergence
	if len(valCounts) > 1 {
		var detail string
		for vc, names := range valCounts {
			detail += fmt.Sprintf("%d validators: %v; ", vc, names)
		}
		allProbes := []string{}
		for _, names := range valCounts {
			allProbes = append(allProbes, names...)
		}
		report.Issues = append(report.Issues, HealthIssue{
			Severity: "error",
			Check:    "validator_set_divergence",
			Probes:   allProbes,
			Detail:   "Different validator counts: " + detail,
		})
		for _, name := range allProbes {
			worstSeverity(probeIssues, name, "error")
		}
	}

	// Consensus round mismatch (probes at same height but different rounds)
	roundsByHeight := map[int]map[string][]string{} // height -> round -> probes
	for name, ps := range probes {
		if ps.Snapshot == nil || ps.Snapshot.Consensus == nil {
			continue
		}
		h, _ := strconv.Atoi(ps.Snapshot.Consensus.Height)
		r := ps.Snapshot.Consensus.Round
		if h == 0 || r == "" {
			continue
		}
		if roundsByHeight[h] == nil {
			roundsByHeight[h] = make(map[string][]string)
		}
		roundsByHeight[h][r] = append(roundsByHeight[h][r], name)
	}
	for h, rounds := range roundsByHeight {
		if len(rounds) > 1 {
			var detail string
			allProbes := []string{}
			for r, names := range rounds {
				detail += fmt.Sprintf("round %s: %v; ", r, names)
				allProbes = append(allProbes, names...)
			}
			report.Issues = append(report.Issues, HealthIssue{
				Severity: "warn",
				Check:    "consensus_mismatch",
				Probes:   allProbes,
				Detail:   fmt.Sprintf("Height %d, different rounds: %s", h, detail),
			})
			for _, name := range allProbes {
				worstSeverity(probeIssues, name, "warn")
			}
		}
	}

	// Sort issues by severity (error first)
	sort.Slice(report.Issues, func(i, j int) bool {
		return severityRank(report.Issues[i].Severity) > severityRank(report.Issues[j].Severity)
	})

	// Count healthy/warning/error
	for name := range probes {
		switch probeIssues[name] {
		case "error":
			report.Error++
		case "warn":
			report.Warning++
		default:
			report.Healthy++
		}
	}

	return report
}

func worstSeverity(m map[string]string, name, severity string) {
	cur := m[name]
	if severityRank(severity) > severityRank(cur) {
		m[name] = severity
	}
}

func severityRank(s string) int {
	switch s {
	case "error":
		return 2
	case "warn":
		return 1
	default:
		return 0
	}
}

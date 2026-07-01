package web

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"time"

	"github.com/gnoverse/gnockpit/node"
)

// StatusInfo is gnockpit's own assessment of the monitored chain/node health,
// exposed for external badges/integrations.
type StatusInfo struct {
	Status string `json:"status"` // operational | degraded | down
	Chain  string `json:"chain,omitempty"`
	Height string `json:"height,omitempty"`
	Reason string `json:"reason,omitempty"`
	Time   string `json:"time"`
}

// deriveStatus computes health from the latest snapshot, statelessly: an
// unreachable node or a chain that hasn't produced a block within
// chainStuckSecs is "down"; a node still catching up is "degraded".
func deriveStatus(snap *node.Snapshot, chainStuckSecs int, now time.Time) StatusInfo {
	si := StatusInfo{Time: now.UTC().Format(time.RFC3339)}
	if snap == nil || snap.Status == nil || snap.Error != "" {
		si.Status = "down"
		si.Reason = "node unreachable"
		return si
	}
	si.Chain = snap.Status.NodeInfo.Network
	si.Height = snap.Status.SyncInfo.LatestBlockHeight
	if t, err := time.Parse(time.RFC3339Nano, snap.Status.SyncInfo.LatestBlockTime); err == nil {
		if now.Sub(t) > time.Duration(chainStuckSecs)*time.Second {
			si.Status = "down"
			si.Reason = "chain stuck"
			return si
		}
	} else if snap.Status.SyncInfo.LatestBlockTime != "" {
		// Reachable node but its block time doesn't parse — can't confirm the
		// chain is live, so don't claim operational.
		si.Status = "degraded"
		si.Reason = "unparseable block time"
		return si
	}
	if snap.Status.SyncInfo.CatchingUp {
		si.Status = "degraded"
		si.Reason = "node catching up"
		return si
	}
	si.Status = "operational"
	return si
}

// handleStatus serves gnockpit's own health as JSON. CORS-open so third parties
// can build their own badges/dashboards from it.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	si := deriveStatus(s.getSnapshot(), s.ChainStuckSecs, time.Now())
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(si)
}

// handleBadge serves a gnockpit-styled SVG status badge.
func (s *Server) handleBadge(w http.ResponseWriter, r *http.Request) {
	si := deriveStatus(s.getSnapshot(), s.ChainStuckSecs, time.Now())
	label := si.Chain
	if label == "" {
		label = "gnockpit"
	}
	w.Header().Set("Content-Type", "image/svg+xml;charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	fmt.Fprint(w, statusBadgeSVG(label, si.Status))
}

func badgeColor(status string) string {
	switch status {
	case "operational":
		return "#3fb950"
	case "degraded":
		return "#d29922"
	case "down":
		return "#f85149"
	default:
		return "#8b949e"
	}
}

// statusBadgeSVG renders a small dark badge: "<label>  ● <status>", with the
// dot colored by status — matching gnockpit's palette.
func statusBadgeSVG(label, status string) string {
	const charW = 6.5
	lw := charW * float64(len([]rune(label)))
	sw := charW * float64(len([]rune(status)))
	dotCx := 8 + lw + 12      // 8 left pad + label + 8 gap + 4 radius
	statusX := dotCx + 8      // 4 radius + 4 gap
	total := statusX + sw + 8 // + right pad
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="20" role="img" aria-label="%s: %s">`+
		`<rect x="0.5" y="0.5" width="%.0f" height="19" rx="4" fill="#21262d" stroke="#30363d"/>`+
		`<g font-family="Verdana,DejaVu Sans,sans-serif" font-size="11" fill="#c9d1d9">`+
		`<text x="8" y="14">%s</text>`+
		`<circle cx="%.1f" cy="10" r="4" fill="%s"/>`+
		`<text x="%.1f" y="14">%s</text>`+
		`</g></svg>`,
		total, html.EscapeString(label), html.EscapeString(status),
		total-1, html.EscapeString(label), dotCx, badgeColor(status), statusX, html.EscapeString(status))
}

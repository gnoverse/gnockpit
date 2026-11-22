package web

import (
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"strconv"
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

// NetworkState is the live consensus state in the public status payload.
type NetworkState struct {
	Round      string `json:"round,omitempty"`
	Step       string `json:"step,omitempty"`
	Proposer   string `json:"proposer,omitempty"`
	CatchingUp bool   `json:"catching_up"`
}

// nodeLocation is where a node runs, resolved from its public IP: geolocation
// and cloud provider. Embedded in StatusPeer and StatusValidator, its fields
// serialize inline; unknown ones are omitted.
type nodeLocation struct {
	Country  string   `json:"country,omitempty"`
	City     string   `json:"city,omitempty"`
	Lat      *float64 `json:"lat,omitempty"`
	Lon      *float64 `json:"lon,omitempty"`
	Provider string   `json:"provider,omitempty"`
}

// locationOf returns the location resolved for peer p.
func locationOf(p node.Peer) nodeLocation {
	return nodeLocation{Country: p.Country, City: p.City, Lat: p.Lat, Lon: p.Lon, Provider: p.Provider}
}

// StatusPeer is a peer's column data in the public status payload. Its
// location fields come from the embedded nodeLocation.
type StatusPeer struct {
	Name   string `json:"name,omitempty"`
	NodeID string `json:"node_id,omitempty"`
	nodeLocation
	NPeers    int  `json:"n_peers,omitempty"`
	Reachable bool `json:"reachable"`
}

// StatusValidator is a validator's column data in the public status payload.
// Its location fields come from the embedded nodeLocation, copied from the
// peer matched to its address.
type StatusValidator struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
	nodeLocation
	VotingPower string `json:"voting_power,omitempty"`
	// SPOF tells whether losing it drops the rest below quorum; nil while
	// the total voting power is unknown.
	SPOF      *bool `json:"spof,omitempty"`
	Inactive  bool  `json:"inactive,omitempty"`
	Missed100 int   `json:"missed_100"`
	Missed24h int   `json:"missed_24h"`
	// Nil100 and OtherBlock100 split the last 100: nil votes (signed) and votes
	// for another block (counted in Missed100).
	Nil100        int `json:"nil_100"`
	OtherBlock100 int `json:"other_block_100"`
	// VoteDelayMs is the median delay of its precommits in the last 100 from
	// each commit's weighted median time; null without one.
	VoteDelayMs          *int `json:"vote_delay_ms"`
	MissedProposals24h   int  `json:"missed_proposals_24h"`
	ExpectedProposals24h int  `json:"expected_proposals_24h"`
}

// StatusReport is the public /api/status payload: the retrocompat health summary
// (embedded, so status/chain/height/reason/time stay top-level) plus curated
// network state, the recent-block window, and per-node column data.
type StatusReport struct {
	StatusInfo
	Network      *NetworkState     `json:"network,omitempty"`
	RecentBlocks []node.BlockInfo  `json:"recent_blocks,omitempty"`
	Peers        []StatusPeer      `json:"peers,omitempty"`
	Validators   []StatusValidator `json:"validators,omitempty"`
}

// isSPOF reports whether a validator holding vp of totalVP is a single point of
// failure: losing it drops the remaining power below the 2/3+1 quorum.
func isSPOF(vp, totalVP int) bool {
	if totalVP <= 0 {
		return false
	}
	bft := (totalVP*2)/3 + 1
	return totalVP-vp < bft
}

// peersByValAddr indexes peers by the validator address they've been correlated
// to, for looking up a validator's location.
func peersByValAddr(peers []node.Peer) map[string]node.Peer {
	m := make(map[string]node.Peer)
	for _, p := range peers {
		if p.ValAddress != "" {
			m[p.ValAddress] = p
		}
	}
	return m
}

// buildStatusReport assembles the public /api/status payload from the latest
// snapshot, reusing buildVotesReport for the per-validator signing/perf figures.
func (s *Server) buildStatusReport(snap *node.Snapshot, now time.Time) StatusReport {
	rep := StatusReport{StatusInfo: deriveStatus(snap, s.ChainStuckSecs, now)}
	if snap == nil {
		return rep
	}
	if snap.Signing != nil {
		rep.RecentBlocks = snap.Signing.RecentBlocks
	}
	rep.Peers = make([]StatusPeer, 0, len(snap.Peers))
	for _, p := range snap.Peers {
		rep.Peers = append(rep.Peers, StatusPeer{
			Name:         p.Moniker,
			NodeID:       p.NodeID,
			nodeLocation: locationOf(p),
			NPeers:       p.NPeers,
			Reachable:    p.RPCURL != "",
		})
	}
	votes := s.buildVotesReport(snap)
	if votes != nil {
		rep.Network = &NetworkState{Round: votes.Round, Step: votes.Step, Proposer: votes.Proposer}
		if snap.Status != nil {
			rep.Network.CatchingUp = snap.Status.SyncInfo.CatchingUp
		}
		byAddr := peersByValAddr(snap.Peers)
		// Voting power as the payload lists it, each validator's from the
		// set, parsed once, by its place. While one is unknown (unreadable,
		// or a validator out of the set) so is the total, and so is each
		// validator's spof.
		vps := make([]int, len(votes.Validators))
		totalVP, totalKnown := 0, true
		for i, vi := range votes.Validators {
			vp, err := strconv.Atoi(vi.VotingPower)
			if err != nil {
				totalKnown = false
				break
			}
			vps[i] = vp
			totalVP += vp
		}
		rep.Validators = make([]StatusValidator, 0, len(votes.Validators))
		for i, vi := range votes.Validators {
			sv := StatusValidator{
				Name:                 vi.Name,
				Address:              vi.Address,
				VotingPower:          vi.VotingPower,
				Inactive:             vi.Inactive,
				Missed100:            vi.Missed100,
				Missed24h:            vi.Missed24h,
				Nil100:               vi.Nil100,
				OtherBlock100:        vi.OtherBlock100,
				VoteDelayMs:          vi.VoteDelayMs,
				MissedProposals24h:   vi.MissedProposals24h,
				ExpectedProposals24h: vi.ExpectedProposals24h,
			}
			if totalKnown {
				spof := isSPOF(vps[i], totalVP)
				sv.SPOF = &spof
			}
			if p, ok := byAddr[vi.Address]; ok {
				sv.nodeLocation = locationOf(p)
			}
			rep.Validators = append(rep.Validators, sv)
		}
	}
	return rep
}

// handleStatus serves gnockpit's own health plus curated network state, recent
// blocks, and per-node column data as JSON. The top-level status/chain/height/
// reason/time fields are retained for retrocompat. CORS-open so third parties
// can build their own badges/dashboards from it.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	rep := s.buildStatusReport(s.getSnapshot(), s.now())
	body, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		log.Printf("status: encode: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(body)
}

// handleBadge serves a gnockpit-styled SVG status badge.
func (s *Server) handleBadge(w http.ResponseWriter, r *http.Request) {
	si := deriveStatus(s.getSnapshot(), s.ChainStuckSecs, s.now())
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

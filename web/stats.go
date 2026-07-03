package web

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gnoverse/gnockpit/history"
	"github.com/gnoverse/gnockpit/node"
)

// window24h is the label of the 24h window, referenced when summing per-provider
// missed blocks. Kept as a constant so it stays in sync with statsWindows.
const window24h = "24h"

// statsWindows are the time windows reported per validator, in output order.
// A zero duration means "all recorded history".
var statsWindows = []struct {
	label string
	dur   time.Duration
}{
	{"1h", time.Hour},
	{window24h, 24 * time.Hour},
	{"7d", 7 * 24 * time.Hour},
	{"30d", 30 * 24 * time.Hour},
	{"total", 0},
}

// Stats is gnockpit's aggregate network statistics, exposed at /api/stats for
// external dashboards. It is derived from the latest snapshot plus recorded
// block-signing history.
type Stats struct {
	Time          string         `json:"time"`
	Chain         string         `json:"chain,omitempty"`
	Height        string         `json:"height,omitempty"`
	RecordedSince string         `json:"recorded_since,omitempty"` // how far back missed-block history reaches
	Validators    ValidatorStats `json:"validators"`
	Providers     []ProviderStat `json:"providers"`
	Countries     []CountryStat  `json:"countries"`
	Peers         PeerStats      `json:"peers"`
}

// ValidatorStats summarizes the validator set: health, decentralization, and
// per-validator missed-block counts across the reported windows.
type ValidatorStats struct {
	Total               int               `json:"total"`
	Active              int               `json:"active"`        // below the missed-blocks threshold
	BFTThreshold        int               `json:"bft_threshold"` // validators needed for consensus
	Margin              int               `json:"margin"`        // active - threshold
	NakamotoCoefficient int               `json:"nakamoto_coefficient"`
	WindowBlocks        map[string]int    `json:"window_blocks"` // window label -> blocks recorded
	Missed              []ValidatorMissed `json:"missed"`
}

// ValidatorMissed holds one validator's missed-block count per window.
type ValidatorMissed struct {
	Address string         `json:"address"`
	Name    string         `json:"name,omitempty"`
	Missed  map[string]int `json:"missed"` // window label -> missed count
}

// ProviderStat aggregates peers and (known) validators hosted on one cloud
// provider. Validator figures cover only validators this node is peered with.
type ProviderStat struct {
	Provider   string `json:"provider"`
	Peers      int    `json:"peers"`
	Validators int    `json:"validators"`
	Missed24h  int    `json:"missed_24h"`
}

// CountryStat aggregates peers and (known) validators in one country.
type CountryStat struct {
	Country    string `json:"country"`
	Peers      int    `json:"peers"`
	Validators int    `json:"validators"`
}

// PeerStats summarizes connected peers.
type PeerStats struct {
	Total     int `json:"total"`
	Reachable int `json:"reachable"` // peers whose RPC answered
}

// nakamotoCoefficient returns the minimum number of validators whose combined
// voting power exceeds one third of the total — the count that could halt
// consensus. Returns 0 when no voting power is known.
func nakamotoCoefficient(vals []node.Validator) int {
	powers := make([]int, 0, len(vals))
	total := 0
	for _, v := range vals {
		p, err := strconv.Atoi(v.VotingPower)
		if err != nil || p <= 0 {
			continue
		}
		powers = append(powers, p)
		total += p
	}
	if total == 0 {
		return 0
	}
	sort.Sort(sort.Reverse(sort.IntSlice(powers)))
	threshold := total / 3
	cum, count := 0, 0
	for _, p := range powers {
		cum += p
		count++
		if cum > threshold {
			break
		}
	}
	return count
}

// buildStats assembles the /api/stats payload from the latest snapshot and the
// signing-history store (which may be nil when history is disabled).
func buildStats(ctx context.Context, snap *node.Snapshot, hist *history.Store, now time.Time) Stats {
	st := Stats{Time: now.UTC().Format(time.RFC3339)}
	if snap == nil {
		return st
	}
	if snap.Status != nil {
		st.Chain = snap.Status.NodeInfo.Network
		st.Height = snap.Status.SyncInfo.LatestBlockHeight
	}

	vs := ValidatorStats{
		Total:               len(snap.Validators),
		NakamotoCoefficient: nakamotoCoefficient(snap.Validators),
		WindowBlocks:        make(map[string]int, len(statsWindows)),
	}
	if snap.Signing != nil {
		vs.Active = snap.Signing.ActiveCount
		vs.BFTThreshold = snap.Signing.BFTThreshold
		vs.Margin = snap.Signing.Margin
		if snap.Signing.TotalCount > 0 {
			vs.Total = snap.Signing.TotalCount
		}
	}

	// Missed-block windows from history.
	durs := make([]time.Duration, len(statsWindows))
	for i, w := range statsWindows {
		durs[i] = w.dur
	}
	var rep history.WindowReport
	if hist != nil {
		if r, err := hist.MissedByWindows(ctx, durs, now); err != nil {
			log.Printf("stats: missed by windows: %v", err)
		} else {
			rep = r
			for i, w := range statsWindows {
				vs.WindowBlocks[w.label] = r.Blocks[i]
			}
		}
		if since, err := hist.EarliestRecorded(ctx); err != nil {
			log.Printf("stats: earliest recorded: %v", err)
		} else if !since.IsZero() {
			st.RecordedSince = since.UTC().Format(time.RFC3339)
		}
	}

	vs.Missed = make([]ValidatorMissed, 0, len(snap.Validators))
	missed24h := make(map[string]int, len(snap.Validators))
	for _, v := range snap.Validators {
		m := ValidatorMissed{Address: v.Address, Name: v.Name, Missed: make(map[string]int, len(statsWindows))}
		counts := rep.Missed[v.Address] // nil when the validator never missed
		for i, w := range statsWindows {
			n := 0
			if counts != nil {
				n = counts[i]
			}
			m.Missed[w.label] = n
			if w.label == window24h {
				missed24h[v.Address] = n
			}
		}
		vs.Missed = append(vs.Missed, m)
	}
	sort.Slice(vs.Missed, func(i, j int) bool {
		if a, b := vs.Missed[i].Missed["total"], vs.Missed[j].Missed["total"]; a != b {
			return a > b
		}
		return vs.Missed[i].Address < vs.Missed[j].Address
	})
	st.Validators = vs

	// Provider / country aggregates: peer counts are complete; validator counts
	// cover only validators this node is peered with (matched via ValAddress).
	peerByValAddr := make(map[string]node.Peer)
	for _, p := range snap.Peers {
		if p.ValAddress != "" {
			peerByValAddr[p.ValAddress] = p
		}
	}
	provAgg := make(map[string]*ProviderStat)
	ctryAgg := make(map[string]*CountryStat)
	reachable := 0
	for _, p := range snap.Peers {
		if p.RPCURL != "" {
			reachable++
		}
		if p.Provider != "" {
			prov(provAgg, p.Provider).Peers++
		}
		if p.Country != "" {
			ctry(ctryAgg, p.Country).Peers++
		}
	}
	for _, v := range snap.Validators {
		p, ok := peerByValAddr[v.Address]
		if !ok {
			continue
		}
		if p.Provider != "" {
			ps := prov(provAgg, p.Provider)
			ps.Validators++
			ps.Missed24h += missed24h[v.Address]
		}
		if p.Country != "" {
			ctry(ctryAgg, p.Country).Validators++
		}
	}

	st.Providers = make([]ProviderStat, 0, len(provAgg))
	for _, p := range provAgg {
		st.Providers = append(st.Providers, *p)
	}
	sort.Slice(st.Providers, func(i, j int) bool {
		if st.Providers[i].Peers != st.Providers[j].Peers {
			return st.Providers[i].Peers > st.Providers[j].Peers
		}
		return st.Providers[i].Provider < st.Providers[j].Provider
	})
	st.Countries = make([]CountryStat, 0, len(ctryAgg))
	for _, c := range ctryAgg {
		st.Countries = append(st.Countries, *c)
	}
	sort.Slice(st.Countries, func(i, j int) bool {
		if st.Countries[i].Peers != st.Countries[j].Peers {
			return st.Countries[i].Peers > st.Countries[j].Peers
		}
		return st.Countries[i].Country < st.Countries[j].Country
	})
	st.Peers = PeerStats{Total: len(snap.Peers), Reachable: reachable}
	return st
}

func prov(m map[string]*ProviderStat, name string) *ProviderStat {
	if m[name] == nil {
		m[name] = &ProviderStat{Provider: name}
	}
	return m[name]
}

func ctry(m map[string]*CountryStat, code string) *CountryStat {
	if m[code] == nil {
		m[code] = &CountryStat{Country: code}
	}
	return m[code]
}

// handleStats serves aggregate network statistics as JSON. CORS-open so third
// parties can build their own dashboards from it.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	st := buildStats(r.Context(), s.getSnapshot(), s.History, time.Now())
	body, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		log.Printf("stats: encode: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(body)
}

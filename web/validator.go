package web

import (
	"cmp"
	"context"
	"errors"
	"log"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/gnolang/gno/tm2/pkg/crypto"

	"github.com/gnoverse/gnockpit/blocks"
	"github.com/gnoverse/gnockpit/node"
)

// validatorReport is the /api/validator payload: what gnockpit knows of one
// validator, its operator's signing keys taken together.
type validatorReport struct {
	Chain            string              `json:"chain"`
	Address          string              `json:"address"`  // as asked
	Operator         string              `json:"operator"` // "" for a key no event or profile links to one
	Moniker          string              `json:"moniker"`
	Status           *validatorStatus    `json:"status,omitempty"` // omitted while the node's current set is unknown
	Keys             []validatorKey      `json:"keys"`
	Location         *validatorLocation  `json:"location,omitempty"` // omitted without a matched peer
	Profile          *validatorProfile   `json:"profile,omitempty"`
	Timeline         []timelineEntry     `json:"timeline"` // newest first
	VoteDelayEdgesMs []int64             `json:"vote_delay_edges_ms"`
	Days             []validatorDay      `json:"days"` // ascending
	Recoveries       []validatorRecovery `json:"recoveries"`
	Summary          validatorSummary    `json:"summary"`
	Live             validatorLive       `json:"live"`
}

// validatorStatus is the validator's place in the node's current set, and the
// height it holds it since: in the set, its latest join with no leave after
// (a key rotation is no leave); out of it, its latest leave. Since is omitted
// when that height is below the stored history.
type validatorStatus struct {
	InSet          bool    `json:"in_set"`
	SinceHeight    int64   `json:"since_height,omitempty"`
	SinceTime      string  `json:"since_time,omitempty"`
	SigningAddress string  `json:"signing_address,omitempty"` // its key in the set
	Power          int64   `json:"power"`
	Share          float64 `json:"share"` // of the set's total power
}

type validatorKey struct {
	Address string `json:"address"`
	From    int64  `json:"from"`         // 0: before the stored history
	To      int64  `json:"to,omitempty"` // omitted: still its key
}

type validatorLocation struct {
	Country  string `json:"country,omitempty"`
	City     string `json:"city,omitempty"`
	Provider string `json:"provider,omitempty"`
	Version  string `json:"version,omitempty"`
}

// validatorProfile is the operator's valopers profile; the state fields are
// omitted when the realm did not answer for them.
type validatorProfile struct {
	Moniker            string `json:"moniker"`
	Description        string `json:"description"`
	ServerType         string `json:"server_type"`
	Operator           string `json:"operator"`
	SigningAddress     string `json:"signing_address"`
	SigningPubKey      string `json:"signing_pubkey"`
	KeepRunning        *bool  `json:"keep_running,omitempty"`
	LastRotationHeight *int64 `json:"last_rotation_height,omitempty"`
}

// timelineEntry is one entry of a validator's timeline: its kind's fields
// are set, the other kinds' are nil.
type timelineEntry struct {
	Kind   string `json:"kind"` // recovered, outage, power, rotated, joined, left or genesis
	Height int64  `json:"height"`
	Time   string `json:"time"`
	*timelineRecovery
	*timelineOutage
	*timelinePower
	*timelineRotation
	*timelineMembership
}

// timelineRecovery is a recovered entry: Height is the first height signed
// after the restart at RestartHeight, Time the time that signature landed.
type timelineRecovery struct {
	RestartHeight int64 `json:"restart_height"`
	DurationMs    int64 `json:"duration_ms"`
}

// timelineOutage is an outage entry: Height is its first height.
type timelineOutage struct {
	To      int64  `json:"to"`
	Blocks  int64  `json:"blocks"`
	EndTime string `json:"end_time"`
	Ongoing bool   `json:"ongoing"`
}

type timelinePower struct {
	Before int64 `json:"before"`
	After  int64 `json:"after"`
}

type timelineRotation struct {
	OldAddress string `json:"old_address"`
	NewAddress string `json:"new_address"`
}

// timelineMembership is a joined, left or genesis entry: the power the key
// came in or went out with.
type timelineMembership struct {
	Power int64 `json:"power"`
}

// signingCounts are a validator's counters over a span of blocks: a nil
// vote signs.
type signingCounts struct {
	InSet             int64 `json:"in_set"`
	Signed            int64 `json:"signed"`
	Absent            int64 `json:"absent"`
	Nil               int64 `json:"nil"`
	OtherBlock        int64 `json:"other_block"`
	ProposalsExpected int64 `json:"proposals_expected"`
	ProposalsMissed   int64 `json:"proposals_missed"`
	Votes             int64 `json:"votes"`
	VoteDelayMeanMs   int64 `json:"vote_delay_mean_ms"` // 0 without votes
}

type validatorDay struct {
	Day string `json:"day"` // YYYY-MM-DD, UTC
	signingCounts
	VoteDelayBuckets []int64 `json:"vote_delay_buckets"` // by vote_delay_edges_ms
}

type validatorRecovery struct {
	RestartHeight int64  `json:"restart_height"` // b: block b+1 came more than blocks.RestartGap after block b
	RestartTime   string `json:"restart_time"`   // block b+1's
	SignedAt      int64  `json:"signed_at"`      // 0 while pending
	DurationMs    int64  `json:"duration_ms"`
	Pending       bool   `json:"pending"` // still absent at the newest block
}

// validatorSummary is the validator's raw figures over the scoring window:
// the 30 complete UTC days before today; recovery over the 180.
type validatorSummary struct {
	FirstDay string `json:"first_day"`
	LastDay  string `json:"last_day"`
	signingCounts
	Outages  int64                    `json:"outages"`
	Recovery validatorRecoverySummary `json:"recovery"`
}

type validatorRecoverySummary struct {
	Samples   int   `json:"samples"`
	MedianMs  int64 `json:"median_ms"`  // of the last 3; 0 without samples
	SlowestMs int64 `json:"slowest_ms"` // 0 without samples
}

// validatorLive is the live window's figures of the validator's key, as the
// validators table has them.
type validatorLive struct {
	VoteDelayMs          *int `json:"vote_delay_ms"` // null when the window holds none of its precommits
	Missed100            int  `json:"missed_100"`
	Nil100               int  `json:"nil_100"`
	OtherBlock100        int  `json:"other_block_100"`
	Missed24h            int  `json:"missed_24h"`
	MissedProposals24h   int  `json:"missed_proposals_24h"`
	ExpectedProposals24h int  `json:"expected_proposals_24h"`
}

// handleValidator serves what gnockpit knows of the validator ?val= names
// (its operator or one of its signing keys) as JSON, CORS-open.
func (s *Server) handleValidator(w http.ResponseWriter, r *http.Request) {
	rep, status, err := s.validatorReport(r.Context(), r.URL.Query().Get("val"), time.Now())
	if err != nil {
		serveJSON(w, "validator", status, errorBody{Error: err.Error()})
		return
	}
	serveJSON(w, "validator", status, rep)
}

func (s *Server) validatorReport(ctx context.Context, val string, now time.Time) (validatorReport, int, error) {
	if s.Blocks == nil || s.BlockFill == nil {
		return validatorReport{}, http.StatusServiceUnavailable, errors.New("block history disabled")
	}
	if _, err := crypto.AddressFromBech32(val); err != nil {
		return validatorReport{}, http.StatusBadRequest, errors.New("val must be a g1 address")
	}
	snap := s.getSnapshot()
	s.mu.RLock()
	valopers := s.valopers
	s.mu.RUnlock()
	operator, spans, found, err := s.resolveValidator(ctx, val, valopers, snap)
	if err != nil {
		log.Printf("validator %s: %v", val, err)
		return validatorReport{}, http.StatusInternalServerError, errors.New("internal error")
	}
	if !found {
		return validatorReport{}, http.StatusNotFound, errors.New("unknown validator")
	}
	rep, err := s.buildValidatorReport(ctx, val, operator, spans, valopers, snap, now)
	if err != nil {
		log.Printf("validator %s: %v", val, err)
		return validatorReport{}, http.StatusInternalServerError, errors.New("internal error")
	}
	return rep, http.StatusOK, nil
}

// resolveValidator finds the validator val names, in this order: the
// operator val is; the operator an event links val to as a signing key; the
// operator whose profile names val as its signing key; else val alone as an
// unlinked key, operator "", when a stored or the current set holds it.
// found is false when gnockpit knows nothing of val.
func (s *Server) resolveValidator(ctx context.Context, val string, valopers map[string]node.Valoper, snap *node.Snapshot) (operator string, spans []blocks.KeySpan, found bool, err error) {
	operatorKeys := func(op string) ([]blocks.KeySpan, error) {
		return s.Blocks.OperatorKeys(ctx, op, valopers[op].SigningAddress)
	}
	if spans, err = operatorKeys(val); err != nil {
		return "", nil, false, err
	}
	if _, isProfile := valopers[val]; isProfile || len(spans) > 0 {
		return val, spans, true, nil
	}
	if operator, err = s.Blocks.OperatorOf(ctx, val); err != nil {
		return "", nil, false, err
	}
	if operator == "" {
		for _, op := range slices.Sorted(maps.Keys(valopers)) {
			if valopers[op].SigningAddress == val {
				operator = op
				break
			}
		}
	}
	if operator != "" {
		if spans, err = operatorKeys(operator); err != nil {
			return "", nil, false, err
		}
		return operator, spans, true, nil
	}
	known, err := s.Blocks.Known(ctx, val)
	if err != nil {
		return "", nil, false, err
	}
	if _, inSet := currentSet(snap)[val]; !known && !inSet {
		return "", nil, false, nil
	}
	return "", []blocks.KeySpan{{Address: val}}, true, nil
}

func (s *Server) buildValidatorReport(ctx context.Context, val, operator string, spans []blocks.KeySpan, valopers map[string]node.Valoper, snap *node.Snapshot, now time.Time) (validatorReport, error) {
	edges := blocks.DelayEdges()
	rep := validatorReport{
		Chain: s.BlockFill.Progress().ChainID, Address: val, Operator: operator,
		Keys: make([]validatorKey, len(spans)), VoteDelayEdgesMs: edges[:],
	}
	var addresses []string // oldest key first
	for i, k := range spans {
		rep.Keys[i] = validatorKey{Address: k.Address, From: k.From, To: k.To}
		if !slices.Contains(addresses, k.Address) {
			addresses = append(addresses, k.Address)
		}
	}
	rep.Status = statusOf(snap, addresses)
	liveKey := ""
	switch {
	case rep.Status != nil && rep.Status.InSet:
		liveKey = rep.Status.SigningAddress
	case len(addresses) > 0:
		liveKey = addresses[len(addresses)-1]
	}
	if p, ok := valopers[operator]; ok && operator != "" {
		rep.Profile, rep.Moniker = profileOf(p), p.Moniker
	} else {
		rep.Moniker = s.moniker(liveKey)
	}
	if snap != nil {
		if p, ok := peersByValAddr(snap.Peers)[liveKey]; ok {
			rep.Location = &validatorLocation{Country: p.Country, City: p.City, Provider: p.Provider, Version: p.Version}
		}
	}
	rep.Live = s.liveOf(snap, liveKey)

	_, high, err := s.Blocks.Range(ctx)
	if err != nil {
		return validatorReport{}, err
	}
	changes, err := s.Blocks.ValsetChangesIn(ctx, 1, high)
	if err != nil {
		return validatorReport{}, err
	}
	setEntries := setChangeEntries(changes, addresses)
	if rep.Status != nil {
		if e, ok := statusSince(setEntries, rep.Status.InSet); ok {
			rep.Status.SinceHeight, rep.Status.SinceTime = e.Height, e.Time
		}
	}
	h, err := s.Blocks.ValidatorHistory(ctx, addresses, now)
	if err != nil {
		return validatorReport{}, err
	}
	rep.Timeline = historyEntries(setEntries, h)
	rep.Days = make([]validatorDay, len(h.Days))
	for i, d := range h.Days {
		rep.Days[i] = validatorDay{
			Day: d.Day.Format(time.DateOnly),
			signingCounts: signingCounts{
				InSet: d.InSet, Signed: d.Signed(), Absent: d.Absent, Nil: d.Nil, OtherBlock: d.OtherBlock,
				ProposalsExpected: d.ProposalsExpected, ProposalsMissed: d.ProposalsMissed,
				Votes: d.Votes, VoteDelayMeanMs: d.DelaySumMs / max(d.Votes, 1),
			},
			VoteDelayBuckets: d.DelayBuckets[:],
		}
	}
	rep.Recoveries = make([]validatorRecovery, len(h.Recoveries))
	for i, r := range h.Recoveries {
		rep.Recoveries[i] = validatorRecovery{
			RestartHeight: r.Restart, RestartTime: r.RestartTime.UTC().Format(historyTime),
			SignedAt: r.SignedAt, DurationMs: r.Duration.Milliseconds(), Pending: r.Pending,
		}
	}
	sm := h.Summary
	rep.Summary = validatorSummary{
		FirstDay: sm.FirstDay.Format(time.DateOnly), LastDay: sm.LastDay.Format(time.DateOnly),
		signingCounts: signingCounts{
			InSet: sm.InSet, Signed: sm.Signed, Absent: sm.Absent, Nil: sm.Nil, OtherBlock: sm.OtherBlock,
			ProposalsExpected: sm.ProposalsExpected, ProposalsMissed: sm.ProposalsMissed,
			Votes: sm.Votes, VoteDelayMeanMs: sm.DelaySumMs / max(sm.Votes, 1),
		},
		Outages: sm.Outages,
		Recovery: validatorRecoverySummary{
			Samples: sm.RecoverySamples, MedianMs: sm.RecoveryMedian.Milliseconds(), SlowestMs: sm.RecoverySlowest.Milliseconds(),
		},
	}
	return rep, nil
}

// currentSet returns the voting power of each member of the node's current
// set, nil while it is unknown.
func currentSet(snap *node.Snapshot) map[string]int64 {
	if snap == nil || len(snap.Validators) == 0 {
		return nil
	}
	powers := make(map[string]int64, len(snap.Validators))
	for _, v := range snap.Validators {
		powers[v.Address], _ = strconv.ParseInt(v.VotingPower, 10, 64)
	}
	return powers
}

// statusOf returns the place of the validator with keys addresses in the
// node's current set, without its since; nil while the set is unknown. One
// key at most is in the set: a rotation swaps them in one change.
func statusOf(snap *node.Snapshot, addresses []string) *validatorStatus {
	powers := currentSet(snap)
	if powers == nil {
		return nil
	}
	st := &validatorStatus{}
	for _, a := range addresses {
		if power, ok := powers[a]; ok {
			st.InSet, st.SigningAddress, st.Power = true, a, power
			break
		}
	}
	var total int64
	for _, p := range powers {
		total += p
	}
	if total > 0 {
		st.Share = float64(st.Power) / float64(total)
	}
	return st
}

func profileOf(p node.Valoper) *validatorProfile {
	out := &validatorProfile{
		Moniker: p.Moniker, Description: p.Description, ServerType: p.ServerType, Operator: p.Operator,
		SigningAddress: p.SigningAddress, SigningPubKey: p.SigningPubKey,
	}
	if p.State != nil {
		out.KeepRunning, out.LastRotationHeight = &p.State.KeepRunning, &p.State.LastRotationHeight
	}
	return out
}

// liveOf returns the live figures of the validator's key.
func (s *Server) liveOf(snap *node.Snapshot, key string) validatorLive {
	var live validatorLive
	if snap != nil && snap.Signing != nil {
		sig := snap.Signing.ValidatorSigning[key]
		live.Missed100, live.Nil100, live.OtherBlock100 = sig.Missed, sig.Nil, sig.OtherBlock
		if sig.Votes > 0 {
			live.VoteDelayMs = &sig.VoteDelayMs
		}
	}
	s.mu.RLock()
	live.Missed24h = s.missed24h[key]
	proposals := s.proposals24h[key]
	s.mu.RUnlock()
	live.ExpectedProposals24h, live.MissedProposals24h = proposals[0], proposals[1]
	return live
}

// setChangeEntries returns the timeline entries of the set changes that
// touch the keys addresses, ascending. A rotation is one entry; a key the
// genesis set holds is a genesis entry.
func setChangeEntries(changes []blocks.ValsetChange, addresses []string) []timelineEntry {
	var out []timelineEntry
	for _, c := range changes {
		entry := func(kind string) timelineEntry {
			return timelineEntry{Kind: kind, Height: c.Height, Time: c.Time.UTC().Format(historyTime)}
		}
		for _, m := range c.Added {
			if slices.Contains(addresses, m.Address) {
				e := entry("joined")
				if c.Height == 1 {
					e.Kind = "genesis"
				}
				e.timelineMembership = &timelineMembership{Power: m.Power}
				out = append(out, e)
			}
		}
		for _, m := range c.Removed {
			if slices.Contains(addresses, m.Address) {
				e := entry("left")
				e.timelineMembership = &timelineMembership{Power: m.Power}
				out = append(out, e)
			}
		}
		for _, p := range c.Repowered {
			if slices.Contains(addresses, p.Address) {
				e := entry("power")
				e.timelinePower = &timelinePower{Before: p.Before, After: p.After}
				out = append(out, e)
			}
		}
		for _, r := range c.Rotated {
			if slices.Contains(addresses, r.OldAddress) || slices.Contains(addresses, r.NewAddress) {
				e := entry("rotated")
				e.timelineRotation = &timelineRotation{OldAddress: r.OldAddress, NewAddress: r.NewAddress}
				out = append(out, e)
			}
		}
	}
	return out
}

// statusSince returns the set change entry (ascending entries) a status
// counts from: in the set, its latest join with no leave after; out of it,
// its latest leave. ok is false when the stored history holds none.
func statusSince(entries []timelineEntry, inSet bool) (since timelineEntry, ok bool) {
	var join, leave timelineEntry
	for _, e := range entries {
		switch e.Kind {
		case "joined", "genesis":
			join = e
		case "left":
			leave = e
		}
	}
	if inSet {
		return join, join.Height > 0 && join.Height >= leave.Height
	}
	return leave, leave.Height > 0 && leave.Height >= join.Height
}

// historyEntries returns the timeline: the set change entries, the outages
// and the recoveries, newest first.
func historyEntries(setEntries []timelineEntry, h blocks.History) []timelineEntry {
	out := slices.Clone(setEntries)
	for _, o := range h.Outages {
		out = append(out, timelineEntry{
			Kind: "outage", Height: o.First, Time: o.FirstTime.UTC().Format(historyTime),
			timelineOutage: &timelineOutage{To: o.Last, Blocks: o.Last - o.First + 1, EndTime: o.EndTime.UTC().Format(historyTime), Ongoing: o.Ongoing},
		})
	}
	for _, r := range h.Recoveries {
		if r.Pending {
			continue
		}
		// Its precommit for SignedAt is in block SignedAt+1, whose time is
		// RestartTime + Duration.
		out = append(out, timelineEntry{
			Kind: "recovered", Height: r.SignedAt, Time: r.RestartTime.Add(r.Duration).UTC().Format(historyTime),
			timelineRecovery: &timelineRecovery{RestartHeight: r.Restart, DurationMs: r.Duration.Milliseconds()},
		})
	}
	slices.SortStableFunc(out, func(a, b timelineEntry) int { return cmp.Compare(b.Height, a.Height) })
	if out == nil {
		out = []timelineEntry{}
	}
	return out
}

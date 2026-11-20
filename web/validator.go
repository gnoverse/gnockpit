package web

import (
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
	Pending       bool   `json:"pending"` // block signed_at+1, which times it, is not stored yet
	Scored        bool   `json:"scored"`  // one of the last 3 samples of the 180 complete UTC days before today
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
	s.serveValidator(w, r, time.Now())
}

// serveValidator answers a /api/validator request as of now.
func (s *Server) serveValidator(w http.ResponseWriter, r *http.Request, now time.Time) {
	rep, status, err := s.validatorReport(r.Context(), r.URL.Query().Get("val"), now)
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
	src := reportSources{snap: s.getSnapshot()}
	s.mu.RLock()
	src.valopers = s.valopers
	s.mu.RUnlock()
	ref, found, err := s.resolveValidator(ctx, val, src)
	if err != nil {
		log.Printf("validator %s: %v", val, err)
		return validatorReport{}, http.StatusInternalServerError, errors.New("internal error")
	}
	if !found {
		return validatorReport{}, http.StatusNotFound, errors.New("unknown validator")
	}
	rep, err := s.buildValidatorReport(ctx, val, ref, src, now)
	if err != nil {
		log.Printf("validator %s: %v", val, err)
		return validatorReport{}, http.StatusInternalServerError, errors.New("internal error")
	}
	return rep, http.StatusOK, nil
}

// validatorRef is the validator a ?val= resolves to: its operator, "" for an
// unlinked key, and its signing keys.
type validatorRef struct {
	operator string
	spans    []blocks.KeySpan
}

// reportSources is what a report reads beside the store, once per request:
// the valopers profiles and the node's snapshot.
type reportSources struct {
	valopers map[string]node.Valoper
	snap     *node.Snapshot
}

// resolveValidator finds the validator val names, in this order: the
// operator val is; the operator an event links val to as a signing key; the
// operator whose profile names val as its signing key; else val alone as an
// unlinked key, operator "", when a stored or the current set holds it.
// found is false when gnockpit knows nothing of val.
func (s *Server) resolveValidator(ctx context.Context, val string, src reportSources) (ref validatorRef, found bool, err error) {
	operatorKeys := func(op string) ([]blocks.KeySpan, error) {
		return s.Blocks.OperatorKeys(ctx, op, src.valopers[op].SigningAddress)
	}
	spans, err := operatorKeys(val)
	if err != nil {
		return validatorRef{}, false, err
	}
	if _, isProfile := src.valopers[val]; isProfile || len(spans) > 0 {
		return validatorRef{operator: val, spans: spans}, true, nil
	}
	operator, err := s.Blocks.OperatorOf(ctx, val)
	if err != nil {
		return validatorRef{}, false, err
	}
	if operator == "" {
		for _, op := range slices.Sorted(maps.Keys(src.valopers)) {
			if src.valopers[op].SigningAddress == val {
				operator = op
				break
			}
		}
	}
	if operator != "" {
		if spans, err = operatorKeys(operator); err != nil {
			return validatorRef{}, false, err
		}
		return validatorRef{operator: operator, spans: spans}, true, nil
	}
	known, err := s.Blocks.Known(ctx, val)
	if err != nil {
		return validatorRef{}, false, err
	}
	if _, inSet := currentSet(src.snap)[val]; !known && !inSet {
		return validatorRef{}, false, nil
	}
	return validatorRef{spans: []blocks.KeySpan{{Address: val}}}, true, nil
}

func (s *Server) buildValidatorReport(ctx context.Context, val string, ref validatorRef, src reportSources, now time.Time) (validatorReport, error) {
	edges := blocks.DelayEdges()
	rep := validatorReport{
		Chain: s.BlockFill.Progress().ChainID, Address: val, Operator: ref.operator,
		Keys: make([]validatorKey, len(ref.spans)), VoteDelayEdgesMs: edges[:],
	}
	var addresses []string // oldest key first
	for i, k := range ref.spans {
		rep.Keys[i] = validatorKey{Address: k.Address, From: k.From, To: k.To}
		if !slices.Contains(addresses, k.Address) {
			addresses = append(addresses, k.Address)
		}
	}
	rep.Status = statusOf(src.snap, addresses)
	liveKey := ""
	switch {
	case rep.Status != nil && rep.Status.InSet:
		liveKey = rep.Status.SigningAddress
	case len(addresses) > 0:
		liveKey = addresses[len(addresses)-1]
	}
	if p, ok := src.valopers[ref.operator]; ok && ref.operator != "" {
		rep.Profile, rep.Moniker = profileOf(p), p.Moniker
	} else {
		rep.Moniker = s.moniker(liveKey)
	}
	if src.snap != nil {
		if p, ok := peersByValAddr(src.snap.Peers)[liveKey]; ok {
			rep.Location = &validatorLocation{Country: p.Country, City: p.City, Provider: p.Provider, Version: p.Version}
		}
	}
	rep.Live = s.liveOf(src.snap, liveKey)

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
			SignedAt: r.SignedAt, DurationMs: r.Duration.Milliseconds(), Pending: r.Pending, Scored: r.Scored,
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

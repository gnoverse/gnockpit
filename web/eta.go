package web

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/gnoverse/gnockpit/eta"
	"github.com/gnoverse/gnockpit/node"
)

// ETAView is what /api/eta reads: the block-time model, the scheduled halt
// and the history coverage. *eta.Tracker provides it.
type ETAView interface {
	View() eta.View
}

// sinceSkew tolerates a since slightly ahead of the server clock, as written
// by a viewer whose clock runs fast.
const sinceSkew = time.Minute

// maxAhead bounds how far ahead, at the current pace, a height may be asked
// about.
const maxAhead = 100 * 365 * 24 * time.Hour

// etaScenarioSpans are the look-back spans the scenarios take the pace over;
// a zero span means the whole history.
var etaScenarioSpans = []struct {
	name string
	span time.Duration
}{
	{"last_hour", time.Hour},
	{"last_24h", 24 * time.Hour},
	{"last_7d", 7 * 24 * time.Hour},
	{"last_30d", 30 * 24 * time.Hour},
	{"all_history", 0},
}

// etaReport is the /api/eta payload. Height mode (?height=, or the scheduled
// halt) fills target and the eta fields; time mode (?time=) fills estimate.
// Times are RFC 3339 UTC, to the second.
type etaReport struct {
	Chain       string             `json:"chain"`
	Now         *etaBlock          `json:"now"`
	Target      *etaTarget         `json:"target,omitempty"`
	ETA         string             `json:"eta,omitempty"`
	SecondsLeft *int64             `json:"seconds_left,omitempty"`
	ReachedAt   string             `json:"reached_at,omitempty"`
	Range80     *etaTimeRange      `json:"range_80,omitempty"`
	Range95     *etaTimeRange      `json:"range_95,omitempty"`
	Estimate    *etaHeightEstimate `json:"estimate,omitempty"`
	Pace        *etaPace           `json:"pace,omitempty"`
	Original    *etaOriginal       `json:"original,omitempty"`
	Trail       []etaTrailPoint    `json:"trail,omitempty"`
	Scenarios   []etaScenario      `json:"scenarios,omitempty"`
	Halt        *etaHalt           `json:"halt"`
	Warning     string             `json:"warning,omitempty"`
	History     *etaHistory        `json:"history,omitempty"`
}

type etaBlock struct {
	Height    int64  `json:"height"`
	BlockTime string `json:"block_time"`
}

type etaTarget struct {
	Height     int64 `json:"height"`
	BlocksLeft int64 `json:"blocks_left"`
}

type etaTimeRange struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type etaHeightRange struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

type etaHeightEstimate struct {
	At      string          `json:"at"`
	Height  int64           `json:"height"`
	Range80 *etaHeightRange `json:"range_80,omitempty"`
	Range95 *etaHeightRange `json:"range_95,omitempty"`
}

type etaPace struct {
	SecondsPerBlock float64 `json:"seconds_per_block"`
	OverBlocks      int64   `json:"over_blocks"`
}

// etaOriginal is the estimate as of since: from the last sampled block at or
// before it, whose height and time are Height and AsOf. DriftSeconds is how
// much later (positive) or earlier the current ETA, or the block once reached,
// is than that estimate.
type etaOriginal struct {
	AsOf         string `json:"as_of"`
	Height       int64  `json:"height"`
	ETA          string `json:"eta"`
	DriftSeconds int64  `json:"drift_seconds"`
}

type etaTrailPoint struct {
	At      string        `json:"at"`
	Height  int64         `json:"height"`
	ETA     string        `json:"eta"`
	Range80 *etaTimeRange `json:"range_80,omitempty"`
}

// etaScenario is the answer at the pace of a look-back span: ETA in height
// mode, Height in time mode.
type etaScenario struct {
	PaceOf          string  `json:"pace_of"`
	SecondsPerBlock float64 `json:"seconds_per_block"`
	ETA             string  `json:"eta,omitempty"`
	Height          int64   `json:"height,omitempty"`
}

type etaHalt struct {
	Height     int64  `json:"height"`
	MinVersion string `json:"min_version"`
	FirstSeen  string `json:"first_seen"`
}

type etaHistory struct {
	From     string `json:"from,omitempty"`
	Samples  int    `json:"samples"`
	Complete bool   `json:"complete"`
}

// etaError is a request /api/eta cannot answer, with its HTTP status.
type etaError struct {
	status int
	msg    string
}

func (e *etaError) Error() string { return e.msg }

func badETARequest(format string, args ...any) *etaError {
	return &etaError{http.StatusBadRequest, fmt.Sprintf(format, args...)}
}

func etaUnavailable(msg string) *etaError {
	return &etaError{http.StatusServiceUnavailable, msg}
}

// errShortHistory answers an estimate the stored history cannot support yet.
var errShortHistory = etaUnavailable("the block-time history is too short to measure the pace yet")

// handleETA serves block ETAs: when a height is reached, which height a time
// reaches, or the scheduled halt. CORS-open for scripts and dashboards.
func (s *Server) handleETA(w http.ResponseWriter, r *http.Request) {
	s.serveETA(w, r, time.Now())
}

func (s *Server) serveETA(w http.ResponseWriter, r *http.Request, now time.Time) {
	rep, err := s.etaReport(r.Context(), r.URL.Query(), now)
	if err != nil {
		status := http.StatusInternalServerError
		var ee *etaError
		if errors.As(err, &ee) {
			status = ee.status
		}
		serveJSON(w, "eta", status, errorBody{Error: err.Error()})
		return
	}
	serveJSON(w, "eta", http.StatusOK, rep)
}

// etaRequest is a parsed /api/eta query: a target height or time (neither
// for the scheduled halt), and since for a height.
type etaRequest struct {
	height int64
	at     time.Time
	since  time.Time
}

func parseETARequest(q url.Values, tipTime, now time.Time) (etaRequest, error) {
	var req etaRequest
	if v := q.Get("height"); v != "" {
		h, err := strconv.ParseInt(v, 10, 64)
		if err != nil || h <= 0 {
			return req, badETARequest("height must be a positive block height, got %q", v)
		}
		req.height = h
	}
	if v := q.Get("time"); v != "" {
		if req.height != 0 {
			return req, badETARequest("give a height or a time, not both")
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return req, badETARequest("time must be RFC 3339, such as 2026-11-18T13:00:00Z, got %q", v)
		}
		if !t.After(tipTime) {
			return req, badETARequest("time %s is not after the newest block (%s)", v, rfc3339(tipTime))
		}
		req.at = t
	}
	if v := q.Get("since"); v != "" {
		if req.height == 0 {
			return req, badETARequest("since applies to a height")
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return req, badETARequest("since must be RFC 3339, such as 2026-11-16T13:47:26Z, got %q", v)
		}
		if t.After(now.Add(sinceSkew)) {
			return req, badETARequest("since %s is in the future", v)
		}
		req.since = t
	}
	return req, nil
}

func (s *Server) etaReport(ctx context.Context, q url.Values, now time.Time) (etaReport, error) {
	if s.ETA == nil {
		return etaReport{}, etaUnavailable("block ETA is not enabled")
	}
	snap := s.getSnapshot()
	if snap == nil || snap.Status == nil {
		return etaReport{}, etaUnavailable("no chain status yet")
	}
	head, err := snap.Status.Head()
	if err != nil {
		return etaReport{}, etaUnavailable(fmt.Sprintf("chain status: %v", err))
	}
	in := etaInput{view: s.ETA.View(), tip: eta.Sample{Height: head.Height, Time: head.Time}, now: now}
	req, err := parseETARequest(q, in.tip.Time, now)
	if err != nil {
		return etaReport{}, err
	}
	rep := etaReport{
		Chain:   head.ChainID,
		Now:     &etaBlock{Height: in.tip.Height, BlockTime: rfc3339(in.tip.Time)},
		Halt:    haltJSON(in.view.Halt),
		History: historyJSON(in.view),
	}
	switch halt := in.view.Halt; {
	case !req.at.IsZero():
		err = fillETATime(&rep, in, req.at)
	case req.height != 0:
		err = s.fillETAHeight(ctx, &rep, in, req.height, req.since)
	case halt != nil:
		err = s.fillETAHeight(ctx, &rep, in, halt.Height, halt.FirstSeen)
	default:
		if p, ok := in.view.Model.Pace(in.tip); ok {
			rep.Pace = paceJSON(p)
		}
	}
	return rep, err
}

// etaInput is what an /api/eta answer is computed from: the tracker's view,
// the newest block, and the server's time.
type etaInput struct {
	view eta.View
	tip  eta.Sample
	now  time.Time
}

// fillETAHeight answers when target is reached, and how the estimate moved
// since since when it is set.
func (s *Server) fillETAHeight(ctx context.Context, rep *etaReport, in etaInput, target int64, since time.Time) error {
	v, tip := in.view, in.tip
	if p, ok := v.Model.Pace(tip); ok && p.Duration(target-tip.Height) > maxAhead {
		return badETARequest("height %d is more than 100 years ahead at the current pace", target)
	}
	rep.Target = &etaTarget{Height: target, BlocksLeft: max(0, target-tip.Height)}
	if v.Halt != nil && target > v.Halt.Height {
		rep.Warning = fmt.Sprintf("a halt is scheduled at #%d, before this height; the estimate does not include the upgrade downtime", v.Halt.Height)
	}
	var outcome time.Time
	if target <= tip.Height {
		at, err := s.reachedBlockTime(ctx, target)
		if node.IsRefusal(err) {
			return etaUnavailable(fmt.Sprintf("the RPC does not serve block #%d: it may be behind, or not keep blocks that old", target))
		}
		if err != nil {
			// The error names the RPC, whose address may carry credentials:
			// keep it in the log, once per distinct failure while polling
			// cards repeat the request.
			s.etaBlockTimes.logOnce(fmt.Sprintf("eta: time of block #%d: %v", target, err))
			return &etaError{http.StatusBadGateway, fmt.Sprintf("the RPC could not give the time of block #%d", target)}
		}
		outcome = at
		rep.ReachedAt, rep.ETA = rfc3339(at), rfc3339(at)
		rep.SecondsLeft = new(int64)
	} else {
		e, ok := v.Model.Estimate(tip, target)
		if !ok {
			return errShortHistory
		}
		outcome = e.ETA
		left := max(0, int64(math.Round(e.ETA.Sub(in.now).Seconds())))
		rep.ETA, rep.SecondsLeft = rfc3339(e.ETA), &left
		rep.Range80, rep.Range95 = timeRangeJSON(e.Range80), timeRangeJSON(e.Range95)
		rep.Pace = paceJSON(e.Pace)
		rep.Scenarios = etaScenarios(v.Model, tip, func(p eta.Pace) etaScenario {
			return etaScenario{ETA: rfc3339(tip.Time.Add(p.Duration(target - tip.Height)))}
		})
	}
	if since.IsZero() {
		return nil
	}
	trail := v.Model.Trail(since, target)
	if len(trail) == 0 {
		return nil
	}
	o := trail[0]
	rep.Original = &etaOriginal{
		AsOf:         rfc3339(o.From.Time),
		Height:       o.From.Height,
		ETA:          rfc3339(o.ETA),
		DriftSeconds: int64(math.Round(outcome.Sub(o.ETA).Seconds())),
	}
	for _, e := range trail {
		rep.Trail = append(rep.Trail, etaTrailPoint{
			At: rfc3339(e.From.Time), Height: e.From.Height, ETA: rfc3339(e.ETA), Range80: timeRangeJSON(e.Range80),
		})
	}
	return nil
}

// fillETATime answers which height the chain reaches at at.
func fillETATime(rep *etaReport, in etaInput, at time.Time) error {
	v, tip := in.view, in.tip
	h, ok := v.Model.HeightAt(tip, at)
	if !ok {
		return errShortHistory
	}
	rep.Estimate = &etaHeightEstimate{
		At: rfc3339(at), Height: h.Height, Range80: heightRangeJSON(h.Range80), Range95: heightRangeJSON(h.Range95),
	}
	rep.Pace = paceJSON(h.Pace)
	rep.Scenarios = etaScenarios(v.Model, tip, func(p eta.Pace) etaScenario {
		return etaScenario{Height: tip.Height + int64(math.Round(at.Sub(tip.Time).Seconds()/p.SecondsPerBlock))}
	})
	if v.Halt != nil && h.Height > v.Halt.Height {
		rep.Warning = fmt.Sprintf("a halt is scheduled at #%d, before this time; the estimate does not include the upgrade downtime", v.Halt.Height)
	}
	return nil
}

// etaScenarios answers at the pace of each look-back span the history covers.
func etaScenarios(m *eta.Model, tip eta.Sample, answer func(eta.Pace) etaScenario) []etaScenario {
	var out []etaScenario
	for _, sc := range etaScenarioSpans {
		from := tip.Time.Add(-sc.span)
		if sc.span == 0 {
			first, ok := m.First()
			if !ok {
				continue
			}
			from = first.Time
		}
		p, ok := m.PaceSince(tip, from)
		if !ok || p.SecondsPerBlock <= 0 {
			continue
		}
		a := answer(p)
		a.PaceOf, a.SecondsPerBlock = sc.name, roundPace(p.SecondsPerBlock)
		out = append(out, a)
	}
	return out
}

// blockTimeCache remembers the time of reached blocks, which never changes,
// so cards polling a reached target do not query the RPC each time. lastLog
// is the last failure logged.
type blockTimeCache struct {
	mu      sync.Mutex
	m       map[int64]time.Time
	lastLog string
}

// logOnce logs msg unless it is the last one logged.
func (c *blockTimeCache) logOnce(msg string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if msg == c.lastLog {
		return
	}
	c.lastLog = msg
	log.Print(msg)
}

// maxCachedBlockTimes bounds the cache; it starts over when full.
const maxCachedBlockTimes = 1024

func (s *Server) reachedBlockTime(ctx context.Context, height int64) (time.Time, error) {
	c := &s.etaBlockTimes
	c.mu.Lock()
	t, ok := c.m[height]
	c.mu.Unlock()
	if ok {
		return t, nil
	}
	t, err := s.Client.BlockTime(ctx, height)
	if err != nil {
		return time.Time{}, err
	}
	c.mu.Lock()
	if c.m == nil || len(c.m) >= maxCachedBlockTimes {
		c.m = map[int64]time.Time{}
	}
	c.m[height] = t
	c.mu.Unlock()
	return t, nil
}

func haltJSON(h *eta.ScheduledHalt) *etaHalt {
	if h == nil {
		return nil
	}
	return &etaHalt{Height: h.Height, MinVersion: h.MinVersion, FirstSeen: rfc3339(h.FirstSeen)}
}

func historyJSON(v eta.View) *etaHistory {
	h := &etaHistory{Samples: v.Model.Len(), Complete: v.Complete}
	if first, ok := v.Model.First(); ok {
		h.From = rfc3339(first.Time)
	}
	return h
}

func paceJSON(p eta.Pace) *etaPace {
	return &etaPace{SecondsPerBlock: roundPace(p.SecondsPerBlock), OverBlocks: p.OverBlocks}
}

func timeRangeJSON(r *eta.TimeRange) *etaTimeRange {
	if r == nil {
		return nil
	}
	return &etaTimeRange{From: rfc3339(r.From), To: rfc3339(r.To)}
}

func heightRangeJSON(r *eta.HeightRange) *etaHeightRange {
	if r == nil {
		return nil
	}
	return &etaHeightRange{From: r.From, To: r.To}
}

// roundPace keeps four decimals: tenths of a millisecond per block.
func roundPace(s float64) float64 { return math.Round(s*1e4) / 1e4 }

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

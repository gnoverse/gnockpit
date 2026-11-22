package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/eta"
	"github.com/gnoverse/gnockpit/node"
)

// etaStart is the time of height 0 on the test chain, whose blocks all take
// 3.5 s: block h is at etaStart + h*3.5s.
var etaStart = time.Date(2026, 9, 12, 15, 0, 0, 0, time.UTC)

func etaBlockTime(h int64) time.Time { return etaStart.Add(time.Duration(h) * 3500 * time.Millisecond) }

type staticETA struct{ v eta.View }

func (s staticETA) View() eta.View { return s.v }

// answerBlockTimes is an RPC answering /blockchain with the test chain's times.
func answerBlockTimes(w http.ResponseWriter, r *http.Request) {
	h, _ := strconv.ParseInt(r.URL.Query().Get("minHeight"), 10, 64)
	fmt.Fprintf(w, `{"jsonrpc":"2.0","result":{"block_metas":[{"header":{"time":%q}}]}}`,
		etaBlockTime(h).Format(time.RFC3339Nano))
}

// etaServer serves /api/eta for a chain at tip with 40 days of regular
// history; its RPC answers /blockchain for reached heights.
func etaServer(t *testing.T, tip int64, halt *eta.ScheduledHalt) *Server {
	t.Helper()
	return etaServerWithRPC(t, tip, halt, answerBlockTimes)
}

func etaServerWithRPC(t *testing.T, tip int64, halt *eta.ScheduledHalt, rpcHandler http.HandlerFunc) *Server {
	t.Helper()
	var samples []eta.Sample
	for h := eta.Stride; h <= tip; h += eta.Stride {
		samples = append(samples, eta.Sample{Height: h, Time: etaBlockTime(h)})
	}
	rpc := httptest.NewServer(rpcHandler)
	t.Cleanup(rpc.Close)
	s := &Server{Client: node.NewClient(rpc.URL, time.Second)}
	s.ETA = staticETA{eta.View{Model: eta.NewModel(samples), Halt: halt, Complete: true}}
	s.setSnapshot(&node.Snapshot{Status: &node.Status{
		NodeInfo: node.NodeInfo{Network: "gnoland-1"},
		SyncInfo: node.SyncInfo{
			LatestBlockHeight: strconv.FormatInt(tip, 10),
			LatestBlockTime:   etaBlockTime(tip).Format(time.RFC3339Nano),
		},
	}})
	return s
}

// tipHeight is 40 days of 3.5 s blocks, plus a few off the sample grid.
const tipHeight = 987_642

// etaReply is an /api/eta answer: a report, or an error.
type etaReply struct {
	etaReport
	Error string `json:"error"`
}

// getETA queries /api/eta at the tip's block time and decodes the answer.
func getETA(t *testing.T, s *Server, query string) (int, etaReply, map[string]json.RawMessage) {
	t.Helper()
	w := httptest.NewRecorder()
	s.serveETA(w, httptest.NewRequest(http.MethodGet, "/api/eta?"+query, nil), etaBlockTime(tipHeight))
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if o := w.Header().Get("Access-Control-Allow-Origin"); o != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want *", o)
	}
	var rep etaReply
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &rep); err != nil {
		t.Fatalf("decode %s: %v", w.Body, err)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &keys); err != nil {
		t.Fatalf("decode %s: %v", w.Body, err)
	}
	return w.Code, rep, keys
}

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func TestETAHeight(t *testing.T) {
	s := etaServer(t, tipHeight, nil)
	target := int64(tipHeight + 24_000)
	code, rep, keys := getETA(t, s, "height="+strconv.FormatInt(target, 10))
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	want := etaBlockTime(target)
	if rep.ETA != rfc(want) || rep.SecondsLeft == nil || *rep.SecondsLeft != 24_000*35/10 {
		t.Errorf("eta = %s, seconds_left = %v; want %s, %d", rep.ETA, rep.SecondsLeft, rfc(want), 24_000*35/10)
	}
	if rep.Target == nil || rep.Target.Height != target || rep.Target.BlocksLeft != 24_000 {
		t.Errorf("target = %+v, want height %d, 24000 blocks left", rep.Target, target)
	}
	if rep.Range80 == nil || rep.Range80.From != rfc(want) || rep.Range80.To != rfc(want) || rep.Range95 == nil {
		t.Errorf("ranges = %+v / %+v, want both on %s for a regular chain", rep.Range80, rep.Range95, rfc(want))
	}
	if rep.Pace == nil || rep.Pace.SecondsPerBlock != 3.5 {
		t.Errorf("pace = %+v, want 3.5 s/block", rep.Pace)
	}
	if rep.Chain != "gnoland-1" || rep.Now.Height != tipHeight || rep.Now.BlockTime != rfc(etaBlockTime(tipHeight)) {
		t.Errorf("chain/now = %q / %+v", rep.Chain, rep.Now)
	}
	if len(rep.Scenarios) != 5 || rep.Scenarios[0].PaceOf != "last_hour" || rep.Scenarios[0].ETA != rfc(want) {
		t.Errorf("scenarios = %+v, want 5 starting with last_hour at %s", rep.Scenarios, rfc(want))
	}
	if !rep.History.Complete || rep.History.From != rfc(etaBlockTime(eta.Stride)) {
		t.Errorf("history = %+v", rep.History)
	}
	// A script relies on these keys; halt is always present, null when none.
	for _, k := range []string{"chain", "now", "target", "eta", "seconds_left", "range_80", "range_95", "pace", "scenarios", "halt", "history"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("missing key %q", k)
		}
	}
	if string(keys["halt"]) != "null" {
		t.Errorf("halt = %s, want null", keys["halt"])
	}
	if _, ok := keys["original"]; ok {
		t.Error("original present without since")
	}
}

func TestETAHeightSince(t *testing.T) {
	s := etaServer(t, tipHeight, nil)
	target := int64(tipHeight + 24_000)
	since := etaBlockTime(tipHeight - 7_000).Add(time.Minute)
	_, rep, _ := getETA(t, s, fmt.Sprintf("height=%d&since=%s", target, rfc(since)))

	// The original is the estimate from the last sample at or before since.
	if rep.Original == nil || rep.Original.Height != tipHeight-7_042 || rep.Original.AsOf != rfc(etaBlockTime(tipHeight-7_042)) {
		t.Fatalf("original = %+v, want as of #%d", rep.Original, tipHeight-7_042)
	}
	if rep.Original.ETA != rfc(etaBlockTime(target)) || rep.Original.DriftSeconds != 0 {
		t.Errorf("original = %+v, want ETA %s and no drift on a regular chain", rep.Original, rfc(etaBlockTime(target)))
	}
	if len(rep.Trail) < 2 || rep.Trail[0].Height != rep.Original.Height || rep.Trail[0].Range80 == nil {
		t.Errorf("trail = %+v, want it to start at the original with ranges", rep.Trail)
	}
}

func TestETAReached(t *testing.T) {
	s := etaServer(t, tipHeight, nil)
	target := int64(tipHeight - 1_234)
	since := etaBlockTime(target - 30_000)
	code, rep, keys := getETA(t, s, fmt.Sprintf("height=%d&since=%s", target, rfc(since)))
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	if rep.ReachedAt != rfc(etaBlockTime(target)) || rep.ETA != rep.ReachedAt || rep.SecondsLeft == nil || *rep.SecondsLeft != 0 {
		t.Errorf("reached_at/eta/seconds_left = %s / %s / %v, want %s twice and 0", rep.ReachedAt, rep.ETA, rep.SecondsLeft, rfc(etaBlockTime(target)))
	}
	if rep.Original == nil || rep.Original.DriftSeconds != 0 {
		t.Errorf("original = %+v, want the estimate before the target, no drift", rep.Original)
	}
	for _, k := range []string{"range_80", "range_95", "scenarios"} {
		if _, ok := keys[k]; ok {
			t.Errorf("%s present for a reached height", k)
		}
	}
}

func TestETATime(t *testing.T) {
	s := etaServer(t, tipHeight, nil)
	at := etaBlockTime(tipHeight + 1_000)
	code, rep, keys := getETA(t, s, "time="+rfc(at))
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	e := rep.Estimate
	if e == nil || e.Height != tipHeight+1_000 || e.At != rfc(at) || e.Range80 == nil || e.Range80.From != e.Height || e.Range95 == nil {
		t.Errorf("estimate = %+v, want #%d at %s with ranges", e, tipHeight+1_000, rfc(at))
	}
	if len(rep.Scenarios) == 0 || rep.Scenarios[0].Height != tipHeight+1_000 {
		t.Errorf("scenarios = %+v, want heights", rep.Scenarios)
	}
	if _, ok := keys["eta"]; ok {
		t.Error("eta present in time mode")
	}
}

func TestETAScheduledHalt(t *testing.T) {
	seen := etaBlockTime(tipHeight - 10_042) // a sampled block: the original is as of it
	halt := &eta.ScheduledHalt{Height: tipHeight + 5_000, MinVersion: "v1.8.0", FirstSeen: seen}
	s := etaServer(t, tipHeight, halt)

	_, rep, _ := getETA(t, s, "")
	if rep.Halt == nil || rep.Halt.Height != halt.Height || rep.Halt.MinVersion != "v1.8.0" || rep.Halt.FirstSeen != rfc(seen) {
		t.Errorf("halt = %+v, want %+v", rep.Halt, halt)
	}
	if rep.Target == nil || rep.Target.Height != halt.Height || rep.ETA != rfc(etaBlockTime(halt.Height)) {
		t.Errorf("target/eta = %+v / %s, want the halt's", rep.Target, rep.ETA)
	}
	if rep.Original == nil || rep.Original.AsOf != rfc(seen) {
		t.Errorf("original = %+v, want as of the first sighting %s", rep.Original, rfc(seen))
	}

	// A target past the halt: the downtime is not in the estimate.
	_, rep, _ = getETA(t, s, fmt.Sprintf("height=%d", halt.Height+1))
	if !strings.Contains(rep.Warning, fmt.Sprintf("#%d", halt.Height)) {
		t.Errorf("warning = %q, want the halt named", rep.Warning)
	}
	_, rep, _ = getETA(t, s, fmt.Sprintf("height=%d", halt.Height))
	if rep.Warning != "" {
		t.Errorf("warning = %q for the halt height itself, want none", rep.Warning)
	}
}

func TestETAScheduledHaltWithoutEstimate(t *testing.T) {
	// The halt is known even when its estimate is not: without a query, the
	// estimate's error names the halt; a question of its own gets the error
	// alone. The status is the estimate's.
	var logs bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prev) })
	failing := func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) }
	for _, tc := range []struct {
		name   string
		tip    int64
		halt   eta.ScheduledHalt
		rpc    http.HandlerFunc
		own    string // a question of its own on the halt's height
		status int
		msg    string
	}{
		{
			// 2,000 blocks of history are too few for a pace.
			name: "no pace", tip: 2_000, rpc: answerBlockTimes, own: "height=5000",
			halt:   eta.ScheduledHalt{Height: 5_000, MinVersion: "v1.8.0", FirstSeen: etaBlockTime(1_900)},
			status: http.StatusServiceUnavailable, msg: errShortHistory.msg,
		},
		{
			// The halt's block is produced and the RPC cannot date it.
			name: "undated halt block", tip: tipHeight, rpc: failing, own: "height=987000",
			halt:   eta.ScheduledHalt{Height: 987_000, MinVersion: "v1.8.0", FirstSeen: etaBlockTime(980_000)},
			status: http.StatusBadGateway, msg: "the RPC could not give the time of block #987000",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := etaServerWithRPC(t, tc.tip, &tc.halt, tc.rpc)
			var named struct {
				Error string   `json:"error"`
				Halt  *etaHalt `json:"halt"`
			}
			want := etaHalt{Height: tc.halt.Height, MinVersion: tc.halt.MinVersion, FirstSeen: rfc(tc.halt.FirstSeen)}
			if code := decodeETAStrict(t, s, tc.tip, "", &named); code != tc.status || named.Error != tc.msg || named.Halt == nil || *named.Halt != want {
				t.Errorf("no query: status %d, error %q, halt %+v; want %d, %q, %+v", code, named.Error, named.Halt, tc.status, tc.msg, want)
			}
			var own struct {
				Error string `json:"error"`
			}
			if code := decodeETAStrict(t, s, tc.tip, tc.own, &own); code != tc.status || own.Error != tc.msg {
				t.Errorf("?%s: status %d, error %q; want %d, %q", tc.own, code, own.Error, tc.status, tc.msg)
			}
		})
	}
	if !strings.Contains(logs.String(), "HTTP 500") {
		t.Errorf("log = %q, want the RPC failure", logs.String())
	}
}

// decodeETAStrict queries /api/eta?query at the time of block tip and
// decodes the answer into v, which must have a field for each of its keys.
func decodeETAStrict(t *testing.T, s *Server, tip int64, query string, v any) int {
	t.Helper()
	w := httptest.NewRecorder()
	s.serveETA(w, httptest.NewRequest(http.MethodGet, "/api/eta?"+query, nil), etaBlockTime(tip))
	dec := json.NewDecoder(bytes.NewReader(w.Body.Bytes()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("?%s: decode %s: %v", query, w.Body, err)
	}
	return w.Code
}

func TestETANoHalt(t *testing.T) {
	s := etaServer(t, tipHeight, nil)
	code, rep, keys := getETA(t, s, "")
	if code != http.StatusOK || rep.Halt != nil || rep.Target != nil || rep.Pace == nil {
		t.Errorf("status %d, halt %+v, target %+v, pace %+v; want 200, no halt, no target, the pace", code, rep.Halt, rep.Target, rep.Pace)
	}
	if string(keys["halt"]) != "null" {
		t.Errorf("halt = %s, want null", keys["halt"])
	}
}

func TestETABadRequests(t *testing.T) {
	s := etaServer(t, tipHeight, nil)
	tip := etaBlockTime(tipHeight)
	for _, q := range []string{
		"height=abc",
		"height=0",
		"height=-5",
		"time=tomorrow",
		"time=" + rfc(tip.Add(-time.Hour)),
		fmt.Sprintf("height=%d&time=%s", tipHeight+10, rfc(tip.Add(time.Hour))),
		fmt.Sprintf("height=%d&since=yesterday", tipHeight+10),
		fmt.Sprintf("height=%d&since=%s", tipHeight+10, rfc(tip.Add(time.Hour))),
		fmt.Sprintf("time=%s&since=%s", rfc(tip.Add(time.Hour)), rfc(tip.Add(-time.Hour))),
	} {
		code, rep, keys := getETA(t, s, q)
		if code != http.StatusBadRequest || rep.Error == "" {
			t.Errorf("?%s: status %d, error %q; want 400 with a reason", q, code, rep.Error)
		}
		if len(keys) != 1 {
			t.Errorf("?%s: keys %v, want only error", q, keys)
		}
	}
}

func TestETAUnavailable(t *testing.T) {
	// No tracker, no chain status, or too little history for a pace: 503.
	s := etaServer(t, tipHeight, nil)
	s.ETA = nil
	if code, rep, _ := getETA(t, s, "height=999999999"); code != http.StatusServiceUnavailable || rep.Error == "" {
		t.Errorf("no tracker: %d %q, want 503 with a reason", code, rep.Error)
	}
	s = etaServer(t, tipHeight, nil)
	s.setSnapshot(nil)
	if code, _, _ := getETA(t, s, "height=999999999"); code != http.StatusServiceUnavailable {
		t.Errorf("no status: %d, want 503", code)
	}
	s = etaServer(t, 2_000, nil)
	if code, _, _ := getETA(t, s, "height=50000"); code != http.StatusServiceUnavailable {
		t.Errorf("2000 blocks of history: %d, want 503", code)
	}
}

func TestETAReachedBlockTimeIsCached(t *testing.T) {
	var calls atomic.Int32
	s := etaServerWithRPC(t, tipHeight, nil, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		answerBlockTimes(w, r)
	})
	for range 3 {
		if code, _, _ := getETA(t, s, "height=987000"); code != http.StatusOK {
			t.Fatalf("status %d, want 200", code)
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("RPC asked %d times for a reached block's time, want 1", n)
	}
}

func TestETAReachedBlockTimeFailures(t *testing.T) {
	t.Run("RPC error", func(t *testing.T) {
		var logs bytes.Buffer
		prev := log.Writer()
		log.SetOutput(&logs)
		t.Cleanup(func() { log.SetOutput(prev) })
		s := etaServerWithRPC(t, tipHeight, nil, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		code, rep, _ := getETA(t, s, "height=987000")
		if code != http.StatusBadGateway || rep.Error == "" {
			t.Fatalf("status %d, error %q; want 502 with a reason", code, rep.Error)
		}
		// The RPC's address may carry credentials: the public answer leaves
		// it out, the log keeps it.
		if strings.Contains(rep.Error, "127.0.0.1") || strings.Contains(rep.Error, "http") {
			t.Errorf("error %q names the RPC", rep.Error)
		}
		if !strings.Contains(logs.String(), "HTTP 500") {
			t.Errorf("log = %q, want the RPC failure", logs.String())
		}
	})
	t.Run("not served yet", func(t *testing.T) {
		// The RPC is behind the source that gave the newest block.
		s := etaServerWithRPC(t, tipHeight, nil, func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"jsonrpc":"2.0","id":"","error":{"code":-32603,"message":"Internal error","data":"min height 987000 can't be greater than max height 986990"}}`)
		})
		if code, rep, _ := getETA(t, s, "height=987000"); code != http.StatusServiceUnavailable || rep.Error == "" {
			t.Errorf("status %d, error %q; want 503 with a reason", code, rep.Error)
		}
	})
}

func TestETATimeWarnsOfAHalt(t *testing.T) {
	halt := &eta.ScheduledHalt{Height: tipHeight + 500, MinVersion: "v1.8.0", FirstSeen: etaBlockTime(tipHeight)}
	s := etaServer(t, tipHeight, halt)
	_, rep, _ := getETA(t, s, "time="+rfc(etaBlockTime(tipHeight+1_000)))
	if !strings.Contains(rep.Warning, fmt.Sprintf("#%d", halt.Height)) {
		t.Errorf("warning = %q, want the halt named", rep.Warning)
	}
}

func TestETAHeightTooFarAhead(t *testing.T) {
	// The bound is a century at the current pace (3.5 s/block here).
	s := etaServer(t, tipHeight, nil)
	if code, _, _ := getETA(t, s, fmt.Sprintf("height=%d", tipHeight+200_000_000)); code != http.StatusOK {
		t.Errorf("22 years ahead: status %d, want 200", code)
	}
	if code, rep, _ := getETA(t, s, fmt.Sprintf("height=%d", tipHeight+1_000_000_000)); code != http.StatusBadRequest || rep.Error == "" {
		t.Errorf("111 years ahead: status %d, error %q; want 400 with a reason", code, rep.Error)
	}
}

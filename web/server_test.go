package web

import (
	"bytes"
	"cmp"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/png"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/eta"
	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/web/push"
)

func TestChainName(t *testing.T) {
	withNet := func(network string) *node.Snapshot {
		snap := &node.Snapshot{Status: &node.Status{}}
		snap.Status.NodeInfo.Network = network
		return snap
	}
	// configured name wins over the chain-id
	s := &Server{ChainName: "test13"}
	s.setSnapshot(withNet("test-13"))
	if got := s.chainName(); got != "test13" {
		t.Errorf("configured: got %q, want test13", got)
	}
	// no configured name → chain-id from status
	s = &Server{}
	s.setSnapshot(withNet("test-13"))
	if got := s.chainName(); got != "test-13" {
		t.Errorf("default: got %q, want test-13", got)
	}
	// nothing known yet → gnockpit fallback
	s = &Server{}
	if got := s.chainName(); got != "gnockpit" {
		t.Errorf("fallback: got %q, want gnockpit", got)
	}
}

func TestHTMLEmbedded(t *testing.T) {
	data, err := content.ReadFile("index.html")
	if err != nil {
		t.Fatal("index.html not embedded:", err)
	}
	html := string(data)
	if !strings.Contains(html, "<title>gnockpit</title>") {
		t.Error("missing title in index.html")
	}
	if !strings.Contains(html, "<script>") {
		t.Error("missing script tag")
	}
	if !strings.Contains(html, "</script>") {
		t.Error("missing closing script tag")
	}
	if !strings.Contains(html, `<link rel="manifest"`) {
		t.Error("missing manifest link in index.html")
	}
	if !strings.Contains(html, `<meta name="theme-color"`) {
		t.Error("missing theme-color meta in index.html")
	}
}

func TestNetworkStateVPFields(t *testing.T) {
	data, err := content.ReadFile("index.html")
	if err != nil {
		t.Fatal("index.html not embedded:", err)
	}
	html := string(data)
	// VP-based active voting power field
	if !strings.Contains(html, `id="ns-active-vp"`) {
		t.Error("missing ns-active-vp element for VP-based liveness margin")
	}
	// Removed fields (redundant with VP-based approach)
	if strings.Contains(html, `id="ns-valset"`) {
		t.Error("ns-valset should be removed (redundant with active vals)")
	}
	if strings.Contains(html, `id="ns-bft"`) {
		t.Error("ns-bft should be removed (BFT quorum is VP-based, shown in votes)")
	}
	// formatVP utility function
	if !strings.Contains(html, "function formatVP(") {
		t.Error("missing formatVP utility function")
	}
	// Network State has no peers field; peer counts live in the peers table.
	if strings.Contains(html, `id="ns-peers"`) {
		t.Error("ns-peers should be removed from Network State (moved to peers table)")
	}
	// The validators table has no Peers column; peer counts belong to the peers
	// table (whose own Peers column is sortable). Scope the check to the
	// validators table's header so the peers table's data-sort="peers" is allowed.
	if vIdx := strings.Index(html, `id="validators-body"`); vIdx != -1 {
		vHead := html[:vIdx]
		if ts := strings.LastIndex(vHead, "<thead>"); ts != -1 {
			if strings.Contains(vHead[ts:], `data-sort="peers"`) {
				t.Error("validators table should not have a sortable Peers column")
			}
		}
	}
}

func TestJSSyntax(t *testing.T) {
	// Extract JS from HTML and validate with node --check
	data, err := content.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(data)
	start := strings.Index(html, "<script>")
	end := strings.Index(html, "</script>")
	if start == -1 || end == -1 {
		t.Fatal("no script block found")
	}
	js := html[start+len("<script>") : end]

	cmd := exec.Command("node", "--check", "--input-type=module")
	cmd.Stdin = strings.NewReader(js)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("JS syntax error: %s\n%s", err, out)
	}
}

func TestIndexHandler(t *testing.T) {
	c := node.NewClient("http://localhost:1", 1*time.Second)
	srv := NewServer(c, "127.0.0.1:0", 5*time.Second)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	srv.handleIndex(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q, want text/html", ct)
	}
}

func TestAPIHandler(t *testing.T) {
	c := node.NewClient("http://localhost:1", 1*time.Second)
	srv := NewServer(c, "127.0.0.1:0", 5*time.Second)

	// No snapshot yet
	req := httptest.NewRequest("GET", "/api", nil)
	w := httptest.NewRecorder()
	srv.handleAPI(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "no data yet") {
		t.Errorf("expected 'no data yet', got %q", body)
	}

	// With snapshot
	srv.setSnapshot(&node.Snapshot{
		AppHashLast: "abc123",
		Timestamp:   time.Now(),
	})
	w = httptest.NewRecorder()
	srv.handleAPI(w, httptest.NewRequest("GET", "/api", nil))
	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var snap node.Snapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snap); err != nil {
		t.Fatal("invalid JSON:", err)
	}
	if snap.AppHashLast != "abc123" {
		t.Errorf("apphash = %q, want abc123", snap.AppHashLast)
	}
}

// TestAPIDumpCarriesTheFetchTime checks that /api's raw dump of a fetched
// snapshot carries the node time it was fetched at.
func TestAPIDumpCarriesTheFetchTime(t *testing.T) {
	s := newSeamServer(t)
	s.clock = func() time.Time { return seamNow }
	s.Sources = node.NewSources(nil, time.Second, s.Client.Names, nil)
	s.setSnapshot(s.fetchSnapshot(t.Context()))
	w := httptest.NewRecorder()
	s.handleAPI(w, httptest.NewRequest("GET", "/api", nil))
	var dump struct {
		System *node.SystemInfo `json:"system"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &dump); err != nil {
		t.Fatalf("unmarshal /api: %v", err)
	}
	if dump.System == nil || dump.System.NodeTime != "2026-11-18T12:00:00Z" {
		t.Errorf("/api system = %+v, want node_time 2026-11-18T12:00:00Z", dump.System)
	}
}

func TestHandleIconSVG(t *testing.T) {
	c := node.NewClient("http://localhost:1", 1*time.Second)
	srv := NewServer(c, "127.0.0.1:0", 5*time.Second)
	w := httptest.NewRecorder()
	srv.handleIconSVG(w, httptest.NewRequest("GET", "/icon.svg", nil))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "image/svg+xml") {
		t.Errorf("expected SVG content-type, got %q", ct)
	}
	body := w.Body.String()
	n := len(body)
	if n > 40 {
		n = 40
	}
	if !strings.HasPrefix(body, "<svg") {
		t.Errorf("body does not start with <svg: %q", body[:n])
	}
}

func TestHandleIconPNG(t *testing.T) {
	c := node.NewClient("http://localhost:1", 1*time.Second)
	srv := NewServer(c, "127.0.0.1:0", 5*time.Second)
	w := httptest.NewRecorder()
	srv.handleIconPNG(w, httptest.NewRequest("GET", "/icon-192.png", nil))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("expected image/png, got %q", ct)
	}
	if _, err := png.Decode(w.Body); err != nil {
		t.Errorf("invalid PNG: %v", err)
	}
}

func TestHandleManifest(t *testing.T) {
	c := node.NewClient("http://localhost:1", 1*time.Second)
	srv := NewServer(c, "127.0.0.1:0", 5*time.Second)
	w := httptest.NewRecorder()
	srv.handleManifest(w, httptest.NewRequest("GET", "/manifest.json", nil))
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("expected JSON content-type, got %q", ct)
	}
	var m map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if name, _ := m["name"].(string); !strings.HasPrefix(name, "Gnockpit ") {
		t.Errorf("manifest name should start with 'Gnockpit ', got %q", name)
	}
	if m["icons"] == nil {
		t.Error("manifest missing 'icons' field")
	}
	if m["theme_color"] == nil {
		t.Error("manifest missing 'theme_color' field")
	}
}

// newPushManager opens a push manager on an in-memory database closed with
// the test.
func newPushManager(t *testing.T, chainStuckSecs, maxMissedInARow int) *push.Manager {
	t.Helper()
	db, err := push.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test db: %v", err)
		}
	})
	mgr, err := push.NewManager(db, chainStuckSecs, maxMissedInARow)
	if err != nil {
		t.Fatalf("new push manager: %v", err)
	}
	return mgr
}

func newSrvWithPush(t *testing.T) (*Server, *push.Manager) {
	t.Helper()
	mgr := newPushManager(t, 30, 5)
	c := node.NewClient("http://localhost:1", time.Second)
	srv := NewServer(c, "127.0.0.1:0", 5*time.Second)
	srv.PushManager = mgr
	return srv, mgr
}

func TestHandlePushVAPIDKey(t *testing.T) {
	srv, mgr := newSrvWithPush(t)

	req := httptest.NewRequest(http.MethodGet, "/api/push/vapid-key", nil)
	w := httptest.NewRecorder()
	srv.handlePushVAPIDKey(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Key != mgr.VAPIDPublicKey() {
		t.Errorf("got key %q, want %q", body.Key, mgr.VAPIDPublicKey())
	}
}

func TestHandlePushSubscribe_CreateAndDelete(t *testing.T) {
	srv, _ := newSrvWithPush(t)

	reqBody := push.SubscribeRequest{
		Endpoint: "https://example.com/push",
		P256dh:   "p256dh-value",
		Auth:     "auth-value",
		Alerts: []push.SubscriptionAlert{
			{AlertType: push.AlertChainStuck, EntityID: "", RecoveryNotif: true},
		},
	}
	b, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/push/subscribe", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handlePushSubscribe(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", w.Code)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode created response: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected non-empty subscription ID")
	}

	// Delete it.
	delReq := httptest.NewRequest(http.MethodDelete, "/api/push/subscribe?id="+created.ID, nil)
	w2 := httptest.NewRecorder()
	srv.handlePushSubscribe(w2, delReq)
	if w2.Code != http.StatusNoContent {
		t.Errorf("expected 204, got %d", w2.Code)
	}
}

// TestHandlePushEntitiesWithoutConsensus checks that the validators are
// answered as unread, a 503 with a JSON error and the headers of the
// endpoint's 200, while gnockpit has read no consensus state: at start, and
// while every source is down or its consensus read fails. A page reading an
// empty list there would take it for a set without validators.
func TestHandlePushEntitiesWithoutConsensus(t *testing.T) {
	for _, tc := range []struct {
		name string
		snap *node.Snapshot
	}{
		{"at start", nil},
		{"no consensus", &node.Snapshot{Error: "all sources unreachable", Timestamp: time.Now()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newSrvWithPush(t)
			if tc.snap != nil {
				srv.setSnapshot(tc.snap)
			}
			w := httptest.NewRecorder()
			srv.handlePushEntities(w, httptest.NewRequest(http.MethodGet, "/api/push/entities", nil))
			var body errorBody
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode the answer: %v", err)
			}
			if w.Code != http.StatusServiceUnavailable || body.Error != "validator set unread" ||
				w.Header().Get("Content-Type") != "application/json" {
				t.Errorf("answer = %d %q %+v, want 503 application/json and the error validator set unread",
					w.Code, w.Header().Get("Content-Type"), body)
			}
			if o := w.Header().Get("Access-Control-Allow-Origin"); o != "" {
				t.Errorf("Access-Control-Allow-Origin = %q, want none: the push answers are the dashboard's own", o)
			}

			// The 503's headers are those of the 200 the read set gets.
			srv.setSnapshot(&node.Snapshot{Consensus: &node.ConsensusState{}, Timestamp: time.Now()})
			read := httptest.NewRecorder()
			srv.handlePushEntities(read, httptest.NewRequest(http.MethodGet, "/api/push/entities", nil))
			if read.Code != http.StatusOK {
				t.Fatalf("answer once the set is read = %d, want 200", read.Code)
			}
			for _, h := range []string{"Content-Type", "Access-Control-Allow-Origin", "Cache-Control"} {
				if got, want := w.Header().Get(h), read.Header().Get(h); got != want {
					t.Errorf("503 %s = %q, want the 200's %q", h, got, want)
				}
			}
		})
	}
}

func TestHandlePushEntities_WithSnapshot(t *testing.T) {
	srv, _ := newSrvWithPush(t)
	srv.setSnapshot(&node.Snapshot{
		Status: &node.Status{NodeInfo: node.NodeInfo{Moniker: "my-node"}},
		Consensus: &node.ConsensusState{
			Votes: []node.VoteInfo{{Address: "g1aaa", Name: "alice"}},
		},
		Timestamp: time.Now(),
	})

	req := httptest.NewRequest(http.MethodGet, "/api/push/entities", nil)
	w := httptest.NewRecorder()
	srv.handlePushEntities(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var entities push.EntitiesResponse
	if err := json.NewDecoder(w.Body).Decode(&entities); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(entities.Validators) != 1 || entities.Validators[0].ValAddress != "g1aaa" {
		t.Errorf("expected 1 validator g1aaa, got %v", entities.Validators)
	}
	if entities.ChainStuckSecs != 30 {
		t.Errorf("expected chain_stuck_secs=30, got %d", entities.ChainStuckSecs)
	}
	if entities.MaxMissedInARow != 5 {
		t.Errorf("expected max_missed_in_a_row=5, got %d", entities.MaxMissedInARow)
	}
}

// saveTestDevice stores device id, with a fresh key pair, whose push service
// is at endpoint.
func saveTestDevice(t *testing.T, mgr *push.Manager, id, endpoint string) {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("device key: %v", err)
	}
	auth := make([]byte, 16)
	if _, err := rand.Read(auth); err != nil {
		t.Fatalf("device auth: %v", err)
	}
	if err := mgr.DB().SaveSubscription(push.Subscription{
		ID:       id,
		Endpoint: endpoint,
		P256dh:   base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes()),
		Auth:     base64.RawURLEncoding.EncodeToString(auth),
	}); err != nil {
		t.Fatalf("save device: %v", err)
	}
}

// TestHandlePushTest checks /api/push/test's answers: 200 when the push
// service takes the test push, 502 with the push service's answer in the log
// when it refuses it or its endpoint is malformed, 404 for a device the
// server does not know, 500 when the push cannot be made.
func TestHandlePushTest(t *testing.T) {
	for _, tc := range []struct {
		name       string
		id         string
		pushStatus int    // the push service's answer
		endpoint   string // the device's endpoint; the push service's when empty
		brokenKey  bool   // the device's key is no P-256 point
		want       int
		wantLog    string
	}{
		{name: "delivered", id: "dev", pushStatus: http.StatusCreated, want: http.StatusOK},
		{name: "refused", id: "dev", pushStatus: http.StatusGone, want: http.StatusBadGateway, wantLog: `push: test to dev: push delivery failed: status 410: ""`},
		{name: "malformed endpoint", id: "dev", endpoint: "://push", want: http.StatusBadGateway, wantLog: "push: test to dev: push delivery failed:"},
		{name: "unknown device", id: "other", pushStatus: http.StatusCreated, want: http.StatusNotFound},
		{name: "broken key", id: "dev", pushStatus: http.StatusCreated, brokenKey: true, want: http.StatusInternalServerError, wantLog: "push: test to dev: prepare the push:"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			prev := log.Writer()
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(prev) })
			pushService := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.pushStatus)
			}))
			t.Cleanup(pushService.Close)
			srv, mgr := newSrvWithPush(t)
			endpoint := cmp.Or(tc.endpoint, pushService.URL)
			if tc.brokenKey {
				if err := mgr.DB().SaveSubscription(push.Subscription{ID: "dev", Endpoint: endpoint, P256dh: "AAAA", Auth: "AAAA"}); err != nil {
					t.Fatalf("save device: %v", err)
				}
			} else {
				saveTestDevice(t, mgr, "dev", endpoint)
			}

			w := httptest.NewRecorder()
			srv.handlePushTest(w, httptest.NewRequest(http.MethodPost, "/api/push/test", strings.NewReader(`{"id":"`+tc.id+`"}`)))
			if w.Code != tc.want {
				t.Errorf("POST /api/push/test for %s = %d, want %d", tc.id, w.Code, tc.want)
			}
			if !strings.Contains(logs.String(), tc.wantLog) {
				t.Errorf("log = %q, want %q", logs.String(), tc.wantLog)
			}
		})
	}
}

func TestBuildVotesReport_VotingPower(t *testing.T) {
	c := node.NewClient("http://localhost:1", time.Second)
	srv := NewServer(c, "127.0.0.1:0", 5*time.Second)

	snap := &node.Snapshot{
		Consensus: &node.ConsensusState{
			Height: "100",
			Round:  "0",
			Step:   "1",
			Votes: []node.VoteInfo{
				{Index: 0, Address: "g1aaa", Name: "alice"},
				{Index: 1, Address: "g1bbb", Name: "bob"},
			},
		},
		Validators: []node.Validator{
			{Address: "g1aaa", VotingPower: "1000", PubKey: node.PubKey{Type: "ed25519", Value: "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8="}},
			{Address: "g1bbb", VotingPower: "500", PubKey: node.PubKey{Type: "ed25519", Value: "HyAdHBsaGRgXFhUUExIREA8ODQwLCgkIBwYFBAMCAQA="}},
		},
		Timestamp: time.Now(),
	}

	report := srv.buildVotesReport(snap)
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if len(report.Validators) != 2 {
		t.Fatalf("expected 2 validators, got %d", len(report.Validators))
	}
	if report.Validators[0].VotingPower != "1000" {
		t.Errorf("validator 0 voting power = %q, want %q", report.Validators[0].VotingPower, "1000")
	}
	if report.Validators[1].VotingPower != "500" {
		t.Errorf("validator 1 voting power = %q, want %q", report.Validators[1].VotingPower, "500")
	}
	wantPK0 := "gpub1pggj7ard9eg82cjtv4u52epjx56nzwgjyg9zqqqpqgpsgpgxquyqjzstpsxsurcszyfpx9q4zct3sxg6rvwp68sluq9csv"
	if report.Validators[0].PubKey != wantPK0 {
		t.Errorf("validator 0 pub_key = %q, want %q", report.Validators[0].PubKey, wantPK0)
	}
	wantPK1 := "gpub1pggj7ard9eg82cjtv4u52epjx56nzwgjyg9zq8eqr5wpkxserqt3v9g5zvfpzyq0pcxsczc2pyyqwps9qspsyqgqn4c4hp"
	if report.Validators[1].PubKey != wantPK1 {
		t.Errorf("validator 1 pub_key = %q, want %q", report.Validators[1].PubKey, wantPK1)
	}
}

// subscribe POSTs a device saving alerts to /api/push/subscribe.
func subscribe(t *testing.T, srv *Server, endpoint string, alerts []push.SubscriptionAlert) (int, string) {
	t.Helper()
	b, err := json.Marshal(push.SubscribeRequest{Endpoint: endpoint, P256dh: "p", Auth: "a", Alerts: alerts})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	srv.handlePushSubscribe(w, httptest.NewRequest(http.MethodPost, "/api/push/subscribe", bytes.NewReader(b)))
	var body struct {
		ID string `json:"id"`
	}
	json.NewDecoder(w.Body).Decode(&body)
	return w.Code, body.ID
}

func TestSubscribeAnswersTheSameIdOnASecondSave(t *testing.T) {
	srv, _ := newSrvWithPush(t)
	code, first := subscribe(t, srv, "https://example.com/push", []push.SubscriptionAlert{{AlertType: push.AlertChainStuck}})
	if code != http.StatusCreated || first == "" {
		t.Fatalf("first save = %d %q, want 201 and an id", code, first)
	}
	code, second := subscribe(t, srv, "https://example.com/push", []push.SubscriptionAlert{{AlertType: push.AlertHaltApproaching, Lead: "30m"}})
	if code != http.StatusOK || second != first {
		t.Errorf("second save = %d %q, want 200 and %q", code, second, first)
	}
	code, gone := subscribe(t, srv, "https://example.com/push", nil)
	if code != http.StatusOK || gone != "" {
		t.Errorf("save with no alert = %d %q, want 200 and no id", code, gone)
	}
}

func TestSubscribeRejectsAnUnknownAlertOrBadLead(t *testing.T) {
	srv, _ := newSrvWithPush(t)
	for _, a := range []push.SubscriptionAlert{
		{AlertType: "node_down"},
		{AlertType: push.AlertETAReminder},
		{AlertType: push.AlertHaltApproaching},
		{AlertType: push.AlertHaltApproaching, Lead: "soon"},
		{AlertType: push.AlertChainStuck, Lead: "1h"},
	} {
		if code, _ := subscribe(t, srv, "https://example.com/push", []push.SubscriptionAlert{a}); code != http.StatusBadRequest {
			t.Errorf("alert %+v: status %d, want 400", a, code)
		}
	}
}

// subscribeJSON POSTs body to /api/push/subscribe and returns the answer's
// code, and the device id of a 2xx answer or the text of another.
func subscribeJSON(t *testing.T, srv *Server, body string) (code int, id, text string) {
	t.Helper()
	w := httptest.NewRecorder()
	srv.handlePushSubscribe(w, httptest.NewRequest(http.MethodPost, "/api/push/subscribe", strings.NewReader(body)))
	if w.Code >= 300 {
		return w.Code, "", strings.TrimSpace(w.Body.String())
	}
	var answer struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(w.Body).Decode(&answer); err != nil {
		t.Fatalf("decode the answer %d to %s: %v", w.Code, body, err)
	}
	return w.Code, answer.ID, ""
}

// TestSubscribeKeepsTheAlertsOfAKeptType checks a save that keeps the alerts
// of a type, as a page that could not show them sends: the device's stored
// alerts of that type stay as they are, its other alerts are replaced, and
// the device stays while the kept alerts do, though the save sends none.
func TestSubscribeKeepsTheAlertsOfAKeptType(t *testing.T) {
	srv, mgr := newSrvWithPush(t)
	const endpoint = "https://example.com/push"
	_, first := subscribe(t, srv, endpoint, []push.SubscriptionAlert{
		{AlertType: push.AlertChainStuck},
		{AlertType: push.AlertValidatorMissingBlocks, EntityID: "g1val", RecoveryNotif: true},
	})
	subscribers := func(typ push.AlertType, entity string) []push.Subscriber {
		t.Helper()
		got, err := mgr.DB().SubscribersForAlert(typ, entity)
		if err != nil {
			t.Fatalf("%s %q subscribers: %v", typ, entity, err)
		}
		return got
	}
	code, id, _ := subscribeJSON(t, srv, `{"endpoint":"`+endpoint+`","p256dh":"p","auth":"a",
		"alerts":[{"alert_type":"valset_changed"}],"keep":["validator_missing_blocks"]}`)
	if code != http.StatusOK || id != first {
		t.Fatalf("save keeping the validator alerts = %d %q, want 200 %q", code, id, first)
	}
	if kept := subscribers(push.AlertValidatorMissingBlocks, "g1val"); len(kept) != 1 || !kept[0].RecoveryNotif {
		t.Errorf("validator alert subscribers = %+v, want the device, its recovery kept", kept)
	}
	if got := subscribers(push.AlertChainStuck, ""); len(got) != 0 {
		t.Errorf("chain_stuck subscribers = %+v, want none: the save replaced it", got)
	}
	if got := subscribers(push.AlertValsetChanged, ""); len(got) != 1 {
		t.Errorf("valset_changed subscribers = %+v, want the device", got)
	}

	code, id, _ = subscribeJSON(t, srv, `{"endpoint":"`+endpoint+`","p256dh":"p","auth":"a",
		"alerts":[],"keep":["validator_missing_blocks"]}`)
	if code != http.StatusOK || id != first {
		t.Errorf("save keeping only the validator alerts = %d %q, want 200 %q: the device stays", code, id, first)
	}
	if kept := subscribers(push.AlertValidatorMissingBlocks, "g1val"); len(kept) != 1 {
		t.Errorf("validator alert subscribers = %+v, want the device", kept)
	}
}

// TestSubscribeRejectsABadKeep checks that a save may keep only alert types
// it routes, and none it also sends, each refusal named as README says.
func TestSubscribeRejectsABadKeep(t *testing.T) {
	srv, _ := newSrvWithPush(t)
	for _, tc := range []struct{ body, want string }{
		{`"alerts":[],"keep":["node_down"]`, `unknown alert "node_down"`},
		{
			`"alerts":[{"alert_type":"validator_missing_blocks","entity_id":"g1val"}],"keep":["validator_missing_blocks"]`,
			"validator_missing_blocks is kept and saved at once",
		},
	} {
		code, _, text := subscribeJSON(t, srv, `{"endpoint":"https://example.com/push","p256dh":"p","auth":"a",`+tc.body+`}`)
		if code != http.StatusBadRequest || text != tc.want {
			t.Errorf("%s: answer %d %q, want 400 %q", tc.body, code, text, tc.want)
		}
	}
}

// reminderServer serves the ETA test chain with push on.
func reminderServer(t *testing.T) *Server {
	t.Helper()
	s := etaServer(t, tipHeight, nil)
	_, mgr := newSrvWithPush(t)
	s.PushManager = mgr
	return s
}

func reminderCall(t *testing.T, s *Server, method, query string, body any) (int, map[string]any) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	w := httptest.NewRecorder()
	s.handlePushReminder(w, httptest.NewRequest(method, "/api/push/reminder"+query, r))
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	if out == nil {
		out = map[string]any{"text": strings.TrimSpace(w.Body.String())}
	}
	return w.Code, out
}

func reminderBody(height int64, lead string) map[string]any {
	return map[string]any{
		"endpoint": "https://push.example/dev", "p256dh": "p", "auth": "a",
		"height": height, "since": "2026-11-19T10:00:00Z", "label": "upgrade", "lead": lead,
	}
}

func TestReminderAPISetsReadsAndDeletes(t *testing.T) {
	s := reminderServer(t)
	code, set := reminderCall(t, s, http.MethodPut, "", reminderBody(tipHeight+5000, "100 blocks"))
	id, _ := set["id"].(string)
	if code != http.StatusOK || id == "" {
		t.Fatalf("PUT = %d %v, want 200 and the device id", code, set)
	}
	code, got := reminderCall(t, s, http.MethodGet, "?id="+id, nil)
	want := map[string]any{"height": float64(tipHeight + 5000), "since": "2026-11-19T10:00:00Z", "label": "upgrade", "lead": "100 blocks"}
	if code != http.StatusOK || !reflect.DeepEqual(got, want) {
		t.Errorf("GET = %d %v, want 200 %v", code, got, want)
	}
	code, del := reminderCall(t, s, http.MethodDelete, "?id="+id, nil)
	if code != http.StatusOK || del["device_kept"] != false {
		t.Errorf("DELETE = %d %v, want 200 and the device gone", code, del)
	}
	if code, _ := reminderCall(t, s, http.MethodGet, "?id="+id, nil); code != http.StatusNoContent {
		t.Errorf("GET after DELETE = %d, want 204: no reminder is no error", code)
	}
}

func TestReminderAPIRefusesALeadAlreadyMet(t *testing.T) {
	s := reminderServer(t)
	for _, tc := range []struct {
		height int64
		lead   string
		want   string
	}{
		{tipHeight + 50, "100 blocks", fmt.Sprintf("#%d is 100 blocks away or less", tipHeight+50)},
		{tipHeight + 1, "1 blocks", fmt.Sprintf("#%d is 1 block away or less", tipHeight+1)},
		{tipHeight + 100, "100 blocks", fmt.Sprintf("#%d is 100 blocks away or less", tipHeight+100)},
		{tipHeight + 100, "30m", fmt.Sprintf("#%d is 30m away or less", tipHeight+100)},
		{tipHeight, "30m", fmt.Sprintf("#%d is already reached", tipHeight)},
		{tipHeight - 10, "100 blocks", fmt.Sprintf("#%d is already reached", tipHeight-10)},
	} {
		code, body := reminderCall(t, s, http.MethodPut, "", reminderBody(tc.height, tc.lead))
		if code != http.StatusConflict || body["text"] != tc.want {
			t.Errorf("PUT %d %s = %d %v, want 409 %q", tc.height, tc.lead, code, body, tc.want)
		}
	}
}

func TestReminderAPIRejectsBadInput(t *testing.T) {
	s := reminderServer(t)
	for name, body := range map[string]map[string]any{
		"no endpoint": func() map[string]any { b := reminderBody(tipHeight+5000, "1h"); delete(b, "endpoint"); return b }(),
		"no height":   reminderBody(0, "1h"),
		"bad lead":    reminderBody(tipHeight+5000, "soon"),
		"bad since":   func() map[string]any { b := reminderBody(tipHeight+5000, "1h"); b["since"] = "yesterday"; return b }(),
	} {
		if code, _ := reminderCall(t, s, http.MethodPut, "", body); code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, code)
		}
	}
	bad := reminderBody(tipHeight+5000, "1h")
	bad["since"] = "yesterday"
	if _, body := reminderCall(t, s, http.MethodPut, "", bad); body["text"] != "since must be an RFC 3339 time" {
		t.Errorf("bad since: %v, want it named", body)
	}
	if code, _ := reminderCall(t, s, http.MethodGet, "", nil); code != http.StatusBadRequest {
		t.Errorf("GET without id: status %d, want 400", code)
	}
	if code, _ := reminderCall(t, s, http.MethodPost, "", nil); code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", code)
	}
}

// TestReminderAPIAnswers checks the answers to the requests the dashboard
// never sends, and to the server's own failures: each with its status, its
// text, and the line the server logs for it.
func TestReminderAPIAnswers(t *testing.T) {
	jsonOf := func(body map[string]any) string {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	without := func(key string) string {
		b := reminderBody(tipHeight+5000, "1h")
		delete(b, key)
		return jsonOf(b)
	}
	valid := jsonOf(reminderBody(tipHeight+5000, "1h"))
	oversized := reminderBody(tipHeight+5000, "1h")
	oversized["label"] = strings.Repeat("x", 64<<10)

	withPush := reminderServer
	withoutPush := func(t *testing.T) *Server { return etaServer(t, tipHeight, nil) }
	withoutStatus := func(t *testing.T) *Server {
		s, _ := newSrvWithPush(t)
		return s
	}
	closedStore := func(t *testing.T) *Server {
		s := reminderServer(t)
		if err := s.PushManager.DB().Close(); err != nil {
			t.Fatal(err)
		}
		return s
	}
	const closed = "push: sql: database is closed"
	for _, tc := range []struct {
		name          string
		server        func(t *testing.T) *Server
		method, query string
		body          string
		code          int
		text, logged  string
	}{
		{
			name: "malformed body", server: withPush, method: http.MethodPut, body: `{"endpoint":`,
			code: http.StatusBadRequest, text: "invalid request body",
		},
		{
			name: "body over 64 KiB", server: withPush, method: http.MethodPut, body: jsonOf(oversized),
			code: http.StatusBadRequest, text: "invalid request body",
		},
		{
			name: "no endpoint", server: withPush, method: http.MethodPut, body: without("endpoint"),
			code: http.StatusBadRequest, text: "endpoint, p256dh and auth are required",
		},
		{
			name: "no p256dh", server: withPush, method: http.MethodPut, body: without("p256dh"),
			code: http.StatusBadRequest, text: "endpoint, p256dh and auth are required",
		},
		{
			name: "no auth", server: withPush, method: http.MethodPut, body: without("auth"),
			code: http.StatusBadRequest, text: "endpoint, p256dh and auth are required",
		},
		{
			name: "no height", server: withPush, method: http.MethodPut, body: jsonOf(reminderBody(0, "1h")),
			code: http.StatusBadRequest, text: "height must be a block height",
		},
		{
			name: "negative height", server: withPush, method: http.MethodPut, body: jsonOf(reminderBody(-5, "1h")),
			code: http.StatusBadRequest, text: "height must be a block height",
		},
		{
			name: "GET without id", server: withPush, method: http.MethodGet,
			code: http.StatusBadRequest, text: "missing id",
		},
		{
			name: "DELETE without id", server: withPush, method: http.MethodDelete,
			code: http.StatusBadRequest, text: "missing id",
		},
		{
			name: "no chain status", server: withoutStatus, method: http.MethodPut, body: valid,
			code: http.StatusServiceUnavailable, text: "no chain status yet",
		},
		{
			name: "GET without push", server: withoutPush, method: http.MethodGet, query: "?id=x",
			code: http.StatusServiceUnavailable, text: "push not configured",
		},
		{
			name: "PUT without push", server: withoutPush, method: http.MethodPut, body: valid,
			code: http.StatusServiceUnavailable, text: "push not configured",
		},
		{
			name: "DELETE without push", server: withoutPush, method: http.MethodDelete, query: "?id=x",
			code: http.StatusServiceUnavailable, text: "push not configured",
		},
		{
			name: "POST without push", server: withoutPush, method: http.MethodPost,
			code: http.StatusServiceUnavailable, text: "push not configured",
		},
		{
			name: "read failure", server: closedStore, method: http.MethodGet, query: "?id=x",
			code: http.StatusInternalServerError, text: "read reminder", logged: "push: reminder of x: sql: database is closed",
		},
		{
			name: "save failure", server: closedStore, method: http.MethodPut, body: valid,
			code: http.StatusInternalServerError, text: "save reminder", logged: closed,
		},
		{
			name: "delete failure", server: closedStore, method: http.MethodDelete, query: "?id=x",
			code: http.StatusInternalServerError, text: "delete reminder", logged: closed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			prev := log.Writer()
			log.SetOutput(&logs)
			t.Cleanup(func() { log.SetOutput(prev) })
			s := tc.server(t)
			w := httptest.NewRecorder()
			s.handlePushReminder(w, httptest.NewRequest(tc.method, "/api/push/reminder"+tc.query, strings.NewReader(tc.body)))
			if text := strings.TrimSpace(w.Body.String()); w.Code != tc.code || text != tc.text {
				t.Errorf("%s %s = %d %q, want %d %q", tc.method, tc.query, w.Code, text, tc.code, tc.text)
			}
			if got := strings.TrimSpace(logs.String()); !strings.HasSuffix(got, tc.logged) || (tc.logged == "") != (got == "") {
				t.Errorf("server log = %q, want a line ending %q", got, tc.logged)
			}
		})
	}
}

func TestScheduleComesFromTheSnapshotAndETA(t *testing.T) {
	s := etaServer(t, tipHeight, &eta.ScheduledHalt{Height: tipHeight + 1000, MinVersion: "v1.2.0"})
	sched, ok := s.schedule()
	if !ok || sched.Chain != "gnoland-1" || sched.Head != tipHeight || sched.Halt == nil || *sched.Halt != (push.Halt{Height: tipHeight + 1000, MinVersion: "v1.2.0"}) {
		t.Fatalf("schedule() = %+v, %v; want gnoland-1 at %d with the halt", sched, ok, tipHeight)
	}
	// 1,000 blocks of 3.5 s, from the newest block's time.
	if left, ok := sched.TimeLeft(tipHeight + 1000); !ok || left.Round(time.Second) != 3500*time.Second {
		t.Errorf("TimeLeft = %v, %v; want 3500s", left, ok)
	}
	if _, ok := (&Server{}).schedule(); ok {
		t.Error("schedule() before the first snapshot is ok, want not")
	}
}

func TestNotifyScheduleSendsTheHaltAlerts(t *testing.T) {
	var pushes int
	pushSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pushes++
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(pushSrv.Close)
	s := etaServer(t, tipHeight, &eta.ScheduledHalt{Height: tipHeight + 1000, MinVersion: "v1.2.0"})
	_, mgr := newSrvWithPush(t)
	s.PushManager = mgr
	// Keys of a real browser push subscription: sends encrypt to them.
	if _, err := mgr.DB().SaveDevice(push.Subscription{
		ID: "d1", Endpoint: pushSrv.URL,
		P256dh: "BNNL5ZaTfK81qhXOx23-wewhigUeFb632jN6LvRWCFH1ubQr77FE_9qV1FuojuRmHP42zmf34rXgW80OvUVDgTk", Auth: "zqbxT6JKstKSY9JKibZLSQ",
	},
		[]push.SubscriptionAlert{{AlertType: push.AlertHaltScheduled}}, nil); err != nil {
		t.Fatal(err)
	}
	quietLog(t)
	s.notifySchedule()
	s.notifySchedule()
	if pushes != 1 {
		t.Errorf("pushes = %d, want the new halt once", pushes)
	}
	(&Server{}).notifySchedule() // push off: nothing to do
}

// float64Ptr returns a pointer to v, for an optional float64 field such as a
// located peer's coordinates.
func float64Ptr(v float64) *float64 { return &v }

// geoCityFixture is the node package's DB-IP City test database: it resolves
// 1.1.1.1 to (12.5, -34.25), Testville, TC, and nothing else.
const geoCityFixture = "../node/testdata/dbip-city-test.mmdb"

// TestGeolocatePeers checks that a peer whose IP the geolocation database
// locates gets its coordinates, city and country, and that a peer it does
// not locate, a peer with no public IP, and every peer of a server without
// the database keep none.
func TestGeolocatePeers(t *testing.T) {
	// location prints p's location, its coordinates dereferenced.
	location := func(p node.Peer) string {
		coord := func(v *float64) string {
			if v == nil {
				return "none"
			}
			return fmt.Sprint(*v)
		}
		return fmt.Sprintf("lat %s, lon %s, city %q, country %q", coord(p.Lat), coord(p.Lon), p.City, p.Country)
	}
	const none = `lat none, lon none, city "", country ""`
	located := &Server{GeoIP: node.NewGeoIP(geoCityFixture, nil)}
	for _, tc := range []struct {
		name string
		srv  *Server
		ip   string
		want string
	}{
		{"located", located, "1.1.1.1", `lat 12.5, lon -34.25, city "Testville", country "TC"`},
		{"not in the database", located, "9.9.9.9", none},
		{"no public IP", located, "", none},
		{"no database", &Server{}, "1.1.1.1", none},
	} {
		t.Run(tc.name, func(t *testing.T) {
			peers := []node.Peer{{RemoteIP: tc.ip}}
			tc.srv.geolocatePeers(peers)
			if got := location(peers[0]); got != tc.want {
				t.Errorf("geolocatePeers(%q) = %s, want %s", tc.ip, got, tc.want)
			}
		})
	}
}

// TestWSMessagesCarryPeerCoordinates checks that the page receives a located
// peer's coordinates, a zero latitude or longitude included, and none for a
// peer that is not located.
func TestWSMessagesCarryPeerCoordinates(t *testing.T) {
	s := newSeamServer(t)
	snap := &node.Snapshot{Timestamp: seamNow, Peers: []node.Peer{
		{NodeID: "equator", Lat: float64Ptr(0), Lon: float64Ptr(-78.5)},
		{NodeID: "meridian", Lat: float64Ptr(51.48), Lon: float64Ptr(0)},
		{NodeID: "frankfurt", Lat: float64Ptr(50.11), Lon: float64Ptr(8.68)},
		{NodeID: "unlocated"},
	}}
	want := map[string]map[string]any{
		"equator":   {"lat": 0.0, "lon": -78.5},
		"meridian":  {"lat": 51.48, "lon": 0.0},
		"frankfurt": {"lat": 50.11, "lon": 8.68},
		"unlocated": {},
	}
	for _, msg := range []wsMsg{s.buildSnapshotMsg(snap), s.buildUpdateMsg(snap)} {
		t.Run(msg.Type, func(t *testing.T) {
			body, err := json.Marshal(msg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got struct {
				Data struct {
					Peers []map[string]any `json:"peers"`
				} `json:"data"`
			}
			if err := json.Unmarshal(body, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(got.Data.Peers) != len(want) {
				t.Fatalf("peers = %v, want %d", got.Data.Peers, len(want))
			}
			for _, p := range got.Data.Peers {
				id, _ := p["node_id"].(string)
				coords := map[string]any{}
				for _, k := range []string{"lat", "lon"} {
					if v, ok := p[k]; ok {
						coords[k] = v
					}
				}
				if !reflect.DeepEqual(coords, want[id]) {
					t.Errorf("peer %q: coordinates = %v, want %v", id, coords, want[id])
				}
			}
		})
	}
}

// wsMsgParts returns the parts of msg's data, each as the page gets it.
func wsMsgParts(t *testing.T, msg wsMsg) map[string]json.RawMessage {
	t.Helper()
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal the %s message: %v", msg.Type, err)
	}
	var got struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal the %s message: %v", msg.Type, err)
	}
	return got.Data
}

// TestWSMessagesCarryStatusPeersAndVotes checks that both messages carry the
// status, the peers and the votes when the sources gave none: null, which
// tells the page to empty what they fill. Sources that see no peer send an
// empty list, which the page empties the same way.
func TestWSMessagesCarryStatusPeersAndVotes(t *testing.T) {
	s := newSeamServer(t)
	for _, tc := range []struct {
		name  string
		snap  *node.Snapshot
		peers string
	}{
		{"sources down", &node.Snapshot{Error: "all sources unreachable", Timestamp: seamNow}, "null"},
		{"no peer", &node.Snapshot{Peers: []node.Peer{}, Timestamp: seamNow}, "[]"},
	} {
		for _, msg := range []wsMsg{s.buildSnapshotMsg(tc.snap), s.buildUpdateMsg(tc.snap)} {
			t.Run(tc.name+"/"+msg.Type, func(t *testing.T) {
				parts := wsMsgParts(t, msg)
				want := map[string]string{"status": "null", "peers": tc.peers, "votes": "null"}
				for _, k := range []string{"status", "peers", "votes"} {
					if got, ok := parts[k]; !ok || string(got) != want[k] {
						t.Errorf("%s message: %s = %s (present: %t), want %s", msg.Type, k, got, ok, want[k])
					}
				}
			})
		}
	}
}

// TestWSSnapshotCarriesSigning checks that the message a page gets on
// connecting carries the signing stats, as the updates do: the recent blocks
// and the active validators show from the first message.
func TestWSSnapshotCarriesSigning(t *testing.T) {
	s := newSeamServer(t)
	snap := &node.Snapshot{Timestamp: seamNow, Signing: &node.SigningStats{
		ActiveCount: 3, TotalCount: 4, AvgBlockMs: 5000,
		RecentBlocks: []node.BlockInfo{{Height: "41", Signers: 3, Total: 4}},
	}}
	want := wsMsgParts(t, s.buildUpdateMsg(snap))["signing"]
	if !strings.Contains(string(want), `"active_count":3`) {
		t.Fatalf("update message signing = %s, want the snapshot's stats", want)
	}
	if got := wsMsgParts(t, s.buildSnapshotMsg(snap))["signing"]; string(got) != string(want) {
		t.Errorf("snapshot message signing = %s, want the update's %s", got, want)
	}
}

// TestWSMessagesStampTheServerTime checks that each message carries the
// server's time as of its building, to the millisecond, whenever the
// snapshot was fetched, with the snapshot's genesis time; the snapshot is
// left as it is.
func TestWSMessagesStampTheServerTime(t *testing.T) {
	s := newSeamServer(t)
	s.clock = func() time.Time { return seamNow.Add(5*time.Second + 250*time.Millisecond) }
	const fetched, genesis = "2026-11-18T12:00:00Z", "2026-11-15T12:00:00Z"
	for _, tc := range []struct {
		name   string
		system *node.SystemInfo
		want   node.SystemInfo
	}{
		{
			"fetched before", &node.SystemInfo{NodeTime: fetched, GenesisTime: genesis},
			node.SystemInfo{NodeTime: "2026-11-18T12:00:05.250Z", GenesisTime: genesis},
		},
		{"no system info", nil, node.SystemInfo{NodeTime: "2026-11-18T12:00:05.250Z"}},
	} {
		snap := &node.Snapshot{System: tc.system, Timestamp: seamNow}
		for _, msg := range []wsMsg{s.buildSnapshotMsg(snap), s.buildUpdateMsg(snap)} {
			t.Run(tc.name+"/"+msg.Type, func(t *testing.T) {
				var checks struct {
					System *node.SystemInfo `json:"system"`
				}
				if err := json.Unmarshal(wsMsgParts(t, msg)["checks"], &checks); err != nil {
					t.Fatalf("unmarshal checks: %v", err)
				}
				if checks.System == nil || *checks.System != tc.want {
					t.Errorf("%s message checks.system = %+v, want %+v", msg.Type, checks.System, tc.want)
				}
			})
		}
		if tc.system != nil && tc.system.NodeTime != fetched {
			t.Errorf("snapshot node time = %q after building the messages, want %q kept", tc.system.NodeTime, fetched)
		}
	}
}

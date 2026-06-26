package web

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/node"
	"github.com/gnoverse/gnockpit/web/push"
)

// mockBackend is a test double for RuntimeBackend.
type mockBackend struct {
	streamLines []string
	uptime      time.Duration
	memKB       int
	streamErr   error
}

func (m *mockBackend) StreamLogs(ctx context.Context) (io.ReadCloser, error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	return io.NopCloser(strings.NewReader(strings.Join(m.streamLines, "\n"))), nil
}

func (m *mockBackend) ServiceUptime(ctx context.Context) (time.Duration, error) {
	return m.uptime, nil
}

func (m *mockBackend) ProcessMemory(ctx context.Context) (int, error) {
	return m.memKB, nil
}

func TestBackendInterfaceSatisfied(t *testing.T) {
	var _ RuntimeBackend = &mockBackend{}
}

func TestSystemdBackendNameCachesOnSuccess(t *testing.T) {
	calls := 0
	b := NewSystemdBackend("", func() string {
		calls++
		if calls == 1 {
			return "" // not yet known
		}
		return "mychain.service"
	})

	// First call: nameFn returns "", falls back to "gnoland.service" without caching
	name := b.resolvedName()
	if name != "gnoland.service" {
		t.Errorf("want gnoland.service fallback, got %q", name)
	}

	// Second call: nameFn returns a value, should cache it
	name = b.resolvedName()
	if name != "mychain.service" {
		t.Errorf("want mychain.service, got %q", name)
	}

	// Third call: should use cached value without calling nameFn again
	name = b.resolvedName()
	if name != "mychain.service" {
		t.Errorf("want mychain.service cached, got %q", name)
	}
	if calls != 2 {
		t.Errorf("nameFn called %d times, want 2 (not called after cache hit)", calls)
	}
}

func TestSystemdBackendStaticName(t *testing.T) {
	b := NewSystemdBackend("explicit.service", nil)
	if b.resolvedName() != "explicit.service" {
		t.Errorf("want explicit.service, got %q", b.resolvedName())
	}
}

func TestSystemdBackendNoNameFn(t *testing.T) {
	// nil nameFn with no static name always falls back to gnoland.service
	b := NewSystemdBackend("", nil)
	if b.resolvedName() != "gnoland.service" {
		t.Errorf("want gnoland.service, got %q", b.resolvedName())
	}
}

func TestSystemdBackendUsesCat(t *testing.T) {
	b := NewSystemdBackend("test.service", nil)

	hasCat := func(args []string) bool {
		for i, a := range args {
			if a == "-o" && i+1 < len(args) && args[i+1] == "cat" {
				return true
			}
		}
		return false
	}
	if !hasCat(b.streamArgs()) {
		t.Errorf("streamArgs() = %v, missing -o cat", b.streamArgs())
	}
}

func TestDockerBackendInterfaceSatisfied(t *testing.T) {
	// Compile-time check that DockerBackend implements RuntimeBackend.
	var _ RuntimeBackend = &DockerBackend{}
}

func TestDockerBackendStreamLogsIncludesTail(t *testing.T) {
	b := &DockerBackend{ContainerName: "mycontainer"}
	args := b.streamLogsArgs()
	hasTail := false
	for i, a := range args {
		if a == "--tail" && i+1 < len(args) {
			hasTail = true
			if args[i+1] != "300" {
				t.Errorf("--tail value = %q, want %q", args[i+1], "300")
			}
		}
	}
	if !hasTail {
		t.Errorf("streamLogsArgs() = %v, missing --tail flag", args)
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
	// Peers column in validator table (peers moved from Network State)
	if strings.Contains(html, `id="ns-peers"`) {
		t.Error("ns-peers should be removed from Network State (moved to peers table)")
	}
	if !strings.Contains(html, `data-sort="peers"`) {
		t.Error("missing sortable Peers column in validator table")
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

func newSrvWithPush(t *testing.T) (*Server, *push.Manager) {
	t.Helper()
	db, err := push.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	mgr, err := push.NewManager(db, 30, 5)
	if err != nil {
		t.Fatalf("new push manager: %v", err)
	}
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

func TestHandlePushEntities_ReturnsJSON(t *testing.T) {
	srv, _ := newSrvWithPush(t)

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
	// No snapshot set — no validators returned, but thresholds are populated.
	if len(entities.Validators) != 0 {
		t.Errorf("expected 0 validators with no snapshot, got %d", len(entities.Validators))
	}
	if entities.ChainStuckSecs != 30 {
		t.Errorf("expected chain_stuck_secs=30, got %d", entities.ChainStuckSecs)
	}
	if entities.MissedBlocksPct != 5 {
		t.Errorf("expected missed_blocks_pct=5, got %d", entities.MissedBlocksPct)
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
	if entities.MissedBlocksPct != 5 {
		t.Errorf("expected missed_blocks_pct=5, got %d", entities.MissedBlocksPct)
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

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
	streamLines    []string
	fetchLines     []string
	uptime         time.Duration
	memKB          int
	binaryHash     string
	streamErr      error
	fetchErr       error
	binaryHashErr  error
}

func (m *mockBackend) StreamLogs(ctx context.Context) (io.ReadCloser, error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	return io.NopCloser(strings.NewReader(strings.Join(m.streamLines, "\n"))), nil
}

func (m *mockBackend) FetchLogs(ctx context.Context, n int) ([]string, error) {
	if m.fetchErr != nil {
		return nil, m.fetchErr
	}
	return m.fetchLines, nil
}

func (m *mockBackend) ServiceUptime(ctx context.Context) (time.Duration, error) {
	return m.uptime, nil
}

func (m *mockBackend) ProcessMemory(ctx context.Context) (int, error) {
	return m.memKB, nil
}

func (m *mockBackend) BinaryHash(ctx context.Context) (string, error) {
	return m.binaryHash, m.binaryHashErr
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
	if !hasCat(b.fetchArgs(100)) {
		t.Errorf("fetchArgs(100) = %v, missing -o cat", b.fetchArgs(100))
	}
}

func TestDockerBackendInterfaceSatisfied(t *testing.T) {
	// Compile-time check that DockerBackend implements RuntimeBackend.
	var _ RuntimeBackend = &DockerBackend{}
}


func TestDockerBackendBinaryHashArgs(t *testing.T) {
	b := &DockerBackend{ContainerName: "mycontainer"}
	args := b.binaryHashArgs()
	want := []string{"exec", "mycontainer", "sha256sum", "/usr/local/bin/gnoland"}
	if len(args) != len(want) {
		t.Fatalf("binaryHashArgs() = %v, want %v", args, want)
	}
	for i, a := range args {
		if a != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, a, want[i])
		}
	}
}

func TestDockerBackendStreamLogsIncludesSince(t *testing.T) {
	b := &DockerBackend{ContainerName: "mycontainer"}
	args := b.streamLogsArgs()
	hasSince := false
	for i, a := range args {
		if a == "--since" && i+1 < len(args) {
			hasSince = true
			if args[i+1] != "1m" {
				t.Errorf("--since value = %q, want %q", args[i+1], "1m")
			}
		}
	}
	if !hasSince {
		t.Errorf("streamLogsArgs() = %v, missing --since flag", args)
	}
}

func TestHandleLogsNilBackend(t *testing.T) {
	c := node.NewClient("http://localhost:1", 1*time.Second)
	srv := NewServer(c, "127.0.0.1:0", 5*time.Second)
	// Backend is nil — should return empty array [], not null or 500

	req := httptest.NewRequest("GET", "/api/logs", nil)
	w := httptest.NewRecorder()
	srv.handleLogs(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var respFull map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &respFull); err != nil {
		t.Fatal("invalid JSON:", err)
	}
	linesRaw, ok := respFull["lines"]
	if !ok {
		t.Fatal("missing 'lines' field")
	}
	var lines []json.RawMessage
	if err := json.Unmarshal(linesRaw, &lines); err != nil {
		t.Fatalf("'lines' is not a JSON array: %s", linesRaw)
	}
	if lines == nil {
		t.Error("lines must be [] not null")
	}
}

func TestHandleLogsSanitizesRootPaths(t *testing.T) {
	c := node.NewClient("http://localhost:1", 1*time.Second)
	srv := NewServer(c, "127.0.0.1:0", 5*time.Second)
	srv.Backend = &mockBackend{
		fetchLines: []string{
			`{"level":"info","ts":0,"msg":"loading /root/gnoland-data/config","module":"node"}`,
			`{"level":"info","ts":0,"msg":"normal log line","module":"node"}`,
		},
	}

	req := httptest.NewRequest("GET", "/api/logs", nil)
	w := httptest.NewRecorder()
	srv.handleLogs(w, req)

	body := w.Body.String()
	if strings.Contains(body, "/root/") {
		t.Error("response should not contain /root/ paths")
	}
	if !strings.Contains(body, "~/") {
		t.Error("response should contain ~/ replacement")
	}
}

func TestHandleLogsReturnsLogEntries(t *testing.T) {
	c := node.NewClient("http://localhost:1", 1*time.Second)
	srv := NewServer(c, "127.0.0.1:0", 5*time.Second)
	srv.Backend = &mockBackend{
		fetchLines: []string{
			`{"level":"info","ts":0,"msg":"hello","module":"test"}`,
		},
	}

	req := httptest.NewRequest("GET", "/api/logs", nil)
	w := httptest.NewRecorder()
	srv.handleLogs(w, req)

	var resp struct {
		Lines []LogEntry `json:"lines"`
		Count int        `json:"count"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal("invalid JSON:", err)
	}
	if resp.Count != 1 {
		t.Errorf("count = %d, want 1", resp.Count)
	}
	if len(resp.Lines) != 1 {
		t.Fatalf("len(lines) = %d, want 1", len(resp.Lines))
	}
	if resp.Lines[0].Level != "INFO" {
		t.Errorf("Level = %q, want INFO", resp.Lines[0].Level)
	}
	if resp.Lines[0].Module != "test" {
		t.Errorf("Module = %q, want test", resp.Lines[0].Module)
	}
}

func TestParseLogEventPeerExtraction(t *testing.T) {
	c := node.NewClient("http://localhost:1", 1*time.Second)
	srv := NewServer(c, "127.0.0.1:0", 5*time.Second)

	entry := LogEntry{
		Msg:   "dial peer",
		Level: "INFO",
		Extra: map[string]interface{}{"peer": "abc123@1.2.3.4:26656"},
	}
	srv.parseLogEvent(entry)

	var foundIP string
	srv.peerActivity.Range(func(k, v interface{}) bool {
		foundIP = k.(string)
		return true
	})
	if foundIP != "1.2.3.4" {
		t.Errorf("peerActivity IP = %q, want 1.2.3.4", foundIP)
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
		GenesisSHA: "abc123",
		Timestamp:  time.Now(),
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
	if snap.GenesisSHA != "abc123" {
		t.Errorf("genesis = %q, want abc123", snap.GenesisSHA)
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

func TestSSEHandler(t *testing.T) {
	c := node.NewClient("http://localhost:1", 1*time.Second)
	srv := NewServer(c, "127.0.0.1:0", 5*time.Second)

	// Pre-populate snapshot
	srv.setSnapshot(&node.Snapshot{
		GenesisSHA: "test",
		Timestamp:  time.Now(),
	})

	ts := httptest.NewServer(http.HandlerFunc(srv.handleEvents))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Errorf("content-type = %q, want text/event-stream", resp.Header.Get("Content-Type"))
	}

	// Read first few bytes to verify we get SSE data
	buf := make([]byte, 512)
	n, _ := resp.Body.Read(buf)
	data := string(buf[:n])
	if !strings.Contains(data, "event:") {
		t.Errorf("expected SSE event data, got %q", data)
	}
}

func newSrvWithPush(t *testing.T) (*Server, *push.Manager) {
	t.Helper()
	db, err := push.OpenDB(":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	mgr, err := push.NewManager(db, 30)
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
}

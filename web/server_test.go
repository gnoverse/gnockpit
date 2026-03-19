package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/gnoverse/gnockpit/node"
)

// mockBackend is a test double for RuntimeBackend.
type mockBackend struct {
	streamLines []string
	fetchLines  []string
	uptime      time.Duration
	memKB       int
	streamErr   error
	fetchErr    error
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
	// Backend is nil — should return empty list, not 500

	req := httptest.NewRequest("GET", "/api/logs", nil)
	w := httptest.NewRecorder()
	srv.handleLogs(w, req)

	if w.Code != 200 {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal("invalid JSON:", err)
	}
	lines, ok := resp["lines"]
	if !ok {
		t.Fatal("missing 'lines' field")
	}
	if lines == nil {
		t.Error("lines should not be nil")
	}
}

func TestHandleLogsSanitizesRootPaths(t *testing.T) {
	c := node.NewClient("http://localhost:1", 1*time.Second)
	srv := NewServer(c, "127.0.0.1:0", 5*time.Second)
	srv.Backend = &mockBackend{
		fetchLines: []string{
			"2024-01-15T12:34:56Z gnoland[123]: loading /root/gnoland-data/config",
			"2024-01-15T12:34:56Z gnoland[123]: normal log line",
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

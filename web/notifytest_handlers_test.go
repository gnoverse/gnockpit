package web

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNotifyTargetsHandler_Auth(t *testing.T) {
	srv, mgr := newSrvWithPush(t)

	// No token configured → feature disabled (404).
	w := httptest.NewRecorder()
	srv.handleNotifyTargets(w, httptest.NewRequest(http.MethodGet, "/api/notify/targets", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("no token configured: got %d, want 404", w.Code)
	}

	srv.NotifyTestToken = "s3cret"
	if err := mgr.SetNotifyURLs([]string{"generic+http://example.com/hook"}); err != nil {
		t.Fatal(err)
	}

	// Missing token → 401.
	w = httptest.NewRecorder()
	srv.handleNotifyTargets(w, httptest.NewRequest(http.MethodGet, "/api/notify/targets", nil))
	if w.Code != http.StatusUnauthorized {
		t.Errorf("missing token: got %d, want 401", w.Code)
	}

	// Wrong token → 401.
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notify/targets", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	srv.handleNotifyTargets(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: got %d, want 401", w.Code)
	}

	// Correct token → 200, redacted targets (must not leak the raw URL).
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/api/notify/targets", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	srv.handleNotifyTargets(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("correct token: got %d, want 200 (%s)", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if strings.Contains(body, "example.com") {
		t.Error("raw notify URL leaked in /api/notify/targets response")
	}
	if !strings.Contains(body, "generic+http") {
		t.Errorf("service missing from targets response: %s", body)
	}
}

func TestNotifyTestHandler_SendsWithToken(t *testing.T) {
	var gotBody string
	ext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer ext.Close()
	host := strings.TrimPrefix(ext.URL, "http://")

	srv, mgr := newSrvWithPush(t)
	srv.NotifyTestToken = "s3cret"
	if err := mgr.SetNotifyURLs([]string{"generic+http://" + host + "/hook"}); err != nil {
		t.Fatal(err)
	}

	// Wrong token → 401 and nothing sent.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notify/test", strings.NewReader(`{"index":0,"message":"nope"}`))
	req.Header.Set("Authorization", "Bearer wrong")
	srv.handleNotifyTest(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token: got %d, want 401", w.Code)
	}
	if gotBody != "" {
		t.Error("notification was sent despite an unauthorized request")
	}

	// Correct token → 204 and the target server receives the custom message.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/notify/test", strings.NewReader(`{"index":0,"message":"hello from test"}`))
	req.Header.Set("Authorization", "Bearer s3cret")
	req.Header.Set("Content-Type", "application/json")
	srv.handleNotifyTest(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("correct token: got %d, want 204 (%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(gotBody, "hello from test") {
		t.Errorf("target server did not receive the message; body = %q", gotBody)
	}

	// Out-of-range index → 400.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/notify/test", strings.NewReader(`{"index":9,"message":"x"}`))
	req.Header.Set("Authorization", "Bearer s3cret")
	srv.handleNotifyTest(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("out-of-range index: got %d, want 400", w.Code)
	}
}

// A send failure must not echo the raw notify URL (which holds the secret) in
// the response body.
func TestNotifyTestHandler_SendErrorDoesNotLeakURL(t *testing.T) {
	srv, mgr := newSrvWithPush(t)
	srv.NotifyTestToken = "s3cret"
	const secret = "SUPERSECRETTOKEN"
	const host = "nonexistent.invalid.example"
	if err := mgr.SetNotifyURLs([]string{"generic+https://" + host + "/webhook/" + secret}); err != nil {
		t.Fatal(err)
	}

	// Capture server-side logs (they legitimately contain the URL) so they don't
	// print the secret to test output, and to assert the failure is recorded.
	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(io.Discard)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notify/test", strings.NewReader(`{"index":0,"message":"x"}`))
	req.Header.Set("Authorization", "Bearer s3cret")
	srv.handleNotifyTest(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("send to unreachable host: got %d, want 502", w.Code)
	}
	body := w.Body.String()
	if strings.Contains(body, secret) || strings.Contains(body, host) {
		t.Errorf("response leaked the raw notify URL/secret: %q", body)
	}
	// The operator still gets the detail server-side.
	if !strings.Contains(logBuf.String(), "send to target 0 failed") {
		t.Errorf("server-side log should record the send failure, got: %q", logBuf.String())
	}
}

package push

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNotifyScheme(t *testing.T) {
	cases := map[string]string{
		"discord://token@123456":          "discord",
		"generic+http://example.com/hook": "generic+http",
		"smtp://user:pass@host:25":        "smtp",
		"not-a-url":                       "unknown",
	}
	for in, want := range cases {
		if got := notifyScheme(in); got != want {
			t.Errorf("notifyScheme(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestManager_NotifyTargets_Redacted(t *testing.T) {
	m := &Manager{notifyURLs: []string{
		"discord://sometoken@123456",
		"generic+http://example.com/hook",
	}}
	targets := m.NotifyTargets()
	if len(targets) != 2 {
		t.Fatalf("got %d targets, want 2", len(targets))
	}
	if targets[0].Index != 0 || targets[0].Service != "discord" {
		t.Errorf("targets[0] = %+v", targets[0])
	}
	if targets[1].Index != 1 || targets[1].Service != "generic+http" {
		t.Errorf("targets[1] = %+v", targets[1])
	}
	if targets[0].Fingerprint == "" || targets[0].Fingerprint == targets[1].Fingerprint {
		t.Errorf("fingerprints missing or not distinct: %q %q", targets[0].Fingerprint, targets[1].Fingerprint)
	}
	// The secret in the raw URL must never appear in the redacted metadata.
	for _, tg := range targets {
		if strings.Contains(tg.Service+tg.Fingerprint, "sometoken") {
			t.Error("raw URL secret leaked in NotifyTargets output")
		}
	}
}

func TestManager_SendTestNotify_OutOfRange(t *testing.T) {
	m := &Manager{notifyURLs: []string{"generic+http://example.com/hook"}}
	if err := m.SendTestNotify(5, "hi"); err == nil {
		t.Error("expected out-of-range error for index 5")
	}
	if err := m.SendTestNotify(-1, "hi"); err == nil {
		t.Error("expected out-of-range error for negative index")
	}
	if err := (&Manager{}).SendTestNotify(0, "hi"); err == nil {
		t.Error("expected error when no notify URLs are configured")
	}
}

func TestManager_SendTestNotify_Sends(t *testing.T) {
	var gotMethod, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://")
	m := &Manager{notifyURLs: []string{"generic+http://" + host + "/hook"}}
	if err := m.SendTestNotify(0, "gnockpit unit test"); err != nil {
		t.Fatalf("SendTestNotify: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if !strings.Contains(gotBody, "gnockpit unit test") {
		t.Errorf("target server did not receive the message; body = %q", gotBody)
	}
}

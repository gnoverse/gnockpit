package push

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"strings"
	"testing"
)

// discordURL is a Discord notification URL whose webhook token and ID stand
// in for real secrets; with secret redaction on, neither may reach an error or
// a log.
const discordURL = "discord://SECRETTOKEN@123456789"

var discordSecrets = []string{"SECRETTOKEN", "123456789"}

// failNetwork makes every outgoing HTTP connection fail at dial time, as a DNS
// or TCP failure does, for the duration of the test. Shoutrrr's Discord
// service posts through http.DefaultClient, which dials via DefaultTransport.
// It swaps that process-wide transport, so its callers must not run in
// parallel.
func failNetwork(t *testing.T) {
	t.Helper()
	prev := http.DefaultTransport
	http.DefaultTransport = &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("network is down")
		},
	}
	t.Cleanup(func() { http.DefaultTransport = prev })
}

func assertNoSecret(t *testing.T, where, text string, secrets []string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(text, s) {
			t.Errorf("%s leaks secret %q: %q", where, s, text)
		}
	}
}

func TestRedactNotifyURLs(t *testing.T) {
	tests := []struct {
		name string
		msg  string
		urls []string
		want string
	}{
		{
			name: "request URL built by the service keeps only its scheme",
			msg:  `failed to send discord notification: Post "https://discord.com/api/webhooks/123456789/SECRETTOKEN": dial tcp: lookup discord.com: no such host`,
			urls: []string{discordURL},
			want: `failed to send discord notification: Post "https://[redacted]": dial tcp: lookup discord.com: no such host`,
		},
		{
			name: "configured URL becomes its scheme and fingerprint",
			msg:  `error initializing router services: parse "discord://SECRET TOKEN@123456789": net/url: invalid userinfo`,
			urls: []string{"generic+https://example.com/hook", "discord://SECRET TOKEN@123456789"},
			want: `error initializing router services: parse "discord:88bd72a7": net/url: invalid userinfo`,
		},
		{
			name: "configured URL is matched as Go quotes it",
			msg:  `error initializing router services: parse "smtp://user:hunter\"2secretpw@smtp.example.com:587/": net/url: invalid userinfo`,
			urls: []string{`smtp://user:hunter"2secretpw@smtp.example.com:587/`},
			want: `error initializing router services: parse "smtp:c5e4703e": net/url: invalid userinfo`,
		},
		{
			name: "request URL with an escaped quote is redacted whole",
			msg:  `Post "https://hooks.example.com/a\"b/SECRETPATH": dial tcp: connection refused`,
			want: `Post "https://[redacted]": dial tcp: connection refused`,
		},
		{
			name: "longer configured URL wins over one it extends",
			msg:  `error initializing router services: parse "generic+https://hooks.example.com/notify/SECRETPATH%zz": invalid URL escape "%zz"`,
			urls: []string{"generic+https://hooks.example.com/notify", "generic+https://hooks.example.com/notify/SECRETPATH%zz"},
			want: `error initializing router services: parse "generic+https:ec71822c": invalid URL escape "%zz"`,
		},
		{
			name: "configured URL pieces echoed outside a URL are redacted",
			msg:  `error initializing router services: invalid telegram token bot123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw`,
			urls: []string{"telegram://bot123456789:AAHdqTcvCH1vGWJxfSeofSAs0K5PALDsaw@telegram?chats=@mychan"},
			want: `error initializing router services: invalid [redacted] token [redacted]:[redacted]`,
		},
		{
			name: "percent-decoded configured URL piece is redacted",
			msg:  `error initializing router services: invalid telegram token bot123456789:AAH*secretpart`,
			urls: []string{"telegram://bot123456789:AAH%2Asecretpart@telegram?chats=@mychan"},
			want: `error initializing router services: invalid [redacted] token [redacted]:[redacted]`,
		},
		{
			name: "configured URL label survives a piece equal to its scheme",
			msg:  `error initializing router services: parse "telegram://bot123456789:AAH secretpart@telegram?chats=@mychan": net/url: invalid userinfo`,
			urls: []string{"telegram://bot123456789:AAH secretpart@telegram?chats=@mychan"},
			want: `error initializing router services: parse "telegram:0b266601": net/url: invalid userinfo`,
		},
		{
			name: "decoded piece the service splits further is redacted",
			msg:  `error initializing router services: invalid URL format: first token part is invalid: 'BADGROUP-4444-4444-4444-444444444444'`,
			urls: []string{"teams://BADGROUP-4444-4444-4444-444444444444%4011111111-2222-3333-4444-555555555555:0123456789abcdef0123456789abcdef@66666666-7777-8888-9999-000000000000"},
			want: `error initializing router services: invalid URL format: first token part is invalid: '[redacted]'`,
		},
		{
			name: "configured URL pieces are redacted from six characters on",
			msg:  `failed to send smtp notification: 535 auth failed for admin (password s3cret)`,
			urls: []string{"smtp://admin:s3cret@mail.example.com:587/"},
			want: `failed to send smtp notification: 535 auth failed for admin (password [redacted])`,
		},
		{
			name: "empty configured URL is ignored",
			msg:  `response status code 429 Too Many Requests`,
			urls: []string{""},
			want: `response status code 429 Too Many Requests`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redactNotifyURLs(tt.msg, tt.urls); got != tt.want {
				t.Errorf("redactNotifyURLs(%q, %q) = %q, want %q", tt.msg, tt.urls, got, tt.want)
			}
		})
	}
}

func TestManagerSetNotifyURLs_InvalidURLErrorIsRedacted(t *testing.T) {
	m := &Manager{}
	m.SetRedactSecrets(true)
	err := m.SetNotifyURLs([]string{"discord://SECRET TOKEN@123456789"})
	if err == nil {
		t.Fatal("expected error for a malformed URL")
	}
	assertNoSecret(t, "SetNotifyURLs error", err.Error(), []string{"SECRET", "TOKEN", "123456789"})
	if !strings.Contains(err.Error(), "discord:88bd72a7") {
		t.Errorf("SetNotifyURLs error = %q, want it to name the target as discord:88bd72a7", err)
	}
}

func TestManagerNotifyAlert_NetworkErrorLogIsRedacted(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	mgr, err := NewManager(db, 30, 5)
	if err != nil {
		t.Fatal(err)
	}
	var logBuf bytes.Buffer
	prevLog := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(prevLog) })
	mgr.SetRedactSecrets(true)
	if err := mgr.SetNotifyURLs([]string{discordURL}); err != nil {
		t.Fatalf("SetNotifyURLs: %v", err)
	}
	failNetwork(t)

	mgr.NotifyAlert(Alert{Type: AlertChainStuck, Firing: true, Title: "Chain stuck", Body: "test"})

	logged := logBuf.String()
	if !strings.Contains(logged, "network is down") {
		t.Fatalf("log should record the network failure, got: %q", logged)
	}
	assertNoSecret(t, "notify log", logged, discordSecrets)
}

func TestFormatAlert(t *testing.T) {
	tests := []struct {
		name      string
		chainName string
		publicURL string
		alert     Alert
		want      string
	}{
		{
			name:      "firing alert with chain name",
			chainName: "test4",
			alert:     Alert{Firing: true, Title: "Chain stuck", Body: "No new block for 30+ seconds"},
			want:      "🚨 test4 — Chain stuck\nNo new block for 30+ seconds",
		},
		{
			name:      "recovery alert with chain name",
			chainName: "test4",
			alert:     Alert{Firing: false, Title: "Chain resumed", Body: "New block at height 100"},
			want:      "✅ test4 — Chain resumed\nNew block at height 100",
		},
		{
			name:      "firing alert without chain name",
			chainName: "",
			alert:     Alert{Firing: true, Title: "Chain stuck", Body: "No new block for 30+ seconds"},
			want:      "🚨 Chain stuck\nNo new block for 30+ seconds",
		},
		{
			name:      "with public URL",
			chainName: "test4",
			publicURL: "https://gnockpit.example.com",
			alert:     Alert{Firing: true, Title: "Chain stuck", Body: "No new block for 30+ seconds"},
			want:      "🚨 test4 — Chain stuck\nNo new block for 30+ seconds\nhttps://gnockpit.example.com",
		},
		{
			name:      "empty public URL omitted",
			chainName: "test4",
			alert:     Alert{Firing: true, Title: "Chain stuck", Body: "No new block for 30+ seconds"},
			want:      "🚨 test4 — Chain stuck\nNo new block for 30+ seconds",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatAlert(tt.chainName, tt.publicURL, tt.alert)
			if got != tt.want {
				t.Errorf("FormatAlert() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewNotifier(t *testing.T) {
	t.Run("nil for empty urls", func(t *testing.T) {
		r, err := NewNotifier(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r != nil {
			t.Fatal("expected nil router for empty URLs")
		}
	})

	t.Run("nil when all urls are empty strings", func(t *testing.T) {
		r, err := NewNotifier([]string{""})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r != nil {
			t.Fatal("expected nil router for all-empty URL strings")
		}
	})

	t.Run("valid logger url", func(t *testing.T) {
		r, err := NewNotifier([]string{"logger://"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r == nil {
			t.Fatal("expected non-nil router")
		}
	})

	t.Run("invalid url returns error", func(t *testing.T) {
		_, err := NewNotifier([]string{"invalid://bogus"})
		if err == nil {
			t.Fatal("expected error for invalid URL")
		}
	})
}

func TestManagerSetNotifyURLs(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mgr, err := NewManager(db, 30, 5)
	if err != nil {
		t.Fatal(err)
	}

	if err := mgr.SetNotifyURLs([]string{"logger://"}); err != nil {
		t.Fatalf("SetNotifyURLs: %v", err)
	}
	if mgr.notifier == nil {
		t.Fatal("expected notifier to be set")
	}
}

func TestManagerSetNotifyURLsEmpty(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mgr, err := NewManager(db, 30, 5)
	if err != nil {
		t.Fatal(err)
	}

	if err := mgr.SetNotifyURLs(nil); err != nil {
		t.Fatalf("SetNotifyURLs: %v", err)
	}
	if mgr.notifier != nil {
		t.Fatal("expected notifier to be nil for empty URLs")
	}
}

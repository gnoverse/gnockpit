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

// Notification URLs whose secrets stand in for real ones. discordURL fails
// only on the network (see failNetwork). gotifyURL carries a malformed token
// (Gotify's are 15 characters), which Shoutrrr accepts at startup and echoes,
// outside any URL, on every send. malformedDiscordURL does not parse, so
// Shoutrrr quotes it whole at startup.
const (
	discordURL          = "discord://SECRETTOKEN@123456789"
	gotifyURL           = "gotify://gotify.example.invalid/AGOTIFYTOKENXXXX"
	malformedDiscordURL = "discord://SECRET TOKEN@123456789"
)

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

// captureLog sends the standard logger's output to the returned buffer until
// the test ends.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

// newTestManager returns a Manager on an in-memory database, with secret
// redaction on when redact is set and left at its default otherwise.
func newTestManager(t *testing.T, redact bool) *Manager {
	t.Helper()
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	m, err := NewManager(db, 30, 5)
	if err != nil {
		t.Fatal(err)
	}
	if redact {
		m.SetRedactSecrets(true)
	}
	return m
}

func assertNoSecret(t *testing.T, where, text string, secrets []string) {
	t.Helper()
	for _, s := range secrets {
		if strings.Contains(text, s) {
			t.Errorf("%s leaks secret %q: %q", where, s, text)
		}
	}
}

func assertContainsAll(t *testing.T, where, text string, wants []string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			t.Errorf("%s = %q, want it to contain %q", where, text, w)
		}
	}
}

func TestRedactNotifySecrets(t *testing.T) {
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
			name: "URL with an escaped quote is redacted whole",
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
			if got := redactNotifySecrets(tt.msg, tt.urls); got != tt.want {
				t.Errorf("redactNotifySecrets(%q, %q) = %q, want %q", tt.msg, tt.urls, got, tt.want)
			}
		})
	}
}

// TestManager_ShoutrrrErrors covers every path by which a Manager emits a
// Shoutrrr error. With secret redaction on, no secret remains but the cause
// does; by default, the error is Shoutrrr's verbatim.
func TestManager_ShoutrrrErrors(t *testing.T) {
	sendSecrets := []string{"SECRETTOKEN", "123456789", "AGOTIFYTOKENXXXX"}
	sendCauses := []string{"network is down", "invalid gotify token"}
	sites := []struct {
		name    string
		emit    func(t *testing.T, m *Manager) string
		secrets []string
		causes  []string
	}{
		{
			name: "SetNotifyURLs error",
			emit: func(t *testing.T, m *Manager) string {
				err := m.SetNotifyURLs([]string{malformedDiscordURL})
				if err == nil {
					t.Fatal("SetNotifyURLs: expected error for a malformed URL")
				}
				return err.Error()
			},
			secrets: []string{"SECRET", "TOKEN", "123456789"},
			causes:  []string{"invalid userinfo"},
		},
		{
			name: "NotifyAlert log",
			emit: func(t *testing.T, m *Manager) string {
				logBuf := captureLog(t)
				if err := m.SetNotifyURLs([]string{discordURL, gotifyURL}); err != nil {
					t.Fatalf("SetNotifyURLs: %v", err)
				}
				failNetwork(t)
				m.NotifyAlert(Alert{Type: AlertChainStuck, Firing: true, Title: "Chain stuck", Body: "test"})
				return logBuf.String()
			},
			secrets: sendSecrets,
			causes:  sendCauses,
		},
		{
			name: "SendTestNotify errors",
			emit: func(t *testing.T, m *Manager) string {
				captureLog(t) // keeps SetNotifyURLs's log line out of the test output
				if err := m.SetNotifyURLs([]string{discordURL, gotifyURL}); err != nil {
					t.Fatalf("SetNotifyURLs: %v", err)
				}
				failNetwork(t)
				var texts []string
				for i := range 2 {
					err := m.SendTestNotify(i, "hi")
					if err == nil {
						t.Fatalf("SendTestNotify(%d, ...): expected error", i)
					}
					texts = append(texts, err.Error())
				}
				return strings.Join(texts, "\n")
			},
			secrets: sendSecrets,
			causes:  sendCauses,
		},
	}
	for _, s := range sites {
		t.Run(s.name+"/redaction on", func(t *testing.T) {
			text := s.emit(t, newTestManager(t, true))
			assertNoSecret(t, s.name, text, s.secrets)
			assertContainsAll(t, s.name, text, s.causes)
		})
		t.Run(s.name+"/default", func(t *testing.T) {
			text := s.emit(t, newTestManager(t, false))
			assertContainsAll(t, s.name, text, s.secrets)
			assertContainsAll(t, s.name, text, s.causes)
		})
	}
}

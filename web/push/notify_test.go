package push

import "testing"

func TestFormatAlert(t *testing.T) {
	tests := []struct {
		name      string
		chainName string
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatAlert(tt.chainName, tt.alert)
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

func TestNotifyChannels(t *testing.T) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mgr, err := NewManager(db, 30, 5)
	if err != nil {
		t.Fatal(err)
	}

	// No notify URLs → only push channel
	channels := mgr.NotifyChannels()
	if len(channels) != 1 {
		t.Fatalf("expected 1 channel (push), got %d", len(channels))
	}
	if channels[0].Type != "push" {
		t.Fatalf("expected push channel, got %q", channels[0].Type)
	}

	// Add notify URLs
	if err := mgr.SetNotifyURLs([]string{"logger://"}); err != nil {
		t.Fatal(err)
	}
	channels = mgr.NotifyChannels()
	if len(channels) != 2 {
		t.Fatalf("expected 2 channels, got %d", len(channels))
	}
	if channels[0].Type != "logger" {
		t.Fatalf("expected logger channel, got %q", channels[0].Type)
	}
	if channels[1].Type != "push" {
		t.Fatalf("expected push channel last, got %q", channels[1].Type)
	}
}

func TestSendTestNotify(t *testing.T) {
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
		t.Fatal(err)
	}

	// Test all channels (nil = all)
	results := mgr.SendTestNotify(nil, "")
	if len(results) != 1 {
		t.Fatalf("expected 1 result (logger only, push skipped with 0 subscribers), got %d", len(results))
	}
	if !results[0].OK {
		t.Fatalf("expected logger test to succeed, got error: %s", results[0].Error)
	}

	// Test with custom message
	results = mgr.SendTestNotify([]int{0}, "Custom test message")
	if len(results) != 1 || !results[0].OK {
		t.Fatalf("expected success for channel 0")
	}
}

func TestMaskURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "discord with token@id",
			url:  "discord://supersecrettoken@1234567890",
			want: "discord://****@1234567890",
		},
		{
			name: "telegram with token",
			url:  "telegram://bottoken123@telegram?chats=@mychan",
			want: "telegram://****@telegram?chats=@mychan",
		},
		{
			name: "generic no auth",
			url:  "generic://signal-api:8080/v2/send?template=json",
			want: "generic://signal-api:8080/v2/send?template=json",
		},
		{
			name: "smtp with user:pass",
			url:  "smtp://user:password@mail.example.com:587/?from=a@b.com&to=c@d.com",
			want: "smtp://****@mail.example.com:587/?from=a@b.com&to=c@d.com",
		},
		{
			name: "empty string",
			url:  "",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MaskURL(tt.url)
			if got != tt.want {
				t.Errorf("MaskURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

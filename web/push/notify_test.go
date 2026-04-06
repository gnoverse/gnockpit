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

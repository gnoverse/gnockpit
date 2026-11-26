package push

import (
	"strings"
	"testing"
)

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

func TestFormatAlertAppendsTheLink(t *testing.T) {
	a := Alert{Notice: true, Title: "Validator ranking", Body: "#1 A 99.0", Link: "/?view=ranking"}
	got := FormatAlert("test4", "https://x/", a)
	if want := "🔔 test4 — Validator ranking\n#1 A 99.0\nhttps://x/?view=ranking"; got != want {
		t.Errorf("FormatAlert(link) = %q, want %q", got, want)
	}
	if got := FormatAlert("test4", "", a); strings.Contains(got, "view=ranking") {
		t.Errorf("FormatAlert(no public URL) = %q, want no link", got)
	}
	a.Link = ""
	want := "🔔 test4 — Validator ranking\n#1 A 99.0\nhttps://x/"
	if got := FormatAlert("test4", "https://x/", a); got != want {
		t.Errorf("FormatAlert(no link) = %q, want %q", got, want)
	}
}

package push

import "testing"

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

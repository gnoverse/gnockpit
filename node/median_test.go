package node

import (
	"testing"
	"time"
)

func TestMedianTime(t *testing.T) {
	at := time.Date(2026, 11, 19, 2, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		votes []WeightedTime
		want  time.Time
	}{
		{"equal powers", []WeightedTime{{"A", at.Add(5 * time.Second), 10}, {"B", at.Add(1 * time.Second), 10}, {"C", at.Add(2 * time.Second), 10}}, at.Add(2 * time.Second)},
		// Half the power, 50, is reached at A's 4 s.
		{"weighted", []WeightedTime{{"A", at.Add(4 * time.Second), 70}, {"B", at.Add(1 * time.Second), 30}}, at.Add(4 * time.Second)},
	} {
		if got := MedianTime(tc.votes); !got.Equal(tc.want) {
			t.Errorf("%s: MedianTime = %v, want %v", tc.name, got, tc.want)
		}
	}
}

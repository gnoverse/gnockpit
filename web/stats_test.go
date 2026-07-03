package web

import (
	"testing"

	"github.com/gnoverse/gnockpit/node"
)

func TestNakamotoCoefficient(t *testing.T) {
	vals := func(powers ...string) []node.Validator {
		out := make([]node.Validator, len(powers))
		for i, p := range powers {
			out[i] = node.Validator{VotingPower: p}
		}
		return out
	}
	tests := []struct {
		name string
		vals []node.Validator
		want int
	}{
		{"one dominant", vals("10", "1", "1", "1"), 1}, // 10/13 > 1/3
		{"four equal", vals("1", "1", "1", "1"), 2},    // need 2 of 4 for >1/3
		{"three equal", vals("1", "1", "1"), 2},        // 2/3 > 1/3
		{"empty", nil, 0},
		{"all zero", vals("0", "0"), 0},
	}
	for _, tc := range tests {
		if got := nakamotoCoefficient(tc.vals); got != tc.want {
			t.Errorf("%s: nakamotoCoefficient = %d, want %d", tc.name, got, tc.want)
		}
	}
}

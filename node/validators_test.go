package node

import "testing"

func TestNameRegistry(t *testing.T) {
	r := NewNameRegistry()
	r.SetOurs("g1our", "my-node")
	r.Register("g1abc", "node-abc")
	r.Register("g1def", "node-def")

	if got := r.Name("g1abc"); got != "node-abc" {
		t.Errorf("Name(g1abc) = %q, want node-abc", got)
	}
	if got := r.Name("g1unknown"); got != "g1unknown" {
		t.Errorf("Name(g1unknown) = %q, want g1unknown", got)
	}
	if got := r.NameWithUs("g1our"); got != "my-node (us)" {
		t.Errorf("NameWithUs(g1our) = %q, want my-node (us)", got)
	}
	if got := r.NameWithUs("g1abc"); got != "node-abc" {
		t.Errorf("NameWithUs(g1abc) = %q, want node-abc", got)
	}
	if !r.IsOurs("g1our") {
		t.Error("expected IsOurs(g1our) = true")
	}
	if r.IsOurs("g1abc") {
		t.Error("expected IsOurs(g1abc) = false")
	}
	if addr, ok := r.AddrByMoniker("node-abc"); !ok || addr != "g1abc" {
		t.Errorf("AddrByMoniker(node-abc) = %q, %v", addr, ok)
	}
	if !r.IsKnownMoniker("node-def") {
		t.Error("expected IsKnownMoniker(node-def) = true")
	}
	if r.IsKnownMoniker("unknown") {
		t.Error("expected IsKnownMoniker(unknown) = false")
	}
}

func TestBitArrayToBitmask(t *testing.T) {
	tests := []struct {
		ba   bitArray
		want uint64
	}{
		{bitArray{Bits: "6", Elems: []string{"35"}}, 35},
		{bitArray{Bits: "6", Elems: []string{"0"}}, 0},
		{bitArray{Bits: "7", Elems: []string{"107"}}, 107},
		{bitArray{}, 0},
	}
	for _, tt := range tests {
		if got := tt.ba.toBitmask(); got != tt.want {
			t.Errorf("bitArray{%v}.toBitmask() = %d, want %d", tt.ba.Elems, got, tt.want)
		}
	}
}

func TestPopcount(t *testing.T) {
	tests := []struct {
		n    uint64
		want int
	}{
		{0, 0}, {1, 1}, {35, 3}, {107, 5}, {43, 4}, {63, 6}, {127, 7},
	}
	for _, tt := range tests {
		if got := popcount(tt.n); got != tt.want {
			t.Errorf("popcount(%d) = %d, want %d", tt.n, got, tt.want)
		}
	}
}

func TestIsVotePresent(t *testing.T) {
	tests := []struct {
		vote string
		want bool
	}{
		{"", false},
		{"nil-Vote", false},
		{"Vote{12:AABB}", true},
	}
	for _, tt := range tests {
		if got := isVotePresent(tt.vote); got != tt.want {
			t.Errorf("isVotePresent(%q) = %v, want %v", tt.vote, got, tt.want)
		}
	}
}

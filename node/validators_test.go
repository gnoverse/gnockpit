package node

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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

func TestNameRegistry_ReloadIfChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	os.WriteFile(path, []byte(`{"g1abc":"alice"}`), 0644)

	r := NewNameRegistryWithPersist(path)
	if got := r.Name("g1abc"); got != "alice" {
		t.Fatalf("initial load: got %q, want alice", got)
	}

	// Overwrite with new content; bump modtime to ensure detection.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(path, []byte(`{"g1abc":"alice","g1def":"bob"}`), 0644)

	r.ReloadIfChanged()
	if got := r.Name("g1def"); got != "bob" {
		t.Errorf("after ReloadIfChanged: got %q, want bob", got)
	}

	// Call again without changing the file — should not reload (no-op).
	r.Register("g1zzz", "temp")
	r.ReloadIfChanged()
	if got := r.Name("g1zzz"); got != "temp" {
		t.Errorf("expected in-memory entry preserved when file unchanged, got %q", got)
	}
}

func TestRegisterDoesNotOverwriteExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	os.WriteFile(path, []byte(`{"g1abc":"alice"}`), 0644)

	r := NewNameRegistryWithPersist(path)

	// Attempt to overwrite existing entry — must be ignored.
	r.Register("g1abc", "different-name")

	if got := r.Name("g1abc"); got != "alice" {
		t.Errorf("Name(g1abc) = %q, want alice (registry must not overwrite)", got)
	}
}

func TestSeedFromGenesisDoesNotOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	namesPath := filepath.Join(dir, "names.json")
	genesisPath := filepath.Join(dir, "genesis.json")

	// User's preferred name in the registry.
	os.WriteFile(namesPath, []byte(`{"g1abc":"my-custom-alice"}`), 0644)
	// Genesis says the validator's name is "alice-from-genesis".
	os.WriteFile(genesisPath, []byte(`{
		"genesis_time": "2026-01-01T00:00:00Z",
		"validators": [{"address": "g1abc", "name": "alice-from-genesis"}]
	}`), 0644)

	r := NewNameRegistryWithPersist(namesPath)
	if err := r.SeedFromGenesis(genesisPath); err != nil {
		t.Fatal(err)
	}

	if got := r.Name("g1abc"); got != "my-custom-alice" {
		t.Errorf("Name(g1abc) = %q, want my-custom-alice (genesis must not overwrite)", got)
	}
}

func TestSeedFromGenesisFillsGaps(t *testing.T) {
	dir := t.TempDir()
	namesPath := filepath.Join(dir, "names.json")
	genesisPath := filepath.Join(dir, "genesis.json")

	os.WriteFile(namesPath, []byte(`{"g1abc":"alice"}`), 0644)
	os.WriteFile(genesisPath, []byte(`{
		"validators": [
			{"address": "g1abc", "name": "alice-from-genesis"},
			{"address": "g1def", "name": "bob-from-genesis"}
		]
	}`), 0644)

	r := NewNameRegistryWithPersist(namesPath)
	if err := r.SeedFromGenesis(genesisPath); err != nil {
		t.Fatal(err)
	}

	if got := r.Name("g1abc"); got != "alice" {
		t.Errorf("Name(g1abc) = %q, want alice (preserved)", got)
	}
	if got := r.Name("g1def"); got != "bob-from-genesis" {
		t.Errorf("Name(g1def) = %q, want bob-from-genesis (filled from genesis)", got)
	}
}

func TestEnsureName_NoOpWhenAlreadyKnown(t *testing.T) {
	r := NewNameRegistry()
	r.Register("g1abc", "alice")

	r.EnsureName("g1abc", "ignored-candidate")

	if got := r.Name("g1abc"); got != "alice" {
		t.Errorf("Name(g1abc) = %q, want alice (must not change)", got)
	}
}

func TestEnsureName_UsesFirstNonEmptyCandidate(t *testing.T) {
	r := NewNameRegistry()

	r.EnsureName("g1abc", "", "first-real", "second-real")

	if got := r.Name("g1abc"); got != "first-real" {
		t.Errorf("Name(g1abc) = %q, want first-real", got)
	}
}

func TestEnsureName_FallsBackToUnknownValN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	r := NewNameRegistryWithPersist(path)

	// Three new addresses with no candidates → unknown-val-1, -2, -3.
	r.EnsureName("g1aaa")
	r.EnsureName("g1bbb")
	r.EnsureName("g1ccc")

	if got := r.Name("g1aaa"); got != "unknown-val-1" {
		t.Errorf("Name(g1aaa) = %q, want unknown-val-1", got)
	}
	if got := r.Name("g1bbb"); got != "unknown-val-2" {
		t.Errorf("Name(g1bbb) = %q, want unknown-val-2", got)
	}
	if got := r.Name("g1ccc"); got != "unknown-val-3" {
		t.Errorf("Name(g1ccc) = %q, want unknown-val-3", got)
	}

	// Verify persistence: a fresh registry loads all three back.
	r2 := NewNameRegistryWithPersist(path)
	if got := r2.Name("g1aaa"); got != "unknown-val-1" {
		t.Errorf("after reload Name(g1aaa) = %q, want unknown-val-1", got)
	}

	// New unknown picks up where existing N left off.
	r2.EnsureName("g1ddd")
	if got := r2.Name("g1ddd"); got != "unknown-val-4" {
		t.Errorf("Name(g1ddd) = %q, want unknown-val-4", got)
	}
}

func TestEnsureName_UnknownValNContinuesAfterGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	// Pre-existing file with a gap: only unknown-val-2 exists.
	os.WriteFile(path, []byte(`{"g1xxx":"unknown-val-2"}`), 0644)

	r := NewNameRegistryWithPersist(path)
	r.EnsureName("g1aaa")

	if got := r.Name("g1aaa"); got != "unknown-val-3" {
		t.Errorf("Name(g1aaa) = %q, want unknown-val-3 (max+1)", got)
	}
}

func TestRegisterUpgradesUnknownValN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	os.WriteFile(path, []byte(`{"g1abc":"unknown-val-1"}`), 0644)

	r := NewNameRegistryWithPersist(path)

	// A real moniker becomes available — must replace the placeholder.
	r.Register("g1abc", "alice")

	if got := r.Name("g1abc"); got != "alice" {
		t.Errorf("Name(g1abc) = %q, want alice (unknown-val-N must upgrade)", got)
	}
	// The stale unknown-val-1 → addr mapping must be cleared.
	if _, ok := r.AddrByMoniker("unknown-val-1"); ok {
		t.Error("AddrByMoniker(unknown-val-1) still resolves after upgrade")
	}
	// And persisted to disk.
	r2 := NewNameRegistryWithPersist(path)
	if got := r2.Name("g1abc"); got != "alice" {
		t.Errorf("after reload Name(g1abc) = %q, want alice", got)
	}
}

func TestEnsureName_UpgradesUnknownValNWithCandidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	os.WriteFile(path, []byte(`{"g1abc":"unknown-val-1"}`), 0644)

	r := NewNameRegistryWithPersist(path)
	r.EnsureName("g1abc", "alice-from-rpc")

	if got := r.Name("g1abc"); got != "alice-from-rpc" {
		t.Errorf("Name(g1abc) = %q, want alice-from-rpc", got)
	}
}

func TestEnsureName_KeepsUnknownValNWithoutCandidate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	os.WriteFile(path, []byte(`{"g1abc":"unknown-val-2"}`), 0644)

	r := NewNameRegistryWithPersist(path)

	// No candidate available; existing unknown-val-2 must NOT be replaced
	// with a freshly-allocated unknown-val-N.
	r.EnsureName("g1abc")

	if got := r.Name("g1abc"); got != "unknown-val-2" {
		t.Errorf("Name(g1abc) = %q, want unknown-val-2 (no churn)", got)
	}
}

func TestSeedFromGenesisUpgradesUnknownValN(t *testing.T) {
	dir := t.TempDir()
	namesPath := filepath.Join(dir, "names.json")
	genesisPath := filepath.Join(dir, "genesis.json")

	os.WriteFile(namesPath, []byte(`{"g1abc":"unknown-val-1"}`), 0644)
	os.WriteFile(genesisPath, []byte(`{
		"validators": [{"address": "g1abc", "name": "alice-from-genesis"}]
	}`), 0644)

	r := NewNameRegistryWithPersist(namesPath)
	if err := r.SeedFromGenesis(genesisPath); err != nil {
		t.Fatal(err)
	}

	if got := r.Name("g1abc"); got != "alice-from-genesis" {
		t.Errorf("Name(g1abc) = %q, want alice-from-genesis (genesis must upgrade unknown-val-N)", got)
	}
}

func TestSetOursPreservesRealName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	os.WriteFile(path, []byte(`{"g1us":"my-custom-name"}`), 0644)

	r := NewNameRegistryWithPersist(path)
	// Gnoland self-reports a different moniker — file must win.
	r.SetOurs("g1us", "node1-from-gnoland")

	if got := r.Name("g1us"); got != "my-custom-name" {
		t.Errorf("Name(g1us) = %q, want my-custom-name", got)
	}
	if !r.IsOurs("g1us") {
		t.Error("expected IsOurs(g1us) = true (tracking must still work)")
	}
}

func TestSetOursUpgradesUnknownValN(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	os.WriteFile(path, []byte(`{"g1us":"unknown-val-1"}`), 0644)

	r := NewNameRegistryWithPersist(path)
	r.SetOurs("g1us", "node1")

	if got := r.Name("g1us"); got != "node1" {
		t.Errorf("Name(g1us) = %q, want node1 (unknown-val-N must upgrade)", got)
	}
}

func TestSetOursFillsGap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	r := NewNameRegistryWithPersist(path)

	r.SetOurs("g1us", "node1")

	if got := r.Name("g1us"); got != "node1" {
		t.Errorf("Name(g1us) = %q, want node1 (must fill gap)", got)
	}
	// Persistence: gap-fill writes to disk.
	r2 := NewNameRegistryWithPersist(path)
	if got := r2.Name("g1us"); got != "node1" {
		t.Errorf("after reload Name(g1us) = %q, want node1", got)
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

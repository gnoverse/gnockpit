package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	if addr, ok := r.AddrByMoniker("node-abc"); !ok || addr != "g1abc" {
		t.Errorf("AddrByMoniker(node-abc) = %q, %v", addr, ok)
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
	namesPath := filepath.Join(t.TempDir(), "names.json")

	// User's preferred name in the registry.
	os.WriteFile(namesPath, []byte(`{"g1abc":"my-custom-alice"}`), 0644)

	r := NewNameRegistryWithPersist(namesPath)
	// Genesis says the validator's name is "alice-from-genesis".
	stream := strings.NewReader(`{"result":{"genesis":{` +
		`"genesis_time":"2026-01-01T00:00:00Z",` +
		`"validators":[{"address":"g1abc","name":"alice-from-genesis"}]}}}`)
	if err := r.SeedFromGenesisStream(stream); err != nil {
		t.Fatal(err)
	}

	if got := r.Name("g1abc"); got != "my-custom-alice" {
		t.Errorf("Name(g1abc) = %q, want my-custom-alice (genesis must not overwrite)", got)
	}
}

func TestSeedFromGenesisFillsGaps(t *testing.T) {
	namesPath := filepath.Join(t.TempDir(), "names.json")
	os.WriteFile(namesPath, []byte(`{"g1abc":"alice"}`), 0644)

	r := NewNameRegistryWithPersist(namesPath)
	stream := strings.NewReader(`{"result":{"genesis":{"validators":[` +
		`{"address":"g1abc","name":"alice-from-genesis"},` +
		`{"address":"g1def","name":"bob-from-genesis"}]}}}`)
	if err := r.SeedFromGenesisStream(stream); err != nil {
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
	namesPath := filepath.Join(t.TempDir(), "names.json")
	os.WriteFile(namesPath, []byte(`{"g1abc":"unknown-val-1"}`), 0644)

	r := NewNameRegistryWithPersist(namesPath)
	stream := strings.NewReader(`{"result":{"genesis":{"validators":[` +
		`{"address":"g1abc","name":"alice-from-genesis"}]}}}`)
	if err := r.SeedFromGenesisStream(stream); err != nil {
		t.Fatal(err)
	}

	if got := r.Name("g1abc"); got != "alice-from-genesis" {
		t.Errorf("Name(g1abc) = %q, want alice-from-genesis (genesis must upgrade unknown-val-N)", got)
	}
}

func TestSeedFromGenesisStream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	// Registry already knows g1abc (custom name) and a placeholder for g1def.
	os.WriteFile(path, []byte(`{"g1abc":"my-alice","g1def":"unknown-val-1"}`), 0644)
	r := NewNameRegistryWithPersist(path)

	// An enveloped /genesis response. The app_state after the validators array is
	// deliberately invalid JSON: a correct streaming parser stops at validators and
	// never tokenizes app_state, so this must still succeed.
	stream := strings.NewReader(`{"jsonrpc":"2.0","id":-1,"result":{"genesis":{` +
		`"genesis_time":"2026-03-16T09:00:00Z",` +
		`"chain_id":"test-13",` +
		`"initial_height":"0",` +
		`"consensus_params":{"block":{"max_tx_bytes":"1000000"}},` +
		`"validators":[` +
		`{"address":"g1abc","name":"alice-from-genesis"},` +
		`{"address":"g1def","name":"bob-from-genesis"},` +
		`{"address":"g1ghi","name":"carol-from-genesis"}` +
		`],` +
		`"app_hash":"",` +
		`"app_state": @@@ this is deliberately not valid json @@@ }}}`)

	if err := r.SeedFromGenesisStream(stream); err != nil {
		t.Fatalf("SeedFromGenesisStream: %v", err)
	}

	if r.GenesisTime != "2026-03-16T09:00:00Z" {
		t.Errorf("GenesisTime = %q, want 2026-03-16T09:00:00Z", r.GenesisTime)
	}
	// Existing real name preserved (registry is gospel).
	if got := r.Name("g1abc"); got != "my-alice" {
		t.Errorf("Name(g1abc) = %q, want my-alice (must not overwrite)", got)
	}
	// unknown-val-N placeholder upgraded to the real genesis name.
	if got := r.Name("g1def"); got != "bob-from-genesis" {
		t.Errorf("Name(g1def) = %q, want bob-from-genesis (placeholder upgrade)", got)
	}
	// Brand-new validator filled in.
	if got := r.Name("g1ghi"); got != "carol-from-genesis" {
		t.Errorf("Name(g1ghi) = %q, want carol-from-genesis (gap fill)", got)
	}
}

func TestSeedFromValopers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	os.WriteFile(path, []byte(`{"g1real":"my-custom","g1placeholder":"unknown-val-1"}`), 0644)

	r := NewNameRegistryWithPersist(path)
	r.SeedFromValopers(map[string]string{
		"g1real":        "valoper-real", // real name: must NOT be overwritten
		"g1placeholder": "valoper-up",   // unknown-val-N: must be upgraded
		"g1new":         "valoper-new",  // unseen: gap fill
	})

	if got := r.Name("g1real"); got != "my-custom" {
		t.Errorf("Name(g1real) = %q, want my-custom (must not overwrite)", got)
	}
	if got := r.Name("g1placeholder"); got != "valoper-up" {
		t.Errorf("Name(g1placeholder) = %q, want valoper-up (upgrade unknown-val-N)", got)
	}
	if got := r.Name("g1new"); got != "valoper-new" {
		t.Errorf("Name(g1new) = %q, want valoper-new (gap fill)", got)
	}
	// Persisted to disk.
	r2 := NewNameRegistryWithPersist(path)
	if got := r2.Name("g1new"); got != "valoper-new" {
		t.Errorf("after reload Name(g1new) = %q, want valoper-new", got)
	}
}

func TestSetOursPreservesRealName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	os.WriteFile(path, []byte(`{"g1us":"my-custom-name"}`), 0644)

	r := NewNameRegistryWithPersist(path)
	// Gnoland self-reports a different moniker — file must win.
	r.SetOurs("g1us", "node1-from-gnoland")

	// NameWithUs verifies both that the custom name is preserved and that
	// ours-tracking still works (the "(us)" suffix).
	if got := r.NameWithUs("g1us"); got != "my-custom-name (us)" {
		t.Errorf("NameWithUs(g1us) = %q, want my-custom-name (us)", got)
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

func TestNameRegistryConcurrentSave(t *testing.T) {
	// Persist path makes Register() call save(); many goroutines registering at
	// once must not race the map read inside save(). Run under -race to verify.
	path := filepath.Join(t.TempDir(), "names.json")
	r := NewNameRegistryWithPersist(path)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r.Register(fmt.Sprintf("g1addr%02d", n), fmt.Sprintf("node-%02d", n))
		}(i)
	}
	wg.Wait()
	if got := r.Name("g1addr00"); got != "node-00" {
		t.Errorf("Name(g1addr00) = %q, want node-00", got)
	}
}

func TestBitArrayCount(t *testing.T) {
	tests := []struct {
		ba   bitArray
		want int
	}{
		{bitArray{Bits: "6", Elems: []string{"35"}}, 3},  // 0b100011
		{bitArray{Bits: "6", Elems: []string{"0"}}, 0},   // none
		{bitArray{Bits: "7", Elems: []string{"107"}}, 5}, // 0b1101011
		{bitArray{}, 0}, // empty
		{bitArray{Bits: "65", Elems: []string{"5", "1"}}, 3}, // bits 0,2 in word0 + bit 0 of word1 (index 64)
	}
	for _, tt := range tests {
		if got := tt.ba.count(); got != tt.want {
			t.Errorf("bitArray{%v}.count() = %d, want %d", tt.ba.Elems, got, tt.want)
		}
	}
}

func TestBitArrayBit(t *testing.T) {
	// Two 64-bit words: word0 = 5 (bits 0,2), word1 = 1 (bit 0 => validator index 64).
	// Exercises the >64-validator path that the old single-word decoder dropped.
	ba := bitArray{Bits: "66", Elems: []string{"5", "1"}}
	want := map[int]bool{0: true, 1: false, 2: true, 3: false, 63: false, 64: true, 65: false}
	for i, w := range want {
		if got := ba.bit(i); got != w {
			t.Errorf("bit(%d) = %v, want %v", i, got, w)
		}
	}
	// Out-of-range index must be false, not panic.
	if ba.bit(999) {
		t.Error("bit(999) = true, want false (out of range)")
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

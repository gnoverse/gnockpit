package node

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"
)

var unknownValPattern = regexp.MustCompile(`^unknown-val-(\d+)$`)

// isUnknownVal reports whether name is an "unknown-val-N" placeholder
// assigned when no real moniker could be discovered.
func isUnknownVal(name string) bool {
	return unknownValPattern.MatchString(name)
}

// NameRegistry dynamically maps validator addresses to monikers,
// discovered from /net_info peers and their /status responses.
// Persists to a JSON file so discoveries survive restarts.
type NameRegistry struct {
	mu           sync.RWMutex
	addrToName   map[string]string // validator address → moniker
	nameToAddr   map[string]string // moniker → validator address
	addrToPubKey map[string]string // validator address → base64 pubkey
	ourAddress   string
	ourMoniker   string
	persistPath  string
	lastModTime  time.Time
	GenesisTime  string // from genesis.json
}

// NewNameRegistry creates a registry, optionally loading from a persist file.
func NewNameRegistry() *NameRegistry {
	return &NameRegistry{
		addrToName:   make(map[string]string),
		nameToAddr:   make(map[string]string),
		addrToPubKey: make(map[string]string),
	}
}

// NewNameRegistryWithPersist creates a registry that auto-saves to a file.
func NewNameRegistryWithPersist(path string) *NameRegistry {
	r := &NameRegistry{
		addrToName:   make(map[string]string),
		nameToAddr:   make(map[string]string),
		addrToPubKey: make(map[string]string),
		persistPath:  path,
	}
	r.load()
	return r
}

func (r *NameRegistry) load() {
	if r.persistPath == "" {
		return
	}
	info, err := os.Stat(r.persistPath)
	if err != nil {
		return
	}
	r.lastModTime = info.ModTime()
	data, err := os.ReadFile(r.persistPath)
	if err != nil {
		return
	}
	var m map[string]string
	if json.Unmarshal(data, &m) == nil {
		for addr, name := range m {
			r.addrToName[addr] = name
			r.nameToAddr[name] = addr
		}
	}
}

// ReloadIfChanged reloads the name registry from disk if the file has been
// modified since the last load. This allows external edits to be picked up
// automatically without a full restart.
func (r *NameRegistry) ReloadIfChanged() {
	if r.persistPath == "" {
		return
	}
	info, err := os.Stat(r.persistPath)
	if err != nil || !info.ModTime().After(r.lastModTime) {
		return
	}
	r.Reload()
}

func (r *NameRegistry) save() {
	if r.persistPath == "" {
		return
	}
	data, err := json.MarshalIndent(r.addrToName, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(r.persistPath, data, 0644)
}

// SetOurs records our own validator address and moniker (from /status).
// Reset clears all cached name mappings (keeps ourAddress/ourMoniker).
func (r *NameRegistry) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	our := r.ourAddress
	ourM := r.ourMoniker
	r.addrToName = make(map[string]string)
	r.nameToAddr = make(map[string]string)
	r.addrToPubKey = make(map[string]string)
	if our != "" && ourM != "" {
		r.addrToName[our] = ourM
		r.nameToAddr[ourM] = our
	}
}

// Reload clears cached name mappings, then reloads from the persist file.
func (r *NameRegistry) Reload() {
	r.Reset()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.load()
}

// SetOurs records our own validator address and moniker. The address is
// always tracked so IsOurs / NameWithUs work. Registry writes follow the
// same rule as Register: a real existing entry is preserved (file is
// gospel); an unknown-val-N placeholder gets upgraded to the real moniker.
func (r *NameRegistry) SetOurs(address, moniker string) {
	r.mu.Lock()
	r.ourAddress = address
	r.ourMoniker = moniker
	if address == "" || moniker == "" {
		r.mu.Unlock()
		return
	}
	existing, existed := r.addrToName[address]
	if existed && !isUnknownVal(existing) {
		r.mu.Unlock()
		return
	}
	if existed && existing == moniker {
		r.mu.Unlock()
		return
	}
	if existed {
		delete(r.nameToAddr, existing)
	}
	r.addrToName[address] = moniker
	r.nameToAddr[moniker] = address
	r.mu.Unlock()
	r.save()
}

// Register maps a validator address to a moniker. gnockpit-names.json is
// the source of truth: a real existing entry is never overwritten. An
// "unknown-val-N" placeholder, however, is upgraded to the real moniker.
func (r *NameRegistry) Register(address, moniker string) {
	if address == "" || moniker == "" {
		return
	}
	r.mu.Lock()
	existing, existed := r.addrToName[address]
	if existed && !isUnknownVal(existing) {
		r.mu.Unlock()
		return
	}
	if existed && existing == moniker {
		r.mu.Unlock()
		return
	}
	if existed {
		delete(r.nameToAddr, existing)
	}
	r.addrToName[address] = moniker
	r.nameToAddr[moniker] = address
	r.mu.Unlock()
	r.save()
}

// EnsureName guarantees that address has an entry in the registry.
// A real existing entry is preserved. An "unknown-val-N" placeholder is
// upgraded if a non-empty candidate is supplied (otherwise kept as-is to
// avoid renumbering churn). If no entry exists, the first non-empty
// candidate is used; otherwise a fresh "unknown-val-N" is assigned.
func (r *NameRegistry) EnsureName(address string, candidates ...string) {
	if address == "" {
		return
	}
	r.mu.Lock()
	existing, existed := r.addrToName[address]
	if existed && !isUnknownVal(existing) {
		r.mu.Unlock()
		return
	}
	chosen := ""
	for _, c := range candidates {
		if c != "" {
			chosen = c
			break
		}
	}
	if chosen == "" {
		if existed {
			// Already a placeholder, no candidate — leave it alone.
			r.mu.Unlock()
			return
		}
		chosen = fmt.Sprintf("unknown-val-%d", r.nextUnknownValNLocked())
	}
	if existed && existing == chosen {
		r.mu.Unlock()
		return
	}
	if existed {
		delete(r.nameToAddr, existing)
	}
	r.addrToName[address] = chosen
	r.nameToAddr[chosen] = address
	r.mu.Unlock()
	r.save()
}

// nextUnknownValNLocked returns max(N) + 1 across all "unknown-val-N"
// names currently in the registry. Caller must hold r.mu.
func (r *NameRegistry) nextUnknownValNLocked() int {
	max := 0
	for _, name := range r.addrToName {
		m := unknownValPattern.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		if n, err := strconv.Atoi(m[1]); err == nil && n > max {
			max = n
		}
	}
	return max + 1
}

// RegisterPubKey stores a validator's base64 pubkey.
func (r *NameRegistry) RegisterPubKey(address, pubkey string) {
	if address == "" || pubkey == "" {
		return
	}
	r.mu.Lock()
	r.addrToPubKey[address] = pubkey
	r.mu.Unlock()
}

// PubKey returns the base64 pubkey for a validator address, or empty string.
func (r *NameRegistry) PubKey(addr string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.addrToPubKey[addr]
}

// Name returns the moniker for a validator address, or the address itself.
func (r *NameRegistry) Name(addr string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if name, ok := r.addrToName[addr]; ok {
		return name
	}
	return addr
}

// NameWithUs returns the moniker with "(us)" suffix if it's our address.
func (r *NameRegistry) NameWithUs(addr string) string {
	name := r.Name(addr)
	r.mu.RLock()
	isOurs := addr != "" && addr == r.ourAddress
	r.mu.RUnlock()
	if isOurs {
		return name + " (us)"
	}
	return name
}

// IsOurs returns true if the address is our validator.
func (r *NameRegistry) IsOurs(addr string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return addr != "" && addr == r.ourAddress
}

// AddrByMoniker returns the validator address for a moniker, if known.
func (r *NameRegistry) AddrByMoniker(moniker string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	addr, ok := r.nameToAddr[moniker]
	return addr, ok
}

// IsKnownMoniker returns true if we've seen this moniker as a validator.
func (r *NameRegistry) IsKnownMoniker(moniker string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.nameToAddr[moniker]
	return ok
}

// OurAddress returns our validator address.
func (r *NameRegistry) OurAddress() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.ourAddress
}

// SeedFromGenesis reads validator names from a genesis.json file.
// The genesis contains validators with "address" and "name" fields.
func (r *NameRegistry) SeedFromGenesis(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// Parse just the validators array — genesis is huge, only unmarshal what we need
	var genesis struct {
		GenesisTime string `json:"genesis_time"`
		Validators  []struct {
			Address string `json:"address"`
			Name    string `json:"name"`
		} `json:"validators"`
	}
	if err := json.Unmarshal(data, &genesis); err != nil {
		return err
	}
	r.mu.Lock()
	if genesis.GenesisTime != "" {
		r.GenesisTime = genesis.GenesisTime
	}
	for _, v := range genesis.Validators {
		if v.Address == "" || v.Name == "" {
			continue
		}
		existing, existed := r.addrToName[v.Address]
		if existed && !isUnknownVal(existing) {
			continue
		}
		if existed && existing == v.Name {
			continue
		}
		if existed {
			delete(r.nameToAddr, existing)
		}
		r.addrToName[v.Address] = v.Name
		r.nameToAddr[v.Name] = v.Address
	}
	r.mu.Unlock()
	r.save()
	return nil
}
